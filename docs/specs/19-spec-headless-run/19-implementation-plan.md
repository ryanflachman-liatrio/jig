# Implementation Plan: Headless `jig run`

**Status:** Plan locked (open goal A1 — P0) — decisions resolved 2026-09-07
**Depends on:** Engine Manager/Run APIs (`internal/engine`), transcript-as-truth,
TOML-only harness selection (Spec 14)
**Complements:** A2 thin ops CLI (`status` / `logs` / `doctor`); A3 mid-crash recovery
**Breaks:** Nothing yet — additive CLI surface. Pre-v1: prefer the correct long-term
contract over compatibility shims once flags ship.

---

## Problem

The execution engine already runs workflows without Bubble Tea. Engine design
explicitly kept the TUI as *one* event subscriber so a headless consumer stays
possible (`docs/engine-design.md` §4; historical phrase `jig run --headless`).
Today that second consumer does not exist.

`cmd/jig` only exposes:

| Surface | Behavior |
|---|---|
| (no args) | Full Bubble Tea TUI |
| `jig validate <file>` | Load + static validate, exit |
| `jig prune …` | Retention housekeeping |

There is no way to start a workflow from CI, a Makefile, a script, or a remote
agent host and get a process exit code. Operators who want unattended runs must
drive the TUI or write Go against `engine.Manager` themselves.

Open-goals ranks this **#1** cross-cutting P0 (`docs/plans/open-goals.md` A1).

**Engine reality the CLI must respect:** with default persistence (`root=.jig`)
inside a git repo, a successful mutating run parks on `FinalMergeRequest` before
`RunFinished`, and the default `on_failure=abort` path parks hard failures on
`RecoveryRequest` — not on an immediate failed finish. Headless that only
fail-closes `type=review` will hang on the common success and failure paths.

---

## Goal

Ship a first-class **headless runner**: a non-interactive CLI that loads a
workflow TOML, starts an engine run, drains events, applies an explicit **gate
policy that settles every park path**, writes human progress to stderr and
machine data to stdout, and exits with a stable status code.

The TUI remains the interactive operator surface. Headless is the automation /
CI / scripting surface. Both share the same engine, runners, harnesses, journal,
and `.jig/runs/<id>/` layout.

---

## Non-goals (this spec)

- Replacing or forking the TUI run path
- Mid-execution crash recovery beyond today’s review-gate `Resume` (A3)
- Remote / distributed workers (A19)
- OTel / Prometheus export (A18)
- Full thin ops CLI (`status`, `logs`, `doctor`) — design the run contract so A2
  can attach later; do not implement A2 in the MVP
- Auto-answering arbitrary agent questions with an LLM
- Silent auto-approve of human review documents (dangerous; policy must be
  explicit and fail-closed by default)
- Env-based backend selection (`JIG_HARNESS`) — already deleted; do not reintroduce
- Expanding `@autonomous` to mean “headless-safe” (it only disallows
  `AskUserQuestion`; CI lint belongs in A2 `doctor --ci`)
- Headless `--on-conflict agent` (engine still requires a human finalize after
  agent prepare — not CI-complete)
- Opt-in stdin prompting in MVP (`--interactive-gates` is a later escape hatch)
- A first-class “disable git / skip final merge” TOML switch (use
  `--discard-merge`, `--ci`, persistence-off, or a non-git cwd)

---

## What “headless jig” means

Headless jig is **not** “Claude `-p` inside jig.” Agent steps already run their
backends non-interactively (SDK / ACP). Headless jig means:

> **The jig process itself** does not claim a terminal UI, does not prompt on
> stdin for operator decisions, and terminates when the run settles —
> suitable for CI, cron, and scripted orchestration.

Analogues in production tools:

| Tool | Interactive | Headless / scripted |
|---|---|---|
| Claude Code | REPL / TUI | `claude -p` / `--print` + `--output-format` |
| Terraform | prompts | `-input=false`, `-auto-approve`, `-json` |
| kubectl | human tables | `-o json` / `-o yaml` |
| GitHub CLI | prompts | `--json`, non-interactive flags |
| Docker Compose | TTY attach | `-d`, `--ansi never`, exit codes |

jig’s unique constraint: a workflow is a **DAG of steps with labeled human
gates**. Headless must define what happens when a gate appears — not pretend
gates do not exist. Fail-closed is the *definition* of `jig run`, not an
optional flag.

---

## Product support surface

### Must support (MVP / Phase 1)

