# Implementation Plan: Thin ops CLI (A2)

**Status:** Implemented — goal A2 (P0)
**Risk:** **high** — read-only inspection spans datastore, journal, transcript,
and CLI packages; historical resume/reset additionally crosses engine ownership,
headless gate policy, Git worktrees, and destructive operator actions.
**Depends on:** Spec 19 headless `jig run`; Specs 20/21 durable unfinished-run
reopen; Spec 08 reset-to-step; transcript-as-truth; `scheduler.lock` ownership.
**Complements:** A12 settled-run reset; T2/T3/T4 transcript copy/open/diff UX;
B8 in-monitor run switcher.
**Breaks:** Unknown first arguments stop falling through to the TUI and instead
return usage exit 2. The CLI JSON contracts introduced here are pre-v1 but must
be tested as stable once shipped.

---

## Summary

Add scriptable, non-TUI operations for inspecting run state, reading bounded
transcripts, checking runtime readiness, and explicitly reopening or resetting
unfinished automation-safe runs. The top risk is preserving scheduler ownership
and fail-closed gate behavior while a second process reads or takes control of a
persisted run.

## Approach

Build a read-only `internal/ops` layer first, backed by validated datastore paths,
timestamp-preserving raw journal replay, and bounded transcript readers; expose it
through `jig status`, `jig logs`, and `jig doctor` with text and machine-readable
outputs. Then extract the already-proven Spec 19 event/policy loop into a
supervisor that can own either a newly started or `Manager.Resume`d run, tighten
the engine reset API so it acknowledges success or failure, and add guarded
`jig resume` / `jig reset --apply` commands. Keep the TUI and CLI as thin clients:
state reconstruction stays in `engine`/`ops`, durable content stays in
`transcript`, scheduler mutation stays behind `Run`, and all backend selection
continues to come from the captured workflow TOML.

---

## Problem

`jig run` made workflow execution available to CI, but every operation after a
run starts still requires the TUI or manual inspection under `.jig/runs/`:

- there is no supported way to ask whether a run is active, parked,
  interrupted, failed, or complete;
- transcripts are durable but operators must locate and parse per-step JSONL;
- runtime prerequisites and CI-hostile gates are warnings at `jig run` time,
  not a reusable preflight command;
- `Manager.Resume` and `Run.Reset` exist, but have no non-TUI client;
- an unknown command such as `jig stats` silently opens the TUI.

The durable pieces already exist. `journal.jsonl` is the lifecycle truth,
`transcript.jsonl` is the content truth, `workflow.json` locks the executed graph,
and `scheduler.lock` prevents two processes from owning one run. A2 should expose
those pieces rather than create a second run database or scrape TUI rendering.

## Goals

1. Give humans and scripts a stable, non-interactive view of persisted runs.
2. Make step output readable and followable without loading Bubble Tea.
3. Detect missing runtime prerequisites and workflows that cannot settle under
   `jig run --ci` before starting an agent.
4. Reopen recoverable historical runs under the same policies and exit codes as
   headless execution.
5. Preview reset blast radius without writes, and apply reset only with explicit
   confirmation and an automation-safe continuation policy.
6. Preserve one source of truth for journal parsing, gate policy, backend
   selection, and run ownership.

## Non-goals

- Resetting a run after `RunFinished` (A12). `Manager.Resume` correctly rejects
  settled runs and A2 must preserve that boundary.
- Cross-process control of a scheduler that currently holds `scheduler.lock`.
  Read-only commands may inspect it; mutation commands fail with an ownership
  error rather than attaching to its in-memory inbox.
- Answering document reviews, `from=user`, `block_on`, or AskUserQuestion from
  CLI flags. Use the TUI for human gates; a future answer-file contract can be
  specified separately.
- Running auth or network probes. `doctor` checks deterministic local
  prerequisites and reports that login validity is unverified.
- Replacing `jig prune` or making `reset` mean deleting a run directory.
- `jig diff` in this delivery. See the explicit deferral below.
- A global config file or global flags redesign; each subcommand owns `--root`
  until a broader CLI framework is justified.

---

## Command contract

### Surface

