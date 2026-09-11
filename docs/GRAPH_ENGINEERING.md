# Graph and workflow engineering

Use for `internal/workflow`, `internal/engine`, runner contracts, and executable
workflow changes. [Workflow schema](workflow-schema.md) defines syntax;
[Architecture](ARCHITECTURE.md) identifies the owners. The rules below apply
established graph and durable-execution ideas to jig's implementation; they do
not imply jig implements a distributed workflow service.

## Keep the graph explicit

Treat `depends_on` as the forward dependency graph: a dependency must settle
according to engine policy before its consumer is eligible. Keep data
references and typed conditions consistent with declared dependencies. A
workflow author should not need to inspect a skill's prose to discover a
scheduling dependency or a hidden output channel.

Validate duplicate IDs, missing references, self-dependencies, forward cycles,
condition types, route targets and bounds before dispatch. Use the existing
condition parser/AST for reference discovery, rewriting, and evaluation; do not
introduce regex substitution or a second condition language for modules or UI.
Separate dependency edges from bounded route back-edges when detecting cycles
and drawing the graph.

Topological order is generally not unique. Use stable declared order or an
explicit tie-breaker where user-visible output, diagnostics, or replay requires
it; never rely on map iteration order. Concurrent completion order can differ
between runs. Test partial-order guarantees rather than assuming independent
workers finish in one total order. For algorithm changes, document edge
direction, complexity, and handling of disconnected components. Aim for
O(V+E) traversals when suitable; do not repeatedly walk the whole graph for
every node without understanding the bound.

