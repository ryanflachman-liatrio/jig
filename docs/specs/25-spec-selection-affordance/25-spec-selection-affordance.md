# 25-spec-selection-affordance.md

## Introduction/Overview

Fix the Monitor Transcript's block-cursor so moving between transcript
items changes only color and glyph — never the horizontal position of any
content. Today the selected row's prefix is two columns *wider* than the
unselected row's prefix (`"  "` → `"  " + "▌ "`), so every `n`/`N` press
slides the tool-card frame two columns to the right and back again. The
detail body writers use their own fixed six- and four-column indents, so
the header shifts while the body does not, which reads as a rendering bug.

This slice keeps the current visual vocabulary: a `▌` bar in the cursor
gutter, colored with `Theme.SelectedBar`. It just makes the selected and
unselected prefixes occupy the same two visible columns.

Source: [OMP transcript parity, slice 03](../../epics/omp-transcript-parity/slices/03-selection-affordance.md).
Depends on: none — slice 03 is explicitly independent of the card work.
The current implementation already renders tool-exchange headers inside
the slice 01 card frame (see
[docs/specs/25-spec-transcript-card-primitive](../25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md)),
so this slice measures success against the framed rendering rather than
the pre-card indented rows described in the epic. This document is
Phase 1 output; it does not authorize implementing any other epic slice.

## Goals

- **G-03.a** Cursor movement produces zero horizontal displacement of any
  transcript content, measured after stripping ANSI.
- **G-03.b** Selection stays visually distinct: a colored `▌` bar occupies
  the cursor gutter of the selected row and no other rows.
- **G-03.c** The number of lines any transcript item occupies is identical
  whether or not it is the selected item, so `chatItemLineRanges` stays
  stable across cursor movement and `n`/`N` block navigation cannot desync
  scroll-to-item.
- **G-03.d** Tool-exchange header cards use the same content width whether
  selected or not, so the slice 01 card cache does not have to hold two
  variants per item and the frame stops re-flowing on selection.
- **G-03.e** The fix routes through the shared glyph vocabulary
  (`shared.CursorBar`, `Theme.SelectedBar`) with no new hardcoded `"▌"`
  literal added at the Monitor call site, satisfying epic CC-7.

## User Stories

- **As an operator scanning a transcript with `n`/`N`**, I want cards and
  detail bodies to stay in a single vertical column so I can track cursor
  motion by color alone without my eyes chasing content sideways.
- **As an operator paging through a long run**, I want the selected item
  to always occupy the same number of lines it would if unselected, so
  scroll position and `n`/`N` boundaries do not shift under me when the
  transcript re-renders around the current selection.
- **As a maintainer completing later epic slices**, I want the transcript
  cursor gutter to have a stable, fixed-width contract so slice 04
  (vertical rhythm) and slice 05 (tool detail sections) can reason about
  card width and body indent without a selection-dependent branch.

## Demoable Units of Work

### Unit 1: Fixed-width cursor gutter for the transcript items view

**Purpose:** Make the selected and unselected transcript rows occupy the
same visible columns, using the shared cursor glyph and the existing
selected-bar style. Cover the change with tests that assert prefix width
equality, unchanged line accounting, and unchanged card width across
selection states — then hold the audit result for the detail writers so
the invariant is documented, not just observed.

**Functional Requirements:**

- **FR-03.1** The Monitor's transcript items view shall render the same
  fixed-width leading prefix on every item regardless of selection, so the
  visible width of the selected-state prefix equals the visible width of
  the unselected-state prefix, measured with `lipgloss.Width` after
  stripping ANSI. The selected prefix shall consist of one `▌` glyph
  styled via `Theme.SelectedBar` followed by one space; the unselected
  prefix shall be two spaces.
- **FR-03.2** Selection shall be indicated in that fixed-width prefix
  using the shared cursor glyph. The Monitor shall reference
  `shared.CursorBar` (not a bare `"▌"` string literal) so slice 14's glyph
  preset table can retheme the cursor without editing the transcript
  renderer.
