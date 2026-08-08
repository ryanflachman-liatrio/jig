# Engine Design — Event-Loop Scheduler + Event Journal

Implementation plan for the execution engine (`internal/engine`, `internal/runner`,
`internal/step`, `internal/manifest`, `internal/datastore`) and the TUI run
monitor. This document is self-contained: it captures the design decisions,
the low-level API sketches, and the phased implementation order.

Prerequisites for the reader: [`ARCHITECTURE.md`](ARCHITECTURE.md) (package
layout, invariants), [`workflow-schema.md`](workflow-schema.md) (the runtime
semantics the engine must implement). The workflow package
(`internal/workflow`) is done and tested; a `*Workflow` returned without error
is fully valid and the engine never re-checks structure.

## The chosen architecture, in one sentence

Each run is a single goroutine (the **scheduler**) that owns all of that run's
mutable state, dispatches ready steps to executors, and reacts to one inbound
channel of messages; every state transition is emitted as a typed **Event**,
appended to a JSONL **journal** first, then fanned out to subscribers — of
which the TUI is merely one.

This is "Option A + D" from the design discussion: an event-loop scheduler per
run (A), layered with an event-sourced journal as the seam between engine,
manifest, and TUI (D). Alternatives considered and rejected:

- **A compiled state machine from the .toml** — the validated `*Workflow`
  *already is* the state machine (`depends_on` edges + `[step.loop]`
  back-edges determine every legal transition). A second representation would
  duplicate the validator's knowledge and drift as the schema grows.
- **Actor/dataflow (goroutine per step, channels as edges)** — elegant for
  pure DAGs, but back-edges re-send on channels, `max_parallel` needs a global
  semaphore cutting across the topology, and run state gets smeared across N
  goroutines, making snapshots for the manifest and TUI a coordination
  exercise instead of a struct read.

## Load-bearing decisions

1. **Single-writer state, channel-carried events.** Only the scheduler
   goroutine touches a run's state. Workers and the TUI communicate with it
   exclusively through its inbox channel. No mutexes in workflow logic;
   race-free by construction, not discipline.
2. **The workflow is already the state machine.** The scheduler adds only a
   per-step `Status` and recomputes the ready set after each event. No
   transition table.
3. **Journal before fan-out.** Events are appended to `journal.jsonl`
   *synchronously, before* publishing to subscribers. In-memory state is
   always `fold(journal)`; the TUI can never have seen something the journal
   missed; crash-recovery/resume (deferred, MVP-1) becomes "replay the
   journal" later with no redesign.
4. **The engine never imports Bubble Tea; the TUI never mutates engine
   state.** Events out through channels; verdicts in through `Run.Resolve`.
   Keeps `jig run --headless` possible and engine tests terminal-free.
5. **Everything under `.jig/` is engine-written.** Agents only ever write
   inside their worktree, and only mutators can. See "Resolved semantics"
   below — this is load-bearing for gate trust.

## Component map

```
                 ┌────────────────────────────────────────────────┐
                 │ internal/engine                                 │
  TUI ── Start ─▶│  Manager ──── owns ───▶ map[runID]*Run          │
                 │                            │                    │
                 │                    ┌───────▼────────┐           │
                 │                    │ scheduler loop │ 1 goroutine│
                 │                    │ (owns runState)│ per run   │
                 │                    └───┬────────┬───┘           │
                 │            dispatch ▼            ▼ emit Event   │
                 └──────────────┬───────────────────┬──────────────┘
                                │                   │ (journal write, then fan-out)
                 ┌──────────────▼─────┐   ┌─────────▼──────────┬─────────────┐
                 │ internal/runner    │   │ internal/manifest  │ internal/tui │
                 │ Executor impls:    │   │ journal.jsonl +    │ run list +   │
                 │ agent / command /  │   │ steps/*/result.json│ run monitor  │
                 │ fake (dry-run)     │   └────────────────────┴─────────────┘
                 └────────┬───────────┘
                 ┌────────▼───────────┐
                 │ internal/datastore │  .jig/runs/<id>/ layout, @ref → path
                 └────────────────────┘
```

Responsibilities:

- **`internal/engine`** — `Manager` (registry of concurrent runs), `Run`
  handle, scheduler loop, the `Event` vocabulary + journal envelope, guard and
  loop evaluation. Defines the `Executor` *interface*; contains no execution
  code, no SDK import, no os/exec.
- **`internal/step`** — per-step runtime model: `Status`, `State`, `Result`.
  Pure data, imported by everyone.
- **`internal/runner`** — `Executor` *implementations*: `CommandExecutor`
  (os/exec), `AgentExecutor` (Claude Agent SDK), `FakeExecutor` (tests +
  dry-run), and a `Mux` routing by `Step.Type`. `cmd/jig` wires the mux into
  the manager.
- **`internal/manifest`** — an event *subscriber* that persists: appends every
  envelope to `journal.jsonl`, materializes `steps/<id>/result.json` on
  terminal step events. Deliberately boring.
- **`internal/datastore`** — run-directory convention: creates
  `.jig/runs/<run-id>/`, resolves `@stepid` / `@stepid.field` to paths or
  values, reads producer artifacts.
- **`internal/tui`** — new `screenRuns` (run list) and `screenMonitor`
  (per-run step graph, streaming panes, review prompts) alongside the existing
  selector/detail screens in `rootModel`.

## Low-level design

### `internal/step`

```go
type Status string

const (
    StatusPending          Status = "pending"           // deps not yet satisfied
    StatusRunning          Status = "running"
    StatusValidating       Status = "validating"        // [step.validate] in progress
    StatusAwaitingReview   Status = "awaiting_review"   // parked on a human verdict
    StatusNeedsInput       Status = "needs_input"       // parked on block_on / AskUserQuestion
    StatusAwaitingRecovery Status = "awaiting_recovery" // failed, parked for a human recovery decision
    // StatusAwaitingIntegration is a step whose squash-merge into the run branch
    // hit a conflict; parked for a human to resolve in the run worktree. Like
    // StatusAwaitingRecovery it is parked-but-alive: in-flight siblings keep running.
    StatusAwaitingIntegration Status = "awaiting_integration"
    // StatusStopped is a step whose worker was deliberately stopped by an operator
    // (Run.Stop) — not a failure and not end-of-run. The run stays quiescent (no
    // worker in flight); stopped steps are eligible for resume or reset.
    StatusStopped   Status = "stopped"
    StatusSucceeded Status = "succeeded"
    StatusFailed    Status = "failed"
    StatusSkipped   Status = "skipped" // when=false, or dep skipped/failed
)

// State is the scheduler's mutable record for one step. Only the scheduler
// goroutine reads or writes it.
type State struct {
    ID        string
    Status    Status
    Attempt   int // retry count under on_failure = "retry"
    Iteration int // loop iteration when re-run via [step.loop]
    // Generation counts manual operator resets (Run.Reset). Unlike Attempt,
    // which gates the MaxRetries budget, Generation is purely a provenance axis
    // that marks manual re-runs and makes them legible in the transcript.
    Generation int
    Result     *Result
}

// Result is what execution produced; serialized as result.json.
type Result struct {
    Status       Status          `json:"status"`
    OutputPath   string          `json:"output_path,omitempty"`   // the step's `output` file
    Structured   json.RawMessage `json:"structured,omitempty"`    // producer JSON artifact
    Verdict      string          `json:"verdict,omitempty"`       // scalar output_type / review choice
    ChangedFiles []string        `json:"changed_files,omitempty"` // engine-observed
    Duration     time.Duration   `json:"duration_ms"`
    Err          string          `json:"error,omitempty"`
}
```

Typed string constants match the house style in `internal/workflow/schema.go`
(`StepType`, `FailurePolicy`) and serialize legibly in the journal.

### `internal/engine` — events

A sealed set of small structs behind a marker interface — the `tea.Msg` /
`go/ast.Node` pattern. Consumers type-switch; each event carries exactly its
own fields; TUI conversion to `tea.Msg` is one-to-one.