1. **Start a workflow from a file path**
   - `jig run <workflow.toml>`
   - Same `workflow.Load` + validate path as `jig validate` (fail before tokens)
2. **Reuse production executors**
   - Same `runner.Mux` wiring as TUI: agent / command / check / review
   - Same `harness.For(backend, transport)` selection from TOML
3. **Persist under `.jig/`**
   - Journal, transcripts, `result.json`, artifacts — identical layout
   - Persistence-off remains first-class for tests (`root=""`)
4. **Settle the run and exit**
   - Observe `RunFinished` for this `run.ID` (and/or exported `Wait`)
   - **Never return from the event loop before settle** after `Cancel` or a
     policy action (ctrl channels are drop-on-full and never closed)
   - Map success / failure / gate-blocked / timeout / signal to exit codes
5. **Fail closed on every park path** (hard-coded defaults even before fancy flags)
   - Unexpected: review, `from=user` prompts, AskUserQuestion, `block_on` →
     typed gate error, `Cancel()`, wait settle, exit **3**
   - `RecoveryRequest` → `Recover(abort)` (native), wait settle, exit **1**
   - `IntegrationConflictRequest` → `ResolveIntegration(abort=true)` then
     recovery abort cascade (native), wait settle, exit **1**
   - `FinalMergeRequest` without merge flags → fail-closed exit **3** (do not
     hang); with flags → native `FinalMerge(approve|discard)`
6. **Signal handling**
   - SIGINT / SIGTERM → `run.Cancel()`, wait for settle, exit 130 / 143
7. **Output modes**
   - Default `text`: progress on stderr; short summary on stdout
   - `--output json`: one JSON object on stdout at end (incl. failures)
   - `--output jsonl`: NDJSON ctrl (and optional progress) lines; on gate/timeout
     failure still emit a final envelope line with `"ok": false` before exit
8. **Merge policy flags in Phase 1** — `--approve-merge` / `--discard-merge`
   (no silent default; required when `FinalMergeRequest` fires unless `--ci`)
9. **`--ci` sugar** — expands to a documented unattended profile (see CLI)
10. **Start-time gate inventory warning** (stderr) — list hostile constructs and
    remind about merge flags when cwd is a git repo with persistence on
11. **Print run id early** on stderr (even under `--quiet` / json — forensics)

### Should support (Phase 2 — same CLI family)

12. **`--root`** for `.jig` location (tests + multi-project hosts)
13. **`--quiet` / `-q`** — suppress progress; keep errors, early run id, and
    final structured/text summary as selected by `--output`
14. **`--timeout`** — hard wall clock; `0` = none (default without `--ci`);
    `--ci` requires a bound or supplies a documented default (45m)
15. **`--on-recovery abort|retry|skip`** — default abort already hard-coded in
    Phase 1; flag exposes override (`resume` stays engine/TUI-only)
16. **`--on-conflict abort` only** — documents the abort→recovery cascade;
    no `agent` value
17. **Example** `examples/headless-smoke.toml` (command + check only)
18. **Docs** `docs/headless.md` + pointers from schema / engine-design / AGENTS

### Later (A2 / follow-ons — design hooks only)

19. `jig status <run-id>`, `jig logs <run-id> [--step id]`, `jig resume <run-id>`
20. `jig doctor --ci` / `jig run --strict-ci` — refuse Start on hostile constructs
21. `--dry-run` with `FakeExecutor` script
22. `--auto-review` / `--inputs-file` for controlled automation
23. `--interactive-gates` opt-in stdin prompting (incompatible with CI)
24. Attach TUI monitor to a live headless run by run id (A2 / B8)
25. Headless `agent-then-accept` conflict finalize (if ever needed)

---

## How a user interacts with the running executable

### Mental model

```
┌─────────────┐     Start / Cancel / Resolve*     ┌──────────────────┐
│  jig run    │ ─────────────────────────────────▶│ engine.Manager   │
│  (headless) │◀──── Subscribe(live, ctrl) ───────│   └─ Run         │
└─────────────┘                                   └────────┬─────────┘
      │ stderr: progress + warnings + run id               │
      │ stdout: data (--output) / text summary             ▼
      │ exit: 0/1/2/3/4/130/143                   .jig/runs/<id>/
      ▼                                           journal + transcripts
   CI / shell / agent host
```

`*` Resolve via **native engine APIs** when a configured policy applies;
unexpected human gates use **Cancel + exit 3**. Never silent stdin prompts.

### Typical invocations

**CI / unattended (recommended):**

