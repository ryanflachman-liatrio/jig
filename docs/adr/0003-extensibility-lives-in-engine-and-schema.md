# Extensibility lives in the engine and schema, not the TUI

jig's open extension seam already exists in the domain layer: the `engine.Executor`
interface plus `runner.Mux`, a `map[StepType]Executor` registry that `cmd/jig`
populates at startup (and which degrades gracefully — a failed result, not a crash —
for an unregistered type). The `.toml` workflow schema is the user-facing extension
surface. We decided that new capability plugs in at those two layers, and the TUI
stays a thin, data-driven renderer over the resulting domain model.

We explicitly reject a pluggable-UI architecture (runtime-loaded panels or
renderers contributed by third-party code). It is a VS-Code-scale commitment whose
cost would dwarf jig itself, and it is unnecessary: the open set lives in the
domain — step types, executors, gate kinds — not in the view. "Make the TUI
extensible" with no named plugin is speculative generality, the most expensive form
of hardcoding.

## Consequence

When the domain grows (a new step type or gate kind), the correct move is to render
it from model data through a small set of rendering contracts — never a per-type
branch in the TUI, and never a UI plugin API. Extensibility is treated as the
measured cost of the next concrete change, not a framework built ahead of demand.
