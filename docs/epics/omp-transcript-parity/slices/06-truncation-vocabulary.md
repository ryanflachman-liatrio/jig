# Slice 06 — One truncation vocabulary

- **Slice ID:** `truncation-vocabulary`
- **Outcome:** Every truncation affordance in the Transcript panel states the
  hidden quantity and the key that reveals it, using one wording; live output
  truncates from the head so the newest lines stay visible.
- **Why this slice exists:** Truncation wording is currently invented per call
  site, and the live-output case truncates the wrong end. Both are small, both
  are independent of the card work, and slices 05, 07, and 08 all need the
  vocabulary to exist before they emit their own indicators.
- **Depends on:** None. (Slices 05, 07, 08 consume it.)

---

## Current jig state

Four different phrasings, all in the live path:

| Site | Wording | File |
|---|---|---|
| tool detail | `… %d lines hidden` | `monitor_transcript_items_view.go:135` |
| new-code card | `… %d lines hidden` | `items_view.go:200` |
| write-truncation note | `… capture truncated at write` | `items_view.go:103` |
| expand elision | `… %d KB elided …` | `monitor_transcript.go:1204` |
| page markers | `── earlier messages available · [ load older ──` | `monitor_transcript.go:777` |
| page markers | `── newer messages available · ] load newer ──` | `monitor_transcript.go:845` |

None of them names the key that expands the item. The expand keys exist —
`keys.go:108` `Toggle` (`enter`/`space`) and `keys.go:109` `ExpandAll` (`o`) —
but the truncation text does not mention them, so a truncated body is a dead end
unless the operator already knows.

### The wrong-end problem

`boundTranscriptDetail` (`monitor_transcript_detail.go:19-43`) keeps **head +
tail** with the middle elided:

```go
headRows := transcriptDetailRows - transcriptDetailTailRows   // 12 - 3 = 9
hiddenRows = len(rows) - transcriptDetailRows
kept := append([]string{}, rows[:headRows]...)
kept = append(kept, rows[len(rows)-transcriptDetailTailRows:]...)
```

For a *settled* result that is a defensible choice. For **live streaming output**
it is wrong: the operator wants the newest lines, and a 9-line head of a growing
log is stale by definition.

The live tail path (`monitor_transcript.go:849-859`) does get this right:

```go
lines := strings.Split(tail, "\n")
if len(lines) > outputMaxLines {
    lines = lines[len(lines)-outputMaxLines:]      // keeps the tail
}
```

…but it prints no indicator at all for what it dropped.

---

## omp reference

### The canonical builder

`packages/coding-agent/src/tools/render-utils.ts:295-298`:

```ts
export function formatMoreItems(remaining: number, itemType: string): string {
    const safe = Number.isFinite(remaining) ? remaining : 0;
    return `… ${safe} more ${pluralize(itemType, safe)}`;
}
```

→ `… 12 more lines`, `… 1 more line`, `… 3 more files`, `… 2 more matches`.

### The expand hint

`render-utils.ts:276-280`:

```ts
export function formatExpandHint(theme: Theme, expanded?: boolean, hasMore?: boolean): string {
    if (expanded) return "";
    if (hasMore === false) return "";
    return theme.fg("dim", wrapBrackets(`${expandKeyHint()}: Expand`, theme));
}
```

Two properties worth copying:

- Returns `""` when already expanded or when there is nothing more — the hint
  never lies.
- **The key comes from the live keybinding table**, so a rebound key shows
  through. Verified by omp's own test (`test/tools/render-utils.test.ts:497`)
  asserting `"[Ctrl+O: Expand]"` under the ASCII preset.

### Head versus tail — the important distinction

`tui/code-cell.ts:165-176`:

```ts
if (tail) {
    // Earlier rows scrolled above the live tail window — mark them on top so
    // the newest streamed line stays pinned to the bottom of the box.
    const earlier = `… ${hidden} earlier line${hidden === 1 ? "" : "s"}${hint ? ` ${hint}` : ""}`;
    codeLines.unshift(theme.fg("dim", gutterPad + earlier));
} else {
    const moreLine = `${formatMoreItems(hidden, "line")}${hint ? ` ${hint}` : ""}`;
    codeLines.push(theme.fg("dim", gutterPad + moreLine));
}
```

| Window | Marker | Position |
|---|---|---|
| head (settled content) | `… 12 more lines ⟦…⟧` | **appended** |
| tail (live streaming) | `… 240 earlier lines ⟦…⟧` | **prepended** |

`gutterPad = " ".repeat(lineNumberWidth + 1)` aligns the marker under the code
text rather than under the line-number column.

### Viewport-derived tail window

`render-utils.ts:307-317`:

```ts
previewWindowRows() = max(6, (process.stdout.rows || 30) - 20)
```

Twenty rows reserved for frame, output section, stats, and the editor below.

### Truncation is by visual lines

`modes/components/visual-truncate.ts:37-63` lays text out through a cached `Text`
component **at the render width** and keeps the last N *visual* lines. jig's
`boundTranscriptDetail` already does the equivalent — this is one place jig is
already correct.