```bash
jig run examples/headless-smoke.toml --ci
# --ci expands to: --output json --discard-merge --on-recovery abort \
#                  --on-conflict abort --timeout 45m
echo $?   # 0 = RunFinished && !Failed
```

**Local script with human-readable progress (mutating, land on base):**

```bash
jig run .agents/jig/research.toml --approve-merge --timeout 30m
# stderr: gate inventory warning (if any), step status lines, run id
# stdout: final summary
```

**Streaming into another process:**

```bash
jig run examples/headless-smoke.toml --output jsonl --discard-merge --timeout 10m \
  | jq -c 'select(.type=="step_status")'
```

**Fail closed when a human gate fires:**

```bash
jig run .agents/jig/bugfix.toml --discard-merge --timeout 10m
# stderr: error: step "…" requires human review (fail-closed)
# exit: 3
```

### Interaction rules (contract)

| Situation | Default headless behavior |
|---|---|
| Step succeeds / fails | Progress on stderr; continue per workflow / failure policy |
| `RunFinished{Failed:false}` | Exit 0 (includes successful `--discard-merge`) |
| `RunFinished{Failed:true}` after step/harness/recovery-abort | Exit 1 |
| Unexpected gate (review / question / prompt / input) | Typed error → `Cancel()` → wait settle → exit **3** |
| `FinalMergeRequest` without merge flags / without `--ci` | Fail closed → exit **3** (event-time; do not hang) |
| `FinalMergeRequest` + `--approve-merge` / `--discard-merge` / `--ci` | Native `FinalMerge`; on approve conflict/`RunError`, do **not** stay parked — discard or fail-closed settle |
| `RecoveryRequest` | Native `Recover(abort)` (override via `--on-recovery` in Phase 2) → exit 1 if run failed |
| `IntegrationConflictRequest` | Native abort → then recovery abort cascade → exit 1 |
| SIGINT / SIGTERM | `Cancel()`, wait settle, exit 130 / 143 (only when cancel was signal-initiated) |
| Invalid flags / missing file | Exit **2** before Start |
| Load / validate error | Exit **1** before Start (match `jig validate`) |
| Auth / harness spawn failure | Step fails → recovery abort path → exit 1; detail on stderr / JSON `error` |
| `--timeout` expiry | `Cancel()`, wait settle, exit **4** |

**Never** block on a stdin readline. Future `--interactive-gates` must be opt-in
and documented as incompatible with CI.

### Dual-mode operator workflow

Headless and TUI share disk state:

1. `jig run wf.toml …` starts run `20260906-…` (id printed immediately)
2. Operator can later open the TUI Runs screen and inspect the same journal /
   transcripts (read-only for a finished run; live attach / answer is A2)
3. Fail-closed or cancel may leave inspectable run dirs and worktree debris;
   `jig prune` only removes runs that reached `RunFinished` — document that
   settled-failed runs are prune-eligible; wedged pre-fix hangs are a bug
4. Bare `jig` remains the TUI; help should mention `jig run` for CI

---

## Gate policy design (the hard part)

### What keeps a run alive

| Mechanism | Gates |
|---|---|
| `anyPendingRunnable` | `awaiting_review` (review **and** `from=user` prompts), `needs_input` (`block_on` **and** AskUserQuestion), `awaiting_recovery`, `awaiting_integration`, `stopped` |
| `awaitingFinalMerge` flag (NOT `anyPendingRunnable`) | `FinalMergeRequest` — scheduler parks on inbox after all steps are terminal |

`Snapshot().Done` can be **true while FinalMerge is pending**. Headless must key
off `RunFinished` / `Run.Wait` / `done`, never `Snapshot().Done` alone.

### MVP settle table

| Status / event | Engine API | Phase 1 default | Flags |
|---|---|---|---|
| `ReviewRequest` | `Resolve` / `ResolveReview` | Unexpected → Cancel + exit 3 | Later: `--auto-review` |
| `PromptRequest` (`from=user`) | `ProvideUserInput` | Unexpected → Cancel + exit 3 | Later: `--inputs-file` |
| `InputRequest` (`block_on`) | `SendInput` | Unexpected → Cancel + exit 3 | Later: `--inputs-file` |
| `AgentQuestion` | `AnswerQuestion` | Unexpected → Cancel + exit 3 | Prefer `@autonomous` on agents (necessary, not sufficient) |
| `RecoveryRequest` | `Recover` | Native `abort` → exit 1 | Phase 2: `--on-recovery abort\|retry\|skip` (`resume` omitted) |
| `IntegrationConflictRequest` | `ResolveIntegration` | Native `abort` → recovery abort cascade | Phase 2: `--on-conflict abort` only (no `agent`) |
| `FinalMergeRequest` | `FinalMerge` | No flags → exit 3; flags/`--ci` → native approve/discard | `--approve-merge` \| `--discard-merge`; `--ci` ⇒ discard |

