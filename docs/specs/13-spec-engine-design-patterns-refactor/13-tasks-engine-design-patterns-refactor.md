# 13-tasks-engine-design-patterns-refactor.md

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | No abstraction beyond what's needed; comments explain *why* not *what*; idiomatic small `internal/` packages; `gofmt -l -w .` + `go vet ./...` before commit | none |
| `README.md` | yes | Engine traverses DAG, dispatches agent/command steps, drives loops/review gates | none |
| `docs/TESTING.md` | yes | Table-driven tests; `go test ./... -race` for engine work; keep agent/shell execution behind interfaces so DAG/gate/loop logic is testable with fakes | none |
| `AGENTS.md` | not found | — | — |
| `CONTRIBUTING.md` | not found | — | — |
| `.github/pull_request_template.md` | not found | — | — |
| lint config (`.golangci.yml`/`.yaml`) | not found | `gofmt`/`go vet` are the only enforced gates (per `CLAUDE.md`/`TESTING.md`) | none |

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/engine/engine.go` | Contains `scheduler.handle()` (the type-switch to replace), `dispatch()` and `applyFailurePolicy()` (branching to convert to Strategy), `postExecChain`, `Subscribe`/`fanOutLive`/`fanOutCtrl`, and `RunSnapshot`/`snapshot()` (patterns to formalize). |
| `internal/engine/commands.go` (new) | Houses the `execute(s *scheduler)` methods for each `schedMsg` type (Task 1.0). |
| `internal/engine/strategies.go` (new) | Houses step-dispatch and failure-policy `Strategy` implementations and their lookup tables (Task 2.0). |
| `internal/engine/handlers.go` | Existing named handler methods (`handleStop`, `handleResume`, `handleReset`, etc.) invoked from the new command `execute()` methods; unchanged behaviorally, referenced not rewritten. |
| `internal/engine/event.go` | Defines `Event` and related types consumed by the Observer (`Subscribe`/`fanOut*`) documentation in Task 3.0. |
| `internal/engine/executor.go` | Defines `Executor`/`Reporter` interfaces — the existing Bridge-pattern fit referenced in the Task 4.0 audit. |
| `internal/engine/replay.go` | Consumes `RunSnapshot` for replay — the Memento pattern's caretaker side, documented in Task 3.0. |
| `internal/engine/journal.go` | Persists events; reviewed to confirm Task 3.0 doc comments don't misdescribe journal vs. snapshot responsibilities. |
| `internal/engine/engine_test.go` | Existing scheduler tests; must pass unchanged (behaviorally) after Tasks 1.0-2.0, updated only for renamed symbols. |
| `internal/engine/integration_test.go` | End-to-end engine tests; regression gate for Task 5.0. |
| `internal/engine/worktree_test.go` | Regression gate for Task 5.0 (worktree-related dispatch paths). |
| `internal/engine/stop_test.go` | Regression gate for `stopMsg` command behavior (Task 1.0). |
| `internal/engine/reset_test.go` | Regression gate for `resetMsg` command behavior (Task 1.0). |
| `internal/engine/recovery_test.go` | Regression gate for `recoverMsg`/failure-policy behavior (Tasks 1.0-2.0). |
| `internal/engine/replay_test.go` | Regression gate for Memento/replay documentation (Task 3.0). |
| `internal/engine/journal_test.go` | Regression gate for journal/event behavior (Task 3.0). |
| `internal/engine/loop_coalesce_test.go` | Regression gate for loop-intent handling touched by `stepDoneMsg` command extraction (Task 1.0). |
| `internal/engine/question_race_test.go` | Regression gate for `agentQuestionNotifyMsg`/`agentQuestionAnswerMsg` command extraction (Task 1.0). |
| `internal/engine/worker_leak_test.go` | Regression gate for Observer/event-delivery documentation (Task 3.0). |
| `internal/engine/context_test.go` | Regression gate unaffected by scheduler internals but part of the full-package `-race` run (Task 5.0). |
| `internal/runner/agent.go`, `internal/runner/command.go`, `internal/runner/mux.go`, `internal/runner/monitor.go`, `internal/runner/fake.go` | Implement `Executor`; call sites to update if Tasks 1.0-2.0 rename any exported `engine` symbols. |
| `internal/tui/root.go`, `internal/tui/root_cmds.go`, `internal/tui/root_update.go` | Call into `engine.Manager`/`engine.Run`; call sites to update if exported symbols are renamed. |
| `docs/specs/13-spec-engine-design-patterns-refactor/PATTERN-AUDIT.md` (new) | The Task 4.0 deliverable: full 23-pattern coverage audit. |

### Notes

- Run the full engine suite with the race detector per `docs/TESTING.md`: `go test ./internal/engine/... -race`.
- Table-driven test style (`{name, ..., want}` slices run via `t.Run`) matches the existing `engine_test.go`/`workflow_test.go` convention — follow it for any new characterization tests.
- `gofmt -l -w .` and `go vet ./...` are this repo's only enforced formatting/lint gates (no `.golangci.yml` present) — run both before considering any task complete.
- Tasks 1.0-3.0 are pure refactors: no test assertions should need to change, only potentially the symbols they reference. If a test assertion needs to change to keep passing, that's a signal the refactor introduced a behavior change — stop and reconcile before proceeding.

## Tasks

### [x] 1.0 Command Pattern for Scheduler Message Dispatch

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/engine/... -race` passes, demonstrating the Command-based dispatch preserves existing scheduler behavior for every `schedMsg` type.
- Code: `git diff internal/engine/engine.go` shows `scheduler.handle()`'s ~200-line `switch m := msg.(type)` (engine.go:1408-1609) replaced by a single delegation call per message, with per-message logic extracted into named `execute`-style methods/files.