- **FR-03.3** Body, detail, and code-card lines shall render at the same
  horizontal offset regardless of selection. Existing detail writers
  (`writeItemDetail`, `writeNewCodeCards`, the write-time truncation
  hint, and the header-card frame) shall continue to draw at their
  current fixed indents; no writer shall gain a selection-dependent
  indent.
- **FR-03.4** The rendered line count of a transcript item shall be
  identical whether or not the item is selected, so `chatItemLineRanges`
  entries computed by `itemTranscriptBody` are stable across cursor
  movement. `n`/`N` block navigation and scroll-to-item shall not drift
  when only the selection changes.
- **FR-03.5** Tool-exchange header cards produced by
  `renderToolExchangeCard` shall use the same `available` content width
  whether or not the item is selected. The slice 01 card cache shall
  therefore hold at most one card render per `(item, width, expanded,
  state, header)` tuple across selection changes; per-selection cache
  variants shall not accumulate.
- **FR-03.6** The change shall not add any new package-level style
  variable, new hex constant, new glyph literal, or new theme mutation at
  a renderer call site. `Theme.SelectedBar` and `shared.CursorBar` are
  the only presentation surfaces this slice touches; other transcript
  styles remain untouched.

**Proof Artifacts:**

- Monitor behavioral test in
  `internal/tui/monitor/monitor_transcript_items_view_test.go` that
  renders the same synthetic page twice, once with the cursor on item *i*
  and once on item *j*, strips ANSI from both, and asserts every line
  begins at the same visible column. Cover at least one item of each
  kind that has a leading prefix today: text, system, thinking,
  unsupported, tool-exchange header card, and orphan tool-result row.
- Monitor behavioral test asserting `lipgloss.Width` of the selected
  prefix equals `lipgloss.Width` of the unselected prefix on every
  rendered item and equals exactly two visible cells, for widths of at
  least 40 and 80 (the epic's small-terminal target and a comfortable
  default).
- Monitor behavioral test asserting the `chatItemLineRanges` entry for a
  given item is identical whether or not it is the selected item, using
  the existing table-driven synthetic page and the same fixture reused
  from the slice 01 card cache tests.
- Monitor behavioral test asserting `renderToolExchangeCard` produces
  card output of the same visible width in selected and unselected
  states, and that the card cache does not grow when selection alone
  changes on an already-rendered item.
- Grep-based regression assertion (executed as a Monitor package test)
  showing that after the change `internal/tui/monitor/*.go` contains no
  bare `"▌"` literal used to build the transcript-item prefix. The Steps
  panel's `shared.Theme.SelectedBar.Render(shared.CursorBar)` calls in
  `monitor_steps.go` remain the reference pattern.
- Terminal capture at 80 columns of the Monitor with a synthetic page
  containing a text item, a running tool exchange, a settled-success
  tool exchange, and an errored tool exchange. Two files: cursor on the
  first item, cursor on the third item. The two captures shall be
  byte-identical after stripping ANSI *except* for the two-cell prefix
  content (bar vs. spaces). Recorded to
  `docs/specs/25-spec-selection-affordance/25-proofs/` alongside a
  `.notes.txt` recording terminal size and cursor position.
- Root acceptance checks: `go build ./cmd/jig`, `go test ./...`, `go
  vet ./...`, `gofmt -l <changed-go-files>`, and `git diff --check`
  captured for the change. These are implementation acceptance checks,
  not results claimed by this Phase 1 document.

## Non-Goals (Out of Scope)

1. **The dotted-outline overlay** (slice 03 Option B —
   `╭┄ … ┄╮ / ┆ … ┆ / ╰┄ … ┄╯` wrapping the selected item). This slice
   ships Option A only. Option B is deferred pending Q-03.1 resolution
   and would compose a second frame around the slice 01 card, so it
   requires an explicit later design pass.
2. **Selection semantics or navigation keys.** `n`/`N` behavior, the
   `chatItemCursor` model field, and which item is initially selected
   are unchanged. Only the horizontal presentation of the currently
   selected item's prefix changes.
3. **The Steps panel cursor.** `SelectedLine` is a full-row highlight
   that does not shift content and is not part of this defect. The three
   `shared.Theme.SelectedBar.Render(shared.CursorBar)` call sites in
   `internal/tui/monitor/monitor_steps.go` are the reference pattern,
   not targets of this change.