### Fail-closed semantics (D15)

| Class | Action | Exit |
|---|---|---|
| Unexpected human gate (review / prompt / input / question / merge without policy) | Typed gate error, then `Cancel()`, **wait for `RunFinished`** | 3 |
| Configured recovery / conflict abort | Native `Recover` / `ResolveIntegration`, then wait | 1 if `Failed` |
| Configured merge discard/approve that settles cleanly | Native `FinalMerge`, then wait | 0 if `!Failed` |
| Approve-merge hits conflict / `RunError` and stays parked in TUI | Headless must **not** hang: apply discard if `--discard-merge`/`--ci`, else fail-closed Cancel + exit 3 | 3 (or 0 after successful discard) |

### Conflict cascade (honest)

`ResolveIntegration(stepID, abort=true)` does **not** finish the run — it fails
the step into `applyFailurePolicy` → usually another `RecoveryRequest`. Headless
must then `Recover(abort)` (or the configured recovery action). Do not claim
`--on-conflict abort` alone settles the run.

`--on-conflict agent` is **out of MVP**: `ResolveIntegrationWithAgent` re-emits
`IntegrationConflictRequest` for human finalize.

### Workflow author guidance

For CI-safe workflows:

- Use `--ci` (or the expanded flag set) on the command line
- Prefer `profile = "@autonomous"` on agent steps — **only** disallows
  `AskUserQuestion`, and only when `disallowed_tools` was left empty; it does
  **not** block review / `from=user` / `block_on` / recovery / merge
- Avoid `type = "review"`, `from = "user"`, and `block_on`
- Expect `FinalMergeRequest` whenever persistence is on, cwd is a git repo, and
  the run branch gained commits — pass `--discard-merge` / `--approve-merge` /
  `--ci`, or use persistence-off / non-git cwd for pure analysis tests
- Ship / use `examples/headless-smoke.toml` (command + check only) as the
  zero-gate fixture — do not point CI recipes at `.agents/jig/bugfix.toml`
  (has `type=review`)

`jig validate` stays structural. MVP emits a **start-time warning** listing
gates that will fail-closed if hit, and reminds about merge flags when git
persistence applies. A2 adds `jig doctor --ci` / optional `--strict-ci` refuse.

---

## Output & exit-code contract

Drawn from stable production sources (see Research appendix): Claude Code
headless docs, kubectl conventions, Terraform `-input=false` / `-json`, and
agent-first CLI output specs (clispec / cli-output-spec).

### Streams

| Stream | Contents |
|---|---|
| **stdout** | `--output json\|jsonl`: machine data only (incl. structured failures). `text`: single final summary line (run id, status, cost if known). No ANSI when not a TTY |
| **stderr** | Progress (`step_id status`), start-time warnings, early run id, errors, usage hints. Safe to ignore if you only care about exit code + JSON |

`--quiet`: suppress progress lines only; keep early run id, warnings/errors, and
the final stdout payload selected by `--output`.

### `--output` modes

| Value | Behavior |
|---|---|
| `text` (default) | Human progress on stderr; short summary on stdout at end |
| `json` | No text summary; one JSON object on stdout at end (success or failure) |
| `jsonl` | NDJSON line-by-line; always end with a final envelope (`ok` true/false) before process exit |

Frozen final JSON shape (flat envelope — D16):

```json
{
  "ok": true,
  "run_id": "20260906-221500-a1b2c3d4",
  "workflow": "headless-smoke",
  "failed": false,
  "total_cost_usd": 0.42,
  "total_tokens": 12000,
  "run_dir": ".jig/runs/20260906-221500-a1b2c3d4",
  "error": null
}
```

`total_cost_usd` / `total_tokens` are best-effort rollups from step results when
available; may be `0` / omitted-null until rollup is wired — `run_id`, `ok`,
`failed`, `run_dir`, and typed `error` are required.

On policy/gate failure, same envelope with `"ok": false` and
`error: { "code", "message", "step_id"? }`. Never mix unstructured failure text
into stdout in `json` / `jsonl` modes.