#### 1.0 Tasks

- [x] 1.1 Create `internal/engine/commands.go` and define the dispatch contract each `schedMsg` type will implement, alongside the existing `isSchedMsg()` marker method set (e.g. `execute(s *scheduler)`), documented with a comment naming this as the Command pattern and explaining why it replaces the type-switch.
- [x] 1.2 Move the `stepDoneMsg` case body (engine.go:1410-1509, the largest branch — worker cleanup, `StatusAwaitingRecovery`/`stopping` early returns, result/error recording, failure-policy dispatch, `postExecChain` run, loop-intent recording) verbatim into `(stepDoneMsg) execute(s *scheduler)` in `commands.go`, preserving every comment that explains non-obvious ordering (e.g. the cost-accrual comment at engine.go:1413-1417).
- [x] 1.3 Move the `userInputMsg` and `verdictMsg` case bodies (engine.go:1511-1550) into their own `execute(s *scheduler)` methods in `commands.go`.
- [x] 1.4 Convert the remaining cases that already delegate to a named handler (`humanMessageMsg`→`handleHumanMessage`, `agentInputMsg`→`handleAgentInput`, `recoverMsg`→`handleRecover`, `resolveIntegrationMsg`→`handleResolveIntegration`, `finalMergeMsg`→`handleFinalMerge`, `stopMsg`→`handleStop`, `resumeMsg`→`handleResume`, `resetMsg`→`handleReset`, `securityFindingMsg`→`handleSecurityFinding`) into one-line `execute(s *scheduler)` methods that call the existing handler — no logic changes to the handlers themselves in `handlers.go`.
- [x] 1.5 Move the four remaining inline cases (`agentQuestionNotifyMsg`, `agentQuestionAnswerMsg`, `snapshotReqMsg`, `closureReqMsg`; engine.go:1579-1608) into their own `execute(s *scheduler)` methods.
- [x] 1.6 Replace the body of `scheduler.handle()` (or its call site in `scheduler.run()`) so it invokes `msg.(command).execute(s)` (or equivalent single dispatch line) instead of the type-switch; delete the now-empty `handle()` type-switch once every message type has an `execute()` method.
- [x] 1.7 Run `gofmt -l -w internal/engine/` and `go vet ./internal/engine/...`, then `go test ./internal/engine/... -race`; fix any compile errors or behavior regressions before proceeding.