| Command | Purpose | Default mutation |
|---|---|---|
| `jig status [RUN_ID]` | List persisted runs or inspect one in detail | none |
| `jig logs RUN_ID [--step ID]` | Render bounded transcript entries; optionally follow | none |
| `jig doctor [WORKFLOW.toml]` | Check local/runtime readiness; `--ci` enforces unattended safety | none |
| `jig resume RUN_ID --on-recovery ACTION` | Reopen an eligible unfinished run and supervise it | yes, explicit action required |
| `jig reset RUN_ID --to STEP` | Preview dependency-closure reset | none |
| `jig reset RUN_ID --to STEP --apply --ci` | Reopen, reset, and supervise an automation-safe run | yes, explicit `--apply` required |

All commands accept `--root PATH` with the same meaning as `jig run --root`.
Flags may appear before or after positional arguments, matching the existing
documented `jig run file.toml --ci` UX.

### Output and exits

| Command | Text output | Machine output | Exit behavior |
|---|---|---|---|
| `status` | table or detailed step list | `--output json` | 0 if inspected; 1 missing/corrupt/unreadable; 2 usage |
| `logs` | plain, non-ANSI blocks | `--output jsonl` | 0 at EOF/terminal; 1 read failure; 2 usage; 130/143 signal while following |
| `doctor` | one `pass`/`warn`/`fail` row per check | `--output json` | 0 with pass/warn only; 1 any fail; 2 usage |
| `resume` / applied `reset` | Spec 19 stderr progress + selected final output | `text\|json\|jsonl` | reuse 0/1/2/3/4/130/143 from headless |

Machine modes write data only to stdout. Diagnostics, truncation notices,
follow-state notices, and early run IDs go to stderr. No command emits ANSI when
stdout is redirected; the initial implementation may simply emit no ANSI at all.

### Run ID resolution

Every command validates `RUN_ID` as one path component before joining it beneath
`<root>/runs`. Absolute paths, separators, `.`/`..`, symlink escapes, and a
non-directory target fail before any read or write. The resolver returns an
error instead of calling `datastore.RunDir`, because inspection must never create
a missing run as a side effect.

---

## `jig status`

### Forms

```bash
jig status
jig status --output json
jig status 20260908-185703-m2u5u98s
jig status 20260908-185703-m2u5u98s --output json --root .jig
```

With no ID, print all persisted runs newest-first in a compact table containing
full run ID, workflow, derived state, completed/total steps, updated time, cost,
and tokens. With an ID, also print every step in workflow order with status,
attempt, iteration, generation, latest error/subtype, cost, and tokens, plus any
current wait reason.

### State derivation

Status folds **raw durable records**, not `ReplayJournal`'s display-only orphan
reconciliation. It preserves envelope timestamps and initializes every
`RunStarted.Steps` entry to `pending` before applying `StepStatus` records.

The run-level `state` is derived in this order:

1. `RunFinished{Failed:false}` → `succeeded`.
2. `RunFinished{Failed:true}` → `failed`.
3. missing/empty journal in an existing run dir → `orphaned` (unhealthy).
4. newline-complete journal corruption → `corrupt` (unhealthy; retain and print
   the decodable prefix for diagnosis).
5. no `RunFinished` and lock is held → `active`; add wait reasons from current
   parked statuses and an outstanding `FinalMergeRequest`.
6. no lock and a current `running`/`validating` step → `interrupted`.
7. no lock and another unfinished park/pending graph → `paused`.

`active` is ownership, not an assertion that a worker is consuming CPU. A live
review/recovery/final-merge park is `active` with `waiting_on` details. A stale
lock file alone never means active: liveness is determined by attempting the
same non-blocking advisory lock used by `Manager.Resume` and immediately
releasing it when acquired.

For each step, the most recent `StepStatus.Cost`/`Tokens` values are cumulative,
so run totals sum only the latest value per step. `updated_at` is the latest
well-formed journal envelope timestamp; start/finish times come from their
respective envelopes.

### JSON shape

The detailed object is the canonical shape; list mode returns an array of the
same objects with `steps` retained so scripts do not need two schemas:

```json
{
  "run_id": "20260908-185703-m2u5u98s",
  "workflow": "feature",
  "state": "paused",
  "lock_held": false,
  "reopenable": true,
  "started_at": "2026-09-08T18:57:03Z",
  "updated_at": "2026-09-08T19:03:11Z",
  "finished_at": null,
  "total_cost_usd": 0.42,
  "total_tokens": 12000,
  "waiting_on": [{"step_id":"review","kind":"review"}],
  "steps": [{
    "id": "plan",
    "status": "succeeded",
    "attempt": 0,
    "iteration": 0,
    "generation": 0,
    "error": "",
    "subtype": "",
    "cost_usd": 0.42,
    "tokens": 12000
  }]
}
```

---

## `jig logs`

### Forms

```bash
jig logs 20260908-185703-m2u5u98s
jig logs 20260908-185703-m2u5u98s --step implement --tail 50
jig logs 20260908-185703-m2u5u98s --follow --output jsonl
jig logs 20260908-185703-m2u5u98s --all --include-thinking
```

### Read and rendering rules

- Default to the newest 200 well-formed entries across all steps. `--tail N`
  changes that bound; `--all` is the explicit unbounded opt-in and is mutually
  exclusive with `--tail`.
- `--step ID` reads one transcript and rejects IDs not declared by
  `RunStarted.Steps`; without it, merge entries by parsed timestamp, then
  workflow step order, then per-file sequence. This is deterministic but is not
  presented as a causality guarantee for concurrent steps.
- Text mode prefixes each entry with timestamp, step ID, role, and
  generation/iteration/attempt when non-zero. Assistant/system/result text is
  printed verbatim. Tool activity gets a single lifecycle line followed by
  indented textual content; raw JSON is compact JSON, never markdown-rendered.
- Thinking blocks are omitted by default and represented by a one-line marker;
  `--include-thinking` prints persisted thinking verbatim. This is presentation
  policy only and does not alter transcript files.
- JSONL emits one stable wrapper per entry:
  `{"run_id":...,"step_id":...,"entry":{...}}`.
- A bounded read that omits older entries says how many were omitted on stderr.
- Malformed/torn trailing transcript records keep the existing reader tolerance;
  interior malformed records are skipped, matching `internal/transcript`.

### Follow behavior

`--follow` first prints the requested tail, then uses each file's opaque byte
cursor with `PageAfter` so memory remains proportional to the page size. It polls
with a modest fixed interval (250–500 ms), discovers transcripts for declared
steps that appear after follow starts, and exits after observing `RunFinished`
and draining one final pass. A paused/interrupted run has no terminal record, so
follow remains open until signal; stderr identifies that it is following an
inactive unfinished run. Follow never subscribes to an engine bus and therefore
works across processes.

Logs can contain model/tool output and may contain sensitive material. The
command must never add environment secret values of its own, but it does not
claim retrospective redaction before T6 defines a transcript-wide policy.

---

## `jig doctor`

### Forms

```bash
jig doctor
jig doctor .agents/jig/feature.toml
jig doctor .agents/jig/feature.toml --ci --output json
```

With no workflow, run core repository/store checks and report optional backend
prerequisites as warnings. With a workflow, load it through `workflow.Load` and
check only the resolved backends, transports, required check tools, secrets, and
filesystem/Git capabilities that graph needs.

Each check has a stable ID, `pass|warn|fail`, scope, message, and remediation.
The first implementation includes:

| Check ID | Rule |
|---|---|
| `workflow.load` | TOML loads, defaults, and validation succeed |
| `store.access` | root exists or its nearest parent is readable/writable; probe file is removed |
| `store.runs` | existing run dirs have safe names and readable journals; corrupt/orphan dirs warn |
| `git.binary` | `git` is on PATH when a workflow needs worktree isolation |
| `git.repository` | cwd is a real work tree with `HEAD` when worktree isolation is requested; fail instead of accepting engine's non-git degrade |
| `backend.claude.sdk` | `claude` is on PATH |
| `backend.claude.acp` | `npx` and `claude` are on PATH |
| `backend.cursor.acp` | `cursor-agent` is on PATH |
| `backend.codex.acp` | `npx` and `codex` are on PATH |
| `check.required_tool` | every unique `[step.findings].required_tools` executable is on PATH |
| `secret.environment` | every named secret has its normalized `JIG_SECRET_<NAME>` variable; report key names only |
| `ci.human_gate` | under `--ci`, reject review steps, `from=user`, `block_on`, and `@interactive`/AskUserQuestion capability |
| `ci.final_merge` | worktree mutation under `--ci` passes with a note that `jig run --ci` discards the final merge by policy |