4. **A caption on the selected item** (omp's `3 blocks →` /
   `enter expand` overlay). jig surfaces these in the footer help line;
   duplicating them into the transcript is Q-03.2, deferred and not part
   of this slice.
5. **Any other epic slice.** No header-grammar change (slice 02), no
   detail-section conversion (slice 05), no truncation-vocabulary edit
   (slice 06), no diff renderer (slice 07), no grouped-read tree (slice
   08), no vertical-rhythm change (slice 04), no message framing (slice
   09), no glyph-preset table (slice 14).
6. **Removal of the two-space unselected gutter.** The two-cell gutter
   is the shared contract between transcript items and the header-card
   `prefix` argument. Reducing it to zero is a separate design change
   that would ripple into `renderToolExchangeCard`, `writeNewCodeCards`,
   `writeItemDetail`, and the write-time truncation hint.

## Design Considerations

The transcript items view currently composes a per-item prefix as:

```
prefix := "  "
if selected {
    prefix += shared.Theme.SelectedBar.Render("▌") + " "
}
```

The `+=` on the selected branch is the entire mechanical defect: the
selected prefix carries the two-cell unselected gutter *plus* the two
new cells of bar-and-space, so every cursor move slides content two
columns sideways. The fix replaces `+=` with `=` and uses
`shared.CursorBar` in place of the inline `"▌"` literal:

```
prefix := "  "
if selected {
    prefix = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
}
```

Both branches now render as exactly two visible cells (measured with
`lipgloss.Width` after stripping ANSI). `Theme.SelectedBar` is a bold
Charple foreground; the space after `▌` matches the two-space gutter
column-for-column.

The detail writers already draw at fixed indents that do not depend on
selection:

- `writeItemDetail` prepends `"      "` (six spaces) to its label,
  content, and hidden-lines hint (`monitor_transcript_items_view.go:200`).
- `writeNewCodeCards` prepends `"    "` (four spaces) to its label and
  each code-card row (`monitor_transcript_items_view.go:260`).
- The write-time truncation hint under an expanded tool-exchange
  prepends `"      "` (six spaces) at
  `monitor_transcript_items_view.go:89`.
- `renderToolExchangeCard` calls `prefixCardRows(prefix, card)`, which
  applies the item's prefix once per card row
  (`monitor_transcript_items_view.go:189-194`). Under the fix, every
  card row still carries the current item's two-cell prefix, but its
  width is now stable across selection.

The regression the audit protects against is a future writer that adds
its own selection-conditional indent. FR-03.3 captures that as a
functional requirement; the tests below cover it by asserting every
rendered line begins at the same column regardless of selection.

`renderToolExchangeCard` currently computes `available :=
m.transcriptInnerW - lipgloss.Width(prefix)` and stores `available` in
the card cache key (`transcriptRenderKey.width`). Today `available` is
`transcriptInnerW - 4` when selected and `transcriptInnerW - 2` when
unselected, so the cache carries a distinct width variant per selection
state. Under the fix `available` becomes `transcriptInnerW - 2`
independent of selection; the cache holds one entry per item.
`transcriptRenderKey.selected` remains a cache key component because
selection can still change the composed `header` string (slice 02 wraps
the title in `TranscriptSelected`), so cache identity still needs to
discriminate selection — the cost saved is the width variant, not the
selection variant. FR-03.5 makes this explicit.

Existing tests that assert `"  ▌ ╭"` and `"\n  ▌ ╰"` prefixes
(`monitor_transcript_items_view_test.go:93,254`) encode the pre-fix
four-cell selected prefix. Those assertions become `"▌ ╭"` and
`"\n▌ ╰"` after the change and must be updated in the same commit.

### omp reference

`packages/coding-agent/src/modes/components/transcript-outline.ts:163-214`
in oh-my-pi solves the same problem by placing the selection outline in
a *separate fullscreen overlay*, not the live transcript. The live
transcript has no cursor at all. jig cannot copy that pattern without
retiring its per-item block cursor, which is out of scope for this
slice. omp's independent lesson — the selected and unselected paths are
built to occupy identical columns — is the invariant this slice ports:
`"┆ "` (selected) and `"  "` (unselected) are both two columns in omp;
`"▌ "` (selected) and `"  "` (unselected) are both two columns after
this fix.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md),
  [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), and [Testing](../../TESTING.md).
