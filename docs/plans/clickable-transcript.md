# Implementation Plan: clickable Monitor transcript

**Status:** Draft — awaiting approval
**Risk:** **low–medium** — the change lives entirely inside
`internal/tui/monitor` and extends the existing optional-mouse contract already
covered by [`monitor_mouse.go`](../../internal/tui/monitor/monitor_mouse.go)
and the [Mouse navigation](../TUI.md#mouse-navigation) section of
[TUI engineering](../TUI.md). It does not touch workflow schema, engine
scheduling, harness normalization, `transcript.jsonl`, prefs, or any package
outside Monitor. The main risk is keeping selection, expansion, cursor
targets, line ranges, follow, and search coherent when a new mouse activation
path can now change the same view state that `enter`/`space`/`o` change today.
**Supersedes / relates to:** the "Mouse-first transcript selection" item
listed as an open TUI gap in
[`docs/plans/open-goals.md`](open-goals.md) (T-series) and reuses the
[compact tool groups](compact-tool-groups.md) line-range model as its
hit-testing substrate. It does **not** replace the keyboard-primary contract.

## Summary

Extend the Monitor's optional-mouse contract so a primary click in the
Transcript panel is not just a focus event: on any expandable transcript row
it also moves the transcript block cursor to that row and toggles its
expansion. Two levels are supported in one uniform gesture:

- Click a **tool group header** (a compact non-read tool group or the
  existing automatic read group) — the group expands or collapses, exactly
  as `enter`/`space` on the group header does today.
- Click an **individual tool exchange** (a standalone exchange, or an
  already-expanded child inside a group) — that exchange's detail card
  expands or collapses, exactly as `enter`/`space` on that child does today.

Clicks on non-expandable rows (system prose, assistant/user text, reasoning
that is under the collapse threshold, boundary banners, per-turn metadata
rows, and file view) keep the current behavior: focus moves to Transcript,
selection and scroll do not change. Mouse remains optional and additive; the
transcript is fully usable with the keyboard alone, and every gesture below
has a documented `k` binding.

## Non-goals

- No mouse-driven activation of gates, review workspaces, step lifecycle
  actions, or any command that mutates run/graph state. The mouse never
  becomes a workflow control surface (
  [`docs/TUI.md`](../TUI.md#mouse-navigation) contract preserved).
- No drag-select, no rubber-band selection, no copy-on-select. The
  clipboard path stays `y`/`Y` (spec 23 clipboard yank).
- No right-click context menu, no middle-click paste. Modified clicks
  (shift/ctrl/alt/meta) remain ignored, same as today.
- No hover affordances. Bubble Tea's terminal event stream does deliver
  `tea.MouseMotionMsg`, but reacting to hover would require repaints on
  every mouse move; skip it.
- No mouse-controlled resize between Steps and Transcript panels.
  (`B9` in [`open-goals.md`](open-goals.md).)
- No scrolling change: vertical wheel behavior is unchanged. Horizontal
  wheels and modified wheels remain ignored.

## Decisions

This plan records the approved interaction contract:

- **Left click is the only activation event.** `MouseLeft`, no modifiers.
  Anything else (right/middle click, wheel-button click, shift/ctrl-click,
  `MouseMotionMsg`, `MouseReleaseMsg`) is ignored — mirrors the pattern
  already in `updateMouse` today.
- **A click in Transcript first moves keyboard focus to Transcript.** This
  preserves today's rule that a click never steals input away from a modal,
  gate, search, or textarea (`mouseExcluded()` gates the entire path).
- **A click on a transcript row moves the transcript block cursor to that
  row.** This is new. It is the counterpart to what a click already does in
  the Steps panel: click-to-select. Selection is exactly a `n`/`N` block
  stop change, not a scroll change; the viewport is not re-centered on
  click.
- **A click on an expandable row also toggles its expansion state.** This
  is the core new behavior. "Expandable" is defined as any cursor target
  today whose selected `enter`/`space` toggles `chatItemExpand[item.key]`
  or the group's own expansion. That set is:
  - `transcriptItemToolExchange` (standalone paired use+result)
  - `transcriptItemToolResult` (orphan result)
  - `transcriptItemReadGroup` (automatic read group header)
  - `transcriptItemToolGroup` (compact non-read group header)
  - members of an expanded read group or tool group (each toggles its
    own detail card, same as `enter`/`space` on that child today)
  - `transcriptItemText` / `transcriptItemThinking` **only when
    `oversized`** — same rule `itemHasDetail` uses to decide whether
    `enter`/`space` toggles anything at all
- **A click on a non-expandable row is not an activation.** Focus and the
  block cursor still move to that row (so the user learns which row they
  clicked), but no expansion state changes. This matches the mouse
  contract phrase: "The mouse never becomes an activation or workflow
  control surface." Toggling a view state is a benign, reversible
  presentation change; a row that has no view state to toggle is inert.
- **Auto-scroll follow is paused on any transcript click**, exactly the
  way `n`/`N`/`enter`/`space` pause it today: any deliberate navigation
  breaks tail-follow so the user isn't yanked back to the bottom.
- **Selection is clamped to visible rows.** A click in a body area with no
  registered `chatItemLineRanges` entry (blank spacer between items, or the
  boundary banner / per-turn metadata rows that are intentionally folded
  into the previous item's range) resolves to the nearest enclosing item
  when one exists, and to a plain focus-only event otherwise.
- **Clicking an item that is already selected re-toggles it.** No sticky
  "double-click to activate" state machine. The gesture is idempotent in
  the sense that click → click returns to the original expansion.
- **The file view keeps its current no-op behavior.** A click on the
  file body focuses Transcript without changing selection or scroll — the
  file view has no line cursor for the mouse to target, and no expansion
  state.
- **Docs are updated.** The Monitor "Mouse navigation" paragraph in
  [`docs/TUI.md`](../TUI.md) narrows from "focuses Transcript without
  changing its selection or scroll offset" to the new selection+toggle
  contract, with the file view and non-expandable rows called out as the
  focus-only cases.

## Approach

Keep the entire optional-mouse contract (`mouseExcluded()`,
`monitorMousePanels`, modifier gating, wheel handling) in place. Extend
`updateMouse` so a `MouseLeft` `MouseClickMsg` inside `panels.transcript`
no longer ends with the current single line

```go
} else if panels.transcriptVisible && panels.transcript.contains(mouse.X, mouse.Y) {
    m.focus = focusTranscript
}
```

but instead:

1. Focuses Transcript (unchanged).
2. Translates click Y to a body-line index using the same math the Steps
   panel uses:
   `line := m.chatVP.YOffset() + mouse.Y - panels.transcript.y`.
3. Looks up the transcript item that owns that line via a new helper
   `itemAtTranscriptLine(line int) (transcriptCursorTarget, bool)`. The
   helper consults `chatItemLineRanges` (already recorded by
   `itemTranscriptBody` for top-level items and by
   `renderCompactToolGroup` for each visible group member) and returns the
   most specific cursor target — a child member if the click fell inside
   an expanded group child's range, otherwise the group header.
4. If a target is found, sets `m.chatItemCursor` to that target,
   pauses `chatAutoScroll`, refreshes panels, and (only when the target's
   item is expandable) toggles its expansion in the exact same code path
   `m.keys.Toggle` already uses in `updateTranscript`.

All state mutation flows through the existing single owner of expansion
(`chatItemExpand`, `chatItemExpandAll`, `rebuildTranscriptItemState`).
`ensureTranscriptItemCursorVisible` is deliberately **not** invoked from
the click path: the user is clicking on a row they can already see, and
re-centering the viewport under the pointer would move the row out from
under their finger between the click and the release.

## Data and state model

No new struct fields, no new maps. The plan reuses:

- `m.chatItemLineRanges` (body-line-space; already keyed by
  `transcriptItemKey`)
- `m.chatCursorTargets` (already the canonical navigable-stop sequence,
  and already the mapping between `transcriptItemKey` and a cursor index)
- `m.chatItemExpand` / `m.chatItemExpandAll` (already the expansion
  source of truth)

The one bit of new work is teaching the read-group renderer to record
per-member line ranges the same way the compact tool-group renderer
already does. That gives clicks on read-group children a target and
harmonizes the two group renderers behind one hit-testing rule. See the
**Read-group parity** step below.

### Line-range invariant

`chatItemLineRanges[transcriptLineKey{itemKey}] = lineRange{start, end}` is
body-line-space, inclusive of `end` (matching current usage in
`ensureTranscriptRangeVisible`). After a rebuild:

- Every visible top-level item has exactly one entry.
- Every visible member of an expanded compact tool group has one entry,
  scoped to the lines its child card actually occupies.
- **New:** Every visible member of an expanded read group has one entry,
  scoped to the lines its child detail actually occupies.
- The group header and its children may overlap on the header line
  itself; hit-testing resolves that by preferring the smallest containing
  range (a child's range is always a strict subrange of the group's).

## Behavioral contract by row kind

| Row kind | Click behavior |
|---|---|
| Standalone `transcriptItemToolExchange` header | focus Transcript, select item, toggle `chatItemExpand[item.key]` |
| Standalone `transcriptItemToolResult` header | focus, select, toggle expansion |
| `transcriptItemReadGroup` header (collapsed) | focus, select, expand group |
| `transcriptItemReadGroup` header (expanded) | focus, select, collapse group (returns to summary) |
| `transcriptItemReadGroup` child row (expanded) | focus, select the child, toggle that child's detail card |
| `transcriptItemToolGroup` header (collapsed) | focus, select, expand group |
| `transcriptItemToolGroup` header (expanded) | focus, select, collapse group |
| `transcriptItemToolGroup` child card (expanded) | focus, select the child, toggle that child's detail card |
| Detail body row *inside* an already-expanded exchange | focus, select the enclosing exchange, **do not** re-collapse (avoids the "click to read → oops, collapsed" trap) |
| `transcriptItemText` / `transcriptItemThinking` — under threshold | focus, select |
| `transcriptItemText` / `transcriptItemThinking` — oversized | focus, select, toggle its summary/full toggle |
| Boundary banner / per-turn metadata row | folded into the previous item's range; click behaves as a click on that previous item |
| Blank spacer line | focus only, no selection change |
| File view body | focus only, no selection change (unchanged) |
| Non-expandable prose in file view | focus only (unchanged) |

The "detail body row inside expanded exchange → do not re-collapse" rule
is the single asymmetry with keyboard behavior. It exists because a
click can land anywhere inside the (potentially long) expanded body,
and pressing `enter` when the cursor is on that body would also toggle
today; the pointer analog would frustrate anyone reading past the fold.
Selecting-only there keeps the state predictable while still moving the
block cursor so `y`/`Y` copy the exchange the user is reading.

## Approach — detailed steps

### 1. Hit-testing helper (`itemAtTranscriptLine`)

Add a new method on `Model` in `monitor_mouse.go`:

```go
type transcriptHit struct {
    target transcriptCursorTarget
    // headerLine is true when the click landed on the range's first
    // rendered line — the affordance line that shows the caret/marker.
    // Detail-body clicks (headerLine == false) select-only.
    headerLine bool
}

func (m Model) itemAtTranscriptLine(line int) (transcriptHit, bool)
```

Implementation:

- Walk `m.chatCursorTargets` in reverse. Each target's `itemKey` is a
  key in `m.chatItemLineRanges`; a child target's range is a strict
  subrange of its parent group's range. Reverse iteration returns the
  most specific (deepest) match first.
- `headerLine` is `line == rng.start`.
- Returns `false` for lines with no matching range (blank spacers,
  page-edge banner before the first item).

This helper is deliberately a `Model` method (value receiver) with no
side effects — it is safe to call from `View` in future for hover
affordances if we ever add them.

### 2. Read-group parity — record per-member line ranges

`writeReadGroup` in `monitor_read_group_view.go` currently writes the
header + per-target summary rows into one string that becomes the
group's block, and then walks members to write their expanded detail
below. Extend it to accept `*map[transcriptItemKey]lineRange` (or
return a small `readGroupRender` struct the way
`renderCompactToolGroup` does), and record the start/end line for each
expanded member.

Constraints:

- Keep the existing render cache (`transcriptRenderReadGroup`) intact —
  the summary-string cache is width-and-state keyed and is not affected
  by adding line-range accounting outside the cached string.
- Do not add per-member ranges when the group is collapsed; only the
  group-header range exists then, matching the compact tool-group
  behavior.
- Member ranges must land inside the group's overall range so the
  reverse-walk hit test resolves them before the group header.

### 3. `updateMouse` — Transcript click dispatch

Change the transcript-click branch of `updateMouse`:

```go
} else if panels.transcriptVisible && panels.transcript.contains(mouse.X, mouse.Y) {
    m.focus = focusTranscript
    line := m.chatVP.YOffset() + mouse.Y - panels.transcript.y
    if hit, ok := m.itemAtTranscriptLine(line); ok {
        m.chatItemCursor = m.cursorTargetIndex(hit.target.key)
        m.chatAutoScroll = false
        if hit.headerLine {
            if item, ok := m.itemForCursorTarget(hit.target); ok && itemIsExpandable(item) {
                m.chatItemExpand[item.key] = !m.chatItemExpand[item.key]
                m.rebuildTranscriptItemState(item.key)
            }
        }
        m.refreshPanels()
    } else {
        m.refreshPanels()
    }
}
```

`itemIsExpandable` mirrors the existing `itemHasDetail` (which
`writeTranscriptItem` already consults for the collapse marker) plus
the group kinds. Keeping the two predicates aligned is cheap and keeps
the caret/marker glyph and the click-toggle in agreement: if the row
shows a `▸`/`▾` marker, a click on it toggles that marker.

### 4. Auto-scroll follow

Set `m.chatAutoScroll = false` on any transcript-panel click that
resolves to a hit, mirroring `keys.BlockNav`, `keys.Toggle`, and
`keys.ExpandAll`. Clicks that resolve to no hit (blank spacer) leave
follow alone — a stray click in dead space shouldn't detach the user
from live output.

### 5. Search interaction

If a search is open (`m.searchOpen` is `true`), `mouseExcluded()` already
returns `true` through `m.textareaActive()` — the click is dropped. No
change here.

If `m.searchQuery != ""` but the search input is closed, `n`/`N` today
navigates search hits instead of block cursor targets. A click still
resolves to an item, so it must behave like a block-navigation `n`/`N`,
not a search `n`/`N`: it sets `chatItemCursor` (not
`searchHitCursor`), pauses follow, and toggles when the target is
expandable. This is the natural mapping — a click at a specific line
targets a specific item, not "the next search match after that item."

### 6. File view

`chatBody()` returns `m.fileBody()` when `m.selKind == "file"`, and
`itemTranscriptBody` is never called. `chatItemLineRanges` is
consequently empty. `itemAtTranscriptLine` returns `false`, and the
click path resolves to "focus only" — unchanged from today.

### 7. Docs & help

- **`docs/TUI.md`** — replace the transcript-click paragraph so it
  reads (approximately):

  > A plain primary click in Monitor's Transcript body selects the row
  > under the pointer and moves the block cursor to it. A click on a
  > tool group header, a tool exchange header, an oversized text or
  > reasoning row, or a compact-group child card also toggles that
  > row's expansion; every other row selects without changing scroll
  > or expansion state. The file view still focuses only.

  Keep the existing sentence about clicks never activating workflow
  actions (still true — expansion is a view state, not an activation).

- **Help / palette** — no new bindings. The mouse contract is
  keyboard-parity: every gesture already appears in the Transcript
  section (`enter/space expand`, `o all`, `n/N block`), so the
  in-panel help footer does not gain a new line.

- **`docs/plans/open-goals.md`** — flip the `Mouse-first transcript
  selection` note to reflect the delivered behavior; keep drag-select
  and hover as open items.

### 8. Testing

Add table-driven click tests alongside the existing
`monitor_mouse_test.go` cases. The existing helpers (`monitorClick`,
`newMonitorWithSteps`, `newMonitorWithFamily`) plus the transcript
harness under `monitor_transcript_*_test.go` already build realistic
step transcripts with grouped and standalone tool calls.

Required cases (all model-level, no terminal smoke needed for the
core):

1. Click on a standalone tool exchange header — toggles
   `chatItemExpand[item.key]` and moves `chatItemCursor` to the item.
2. Click on a compact tool group header (`compactToolGroups = true`,
   two adjacent Bash calls) — expands the group, `chatItemCursor`
   lands on the group header target.
3. Click on an expanded compact tool group's child card — toggles
   that child's detail, `chatItemCursor` lands on the child target.
4. Click on a read group header — expands the read group and records
   per-member ranges. Second click collapses.
5. Click on an expanded read group child row — toggles that child's
   detail, without collapsing the read group.
6. Click on a text row / short reasoning row — moves cursor,
   `chatItemExpand` unchanged, `chatAutoScroll` cleared.
7. Click on the boundary banner / per-turn metadata row — folds into
   the previous item, matching the current `chatItemLineRanges`
   coalescing rule.
8. Click on the transcript panel while `searchOpen == true` — no-op
   (excluded).
9. Click while a gate is focused, help modal open, review workspace
   open — no-op (already covered by
   `TestMonitorMouseExcludedInteractionSurfaces`, extend the table).
10. Click at a non-zero `chatVP.YOffset()` — the correct item is
    resolved (mirrors the Steps `nonzero viewport offset` case).
11. Click during autoscroll — `chatAutoScroll` flips to `false`.
12. Click on file view body — focuses Transcript, `selKind` /
    `selFile` unchanged.
13. Modified/right/middle click — no-op (covered by the existing
    `TestMonitorMouseUnsupportedAndInvalidAreNoOps`, extend to the
    transcript panel).
14. Click on an expanded exchange's detail body row (non-header line)
    — selects the enclosing exchange, does **not** re-collapse it
    (the "reader trap" rule).

A single visual smoke check (`docs/TESTING.md` "real terminal" tier) is
appropriate but not required for the model correctness: expand a tool
group with the mouse, capture the frame, confirm the caret glyph state
matches the expected `▸`/`▾`.

### 9. Performance

Each click walks `chatCursorTargets` in reverse — bounded by the
loaded page (`chatWindowMax = 300`) and typically dominated by a
handful of targets. `chatItemLineRanges` lookups are O(1). No
additional renders happen on click that wouldn't already happen on the
keyboard equivalent, so the incremental cost is zero.

## Additional UX suggestions considered

These are **out of scope** for this plan; recorded here so we don't
lose track of the ideas that came up while designing the click
contract.

- **Double-click to expand-all descendants.** A double-click on a group
  header could act as `o` scoped to that group's members. Tempting, but
  double-click has no keyboard analog and forces us to introduce a
  click-timeout state machine into the mouse path. Punt until we've
  lived with single-click for a while.
- **Shift-click to select-only without toggle.** Would be handy for
  "read but don't disturb," but the current contract achieves the
  same thing more discoverably by selecting-only on detail-body rows
  and by not toggling non-expandable rows. Revisit if operators
  ask.
- **Ctrl-click to open a linked file in a $EDITOR.** Real editor
  integration is a larger goal (T-series in `open-goals.md`) and
  should ship as its own feature with its own contract for how the
  editor is chosen.
- **Hover affordances (row highlight, click hint).** Would require
  reacting to `tea.MouseMotionMsg` and repainting on every mouse
  move, which is expensive over SSH and noisy in tmux. Only worth
  it once hover is the norm for other Charm apps.
- **Mouse-drag resize between panels.** Real feature, out of scope
  here. Tracked as B9 in [`open-goals.md`](open-goals.md).
- **Click the collapse marker `▸`/`▾` glyph explicitly.** In this
  plan, clicking anywhere on the header line toggles, matching how
  most operators actually use collapse trees. A finer "only the
  glyph" hit box would be less discoverable and add width-dependent
  hit math for no benefit.
- **Click a search-hit indicator in the transcript to jump to that
  hit.** Requires per-hit line-range accounting today held by the
  search hit list, not `chatItemLineRanges`. Small follow-up, but
  independent — the block-cursor click is the primitive it will
  reuse.
- **Click a `+N/-M` diff badge in an edit card header to jump to
  the diff detail.** Same shape as the search-hit idea — a small
  follow-up once badge coordinates are recorded.
- **Right-click "copy" menu (spec 23 y/Y).** The clipboard contract
  is intentionally keyboard-only today. Consider once a general
  context-menu overlay exists (probably B3 palette related).
- **Click a Steps-panel file row to focus Transcript on that file.**
  Steps clicks already select the file row and `reloadTranscript`
  already redirects to file view — this already works.

## Rollout

- Ship as a plain code change in `internal/tui/monitor` plus the two
  doc files above; no schema, no config, no prefs. The behavior is
  additive to `updateMouse` — a terminal or emulator without mouse
  reporting sees no change.
- No feature flag. Simple mode does not gate mouse behavior today and
  should not gate this either.
- Bisect-safe: the change is one commit for the read-group range
  accounting, one commit for the hit-testing helper + `updateMouse`
  branch, one commit for docs. The tests land alongside the code
  commit they cover.

## Open questions

None at plan time. The primary interaction points were resolved
above:

- What defines "expandable"? — same set `itemHasDetail` uses today,
  plus the two group kinds.
- Does click re-center the viewport? — no.
- Does click break follow? — yes, only when it resolves to a hit.
- Does click ever activate a step / gate / review? — no.
- Does click on a detail body row re-collapse the enclosing
  exchange? — no.
- Does the plan touch the transcript file format, engine, harness,
  workflow schema, or prefs? — no.
