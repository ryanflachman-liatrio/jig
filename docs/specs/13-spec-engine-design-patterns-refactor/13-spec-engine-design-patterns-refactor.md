# 13-spec-engine-design-patterns-refactor.md

## Introduction/Overview

`internal/engine/engine.go` is the workflow scheduler/executor — the actor loop
that drives a run's DAG, dispatches steps, and mutates step state in response
to a single-threaded stream of internal messages (`schedMsg`). At ~2,900
lines it has grown several large, ad-hoc constructs that already resemble
classic design patterns but aren't named or structured as such: a 200-line
type-switch dispatcher (`handle`), an existing but informal chain-of-handlers
(`postExecChain`), an event fan-out (`Subscribe`/`fanOutLive`), and a snapshot
mechanism (`RunSnapshot`) used for both live queries and replay.

This feature refactors `engine.go` by identifying every one of the 23 classic
Gang-of-Four design patterns that has a genuine, load-bearing fit in this
file, applying it to make the corresponding responsibility explicit and
testable, and producing a written audit that accounts for all 23 patterns —
including a documented rationale for each pattern that does **not** apply.
The goal is a more readable, more testable scheduler, not pattern-count
maximalism: a pattern is only introduced where it replaces real
accidental complexity (a long type-switch, a branchy strategy selection, an
informal chain) with a named structure that a future contributor recognizes
immediately.

## Goals

- Replace the `scheduler.handle()` monolithic type-switch with an explicit
  **Command** dispatch so each `schedMsg` type owns its own execution logic.
- Make step-type dispatch (agent/command/review) and failure-policy selection
  explicit **Strategy** implementations instead of inline branching.
- Formalize the existing informal chain-of-handlers (`postExecChain`) and
  event fan-out (`Subscribe`/`fanOutLive`/`fanOutCtrl`) as named **Chain of
  Responsibility** and **Observer** patterns respectively, with no behavior
  change.
- Produce a single audit document mapping all 23 GoF patterns to either
  "Applied — where and why" or "Not applicable — why forcing it would harm
  the design," so the refactor's pattern coverage is traceable and
  reviewable.
- Preserve `internal/engine`'s existing package-level behavior: every
  existing test in `engine_test.go`, `integration_test.go`,
  `worktree_test.go`, `stop_test.go`, `reset_test.go`, `recovery_test.go`,
  `replay_test.go`, `journal_test.go`, `loop_coalesce_test.go`, and
  `question_race_test.go` must pass, updated only for public API renames —
  never for behavior differences.

## User Stories

- **As a jig maintainer**, I want `scheduler.handle()`'s dispatch logic split
  into one method per message type so that I can find and modify the
  handling of a single message kind (e.g. `stopMsg`) without reading a
  200-line switch statement.
- **As a jig maintainer**, I want step-type dispatch (`agent` vs `command` vs
  `review`) and failure-policy selection expressed as named strategies so
  that adding a new step type or failure policy is a matter of adding one
  implementation, not editing a shared branchy function.
- **As a new contributor**, I want a written map from GoF patterns to
  concrete code in `internal/engine` (including patterns deliberately not
  used) so that I can understand the design vocabulary of the scheduler
  without reverse-engineering it from the diff.
- **As a jig maintainer**, I want the refactor to leave existing engine
  behavior provably unchanged (via the existing test suite) so that this is
  a structural clean-up, not a behavior-risking rewrite.

## Demoable Units of Work

### Unit 1: Command Pattern for Scheduler Message Dispatch

**Purpose:** Replace the single ~200-line type-switch in `scheduler.handle()`
(engine.go:1408-1609) with a `Command` interface so each `schedMsg` type
(`stepDoneMsg`, `verdictMsg`, `userInputMsg`, `stopMsg`, `resumeMsg`,
`resetMsg`, `recoverMsg`, `resolveIntegrationMsg`, `finalMergeMsg`,
`humanMessageMsg`, `agentInputMsg`, `agentQuestionNotifyMsg`,
`agentQuestionAnswerMsg`, `securityFindingMsg`, `snapshotReqMsg`,
`closureReqMsg`) implements its own `execute(s *scheduler)` (or equivalent)
method, invoked polymorphically instead of via `switch m := msg.(type)`.

**Functional Requirements:**
- The system shall dispatch every `schedMsg` value received by
  `scheduler.run`'s select loop by invoking a method on the message itself
  (or a command wrapper around it), not via a central type-switch.
- The system shall preserve the exact existing side effects and ordering for
  every message type currently handled in `handle()`, including the
  early-return branches for `StatusAwaitingRecovery` and `stopping[stepID]`
  inside `stepDoneMsg` handling.
- The system shall keep each command's logic in a location no larger than
  the current largest single `case` branch's line count, measured after the
  split (i.e., no case's logic may grow — it may only be extracted into its
  own named unit).