`doctor --ci` requires a workflow path. It promotes every headless-hostile
construct currently emitted by `internal/headless.inventoryGates` from warning
to failure, so the inventory logic must move to an exported, UI-neutral helper
used by both doctor and `jig run`; it must not be copied.

Doctor does not invoke an agent, download an ACP adapter, or validate a login.
For `npx` adapters, it reports that the pinned package may still require network
or a populated cache. Login checks remain warnings with the exact manual command
(`claude`, `cursor-agent login`, or `codex login`) rather than side-effecting
probes.

---

## Historical control: `resume` and `reset`

These commands are a second delivery slice. Read-only commands must land first
so every mutation can tell an operator what it is about to own and change.

### Eligibility common to both commands

Before `Manager.Resume` (which appends a rehydration batch), perform a strictly
read-only preflight:

1. resolve and validate the run directory;
2. require an unfinished, unlocked, non-orphan, non-corrupt journal;
3. load the immutable `workflow.json` snapshot through an exported engine
   reader; never reload the author's current TOML;
4. run the same `doctor --ci` gate inventory for applied reset, and classify
   current parks for resume;
5. subscribe to manager channels **before** calling `Manager.Resume`, because
   restored gate requests are emitted during resume setup;
6. let `Manager.Resume` acquire `scheduler.lock`; a race lost here is a clean
   ownership error, not a retry around the lock.

### `jig resume`

`resume` is for unattended recovery, not a text-mode replacement for every TUI
gate. It supports histories whose current parks are only interrupted workers,
`awaiting_recovery`, or `awaiting_integration`. Review, prompt, input/question,
or stopped parks cause exit 3 **before** `Manager.Resume` writes anything, with a
message to open the TUI.

`--on-recovery abort|retry|resume|skip` is required; unlike `jig run`, an omitted
action must never silently abort a history the operator explicitly asked to
recover. `resume` is accepted only when the restored `RecoveryRequest.CanResume`
is true; otherwise it fails closed before sending an invalid engine action.
Conflict handling remains `abort` only and follows the existing
conflict→recovery cascade. Merge, output, timeout, quiet, and signal flags match
`jig run` exactly.

After `Manager.Resume`, a shared headless supervisor drains live events, applies
the selected policy to ctrl events, waits for `RunFinished`, and produces the
same final envelope and exit mapping as Spec 19. Refactor the supervisor; do not
copy its settle loop.

### `jig reset`

`reset` always means Spec 08 reset-to-step. It never deletes a run and never
reopens a settled run.

Preview is the default:

```bash
jig reset RUN_ID --to plan
# would reset: plan, implement, qa
# no changes made; add --apply --ci to continue
```

The preview loads `workflow.json`, calls a pure exported dependency-closure
helper, shows all invalidated steps and current statuses, reports whether any
park lies outside the closure, and performs no lock acquisition, journal append,
worktree creation, or file deletion.

Applied reset requires both `--apply` and `--ci`. It refuses before mutation if:

- the run is settled, locked, orphaned/corrupt, or has no recoverable run branch;
- the target is unknown;
- any unfinished/parked step lies outside the target closure;
- the captured workflow fails the CI gate inventory;
- `--approve-merge` and `--discard-merge` conflict.

The command then subscribes, calls `Manager.Resume`, and sends an acknowledged
`Run.Reset`. The engine reset command must return a typed result containing the
target, closure, and rewind SHA, or an error explaining the guard/Git failure;
the CLI must never print “reset” after today's silent no-op. A successful reset
also clears `restoredHold` so pending closure steps dispatch. The shared headless
supervisor then owns the run until it settles; `--ci` supplies JSON output,
discard-merge, abort-conflict, and timeout defaults just as `jig run --ci` does.

