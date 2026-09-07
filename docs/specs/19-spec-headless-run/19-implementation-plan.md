# Implementation Plan: Headless `jig run`

**Status:** Plan (open goal A1 — P0)
**Depends on:** Engine Manager/Run APIs (`internal/engine`), transcript-as-truth,
TOML-only harness selection (Spec 14)
**Complements:** A2 thin ops CLI (`status` / `logs` / `doctor`); A3 mid-crash recovery
**Breaks:** Nothing yet — additive CLI surface. Pre-v1: prefer the correct long-term
contract over compatibility shims once flags ship.

---

## Problem

The execution engine already runs workflows without Bubble Tea. Engine design
explicitly kept the TUI as *one* event subscriber so `jig run --headless` stays
possible (`docs/engine-design.md` §4). Today that second consumer does not exist.

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

---

## Goal

Ship a first-class **headless runner**: a non-interactive CLI that loads a
workflow TOML, starts an engine run, drains events, applies an explicit **gate
policy**, writes human progress to stderr and machine data to stdout, and exits
with a stable status code.

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

---

## What “headless jig” means

Headless jig is **not** “Claude `-p` inside jig.” Agent steps already run their
backends non-interactively (SDK / ACP). Headless jig means:

> **The jig process itself** does not claim a terminal UI, does not prompt on
> stdin for operator decisions by default, and terminates when the run settles —
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
gates do not exist.

---

## Product support surface

### Must support (MVP)

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
   - Observe `RunFinished` (or exported `Wait`)
   - Map success / failure / cancelled / policy-blocked to exit codes
5. **Fail closed on unexpected human gates**
   - Review, `from=user` prompts, AskUserQuestion, `block_on` input, integration
     conflict, recovery (default), final merge without policy → non-zero exit
     with a clear stderr reason and run id
6. **Signal handling**
   - SIGINT / SIGTERM → `run.Cancel()`, wait for settle, exit non-zero
     (Claude Code documents SIGTERM → abort + exit 143; match that spirit)
7. **Output modes**
   - Default: human progress on stderr, summary line on stdout (or quiet)
   - `--output json`: single JSON document on stdout when finished
   - `--output jsonl` (or `stream-json`): NDJSON events as they happen
8. **Explicit merge / recovery policies** for CI-friendly workflows that still
   produce a run branch or hit recoverables

### Should support (near-term, same CLI family)

9. **`--root`** for `.jig` location (tests + multi-project hosts)
10. **`--quiet` / `-q`** — suppress progress; keep errors + final JSON
11. **`--timeout`** — hard wall clock; cancel run on expiry (CI always needs a
    bound — Claude headless guidance: “always set a timeout”)
12. **Print run id early** so operators can `tail` transcripts / attach TUI later
13. **Validate-before-run** (default on) — refuse to start if load fails
14. **Gate inventory at start** (optional warning): if workflow contains
    `type=review`, `@interactive`, `from=user`, or `block_on`, print which
    policies are required

### Later (A2 / follow-ons — design hooks only)

15. `jig status <run-id>`, `jig logs <run-id> [--step id]`, `jig resume <run-id>`
16. `--dry-run` with `FakeExecutor` script (engine tests already do this)
17. `--approve-review <verdict>` / answer files for controlled automation
18. Attach TUI monitor to a live headless run by run id (A2 / B8)

---

## How a user interacts with the running executable

### Mental model

```
┌─────────────┐     Start / Cancel / Resolve*     ┌──────────────────┐
│  jig run    │ ─────────────────────────────────▶│ engine.Manager   │
│  (headless) │◀──── Subscribe(live, ctrl) ───────│   └─ Run         │
└─────────────┘                                   └────────┬─────────┘
      │ stderr: progress                                    │
      │ stdout: data (--output)                             ▼
      │ exit: 0/1/2/…                              .jig/runs/<id>/
      ▼                                            journal + transcripts
   CI / shell / agent host
```

`*` Resolve only under an explicit gate policy — never silent stdin prompts in
the default CI profile.

### Typical invocations

**CI / unattended DAG (no human gates):**

