# Go engineering conventions

Apply these rules to Go implementation and review. Package ownership is in
[Architecture](ARCHITECTURE.md); graph and terminal rules live in
[Graph engineering](GRAPH_ENGINEERING.md) and [TUI engineering](TUI.md).

## Design around ownership

Keep packages cohesive and APIs small. Define interfaces at the consuming
boundary when substitution is useful; `engine.Executor` and `engine.Reporter`
are the local examples. Prefer concrete types internally. Do not add an
interface for every struct, a generic service layer, or a package merely to
reduce a file's line count.

Name files after the concern they own. Split methods within a package when
that improves navigation; extract a package when it establishes a useful
boundary and an acyclic dependency direction. The TUI already has screen
packages and a shared foundation. There is no requirement to flatten all models
or to extract every model into a package. Extract substantial switch arms when
it clarifies behavior; fixed line-count rules are not a design test.

Place constants and helpers near their owning behavior. Export only what other
packages need. Comments document contracts and explain non-obvious reasoning,
especially lifecycle, ownership, and recovery assumptions. Avoid comments that
repeat the next statement. These choices follow Go's guidance on small
consumer-owned interfaces, naming, and useful documentation.
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)

## Represent domain state explicitly

Use the existing typed constants and structures for step status, failure
classification, events, and workflow fields. Avoid stringly typed maps for
known contracts. Validate external data at the boundary that owns the rule;
keep runtime checks for facts only known during execution, such as producer
output, file contents, transport capabilities, and budgets.

Distinguish absent values from explicit `false`, zero, empty collections, and
null when the schema needs that distinction. Use pointers or presence metadata
at decoding boundaries rather than silently changing precedence. Resolve
inheritable values once. Backend/transport defaults differ from agent-profile
precedence; consult `internal/workflow/load.go` for each field.

Do not import Rust representation conventions wholesale into Go. Useful zero
values, focused structs, and explicit validation are appropriate Go designs.
Introduce generics only when a real repeated algorithm benefits from them.

## Errors and resource lifetime

- Return errors from library code; keep process exit decisions in `cmd/jig`.
  Invalid workflows, backend failures, and malformed persisted data are errors,
  not reasons to panic.
- Wrap with `%w` when callers need the underlying error; add operation and
  safe identifiers. Use `errors.Is`/`errors.As` for programmatic decisions.
  Human-facing validator tests may assert distinctive message fragments.
- Handle an error at a clear boundary. Avoid logging and returning the same
  error at every layer. Keep secrets, prompt text, and raw tool data out of
  generic error and telemetry fields.
- Acquire a resource before registering its cleanup. Check write/flush/close
  errors when they determine whether required output was persisted. A
  best-effort cleanup error must not hide the primary failure.
- Use `filepath` and existing datastore/path helpers for filesystem paths.
  Preserve the distinction between workflow-relative assets, repo-root scripts,
  execution directories, and run-owned output. Check containment at trust
  boundaries, including symlink behavior where the contract requires it.

Use the standard library's [error contracts](https://pkg.go.dev/errors) and
[Effective Go](https://go.dev/doc/effective_go) for language fundamentals;
Effective Go alone does not cover modern modules or generics.

## Concurrency and cancellation

Every goroutine needs an owner, a stop condition, and a way for its owner to
observe completion. Pass `context.Context` as the first parameter for work
that can block; propagate cancellation to subprocesses, reads, sends, and
waits. Existing long-lived owner structs may retain their lifecycle context;
avoid hiding request contexts in unrelated data or replacing them with
`context.Background()` inside a request.

Preserve scheduler ownership rather than adding locks around arbitrary run
state access. Snapshot maps, slices, pointers, and event payloads before
crossing ownership boundaries when they can still be mutated. Value receivers
copy a struct, not its map/slice storage; a shared cache is safe only with an
explicit single-owner or synchronization contract. Do not copy structs
containing mutexes or other synchronization primitives after use.

Keep channels bounded and define their delivery policy. A full live-preview
queue may drop updates; a human answer, security finding, or terminal result
must use the reliable path. Make blocked operations cancellation-aware and
avoid closing channels owned by another producer. Do not hold a mutex while
calling external code or performing potentially unbounded I/O.

When subprocess trees are involved, cancellation must release children and
pipes, not just the immediate parent. Reuse the platform-specific ACP process
helpers. Test shutdown under blocked output, pending questions, and process
exit. For new designs, use [context](https://pkg.go.dev/context) and
[sync](https://pkg.go.dev/sync) contracts rather than timing assumptions.

## Performance and dependencies

Bound inputs before allocating or expanding them: transcript blocks/windows,
JSON and findings files, fan-out items, archives, and queues. Avoid rereading
whole transcripts or rerendering unchanged documents for every message. Use
indexes and caches with explicit invalidation; measure representative input
sizes with benchmarks/profiles before adding complexity.

Use the versions selected in `go.mod` and inspect their actual APIs. Prefer
standard-library features when adequate. A dependency addition must justify
its maintenance and runtime cost; a documentation refresh is not a dependency
upgrade. The root module's local ACP replacement does not remove the nested
module's independent dependency and test boundary.

## Refactoring and completion

Preserve the pre-v1 policy in [AGENTS.md](../AGENTS.md): remove replaced paths
and update consumers in the same change. Separate mechanical movement from
behavior edits when practical so reviewers can see each clearly; this does
not require extra commits or user approval. Move white-box tests with the
package that owns their internals instead of exporting implementation details
for tests. Use external-package tests when exercising the public contract.

Finish with formatting of changed files, applicable tests/vet from
[Testing](TESTING.md), a diff review, and updated contracts/examples. Do not
rewrite unrelated code to make a style preference look universal.
