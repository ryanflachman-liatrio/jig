# 25-spec-chart-crossing-gate-labels.md

## Introduction/Overview

The workflow-detail chart is a deterministic, top-down rendering of a workflow's static dependency DAG. It currently keeps nodes in TOML file order within a rank, even when that produces avoidable connector crossings, and it derives a compact `[step.validate]` description without displaying it. This feature makes complex workflow charts easier to read by deterministically reducing crossings and by displaying a concise validation-gate label in each gated node.

## Goals

- Reduce avoidable crossings between adjacent chart ranks with a deterministic heuristic.
- Preserve reproducible chart output: equivalent inputs always produce the same rank ordering and rendered result.
- Make the check performed by a deterministic `[step.validate]` gate visible in its node.
- Preserve the existing chart's horizontal-scroll behavior, static-DAG scope, shared-theme styling, and readable narrow-width fallback.

## User Stories

- **As a workflow author**, I want dependency lines to cross less often so that I can understand fan-out and fan-in relationships at a glance.
- **As an operator**, I want to see what validation a gated step performs so that I can assess a workflow before running it.
- **As a contributor**, I want chart layout to remain deterministic so that tests, screenshots, and reviews do not change unpredictably.

## Demoable Units of Work

### Unit 1: Deterministic crossing-reduced rank order

**Purpose:** Render static workflow dependencies with fewer avoidable crossings while retaining stable output for authors and tests.

**Functional Requirements:**

- The system shall reorder nodes only within their existing longest-path rank; it shall not change workflow execution semantics, dependency edges, ranks, or TOML data.
- The system shall use a deterministic crossing-reduction heuristic for forward `depends_on` edges, rather than promise a mathematical global minimum.
- The system shall use original TOML step order as the stable tie-breaker when candidate orders are equally ranked by the heuristic.
- The system shall retain the existing distinct treatment of conditional edges and bounded route back-edges, including their labels and connector styles.
- The system shall leave a workflow with no reducible crossings in a stable, deterministic order.

**Proof Artifacts:**

- Test: layout tests demonstrate that a crossing-prone fan-out/fan-in workflow is reordered to reduce crossings while preserving ranks and edges.
- Test: repeated layout/render assertions demonstrate identical output for the same workflow and deterministic TOML-order tie handling.
- Golden chart: an ANSI-stripped chart fixture demonstrates the improved connector topology alongside existing conditional and loop rendering.

### Unit 2: Compact validation-gate labels

**Purpose:** Let an operator identify a deterministic validation check without leaving the workflow chart.

**Functional Requirements:**

- The system shall display the existing compact validation-check description inside every node that declares `[step.validate]`.
- The system shall represent command, output-schema, output-contains, and output-exists validation forms with the established compact descriptions.
- The system shall truncate gate-label text using the chart's existing width and Unicode-aware title-truncation conventions so that node geometry remains bounded.
- The system shall retain the existing gate glyph and preserve all existing node markers for loops, retries, and foreach families when space permits.
- The system shall style gate labels exclusively through the `internal/tui` shared theme singleton.

**Proof Artifacts:**

- Test: table-driven node-rendering coverage demonstrates each validation form's compact label, truncation, and marker composition.
- Golden chart: a fixed-width fixture demonstrates a validation command label in a gated node without clipping the chart or changing unrelated labels.

### Unit 3: Detail-chart integration and regression safety

**Purpose:** Keep chart mode usable in the workflow Detail screen at normal and narrow terminal widths.

**Functional Requirements:**

- The system shall continue to render the chart through the existing Detail-screen viewport and horizontal-scroll escape hatch when natural chart width exceeds the viewport.
- The system shall continue to return an empty chart for workflows with no steps and retain the defensive flat-list fallback for an unavailable workflow.
- The system shall not add workflow TOML fields, alter workflow validation, or expose runtime-expanded foreach children in the static author chart.
- The system shall preserve existing chart visual behavior for conditional forward labels, route back-edge labels, node type colors, and non-gated nodes.

**Proof Artifacts:**

- Test: existing chart package tests plus focused narrow-width coverage demonstrate bounded node geometry and horizontal-scroll-compatible output.
- Test: Detail-screen tests demonstrate chart-mode integration remains functional for a valid workflow.

## Non-Goals (Out of Scope)

1. **Exact global crossing minimization:** The chart will use a deterministic heuristic, not an exhaustive optimization algorithm or a guarantee of the fewest possible crossings.
2. **Workflow semantics or schema changes:** This feature will not add fields, change dependency execution order, or modify validation-gate execution.
3. **Runtime graph visualization:** Foreach families remain one static author node; runtime child instances and live execution state are not added to this chart.
4. **New review-step labels:** Displaying `[step.review].label` is separate from labeling deterministic `[step.validate]` gates.
5. **Mouse interaction or exported graph formats:** This work does not add chart selection, clickable rows, Mermaid/DOT/SVG export, or a new chart surface.

## Design Considerations

- Keep the existing top-down box-and-connector composition and use the Detail viewport's horizontal scrolling for charts wider than the terminal.
- Gate labels belong inside the gated node, below or alongside its existing compact type/marker content as permitted by the bounded box layout; they must remain visually subordinate to the step ID.
- The UI must use `internal/tui/shared` theme styles only. No hard-coded colors or ad-hoc Lip Gloss styling may be introduced.
- The chart must remain legible with Unicode-aware widths and retain its current labels and glyph vocabulary.

## Repository Standards

- Keep the pure chart layout pass separate from Lip Gloss rendering so deterministic topology is unit-testable.
- Add focused Go unit tests and update/add ANSI-stripped golden fixtures in `internal/tui/chart/testdata` for observable rendering changes.
- Preserve `internal/tui` package ownership and the shared theme singleton for all TUI styling.
- Format Go with `gofmt`; validate with relevant package tests and the repository-wide Go test/vet suite before handoff.
- Keep examples valid and do not introduce compatibility shims or unrelated schema changes.

## Technical Considerations

- `internal/tui/chart/layout.go` already constructs longest-path ranks, preserves TOML-order rank buckets, and holds a derived `gateLabel`; the implementation should replace only the rank-order policy and connect the existing label to node rendering.
- The crossing heuristic must be deterministic, bounded, and based solely on the validated static forward DAG. Original step index must break all otherwise equal ordering decisions.
- Existing route back-edge channels are deliberately separate from the forward DAG layout. They must not be reinterpreted as ordinary forward edges during crossing reduction.
- The renderer uses a fixed box height today. The implementation must deliberately adjust its box geometry and connector anchors if a third text line is required, then update all affected golden fixtures and narrow-width checks.
- No latest-standards research was needed: this is an internal Go terminal-rendering improvement with no externally governed protocol, security, or interoperability requirement.

## Security Considerations

No new credentials, external I/O, network access, or persistent data are introduced. Validation commands may contain sensitive text; use the existing compact/truncation behavior and do not add new logging or export paths for full command content.

## Success Metrics

1. **Crossing reduction:** A deterministic crossing-prone test workflow renders with fewer forward-edge crossings than the current TOML-order baseline.
2. **Determinism:** Repeated layout and render tests for the same workflow produce byte-identical ANSI-stripped output.
3. **Gate visibility:** All four supported `[step.validate]` forms have test coverage proving their compact label appears in the corresponding chart node.
4. **Regression safety:** `go test ./internal/tui/chart ./internal/tui/detail`, `go test ./...`, `go vet ./...`, and `gofmt -l .` pass.

## Open Questions

No open questions at this time.