```go
type Event interface{ isEvent() }

type RunStarted    struct{ RunID, Workflow string; Steps []string }
type RunFinished   struct{ RunID string; Failed bool }
type StepStatus    struct{ RunID, StepID string; From, To step.Status; Attempt, Iteration, Generation int; Err string }
type StepOutput    struct{ RunID, StepID string; Delta string }   // live-typing tail only
type StepMessage   struct{ RunID, StepID string; Seq, Iteration int } // transcript advanced (liveness)
type StepToolCall  struct{ RunID, StepID, Tool, Detail string }   // observed metadata, live
type GateResult    struct{ RunID, StepID string; Passed bool; Detail string }
type LoopFired     struct{ RunID, StepID, Goto string; Iteration, Max int }
type ReviewRequest struct{ RunID, StepID string; Render ReviewRender; Choices []string }
type RunError      struct{ RunID string; Err string } // engine-level, not step-level
// StepsReset is the journaled audit record for an operator reset. It carries
// the operator's chosen target and blast-radius closure — provenance the
// StepStatus stream cannot express. Written before any destructive git operation
// so a crash leaves journal and disk in a consistent state.
type StepsReset struct {
    RunID   string
    Target  string   // the step the operator reset to
    Closure []string // target + transitive depends_on closure (the reset set)
}
```

Journal envelope (JSONL, one event per line):

```jsonl
{"seq":14,"ts":"2026-07-30T10:04:11Z","kind":"step_status","data":{"run_id":"…","step_id":"fix","from":"pending","to":"running","attempt":1,"iteration":2}}
```

Encode is a type switch; decode uses a `kind → func() Event` registry. Test
with a round-trip table test. `StepOutput` deltas are high-volume; journaling
them is optional (decide at Phase 4 — likely journal a truncated form or skip,
since the artifact carries the full text).

**File is truth, bus is liveness.** Bulk step output never rides the event bus
or the journal. The runner writes each step's full conversation directly to a
per-step `transcript.jsonl` (see `internal/transcript` in ARCHITECTURE.md); the
bus carries only `StepMessage{Seq}` — a lightweight "the transcript advanced to
this seq" nudge. A dropped `StepMessage` just means the TUI is one seq stale,
corrected on its next read from disk. This keeps the drop-on-full fan-out safe
for content and the TUI's memory bounded (windowed reads, not a growing
in-memory log). `StepOutput` survives only as the ephemeral live-typing tail for
the currently-streaming bubble; the finalized transcript entry supersedes it.

### `internal/engine` — Manager and Run

```go
type Manager struct {
    mu   sync.Mutex          // guards the registry only — never workflow state
    runs map[string]*Run
    exec Executor            // injected: runner.Mux in prod, FakeExecutor in tests
    root string              // .jig/ root
    subs []chan<- Event      // manager-level fan-out; TUI subscribes once
}

func NewManager(exec Executor, root string) *Manager
func (m *Manager) Start(wf *workflow.Workflow) (*Run, error) // spawns the scheduler goroutine
func (m *Manager) Runs() []RunSnapshot
func (m *Manager) Subscribe() <-chan Event                   // all runs, RunID-tagged

type Run struct {
    ID     string
    cancel context.CancelFunc
    inbox  chan schedMsg // stepDone from workers, verdicts from Resolve, snapshot requests
}

func (r *Run) Resolve(stepID, verdict string) error // review answer → inbox
func (r *Run) Cancel()
func (r *Run) Snapshot() RunSnapshot // request/reply over inbox — reads also go through the single writer
```

Runs share nothing but the manager's registry. Killing a run is `cancel()`;
per-step contexts derive from the run context so in-flight executors unwind.

### `internal/engine` — the scheduler loop

```go
func (s *scheduler) run(ctx context.Context) {
    s.emit(RunStarted{...}) // emit = journal append (sync), then fan-out
    for {
        // 1. Dispatch every ready step, respecting max_parallel.
        for s.inFlight < s.wf.Defaults.MaxParallel {
            st, ok := s.nextReady()
            if !ok { break }
            s.dispatch(ctx, st) // worker goroutine; sends stepDone on s.inbox
        }
        // 2. Terminal check: nothing running, nothing ready or pending-runnable.
        if s.inFlight == 0 && !s.anyPendingRunnable() {
            s.emit(RunFinished{Failed: s.anyFailed()})
            return
        }
        // 3. Block for exactly one occurrence, then loop to re-dispatch.
        select {
        case msg := <-s.inbox:
            s.handle(msg) // stepDone | verdict | snapshotReq
        case <-ctx.Done():
            s.emit(RunFinished{Failed: true})
            return
        }
    }
}
```

