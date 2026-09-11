# 25 Questions Round 1 - Chart Crossing and Gate Labels

Please answer each question below (select one or more options, or add your own notes). Feel free to add additional context under any question.

## 1. Crossing-reduction contract

For a layered workflow DAG, what level of crossing reduction should the chart promise?

- [x] (A) Deterministic heuristic: reorder nodes within each rank to reduce crossings, retain TOML step order as the deterministic tie-breaker, and do not promise the mathematical minimum.
- [ ] (B) Exact minimum: chart layout must find the fewest possible crossings for each workflow, even when that makes layout computation more complex.
- [ ] (C) Preserve author order: do not reorder nodes; limit this work to documenting existing behavior and adding gate labels.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` improves readability for the common fan-out/fan-in workflows while preserving reproducible output and a clear fallback when several orders are equally good.
- `(B)` adds a substantially harder optimization contract and can make performance and test expectations disproportionate to this small TUI improvement.
- `(C)` leaves the stated A25 crossing-min goal unmet.

## 2. Gate-label content and placement

What should a chart label for a deterministic `[step.validate]` gate show, and where should it appear?

- [x] (A) Show the existing compact validation-check description (command, `schema`, `contains "…"`, or `exists`) inside the gated step box, truncated consistently with other chart labels.
- [ ] (B) Show the full validation command/check outside the node beside the gate glyph, allowing chart width to grow as needed.
- [ ] (C) Label review steps using their `[step.review].label` instead of, or in addition to, deterministic validation gates.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` activates the already-derived `gateLabel` value without changing the chart's compact fixed-box model or horizontal-scroll contract.
- `(B)` makes long validation commands dominate the diagram and introduces a second external-label layout rule.
- `(C)` is a separate semantic expansion: review labels and validation-gate checks represent different workflow concepts and should be planned explicitly if both are wanted.