```bash
jig run .agents/jig/ci-review.toml \
  --output json \
  --on-recovery abort \
  --discard-merge \
  --timeout 45m
echo $?   # 0 = RunFinished && !Failed
```

**Local script with human-readable progress:**

```bash
jig run examples/bugfix.toml --approve-merge
# stderr: step status lines
# stdout: run id + final summary
```

**Streaming into another process:**

```bash
jig run wf.toml --output jsonl | jq -c 'select(.type=="step_status")'
```

**Fail fast when the TOML is not automation-safe:**

```bash
jig run interactive.toml
# stderr: error: step "gate" requires human review; pass --fail-on-gate
#         is the default — refused to wait forever
# exit: 2 (usage/policy) or dedicated gate-blocked code
```

### Interaction rules (contract)

| Situation | Default headless behavior |
|---|---|
| Step succeeds / fails | Progress on stderr; continue per workflow |
| `RunFinished{Failed:false}` | Exit 0 |
| `RunFinished{Failed:true}` | Exit 1 |
| Unexpected gate (review / question / prompt / input) | Cancel or refuse; exit non-zero; **do not hang** |
| `FinalMergeRequest` | Require `--approve-merge` or `--discard-merge`; else fail closed |
| `RecoveryRequest` | Default `--on-recovery abort` (or `fail`) |
| `IntegrationConflictRequest` | Fail closed unless `--on-conflict abort\|agent` policy set |
| SIGINT / SIGTERM | `Cancel()`, drain to settle, exit 130/143-class |
| Invalid flags / missing file | Exit 2 before Start (match `validate` / `prune`) |
| Auth / harness spawn failure | Step fails → run fails → exit 1; detail on stderr / JSON error |

**Never** block on a stdin readline in the default profile. If a future
`--interactive-gates` mode is added, it must be opt-in and documented as
incompatible with CI.

### Dual-mode operator workflow

Headless and TUI share disk state:

1. `jig run wf.toml` starts run `20260906-…`
2. Operator can later open the TUI Runs screen and inspect the same journal /
   transcripts (read-only for a finished run; live attach is A2)
3. `jig prune` already manages finished runs

---

## Gate policy design (the hard part)

Human-blocking statuses kept alive by `anyPendingRunnable`:

| Status / event | Engine API | MVP policy knobs |
|---|---|---|
| `awaiting_review` / `ReviewRequest` | `Resolve` / `ResolveReview` | Fail closed (default). Optional later: `--auto-review <verdict>` |
| `PromptRequest` (`from=user`) | `ProvideUserInput` | Fail closed. Later: `--input as=text` / `--inputs-file` |
| `InputRequest` (`block_on`) | `SendInput` | Fail closed. Later: `--answer-file` |
| `AgentQuestion` | `AnswerQuestion` | Fail closed. Prefer `@autonomous` in CI TOMLs |
| `RecoveryRequest` | `Recover` | `--on-recovery abort` (default) \| `retry` \| `skip` |
| `IntegrationConflictRequest` | `ResolveIntegration` | `--on-conflict abort` (default) \| `agent` |
| `FinalMergeRequest` | `FinalMerge` | `--approve-merge` \| `--discard-merge` (required if gate fires) |

### Workflow author guidance

For CI-safe workflows:

- Prefer `profile = "@autonomous"` on agent steps (disallows `AskUserQuestion`)
- Avoid `type = "review"` and `from = "user"` inputs
- Avoid `block_on` unless paired with a future answer-file policy
- Expect final-merge when git persistence is on and the run branch has commits —
  pass merge policy flags or disable git integration for pure analysis workflows

`jig validate` stays structural; a future `jig doctor --ci` (A2) can lint for
headless-hostile constructs. MVP may emit a **start-time warning** listing gates
that will fail-closed if hit.

---

## Output & exit-code contract

Drawn from stable production sources (see Research appendix): Claude Code
headless docs, kubectl conventions, Terraform `-input=false` / `-json`, and
agent-first CLI output specs (clispec / cli-output-spec).

### Streams