[Go's Gonum topology API](https://pkg.go.dev/gonum.org/v1/gonum/graph/topo)
provides primary references for topological sorting, stabilized sorting, and
strongly connected components. This is algorithm guidance, not a request to
add Gonum as a dependency.

## Authoring and load boundary

Five author-facing kinds exist: agent, command, check, review, and subworkflow.
Subworkflow modules expand into one namespaced graph before final validation;
they do not launch independent engines. Validate module input/export types,
containment, recursive imports, rewritten conditions/references, and namespace
collisions. Module consumers wait for the module's terminal dependencies.
Restore from captured module sources rather than a newly edited checkout.

When adding a schema field, cover parsing, unknown-key rejection, applicable
step kinds, presence/default precedence, validation, runtime use, persistence,
and presentation/CLI exposure where relevant. Update the schema, examples, and
scaffold templates if affected. Not every field inherits from `[defaults]`.
Agent-file and profile folding is field-specific; preserve explicit step
values and test unset versus explicitly empty values where meaningful.

Separate authoring assets from runtime products. Skills, agent files, and
schemas resolve from the workflow directory; command `script` paths have a
repo-root contract. A runtime-produced file cannot be required to exist during
static validation. Runtime output must still satisfy its declared contract
before it is accepted. Never infer decisions by scraping free-form model prose.

Workflow design should expose typed outputs, deterministic checks, explicit
human-review boundaries, and actionable failure outcomes. Prefer command/check
steps for deterministic work. Give retryable external work deadlines and
bounded retries. Use resource classes and mutation isolation for genuine
contention. Validate root workflows that invoke modules; module and profile
TOMLs are not independently runnable workflows.

## Scheduling and liveness

One scheduler owns a run's state. Workers execute through `Executor`, report
through `Reporter`, and return messages; TUI/headless clients send commands.
Keep the scheduler responsive while workers, gates, or external services wait.
Preserve global, read-only/mutating, class, and family capacity accounting;
release capacity on success, failure, cancellation, stop, and recovery paths.

A graph can be valid without guaranteeing wall-clock completion. Bounded routes
prevent unlimited automatic loop passes, but human parks, absent deadlines,
external processes, and manual resets can extend a run indefinitely. State
liveness assumptions explicitly. Observed cost/network/security limits only
cover what the implementation observes; they are not complete accounting of
external side effects.

Do not equate no runnable nodes with success. A run can be waiting on active
workers, human input, retry backoff, an unresolved family, or recovery. New
states need deliberate readiness, settlement, snapshot, replay, and UI behavior.
Review skip propagation and failure policy at joins so one failed branch does
not accidentally turn the rest of the workflow green.

## Routes, retries, and identity

- **Route:** a declared, ordered, bounded graph back-edge. Preserve route guard
  evaluation, caps, feedback, and coalescing when branches share a rewind target.
- **Retry:** another execution attempt after a classified failure. New workflows
  should use `[step.retry]`, whose validator requires `idempotent = true` and
  explicit retryable classes. `max_attempts` includes the initial attempt;
  the existing legacy `max_retries` counts retries. Do not conflate them.
- **Reset:** an operator rewind of the target and its transitive downstream
  dependents, with survivor commit replay. It changes generation and requires
  unfinished, quiescent state.
- **Resume:** continuation of a stopped backend conversation through a newly
  opened session; crash reopen restores the scheduler and parks first.

Retry is safe only when repeated effects are acceptable or deduplicated. An
`idempotent` declaration is an author promise, not a mechanism that makes a
shell command or agent harmless. Keep logical-operation idempotency keys stable
across attempts when an external service supports them. Cancellation/timeout
can leave an unknown external outcome; use recovery or reconciliation where
blind replay could duplicate a mutation. Durable workflow systems likewise
separate replayable orchestration from retryable side effects.
[Temporal architecture](https://github.com/temporalio/temporal/blob/main/docs/architecture/README.md)

Keep attempt, iteration, generation, run/session identity, and runtime child
identity distinct in reports and storage. Late worker results must not mutate
a replacement execution. Review the engine's identity/epoch checks whenever
changing stop, retry, reset, or reopen.

## Dynamic fan-out and fan-in

A `foreach` family is one static graph node and a runtime barrier. Its children
are scheduler-owned instances, never author-addressable `wf.Steps` entries.
Expansion resolves a bounded list, canonicalizes items, and records durable
identity before execution. Enforce `max_items` before allocation/dispatch;
respect family concurrency alongside the global scheduler limits.

Downstream steps reference only the family aggregate. Settle it using every
child of the current expansion and preserve source-index order in results,
regardless of completion order. An empty expansion succeeds immediately.
Exercise child failure/acceptance, skips, retries, caps, reset invalidation, and
crash reopen. Reopen must reconstruct the recorded expansion without rerunning
the producer to invent a new list.

## Persistence and recovery

Journal required state before publishing it. Journal failures stop further
progress; they must not become “best effort.” Live previews may drop, while
control decisions and finalized artifacts require a reliable path. Keep
required snapshots and immutable review/input artifacts consistent with their
journal references; check integrity at the consuming boundary.

Reopen is recovery from recorded facts, not re-execution of completed effects.
Acquire the run lease, load the locked workflow/history, restore counters and
parks, and move interrupted work into recovery according to the existing
contract. Reject missing required artifacts and interior journal corruption.
Display-only fallback records must never authorize execution. Test truncated
tails separately from corrupt interior records and from missing snapshots.

Do not promise exactly-once execution or power-loss durability from an append
call alone. Analyze each write/side-effect boundary and its crash window. Use
existing atomic publication helpers, explicit replay rules, and operator
recovery where no atomic transaction spans Git, files, and external tools.
Persistence-off tests still need useful in-memory behavior without accidental
relative-path writes.

## Completion evidence

For each changed graph behavior, verify the smallest relevant linear, diamond,
disconnected, conditional, bounded-loop, or fan-out case. Include rejection
cases and one adversarial lifecycle case (late result, write failure, resource
exhaustion, interrupted execution, or missing artifact) relevant to the change.
Assert transitions, dependency ordering, side-effect counts, and durable output
rather than only the final status. See [Testing](TESTING.md) for commands.
