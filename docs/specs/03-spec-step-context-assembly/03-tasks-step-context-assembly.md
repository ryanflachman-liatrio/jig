# 03-tasks-step-context-assembly.md

Task list for the **engine-assembled deterministic step-context preamble**.
Derived from
[`03-spec-step-context-assembly.md`](03-spec-step-context-assembly.md); the six
parent tasks map 1:1 to the spec's dependency-ordered
[Demoable Units of Work](03-spec-step-context-assembly.md#demoable-units-of-work).

Scope reminder: this is an **engine + runner + schema + docs** change, not a TUI
change. jig gains a deterministic *input/position* contract that mirrors the
existing deterministic *output* contract (`internal/workflow/base_schema.go`).
The DAG, static validation, and termination guarantee are untouched — no new
edges, no new events (spec Non-Goals 5, 7). The only user-visible artifact is the
text preamble prepended to the agent's single user turn.

Dependency order: Units 1–3 build the always-on, graph-derived preamble
end-to-end (the core value); Units 4–5 add the `inject_context` opt-out and the
optional `[step.context]` authoring block; Unit 6 aligns docs/example and proves
the payoff by deleting the now-redundant skill narration. Later parents depend on
earlier ones (2 needs 1's renderer; 3 needs 2's assembly; 5 needs 4's toggle for
its contradiction check; 6 exercises 4 and 5 in the example).

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/step/context.go` (new) | Home of the new `StepContext` + `ContextNeighbor` data types and the pure `Render()`. `internal/step` imports nothing, keeping the format independently testable (spec Repository Standards). |
| `internal/step/context_test.go` (new) | Golden, determinism (100×), and omission tests for `Render()`. Follows the table-driven house style. Unit 1. |
| `internal/step/step.go` | Existing pure-data home; `State.Iteration` (line 31) is the source for the iteration line. Read-only reference for style/placement. |
| `internal/engine/executor.go` | Add `WorkflowContext string` to `StepRequest` (struct lines 16–40). Unit 2. |
| `internal/engine/engine.go` | `buildRequest` (line 832) assembles + renders the `StepContext` for agent steps; the `scheduler` struct (line 329) and its constructor (line ~406) gain a `rerunSource map[string]string`; `fireLoop` (line 1398) records the firing source beside `stepFeedback[loop.Goto]`; `depsReady` (line 555) is the reason the upstream status token is retained. Units 2–3. |
| `internal/engine/context.go` (new) | The `buildStepContext(st)` assembly helper (topology scan, neighbor building, run-state derivation) kept out of `engine.go` for readability. Units 2, 3, 5. |
| `internal/engine/context_test.go` (new) | Engine-assembly tests: topology, framing-only, iteration/rerun, first-run, inject-off, purpose propagation. Units 2–5. |
| `internal/runner/agent.go` | `buildAgentPrompt` (line 433) prepends `req.WorkflowContext` before the body with the `---` delimiter; `resolvedInputLabel` (line 474) is the style reference for engine-authored prompt text. Unit 2. |
| `internal/runner/agent_test.go` | Prepend test + empty-context byte-identical regression lock. Unit 2. |
| `internal/workflow/schema.go` | Add `InjectContext *bool` + `Context *StepContextSpec` to `Step` (lines 159–219); `InjectContext *bool` to `Defaults` (line 143); define `StepContextSpec{Purpose, Notes}`. Units 4–5. |
| `internal/workflow/load.go` | `applyDefaults` (line 71) propagates `inject_context` from `[defaults]` to unset steps; add the `Step.InjectContextEnabled()` effective-value helper. Unit 4. |
| `internal/workflow/validate.go` | New rejections: `inject_context`/`[step.context]` on non-agent steps (`checkCommand` line 275, `checkReview` line 295); the `[step.context]` + `inject_context = false` contradiction and field checks (near `checkAgent` line 234). Units 4–5. |
| `internal/workflow/workflow_test.go` | Table-driven valid + invalid rows: `inject_context` defaulting/precedence; `[step.context]` parse + rejections. Units 4–5. |
| `internal/workflow/base_schema.go` | No code change — the structural + doc-comment precedent for a jig-imposed deterministic contract. Reference only. |
| `examples/feature.toml` | Exercise an explicit per-step `inject_context` override and a `[step.context]` block; keep `validate` green; refresh the top-of-file capability comments. Unit 6. |
| `docs/workflow-schema.md` | New "Step context (engine-assembled)" section; `inject_context` in the `[defaults]` + agent-step tables; the `[step.context]` block in the agent-step section. Unit 6. |
| `docs/adr/0006-engine-assembles-step-context-preamble.md` (new) | ADR recording the decision and rejected alternatives (mirrors `0001`–`0004`; `0005` is claimed by spec 02). Unit 6. |
| `.agents/skills/plan/SKILL.md`, `.agents/skills/implement/SKILL.md` | Remove the hand-authored upstream/downstream/loop narration; retain only *how to do the job*. Unit 6. |
| `README.md` | Doc-hygiene refresh (audit FLAG-2): correct stale "Go 1.24" → 1.25 and "engine not built" → runs today. Unit 6 (task 6.8). |
| `docs/specs/03-spec-step-context-assembly/03-proofs/*` | Committed proof artifacts: render golden, assembled preambles, `validate` outputs, skill diff, behavioral spot-check. All units. |

### Notes

- **Determinism is the product.** Fixed neighbor ordering (upstream in
  `depends_on` order; downstream in `wf.Steps` declaration order) and **no map
  iteration into output** are load-bearing for the golden tests — comment them at
  the assembly and render sites (CLAUDE.md: comments explain the non-obvious why).
- **Schema changes are exhaustive and load-time** (`docs/TESTING.md`): every new
  field needs a valid-path decode assertion **and** one `TestDecodeInvalid` row
  per rejection, asserting the specific error substring. Use inline TOML string
  constants and the `Decode(data, baseDir)` seam (`""` skips file checks).
- **Persistence-off is a first-class path.** Assembly and rendering are pure and
  need no run dir. The empty-context path must yield a **byte-identical** prompt
  to today's four-part `buildAgentPrompt` output — lock it with a regression test.
- **`internal/step` imports nothing.** `Render()` uses only `strings`/`fmt`; keep
  the engine and workflow types out of it. The engine maps its `workflow`/`step`
  data into the pure `StepContext` at assembly time.
- Run engine/runner work under the race detector and re-validate the example
  before finishing each parent:
  `gofmt -l -w . && go vet ./... && go test ./... -race && go run ./cmd/jig validate examples/feature.toml`.
- Proof artifacts are plain-text renders of the example workflow — no secrets
  (spec Security Considerations); safe to commit under `03-proofs/`.

## Tasks

### [x] 1.0 `StepContext` data model & pure renderer

Define the typed, engine-agnostic `StepContext` / `ContextNeighbor` data types in
`internal/step` (pure data, imports nothing) and a pure `Render() string` that
produces the exact byte layout locked by the spec's
[Rendered format](03-spec-step-context-assembly.md#rendered-format). No engine
wiring yet — pure data + rendering so the format is locked by golden tests in
isolation. Demoable: `go test ./internal/step -run TestStepContextRender` turns
a fully-populated struct into the committed golden string, byte-for-byte.
(Spec Unit 1.)

#### 1.0 Proof Artifact(s)

- Test (golden): `go test ./internal/step -run TestStepContextRenderGolden`
  passes — a fully-populated `StepContext` (2 upstream incl. one `succeeded` +
  one `failed`, 2 downstream incl. one propagated-purpose + one guarded
  graph-derived, own `purpose` + `notes`, a revise re-run at `Iteration == 1`
  under `max_iterations = 3`) renders byte-for-byte equal to the committed
  golden file. Maps Unit 1 FR "pure `Render()` … produces the delimited section".
- Test (determinism): `go test ./internal/step -run TestStepContextRenderStable`
  passes — the same input renders identically across 100 iterations (guards the
  "no map iteration leaks into output" FR).
- Test (omission): `go test ./internal/step -run TestStepContextRenderOmits`
  passes — an all-zero `StepContext` renders to `""`; a topology-only struct
  (no state, no purpose/notes) omits the `Purpose`/`Notes`/`State` lines and the
  iteration clause; a pipeline-entry struct (no upstream/downstream/state)
  renders only the position line plus the `---` delimiter.
- Proof artifact: the rendered golden section committed at
  `03-proofs/1.0-render-golden.txt`, byte-identical to `Render()`'s output
  (no trailing newline) and matching the spec's Rendered format block.

#### 1.0 Tasks

- [x] 1.1 Create `internal/step/context.go`. Define `ContextNeighbor` with fields
  `ID string`, `Kind string` (`agent`/`command`/`review`/`human`), `Status string`
  (upstream only), `Purpose string` (optional, propagated in), `Conditional string`
  (downstream only — the neighbor's `when` guard), and `Fields []string` (downstream
  only — the field name(s) of this step the neighbor consumes). Doc-comment each
  field's provenance in the `base_schema.go` style.
- [x] 1.2 In the same file define `StepContext` with `WorkflowName`, `StepID`,
  `Purpose`, `Notes string`; `Iteration`, `MaxIterations int`; `RerunReason string`;
  and ordered `Upstream`, `Downstream []ContextNeighbor`. Add a type doc comment
  noting there is deliberately **no `block_on` field** (link the spec's "Why no
  `block_on` round"). Import only `strings`/`fmt`.
- [x] 1.3 Implement `func (c StepContext) Render() string` per the spec's
  [exact rendering algorithm](03-spec-step-context-assembly.md#exact-rendering-algorithm):
  build the ordered parts into a `[]string` and join with `"\n\n"`; return `""`
  immediately when `c.StepID == ""`; end at `---` with **no trailing newline**.
  Emit the iteration clause only when `Iteration > 0` (render `Iteration+1`), the
  `Purpose`/`Notes` lines only when non-empty, the `Upstream`/`Downstream` blocks
  only when their slice is non-empty, and the `State:` line only when
  `RerunReason != ""`. Comment the fixed ordering / no-map-iteration determinism
  requirement.
- [x] 1.4 Add the downstream-clause helper: `KindLabel` = `"human review"` for a
  `review`/`human` neighbor else `agent`/`command`; the clause is the neighbor's
  `Purpose` verbatim when present (it *replaces* the derived clause), otherwise
  `a person reviews your <fields>` for review/human else `consumes your <fields>`;
  `<fields>` is the backticked field name(s) in order (`` `tasks` ``;
  `` `tasks`, `approach` ``) or the literal `output` (no backticks) for a bare
  reference with empty `Fields`. Append `` (conditional on `<guard>`)`` when
  `Conditional != ""`. Upstream neighbors render `` - `<ID>` (<Status>)`` plus
  `` — <Purpose>`` only when a purpose is present.
- [x] 1.5 Write the committed golden `03-proofs/1.0-render-golden.txt`,
  byte-identical to the spec's Rendered format block (no trailing newline). This
  is the assertion target, not an illustration.
- [x] 1.6 Add `internal/step/context_test.go` with `TestStepContextRenderGolden`
  (construct the fully-populated struct from 1.0's proof description; compare to
  the golden file bytes), `TestStepContextRenderStable` (assert 100 renders are
  identical), and `TestStepContextRenderOmits` (zero value → `""`; topology-only
  omits optional lines; entry-only → position line + `---`). Table-driven where
  the shape fits.

### [x] 2.0 Engine assembly of topology + runner prepend

Add `WorkflowContext string` to `engine.StepRequest`
(`internal/engine/executor.go`); in `buildRequest` (`engine.go:832`) build a
`StepContext` for **agent steps only** from the scheduler's `wf` (DAG +
declaration order) and `states`, populating **upstream** (each `depends_on` dep
with its dispatch-time `Status`, in `depends_on` order — the status token
retained because `depsReady` admits an `on_failure = "continue"` failed dep) and
**downstream** (every step listing this one in `depends_on`, its `Kind` mapped to
`human`/`agent`/`command`, the consumed `@thisstep.field`s, and a
`(conditional on <guard>)` clause when the consumer has a `when` guard), render
it, and set `req.WorkflowContext`; command/review steps leave it empty. Then
`buildAgentPrompt` (`agent.go:433`) **prepends** a non-empty `WorkflowContext`
ahead of the skill body with the `---` delimiter, preserving the existing
body → append → inputs → feedback order. Framing-only: no upstream artifact
bodies, no live sibling status. Demoable: the assembled `plan` preamble for
`examples/feature.toml` names its upstream deps and downstream consumers.
(Spec Unit 2.)

#### 2.0 Proof Artifact(s)

- Test (engine): `go test ./internal/engine -run TestBuildRequestWorkflowContext`
  passes — dispatching the `plan` step of a fixture DAG yields a
  `req.WorkflowContext` listing its upstream deps with statuses and its two
  downstream consumers (`plan_review` → `human review` of `summary`; `implement`
  → `agent` consuming `tasks`), with the guarded edge annotated
  `(conditional on …)`. Maps Unit 2 upstream/downstream FRs.
- Test (runner, prepend): `go test ./internal/runner -run TestBuildAgentPromptPrependsContext`
  passes — a non-empty `WorkflowContext` appears at the front of the built prompt,
  separated from the body by `---`, ahead of append/inputs/feedback.
- Test (runner, regression lock): `go test ./internal/runner -run TestBuildAgentPromptEmptyContext`
  passes — with `WorkflowContext == ""` the built prompt is byte-identical to the
  current four-part prompt (proves persistence-off / opt-out stays a no-op).
- Test (framing-only): `go test ./internal/engine -run TestBuildRequestNoSiblingLeak`
  passes — the assembled preamble contains no non-dependency sibling ids and no
  upstream artifact body/`summary` text.
- Test (non-agent empty): `go test ./internal/engine -run TestBuildRequestNonAgentEmpty`
  passes — a `command` step and a `review` step both dispatch with
  `req.WorkflowContext == ""`. Maps Unit 2 FR "command and review steps shall
  leave it empty".
- Proof artifact: `03-proofs/2.0-plan-preamble.txt` — the assembled preamble for
  the `plan` step of `examples/feature.toml` (first run: no iteration clause, no
  `State`, graph-derived neighbor lines).

#### 2.0 Tasks

- [x] 2.1 Add `WorkflowContext string` to `StepRequest` in
  `internal/engine/executor.go` (after `Feedback`), with a doc comment: pre-rendered
  preamble, `""` when none (non-agent step or `inject_context` off), prepended at
  the front of the agent's user turn.
- [x] 2.2 Create `internal/engine/context.go` with
  `func (s *scheduler) buildStepContext(st *workflow.Step) step.StepContext`. Set
  `WorkflowName = s.wf.Meta.Name`, `StepID = st.ID`. Build `Upstream` by iterating
  `st.DependsOn` in order: for each dep, `ID`, `Kind` = `s.stepByID(dep).Type`, and
  `Status` = string of `s.states[dep].Status`. Comment that the status token is
  retained because a `on_failure = "continue"` dep can be `failed` yet dispatched
  (`depsReady`, engine.go:561).
- [x] 2.3 In `buildStepContext`, build `Downstream` by iterating `s.wf.Steps` in
  declaration order and selecting steps whose `DependsOn` contains `st.ID`. For each
  consumer set `Kind` (`review` → `human`, else the step's `Type`), `Conditional` =
  `consumer.When` (when non-empty), and `Fields` = the field name(s) it consumes from
  `st`: from the consumer's `inputs` `@st.field` refs (`Input.Ref == st.ID` →
  `Input.RefField`) **and** — for a `review` consumer — from its `review = "@st.field"`
  ref (so `plan_review` yields `summary`). Preserve the consumer's input-declaration
  order; a bare `@st` with no field leaves `Fields` empty (renders `output`).
- [x] 2.4 In `buildRequest` (`engine.go:832`), for `st.Type == StepAgent` only, call
  `buildStepContext`, `Render()` it, and set `req.WorkflowContext`; leave it `""` for
  command/review steps. Keep the rest of the `StepRequest` construction unchanged.
- [x] 2.5 In `buildAgentPrompt` (`agent.go:433`), when `req.WorkflowContext != ""`,
  write it followed by `"\n\n"` **before** the `req.Step.AgentPrompt()` body block,
  leaving the body → append → inputs → feedback order intact. Update the function's
  doc comment to list the preamble as the new first piece; comment that it rides at
  the front of the single user turn (there is no separate system prompt).
- [x] 2.6 Add `internal/engine/context_test.go`:
  `TestBuildRequestWorkflowContext` builds a fixture DAG mirroring the `plan`
  neighborhood (3 upstream agents, `plan_review` review consuming `summary`,
  `implement` agent consuming `tasks`/`approach` guarded by
  `plan_review == 'approve'`), dispatches `plan`, and asserts the upstream lines
  (with statuses), the two downstream lines (kind labels + fields), and the
  conditional clause on `implement`. Add `TestBuildRequestNoSiblingLeak` asserting no
  non-dependency sibling id and no artifact-body text appear. Add
  `TestBuildRequestNonAgentEmpty` asserting a `command` step and a `review` step
  each dispatch with `req.WorkflowContext == ""` (Unit 2 non-agent FR).
- [x] 2.7 Add runner tests in `internal/runner/agent_test.go`:
  `TestBuildAgentPromptPrependsContext` (non-empty context at the front with `---`
  before the body, order preserved) and `TestBuildAgentPromptEmptyContext`
  (byte-identical to the pre-feature four-part prompt for `WorkflowContext == ""`).
- [x] 2.8 Produce `03-proofs/2.0-plan-preamble.txt` — capture the assembled `plan`
  preamble for `examples/feature.toml` (e.g. a small `-run` test or a golden-writer
  helper that renders the real step), first-run form (no iteration clause, no
  `State`).

### [x] 3.0 Run-state framing (loops & re-runs)

Populate the **state** part of `StepContext` so a re-run agent is told it is
iterating and why — scoped to loop re-runs (the only re-dispatch that re-runs
`buildAgentPrompt`; a `block_on` resume overrides the query and continues the
existing conversation, so it needs no fresh preamble). Populate `Iteration` from
`state.Iteration` (0-indexed) and `MaxIterations` from the firing loop's
`max_iterations`, emitting `iteration {Iteration+1} of {M}` **only when
`Iteration > 0`**. Record the firing loop's source step id in a new **in-memory**
scheduler map `rerunSource` (`goto → sourceStepID`, last-fired wins) at the
existing `fireLoop` write site (`engine.go:1398`, beside
`stepFeedback[loop.Goto]`), and derive `RerunReason` from that source step's
`Type` (review → "requested revisions"; agent/command gate → "gate reported a
failure"). No new persisted (on-disk) state, no new events. Demoable: the `plan`
preamble on its second (revise) iteration shows `iteration 2 of 3` and the re-run
reason. (Spec Unit 3.)

#### 3.0 Proof Artifact(s)

- Test (revise loop): `go test ./internal/engine -run TestWorkflowContextReviseIteration`
  passes — a step at `Iteration == 1` under a `max_iterations = 3` review-revise
  loop renders `iteration 2 of 3` and a `State:` line naming the review verdict
  ("requested revisions"). Maps Unit 3 iteration-line + RerunReason FRs.
- Test (gate loop): `go test ./internal/engine -run TestWorkflowContextGateRerun`
  passes — a step re-run by an `agent`/`command` gate renders the gate-failure
  phrasing ("gate reported a failure"), proving `RerunReason` derives from the
  recorded source step's `Type`.
- Test (first run): `go test ./internal/engine -run TestWorkflowContextFirstRun`
  passes — a first-run step (`Iteration == 0`, no loop source) renders **no**
  `State` line and no iteration clause.
- Proof artifact: `03-proofs/3.0-revise-loop-preamble.txt` — the `plan` preamble
  on its `plan_review`-triggered revise iteration (same topology as 2.0 plus the
  iteration clause and `State` line).

#### 3.0 Tasks

- [x] 3.1 Add `rerunSource map[string]string` to the `scheduler` struct
  (`engine.go:329`, beside `stepFeedback` at line 340) with a comment: `goto →
  firing source step id; last-fired wins per re-run; in-memory only, never
  persisted (mirrors stepFeedback)`. Initialize it with `make(...)` in the scheduler
  constructor (~line 406).
- [x] 3.2 In `fireLoop` (`engine.go`, at the `stepFeedback[loop.Goto] = content`
  write, line 1398), also record `s.rerunSource[loop.Goto] = stepID`. Place it
  **outside** the `if loop.Feedback != ""` block so the re-run reason is recorded
  even when no feedback ref is wired. Comment why last-write-wins is correct (only
  one loop fires per re-run).
- [x] 3.3 In `buildStepContext`, set `Iteration = state.Iteration`. When
  `src, ok := s.rerunSource[st.ID]` is present, set `MaxIterations` from that source
  step's firing loop (`s.stepByID(src).Loop.MaxIterations`) — the loop whose `Goto`
  targets `st.ID`.
- [x] 3.4 In `buildStepContext`, derive `RerunReason` only when a `rerunSource`
  entry exists: look up the source step's `Type` — `review` →
  `` "re-running because `<src>` requested revisions on the previous iteration.
  Address the reviewer feedback in your inputs." ``; `agent`/`command` →
  `` "re-running because the `<src>` gate reported a failure. …" `` (the complete,
  pre-punctuated string per the spec's State-line rule). Leave `RerunReason == ""`
  on a first run so `Render()` omits the `State` line.
- [x] 3.5 Confirm the render-side guards from Unit 1 hold end-to-end: iteration
  clause only when `Iteration > 0`; `State` line only when `RerunReason != ""`. No
  new render code — assert via the engine tests below.
- [x] 3.6 Add engine tests: `TestWorkflowContextReviseIteration` (drive the fixture
  through a `plan_review == 'revise'` loop fire so `plan` re-dispatches at
  `Iteration == 1`; assert `iteration 2 of 3` and the revise `State` line),
  `TestWorkflowContextGateRerun` (a command/agent gate loop → gate-failure
  phrasing), and `TestWorkflowContextFirstRun` (`Iteration == 0`, empty
  `rerunSource` → no `State`, no iteration clause).
- [x] 3.8 (audit FLAG-1) Add `TestWorkflowContextMultipleLoops`: a fixture with
  **two** loops whose `goto` both target the same step; fire them in sequence and
  assert the **last-fired** source drives `RerunReason` (last-write-wins on
  `rerunSource`). Hardens the multiple-loops-target-one-step case beyond the
  structural guarantee in task 3.4.
- [x] 3.7 Produce `03-proofs/3.0-revise-loop-preamble.txt` — the `plan` preamble on
  its revise iteration (topology from 2.8 plus the iteration clause and `State`
  line).

### [x] 4.0 `inject_context` opt-out toggle

Add `inject_context` to the schema as a `bool` on `[defaults]` (default `true`)
and a per-step `*bool` override on agent steps (nil = inherit, distinguishable
from explicit `false`, following the `[defaults]`-inheritance convention). When
the effective value is `false`, the engine skips `StepContext` assembly and
leaves `req.WorkflowContext == ""` (byte-identical to today's prompt). The
validator rejects, at load time, `inject_context` on `command`/`review` steps and
the contradiction of `[step.context]` together with `inject_context = false` on
the same step. Demoable: `jig validate` fails fast on a command-step
`inject_context`, and a `[defaults] = false` + per-step `= true` override
resolves to enabled. (Spec Unit 4.)

#### 4.0 Proof Artifact(s)

- CLI: `go run ./cmd/jig validate <fixture>` on a workflow with `inject_context`
  on a command step exits non-zero with a clear message; captured verbatim at
  `03-proofs/4.0-validate-error.txt`. Maps Unit 4 "reject on command/review".
- Test (valid + defaulting): `go test ./internal/workflow -run TestDecodeInjectContext`
  passes — `[defaults].inject_context = false` with a per-step
  `inject_context = true` override resolves to **enabled** for that step
  (defaulting/precedence direction per `docs/TESTING.md`).
- Test (invalid, non-agent): a `TestDecodeInvalid` row asserts the command/review
  rejection error substring.
- Test (invalid, contradiction): a `TestDecodeInvalid` row asserts the
  `[step.context]` + `inject_context = false` rejection error substring.
- Test (engine): `go test ./internal/engine -run TestBuildRequestInjectContextOff`
  passes — with the effective toggle `false`, `req.WorkflowContext == ""` and the
  assembled prompt equals the no-context baseline.

#### 4.0 Tasks

- [x] 4.1 In `schema.go`, add `InjectContext *bool` `toml:"inject_context"` to the
  `Step` agent-only field group (lines 169–194) and to `Defaults` (line 143).
  Doc-comment: parsed as `*bool` so "unset (inherit)" is distinguishable from
  explicit `false`; default is `true`. (Also defined `StepContextSpec` + the
  `Context *StepContextSpec` field here — task 4.4's contradiction check depends
  on the field existing; Unit 5 wires up its behavior.)
- [x] 4.2 In `load.go` `applyDefaults` (line 71), after the existing field
  propagation, resolve the effective toggle into an unexported `Step.injectContext`
  (step > `[defaults]` > true). Added `func (s *Step) InjectContextEnabled() bool`
  returning that effective value — the engine's read (unset ⇒ enabled).
  **Deviation from the literal wording:** the raw `*bool` is *not* overwritten with
  the default (that would erase the explicit/inherited distinction the validator
  needs); the effective value lives in a load-time field instead (mirrors the
  existing `agentPrompt` field). Required to honor the audit's Open-Question
  resolution (4.4). Recorded in `03-task-04-proofs.md`.
- [x] 4.3 In `validate.go`, reject `inject_context` on non-agent steps: in
  `checkCommand` (line 275) and `checkReview` (line 295), if `s.InjectContext != nil`
  emit `errf("step %q: inject_context is only valid on agent steps", s.ID)`. Added a
  `TestDecodeInvalid` row per the shared message.
- [x] 4.4 In `validate.go`, added a new `checkContext(s)` called from `checkAgent`,
  rejecting the contradiction: a `[step.context]` block present together with an
  explicit `inject_context = false` on the same step →
  `errf("agent step %q: [step.context] with inject_context = false is a contradiction (the block would be inert)", s.ID)`.
  **Decision recorded for the audit:** checks the *explicit* per-step value
  (`s.InjectContext != nil && !*s.InjectContext`), which survives because
  `applyDefaults` no longer collapses it, so an inherited-false step with a context
  block is not falsely flagged — see the Open Question in the audit.
- [x] 4.5 In `buildRequest` (Unit 2), gated assembly on `st.InjectContextEnabled()`:
  when it returns `false`, skip assembly and leave `req.WorkflowContext == ""`.
- [x] 4.6 Tests: `TestDecodeInjectContext` (valid — `[defaults].inject_context =
  false` + per-step `= true` resolves enabled, unset inherits disabled); two
  `TestDecodeInvalid` rows (non-agent; contradiction);
  `TestBuildRequestInjectContextOff` in the engine (effective `false` → empty
  context; sibling with toggle on stays non-empty).
- [x] 4.7 Added a small invalid fixture (command step with `inject_context`) and
  captured `go run ./cmd/jig validate <fixture>` output to
  `03-proofs/4.0-validate-error.txt`.

### [ ] 5.0 Optional `[step.context]` authoring block

Add an optional `[step.context]` table on agent steps with two string fields,
`purpose` (why this step exists) and `notes` (local free-form guidance); both
optional, an empty/absent block changes nothing. The engine injects the step's
own `purpose`/`notes` into its own preamble (Purpose/Notes lines) and propagates
each **neighbor's** declared `purpose` into this step's Upstream/Downstream lines
(a neighbor without a purpose stays graph-derived — never a guessed description).
The validator parses, type-checks, and tests both a valid `[step.context]` and an
invalid one (non-string field; plus the Unit 4 contradiction). Document
`[step.context]` as author-supplied context that *supplements, never replaces*,
the graph-derived framing. Demoable: a `[step.context].purpose` shows a
`Purpose:` line in the step's own preamble and annotates a consumer's Upstream
line. (Spec Unit 5.)

#### 5.0 Proof Artifact(s)

- Test (own + propagation): `go test ./internal/engine -run TestWorkflowContextPurposePropagation`
  passes — a step with `[step.context].purpose` renders a `Purpose:` line, and a
  downstream consumer's preamble renders that step's `purpose` on its Upstream
  line. Maps Unit 5 own-injection + neighbor-propagation FRs.
- Test (valid parse): `go test ./internal/workflow -run TestDecodeStepContext`
  passes — a valid `[step.context]` with `purpose` + `notes` parses and the
  values land on the parsed step.
- Test (invalid type): a `TestDecodeInvalid` row asserts a non-string
  `[step.context]` field is a load-time error substring.
- Proof artifact: `03-proofs/5.0-context-block-preamble.txt` — a preamble showing
  a propagated neighbor `purpose`.

#### 5.0 Tasks

- [ ] 5.1 In `schema.go`, define
  `type StepContextSpec struct { Purpose string \`toml:"purpose"\`; Notes string \`toml:"notes"\` }`
  and add `Context *StepContextSpec` `toml:"context"` to the `Step` agent-only
  group. Doc-comment: author-supplied context that *supplements, never replaces*,
  the graph-derived framing.
- [ ] 5.2 In `buildStepContext`, when `st.Context != nil` set `ctx.Purpose =
  st.Context.Purpose` and `ctx.Notes = st.Context.Notes`. When building each
  neighbor, set `neighbor.Purpose` from that neighbor step's `Context.Purpose`
  (propagation) when present; leave empty otherwise (graph-derived only — never a
  guess).
- [ ] 5.3 Confirm `Render()` (Unit 1) already emits the own `Purpose`/`Notes` lines
  and the neighbor purpose clause (upstream `— <purpose>`; downstream purpose
  *replaces* the derived clause). No new render code; assert via 5.5 tests.
- [ ] 5.4 In `validate.go`, extend `checkContext` (from 4.4) to reject a
  `[step.context]` on non-agent steps (shared with `inject_context`) and to surface
  a clear load-time error for a non-string `purpose`/`notes` (TOML type mismatch).
  Add the required rows.
- [ ] 5.5 Tests: `TestWorkflowContextPurposePropagation` (engine — own `Purpose:`
  line + a downstream consumer's preamble showing this step's `purpose` on its
  Upstream line); `TestDecodeStepContext` (valid parse lands `Purpose`/`Notes`); a
  `TestDecodeInvalid` row for a non-string field type.
- [ ] 5.6 Produce `03-proofs/5.0-context-block-preamble.txt` — a preamble showing a
  propagated neighbor `purpose`.

### [ ] 6.0 Docs, example alignment, ADR 0006, and skill-narration removal

Update the spec-of-record and prove the payoff. Add to `docs/workflow-schema.md`
a "Step context (engine-assembled)" section (always-on preamble + framing-only
guarantee), the `inject_context` field in the `[defaults]` and agent-step tables,
and the `[step.context]` block in the agent-step section. Exercise
`inject_context` (≥1 explicit per-step override) and a `[step.context]` block in
`examples/feature.toml`, keeping it valid, and refresh the top-of-file
"not-yet-in-schema" comments. Author **ADR 0006 — the engine assembles a
deterministic step-context preamble** (`docs/adr/`, mirroring the existing ADR
format; ADR 0005 is claimed by spec 02). Remove the hand-authored
upstream/downstream/loop narration from `.agents/skills/plan/SKILL.md` and
`.agents/skills/implement/SKILL.md`, keeping only *how to do the job*.
Behavior-preservation is a **manual human spot-check**, explicitly not an
automated equivalence gate. (Spec Unit 6.)

#### 6.0 Proof Artifact(s)

- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0 after the new
  fields are added; captured at `03-proofs/6.0-validate-ok.txt`. Maps Unit 6
  "example still validates".
- Diff: the `plan`/`implement` `SKILL.md` diffs showing the removed
  position/loop narration, at `03-proofs/6.0-skill-narration-removed.txt`.
- Behavioral spot-check: one real run of the `plan` step recorded both ways
  (old prose vs. new jig-assembled preamble) with a human note that orientation
  is preserved (or gaps found), at `03-proofs/6.0-preamble-spotcheck.md`. This is
  the acceptance evidence for the "behavior preserved" claim — a manual review,
  **not** an automated assertion.
- Docs: the new `docs/workflow-schema.md` sections and `docs/adr/0006-*.md`
  quoted/linked in the proofs.
- CLI (full regression): `go build ./... && go vet ./... && go test ./...` all
  pass, including the persistence-off engine/runner paths (spec Success Metric 6).

#### 6.0 Tasks

- [ ] 6.1 In `docs/workflow-schema.md`, add a "Step context (engine-assembled)"
  section describing the always-on preamble, its framing-only guarantee, and the
  rendered format (link/quote the spec). Add `inject_context` to the `[defaults]`
  and agent-step field tables and the `[step.context]` block to the agent-step
  section.
- [ ] 6.2 In `examples/feature.toml`, add at least one explicit per-step
  `inject_context` override and a `[step.context]` block (`purpose` + `notes`) on at
  least one agent step; refresh the top-of-file "features not yet in the schema /
  undocumented" comments. Keep `go run ./cmd/jig validate examples/feature.toml`
  green.
- [ ] 6.3 Author `docs/adr/0006-engine-assembles-step-context-preamble.md`,
  mirroring the structure and doc-comment style of `0001`–`0004`; record the
  decision (engine-owned deterministic preamble), the honored ADR 0003
  (extensibility in engine + schema; runner stays thin), and the rejected
  alternatives (author template, content injection, live-sibling status).
- [ ] 6.4 Remove the hand-authored upstream/downstream/loop narration from
  `.agents/skills/plan/SKILL.md` (the "you receive research from two agents" /
  "reviewer feedback present only when…" passages) and
  `.agents/skills/implement/SKILL.md` (the "QA feedback present only when…"
  passage), keeping only *how to do the job*. Capture the diff to
  `03-proofs/6.0-skill-narration-removed.txt`.
- [ ] 6.5 Capture `go run ./cmd/jig validate examples/feature.toml` (exit 0) to
  `03-proofs/6.0-validate-ok.txt`.
- [ ] 6.6 Run the `plan` step both ways (old prose vs. new preamble) and record the
  human orientation spot-check in `03-proofs/6.0-preamble-spotcheck.md` — explicitly
  a manual review, not an equivalence assertion.
- [ ] 6.7 Full regression: `gofmt -l -w . && go vet ./... && go test ./... -race`
  and the example `validate`; record that all pass (spec Success Metric 6).
- [ ] 6.8 (audit FLAG-2) Refresh `README.md`: correct the stale "Requires Go 1.24"
  to **1.25** (match `mise.toml`) and the stale "execution engine … Not built yet"
  status to reflect that the engine runs today (per `CLAUDE.md` "Current state").
  Doc-hygiene only, folded in alongside the Unit 6 docs work.
