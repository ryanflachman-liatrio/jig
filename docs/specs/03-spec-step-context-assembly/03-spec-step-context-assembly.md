# 03-spec-step-context-assembly.md

## Introduction/Overview

Today, an agent step's prompt is assembled by `buildAgentPrompt`
(`internal/runner/agent.go:428-472`) from four hand-authored or wired pieces:
the skill/agent body, `append_system_prompt`, the resolved `inputs`, and loop
`feedback`. **Nothing tells the agent where it sits in the workflow** — what ran
before it, what consumes its output, or why it is being (re-)run. That framing is
currently smuggled into the skill prose: `plan/SKILL.md` hand-writes *"You receive
research findings from two parallel research agents… Reviewer feedback — present
only when the human rejected a previous plan iteration,"* and `implement/SKILL.md`
hand-writes *"QA feedback — present only when QA failed and you've been looped
back."* Each skill author re-narrates the graph, the narration drifts from the
actual `.toml`, and the same skill can't be reused in a different workflow position.

This spec makes **jig** own that framing. The engine — which alone holds the DAG,
the step statuses, the structured results, and the loop state — will
**deterministically assemble a "Workflow context" preamble** describing the step's
position (upstream, downstream) and run state (loop iteration, revise/QA re-run
reason), and **prepend it to the agent's prompt** — the single user turn assembled
by `buildAgentPrompt` and sent via `client.Query` (jig passes no separate system
prompt to the SDK; `append_system_prompt` is itself concatenated into that user
turn). The preamble is a pure function of the static graph plus dispatch-time
state, so it is golden-string testable and adds no new graph edges — the
termination guarantee and static validation are untouched.

**Why no `block_on` round.** A step that pauses on `block_on` and then resumes
does *not* re-run `buildAgentPrompt`: the runner overrides the query with the
human's answer (`agent.go`, `query = req.Message`) and the SDK *continues the
existing conversation*, which already carries the preamble injected on the first
dispatch. A block_on resume therefore has nowhere to inject a fresh preamble and
would only duplicate orientation the agent already holds, so run-state framing is
scoped to loop re-runs (fresh dispatches that do re-run `buildAgentPrompt`) only.