| Stream | Contents |
|---|---|
| **stdout** | Machine data only when `--output json|jsonl`; otherwise a single final summary line (run id, status, cost). No ANSI when not a TTY |
| **stderr** | Progress (`step_id status`), warnings, errors, usage hints. Safe to ignore if you only care about exit code + JSON |

### `--output` modes

| Value | Behavior |
|---|---|
| `text` (default) | Human progress on stderr; short summary on stdout |
| `json` | Suppress text summary; emit one JSON object on stdout at end |
| `jsonl` | NDJSON: one envelope per ctrl event (and optional progress), flushed line-by-line |

Recommended final JSON shape (MVP draft):

```json
{
  "ok": true,
  "run_id": "20260906-221500-a1b2c3d4",
  "workflow": "bugfix",
  "failed": false,
  "total_cost_usd": 0.42,
  "total_tokens": 12000,
  "run_dir": ".jig/runs/20260906-221500-a1b2c3d4",
  "error": null
}
```

On policy/gate failure, same envelope with `"ok": false` and a typed `error`
(`code`, `message`, `step_id` when known). stdout stays JSON when `--output json`
was requested — never mix unstructured failure text into stdout in that mode
(cli-output-spec / clispec principle).

### Exit codes (proposed)

Keep close to existing `validate` / `prune` (0 / 1 / 2) and reserve room:

| Code | Meaning |
|---|---|
| 0 | Run finished successfully (`RunFinished` && !Failed) |
| 1 | Run finished failed, cancelled, or step/harness error |
| 2 | Usage / flag / load-validate error (never started) |
| 3 | Gate policy blocked (unexpected human gate; fail-closed) |
| 4 | Timeout |
| 130 / 143 | Interrupted (SIGINT / SIGTERM) when distinguishable |

Do not invent a large ACLI-style 0–9 matrix in MVP; document the small table and
keep codes stable once shipped (pre-v1 allows change until first documented
release notes freeze).

---

## CLI surface (author-facing)

```text
jig run <workflow.toml> [flags]

Flags:
  --root DIR              Persistence root (default: .jig)
  --output, -o text|json|jsonl
  --quiet, -q             Progress off (errors still on stderr)
  --timeout DURATION      Wall-clock cancel (e.g. 45m); 0 = none
  --approve-merge         Answer FinalMergeRequest with approve
  --discard-merge         Answer FinalMergeRequest with discard
  --on-recovery abort|retry|skip   (default: abort)
  --on-conflict abort|agent        (default: abort)
  --fail-on-gate          Default true; refuse to wait on human gates
  # deferred:
  # --auto-review VERDICT
  # --inputs-file PATH
  # --dry-run
  # --resume RUN_ID
```

Naming notes:

- Prefer `jig run` (verb) over `jig --headless` — matches `validate` / `prune`
  subcommand style already in `main.go`
- `--headless` is unnecessary if `run` is always non-interactive; reserve
  `--interactive-gates` only if we later add opt-in prompting
- Engine-design’s phrase `jig run --headless` is historical intent; the shipped
  UX should be the subcommand

---

## Architecture

### Package split

```
cmd/jig/
  main.go          # dispatch: validate | prune | run | (default TUI)
  run.go           # thin: flag parse → headless.Run → os.Exit

internal/headless/   # NEW — no Bubble Tea imports
  run.go             # Start, subscribe, policy, wait, signals
  policy.go          # GatePolicy from flags
  output.go          # text / json / jsonl writers
  run_test.go
```

Keep `internal/engine` free of CLI concerns. Headless is a second *client* of
`Manager`, parallel to `internal/tui`.

### Engine additions (small)

| Change | Why |
|---|---|
| Exported `func (r *Run) Wait() RunSnapshot` | CLI/tests should not only learn completion via Subscribe; wraps `<-r.done` + `finalSnap` |
| Optionally `WaitContext(ctx)` | Unify timeout + cancel |
| No change to event vocabulary | Reuse existing ctrl/live events |

Subscribe channels are never closed — headless must key off `RunFinished` for
*this* `run.ID`, then return (do not range forever).

### Skeleton

