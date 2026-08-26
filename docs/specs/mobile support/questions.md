# Clarification questions: mobile support

The research establishes a presentation-only narrow-terminal feature across
the existing Jig TUI. Please answer the decisions below; each changes the
scope or acceptance criteria of the specification.

## 1. Narrow-terminal support contract

Which terminal target should the implementation and proof artifacts promise?

- **Cell-width responsive support for existing TUI surfaces, tested at a small set of fixed widths, with graceful degradation below the minimum tested width (recommended).** This directly serves Termux and other narrow terminals while keeping the feature presentation-only and deterministic.
- Termux-specific integration, including Android command, storage, or session behavior. This would materially expand the feature beyond the researched presentation scope.
- General responsive behavior without a declared minimum width or fixed-width test matrix. This is less prescriptive and makes acceptance and regression review difficult.

## 2. Sanitization and persisted transcripts

Should the feature sanitize untrusted output only when rendering it, or also alter what is persisted?

- **Sanitize at the display boundary and leave existing file-backed persistence and persistence-off behavior unchanged (recommended).** This preserves transcript truth and avoids a data-layer redesign while guaranteeing safe terminal rendering; any stronger persistence policy can be specified separately.
- Sanitize or redact output before writing transcripts. This improves stored-data safety but changes the current persistence contract and transcript fidelity.
- Disable, truncate, or selectively redact raw command output persistence. This has the largest security and operational impact and needs retention rules that are not currently defined.

## 3. Dependency and Go-version scope

What dependency and language-version policy should the spec use?

- **Keep the current Go baseline and dependencies unless an implementation blocker is demonstrated; treat any required upgrade as a separately approved scope change (recommended).** This keeps the feature focused and makes the resulting diff and validation easier to review.
- Permit compatible Bubble Tea/lipgloss or related dependency upgrades as part of the feature. This may simplify rendering work but adds compatibility and regression risk.
- Raise the Go baseline or perform broad dependency modernization. This could unlock APIs but is disproportionate to a presentation-only narrow-terminal feature.

## Decisions

The recommended options were selected:

1. Support cell-width-responsive layouts across the existing TUI, with fixed-width proof cases and graceful degradation below the minimum tested width.
2. Sanitize untrusted content at the display boundary while preserving existing file-backed persistence and persistence-off behavior.
3. Keep the current Go baseline and dependencies unless an implementation blocker is demonstrated; any required upgrade is a separately approved scope change.