The framing is **always graph-derived**; an **optional `[step.context]` block**
lets a workflow author enrich it with a human-authored `purpose` (why the step
exists — the one thing the graph can't derive) and per-step `notes`. A
`[defaults]`-level `inject_context` toggle (default `true`, per-step override) is
the single opt-out. This is the mirror image of an established pattern: jig
already imposes a deterministic *output* contract on every agent via `BaseSchema`
(`internal/workflow/base_schema.go`); this spec adds the deterministic *input/position*
contract.

Design decisions and their rationale are recorded in the
[Design Decisions & Rationale](#design-decisions--rationale) log at the end, and
warrant a new ADR (proposed **ADR 0006 — the engine assembles a deterministic
step-context preamble**) to be authored during implementation.

## Goals

- The engine assembles a **deterministic** "Workflow context" preamble for every
  agent step from the graph + dispatch-time state — identical `(graph, state)`
  yields byte-identical text, so it is unit-testable with golden strings.
- The preamble describes **upstream** (which dependencies completed and their
  status/purpose), **downstream** (which steps consume this step's output and
  how — human review vs. agent vs. command), and **run state** (loop iteration
  N of M, why this is a re-run).
- Workflow authors stop hand-narrating pipeline position in skills; the
  `.agents/skills/*/SKILL.md` position/loop narration is removed and the behavior
  is preserved by jig-assembled context.
- Authors keep an escape hatch: `inject_context = false` (in `[defaults]` or
  per-step) suppresses the preamble entirely for a step that wants none.
- Authors can optionally enrich the framing with a `[step.context]` block
  (`purpose`, `notes`) whose `purpose` propagates into neighbors' upstream/downstream
  lines — deterministic and author-owned, never agent-owned.
- The change is confined to the deterministic orchestration layer: the DAG,
  static validation, and termination guarantee are unchanged; no new edges, no new
  event-bus traffic.

## User Stories

**As a workflow author**, I want jig to tell each agent where it sits in the
pipeline, so I can write a skill that describes *how to do the job* without
re-narrating *what ran before and what comes next* — and reuse that skill in a
different workflow position unchanged.

**As a workflow author**, I want the position framing to stay in sync with the
`.toml` automatically, so editing `depends_on` or a loop never leaves a stale
hand-written description in a skill.

**As an agent (the consumer of the prompt)**, I want a consistent, engine-provided
statement of my upstream dependencies, my downstream consumers, and why I'm being
re-run, so I orient correctly on iteration 2 of a revise loop instead of assuming
I'm running fresh.

**As a workflow author with a step that must receive a bare prompt** (a raw
passthrough agent), I want to set `inject_context = false` so jig injects nothing.

**As a workflow author**, I want to attach a one-line `purpose` to a step so that
its downstream neighbors are told *why* that step ran, not just its id.

**As a maintainer**, I want the assembled preamble to be a pure, testable function
of the graph and run state, so I can lock its format with golden tests and trust it
never depends on live sibling timing.

## Demoable Units of Work

The units are dependency-ordered. Units 1–3 build the always-on, graph-derived
preamble end-to-end (the core value). Units 4–5 add the `inject_context` toggle and
the optional `[step.context]` authoring block. Unit 6 aligns the docs and example
and proves the payoff by deleting the now-redundant skill narration.

### Unit 1: `StepContext` data model & pure renderer

**Purpose:** A typed, engine-agnostic model of a step's workflow context and a pure
function that renders it to the delimited "Workflow context" section. No engine
wiring yet — pure data + rendering, so the format is locked by golden tests in
isolation.

**Functional Requirements:**
- The system shall define a `StepContext` data type (proposed home: `internal/step`,
  which is pure data and imports nothing) carrying: `WorkflowName`, `StepID`,
  `Purpose` (string), `Notes` (string), `Iteration` / `MaxIterations` (ints),
  `RerunReason` (string, e.g. review-revise or QA loop),
  `Upstream` (ordered `[]ContextNeighbor`), and `Downstream` (ordered
  `[]ContextNeighbor`). (There is no `block_on` field — see
  [Why no `block_on` round](#introductionoverview).)
- `ContextNeighbor` shall carry the neighbor's `ID`, its `Kind`
  (`agent`/`command`/`review`/`human`), an optional `Status` (upstream only), an
  optional `Purpose`, an optional `Conditional` clause (downstream only — the
  neighbor's `when` guard, when it has one), and, for downstream, the field(s) of
  this step it consumes (e.g. `summary`, `tasks`).
- The system shall provide a pure `Render() string` that produces the delimited
  section shown in [Rendered format](#rendered-format): a `## Workflow context`
  header, the step/iteration line, optional `Purpose`/`Notes`, an `Upstream`
  block, a `Downstream` block, an optional `State` line, and a trailing `---`
  delimiter.
- `Render()` shall omit absent parts gracefully: no `Purpose`/`Notes` line when
  empty, no `State` line on a first, non-re-run dispatch, no `Upstream`/`Downstream`
  block when the respective slice is empty. An empty `StepContext` (all fields zero)
  renders to `""` — but note this is a *pure-renderer* property: a live agent step
  always carries `WorkflowName` + `StepID`, so it always renders at least the
  position line ("You are step `x` in workflow `y`."). The `""` output is reached in
  practice only when the engine skips assembly entirely (`inject_context = false`,
  Unit 4). A pipeline-entry step with no upstream, no downstream, and no run state
  therefore renders just the position line plus the `---` delimiter — this is
  intentional; there is no suppression floor.
- Rendering shall be **deterministic**: neighbor ordering is fixed (upstream in the
  step's `depends_on` order; downstream in workflow declaration order); no map
  iteration leaks into output.

**Proof Artifacts:**
- Unit test (golden): a fully-populated `StepContext` (2 upstream, 2 downstream, a
  revise re-run at iteration 2/3, a `purpose`) renders byte-for-byte to a committed
  golden string; the same input renders identically across 100 iterations.
- Unit test: an empty `StepContext` renders to `""`; a topology-only `StepContext`
  (no state, no purpose) omits the `State`/`Purpose` lines.
- Proof artifact: the rendered golden section saved under
  `03-proofs/1.0-render-golden.txt`.

### Unit 2: Engine assembly of topology + runner prepend

**Purpose:** The engine populates the **upstream and downstream** (graph-derived)
parts of `StepContext` at dispatch, renders it, and hands the rendered string to the
runner on `StepRequest`; the runner prepends it before the skill body. Delivers the
always-on "before + after" framing end-to-end.

**Functional Requirements:**
- The system shall add a `WorkflowContext string` field to `engine.StepRequest`
  (`internal/engine/executor.go:16-40`), carrying the pre-rendered preamble (empty
  string when none).
- In `buildRequest` (`internal/engine/engine.go:832`), the engine shall build a
  `StepContext` for **agent** steps only, using the scheduler's `wf` (DAG),
  `states`, and step declaration order:
  - **Upstream** = the step's `depends_on`, each with its dispatch-time `Status`,
    rendered in `depends_on` order. The `Status` token is **retained** (not always
    `succeeded`): `depsReady` admits a `failed` dependency when it declared
    `on_failure = "continue"` (`engine.go:561-564`), so a dispatched step can
    legitimately see a `failed` upstream, and the preamble must tell the agent
    "your upstream `foo` failed but the run continued." (Absent `on_failure =
    continue`, a `failed`/`skipped` dep cascade-skips its dependents, so those
    steps never dispatch.)
  - **Downstream** = every step that lists this step in its `depends_on`, with its
    `Kind` (a `review` step → `human`; otherwise `agent`/`command`) and the consumed
    field(s) parsed from the consumer's `@thisstep.field` refs, rendered in workflow
    declaration order. (The "or references `@thisstep`" case is redundant: the
    validator enforces that any `@ref` input also appears in `depends_on`
    — `validate.go:320` — so the data edge and ordering edge are the same set.) When
    a downstream consumer declares a `when` guard, the engine shall record it as the
    neighbor's `Conditional` clause so the preamble can note the edge is conditional
    (see Unit 2's honesty note below).
- **Downstream honesty under guards.** The preamble is built from the *static*
  graph, so it lists every declared downstream consumer; whether a guarded consumer
  actually runs depends on runtime output and is unknowable at assembly (guard
  evaluation is deterministic but runs later). Rather than overstate ("your output
  feeds X") for a consumer that may be `when`-skipped, the renderer appends a
  deterministic "(conditional on `<guard>`)" clause — derived from the static `when`
  string — to any downstream line whose consumer has a guard. Unguarded edges render
  unchanged.
- The engine shall render the `StepContext` and set `req.WorkflowContext`; command
  and review steps shall leave it empty.
- `buildAgentPrompt` (`internal/runner/agent.go:428-472`) shall **prepend**
  `req.WorkflowContext` (when non-empty) ahead of the skill/agent body, separated by
  the renderer's `---` delimiter, preserving the existing order of the remaining
  four pieces (body → append → inputs → feedback). Note the assembled prompt is the
  agent's single **user turn** (sent via `client.Query`), not a system prompt — jig
  passes no separate system prompt to the SDK. The preamble prepends to the front of
  that user turn.
- Assembly shall be **framing-only**: the preamble references upstream by id +
  status (+ purpose in Unit 5) and points to the `@ref` inputs for content; it shall
  **not** inline any upstream artifact body or `summary`. It shall **not** include
  live status of non-dependency parallel siblings (they may be mid-flight —
  including them would be non-deterministic).
- Persistence-off safe: assembly and rendering are pure and require no run dir; when
  the preamble is empty the prompt is byte-identical to today's.

**Proof Artifacts:**
- Unit test (engine): for a fixture DAG, dispatching the `plan` step yields a
  `req.WorkflowContext` listing its three upstream deps with statuses and its two
  downstream consumers (`plan_review` → human review of `summary`; `implement` →
  agent consuming `tasks`).
- Unit test (runner): `buildAgentPrompt` with a non-empty `WorkflowContext` prepends
  it before the body with the `---` delimiter; with an empty `WorkflowContext` the
  output equals the current four-part prompt (regression lock).
- Proof artifact: `03-proofs/2.0-plan-preamble.txt` — the assembled preamble for the
  `plan` step of `examples/feature.toml`.

### Unit 3: Run-state framing (loops & re-runs)

**Purpose:** Populate the **state** part of `StepContext` so a re-run agent is told
it is iterating and why. Scoped to loop re-runs — the only re-dispatch that actually
re-runs `buildAgentPrompt` (block_on resume overrides the query and continues the
existing conversation; see [Why no `block_on` round](#introductionoverview)).

**Functional Requirements:**
- **Iteration line (0-indexed source, 1-indexed display).** `step.State.Iteration`
  is 0-indexed: a first run is `0` and each loop fire increments it
  (`engine.go`, `newIter := state.Iteration + 1`). The engine shall populate
  `Iteration` from `state.Iteration` and `MaxIterations` from the **firing loop's**
  `[step.loop].max_iterations` (see the loop-identity rule below). The renderer shall
  emit the iteration clause **only when `Iteration > 0`** (a genuine re-run) and
  render it as `iteration {Iteration + 1} of {MaxIterations}` — so the first re-run
  reads "iteration 2 of 3". A first run (`Iteration == 0`) omits the clause entirely.
- **RerunReason via recorded loop source.** A `[step.loop]` records only
  `when`/`goto`/`max_iterations`/`feedback` — it carries **no source-kind**, and at
  dispatch the engine otherwise knows only `stepFeedback[goto]` (the feedback
  *content*), not *which* loop fired. `fireLoop` (`engine.go:1349`), however, holds
  the firing source step id exactly where it already writes
  `stepFeedback[loop.Goto] = content`. The engine shall, at that same point, record
  the firing source in a new in-memory scheduler map (proposed `rerunSource
  map[string]string`, `goto → sourceStepID`; last-fired wins, which is correct
  per re-run event). At dispatch, `RerunReason` shall be derived from that source
  step's `Type`:
  - source is a `review` step → "re-running because `<src>` requested revisions";
  - source is an `agent`/`command` gate → "re-running because the `<src>` gate
    reported a failure".
  The phrase shall direct the agent to the reviewer/QA feedback already present in
  its inputs (`req.Feedback` / `@loopsource` refs). This handles the
  multiple-loops-target-one-step case deterministically: only one loop fires per
  re-run, so the last write to `rerunSource[goto]` names the loop that caused *this*
  dispatch.
- These fields shall be derived from existing scheduler state (`states`,
  `stepFeedback`) plus the new **in-memory** `rerunSource` bookkeeping — **no new
  persisted (on-disk) state and no new events.** The `rerunSource` map lives in the
  scheduler exactly like `stepFeedback` already does; it is never persisted.
- The `State` line shall be **omitted** on a first run (`Iteration == 0` and not a
  loop re-run).

**Proof Artifacts:**
- Unit test: a step at `Iteration == 1` under a `max_iterations = 3` review-revise
  loop renders `iteration 2 of 3` and a re-run reason naming the review verdict; a
  step re-run by an `agent`/`command` gate renders the gate-failure phrasing.
- Unit test: a first-run step (`Iteration == 0`, no loop source) renders no `State`
  line and no iteration clause.
- Proof artifact: `03-proofs/3.0-revise-loop-preamble.txt` — the `plan` preamble on
  its second (revise) iteration.

### Unit 4: `inject_context` opt-out toggle

**Purpose:** A single, deterministic opt-out. Default on; author can disable per
workflow or per step.

**Functional Requirements:**
- The schema shall add `inject_context` as a `bool` on `[defaults]` (default `true`)
  and as a per-step override on agent steps, parsed as `*bool` so "unset" (inherit
  default) is distinguishable from explicit `false` — following the existing
  `[defaults]`-inheritance convention.
- When the effective value is `false`, the engine shall skip `StepContext` assembly
  and leave `req.WorkflowContext == ""` (the prompt is byte-identical to today's).
- The validator shall reject `inject_context` on `command`/`review` steps (they
  have no assembled preamble) with a load-time error and a test.
- The validator shall reject declaring a `[step.context]` block together with
  `inject_context = false` on the same step (a contradiction — the block would be
  inert), failing at parse time per jig's "fail at parse time" goal, with a test.

**Proof Artifacts:**
- CLI: `go run ./cmd/jig validate` on a fixture with `inject_context` on a command
  step exits non-zero with a clear message (`03-proofs/4.0-validate-error.txt`).
- Unit test (workflow): valid path — `[defaults].inject_context = false` with a
  per-step `inject_context = true` override resolves to enabled for that step;
  invalid paths — the two rejections above.
- Unit test (engine): with the effective toggle `false`, `req.WorkflowContext` is
  `""` and the assembled prompt equals the no-context baseline.

### Unit 5: Optional `[step.context]` authoring block

**Purpose:** Let an author enrich the always-derived framing with a human-authored
`purpose` (propagated to neighbors) and per-step `notes` (local) — the optional
"user-defined context" layered on top of the graph-derived context.

**Functional Requirements:**
- The schema shall add an optional `[step.context]` table on agent steps with two
  string fields: `purpose` (why this step exists) and `notes` (free-form guidance
  for this step only). Both optional; an empty/absent block changes nothing.
- The engine shall inject the step's own `purpose` and `notes` into its own
  preamble (Purpose/Notes lines), and shall propagate each **neighbor's** declared
  `purpose` into this step's Upstream/Downstream lines (so a downstream `review`
  neighbor's purpose annotates the "who consumes your output" line). A neighbor
  without a `purpose` renders graph-derived only (id + kind + status), never a
  guessed description.
- The validator shall parse, type-check, and (per the `internal/workflow` house
  rules) test both a valid `[step.context]` and an invalid one (e.g. a non-string
  field, or the `inject_context = false` contradiction from Unit 4).
- `[step.context]` shall be documented as author-supplied context that supplements,
  never replaces, the graph-derived framing.

**Proof Artifacts:**
- Unit test: a step with `[step.context].purpose` renders a `Purpose:` line; a
  downstream consumer's preamble renders that step's `purpose` on its Upstream line.
- Unit test (validation): valid `[step.context]` parses; an invalid field type is a
  load-time error.
- Proof artifact: `03-proofs/5.0-context-block-preamble.txt` — a preamble showing a
  propagated neighbor `purpose`.

### Unit 6: Docs, example alignment, and skill-narration removal

**Purpose:** Update the spec-of-record, exercise the new fields in the kitchen-sink
example, and demonstrate the payoff by deleting the now-redundant workflow-position
narration from the pipeline skills.

**Functional Requirements:**
- `docs/workflow-schema.md` shall gain: a "Step context (engine-assembled)" section
  describing the always-on preamble and its framing-only guarantee; the
  `inject_context` field in the `[defaults]` and agent-step field tables; and the
  `[step.context]` block in the agent-step section.
- `examples/feature.toml` shall exercise `inject_context` (at least one explicit
  per-step override) and a `[step.context]` block on at least one step, and shall
  still pass `go run ./cmd/jig validate examples/feature.toml`. The top-of-file
  "features not yet in the schema / undocumented" comments shall be updated to
  reflect the new capability.
- The `.agents/skills/plan/SKILL.md` and `.agents/skills/implement/SKILL.md` bodies
  shall have their hand-authored upstream/downstream/loop narration (the "you
  receive research from two agents" / "reviewer feedback present only when…" /
  "QA feedback present only when…" passages) **removed**, relying on the
  jig-assembled preamble instead; the skills retain only *how to do the job*.

**Behavior-preservation is validated by manual review, not an automated gate.** The
diff and `validate` proofs below show the narration was *removed* and the workflow
still loads — they do **not**, and cannot cheaply, prove the assembled preamble
orients the agent as well as the deleted prose (the agent is non-deterministic; an
automated equivalence test would be flaky and expensive). Acceptance for the
behavioral claim is therefore a documented human spot-check, recorded as a proof
note.

**Proof Artifacts:**
- CLI: `go run ./cmd/jig validate examples/feature.toml` exits 0 after the fields
  are added (`03-proofs/6.0-validate-ok.txt`).
- Diff: the `plan`/`implement` `SKILL.md` diffs showing the removed position/loop
  narration (`03-proofs/6.0-skill-narration-removed.txt`).
- **Behavioral spot-check:** one real run of the `plan` step recorded both ways —
  old prose vs. new jig-assembled preamble — with a human noting that orientation is
  preserved (or the gaps found), saved under
  `03-proofs/6.0-preamble-spotcheck.md`. This is the acceptance evidence for the
  "behavior preserved" claim; it is a manual review, explicitly not an automated
  equivalence assertion.
- Docs: the new `docs/workflow-schema.md` sections rendered/quoted in the proofs.

## Non-Goals (Out of Scope)

1. **Author-supplied templates for the preamble.** The format is fixed and
   engine-owned (consistency and determinism are the point). A `context_template`
   string was explicitly rejected — it would reintroduce the per-workflow narration
   divergence this feature removes.
2. **Injecting upstream *output content* into the preamble.** The preamble is
   framing/metadata only (ids, statuses, kinds, declared purposes). Delivering an
   upstream step's actual output remains the job of the existing `inputs` / `@ref`
   mechanism; the preamble must not inline or duplicate artifact bodies or summaries.
3. **Live sibling/parallel-step status.** Only completed `depends_on` upstream and
   statically-known downstream are described. Non-dependency siblings may be
   mid-flight; including them would make the preamble non-deterministic.
4. **Context for command or review steps.** Command steps have no prompt; review
   steps render to a human. `inject_context` and `[step.context]` are agent-only.
5. **New event-bus or transcript protocol.** The preamble is part of the agent
   prompt (file-is-truth); it is not streamed as a distinct engine event and adds
   nothing to `journal.jsonl`.
6. **A new TUI surface for the preamble.** If the injected prompt appears in the
   transcript, that is incidental; no new panel, gate, or rendering path is added.
7. **Changing `depends_on`, guards, loops, or the termination guarantee.** No new
   graph edges; the preamble is a read-only projection of the existing graph.

## Design Considerations

This is a non-TUI feature (engine + runner + schema + docs); there are no new visual
elements. The only user-visible artifact is the text preamble prepended to the agent
prompt.

### Rendered format

This **exact** byte layout is locked by Unit 1's golden test. The block below is the
committed golden `03-proofs/1.0-render-golden.txt` — a fully-populated `StepContext`
that exercises every render branch (own `purpose` + `notes`, an upstream with a
purpose and one without, a `succeeded` and a `failed` upstream status, a downstream
with a propagated purpose and one that is graph-derived + guarded, an iteration
clause, and a `State` line). It is not illustrative — it is the assertion target.

```
## Workflow context

You are step `plan` in workflow `feature` (iteration 2 of 3).
Purpose: turn research findings into an ordered implementation plan.
Notes: prefer the smallest change that satisfies the acceptance criteria.

Upstream (already complete):
- `intake` (succeeded) — classify the request and extract research areas
- `research_backend` (failed)
These reach you as the inputs listed below; this section is orientation only.

Downstream (what your output feeds):
- `plan_review` (human review) — a person reviews your summary and approves or requests revisions
- `implement` (agent) — consumes your `tasks` (conditional on `approved == true`)

State: re-running because `plan_review` requested revisions on the previous iteration. Address the reviewer feedback in your inputs.

---
```

The same rules applied to the real `plan` step of `examples/feature.toml` (before
any `[step.context]` authoring) produce the two other committed goldens:
`03-proofs/2.0-plan-preamble.txt` (first run — no iteration clause, no `State`, and
graph-derived neighbor lines) and `03-proofs/3.0-revise-loop-preamble.txt` (the
`plan_review`-triggered revise iteration — same topology plus the iteration clause
and `State` line).

#### Exact rendering algorithm

`Render()` emits these parts in order, joining with single blank lines exactly as
shown above; a part contributes **nothing** (not even its blank line) when absent.
`Render()` returns `""` when `StepID == ""` (the zero value). The returned string
ends at `---` with **no trailing newline**, so `buildAgentPrompt` appends `"\n\n"`
before the skill body and the committed `.txt` goldens are byte-identical to
`Render()`'s output.

1. **Header:** the literal line ``## Workflow context``, then a blank line.
2. **Position line:** ``You are step `<StepID>` in workflow `<WorkflowName>``` then,
   only when `Iteration > 0`, `` (iteration <Iteration+1> of <MaxIterations>)``, then
   a terminating `.`. (0-indexed source; 1-indexed display.)
3. **Purpose line** (only if `Purpose != ""`): `Purpose: <Purpose>` (verbatim; the
   renderer adds no punctuation).
4. **Notes line** (only if `Notes != ""`): `Notes: <Notes>` (verbatim).
5. **Upstream block** (only if `len(Upstream) > 0`), preceded by a blank line: the
   header `Upstream (already complete):`, then one bullet per neighbor in
   `depends_on` order — ``- `<ID>` (<Status>)`` plus `` — <Purpose>`` only when the
   neighbor declares a purpose — then the fixed trailer line `These reach you as the
   inputs listed below; this section is orientation only.`
6. **Downstream block** (only if `len(Downstream) > 0`), preceded by a blank line:
   the header `Downstream (what your output feeds):`, then one bullet per consumer in
   **workflow declaration order** — ``- `<ID>` (<KindLabel>) — <Clause>`` — plus, when
   the consumer has a `when` guard, `` (conditional on `<guard>`)``. `KindLabel` is
   `human review` for a `review`/`human` neighbor, else `agent` or `command`.
   `Clause` is the neighbor's declared `purpose` verbatim when present (it *replaces*
   the derived clause); otherwise the graph-derived clause: `a person reviews your
   <fields>` for a review/human consumer, else `consumes your <fields>`. `<fields>`
   is the consumed field name(s) in backticks, in the consumer's input-declaration
   order (``tasks``; ``tasks`, `approach``), or the literal `output` (no backticks)
   for a bare `@thisstep` reference with no field.
7. **State line** (only if a loop re-run), preceded by a blank line: `State: ` +
   the engine-composed `RerunReason` (a complete, pre-punctuated string that already
   contains its follow-up sentence, e.g. `…requested revisions on the previous
   iteration. Address the reviewer feedback in your inputs.`).
8. **Delimiter**, preceded by a blank line: the literal line `---` (no trailing
   newline).

Notes on the algorithm above, and the provenance of each optional part:

- The `Purpose`/`Notes` lines carry the step's own `[step.context].purpose`/`notes`
  (Unit 5); a neighbor's `— <purpose>` clause carries *that neighbor's* declared
  purpose, propagated in. Absent a purpose, neighbor lines are graph-derived only —
  never a guessed description.
- The upstream `(<status>)` token is `(succeeded)` in the normal case but can read
  `(failed)` when the dep declared `on_failure = "continue"` and the run proceeded
  past its failure — the case that justifies keeping the token (Unit 2).
- The `State:` line appears only for a loop re-run; there is no `block_on` state (see
  [Why no `block_on` round](#introductionoverview)).
- The `---` delimiter separates jig-owned framing from the skill body that follows.
- **Open wording choices baked into these goldens** (reversible now, sticky once the
  golden test lands): the block header (`## Workflow context`), the section headers
  (`Upstream (already complete):` / `Downstream (what your output feeds):`), the
  fixed upstream trailer sentence, and the decision that a neighbor `purpose`
  *replaces* rather than *augments* the derived downstream clause. Flag any you want
  changed before Unit 1 is implemented.

## Repository Standards

- **Schema additions are exhaustive and load-time** (CLAUDE.md): `inject_context`
  and `[step.context]` must each be parsed, defaulted (inherited from `[defaults]`
  where applicable), and validated, with a table-driven test for **both** the valid
  and the invalid path (`internal/workflow/workflow_test.go` house style with inline
  TOML strings).
- **`internal/` packages are the unit of design.** The `StepContext` data type
  belongs with pure data (`internal/step`, which imports nothing); graph traversal
  and assembly belong in `internal/engine` (which owns the scheduler state);
  rendering is a pure function testable without a running engine. The runner stays
  thin — it prepends a pre-rendered string, mirroring how it already consumes
  `req.Feedback` and `req.Inputs`.
- **Comments explain the non-obvious "why."** The determinism constraint (fixed
  ordering, no map iteration, framing-only) is exactly the kind of subtlety that
  earns a comment.
- **Persistence-off is a first-class path.** Assembly is pure and needs no run dir;
  verify the engine/runner tests that exercise the no-run-dir path still pass and
  that an empty preamble yields a byte-identical prompt.
- **Examples are documentation.** Keep `examples/feature.toml` valid after the
  schema change (`go run ./cmd/jig validate examples/feature.toml`).
- **Mirror `BaseSchema`.** `internal/workflow/base_schema.go` is the precedent for a
  deterministic contract jig imposes on every agent; follow its structure and
  doc-comment style for the always-on input/position contract.

## Technical Considerations

**Where the code changes land (confirmed by read-only exploration):**

- **`internal/engine/executor.go:16-40`** — add `WorkflowContext string` to
  `StepRequest`.
- **`internal/engine/engine.go:832` (`buildRequest`)** — assemble the `StepContext`
  for agent steps from `scheduler.wf` (DAG + declaration order), `scheduler.states`
  (upstream statuses, `Iteration`), `scheduler.stepFeedback`, and the new
  `scheduler.rerunSource` map (the firing loop's source step id, recorded in
  `fireLoop` at `engine.go:1349` beside the existing `stepFeedback[loop.Goto]`
  write) for the re-run reason; render it; set `req.WorkflowContext`. Downstream
  edges come from scanning every step's `DependsOn` for the current step (the `@ref`
  set is a subset of `DependsOn` per `validate.go:320`), reading each consumer's
  `@thisstep.field` refs for the consumed fields and its `When` guard for the
  conditional clause.
- **`internal/runner/agent.go:428-472` (`buildAgentPrompt`)** — prepend
  `req.WorkflowContext` (when non-empty) before `req.Step.AgentPrompt()`, with the
  `---` delimiter, at the front of the assembled **user turn** (`buildAgentPrompt`
  builds the string sent via `client.Query`; there is no separate system prompt).
  The existing provenance-label helper (`resolvedInputLabel`, `agent.go:474-489`) is
  the style reference for engine-authored prompt text. Note: this prepend affects
  fresh dispatches only — the resume path (`agent.go`, `query = req.Message` when
  `ResumeSessionID != ""`) intentionally bypasses `buildAgentPrompt`, which is why
  block_on resumes carry no preamble.
- **`internal/step`** — the `StepContext` / `ContextNeighbor` data types and the
  pure `Render()` (pure data, imports nothing; keeps the format independently
  testable). `step.State` (`internal/step/step.go:27-49`) already carries
  `Iteration`/`Attempt`, the source for the iteration line.
- **`internal/workflow/schema.go` (Step struct ~156-219; Input ~245-253)** — add
  `InjectContext *bool` and a `Context *StepContextSpec` (fields `Purpose`, `Notes`).
- **`internal/workflow/validate.go`** — the new rejections (Units 4–5): toggle on
  non-agent steps; `[step.context]` with `inject_context = false`; field type
  checks.
- **`internal/workflow/base_schema.go`** — no code change, but the structural and
  doc-comment precedent for a jig-imposed deterministic contract.
- **`docs/workflow-schema.md`, `examples/feature.toml`, `.agents/skills/*`** — Unit 6.

**Determinism & termination.** The preamble is a pure function of the static graph
plus dispatch-time state; it introduces no new edges, guards, or loops, so static
validation, visualization, and the termination guarantee are unaffected. Fixed
neighbor ordering and no map-iteration-into-output are required for golden tests.

**No external standards research needed.** This is an internal prompt-composition
change with no third-party technology surface; no latest-standards research was
required.

## Security Considerations

- The preamble is assembled entirely from local, in-process data: the workflow
  graph, step ids/statuses/kinds, and author-supplied `purpose`/`notes`. No
  credentials, no network calls, no secrets.
- **Framing-only bounds the blast radius.** Because the preamble carries statuses,
  ids, kinds, and declared purposes — never raw upstream artifact bodies or
  `summary` text — it cannot accidentally leak large or sensitive upstream *content*
  into a downstream agent's prompt. Content still flows only through the
  explicit `@ref` inputs the author wired.
- `purpose`/`notes` are author-controlled TOML; they are no more sensitive than any
  other text an author already puts in a skill or `append_system_prompt`.
- Proof artifacts under `03-proofs/` are rendered preambles from the example
  workflow; they contain no secrets and are safe to commit.

## Success Metrics

1. **Determinism:** the render golden test passes and identical `(graph, state)`
   input yields byte-identical output across repeated runs (Unit 1).
2. **End-to-end injection:** for `examples/feature.toml`, the `plan` step's agent
   prompt begins with a "Workflow context" section naming its 3 upstream deps and 2
   downstream consumers; on a revise iteration it states `iteration 2 of 3` and the
   re-run reason (Units 2–3).
3. **Opt-out works:** a step with the effective `inject_context = false` produces a
   prompt byte-identical to the pre-feature baseline (Unit 4).
4. **Authoring layer:** a `[step.context].purpose` appears in the step's own preamble
   and propagates to a consumer's Upstream line (Unit 5).
5. **Payoff:** `plan/SKILL.md` and `implement/SKILL.md` no longer contain
   hand-authored upstream/downstream/loop narration; the removed passages are
   captured in the Unit 6 diff, and `go run ./cmd/jig validate examples/feature.toml`
   still exits 0.
6. **No regressions:** `go build ./...`, `go vet ./...`, and `go test ./...` pass,
   including the persistence-off engine/runner tests.

## Design Decisions & Rationale

Resolved with the user during spec intake on 2026-08-06, then refined in a
code-grounded design review the same day (see [Refinements from design
review](#refinements-from-design-review-2026-08-06) below). The body reflects both;
this log preserves the rationale and rejected alternatives. Recommend recording the
architectural decision as **ADR 0006 — the engine assembles a deterministic
step-context preamble** during implementation (ADR 0005 is claimed by spec 02).

1. **Assembly lives in the engine; the runner stays thin.** Only the scheduler
   owns the DAG, step statuses, structured results, and loop state, so context can
   only be assembled there (confirmed by exploration). The engine renders the
   preamble to a string and passes it via `StepRequest.WorkflowContext`; the runner
   just prepends it — mirroring how it already consumes `req.Feedback`. This honors
   ADR 0003 (extensibility in engine + schema; consumers stay thin).

2. **Always graph-derived; `[step.context]` optionally enriches.** (Q1.) Rejected a
   purely-derived design (framing would be mechanical — ids and field names only)
   and a bare top-level `purpose` field. Chosen: jig always derives the structural
   framing from the graph, and an **optional** `[step.context]` block layers
   human-authored `purpose`/`notes` on top. `purpose` is the one thing the graph
   can't derive and it propagates to neighbors — while staying in the deterministic
   TOML layer, never authored by the agent.

3. **Scope is before + after + state.** (Q2.) The user's request named all three;
   the preamble describes upstream, downstream, and run state. Rejected the smaller
   "before + state" and "state-only" slices as not matching the ask.

4. **Control is a single opt-out toggle, not a template.** (Q3.) `inject_context`
   defaults `true` in `[defaults]` with a per-step override; parsed as `*bool` to
   distinguish inherit from explicit `false`. Rejected an author template string
   (Non-Goal 1) — it would recreate the per-workflow narration divergence this
   feature exists to eliminate.

5. **Prepend to the agent's prompt.** (Q4.) The preamble is orientation, so it reads
   first, ahead of the skill body, delimited by `---`. Rejected appending (lower
   prominence) and a synthetic `context.md` input file (the agent might never read
   it). It rides at the front of the single **user turn** that `buildAgentPrompt`
   assembles and `client.Query` sends — jig passes no separate system prompt to the
   SDK (`append_system_prompt` is itself concatenated into that user turn), so "the
   agent's prompt" means that user turn, not an SDK system field.

6. **Framing-only, never content, never live siblings.** The preamble carries ids,
   statuses, kinds, and declared purposes — not upstream artifact bodies (that stays
   with `@ref` inputs, Non-Goal 2) and not non-dependency sibling status (which may
   be mid-flight and would break determinism, Non-Goal 3). This bounds both prompt
   bloat and the security blast radius.

7. **Contradictions fail at parse time.** Declaring `[step.context]` together with
   `inject_context = false`, or `inject_context` on a non-agent step, is a load-time
   error — consistent with jig's "fail at parse time, not run time" goal.

### Refinements from design review (2026-08-06)

A code-grounded review (three read-only explorations of `internal/runner`,
`internal/engine`, `internal/workflow`) corrected several assumptions the intake
spec had made about the runtime. Each refinement below is now reflected in the body.

8. **The preamble rides in the user turn, not a "system prompt."** Exploration of
   `agent.go` established that `buildAgentPrompt` builds a single string sent as the
   agent's first **user** message via `client.Query`; jig passes no system prompt to
   the SDK and `append_system_prompt` is concatenated into that same string. Every
   "system prompt" reference was reworded; the prepend mechanism is unchanged.

9. **`block_on` framing was dropped.** On a `block_on`/gate resume the runner
   overrides the query (`query = req.Message`) and the SDK continues the existing
   conversation — so `buildAgentPrompt` never re-runs and the agent already holds the
   preamble from its first dispatch. A `block_on` round was therefore both
   unreachable via the specced path and redundant; the `BlockRound` field and its
   FR/proof were removed and Unit 3 is scoped to loop re-runs (fresh dispatches).

10. **`RerunReason` derives from a recorded loop source.** A `[step.loop]` carries no
    source-kind and multiple loops may target one step; at dispatch the engine knows
    only the feedback *content*. The fix records the firing loop's source step id in
    a new **in-memory** scheduler map (`rerunSource`) at the existing `fireLoop`
    write site, then derives the reason from the source step's `Type` (review →
    "requested revisions"; agent/command → "gate reported a failure"). Unit 3's "no
    new persisted state" was clarified to "no new *persisted (on-disk)* state" — the
    map is in-memory, exactly like `stepFeedback`.

11. **Iteration is 0-indexed; display is 1-indexed and re-run-only.**
    `state.Iteration` starts at `0` and increments per loop fire, so the clause is
    emitted only when `Iteration > 0` and rendered as `iteration {Iteration+1} of M`.
    The intake wording "Iteration 1 with no loop omits the clause" was corrected.

12. **Downstream detection is `depends_on`-only; guards are annotated.** The
    validator enforces that any `@ref` input also appears in `depends_on`
    (`validate.go:320`), so the intake's "`depends_on` **or** `@ref`" union collapses
    to `depends_on`. Because a downstream consumer may be `when`-skipped at runtime
    (unknowable at assembly), a guarded downstream edge gains a deterministic
    "(conditional on `<guard>`)" clause rather than overstating that the output feeds
    it.

13. **The upstream status token is retained deliberately.** The review considered
    dropping it as constant, but `depsReady` admits a `failed` dependency when it
    declared `on_failure = "continue"` (`engine.go:561-564`), so a dispatched step
    can genuinely see a `failed` upstream — high-signal orientation the agent needs.
    The token stays.

14. **Behavior preservation (Unit 6) is a manual spot-check, not an automated gate.**
    The diff + `validate` proofs show the narration was removed and the workflow
    still loads; they cannot prove behavioral equivalence (the agent is
    non-deterministic). Acceptance is a documented human spot-check
    (`03-proofs/6.0-preamble-spotcheck.md`), stated as such rather than dressed up as
    an equivalence assertion.

## Open Questions

1. **Exact wording of the rendered lines** (e.g. "Upstream (already complete):" vs.
   "Inputs from earlier steps:") is non-blocking — the *structure* and determinism
   are fixed by Unit 1's golden test; the prose can be refined during
   implementation review without changing scope or any acceptance criterion.
2. **Whether `[step.context]` should also allow a short `role` distinct from
   `purpose`** is deferred; `purpose` + `notes` covers the stated need, and a third
   field can be added later without breaking the model. Non-blocking.