```go
func Run(ctx context.Context, opts Options) (Result, error) {
    wf, err := workflow.Load(opts.WorkflowPath)
    // wire mux identical to main TUI path
    mgr := engine.NewManager(mux, opts.Root)
    live, ctrl := mgr.Subscribe()
    go drainLive(live) // discard or jsonl-progress; never block ctrl

    run, err := mgr.Start(wf)
    // print run_id immediately to stderr (and jsonl)

    for {
        select {
        case <-ctx.Done():
            run.Cancel()
        case ev := <-ctrl:
            if err := policy.Handle(run, ev); err != nil {
                run.Cancel()
                return fail(err)
            }
            if fin, ok := ev.(engine.RunFinished); ok && fin.RunID == run.ID {
                return resultFrom(run.Snapshot(), fin), nil
            }
        }
    }
}
```

`policy.Handle` auto-answers only configured gates; on unexpected
`ReviewRequest` / `AgentQuestion` / … returns a typed gate error.

### Shared wiring with TUI

Extract mux / secret / monitor registration from `main.go` into a small
`internal/app` or `cmd/jig/wire.go` helper used by both TUI and `run` — avoid
drifting executor registration.

---

## Decision map

| # | Topic | Decision |
|---|---|---|
| D1 | Entry UX | Subcommand `jig run <file>`; not a global `--headless` |
| D2 | Default gate posture | Fail closed — never hang waiting for a human |
| D3 | Final merge | Explicit `--approve-merge` / `--discard-merge`; no default |
| D4 | Recovery | Default abort |
| D5 | stdout/stderr | Data on stdout (structured modes); progress/errors on stderr |
| D6 | Output flag | `--output` / `-o` with `text\|json\|jsonl` (kubectl / clispec) |
| D7 | Exit codes | 0 success, 1 run failed, 2 usage/validate, 3 gate policy, 4 timeout |
| D8 | Engine Wait | Add exported `Run.Wait` |
| D9 | Package | New `internal/headless` (or `internal/cli/run`); engine stays pure |
| D10 | Profiles | Document `@autonomous` for CI; do not auto-rewrite TOML |
| D11 | Dry-run | Deferred; FakeExecutor already exists |
| D12 | Resume | Deferred to A2/A3; print run id so disk inspection works |
| D13 | TUI coexistence | Same `.jig` root; no process-wide lock beyond existing scheduler.lock |
| D14 | Pre-v1 | Freeze exit/JSON shape in this spec once MVP merges; change only with doc bump |

---

## Phased implementation

### Phase 0 — Spec lock (this document)

- [x] Problem, goals, non-goals, research
- [ ] Confirm D1–D14 with maintainers (especially exit codes + merge flags)
- [ ] Add open questions resolution below before coding

### Phase 1 — MVP runner

1. Extract shared executor wiring from `cmd/jig/main.go`
2. Add `internal/headless` with fail-closed policy + text output
3. Add `jig run` subcommand
4. Export `Run.Wait` (or Wait via Finished observation helper)
5. SIGINT/SIGTERM → Cancel
6. Tests: fake mux workflow succeeds; review gate → exit 3; cancel path

### Phase 2 — CI contract

1. `--output json` + `jsonl`
2. `--timeout`, `--quiet`, `--root`
3. `--approve-merge` / `--discard-merge` / `--on-recovery` / `--on-conflict`
4. Example CI workflow TOML under `examples/` using `@autonomous`
5. Docs: `docs/workflow-schema.md` pointer + short `docs/headless.md` user guide
6. Update `open-goals.md` A1 → done when shipped

### Phase 3 — Operator amenity (optional in same PR series)

1. Start-time gate inventory warning
2. `--inputs-file` / `--auto-review` behind explicit flags
3. Hooks for A2 (`status` / `logs` reading same run dir)

### Phase 4 — Out of scope here

- Crash recovery mid-agent (A3)
- Thin ops CLI full set (A2)
- Notifications / webhooks (A24)

---

## Test plan

