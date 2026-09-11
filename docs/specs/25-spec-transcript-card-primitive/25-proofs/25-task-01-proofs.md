# Task 01 Proofs - Shared transcript card contract and geometry

## Task Summary

This task establishes the shared card API, semantic state styles, presence-aware padding, centralized border-bar composition, and exact visible-cell geometry without changing ordinary panel behavior.

## What This Task Proves

- Cards preserve rounded frames and exact row widths from defensive sizes through 40, 60, and 90 columns.
- Headers, metadata, and dividers use the specified grammar while preserving caller styling and Unicode grapheme boundaries.
- Pending/running, success, warning, error, invalid-state, and muted-border presentation resolve to the intended semantic styles.
- Existing panels retain their one-dash titled edge, dimensions, and breadcrumb behavior.

## Evidence Summary

The focused shared and root-TUI tests passed. The sanitized gallery records state cases at the three review widths, and repository searches confirm the obsolete `Chat.Bar*` styles have no remaining callers.

## Artifact: Focused component and panel tests

**What it proves:** Geometry, padding, title truncation, divider rules, state styles, and panel regressions all satisfy their executable contracts.

**Why it matters:** These are the reusable primitive's core acceptance conditions and protect the existing panel compositor while it is generalized.

**Command:**

```bash
go test ./internal/tui/shared ./internal/tui -run 'Test(RenderCard|CardContentWidth|Panel|Breadcrumb|TruncateTitle|CardStyles)' -count=1 -v
```

**Result summary:** PASS for both packages, including widths 1, 2, 3, 4, 5, 40, 60, and 90; padding variants; header/divider grammar; every card state; styled grapheme truncation; ordinary panels; and breadcrumbs.

```text
PASS
ok  jig/internal/tui/shared
PASS
ok  jig/internal/tui
```

## Artifact: Synthetic state gallery

**What it proves:** All five states render stable card geometry at 40, 60, and 90 cells with fabricated content.

**Why it matters:** It gives reviewers a direct terminal representation of the shared primitive without exposing run data.

**Artifact path:** `docs/specs/25-spec-transcript-card-primitive/25-proofs/25-task-1-card-gallery.txt`

**Result summary:** Every gallery row matched its requested `lipgloss.Width`; long labels shortened at 40 while rounded corners remained intact.

## Artifact: Obsolete style and formatting checks

**What it proves:** Removed bar styles have no callers, changed Go files are formatted, and the diff has no whitespace errors.

**Commands:**

```bash
rg 'Bar(Thinking|ToolCall|ToolResult|Error)' . --glob '*.go'
gofmt -l internal/tui/shared/card.go internal/tui/shared/card_test.go internal/tui/shared/palette.go internal/tui/shared/panel.go internal/tui/shared/styles.go internal/tui/shared/styles_test.go internal/tui/panel_test.go
git diff --check
```

**Result summary:** All three commands produced no output, indicating no obsolete references, no unformatted listed files, and no whitespace errors.

## Reviewer Conclusion

The shared card surface, semantic styles, and common border compositor are implemented with exact ANSI-aware geometry and protected panel behavior.