- The system shall continue to route through the existing named handler
  methods (`handleStop`, `handleResume`, `handleReset`, `handleRecover`,
  `handleResolveIntegration`, `handleFinalMerge`, `handleSecurityFinding`,
  `handleHumanMessage`, `handleAgentInput`) rather than duplicating their
  logic inline.

**Proof Artifacts:**
- Test: `go test ./internal/engine/...` passes with zero behavior changes,
  demonstrating the Command refactor preserved scheduler semantics.
- Code: `git diff --stat internal/engine/engine.go` combined with a
  side-by-side of old `handle()` vs the new dispatch entry point demonstrates
  the type-switch was eliminated in favor of per-command execution.

### Unit 2: Strategy Pattern for Step Dispatch and Failure Policy

**Purpose:** Formalize two branchy decision points as explicit `Strategy`
implementations: (a) `scheduler.dispatch()` (engine.go:1143), which currently
branches on `wfStep.Type` to decide how to run an agent/command/review step,
and (b) `scheduler.applyFailurePolicy()` (engine.go:1613), which branches on
`workflow.FailAbort`/other `OnFailure` policy values.

**Functional Requirements:**
- The system shall select the step-type dispatch behavior (agent, command,
  review) via a strategy looked up by `workflow.Step.Type`, rather than an
  inline `if`/`switch` chain in `dispatch()`.
- The system shall select the failure-handling behavior via a strategy
  looked up by the step's resolved `OnFailure` policy, rather than inline
  branching in `applyFailurePolicy()`.
- The system shall support adding a new step type or a new failure policy by
  adding one new strategy implementation, without modifying the dispatch or
  failure-policy selection code path itself.
- The system shall preserve all existing dispatch and failure-policy
  behavior verified by `engine_test.go`, `worktree_test.go`, and
  `recovery_test.go`.

**Proof Artifacts:**
- Test: existing `TestDispatch*`-style and failure-policy test cases in
  `engine_test.go`/`recovery_test.go` pass unchanged (behaviorally), showing
  strategy selection is behaviorally equivalent to the prior branching.
- Code: a short table or constructor (e.g. a map from `workflow.StepType` to
  strategy, and from `workflow.FailurePolicy` to strategy) demonstrates the
  new extension point.

### Unit 3: Formalize Existing Chain of Responsibility, Observer, and Memento Usages

**Purpose:** `engine.go` already contains three informal pattern
applications that work correctly but aren't named or structured
consistently with the rest of the refactor: `postExecChain` (a
chain-of-handlers run in `handle(stepDoneMsg)`), the `Subscribe`/
`fanOutLive`/`fanOutCtrl` event distribution to subscribers
(engine.go:2883-2902), and `RunSnapshot`/`snapshot()`/replay.go's use of
snapshots to reconstruct run state. This unit gives each a clear, named,
documented structure consistent with Units 1-2, without changing behavior.

**Functional Requirements:**
- The system shall document and, where it improves clarity without behavior
  change, restructure `postExecChain` as an explicit Chain of Responsibility
  (each handler returns "continue" or a terminal decision, matching current
  `decisionContinue`/`decisionFailed`/`decisionNeedsInput` semantics).
- The system shall document the existing `Subscribe`/`fanOutLive`/
  `fanOutCtrl` mechanism as the Observer pattern implementation for engine
  events, making the subscriber contract explicit in code comments or a
  doc.go entry.
- The system shall document `RunSnapshot` and its use in `snapshot()` and
  `replay.go` as the Memento pattern, clarifying which fields are the
  originator's captured state versus caretaker bookkeeping.
- The system shall introduce no behavior change to event delivery, snapshot
  contents, or post-exec handler ordering.

**Proof Artifacts:**
- Test: `replay_test.go`, `journal_test.go`, and `worker_leak_test.go` pass
  unchanged, demonstrating no behavior change to snapshotting or event
  delivery.
- Code: package-level doc comments (or a `doc.go`) on the relevant types
  naming the pattern and pointing to the concrete collaborators.

### Unit 4: Full 23-Pattern Design Audit Document

**Purpose:** Produce a single markdown document that walks through all 23
classic GoF patterns (5 Creational, 7 Structural, 11 Behavioral) and states,
for each, either where it is applied in the refactored `internal/engine`
package (pointing at Units 1-3 plus any other genuine pre-existing fit, e.g.
Facade for `Manager`, Bridge for the `Executor`/`Reporter` interfaces), or a
one-paragraph justification for why it is not applicable to this package —
consistent with this repository's stated convention (`CLAUDE.md`) against
introducing abstractions the code doesn't need.

**Functional Requirements:**
- The system shall list all 23 GoF patterns (Creational: Abstract Factory,
  Builder, Factory Method, Prototype, Singleton; Structural: Adapter,
  Bridge, Composite, Decorator, Facade, Flyweight, Proxy; Behavioral: Chain
  of Responsibility, Command, Interpreter, Iterator, Mediator, Memento,
  Observer, State, Strategy, Template Method, Visitor).
- The system shall mark each pattern as "Applied" with a file/type reference,
  or "Not applicable" with a stated reason, and no pattern shall be left
  unaddressed.
