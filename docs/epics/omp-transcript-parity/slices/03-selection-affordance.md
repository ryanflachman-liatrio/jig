# Slice 03 — Non-shifting selection affordance

- **Slice ID:** `selection-affordance`
- **Outcome:** Moving the transcript block cursor changes only color and glyph —
  never the horizontal position of any content.
- **Why this slice exists:** It is the smallest visible defect fix in the epic
  and it is fully independent of the card work, so it can land while slice 01's
  tint question is still open. The current behavior is a two-column horizontal
  jump on every cursor move, which reads as a rendering bug.
- **Depends on:** None.

---

## The defect

`internal/tui/monitor/monitor_transcript_items_view.go:32-35`:

```go
prefix := "  "
if selected {
    prefix += shared.Theme.SelectedBar.Render("▌") + " "
}
```

Unselected rows are indented **2** columns. Selected rows are indented **4**.
Every press of `n` / `N` (`keys.go:107`) shifts the entire item — header,
detail lines, code cards — two columns to the right, then back again. Because
the detail writers use their own fixed `"      "` (6-space) indent
(`items_view.go:130-137`), the *body* does not move while the *header* does, so
the item also visually decoheres while selected.

## omp reference

`packages/coding-agent/src/modes/components/transcript-outline.ts:163-214`.

omp draws selection as a **dotted outline around the whole target**, and the two
states are built to occupy identical columns.

`composeOutlineColumn` (`:200-212`) — the unselected path:

```ts
for (const row of childRows[index]!) lines.push(row ? `  ${row}` : row);
```

Two-space gutter. Genuinely empty rows stay empty (no trailing whitespace).

`outlineRows` (`:163-172`) — the selected path:

```ts
const vertical = theme.fg(color, theme.boxDotted.vertical);   // ┆
lines.push(outlineRule(theme.boxRound.topLeft, theme.boxRound.topRight, innerWidth, color, style.caption));
for (const row of rows) lines.push(`${vertical} ${fit(row, innerWidth)} ${vertical}`);
lines.push(outlineRule(theme.boxRound.bottomLeft, theme.boxRound.bottomRight, innerWidth, color));
```

`"┆ "` is also **two columns**. Content never moves.

`outlineRule` (`:146-160`) puts a bold caption into the **right** end of the top
rule only:

```ts
const label = caption ? ` ${caption} ` : "";
const fill  = Math.max(0, innerWidth + 2 - visibleWidth(label));
return theme.fg(color, left + theme.boxDotted.horizontal.repeat(fill))
     + theme.bold(theme.fg(color, label))
     + theme.fg(color, right);
```

Rendered, `innerWidth = 40`, caption `3 blocks →`:

```
  I'll check the server entrypoint first.        ← unselected, 2-col gutter
╭┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄ 3 blocks → ╮
┆ I'll check the server entrypoint first. ┆      ← selected, "┆ " is also 2 cols
╰┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄╯
```

Glyphs: `boxDotted.horizontal = ┄`, `boxDotted.vertical = ┆`, corners from
`boxRound` (`modes/theme/symbols.ts:406-407, 399-405`). ASCII preset degrades to
`-` and `:`.

Two more details:

- **The box hugs the non-blank core.** Leading and trailing blank rows in the
  target's span are emitted as bare `""` *outside* the outline (`:200-207`), so
  the frame never wraps empty space.
- Outline color is `accent` by default, `success` in the `/copy` selector.

---

## Two viable designs

### Option A — keep the bar, fix the width (minimal)

```go
prefix := "  "
if selected {
    prefix = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
}
```

Note `=`, not `+=`. `"▌ "` is exactly the two columns that `"  "` occupied.
One-line change, zero new concepts, immediately correct.

**Recommended.** It satisfies the outcome and G3 completely.

### Option B — adopt the dotted outline

Wrap the selected item's whole rendered span in `╭┄ … ┄╮ / ┆ … ┆ / ╰┄ … ┄╯`.
Stronger affordance for multi-line items and a natural home for a caption
(`3 sections`, `expanded`, `y copy`).

**Cost:** it composes badly with slice 01. An outline around a card is two
nested frames. omp never faces this because its selection outline lives in a
*separate fullscreen overlay*, not in the live transcript — the live transcript
has no cursor at all.

**Recommendation:** ship Option A now. Revisit Option B only if, after slice 01,
a card's border alone proves insufficient to show selection — in which case the
likely answer is recoloring the card's existing border rather than adding a
second frame.

---

## In Scope

- Make the selected and unselected prefixes the same visible width.
- Audit the detail writers for the same class of bug: `writeItemDetail` uses a
  fixed `"      "` (`items_view.go:130-137`) and `writeNewCodeCards` uses
  `"    "` (`:196-201`) regardless of selection, so body indentation is already
  selection-independent — confirm and add a regression test.
- Add a test asserting prefix-width equality across selection states.
- If Option A is taken, decide how selection reads on a card once slice 01 lands
  and record it as an input to that slice.

## Out of Scope

- The dotted-outline overlay (Option B) unless Q-03.1 resolves toward it.
- Changing which item is selected, or the `n`/`N` navigation keys.
- The Steps panel cursor (`SelectedLine`), which is a full-row highlight and does
  not shift content.

## Functional Requirements

- **FR-03.1** For any transcript item, the visible width of the selected-state
  line prefix shall equal the visible width of the unselected-state prefix,
  measured with `lipgloss.Width` after stripping ANSI.
- **FR-03.2** The system shall render the selected item with a visually distinct
  marker in that fixed-width prefix.
- **FR-03.3** Body, detail, and code-card lines shall render at the same
  horizontal offset regardless of selection.
- **FR-03.4** Selection state shall not change the number of lines an item
  occupies, so `chatItemLineRanges` remains stable across cursor movement.

## Technical and Repository Constraints

- `shared.CursorBar` and `shared.BarThick` are both `"▌"` in
  `internal/tui/shared/icons.go`. Use `CursorBar` here for semantic accuracy.
- Per CC-7, do not hardcode the glyph at the call site.
- `shared.Theme.SelectedBar` already exists (`styles.go`, "charple `▌` cursor
  prefix for list rows") — reuse it, do not add a style.
- FR-03.4 matters because `chatItemLineRanges` is rebuilt on every
  `itemTranscriptBody` call and drives scroll-to-item; a selection-dependent
  height would make the viewport jump.

## Security and Data Considerations

None identified.

## Acceptance Evidence

- A test that renders the same transcript twice — cursor on item *i*, then on
  item *j* — strips ANSI from both, and asserts every line begins at the same
  column.
- A test asserting the rendered line count of an item is identical selected and
  unselected (FR-03.4).
- Manual: hold `n` through a transcript; no horizontal movement.

## Inputs for the Child Spec

- Option A is the recommendation; the spec should implement it unless Q-03.1
  resolves otherwise.
- The `+=` vs `=` distinction at `items_view.go:33` is the entire mechanical fix;
  the rest of the slice is tests and the audit.
- Record the post-slice-01 selection design decision as an explicit input to
  slice 01 even if this slice ships first.

## Open Questions

- **Q-03.1** After slice 01, is a recolored card border a sufficient selection
  affordance, or is a distinct marker still wanted? *Not a blocker — Option A is
  correct either way; this only decides whether more is added later.*
- **Q-03.2** Should the selected item also carry a caption (omp's
  `3 blocks →` pattern) showing available actions such as `y copy` /
  `enter expand`? jig currently surfaces these in the footer help. *Not a
  blocker; likely redundant with the help line.*