If reset itself produces an integration conflict, the configured conflict and
recovery policies settle it. Any unexpected human gate after dispatch remains a
Spec 19 fail-closed error: cancel, wait for `RunFinished`, exit 3. The preflight
makes this an invariant violation rather than the expected path.

### Why no live attach

An active scheduler owns mutable state in its process and accepts commands only
through an in-memory `Run.inbox`. The disk journal is not a command queue.
Inventing a second-process control socket, authentication, lifecycle, and replay
protocol would make A2 a remote-control feature rather than a thin CLI. Mutation
commands therefore fail when the advisory lock is held; status/logs remain safe.

---

## `jig diff` deferral

A2 is complete without `diff`. Today jig durably stores:

- `diff_sha256` in terminal `result.json` (integrity, not content);
- an integration commit SHA per terminal mutating step;
- diff text only when a review document happened to snapshot it;
- a run branch that is normally retained, but no explicit durable run base SHA.

A reliable `jig diff` must not guess a base from current checkout state or rely
on a Git ref surviving manual cleanup. Specify it later with immutable per-step
patch snapshots or a durable run provenance record containing repo identity,
base SHA, run branch/head, and merge/discard decision. That follow-on can expose
`jig diff RUN_ID [--step ID] [--stat]` and reuse `internal/tui/diffview` only for
presentation parsing, never as the source of the patch.

---

## Architecture and ownership

```text
cmd/jig
  flags + streams + exits
       |
       +--> internal/ops ----------> datastore (safe run paths)
       |       |                    engine (raw journal records, lock state)
       |       +------------------> transcript (bounded pages/cursors)
       |       +------------------> workflow/harness prerequisite metadata
       |
       +--> internal/headless -----> shared supervisor + gate policy
                    |
                    +-------------> engine.Manager.Resume / Run.Reset

.jig/runs/<id>/
  journal.jsonl       lifecycle truth
  workflow.json       captured graph used by doctor/resume/reset
  steps/*/transcript  content truth used by logs
  scheduler.lock      single mutable owner
```

Key boundaries:

- `internal/datastore` validates and resolves persisted paths; it does not parse
  engine records.
- `internal/engine` remains the sole decoder of journal events and sole mutator
  of scheduler/Git state.
- `internal/ops` contains immutable report types, folds, log merging, doctor
  checks, and mutation preflight; it does not import TUI packages.
- `internal/headless` owns generic supervision, output envelopes, and gate
  policy for both start and resume/reset flows.
- `cmd/jig` parses arguments and maps typed results to streams/exit codes.

No workflow schema fields are added. Persistence-off remains valid for engine
and runner tests; persisted-run commands return a clear “persistence required”
usage/operational error when `--root` is empty rather than creating writers.

---

## Delivery phases

### Phase 1 — durable read model and safe paths

1. Add non-creating, traversal-safe run resolution in `internal/datastore`.
2. Add timestamp-preserving raw journal records and a non-blocking lock-state
   probe in `internal/engine` using the exact resume lock semantics.
3. Add `internal/ops` report types and the status fold.
4. Add status list/detail text and JSON CLI surfaces.

**Exit:** status works on live append, finished, parked, interrupted, torn-tail,
orphan, corrupt, unknown-ID, and lock-race fixtures without writing under root.

### Phase 2 — bounded logs

1. Add deterministic multi-step merge and text/JSONL projection.
2. Add bounded tail/all reads and opaque-cursor follow.
3. Wire signals and terminal drain behavior in `cmd/jig`.

**Exit:** a concurrently appended transcript can be followed without duplicates,
unbounded memory, markdown interpretation, or stdout contamination.

### Phase 3 — doctor

1. Extract the headless gate inventory into an exported neutral result type.
2. Implement injected filesystem/PATH/Git/env checks with stable IDs.
3. Wire text/JSON output and CI failure promotion.

**Exit:** every supported backend/transport combination, missing tool/secret,
worktree prerequisite, and hostile gate has a deterministic test and remediation.

### Phase 4 — shared resume supervision