### A caveat, recorded honestly

omp is **not** internally consistent here. At least five phrasings coexist:
`… N more lines`, `… +N more`, `… (N earlier lines)`, `… N more`, bare `…`. And
`default-renderer.ts:134` hardcodes `more lines` without pluralizing. **Pick one
vocabulary; do not reproduce the spread.**

---

## In Scope

- A single helper pair in `internal/tui/shared`:

  ```go
  // MoreItems renders "… 12 more lines" with correct pluralization.
  func MoreItems(n int, singular, plural string) string

  // EarlierItems renders "… 240 earlier lines" for tail-anchored windows.
  func EarlierItems(n int, singular, plural string) string

  // ExpandHint renders the bracketed key hint, or "" when expanded or nothing hidden.
  func ExpandHint(expanded, hasMore bool, keyHelp string) string
  ```

- Derive the key text from the live binding rather than a literal. `keys.go`
  bindings expose `Help().Key` (`keybind.WithHelp("enter", "expand")`), so the
  hint can read `enter` / `o` from the same source the footer uses.
- Replace all four current phrasings with the shared helpers.
- Make the **live tail** path tail-anchored *with* an indicator: it already keeps
  the last `outputMaxLines`; add the prepended `… N earlier lines` marker.
- Give `boundTranscriptDetail` a tail-anchored mode for running steps, keeping
  head+tail for settled ones.
- Leave the page markers (`[` / `]`) alone in wording but align their key hints
  with the same source.

## Out of Scope

- Changing the budgets themselves (12 rows / 3 tail / 4 KiB) — tuning belongs
  with the slices that render into cards.
- The `… %d KB elided …` marker inside `expandView`, which describes a *byte*
  elision in the middle of a single blob rather than hidden lines. Keep it
  distinct; it is a different concept and its wording is already clear.
- A general keybinding-hint framework. Read the existing bindings; do not build
  an abstraction.

## Functional Requirements

- **FR-06.1** Every truncation indicator in the Transcript panel shall be
  produced by the shared helpers; no call site shall format its own.
- **FR-06.2** Indicators shall pluralize correctly for n = 1 and n > 1.
- **FR-06.3** An indicator for collapsible content shall name the key that
  expands it, and that key shall be read from the active keybinding table.
- **FR-06.4** The expand hint shall render as empty when the item is already
  expanded or when nothing is hidden.
- **FR-06.5** Head-anchored windows shall append `… N more <items>`;
  tail-anchored windows shall prepend `… N earlier <items>`.
- **FR-06.6** Output for a **running** step shall be tail-anchored, so the newest
  lines are always visible.
- **FR-06.7** The live tail shall display an indicator for lines it dropped.
- **FR-06.8** Indicators shall be styled as hints (dim), never as content.

## Technical and Repository Constraints

- `shared/keys.go` and `internal/tui/monitor/keys.go` hold the bindings; the
  hint must read from whichever is authoritative for the transcript, not
  hardcode `"enter"`.
- jig has both a per-item toggle (`enter`/`space`) and an expand-all (`o`).
  omp has only a global toggle, so it has one hint. Decide which jig shows —
  probably the per-item key, since that is what acts on the focused item.
- `boundTranscriptDetail` is called from `writeItemDetail` and
  `writeNewCodeCards`; adding a mode parameter touches both. Prefer an explicit
  mode enum over a bool for readability.
- Per CC-8 this vocabulary is fixed for the epic; slices 05, 07, 08 consume it
  rather than inventing.
- Bracket glyphs (`⟦⟧`) must go through the icon vocabulary per CC-7.

## Security and Data Considerations

None identified. The helpers format integers and static text.

## Acceptance Evidence

- Table-driven tests for pluralization at n = 0, 1, 2.
- A test that `ExpandHint` returns `""` when expanded and non-empty when
  collapsed with hidden content.
- A test that rebinding the expand key changes the rendered hint text (FR-06.3).
- A repository test that greps the monitor package for the retired literals
  (`lines hidden`, and any hand-built `"… "` prefix outside the helpers) and
  fails if one reappears (FR-06.1). This is the mechanism for EC-6.
- A test that a running step's output shows the last lines plus a leading
  `… N earlier lines` marker.

## Inputs for the Child Spec

- Pick **one** vocabulary. omp's own inconsistency is documented above precisely
  so it is not replicated.
- The head/tail distinction (FR-06.5, FR-06.6) is the substantive behavior change
  in this slice; the wording alignment is the easy part.
- The grep-based conformance test is what makes EC-6 checkable — include it.

## Open Questions

- **Q-06.1** Should the hint show the per-item key (`enter`) or the expand-all
  key (`o`)? *Suggest per-item; it matches the focused affordance.*
- **Q-06.2** Should settled output also become tail-anchored, or is head+tail
  right for a completed step? *Suggest keeping head+tail when settled — the
  beginning of a completed result is usually the informative part.*
- **Q-06.3** Do the page markers (`── earlier messages available · [ load older ──`)
  join this vocabulary or stay as-is? They describe a *page*, not truncation.
  *Suggest leaving them; note the decision so it is not revisited.*