- The system shall avoid recommending or implementing a pattern where doing
  so would only satisfy the checklist (e.g. Singleton, Flyweight,
  Prototype, Abstract Factory are expected to land as "Not applicable" for
  this package, per the reasoning already discussed: Go idioms and this
  package's actual needs don't call for them).

**Proof Artifacts:**
- Doc: `docs/specs/13-spec-engine-design-patterns-refactor/PATTERN-AUDIT.md`
  (or equivalent path agreed at task-planning time) lists all 23 patterns
  with applied/not-applicable status and justification for each, showing
  complete coverage of the ask without forced misuse.

## Non-Goals (Out of Scope)

1. **No new step types, workflow schema fields, or user-facing CLI/TUI
   behavior**: this is an internal structural refactor of
   `internal/engine`; `internal/workflow`, `internal/tui`, and `cmd/jig` are
   not touched except where a renamed exported `engine` symbol requires a
   call-site update.
2. **No forced pattern usage for its own sake**: patterns with no genuine
   fit (expected: Singleton, Flyweight, Prototype, Abstract Factory, and
   possibly others determined during Unit 4) are documented as "Not
   applicable," not shoehorned in.
3. **No performance optimization or concurrency-model change**: the
   scheduler remains single-goroutine-actor-driven via its message channel;
   this refactor does not change threading, locking, or channel buffering
   behavior.
4. **No new tests for pre-existing untested paths**: this refactor relies on
   the existing test suite as the regression safety net. Adding coverage for
   gaps discovered along the way is welcome but not a requirement of this
   spec.

## Design Considerations

No specific design requirements identified — this is a backend/internal
refactor with no UI surface.

## Repository Standards

- Follow `CLAUDE.md`'s explicit conventions: comments explain *why*, not
  *what*; no abstraction beyond what the responsibility actually requires;
  idiomatic Go (prefer small interfaces, composition, and explicit types
  over generic/reflective machinery).
- Keep `internal/engine` as a single focused package per existing
  `ARCHITECTURE.md` conventions — this refactor reorganizes within the
  package, it does not split `internal/engine` into sub-packages unless a
  task-planning discussion determines that's necessary for a specific unit.
- Match existing file organization style (e.g. `journal.go`, `worktree.go`,
  `replay.go` already split by responsibility) — new pattern-specific code
  (e.g. commands, strategies) should live in new same-package files named
  for their responsibility (e.g. `commands.go`, `strategies.go`), consistent
  with how `handlers.go` and `executor.go` are already split out.
- Table-driven tests, following `engine_test.go`'s existing style, for any
  new test coverage introduced to characterize a pattern boundary.

## Technical Considerations

- The refactor operates on `internal/engine/engine.go` and its sibling files
  (`handlers.go`, `executor.go`, `event.go`, `context.go`, `journal.go`,
  `replay.go`, `worktree.go`) which together form the `engine` package.
- Per the user's explicit decision, the public API of `internal/engine`
  (`Executor`, `Reporter`, `Manager`, `Run`, exported event/result types) may
  change if it produces a cleaner design; all call sites in
  `internal/runner`, `internal/tui`, and test files must be updated to
  match, and `go build ./...` / `go vet ./...` must pass after the change.
- GoF patterns are a stable, non-versioned body of knowledge; no
  external/current-year standards research materially affects this spec
  beyond the repository's own stated Go idioms already captured above.
- The `postExecChain` decision type (`decisionContinue`/`decisionFailed`/
  `decisionNeedsInput`) and the `schedMsg`/`isSchedMsg()` marker-interface
  convention already establish the vocabulary this refactor should build on
  rather than replace wholesale.

## Security Considerations

No specific security considerations identified — this is an internal
structural refactor with no change to credential handling, tool execution,
or the security-monitoring pipeline (`SecurityFinding`/`handleSecurityFinding`
behavior is preserved unchanged per Unit 1's requirements).

## Success Metrics

1. **Test suite parity**: `go test ./...` passes with the same set of test
   *behaviors* verified (test files may be edited for renamed symbols, but
   no test's assertions about scheduler behavior may be weakened or
   removed).
2. **Dispatch complexity reduction**: `scheduler.handle()`'s type-switch (or
   its Command-pattern replacement's central dispatch point) is reduced from
   ~200 lines to a delegation-only body (each case is a one-line dispatch to
   a command's execute method).
3. **Complete pattern coverage**: the Unit 4 audit document accounts for all
   23 GoF patterns with zero left unaddressed.

## Open Questions

1. Exact file names for the new pattern-specific source files (e.g.
   `commands.go` vs `scheduler_commands.go`) will be finalized during task
   planning (Phase 2), consistent with existing sibling-file naming.
2. Whether Unit 4's audit document lives under
   `docs/specs/13-.../PATTERN-AUDIT.md` or as a `doc.go` package comment in
   `internal/engine` is a non-blocking presentation choice to finalize
   during task planning; either satisfies the "complete, reviewable
   coverage" requirement.
