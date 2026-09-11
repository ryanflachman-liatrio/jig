# Task 01 Proofs - Shared status-line grammar, icon resolver, and styles

## Task Summary

This task ships the reusable `internal/tui/shared` four-slot status-line
primitive, the `(state, kind) → (glyph, style)` icon resolver, the per-tool
signature glyph vocabulary, and the `Chat.Tool*` styles. It closes FR-02.1
through FR-02.10 at the primitive layer without touching Monitor code.

## What This Task Proves

- Every slot renders independently and composes with the omp separator
  rules (icon + title + ": " + description + " " + badge + " " + meta joined
  with " · ").
- Empty and whitespace-only meta entries are dropped so a row never ends in
  a dangling separator.
- Every slot flattens CR/LF to a space so a card frame's one-row invariant
  cannot be smuggled around by a caller.
- `RenderStatusLine` never truncates; card frames own width clipping.
- `ToolStatusIcon` returns the same glyph for running and pending (CC-4)
  and the kind's signature glyph only on settled success with a known kind.
- Every new glyph (`IconStatus*` and `IconTool*`) measures exactly one cell,
  so the enclosing card frame's width invariant is preserved.
- The per-state icon style shares its foreground with the corresponding
  `Card.Border*` style so the header icon and the card frame read as one
  indicator of state.
- No renderer file introduced by this slice contains a bare
  `lipgloss.NewStyle()` or a bare hex literal; all styles pass through
  `styles.go` and all glyphs through `icons.go` (CC-7).

## Evidence Summary

Focused Go tests in `internal/tui/shared` cover every FR at the primitive
layer. A JIG_UI_SNAPSHOT_DIR-gated gallery test emits a deterministic
plain-text catalog of every state × kind combination this slice ships.

## Artifact: Status-line primitive tests

**What it proves:** Slot combinations, separator rules, filter rules,
newline flattening, per-slot styling, no-truncation contract, and grapheme
+ ANSI preservation all behave as specified.

**Why it matters:** These tests are the authoritative contract for every
Monitor caller and every future consumer (slices 05, 07, 08, 13, 15).

**Command:**

```bash
go test ./internal/tui/shared -run 'TestRenderStatusLine' -count=1 -v
```

**Result summary:** PASS. Cases include `TestRenderStatusLineSlotFiltering
AndSeparators`, `TestRenderStatusLineNoTruncation`,
`TestRenderStatusLineFlattensNewlines`, `TestRenderStatusLinePerSlotStyling`,
`TestRenderStatusLineIconStylingIsOptional`, and
`TestRenderStatusLinePreservesGraphemesAndANSI`.

## Artifact: Icon resolver and vocabulary tests

**What it proves:** Every documented `(state, kind)` combination maps to
the correct glyph and style; running/pending share their glyph;
success-with-unknown-kind falls back to the generic bullet; every icon
glyph is single-cell.

**Why it matters:** The anti-jitter rule (CC-4) is enforced at the
vocabulary layer so no caller can accidentally introduce a per-frame glyph
swap.

**Command:**

```bash
go test ./internal/tui/shared -run 'TestToolStatusIcon' -count=1 -v
```

**Result summary:** PASS. Cases include `TestToolStatusIconStateMapping`,
`TestToolStatusIconRunningAndPendingShareGlyph`,
`TestToolStatusIconSignatureGlyphOnSuccessKnownKind`,
`TestToolStatusIconSuccessUnknownKindFallsBackToGeneric`,
`TestToolStatusIconRunningIgnoresKind`, and
`TestToolStatusIconSignatureGlyphsAreSingleCell`.

## Artifact: Renderer discipline tests

**What it proves:** No file introduced by slice 02 contains a bare
`lipgloss.NewStyle()` or hex literal, and every new status/tool glyph is
declared in `icons.go`.

**Why it matters:** These tests catch a regression at the review layer
before it can propagate through the codebase.

**Command:**

```bash
go test ./internal/tui/shared -run 'TestSlice02' -count=1 -v
```

**Result summary:** PASS. Cases include
`TestSlice02RenderersUseCentralizedTheme` and
`TestSlice02IconGlyphsAreCentralized`.

## Artifact: Deterministic status-line gallery

**What it proves:** Every state × kind combination this slice ships
composes to the expected plain-text row and measures the expected number
of cells.

**Why it matters:** A reviewer can eyeball every combination at once and
confirm the icon slot always carries state, no row ends in a dangling
separator, and the error meta segment is present on every error row.

**Artifact path:**
`docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-1-status-line-gallery.txt`

**Command:**

```bash
JIG_UI_SNAPSHOT_DIR=docs/specs/25-spec-status-line-header-grammar/25-proofs \
  go test ./internal/tui/shared -run '^TestStatusLineGallery$' -count=1
```

**Result summary:** PASS. The gallery test is skipped in ordinary test runs
and writes the deterministic catalog only when `JIG_UI_SNAPSHOT_DIR` is
set. Values in every row are fabricated; no real transcript data or
credentials are read.

## Reviewer Conclusion

The shared status-line primitive, icon resolver, glyph vocabulary, and
`Chat.Tool*` styles are complete, tested, and disciplined. Every FR from
02.1 through 02.10 has an executable assertion, and every glyph and style
passes through the centralized owner packages (`icons.go`, `styles.go`,
`palette.go`).
