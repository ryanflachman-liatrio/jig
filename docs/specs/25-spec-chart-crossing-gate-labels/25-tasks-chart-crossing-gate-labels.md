# 25 Tasks - Chart Crossing and Gate Labels

## Planning Context

### Standards Evidence

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Pure layout/testing seams; shared TUI theme only; `gofmt`, Go tests, and `go vet` are required checks. | none |
| `README.md` | yes | jig is deterministic; workflow schema validates static graphs before execution; Go 1.25 is required. | none |
| `go.mod` | yes | Go 1.25.12; Bubble Tea and Lip Gloss v2 are the established TUI dependencies. | none |
| `CONTRIBUTING.md` | not found | No contribution-specific policy available. | none |
| `.github/pull_request_template.md` | not found | No PR-template requirements available. | none |
| `.golangci.yml` | not found | No additional linter configuration available. | none |
| `.github/workflows/ci.yml` | not found | No single-file CI policy at this path. | none |

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/chart/layout.go` | Owns longest-path ranks, step-index ordering, forward edges, and gate-label derivation; the deterministic crossing heuristic belongs here. |
| `internal/tui/chart/layout_test.go` | Unit-tests pure chart topology and must prove reduced crossings, rank preservation, stable ties, and excluded back-edge semantics. |
| `internal/tui/chart/render.go` | Owns fixed node dimensions, connector anchors, node composition, truncation, and shared-theme rendering of gate labels. |
| `internal/tui/chart/render_test.go` | Owns ANSI-stripped golden assertions and narrow-width/foreach rendering tests. |
| `internal/tui/chart/testdata/*.golden` | Checked-in readable chart evidence; affected fixtures must be intentionally regenerated and reviewed. |
| `internal/tui/detail/view.go` | Integrates the chart with the Detail viewport; expected to remain behaviorally unchanged but defines the horizontal-scroll contract. |
| `internal/tui/detail/detail_test.go` | Covers Detail list/chart toggling and horizontal-scroll key bindings that must remain valid. |
| `internal/tui/shared/styles.go` | Defines the shared theme singleton that chart labels must continue to use; inspect-only unless an existing style proves inadequate. |

### Notes

- Keep crossing reduction in the pure layout package; do not let renderer geometry alter ordering decisions.
- Preserve original step index as the final comparison key so layout remains stable across runs and golden fixtures.
- Run golden regeneration only after tests encode the intended topology; review every fixture diff before retaining it.
- No workflow schema, execution engine, or runtime foreach changes are planned.

## Tasks

### [x] 1.0 Implement deterministic within-rank crossing reduction

Deliver a bounded, deterministic layout policy for forward dependency edges that reorders only nodes sharing a longest-path rank. Preserve ranks, step indices for tie-breaking, conditional-edge semantics, and separately routed bounded back-edges.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/chart -run TestLayoutChart` passes with a crossing-prone multi-rank fixture proving fewer forward-edge crossings while ranks and edge endpoints remain unchanged.
- Test: `go test ./internal/tui/chart -run TestLayoutChart` passes repeated-layout and equal-score tie cases proving byte-stable rank order and TOML-order tie-breaking.
- Test source: `internal/tui/chart/layout_test.go` maps to Unit 1 requirements for restricted rank reordering, determinism, unchanged forward-edge semantics, and stable no-crossing layouts.

#### 1.0 Tasks

- [x] 1.1 Define a pure, bounded crossing-reduction helper in `internal/tui/chart/layout.go` that accepts ranked node indices and forward edges, compares only nodes in the same rank, and retains the original step index as its final deterministic tie-breaker.
- [x] 1.2 Apply the helper after longest-path rank buckets are built, using only forward `depends_on` topology; retain conditional-edge decoration and exclude route back-edges from the ordering objective.
- [x] 1.3 Add table-driven layout tests in `internal/tui/chart/layout_test.go` for a crossing-prone graph, a graph with no reducible crossing, and equal-score ties. Assert rank membership, edge endpoints, stable repeated order, and unchanged route-back-edge classification.

### [x] 2.0 Render compact deterministic validation-gate labels

Extend chart node geometry and rendering so every `[step.validate]` form displays its established compact gate-check description inside the node, without displacing existing markers or bypassing theme ownership.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/chart -run 'Test.*Gate|Test.*Node'` passes table-driven coverage for command, schema, contains, and exists labels; it demonstrates marker composition and Unicode-aware truncation.
- Golden fixture: `internal/tui/chart/testdata/conditional_loop.golden` or a new focused fixture, regenerated and asserted through `go test ./internal/tui/chart -run TestChartGolden`, visibly demonstrates an in-node validation label and unchanged conditional/loop labels.
- Test source: `internal/tui/chart/render_test.go` maps to Unit 2 requirements for label visibility, validation-form coverage, bounded geometry, and theme-consistent node rendering.

#### 2.0 Tasks

- [x] 2.1 Adjust chart box geometry and connector-anchor calculations in `internal/tui/chart/render.go` to accommodate a bounded validation-label line while keeping all node boxes uniform and the chart vertically deterministic.
- [x] 2.2 Render a gated node's existing `gateLabel` inside the node using the shared chart/node theme styles and `shared.TruncateTitle`; preserve the ID, type/gate glyph, loop, retry, and foreach annotations without introducing hard-coded styling.
- [x] 2.3 Add focused renderer tests in `internal/tui/chart/render_test.go` for command, schema, contains, and exists validation forms, including a Unicode/long-label truncation case and combined gate-plus-loop/retry/foreach marker behavior.

### [x] 3.0 Verify Detail-chart behavior and chart regressions

Update fixed-width chart evidence and retain Detail-screen viewport behavior after the layout and node-height changes. Prove narrow charts, static foreach nodes, conditional labels, route back-edges, and non-gated nodes remain within the A25 boundary.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/chart ./internal/tui/detail` passes focused chart and Detail integration coverage, demonstrating the existing horizontal-scroll-compatible render path and defensive no-workflow fallback remain intact.
- Regression suite: `go test ./...`, `go vet ./...`, and `gofmt -l .` return success/empty formatting output after all updated golden fixtures are reviewed.
- Golden fixture: `internal/tui/chart/testdata/` contains updated ANSI-stripped fixtures for normal, conditional, loop, foreach, and wide-label charts, demonstrating no unrelated rendering regressions.

#### 3.0 Tasks

- [x] 3.1 Add or update fixed-width golden workflow cases in `internal/tui/chart/render_test.go`, then regenerate only the affected files in `internal/tui/chart/testdata/`; review that conditional forward labels, route back-edge labels, non-gated nodes, and static foreach markers remain visible and correctly placed.
- [x] 3.2 Extend `internal/tui/chart/render_test.go` narrow-width coverage and `internal/tui/detail/detail_test.go` chart-mode coverage as needed to prove no node/label clipping, no panic, the existing horizontal-scroll contract, and the defensive unavailable-workflow fallback.
- [x] 3.3 Run `go test ./internal/tui/chart ./internal/tui/detail`, `go test ./...`, `go vet ./...`, and `gofmt -l .`; record the exact passing commands and any intentionally updated golden fixtures in the task proof artifact created during implementation.