1. Export captured-workflow reading and immutable run preflight helpers.
2. Split `headless.Run` into start orchestration plus a reusable supervisor.
3. Add guarded `jig resume` with required recovery action and existing exit codes.

**Exit:** eligible interrupted/recovery histories settle without a TUI; unsupported
human parks and held locks fail before durable writes.

### Phase 5 — acknowledged reset

1. Extract pure reset closure calculation and make engine reset synchronous and
   typed; release `restoredHold` only after a successful reset mutation.
2. Implement read-only preview and strict apply preflight.
3. Supervise the reset rerun through terminal/fail-closed settlement.

**Exit:** preview is byte-for-byte non-mutating; apply records `StepsReset`,
rewinds/replays the correct commits, dispatches the closure, and never reports a
silent no-op as success.

### Phase 6 — docs and release gate

1. Publish `docs/operations.md` with examples, JSON contracts, exits, security,
   and TUI handoff.
2. Update headless/engine/architecture/open-goals references.
3. Run full build, vet, tests, race-focused package tests, and validate the
   kitchen-sink workflow.

---

## Ordered implementation tasks

Estimates are focused-agent wall time and intentionally exclude review latency.
Every substantive change has a separate test task.

| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `ResolveRunDir(root, runID)` and safe persisted-run name validation without directory creation | `internal/datastore/datastore.go` | 20 min |
| 2 | Test missing roots, safe IDs, separators, dot segments, symlink/file targets, and persistence-off behavior | `internal/datastore/datastore_test.go` | 20 min |
| 3 | Add `JournalRecord{Seq, Timestamp, Event}` raw replay while retaining torn-tail/corrupt-prefix semantics | `internal/engine/replay.go` | 25 min |
| 4 | Test record timestamps, ordering, torn tails, empty/orphan journals, and newline-complete corruption | `internal/engine/replay_test.go` | 25 min |
| 5 | Add advisory `RunLockState(runDir)` using the same non-blocking flock contract as resume | `internal/engine/resume.go` | 20 min |
| 6 | Test held, free, stale-file, missing-dir, and lock acquisition race behavior | `internal/engine/resume_test.go` | 25 min |
| 7 | Define stable run/step/wait report types and fold raw records into status/list detail | `internal/ops/status.go` | 30 min |
| 8 | Test active parks, interrupted, paused, final merge, settled success/failure, retry/reset cost totals, corrupt/orphan state | `internal/ops/status_test.go` | 30 min |
| 9 | Add bounded transcript collection, timestamp merge, text projection, and JSONL wrapper | `internal/ops/logs.go` | 30 min |
| 10 | Test multi-step ordering, timestamp ties, tool/text/thinking rendering, tail/all bounds, malformed records, and unknown steps | `internal/ops/logs_test.go` | 30 min |
| 11 | Add cursor-based follow with injected ticker/context and terminal journal drain | `internal/ops/follow.go` | 30 min |
| 12 | Test concurrent append, late-created transcripts, no duplicates, terminal exit, paused follow, and cancellation | `internal/ops/follow_test.go` | 30 min |
| 13 | Extract headless hostile-gate inventory into an exported neutral API used by warnings and doctor | `internal/headless/warn.go` | 20 min |
| 14 | Test the shared inventory retains every Spec 19 warning and deterministic ordering | `internal/headless/policy_test.go` | 20 min |
| 15 | Implement injected doctor checks and stable `Check`/`Report` types | `internal/ops/doctor.go` | 30 min |
| 16 | Test every backend/transport tool mapping, required tools, secret key normalization, Git/root checks, CI gates, and no secret values in output | `internal/ops/doctor_test.go` | 30 min |
| 17 | Add status/logs/doctor parsers, renderers, stream discipline, help, and explicit unknown-command errors | `cmd/jig/ops.go` | 30 min |
| 18 | Register ops commands and `help` without changing bare `jig` TUI startup | `cmd/jig/main.go` | 15 min |
| 19 | Test positional/flag reorder, usage, text/JSON/JSONL stdout, stderr notices, exits, and unknown subcommands | `cmd/jig/ops_test.go` | 30 min |
| 20 | Export immutable captured-workflow loading for validated historical run IDs | `internal/engine/resume.go` | 20 min |
| 21 | Test capture loading uses `workflow.json`, rejects settled/corrupt/missing inputs appropriately, and never consults current TOML | `internal/engine/resume_test.go` | 25 min |
| 22 | Extract `Supervise` from `headless.Run` and add explicit `RecoveryResume` handling so started and resumed runs share drain/policy/wait/output logic | `internal/headless` | 30 min |
| 23 | Test resumed event ordering, pre-emitted restored gates, timeout/signal settlement, and identical exit/envelope behavior | `internal/headless/run_test.go` | 30 min |
| 24 | Implement read-only eligibility checks and historical resume orchestration with explicit recovery policy | `internal/ops/control.go` | 30 min |
| 25 | Test eligible/mixed/unsupported parks, held-lock race, `CanResume`, no-write refusal, and recovery/conflict settlement | `internal/ops/control_test.go` | 30 min |
| 26 | Extract pure `ResetClosure`, add acknowledged `ResetResult`, return typed guard/Git errors, and clear restored hold on success | `internal/engine/engine.go` | 30 min |
| 27 | Test unknown targets, closure order, every guard, restored reset dispatch, rewind failure, survivor conflict, acknowledgement, and persistence-off | `internal/engine/reset_test.go` | 30 min |
| 28 | Add reset preview/apply preflight and invoke the shared supervisor after acknowledgement | `internal/ops/reset.go` | 30 min |
| 29 | Test byte-for-byte no-write preview, outside-closure park refusal, CI-gate refusal, successful apply, and no false success on engine errors | `internal/ops/reset_test.go` | 30 min |
| 30 | Add resume/reset CLI flags by reusing run output/policy parsers and CI defaults | `cmd/jig/control.go` | 30 min |
| 31 | Test required action/apply flags, settled/live refusal, mutation confirmation, output contracts, and exit mapping | `cmd/jig/control_test.go` | 30 min |
| 32 | Document operator workflows, safety boundaries, JSON fields, exits, and examples | `docs/operations.md` | 30 min |
| 33 | Cross-link the new contract and remove stale “future A2” language | `docs/headless.md` | 15 min |
| 34 | Document raw inspection, lock ownership, and resumed supervision boundaries | `docs/engine-design.md` | 15 min |
| 35 | Update package ownership for `internal/ops` and CLI consumers | `docs/ARCHITECTURE.md` | 15 min |
| 36 | Mark A2 done only after all exit criteria pass; retain `diff` as an explicit follow-on | `docs/plans/open-goals.md` | 10 min |