### Exit codes (frozen)

Align with existing `validate` / `prune` for 0 / 1 / 2, then extend:

| Code | Meaning |
|---|---|
| 0 | `RunFinished && !Failed` |
| 1 | Run finished failed (step/harness/auth, recovery-abort, configured policy that ends Failed); **also** load/validate error before Start (match `jig validate`) |
| 2 | Usage / flag parse / wrong arity (never started) |
| 3 | Unexpected human gate / merge-without-policy fail-closed |
| 4 | `--timeout` wall clock |
| 130 | SIGINT after Cancel + settle |
| 143 | SIGTERM after Cancel + settle |

Do not invent a large ACLI-style matrix. Change only with a doc bump after MVP
ships (`docs/headless.md`).

---

## CLI surface (author-facing)

```text
jig run <workflow.toml> [flags]

Flags:
  --root DIR                 Persistence root (default: .jig)
  --output, -o text|json|jsonl
  --quiet, -q                Progress off (keep run id, errors, final output)
  --timeout DURATION         Wall-clock cancel (e.g. 45m); 0 = none
  --ci                       Unattended preset (prints effective flags once on stderr)
  --approve-merge            Answer FinalMergeRequest with approve
  --discard-merge            Answer FinalMergeRequest with discard
  --on-recovery abort|retry|skip   (default: abort; resume omitted)
  --on-conflict abort        (default: abort; abort→recovery cascade; no agent)
  # deferred:
  # --strict-ci
  # --auto-review VERDICT
  # --inputs-file PATH
  # --interactive-gates
  # --dry-run
  # --resume RUN_ID
```

`--ci` expansion (print once on stderr):

```text
--output json --discard-merge --on-recovery abort --on-conflict abort --timeout 45m
```

Operators may still override individual flags after `--ci` if the flag parser
allows last-wins; document the precedence in `docs/headless.md`.

Naming notes:

- Prefer `jig run` (verb) over `jig --headless` — matches `validate` / `prune`
- **No `--fail-on-gate`** — fail-closed is inherent to `run`; later escape hatch
  is `--interactive-gates`, not a default-true boolean
- Engine-design’s phrase `jig run --headless` is historical; update docs when
  implementing
- Bare `jig` → TUI forever for now (D17); mention `jig run` in help text

---

## Architecture

### Package split

```
cmd/jig/
  main.go          # dispatch: validate | prune | run | (default TUI)
  run.go           # thin: flag parse → headless.Run → os.Exit
  wire.go          # shared mux / secrets / monitors / Manager construction

internal/headless/   # NEW — no Bubble Tea imports
  run.go             # Start, subscribe, policy, wait, signals
  policy.go          # GatePolicy from flags + hard-coded defaults
  output.go          # text / json / jsonl writers
  run_test.go
```

Keep `internal/engine` free of CLI concerns. Headless is a second *client* of
`Manager`, parallel to `internal/tui`.

### Engine additions (small)

| Change | Why |
|---|---|
| Exported `func (r *Run) Wait() RunSnapshot` | Wrap `<-r.done` + `finalSnap`; safe after Cancel |
| Optionally `WaitContext(ctx)` | Unify timeout + cancel |
| No change to event vocabulary | Reuse existing ctrl/live events |

Subscribe **before** `Start` (Start snapshots current subscribers). Channels are
never closed — key off `RunFinished` for this `run.ID`, then return. Always
drain `live` in a goroutine so the live buffer cannot stall the scheduler.
Filter ctrl events by `RunID` (Manager fans out all runs).

### Skeleton

```go
func Run(ctx context.Context, opts Options) (Result, error) {
    wf, err := workflow.Load(opts.WorkflowPath)
    // wire mux identical to TUI path (cmd/jig/wire.go)
    mgr := engine.NewManager(mux, opts.Root)
    live, ctrl := mgr.Subscribe()
    go drainLive(live) // discard or jsonl-progress; never block ctrl

    run, err := mgr.Start(wf)
    // print run_id immediately to stderr (and jsonl); emit start-time warnings

    var terminal error
    cancelled := false
    for {
        select {
        case <-ctx.Done():
            if !cancelled {
                cancelled = true
                run.Cancel()
            }
        case ev := <-ctrl:
            if evRunID(ev) != "" && evRunID(ev) != run.ID {
                continue
            }
            if err := policy.Handle(run, ev); err != nil {
                terminal = err // typed gate error → exit 3 mapping
                if !cancelled {
                    cancelled = true
                    run.Cancel()
                }
                // do NOT return yet — wait for RunFinished
            }
            if fin, ok := ev.(engine.RunFinished); ok && fin.RunID == run.ID {
                return resultFrom(run.Snapshot(), fin, terminal), nil
            }
        }
    }
}
```