- Use `shared.CursorBar` for the cursor glyph and
  `Theme.SelectedBar` for its style. Do not introduce a new package-level
  `var …Style = lipgloss.NewStyle()` or a new hex constant; palette
  ownership stays in `internal/tui/shared/palette.go` and `styles.go`.
- Do not add a compatibility wrapper, environment alias, or dual code
  path for the old four-cell selected prefix. This is a pre-v1
  correctness fix: delete the buggy `+=` in the same change (AGENTS.md,
  non-negotiable design constraints — pre-v1).
- Measure display cells with `lipgloss.Width` and strip ANSI with the
  Monitor's existing `stripANSI` helper before column assertions. Do not
  count runes or bytes to reason about visible width.
- Use table-driven synthetic transcript fixtures (`syntheticExchange`,
  `newMonitorWithSteps`, `setChatPage`). Do not read real `.jig/` data
  for fixtures or proofs.
- Format only changed Go files with `gofmt -w`. Do not rewrite unrelated
  files merely to run a formatting check.
- Root acceptance is `go build ./cmd/jig`, `go test ./...`, `go vet
  ./...`, `git diff --check`, plus a targeted TUI race
  (`go test -race ./internal/tui/...`) matching the pattern established
  by slice 02.

## Technical Considerations

### Scope of the code change

Exactly one production file needs a mechanical edit:

- `internal/tui/monitor/monitor_transcript_items_view.go` — replace
  the selected-branch `+=` with `=`, and swap the inline `"▌"` literal
  for `shared.CursorBar` (also removes the CC-7 debt this call site
  currently carries).

One production file needs a stale comment update:

- `internal/tui/monitor/monitor_layout.go:252` — the `insetWidth := wordWrap - 4`
  comment reads `// "  ▌ " prefix added by withBar`. `withBar` has been
  removed (slice 00). The math itself is correct: `writeNewCodeCards`
  prepends `"    "` (four spaces) and the inset Glamour renderer needs
  `wordWrap - 4` to fit inside that indent. Update the comment to
  reference `writeNewCodeCards`'s four-space indent; do not change the
  math.

Existing tests to update in the same commit:

- `monitor_transcript_items_view_test.go:93` —
  `TestToolExchangeHeaderCardStatesAndWidths` currently asserts the
  selected card row starts with `"  ▌ ╭"` and contains `"\n  ▌ ╰"`.
  Update the expectations to `"▌ ╭"` and `"\n▌ ╰"`. The width assertion
  (`lipgloss.Width(row) == width`) already covers FR-03.5 once the
  prefix width is stable.
- `monitor_transcript_items_view_test.go:254` —
  `TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes` currently
  asserts `"  ▌ ╭"` and `"\n  ╭"`. Update the selected expectation to
  `"▌ ╭"`; the unselected expectation `"\n  ╭"` remains correct.
- `TestToolExchangeCardCacheRefreshesOnPageReplacementAndWidthChange`
  and any other slice 01 cache test that observes the card `width`
  should continue to pass unchanged because `transcriptInnerW - 2`
  becomes the single stable card width across selection states; add a
  regression sub-case asserting that moving the cursor onto and off an
  item does not grow `chatItemRendered`.

New tests to add (see Unit 1 proof artifacts). Every new test lives in
`internal/tui/monitor/monitor_transcript_items_view_test.go` (or a
sibling file scoped to selection-affordance regressions if grouping
matters for readability), reuses `newMonitorWithSteps`, `setChatPage`,
and `stripANSI`, and is table-driven per the repository standard.

### Cache identity under the fix

`transcriptRenderKey` fields currently in use:
`itemKey`, `surface`, `width`, `expanded`, `selected`, `state`,
`header`. Under the fix:

