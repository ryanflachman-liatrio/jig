# PATTERN-AUDIT.md — internal/engine GoF pattern coverage

This audit covers all 23 Gang-of-Four patterns (5 Creational, 7 Structural,
11 Behavioral) against `internal/engine` as it stands after Tasks 1.0-3.0 of
spec 13. Each entry is marked **Applied** (with the concrete file/type that
implements it) or **Not Applicable** (with a reason grounded in `CLAUDE.md`'s
anti-premature-abstraction convention — "no abstraction beyond what's
needed"). A pattern being Not Applicable here does not mean it is
categorically wrong for Go or for jig; it means forcing it into
`internal/engine` today would add indirection with no real caller needing the
flexibility it buys.

## Creational (5)

| Pattern | Status | Reference / Reason |
| --- | --- | --- |
| Abstract Factory | Not Applicable | There is no family of related objects that must be created together and swapped as a set (e.g. "a UI kit's button+checkbox+scrollbar for theme X"). `internal/engine` creates exactly one kind of thing per factory (a `Manager`, a `scheduler`) — introducing an abstract-factory layer above `NewManager`/`newScheduler` would add a level of indirection with no second product family to justify it. |
| Builder | Applied | `(s *scheduler) buildRequest` (`engine.go:1391`) assembles a `StepRequest` across several independent, conditionally-included parts (workflow-context preamble, security guard + findings path, resumed-session fields) before returning the finished value — separating the assembly steps from the final representation is the Builder contract, even though it's a single method rather than a distinct `Director`/`Builder` type pair. Spec 13's anti-abstraction stance argues against extracting a formal `Builder` type here: `StepRequest` has exactly one caller and one assembly recipe, so a fluent builder object would add ceremony without adding a second construction path to select between. |
| Factory Method | Applied | `NewManager` (`engine.go:54`) and `newScheduler` (`engine.go:736`) are Factory Methods: each hides its type's zero-value/field-wiring details (map initialization, embedded config) behind a constructor function so callers never assemble a `Manager`/`scheduler` by hand. |
| Prototype | Not Applicable | Nothing in `internal/engine` is expensive or complex enough to clone rather than construct fresh. `workflow.Workflow`/`workflow.Step` values are loaded once from TOML and treated as read-only for the run's lifetime; there is no "clone this configured object and tweak it" workflow that Prototype exists to serve. |
| Singleton | Not Applicable | `Manager` is deliberately **not** a singleton: `internal/engine`'s own test suite constructs many independent `Manager`/scheduler instances per test (see `engine_test.go`, `integration_test.go`), and `cmd/jig` could in principle run more than one. Enforcing a single global instance would directly break that testability, which `docs/TESTING.md` treats as a first-class constraint. |

## Structural (7)

| Pattern | Status | Reference / Reason |
| --- | --- | --- |
| Adapter | Not Applicable | Adapter reconciles two *already-existing, incompatible* interfaces. `StepRequest`/`step.Result` are purpose-built DTOs designed from scratch for the `Executor` boundary, not a shim retrofitted onto a pre-existing interface `internal/engine` doesn't control. There is no legacy interface being adapted here — see Bridge below for the actual seam. |
| Bridge | Applied | `Executor`/`Reporter` (`executor.go:13`, `executor.go:71`) decouple the scheduler (the abstraction) from the concrete execution mechanism (agent SDK vs. shell, implemented in `internal/runner`). The scheduler calls `Executor.Execute` without knowing which concrete executor — or `runner.Mux`'s per-type routing — is behind it, which is exactly Bridge's abstraction/implementation split. |
| Composite | Not Applicable | `closureOf`/`bodyUnion`/`loopBody` (`engine.go:2482`, `engine.go:2332`, `engine.go:2432`) traverse the workflow's dependency graph, but over a **flat** `[]workflow.Step` slice keyed by string ID — not a recursive tree of uniform `Component` nodes where leaves and composites share one interface. These are graph-reachability algorithms (BFS-style fixed-point over `DependsOn` edges), not object composition; wrapping the flat step list in a Composite hierarchy would not simplify any caller, since nothing here recurses into sub-steps. |
| Decorator | Not Applicable | Nothing in `internal/engine` wraps an `Executor` (or any other interface) at runtime to layer on additional behavior (e.g. retry, logging, caching) while preserving its interface. `runner.Mux` *routes* to one of several executors by step type — that's Strategy (already covered by Task 2.0's `stepDispatchStrategy`), not Decorator, because it selects one implementation rather than layering behavior onto an existing one. |
| Facade | Applied | `Manager` (`engine.go:43`) is a Facade over the scheduler goroutine, datastore persistence, journal replay, and event fan-out: callers (`cmd/jig`, `internal/tui`) call `Start`/`Subscribe`/`RunDir`/`PersistedRuns` without touching `scheduler`, `datastore`, or `journal.go` directly. |
| Flyweight | Not Applicable | There is no large population of fine-grained, mostly-identical objects whose per-instance memory cost would justify sharing intrinsic state (Flyweight's classic motivation — e.g. tens of thousands of glyphs or particles). A workflow run has at most a few dozen steps, each already a distinct, cheaply-sized value; there is nothing here to pool. |
| Proxy | Not Applicable | No caller needs lazy initialization, access control, or remote-call transparency in front of `Executor` or `Manager` — every `Executor` call goes straight to the registered implementation (`runner.Mux`), and every `Manager` method runs synchronously in-process. Introducing a Proxy would add a layer with no substitutable concern (caching, auth, lazy-load) for it to own. |

## Behavioral (11)

| Pattern | Status | Reference / Reason |
| --- | --- | --- |
| Chain of Responsibility | Applied | `postExecChain` (`engine.go`, built in `newScheduler`) and the `postExecHandler`/`postExecDecision` contract (`handlers.go`) — formalized with doc comments in Task 3.0. Each handler either passes to the next link (`decisionContinue`) or short-circuits with a final verdict. |
| Command | Applied | Every `schedMsg` implements `execute(s *scheduler)` in `commands.go` (Task 1.0's `command` interface), replacing the prior type-switch with one Command object per message type. |
| Interpreter | Applied | `workflow.Condition`/`ParseCondition` (`internal/workflow/condition.go:28`, `:39`) define a tiny grammar for `when`/`loop.when` guards (`stepid`, `stepid.field == 'value'`, etc.), and `(s *scheduler) evalGuard` (`engine.go:1914`) interprets a parsed `Condition` against live step state — a minimal but genuine parse-then-interpret pipeline. |
| Iterator | Not Applicable | `nextReady`, `anyPendingRunnable`, and `anyFailed` use Go's built-in `range` over slices/maps, which already provides Iterator's guarantee (sequential access without exposing the underlying representation) natively. Wrapping that in a custom `Iterator` interface with `Next()`/`HasNext()` would only be justified if a caller needed multiple independent, interruptible traversals of the same collection or a pluggable traversal order — no caller does. |
| Mediator | Applied | `scheduler` is a Mediator: steps, workers, and loop back-edges never reference each other directly — all coordination (dependency readiness, worker completion via `stepDoneMsg`, loop-intent recording) is routed through the scheduler's inbox and state maps, so step/worker code stays decoupled from its peers. |
| Memento | Applied | `RunSnapshot`/`(s *scheduler) snapshot()` (`engine.go:103`, `engine.go:2662`) — formalized with doc comments in Task 3.0. `RunSnapshot` is the opaque memento, `scheduler` the originator, `Manager`/`replay.go` the caretakers. |
| Observer | Applied | `(m *Manager) Subscribe()`/`fanOutLive`/`fanOutCtrl` (`engine.go:262`, `:2696`, `:2710`) — formalized with doc comments in Task 3.0. `Manager` is the subject, each `sub` an observer, `Event` the notification. |
| State | Not Applicable | `step.Status` is a plain enum and `(s *scheduler) transition` (`engine.go:2589`) is a single function that mutates status and emits an event — there is no per-state object hierarchy where each `Status` value owns its own type with overridden behavior. A formal State pattern (one struct per status implementing a shared interface) would add object-per-enum-value bloat that the current enum-plus-guard-function approach avoids, consistent with `CLAUDE.md`'s "no abstraction beyond what's needed." |
| Strategy | Applied | `stepDispatchStrategy`/`failurePolicyStrategy` (`strategies.go`, Task 2.0) — `map[workflow.StepType]stepDispatchStrategy` and `map[workflow.FailurePolicy]failurePolicyStrategy` replace what were inline branches in `dispatch()`/`applyFailurePolicy()`. |
| Template Method | Not Applicable | No algorithm in `internal/engine` is expressed as a fixed skeleton in a base type with individual steps deferred to subtypes/overrides — Go's composition-over-inheritance style and the package's preference for interfaces (`Executor`) plus lookup tables (Strategy) cover the "vary one step of an algorithm" need without a base-class skeleton. |
| Visitor | Not Applicable | Visitor earns its keep when you need to add new operations over a stable, closed set of types without touching each type (double dispatch). `internal/engine`'s closest candidate — `postExecHandler`'s per-outcome logic — already varies by *outcome*, not by a set of node *types* needing new traversable operations added later; forcing a `Visitor` here (e.g. over `schedMsg` variants, already handled by Command in Task 1.0) would duplicate Command's dispatch for no new capability. |

## Summary

- **Applied (11):** Builder, Factory Method, Bridge, Facade, Chain of Responsibility, Command, Interpreter, Mediator, Memento, Observer, Strategy.
- **Not Applicable (12):** Abstract Factory, Prototype, Singleton, Adapter, Composite, Decorator, Flyweight, Proxy, Iterator, State, Template Method, Visitor.
- **Total:** 23 patterns, no duplicates, no omissions (5 Creational + 7 Structural + 11 Behavioral).

Every "Applied" entry above names a file/type that exists in the codebase as of this spec's Task 3.0 commit (`a384089`); every "Not Applicable" entry gives a reason specific to `internal/engine`'s actual code, not a generic dismissal.