`policy.Handle` uses native APIs for configured recovery / conflict / merge;
returns a typed gate error only for unexpected human gates (and merge-without
policy). Caller always waits for settle after Cancel.

### Shared wiring with TUI

Extract from `cmd/jig/main.go` into `wire.go` (or `internal/app`):

- `runner.NewMux` + Register command / check / agent / review(`FakeExecutor`)
- `harness.For` agent executor
- `SetIntegrationResolver`, `SetSecretResolver`, `SetMonitors`
- `engine.NewManager(mux, root)`

---

## Decision map

| # | Topic | Decision |
|---|---|---|
| D1 | Entry UX | Subcommand `jig run <file>`; not a global `--headless` |
| D2 | Default gate posture | Fail closed on **every** park path — never hang |
| D3 | Final merge | Event-time: flags/`--ci` → native; else exit 3. Approve-conflict must not stay parked |
| D4 | Recovery | Hard-coded native abort in Phase 1; Phase 2 flag `abort\|retry\|skip`; no `resume` |
| D5 | stdout/stderr | Data on stdout (structured modes); progress/errors/run id on stderr |
| D6 | Output flag | `--output` / `-o` with `text\|json\|jsonl` |
| D7 | Exit codes | 0 ok; 1 run-failed **or** load error; 2 usage; 3 gate; 4 timeout; 130/143 signals |
| D8 | Engine Wait | Add exported `Run.Wait`; always wait after Cancel |
| D9 | Package | New `internal/headless`; engine stays pure |
| D10 | Profiles | Document `@autonomous` accurately (AskUserQuestion only); do not auto-rewrite TOML |
| D11 | Dry-run | Deferred; FakeExecutor already exists |
| D12 | Resume | Deferred to A2/A3; print run id so disk inspection works |
| D13 | TUI coexistence | Same `.jig` root; finished runs inspectable; live answer is A2 |
| D14 | Pre-v1 freeze | Exit codes + flat JSON envelope freeze when MVP merges; change with doc bump |
| D15 | Fail-closed action | Unexpected → Cancel+exit 3; configured policies → native APIs then wait |
| D16 | JSON shape | Flat envelope (not nested under `result`) |
| D17 | Bare `jig` | Remains TUI; help mentions `jig run` |
| D18 | Static vs fire | Warn at start; fail when gate fires (default). `--strict-ci` / doctor later |
| D19 | Conflict | Abort→recovery cascade only; no headless `agent` |
| D20 | `--ci` | Sugar for json + discard-merge + recovery/conflict abort + timeout 45m |
| D21 | `--fail-on-gate` | **Not shipped** — inherent behavior |
| D22 | Phase 1 scope | Settle all park paths + merge flags + `--ci` + start-time warning; structured output may land Phase 1 or 2 but contract is frozen here |

---

## Phased implementation

### Phase 0 — Spec lock (this document)

- [x] Problem, goals, non-goals, research
- [x] Confirm D1–D22 (exit codes, merge, recovery, conflict, `--ci`, fail-closed)
- [x] Resolve open questions (see below)

### Phase 1 — MVP runner (no hang class left)

1. Extract shared executor wiring (`cmd/jig/wire.go`)
2. Add `internal/headless` with full settle table (review/prompt/input/question/
   recovery/conflict/merge) + text output
3. Add `jig run` with `--approve-merge` / `--discard-merge` / `--ci` / `--timeout`
4. Export `Run.Wait`; skeleton waits for `RunFinished` after every Cancel
5. SIGINT/SIGTERM → Cancel → settle → 130/143
6. Start-time gate inventory + git/merge warning; early run id on stderr
7. Tests: command-only success; each park path settles (no hang); merge without
   flags → exit 3; `--discard-merge` success → 0; recovery abort → 1;
   conflict abort cascade → 1; signal/timeout paths; Cancel drain

### Phase 2 — CI contract polish

1. `--output json` + `jsonl` (if not already in Phase 1)
2. `--quiet`, `--root`
3. Expose `--on-recovery` / `--on-conflict abort` as overrides (defaults already live)
4. Add `examples/headless-smoke.toml`
5. Docs: `docs/headless.md`, schema pointer, replace `jig run --headless` in
   engine-design, AGENTS/CLAUDE commands