### [x] 2.0 Strategy Pattern for Step Dispatch and Failure Policy

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/engine/... -run TestDispatch -v` (or renamed equivalent) and `go test ./internal/engine/... -run TestFailurePolicy -v` (or renamed equivalent) pass, demonstrating strategy-based selection is behaviorally equivalent to prior branching.
- Code: a constructor/table (e.g. `map[workflow.StepType]dispatchStrategy`, `map[workflow.FailurePolicy]failureStrategy`) in the new strategy file shows the extension point replacing the inline branches in `dispatch()` (engine.go:1143) and `applyFailurePolicy()` (engine.go:1613).

#### 2.0 Tasks

- [x] 2.1 Create `internal/engine/strategies.go`; define a `stepDispatchStrategy` interface capturing what each per-type branch in `scheduler.dispatch()` (engine.go:1143-1291) currently does, documented as the Strategy pattern.
- [x] 2.2 Extract the agent-step branch of `dispatch()` into an `agentDispatchStrategy` implementation; extract the command-step branch into a `commandDispatchStrategy`; extract the review-step branch into a `reviewDispatchStrategy` (review dispatch already partly lives in `dispatchReview`, engine.go:2324 — wrap/call it from the strategy, don't duplicate).
- [x] 2.3 Build a `map[workflow.StepType]stepDispatchStrategy` (package-level var or scheduler field) and update `dispatch()` to look up and invoke the strategy for `wfStep.Type`, replacing the inline branch.
- [x] 2.4 Define a `failurePolicyStrategy` interface capturing what `applyFailurePolicy()` (engine.go:1613-1659) does per `workflow.OnFailure` value (including the default-to-`FailAbort` case).
- [x] 2.5 Extract each policy's behavior into its own `failurePolicyStrategy` implementation and build a `map[workflow.FailurePolicy]failurePolicyStrategy`; update `applyFailurePolicy()` to look up and invoke the strategy instead of branching inline.
- [x] 2.6 Run `gofmt -l -w internal/engine/` and `go vet ./internal/engine/...`, then `go test ./internal/engine/... -race` (with particular attention to `worktree_test.go` and `recovery_test.go`, which exercise dispatch and failure-policy paths); fix regressions before proceeding.

### [x] 3.0 Formalize Chain of Responsibility, Observer, and Memento

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/engine/... -run TestReplay -v`, `go test ./internal/engine/... -run TestJournal -v`, and `go test ./internal/engine/... -run TestWorkerLeak -v` (or renamed equivalents) pass unchanged, demonstrating no behavior change to snapshotting, replay, or event delivery.
- Code: named doc comments (or `doc.go` entries) on `postExecChain`, `Subscribe`/`fanOutLive`/`fanOutCtrl`, and `RunSnapshot` identify each as Chain of Responsibility, Observer, and Memento respectively, with a pointer to their concrete collaborators.

#### 3.0 Tasks

- [x] 3.1 Add a doc comment above the `postExecChain` type/field and its `decisionContinue`/`decisionFailed`/`decisionNeedsInput` decision type naming this the Chain of Responsibility pattern, and describing the "continue to next handler vs. short-circuit" contract each handler must honor — no behavior change to the handlers or their invocation order in the (now-relocated) `stepDoneMsg` command.
- [x] 3.2 Add a doc comment above `(m *Manager) Subscribe()` and `fanOutLive`/`fanOutCtrl` (engine.go:2883-2902) naming this the Observer pattern, documenting the `live`/`ctrl` channel contract (what each channel is for, delivery semantics) so a future subscriber implementer doesn't have to reverse-engineer it.
- [x] 3.3 Add a doc comment above `RunSnapshot` and `scheduler.snapshot()` naming this the Memento pattern, clarifying that `RunSnapshot` is the memento (opaque captured state), `scheduler` is the originator, and `Manager`/`replay.go` are the caretakers that store/restore it — cross-reference `replay.go`'s usage in the comment.
- [x] 3.4 Verify no field, method signature, or channel semantics changed by diffing `git diff internal/engine/` and confirming only comments were added/edited in this task.
- [x] 3.5 Run `gofmt -l -w internal/engine/` and `go vet ./internal/engine/...`, then `go test ./internal/engine/... -race`; fix any regressions before proceeding.

### [x] 4.0 Full 23-Pattern Design Audit Document

#### 4.0 Proof Artifact(s)