The logic lives in three independently table-testable methods:

- **`nextReady()`** — a step is ready when `Status == pending`, every
  `depends_on` is `succeeded` (or `failed` under `on_failure = "continue"`),
  and its `when` guard evaluates true. Guard false → `skipped` + event.
  **Skip cascades:** a dependent of a skipped step is itself skipped (its
  `@ref` inputs can't resolve). Add this sentence to workflow-schema.md when
  implemented.
- **`handle(stepDone)`** — run `[step.validate]` (emit `GateResult` either
  way); on failure apply `on_failure`: `retry` → `Attempt++`, back to
  `pending` while under `max_retries`; `continue` → `failed` but dependents
  stay eligible; `abort` (and `retry` past its cap) → **park in
  `awaiting_recovery` and emit `RecoveryRequest`** rather than cancelling — the
  run and any in-flight siblings stay alive until a human retries, resumes the
  failed session, or aborts via `Run.Recover` (see "Failure recovery" in
  workflow-schema.md). Worktree/step-dir setup failures route through the same
  gate. On success,
  evaluate `[step.loop]`: if `when` holds and `Iteration < max_iterations`,
  emit `LoopFired`, reset the goto target *and every step on a path between
  goto and the looping step* to `pending` with `Iteration+1`, and record the
  `feedback` ref as an extra input for the target's next run. Past the cap →
  abort the run (per spec).
- **Guard/loop evaluation** — `workflow.ParseCondition` yields
  `(stepid, fieldPath, op, value)`; evaluation reads the referenced step's
  in-memory `Result` (verdict for scalar `output_type`; decoded `Structured`
  map, cached on `State`, for field paths), walks the path, compares. No
  runtime type checks or error paths — load-time validation plus constrained
  decoding made illegal states unrepresentable (see "Resolved semantics").

**Review steps never enter a worker**: the scheduler marks them
`awaiting_review`, emits `ReviewRequest`, and does *not* count them against
`max_parallel` (they're human-bound, not machine-bound). The eventual verdict
message on the inbox is that step's completion; the verdict becomes
`Result.Verdict`.

### Stop — per-step quiescence

An operator may stop one running step without ending the run:

```go
func (r *Run) Stop(stepID string) { r.inbox <- stopMsg{stepID: stepID} }
```

`handleStop` in the scheduler:
1. Cancels that step's child context (each worker gets its own child context,
   so only that worker unwinds — not the run context or sibling workers).
2. Adds the step to a `stopping` set so the scheduler knows the upcoming
   `stepDone` is intentional rather than a failure.
3. When the worker's `stepDone` arrives with the cancelled context, the scheduler
   detects it is in `stopping`, transitions the step to `StatusStopped` (not
   `StatusFailed`), and does not apply the failure policy.

A run is **quiescent** when `inFlight == 0` with no pending-runnable steps.
A stopped step keeps the run alive and quiescent: the run stays open but idles,
waiting for an operator action (resume or reset). Quiescence is the precondition
for `Run.Reset` — reset never mutates while a worker is live.

### Reset — dependency closure and rewind+replay

`Run.Reset(target)` rewinds the run branch and re-queues the target step and its
dependency closure. It is only valid on an unfinished, quiescent run. See
[ADR 0008](../adr/0008-manual-reset-rewind-and-replay.md) for the full algorithm
rationale and rejected alternatives.

**Algorithm:**
1. Compute the **reset set** = target step ∪ its transitive `depends_on` closure.
2. Write `StepsReset{target, closure}` to the journal *before* any git mutation
   (crash-consistent ordering: if jig crashes after the event write, a future
   `fold(journal)` can reconstruct post-reset state from `StepStatus` transitions).
3. `git reset --hard` the run branch to the commit just before the earliest
   reset-set commit (identified via the step→commit map built from squash commits).
4. Cherry-pick, in original order, every later commit that is **not** in the reset
   set (the independent "survivors"). In a linear workflow the reset set is a
   contiguous tail and this step is a no-op.
5. Return each step in the reset set to `StatusPending` and increment its
   `Generation` counter. Unlike `Attempt` (which gates `MaxRetries`), `Generation`
   is a provenance axis only — it makes manual re-runs legible in the transcript
   without consuming the automatic-retry budget.

Cherry-pick conflicts surface through the integration-conflict gate (same path as
squash-merge conflicts from parallel steps — no auto-resolver). Reset on a fully
settled run is locked; reopening a finished run is a deferred follow-up.

### The `Executor` seam (engine ↔ runner)

The interface is **defined in `engine` (the consumer), implemented in
`runner`** — dependency inversion, so `engine` imports neither the SDK nor
os/exec:

```go
// engine package
type Executor interface {
    Execute(ctx context.Context, req StepRequest, report Reporter) (*step.Result, error)
}

type StepRequest struct {
    RunID    string
    Step     *workflow.Step
    Inputs   []ResolvedInput // @refs already resolved to paths / inlined values by datastore
    Feedback string          // loop feedback path, when re-running
    Worktree string          // "" when isolation = none
}

// Reporter carries live signals out of an in-flight execution. The scheduler
// wraps it to tag events with run/step ids and route through emit().
type Reporter interface {
    Output(delta string)
    ToolCall(tool, detail string)
}
```

`FakeExecutor` is scripted with a `map[stepID]fakeOutcome` (result, delay,
optional streamed deltas). The scheduler cannot tell it from the real thing —
that is the point: an engine test is a workflow TOML string + a scripted fake
+ an expected event sequence, run under `-race`.

### `internal/datastore` and `internal/manifest`

- `datastore.RunDir(root, runID)` creates
  `.jig/runs/<run-id>/{artifacts/,steps/}`.
- `datastore.Resolve(run, input workflow.Input)` maps `@stepid` →
  `artifacts/<stepid>.{md,json}`, `@stepid.field` → the decoded field value
  (inlined), literal paths pass through.
- `manifest.Writer` subscribes to the run's events: every envelope appended to
  `journal.jsonl` (opened `O_APPEND`, one `Write` call per line); on each
  terminal `StepStatus`, write `steps/<id>/result.json`.

### `internal/tui`

Follows the two patterns the package already has:

- **Screens:** `rootModel` gains `screenRuns` + `screenMonitor`; the detail
  screen gains an `r` keybinding that calls `Manager.Start` and switches to
  the monitor.
- **Event ingestion:** the `client.go` idiom — a `waitForEngineEventCmd(ch)`
  tea.Cmd that receives one `engine.Event`, wraps it as a `tea.Msg`, and
  re-arms. One manager-level subscription at the root, routed to the active
  screen. `runsModel` consumes `StepStatus`/`RunFinished` for list rows;
  `monitorModel` consumes everything.
- **Review flow:** `ReviewRequest` renders the artifact (glamour, reuse
  `viewer.go` machinery) or the diff, with `Choices` as a keyed picker;
  selection calls `run.Resolve(stepID, verdict)` in a tea.Cmd. The TUI holds
  no verdict state — the next `StepStatus` event is the confirmation.

## Resolved semantics (decisions from design review)

These were open questions; they are now decided. Implement accordingly.

### 1. Enum/field guards: validated at load, dumb at runtime

End-to-end flow for `status = { enum = ["success", "fail", "review"] }` on a
producer and `when = "research.status == 'success'"` on a consumer:

- **Load time (done, in `internal/workflow`):** the enum parses into
  `Field{Type: FieldEnum, Enum: …}`; `ParseCondition` + the validator check
  that `research` is in `depends_on`, the field path resolves via
  `Schema.lookup`, and the compared literal is an enum member. A typo'd value
  fails `jig validate`.
- **Producer completion (engine):** compile the step's `Schema` via the
  existing `Schema.JSONSchema()`, run headless
  (`claude -p --output-format json --json-schema '<schema>'`), take the
  `structured_output` field from the CLI result — constrained decoding
  guarantees it conforms — store it on `Result.Structured`, write it to
  `artifacts/<step-id>.json`.
- **Guard evaluation (scheduler):** decode `Result.Structured` once into a
  `map[string]any` (cache on `State`), walk the field path, string-compare.
  No re-validation, no "field missing" error path. Bare-bool guards
  (`when = "research.blocked"`) truthy-test; scalar verdicts
  (`when = "approve == 'revise'"`) compare `Result.Verdict`.
- Field refs as *inputs* (`inputs = ["@research.status"]`) use the identical
  decode-and-walk in `datastore.Resolve`, inlined into the prompt.

### 2. The engine writes all results; agents never write artifacts

Uniform rule: **everything under `.jig/` is engine-written; agents only ever
write inside their worktree, and only mutators can.** Per output kind:

- **Producer structured output:** arrives in the headless CLI's
  `structured_output` field — never a file the agent wrote. Engine writes
  `artifacts/<step-id>.json`. Producers run with zero write tools. This is
  load-bearing for gate trust: an agent cannot write its own report card.
- **Content output** (`output = "….md"`, default `text` type): **the engine
  captures the agent's final assistant message and writes it to the `output`
  path.** The agent does not need — and should not get — `Write` for this.
  Rationale: keeps read-only steps genuinely read-only (matches the `triage`
  example in workflow-schema.md, which declares `output` with only
  Read/Grep/Glob); avoids `output` forcing `Write` into `allowed_tools` and
  spuriously flipping worktree isolation via `isMutating()`; removes the
  "agent forgot to write the file" failure mode. The prompt contract is
  "your final message becomes the artifact" — add this sentence to
  workflow-schema.md at Phase 4.
- **Mutators:** `Edit`/`Write`/`Bash` exist to change the repo inside their
  worktree, never to write artifacts. No `output`; the result is the diff plus
  engine-observed metadata (from the SDK tool-call stream) written to
  `steps/<step-id>/result.json`.

`.jig/` living outside the working tree makes this compose with worktrees: the
engine writes artifacts from the orchestrating process regardless of which
worktree the step ran in.

### 3. Custom agent files need no jig-specific frontmatter

All jig invariants are enforced on the **resolved step** (agent-file
`tools`/`model` fold in before defaulting and validation — see
`load.go`/`agent_file.go` ordering) or by the **execution harness** (tool
allowlist enforced by the SDK; structured output constrained-decoded
regardless of prompt; artifacts engine-written). A custom agent file cannot
express anything that bypasses the step contract.

Therefore: do **not** require developers to modify agent-file frontmatter.
The agent file owns the *who* (prompt, default tools, default model); the
`.toml` step owns the entire jig contract (inputs, output, schema, validate,
loop, isolation, budgets). Per-step steering of a borrowed prompt is
`append_system_prompt`. `parseAgentFile` deliberately ignores unknown
frontmatter keys so richer files still load — keep that.

Kept-as-is conservatisms: `isMutating()` checks `AllowedTools` only, so a step
that folds in `Bash` but strips it via `disallowed_tools` still defaults to
worktree isolation — erring toward isolation is the right failure direction
(`isolation = "none"` opts out explicitly). Tool-name typos in frontmatter are
not validated (the tool namespace is open-ended with MCP); same as
`allowed_tools` in the toml.

## Coding patterns (and why they're the idiomatic choice)

| Pattern | Where | Why |
|---|---|---|
| Single-writer event loop | scheduler | "Share memory by communicating." One goroutine owns state; zero mutexes in workflow logic; `-race` clean by construction. |
| Sealed message types + type switch | `engine.Event`, TUI msgs | The `tea.Msg` / `go/ast.Node` pattern. Each variant carries only its own fields; the journal envelope makes the union serializable. |
| Consumer-defined interfaces, dependency inversion | `engine.Executor` implemented by `runner` | "Accept interfaces, return structs"; interfaces belong to the package that needs them. Engine stays free of SDK/os-exec imports; the fake executor is first-class. |
| `context.Context` cancellation tree | Manager → Run → worker → SDK call | One `cancel()` unwinds everything downstream. First parameter, never stored in a struct. |
| Typed string enums, zero-value defaults | `step.Status`, event kinds | Matches `workflow`'s `StepType`/`FailurePolicy` style; self-describing JSON. |
| Request/reply over the owner's channel | `Run.Snapshot()` | Even reads go through the single writer — "who can touch state" has a one-word answer. |
| Append-only JSONL journal | `manifest` | One `O_APPEND` write per event; state = fold(log); buys resume/audit later without redesign. |
| Elm architecture, msgs at the boundary | TUI | Engine events become `tea.Msg`s in one adapter; the whole system is two event-sourced loops meeting at a channel. |
| Table-driven tests with fakes | engine tests | House style (TESTING.md). Workflow TOML string + scripted `FakeExecutor` + expected event sequence, under `-race`. `testing/synctest` (Go 1.24) if timing cases need determinism. |

## Phases — each visibly testable in the TUI

### Phase 1 — the plumbing end to end, fake executor

Build: `internal/step` (Status/State/Result) · `internal/engine` (events,
journal envelope, Manager, Run, scheduler with ready-set + `max_parallel`; no
gates/guards/loops yet) · `runner.FakeExecutor` with configurable per-step
delay · TUI `screenRuns` + `screenMonitor` + event-drain cmd · `r` keybinding
on the detail screen (dry-run mode).

**UI test:** open a workflow, press `r`, watch steps flow
`pending → running → succeeded` with parallelism visible; start the same
workflow twice more, watch three runs progress independently in the run list;
cancel one without touching the others. Proves the entire A+D skeleton —
scheduler, events, fan-out, multi-run — before any real process runs.

Commit order within the phase: (1) `internal/step` + event vocabulary +
journal envelope with table tests; (2) scheduler + Manager + fake executor
with event-sequence tests; (3) TUI screens.

### Phase 2 — real command steps, real persistence

Build: `runner.CommandExecutor` (os/exec, cwd, output capture) ·
`internal/datastore` run dirs + `@ref` path resolution · `manifest.Writer`
(journal + result.json) · failure policies (`abort`/`retry`/`continue`) ·
`[step.validate]` gates (command / exists / contains).

**UI test:** a command-only workflow (`go vet` → `go build` → `go test`)
actually executes; a deliberately failing step shows retry attempts ticking up
then the policy applying; afterwards `.jig/runs/<id>/journal.jsonl` reads as a
faithful transcript of what you watched.

### Phase 3 — control flow and the human

Build: guard evaluation (`when`, skip + skip-cascade) · `[step.loop]` with
iteration reset and feedback wiring · review steps: `ReviewRequest` rendering
(markdown artifact via glamour; diff deferred to Phase 5), verdict picker,
`Run.Resolve`.

**UI test:** a bugfix-shaped workflow built from *command* steps + a review
step — choose `revise`, watch the loop re-run the target with the iteration
counter climbing; choose `approve`, watch the `when`-guarded merge step unlock
while the reject branch shows `skipped`. All of MVP-1's control flow, zero
tokens spent.

### Phase 4 — agent steps

Build: `runner.AgentExecutor` on the Claude Agent SDK (the
connection/streaming knowledge in `tui/client.go` largely migrates here) ·
`Reporter` deltas → live streaming pane per step · producers: compile
`[step.schema]` via `Schema.JSONSchema()`, headless constrained decode, engine
writes the JSON artifact · content outputs: engine writes the final assistant
message to the `output` path · engine-observed metadata (tool calls, changed
files, duration) from the message stream. Update workflow-schema.md with the
"final message becomes the artifact" and skip-cascade sentences.

**UI test:** a research → review workflow with a real non-mutating producer;
watch its text stream live in the step pane; the review step renders
`@research.summary`, and a guard branches on `@research.status`.

### Phase 5 — mutation, safely

Build: run-branch creation, per-step worktree branched off run HEAD, branch
naming (`jig/<workflow>/<step-id>`), squash-merge back into run branch on step
completion, validate-inside-worktree · diff rendering for `review = "diff"` ·
integration-conflict gate (parallel steps that touch the same files) ·
cancellation hardening (worktree cleanup on abort) · final human-gated merge at
run end.

**UI test:** the full `examples/feature.toml` kitchen sink — mutating fix step
in its own worktree, gate runs `go test` inside it, human reviews the actual
diff, `revise` loops with feedback, `approve` triggers the final merge gate.

### Deferred (matches workflow-schema.md MVP-1 scope)

Resume-from-journal (the journal already makes it possible), map/fan-out over
dynamic lists, secrets, remote execution. Also deferred: journaling
`StepOutput` deltas in full (decided to skip at Phase 4 — the transcript carries
the full content). Reopening a fully-settled run for reset is also deferred.