6. Update `open-goals.md` A1 → done when shipped

### Phase 3 — Operator amenity

1. `--strict-ci` / hook for A2 `doctor --ci`
2. `--inputs-file` / `--auto-review`
3. Hooks for A2 (`status` / `logs` reading same run dir)

### Phase 4 — Out of scope here

- Crash recovery mid-agent (A3)
- Thin ops CLI full set (A2)
- Notifications / webhooks (A24)
- `--interactive-gates`, conflict `agent-then-accept`

---

## Test plan

| Layer | Cases |
|---|---|
| `internal/headless` | Success DAG (FakeExecutor); each unexpected gate → exit 3 + settled; recovery abort; conflict→recovery cascade; FinalMerge approve/discard; merge-without-flags → 3; approve-conflict does not hang; `--ci` expansion; timeout → 4; JSON/jsonl envelope; Cancel then Wait |
| `internal/engine` | `Wait` returns final snapshot; cancelled run; FinalMerge park ≠ `anyPendingRunnable` |
| `cmd/jig` | Flag parse; usage → 2; load error → 1; exit mapping via subprocess helpers |
| Integration | `examples/headless-smoke.toml` (or testdata) command-only, no network; validate still passes on kitchen-sink TOMLs |

Persistence-off (`root=""`) must keep working for unit tests (no FinalMerge).

---

## Documentation updates (when implementing)

- `docs/plans/open-goals.md` — mark A1 in progress / done
- `docs/engine-design.md` — replace aspirational `jig run --headless` with `jig run`
- `AGENTS.md` / `CLAUDE.md` — Commands: add `jig run` / `--ci`
- New `docs/headless.md` — user contract (flags, `--ci` expansion, exit codes,
  gate table, `@autonomous` accuracy, CI recipe)
- Example: `examples/headless-smoke.toml` (command + check only)
- Fix any docs that cite non-existent `examples/bugfix.toml` / `ci-review.toml`

---

## Risks

| Risk | Mitigation |
|---|---|
| Hang on AskUserQuestion / review / prompt / input | Cancel + exit 3; never readline; settle wait |
| Hang on recovery (default failure path) | Hard-coded `Recover(abort)` |
| Hang on FinalMerge (default success path in git repos) | Event-time exit 3 or `--discard-merge` / `--ci`; start-time warning |
| Approve-merge conflict stays parked | Headless discard or fail-closed — never TUI-style stay-parked |
| `--on-conflict abort` alone insufficient | Document + implement recovery cascade |
| `@autonomous` false sense of safety | Docs: AskUserQuestion only; gate inventory + doctor later |
| Subscribe channel never closes / drop-on-full | Match `RunFinished`; drain live; never return early after Cancel |
| stdout polluted by harness logs | jig writers disciplined; child CLIs may still write stderr |
| Drift between TUI and run wiring | Shared `wire.go` |
| Operators expect silent auto-approve | Refuse; `--ci` only auto-**discard** merge, never auto-approve review |
| Exit code confusion (auth 1 vs gate 3) | Document; typed JSON `error.code` |
| Unbounded CI jobs | `--ci` supplies `--timeout 45m`; examples always show timeout |

---

## Open questions — resolved

1. **Static refuse vs fail-when-fires?**  
   **Resolved (D18):** warn at start, fail when a gate fires. Later:
   `--strict-ci` / `jig doctor --ci` for refuse-at-Start.

2. **FinalMerge with no flags?**  
   **Resolved (D3):** event-time fail-closed (exit 3). No preflight refuse.
   `--ci` ⇒ `--discard-merge`. Approve-conflict must not remain parked.

3. **JSON flat vs nested?**  
   **Resolved (D16):** flat envelope as drafted; freeze in `docs/headless.md`.

4. **Bare `jig` → TUI vs `jig tui`?**  
   **Resolved (D17):** keep bare → TUI; mention `jig run` in help.

5. **Phase 1 gate coverage?**  
   **Resolved (D22):** settle **all** park paths in Phase 1 with hard-coded
   defaults; merge flags + `--ci` ship in Phase 1.

6. **Conflict `agent`?**  
   **Resolved (D19):** not in headless MVP; abort→recovery only.

7. **Exit codes vs `validate`?**  
   **Resolved (D7):** load error → 1 (match validate); usage → 2; gate → 3;
   timeout → 4; signals → 130/143.