- `width` collapses to a single value per `(item, transcriptInnerW)`
  tuple because both selection branches yield
  `transcriptInnerW - 2`. This is the behavior FR-03.5 requires.
- `selected` remains part of the key because
  `composeToolHeader` still applies `TranscriptSelected` to the title
  slot when `selected` is true (slice 02 FR-02.18). The composed
  `header` string therefore differs between selection states, and the
  cache must be able to serve both without collisions.
- No new key fields are introduced.
- The existing cache-eviction rule at
  `renderToolExchangeCard` — `for old := range m.chatItemRendered { if
  old.surface == transcriptRenderCard && old.itemKey == item.key { delete
  … } }` — continues to bound the cache to one card variant per item at
  a time. It runs on cache miss, so a rapid `n`/`N` sweep across items
  is O(items) in cache churn, unchanged.

### Line accounting under the fix

`chatItemLineRanges` is computed from `strings.Count(b.String()[itemStart:],
"\n")`. The fix does not change the number of newlines any writer emits,
so ranges are stable by construction. The tests assert this explicitly
(the range for a given item is identical whether or not the cursor is on
it), catching a regression where a future writer adds a
selection-conditional line.

### Charm dependency versions

No dependency changes. The fix touches presentation only, using
`lipgloss.Width` and `Theme.SelectedBar` from the pinned Charm v2
libraries (Lip Gloss 2.0.5 per `go.mod`).

### Deliberate deviation from external guidance

omp's `transcript-outline.ts` builds selection as a dotted outline
around the target item, not a gutter bar. jig deliberately keeps the
gutter bar for this slice — the outline is Option B, which composes a
second frame around the slice 01 card (see Non-Goals #1). The
recommendation the epic ships with is Option A, and this spec follows
it.

## Security Considerations

Presentation-only work introduces no credentials, no external writes,
and no new input surfaces. The change touches a two-character prefix
string and a single tests file; there is no expansion of the untrusted
content the renderer processes, no change to sanitization, and no
change to the transcript wire format (epic CC-12). Proofs must use
fabricated fixtures — never real `.jig/` data — per repository
standards.

## Success Metrics

1. Every new prefix-width test asserts `lipgloss.Width(selectedPrefix)
   == lipgloss.Width(unselectedPrefix) == 2` and passes for at least the
   text, system, thinking, unsupported, tool-exchange header card, and
   orphan tool-result rows.
2. The column-parity test renders a synthetic page twice with different
   cursor positions and reports zero lines whose leading non-space
   column differs between the two renders, after stripping ANSI.
3. `chatItemLineRanges` for any item is byte-identical between the
   selected and unselected renderings of the same page.
4. The slice 01 card cache size after moving the cursor onto and back
   off an item equals its size before the movement (no new width
   variant).
5. `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race
   ./internal/tui/...`, `gofmt -l <changed-go-files>` (empty), and
   `git diff --check` all pass on the recorded validation machine.
6. Manual acceptance: holding `n` through a synthetic transcript
   containing at least one tool-exchange card, one text item, and one
   thinking item shows no horizontal movement of any card frame,
   header, detail row, or code-card row.

## Open Questions

The following are non-blocking. Option A is correct either way; these
only affect follow-up work.

1. **Q-03.1** After the whole epic lands, is a recolored card border a
   sufficient selection affordance, or is a distinct gutter marker still
   wanted? Recorded as an explicit input to slice 01's post-implementation
   review. Not a blocker: Option A satisfies FR-03.1–FR-03.6 whether or
   not slice 01 later chooses to recolor the border on selection.
2. **Q-03.2** Should the selected item carry a caption (omp's
   `3 blocks →` / `y copy` pattern) advertising available actions?
   Currently duplicated in the footer help line, so likely redundant.
   Not a blocker; deferred with the option-B outline design.
3. **Q-03.3** Should the two-space unselected gutter be retired entirely
   in favor of a flush left transcript, aligning the card frame with
   `transcriptInnerW`? Would ripple into the card `prefix` argument,
   `writeItemDetail`'s six-space indent, and `writeNewCodeCards`'s
   four-space indent. Explicitly out of scope here; recorded so a
   future width-audit slice can pick it up if operators request more
   horizontal room.