Estimated focused implementation time: **15–16 hours**, best delivered as the
six phases above rather than one review unit.

---

## Test matrix

### Read-only consistency

| Scenario | Status | Logs | Expected invariant |
|---|---|---|---|
| scheduler appending | active, latest complete record | complete transcript lines only | no writes; torn tail tolerated |
| live review/recovery park | active + wait reason | tail/follow available | held lock is ownership truth |
| dead mid-worker | interrupted | partial transcript available | raw journal is not display-reconciled |
| dead at durable gate | paused + wait reason | existing transcript available | reopenable true |
| `RunFinished` success/fail | succeeded/failed | exits at EOF | query exit remains 0 |
| missing/empty journal | orphaned + exit 1 | error/empty diagnosis | never synthesize a healthy run |
| interior journal corruption | corrupt + exit 1 | transcript still addressable by explicit step | print prefix diagnosis, do not skip corruption |
| partial transcript line | unchanged | line skipped | later complete lines remain readable |

### Doctor fixtures

- one valid fixture for each supported backend/transport pair;
- missing `claude`, `npx`, `cursor-agent`, `codex`, `git`, and required check tool;
- named secret present/missing, asserting values never appear;
- git repo/non-repo/unborn or missing `HEAD` with worktree isolation;
- review, user input, block-on, interactive profile, and zero-gate CI workflow;
- unwritable root and removable write probe;
- deterministic check order and identical text/JSON verdicts.

### Mutation and ownership

- process-equivalent interrupted and already-parked recovery histories;
- unsupported review/input/question/stopped histories rejected before journal
  modification;