- Doc: `docs/specs/13-spec-engine-design-patterns-refactor/PATTERN-AUDIT.md` lists all 23 GoF patterns (5 Creational, 7 Structural, 11 Behavioral), each marked Applied (with file/type reference into Tasks 1.0-3.0 or a pre-existing fit such as Facade/`Manager` or Bridge/`Executor`) or Not Applicable (with a one-paragraph reason grounded in `CLAUDE.md`'s anti-premature-abstraction convention), demonstrating complete, non-forced coverage of all 23 patterns.

#### 4.0 Tasks

- [x] 4.1 Create `docs/specs/13-spec-engine-design-patterns-refactor/PATTERN-AUDIT.md` with three sections (Creational, Structural, Behavioral) and a row/entry for each of the 23 named GoF patterns.
- [x] 4.2 Fill in the 5 Creational patterns (Abstract Factory, Builder, Factory Method, Prototype, Singleton): mark `buildRequest()` (engine.go:1362) as Applied/Builder if it fits the Builder contract; mark `NewManager`/`newScheduler` as Applied/Factory Method; mark Abstract Factory, Prototype, and Singleton as Not Applicable with a one-paragraph reason each (e.g. Singleton conflicts with `Manager`'s per-process, testable-instance design; Prototype has no clone-heavy object in this package).
- [x] 4.3 Fill in the 7 Structural patterns (Adapter, Bridge, Composite, Decorator, Facade, Flyweight, Proxy): mark `Executor`/`Reporter` (executor.go) as Applied/Bridge; mark `Manager` as Applied/Facade; assess `closureOf`/`bodyUnion`/`loopBody` (engine.go:2526-2716, recursive step-graph traversal) for a Composite fit and mark accordingly; mark Adapter, Decorator, Flyweight, Proxy as Applied or Not Applicable based on actual code, with reasoning for any Not Applicable entry.
- [x] 4.4 Fill in the 11 Behavioral patterns (Chain of Responsibility, Command, Interpreter, Iterator, Mediator, Memento, Observer, State, Strategy, Template Method, Visitor): mark Chain of Responsibility, Command, Memento, Observer, Strategy as Applied, referencing Tasks 1.0-3.0; assess `evalGuard()`/`workflow.Condition` (engine.go:2108-2165) for an Interpreter fit; assess `nextReady()`/`anyPendingRunnable()`/`anyFailed()` for an Iterator fit; assess `scheduler` itself (coordinating steps/loops/workers without them referencing each other) for a Mediator fit; assess `step.Status`/`transition()` for a State fit, noting that a formal State-object hierarchy would add object bloat the current enum+guard-function approach avoids (per `CLAUDE.md`); mark Template Method and Visitor as Applied or Not Applicable based on actual code.
- [x] 4.5 Cross-check the completed document lists exactly 23 patterns with no duplicates or omissions, and that every "Applied" entry names a real file/type/line that exists in the codebase (re-verify against `git diff` from Tasks 1.0-3.0, not from memory).

### [ ] 5.0 Full-Repository Regression Verification

#### 5.0 Proof Artifact(s)

- CLI: `gofmt -l .` returns no output, demonstrating formatting compliance.
- CLI: `go vet ./...` returns no output, demonstrating no vet violations across the repository after `internal/engine`'s public API changes propagate to call sites.
- CLI: `go build ./cmd/jig` succeeds, demonstrating `internal/runner`, `internal/tui`, and `cmd/jig` compile against any renamed `engine` exported symbols.
- Test: `go test ./... -race` passes for the full repository, demonstrating the refactor introduced no regressions outside `internal/engine` (e.g. in `internal/runner`, `internal/tui`) and no behavior changes anywhere in `internal/engine`'s existing suite (`engine_test.go`, `integration_test.go`, `worktree_test.go`, `stop_test.go`, `reset_test.go`, `recovery_test.go`, `replay_test.go`, `journal_test.go`, `loop_coalesce_test.go`, `question_race_test.go`, `worker_leak_test.go`, `context_test.go`).

#### 5.0 Tasks

- [ ] 5.1 Grep `internal/runner/*.go` (`agent.go`, `command.go`, `mux.go`, `monitor.go`, `fake.go`) for references to any `engine`-package symbol renamed or restructured in Tasks 1.0-2.0, and update call sites accordingly.
- [ ] 5.2 Grep `internal/tui/root.go`, `root_cmds.go`, `root_update.go` for references to any renamed `engine.Manager`/`engine.Run` symbols, and update call sites accordingly.
- [ ] 5.3 Update any `internal/engine/*_test.go` references to renamed unexported/exported symbols so the suite compiles — without altering any test's assertions or expected behavior.
- [ ] 5.4 Run `gofmt -l -w .` and `go vet ./...` across the full repository; resolve any findings.
- [ ] 5.5 Run `go build ./cmd/jig` and confirm it succeeds.
- [ ] 5.6 Run `go test ./... -race`; confirm the full suite passes with no assertion changes beyond symbol renames, and record the pass as the closing proof artifact for this feature.