8. **What does fail-closed do?**  
   **Resolved (D15):** unexpected → Cancel + exit 3 after settle; configured
   policies → native APIs then wait.

9. **Recovery `resume` in CLI?**  
   **Resolved (D4):** omit; engine/TUI keep it.

10. **`--fail-on-gate`?**  
    **Resolved (D21):** do not ship; inherent to `jig run`.

---

## Research appendix — production headless practices

Sources consulted (stable / production-facing):

### Claude Code official headless (`code.claude.com/docs` — Run programmatically)

- Non-interactive via `-p` / `--print`; exits 0 / non-zero for scripts
- `--output-format text|json|stream-json` for machine consumers
- `--bare` for hermetic CI (skip host hooks/MCP/CLAUDE.md) — jig analogue:
  TOML + env secrets only, no hidden env harness switch
- Always bound the job (timeout / max turns); nobody to press Escape
- SIGTERM aborts work and exits 143
- stdout carries the result; failures can still be structured on stdout when
  using JSON mode
- Restrict tools / permissions in CI; prefer deterministic allowlists

**Apply to jig:** `--output`, `--ci` timeout bound, signal cancel, flat JSON
envelope, accurate `@autonomous` docs, no interactive prompts.

### Terraform (HashiCorp) in CI

- `-input=false` on all commands so missing vars fail instead of prompting
- `-auto-approve` is **explicit** for apply — never implied
- `-json` / `terraform show -json` for machine-readable plans
- `-no-color` for log cleanliness

**Apply to jig:** fail instead of prompt; merge approve is explicit;
`--ci` may auto-**discard** (not approve) merge; structured output for pipelines.

### kubectl conventions (Kubernetes SIG-CLI)

- Default human output; `-o json|yaml` for programs
- Exit 0 success / non-zero errors; document semantic extras sparingly
- `--dry-run` for mutations (deferred for jig)

**Apply to jig:** `--output` naming; human default; JSON opt-in.

### CLI Spec / agent-first output conventions (clispec.dev, cli-output-spec)

- stdout = data API; stderr = progress/context
- Explicit format flag; JSON errors when JSON mode requested
- Non-interactive by default for automation
- Semantic exit codes; keep the matrix small and documented

**Apply to jig:** stream split; gate failures as typed errors in JSON mode;
final envelope line in jsonl on failure.

### GitHub Actions / CI packaging patterns

- Capture exit code as the pass/fail signal
- Prefer writing artifacts (jig already has `.jig/runs/`) over scraping logs
- Job summaries from structured data, not ANSI TUI dumps

**Apply to jig:** print `run_dir` + early `run_id`; CI uploads `.jig/runs/<id>`
on failure for forensics; prefer `jig run … --ci`.

---

## Mapping to existing code (implementation anchors)

| Concern | Path |
|---|---|
| CLI entry / subcommands | `cmd/jig/main.go` |
| Manager.Start / Subscribe | `internal/engine/engine.go` |
| Run resolve APIs | `internal/engine/engine.go` (`Resolve*`, `FinalMerge`, `Recover`, …) |
| `anyPendingRunnable` / FinalMerge park | `internal/engine/engine.go` (`requestFinalMergeIfNeeded`, `handleFinalMerge`) |
| Conflict → recovery | `handleResolveIntegration` (abort → `applyFailurePolicy`) |
| Events | `internal/engine/event.go` |
| Review dispatch | `internal/engine/review.go` |
| FakeExecutor | `internal/runner/fake.go` |
| Profiles `@autonomous` / `@interactive` | `internal/workflow/profiles.go` |
| Persistence layout | `internal/datastore/datastore.go` |
| Open goal A1 | `docs/plans/open-goals.md` |
| Design intent | `docs/engine-design.md` |

---

## Success criteria

Headless jig is done for A1 when:

1. `jig run examples/headless-smoke.toml --ci` exits 0 in CI without a TTY
2. Workflows that hit review / prompt / input / question exit **3** quickly and
   leave a settled run (no wedged scheduler)
3. Recovery and conflict-abort paths settle (exit 1) without hanging
4. FinalMerge without flags exits **3**; with `--discard-merge` / `--ci` exits 0
   on success; approve-conflict does not hang
5. `--output json` yields a parseable flat envelope including `run_id` and `ok`
6. SIGINT cancels and settles without a wedged scheduler goroutine
7. `go test ./internal/headless ./cmd/jig/…` covers the settle table + exit mapping
8. Docs describe the user contract (`docs/headless.md`); open-goals A1 can be
   marked done