- held lock rejected, including lock won between preflight and resume;
- `--on-recovery resume` allowed only with durable session plus advertised
  `CapSessionResume`;
- reset preview hashes every file under the run dir before/after and compares
  journal length plus Git refs;
- reset apply on linear and fan-out histories, including survivor replay;
- reset target unknown, settled run, no Git branch, outside-closure park, and
  reset Git failure all produce typed non-success;
- signals and timeouts settle the owned scheduler before returning.

### Release verification

```bash
gofmt -l -w .
go test ./internal/datastore ./internal/engine ./internal/transcript ./internal/ops ./internal/headless ./cmd/jig
go test -race ./internal/engine ./internal/ops ./internal/headless
go test ./...
go vet ./...
go build ./cmd/jig
go run ./cmd/jig validate .agents/jig/feature.toml
```

The implementation must also run a shell-level smoke fixture proving JSON and
JSONL stdout parse cleanly while notices remain on stderr.

---

## Security and failure handling

- Treat run IDs and step IDs as identifiers, never paths. Resolve beneath the
  configured root and reject traversal/symlink escapes.
- Inspection never acquires a long-lived scheduler lock or creates a run dir.
- Mutation acquires the existing advisory lock once and never works around a
  competing owner.
- Reset preview is the default and is strictly read-only; apply requires two
  explicit signals (`--apply --ci`) and prints the exact closure before mutation.
- No doctor result contains a secret value, command environment, transcript
  excerpt, or auth token. Only missing/present environment key names are shown.
- No log content is markdown-interpreted or executed. JSON text is encoded, not
  interpolated into shell commands.
- Follow is context-cancellable and bounded per poll; a growing file cannot
  force a full reread.
- Resume/reset subscribe before scheduler creation so no critical restored gate
  event is missed.
- Any owned-run cancel path waits for `RunFinished`/`Run.Wait`; ctrl channels are
  never assumed to close.
- Persistence-off remains a no-op in writers and engine tests; ops commands that
  require history fail clearly without manufacturing persistence.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Read races with append | Reuse engine torn-tail semantics and transcript opaque cursors; never parse files independently in `cmd/jig` |
| State label lies about liveness | Probe advisory lock, then distinguish active ownership from worker status/wait reason |
| Read-only preflight races mutation ownership | Treat preflight as advisory; `Manager.Resume` lock acquisition is authoritative |
| Restored gate events arrive before supervisor | Subscribe before `Manager.Resume`; test pre-emitted ctrl queue |
| Reset silently no-ops or remains held | Synchronous typed acknowledgement plus explicit `restoredHold=false` on success |
| CLI resume aborts unexpectedly | Require `--on-recovery`; reject unsupported human parks before writes |
| Logs exhaust memory | Default global tail bound; cursor paging; explicit `--all` for unbounded history |
| Doctor drifts from run warnings | One shared hostile-gate inventory and one backend prerequisite table |
| JSON contract drifts across commands | Named report structs and golden decode tests; no `map[string]any` output assembly |
| Diff reconstructed incorrectly | Defer until content and base provenance are durably persisted |

---

## Completion criteria

A2 is done when:

1. `jig status` distinguishes active, waiting, interrupted, paused, failed,
   succeeded, orphaned, and corrupt histories from durable state and lock truth.
2. `jig logs` reads one or all declared steps with a bounded default, stable
   JSONL, deterministic merge, and duplicate-free cross-process follow.
3. `jig doctor WORKFLOW --ci` fails before tokens for every unattended-hostile
   gate or missing required local prerequisite, without auth/network side effects.
4. `jig resume` settles eligible recovery histories under an explicit action and
   refuses human-only parks before writing.
5. `jig reset` previews without any mutation and applies only to eligible,
   automation-safe unfinished runs with a typed engine acknowledgement.
6. Read-only commands work while another jig process owns the run; mutation
   commands lose safely to `scheduler.lock`.
7. Text/JSON/JSONL streams and exit codes match this plan, all package/race tests
   pass, and `.agents/jig/feature.toml` still validates.
8. `docs/operations.md` is the operator contract, A2 is marked done, and `diff`
   remains visibly deferred rather than being implemented from inferred Git state.
