# Phase 2 — Remaining friction

Unify review chrome, quiet titles, progressive disclosure, and dogfood.

**Status:** Implemented (2026-09-05) — see [proofs/](proofs/).

| Item | Title | Visual | File |
|---|---|---|---|
| 2.1 | Unify review panel chrome | Yes | [2.1-unify-review-panel-chrome.md](2.1-unify-review-panel-chrome.md) |
| 2.2 | Breadcrumb truncation | Yes | [2.2-breadcrumb-truncation.md](2.2-breadcrumb-truncation.md) |
| 2.3 | Simple mode | Yes | [2.3-simple-mode.md](2.3-simple-mode.md) |
| 2.4 | Golden-path demo & dogfood | Process | [2.4-golden-path-demo-dogfood.md](2.4-golden-path-demo-dogfood.md) · [../golden-path/](../golden-path/) |

## Exit criteria

- Review feels like Monitor on wide terminals (≥160); full-width below that
- Titles stay short; status line carries LIVE/unseen/cost overflow
- New operators default into simple mode (persisted in `.jig/tui.json`)
- Deterministic golden-path script catches regressions

## Suggested order

`2.1` after `0.4` + `1.2`. `2.2` after `1.1` + `1.2`. `2.3` after `0.3` + `1.3`.
`2.4` last (or continuous after Phase 0) with a deterministic demo workflow.

One item ≈ one PR.