| Layer | Cases |
|---|---|
| `internal/headless` | Success DAG with FakeExecutor; unexpected ReviewRequest → gate error; FinalMerge with approve/discard; recovery abort; timeout cancel; JSON envelope shape |
| `internal/engine` | `Wait` returns final snapshot; cancelled run |
| `cmd/jig` | `jig run` flag parsing / exit codes via `TestMain` or subprocess helpers |
| Integration | `go run ./cmd/jig validate` examples still pass; headless run against a tiny fixtures TOML with command-only steps (no network) |

Persistence-off (`root=""`) must keep working for unit tests.

---

## Documentation updates (when implementing)

- `docs/plans/open-goals.md` — mark A1 in progress / done
- `docs/engine-design.md` — replace aspirational `jig run --headless` with real CLI
- `AGENTS.md` / `CLAUDE.md` — Commands section: add `jig run`
- New `docs/headless.md` — user-facing contract (flags, exit codes, CI recipe)
- Example: `examples/headless-smoke.toml` (command + check only)

---

## Risks

| Risk | Mitigation |
|---|---|
| Hang on AskUserQuestion / review | Fail-closed default; `@autonomous`; never readline |
| Final merge surprises CI | Require explicit merge flags; warn at start if git on |
| Subscribe channel never closes | Match on `RunFinished` for run id; return |
| stdout polluted by harness logs | Keep jig writers disciplined; document that child CLIs may still write stderr |
| Drift between TUI and run wiring | Shared wire helper |
| Operators expect silent auto-approve | Refuse; document; optional explicit flags only |

---

## Open questions (resolve before Phase 1 code)

1. **Should `jig run` refuse to start** if the workflow *statically* contains
   review / `@interactive` / `from=user`, or only fail when a gate *fires*?
   Recommendation: warn at start, fail when gate fires (allows mixed workflows
   with policies later).
2. **Default when FinalMerge would fire and no flag given:** exit 3 immediately
   on `FinalMergeRequest`, or preflight fail if git integration enabled?
   Recommendation: handle at event time (simpler; matches fail-closed).
3. **JSON field stability:** nest under `result` like Claude Code, or flat
   envelope as drafted above?
4. **Subcommand vs default:** keep bare `jig` → TUI forever, or eventually
   `jig tui` / `jig ui` with `run` as peer? Recommendation: keep bare → TUI for
   now (least surprise for current users).

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

**Apply to jig:** `--output`, timeout, signal cancel, JSON envelope, CI-safe
profiles (`@autonomous`), no interactive prompts by default.

### Terraform (HashiCorp) in CI

- `-input=false` on all commands so missing vars fail instead of prompting
- `-auto-approve` is **explicit** for apply — never implied
- `-json` / `terraform show -json` for machine-readable plans
- `-no-color` for log cleanliness

**Apply to jig:** fail instead of prompt; merge/recovery approvals are explicit
flags; structured output for pipelines.

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

**Apply to jig:** stream split; gate failures as typed errors in JSON mode.

### GitHub Actions / CI packaging patterns

- Capture exit code as the pass/fail signal
- Prefer writing artifacts (jig already has `.jig/runs/`) over scraping logs
- Job summaries from structured data, not ANSI TUI dumps

**Apply to jig:** print `run_dir` in the result; CI uploads `.jig/runs/<id>` on
failure for forensics.

---

## Mapping to existing code (implementation anchors)

| Concern | Path |
|---|---|
| CLI entry / subcommands | `cmd/jig/main.go` |
| Manager.Start / Subscribe | `internal/engine/engine.go` |
| Run resolve APIs | `internal/engine/engine.go` (`Resolve*`, `FinalMerge`, `Recover`, …) |
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

1. `jig run examples/…` (command-only fixture) exits 0 in CI without a TTY
2. A workflow that hits `ReviewRequest` exits non-zero quickly with a clear error
3. `--output json` yields a parseable envelope including `run_id` and `ok`
4. SIGINT cancels the run without leaving a wedged scheduler goroutine
5. `go test ./internal/headless ./cmd/jig/…` covers policy + exit mapping
6. Docs describe the user contract; open-goals A1 can be marked done
