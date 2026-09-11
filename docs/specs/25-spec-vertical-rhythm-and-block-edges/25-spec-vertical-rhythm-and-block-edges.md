# 25-spec-vertical-rhythm-and-block-edges.md

## Introduction/Overview

Stop the Monitor transcript from stacking a per-item's own leading or
trailing structural whitespace on top of the inter-item separator, and
stop an item that renders no visible content from consuming a separator
line as if it had. Today `itemTranscriptBody` unconditionally emits
`itemSpacingBefore(previous, current)` newlines between every pair of
loaded items and appends the item's raw bytes without inspecting them.
That works for the current renderers because they happen not to produce
edge blank lines, but slice 01 has already introduced card frames whose
consumers (slice 05 detail sections, slice 09 user bubbles, and glamour
output routed through `writeNewCodeCards`) can and will emit blank
padding — including tinted padding rows that are visually intentional.
When that lands, the current loop double-spaces the panel and collapses
tinted padding into whitespace the terminal renders as bare background.

This slice ports the two invariants from oh-my-pi's
`transcript-container.ts` that make its transcript rhythm feel even —
zero-height items contribute nothing, and structural edge blanks do not
stack with the separator — while keeping jig's execution-coordinate
two-line gap unchanged. The trimming predicate is raw-bytes aware, so a
tinted card padding row (whose bytes include the card background SGR)
survives while a plain blank line does not, which is the property slice
09's user-message bubble depends on.

Source: [OMP transcript parity, slice 04](../../epics/omp-transcript-parity/slices/04-vertical-rhythm-and-block-edges.md).
Depends on: slice 01, committed as the transcript-card primitive under
[docs/specs/25-spec-transcript-card-primitive](../25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md).
Slice 09 (message framing) explicitly depends on this slice's FR-04.3
raw-bytes trimming discipline; that dependency motivates the semantic
choice recorded under Design Considerations but is not implemented
here. This document is Phase 1 output; it does not authorize
implementing slice 05, slice 07, slice 09, or any other epic slice.

## Goals

- **G-04.a** An item whose renderer emits no visible content contributes
  neither content lines nor a separator line to the transcript body,
  verified with a synthetic filtered/empty item between two visible
  items and an exact-separator assertion.
- **G-04.b** Structural leading or trailing blank lines from an item's
  own renderer do not stack on top of the inter-item separator, so the
  rhythm between two adjacent items is `itemSpacingBefore(previous,
  current)` blank lines and no more, regardless of what edge whitespace
  the item happened to produce.
- **G-04.c** A row whose raw bytes contain any non-whitespace (including
  an ANSI escape byte) is treated as content and survives edge trimming,
  so a background-tinted padding row emitted by a future card body
  remains visible while a plain blank line does not.
- **G-04.d** The execution-coordinate rhythm is preserved: two
  consecutive text items from the same role at the same
  generation/iteration/attempt render with no gap, and a change in any
  of those coordinates renders with the current two-line gap.
- **G-04.e** `chatItemLineRanges` remains accurate after per-item edge
  trimming, measured against the actual bytes written to the transcript
  body, so `n`/`N` block navigation and scroll-to-item never desync from
  what the viewport shows.

## User Stories

- **As an operator scanning a transcript with `n`/`N`**, I want the
  vertical spacing between items to be predictable — one blank line
  between neighboring items and no ghost gap where a filtered item used
  to be — so that skimming a run does not require me to count blanks or
  double-check whether a card was hidden.
- **As an operator reading a tool-heavy step in a narrow terminal**, I
  want a card's trailing whitespace not to compound with the separator
  above the next item so I can see three cards in the same viewport
  height that today shows two.
- **As a maintainer implementing slice 09's user-message bubble**, I
  want the transcript loop to keep my tinted padding row visible while
  still trimming a plain blank line, so I can express the bubble as
  regular card content instead of teaching the loop about a new
  render-time flag.
- **As a maintainer adding future item kinds**, I want a filtered or
  conditionally hidden item to disappear cleanly rather than leaving a
  gap that reveals its former position, so filter and expand-all
  interactions cannot drift the transcript layout under me.

## Demoable Units of Work

### Unit 1: Raw-bytes structural blank predicate and edge trimmer

**Purpose:** Introduce the two helpers slice 04 needs — a predicate that
reports whether a line is structurally blank (no bytes at all beyond
ASCII whitespace) and a wrapper that trims leading and trailing
structurally-blank lines from a rendered item. Prove that the predicate
preserves a tinted padding row and trims a plain blank line, and that
the wrapper is a total function returning `""` for an all-blank input.

**Functional Requirements:**

- **FR-04.1** The system shall provide an unexported helper
  `isStructuralBlank(line string) bool` in the Monitor package (colocated
  with the existing `stripSGR` / `stripBlankEdges` in
  `internal/tui/monitor/monitor_transcript.go`) that returns true when
  the line's raw bytes contain no non-whitespace characters (empty
  string, or bytes that are all ASCII space, tab, carriage return, or
  vertical whitespace) and false otherwise. An ANSI escape byte
  (`\x1b`), a printable glyph, or a Unicode non-whitespace rune shall
  each cause it to return false. The helper shall not strip SGR escapes
  before testing; that is the exact behavioral inversion versus
  `stripBlankEdges`, and is deliberate.
- **FR-04.2** The system shall provide an unexported helper
  `trimStructuralBlankEdges(s string) string` that splits `s` on `\n`,
  drops leading and trailing lines for which `isStructuralBlank` is
  true, and rejoins the surviving lines with `\n`. When every line is
  structurally blank the helper shall return the empty string. The
  helper shall not append a trailing newline; the caller reintroduces
  whatever line terminator it needs.
- **FR-04.3** `trimStructuralBlankEdges("")` shall return `""` and
  `trimStructuralBlankEdges("only\n")` shall return `"only"`. A line
  containing only an SGR reset such as `"\x1b[0m"` shall be preserved
  (it is not structurally blank because `\x1b` is non-whitespace); a
  line containing only spaces or a mix of spaces and tabs shall be
  dropped when at an edge.
- **FR-04.4** The existing `stripBlankEdges` helper shall not be modified
  or reused for FR-04.2. `stripBlankEdges` continues to serve
  glamour-output normalization inside `renderNewCodeCard` and
  `fileBody`, where its SGR-stripping semantics are correct because
  those inputs' blank lines are Glamour's top/bottom margin rows
  containing only styling bytes. Introducing `trimStructuralBlankEdges`
  as a separate helper keeps the two semantics visibly distinct and
  documents the inversion at the call sites.

**Proof Artifacts:**

- Unit test `TestIsStructuralBlankRawBytesSemantics` in
  `internal/tui/monitor/monitor_transcript_test.go` (new sibling file
  is acceptable; existing `monitor_transcript_test.go` may host the
  case) tables the predicate over: empty string; single space; multiple
  spaces and tabs; `"\r"`; `"\v"`; a plain glyph; a single `\x1b`; a
  tinted-padding-shaped row `"\x1b[48;2;26;25;31m   \x1b[49m"`; a
  Glamour-shaped blank row `"\x1b[38;2;80;80;80m\x1b[0m"`; and a
  Unicode non-breaking space. Assert `false` for the padding rows and
  every non-whitespace input; `true` for the plain-whitespace cases.
- Unit test `TestTrimStructuralBlankEdges` tables the wrapper over:
  empty input; single blank line; two leading blanks + `"a"`; `"a"` +
  two trailing blanks; interior blank lines preserved; entirely blank
  input; tinted-padding-shaped row alone; tinted-padding-shaped row
  surrounded by plain blanks. Assert equality to the expected trimmed
  string in each case.
- Grep-based `_test.go` assertion showing that `stripBlankEdges` is
  still used only from `renderNewCodeCard` and `fileBody`, and that
  `trimStructuralBlankEdges` is used only from `itemTranscriptBody`.
  This locks the two helpers' intended blast radius so a future edit
  cannot silently swap them.

### Unit 2: Per-item scratch buffer, edge trimming, and zero-height guard

**Purpose:** Refactor `itemTranscriptBody` so each item's bytes flow
through a scratch buffer, get trimmed with `trimStructuralBlankEdges`,
and — if empty after trimming — contribute neither content nor
separator. Preserve every existing invariant the loop already carries
(header-card cache identity, tool-exchange append of `"\n"`,
`writeItemDetail` and `writeNewCodeCards` writes, `chatItemLineRanges`,
execution-coordinate gap).

**Functional Requirements:**

- **FR-04.5** `itemTranscriptBody` shall render each visible item into a
  per-item `strings.Builder` (or equivalent per-iteration buffer), pass
  the resulting string through `trimStructuralBlankEdges`, and only then
  decide whether the item contributes to the transcript body. Item
  emission shall remain in-order and no item's rendered output shall
  cross an iteration boundary.
- **FR-04.6** When an item's trimmed output is the empty string the
  system shall skip it entirely: it shall not write the separator that
  would otherwise precede it, it shall not increment the line counter,
  and it shall not insert an entry into `chatItemLineRanges` for that
  item's key. The next visible item's separator shall be computed
  against the last item that actually contributed content, not against
  the skipped item.
- **FR-04.7** When an item's trimmed output is non-empty the system shall
  write `itemSpacingBefore(previousRendered, current)` newlines before
  it (or none if it is the first rendered item) and then the trimmed
  bytes followed by exactly one `"\n"` so the next iteration's separator
  starts on its own line. `previousRendered` is the most recent visible
  item whose trimmed output was non-empty.
- **FR-04.8** Two consecutive text items with the same role at the same
  execution coordinate shall render with zero blank lines between them
  after trimming, matching current `itemSpacingBefore(previous, current)
  == 0` behavior. Two adjacent items whose execution coordinates differ
  shall render with the current two blank lines between them.
- **FR-04.9** `chatItemLineRanges` entries shall be derived from the
  bytes actually written to the transcript body after trimming. The
  entry for an item shall satisfy `start >= 0`, `end >= start`, and
  `end - start + 1` equal to the number of physical rows the item
  occupies in the rendered body. No entry shall exist for a
  zero-height (skipped) item.
- **FR-04.10** The refactor shall not change the observable output for
  any item whose renderer today happens to emit no structural edge
  blanks. In particular, the existing table-driven regressions in
  `monitor_transcript_items_view_test.go` and
  `monitor_transcript_card_test.go` (headers, prefix widths, line
  ranges, cache identity) shall keep passing without their expectations
  being weakened. Any change to an existing expected `lineRange` is
  itself a regression that must be re-justified.
- **FR-04.11** The header-card cache (`chatItemRendered`) and its
  eviction rule shall be unchanged. Slice 04 must not shift caching
  semantics or add new keys; `renderToolExchangeCard` is called from
  the same case arm with the same arguments and returns the same
  string as today for the same inputs.
- **FR-04.12** `itemSpacingBefore` shall keep its no-theme-dependency
  property and shall continue to return `2` on any generation/
  iteration/attempt change and `0` on consecutive same-role
  `transcriptItemText` items at the same coordinate. Its doc comment
  shall be updated to state (a) that the separator only fires between
  two items that both contributed content, and (b) that a two-line
  execution-coordinate gap remains stronger than a single-item gap
  because the boundary banner (slice 12) sits inside it.
- **FR-04.13** With persistence off (`RunDir == ""`) the transcript
  shall continue to render the existing empty state without invoking
  the item loop. Slice 04 shall not introduce any filesystem or engine
  dependency in the trimmer or the loop refactor.

**Proof Artifacts:**

- Monitor behavioral test
  `TestTranscriptZeroHeightItemContributesNothing` in
  `internal/tui/monitor/monitor_transcript_items_view_test.go` (or a
  sibling `_vertical_rhythm_test.go` file) that seats a synthetic
  empty-render item between two visible items and asserts (a) the
  transcript body contains exactly `itemSpacingBefore(previous, next)`
  blank lines between them, computed against the *neighbors of the
  skipped item*; (b) `chatItemLineRanges` contains no entry for the
  skipped item's key; (c) the visible items' `lineRange` values match
  what they would be if the skipped item were absent from `chatItems`
  entirely.
- Monitor behavioral test
  `TestTranscriptTrimsStructuralEdgeBlanksBetweenItems` that plants a
  synthetic item whose renderer emits a leading and a trailing blank
  line (via a test-only harness path or a synthetic BlockText that
  currently produces edge blank lines through glamour), then asserts
  the number of blank lines between it and its neighbor equals
  `itemSpacingBefore(previous, current)` — not `itemSpacingBefore + 1`
  and not `itemSpacingBefore + 2`.
- Monitor behavioral test
  `TestTranscriptPreservesTintedPaddingRow` that feeds a synthetic
  item whose rendered bytes begin with a tinted-padding-shaped row
  (constructed inline from the shared card background SGR — see
  Technical Considerations for the exact sequence) and asserts the
  padding row is present in the final transcript body byte-for-byte.
  The item shall be surrounded by neighbors so the separator behavior
  is exercised too.
- Monitor behavioral test
  `TestTranscriptConsecutiveTextItemsSameRoleNoGap` (may reuse an
  existing fixture) that renders two adjacent user text items and asserts
  the transcript body contains zero blank lines between them.
- Monitor behavioral test
  `TestTranscriptExecutionCoordinateGapPreserved` (may extend an
  existing case) that seats two items whose `coord.iteration` differs
  and asserts exactly two blank lines separate them, i.e.
  `itemSpacingBefore` still returns 2 across a coordinate change.
- Monitor behavioral test
  `TestTranscriptLineRangesMatchRenderedRows` that renders the
  synthetic multi-item page including at least one zero-height,
  one trimmed-edges, and one tinted-padding item, then asserts every
  `chatItemLineRanges` entry maps to a slice of the transcript body
  whose newline count matches `end - start`. This is the FR-04.9 hard
  invariant behind `n`/`N` navigation.
- Terminal capture: `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-proofs/`
  contains an 80-column Monitor scene with (i) a text item, (ii) a
  tool-exchange card, (iii) a zero-height/skipped filtered item, and
  (iv) another card, plus a `.notes.txt` recording terminal size,
  `transcriptInnerW`, cursor position, and the exact blank-line count
  observed between each pair of visible items. A companion capture at
  the same terminal size against `main` (recorded as an ANSI capture
  only, saved next to the new capture as `-baseline.ansi`) documents
  the current-behavior gap so the reviewer can compare rows directly.
- Root acceptance evidence: `go build ./cmd/jig`, `go test ./...`,
  `go vet ./...`, `gofmt -l <changed-go-files>`, `git diff --check`,
  and the targeted TUI race (`go test -race ./internal/tui/...`) under
  `25-proofs/25-task-4-acceptance/`. These are implementation
  acceptance checks, not results claimed by this Phase 1 document.

## Non-Goals (Out of Scope)

1. **The boundary banner between execution coordinates** — slice 12.
   Slice 04 owns the blank lines around a coordinate change; the
   centered banner glyph or label that goes into them is a separate
   slice. This spec preserves the two-line gap explicitly so slice 12
   has somewhere to sit.
2. **The user-message bubble tint and lazy build** — slice 09. FR-04.3
   is the raw-bytes trimming property slice 09 depends on; it is
   proved here with a synthetic fixture but the bubble itself is not
   introduced.
3. **The tool detail sections refactor** — slice 05. `writeItemDetail`
   and `writeNewCodeCards` continue to run below the card frame in
   their current six- and four-space indented forms. Slice 04 does not
   move detail bodies into card sections and does not change their
   indentation.
4. **omp's native-scrollback retirement** (append-only stable-row
   ledger, freeze-on-drift, two-phase offer/acknowledge, pinned-frontier
   watchdog) — epic NG1. jig owns a Bubble Tea viewport; those pieces
   do not apply.
5. **omp's proportional viewport allocator and emergency-row mechanism**
   — epic NG3. jig has a scrollbar; the allocator is a full-terminal
   REPL concern.
6. **Redesigning `itemSpacingBefore` into a per-render-output rule that
   drops the `transcriptItem` kinds table.** The kind table remains the
   input; slice 04 keeps the execution-coordinate rule and only changes
   *what counts as an item for the purpose of applying the rule* (a
   zero-height item is not counted). Fully re-deriving spacing from
   rendered output — as the source slice suggests — is deferred until
   slices 05/09 introduce items that motivate more than the zero-height
   guard.
7. **Retiring or replacing `stripBlankEdges`.** It stays in use for
   glamour output. Renaming or unifying it with the new helper is
   deferred.
8. **Changing the transcript wire format**, `toolcall.Activity`, or
   `chatItems` normalization — epic NG5 / CC-12.
9. **Any other epic slice.** No card primitive change (slice 01), no
   header grammar change (slice 02), no selection affordance change
   (slice 03), no detail sections (slice 05), no truncation vocabulary
   (slice 06), no diff renderer (slice 07), no grouped-read tree
   (slice 08), no message framing (slice 09), no inline thinking (slice
   10), no per-step metadata row (slice 11), no boundary banner (slice
   12), no liveness/spinners (slice 13), no glyph presets (slice 14),
   no inline arg formatting (slice 15). No harness or backend change.

## Design Considerations

The single rule is imported from oh-my-pi and adapted for jig:

- **In omp**, `transcript-container.ts:532-545` iterates entries and
  writes `if (block.length === 0) continue; if (rows.length > 0)
  rows.push("");` around each `renderEntry(entry)`. The uniform join is
  one blank line, and the empty-block guard prevents a filtered/hidden
  block from leaving a separator behind. `transcript-container.ts:128-134`
  runs `trimBlankEdges` on `entry` rows before the join, where
  `isPlainBlank(line)` tests the **raw** string. A tinted card padding
  row contains `\x1b[48;2;…m` — non-whitespace — so it survives
  trimming; an untinted `Spacer(1)` gets eaten.
- **In jig**, `itemSpacingBefore` already carries genuine structural
  information omp lacks: a change in generation/iteration/attempt
  emits two blank lines, not one. That rule is stronger than omp's flat
  rhythm and slice 12 depends on it. Slice 04 keeps
  `itemSpacingBefore` as the join function and only imports the two
  discipline rules from omp: the zero-height guard and the raw-bytes
  edge trim.

Refactored loop (illustrative, not the final diff):

```
lastRenderedIdx := -1
for i, item := range m.chatVisibleItems {
    var scratch strings.Builder
    m.writeTranscriptItem(&scratch, item, i == m.chatItemCursor)
    body := trimStructuralBlankEdges(scratch.String())
    if body == "" {
        continue
    }
    if lastRenderedIdx >= 0 {
        for range itemSpacingBefore(m.chatVisibleItems[lastRenderedIdx], item) {
            b.WriteString("\n")
            line++
        }
    }
    start := line
    b.WriteString(body)
    b.WriteString("\n")
    line += strings.Count(body, "\n") + 1
    m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}] = lineRange{start: start, end: line - 1}
    lastRenderedIdx = i
}
```

Two implementation details worth calling out here:

1. **`writeTranscriptItem`** in the sketch stands for the extracted
   per-item render body — every existing case arm from the current
   `itemTranscriptBody` moves into it verbatim, keyed by
   `switch item.kind`. Extracting the arms into a helper is the cheap
   way to make the scratch-buffer refactor readable; it is not a
   requirement of the FRs. If the reviewer prefers the arms stay
   inline against a per-iteration `scratch` variable, that is
   acceptable too — the observable behavior is what FR-04.5 pins.
2. **The trailing newline is explicit.** Existing case arms embed their
   own `"\n"` at the end of the last row they write. Trimming with
   `trimStructuralBlankEdges` drops that trailing newline (because
   `strings.Split(s, "\n")` yields an empty last element only when `s`
   ends in `\n`, and the trimmer's Split loop treats an empty tail as
   structurally blank and drops it). The refactor reintroduces one
   `"\n"` explicitly after `body` so the next iteration's separator
   loop starts on a fresh line and `line` still equals the number of
   newlines emitted. FR-04.9 is verified against this counting.

**Choice of predicate — `isStructuralBlank` over reusing `stripBlankEdges`.**
The source slice proposes two options: (a) a raw-bytes predicate, or
(b) an explicit "this row is structural padding" marker owned by the
card API. This slice chooses (a) because (i) the card API today emits
no structural padding rows — its output is always header + content +
bottom, all of which contain visible glyphs — so option (b) would be a
speculative addition to a working primitive, and (ii) slice 09 already
plans to emit tinted padding rows through the same card body path, so
the raw-bytes predicate lands exactly the invariant slice 09 depends
on without cross-slice coupling. Option (b) remains available for a
later slice if a card kind gains a distinct "padding" line concept.

**Interaction with `stripBlankEdges`.** The two helpers are semantic
opposites and both are correct in their respective call sites:

| Helper | Predicate on a line | Correct use |
|---|---|---|
| `stripBlankEdges` (existing) | blank when `strings.TrimSpace(stripSGR(line)) == ""` | Glamour output whose leading/trailing rows contain only style bytes and should be trimmed — e.g. `renderNewCodeCard`, `fileBody`. |
| `trimStructuralBlankEdges` (new) | blank when the raw bytes contain no non-whitespace character | Per-item trimming inside `itemTranscriptBody` where tinted padding rows are semantically content. |

The two coexist. FR-04.4 makes that explicit and a grep-based test
locks the call sites.

**Choice not to fully re-derive spacing from rendered output.** The
source slice suggests re-deriving `itemSpacingBefore` from what the
item actually rendered. Slice 04 does not do that. The kind table is
still correct for every current item kind; the only mismatch it
produces today is a spurious gap when a rendered output is
zero-height, which the zero-height guard fixes directly. A full
re-derivation would require every item kind to declare its structural
padding intent, which is out-of-scope work for slices that do not yet
exist. Q-04.1 records this choice explicitly.

**Selection prefix.** Slice 03 stabilized the two-cell selection
prefix, so both `"  "` and `"▌ "` measure to two visible cells and
the card frame is at a stable column. Slice 04 does not change any
selection behavior and does not touch `renderToolExchangeCard`'s
`available = transcriptInnerW - 2` math. The card cache identity from
slice 01 is unchanged.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md),
  [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), and [Testing](../../TESTING.md).
- Add new helpers to `internal/tui/monitor/monitor_transcript.go` next
  to `stripBlankEdges` and `stripSGR`. Do not add a package-level
  `var …Style = lipgloss.NewStyle()`, a new hex constant, a new glyph
  literal, or a new theme mutation at a renderer call site. Palette
  and style ownership stays in `internal/tui/shared/palette.go` and
  `internal/tui/shared/styles.go`. Slice 04 is presentation
  restructuring with no palette, style, or glyph additions.
- Use pinned Go (1.25.12) and Charm v2 dependencies. Measure display
  cells with `lipgloss.Width` and use ANSI-aware `stripSGR` only where
  SGR-stripped blankness is the correct predicate; use the new
  `isStructuralBlank` where raw-bytes blankness is the correct
  predicate.
- Preserve persistence-off semantics: with `RunDir == ""` the
  transcript renders the empty state and the item loop is not
  entered.
- Use table-driven synthetic transcript fixtures (`syntheticExchange`,
  `syntheticExchangePage`, `syntheticTranscriptCardVisualPage`,
  `newMonitorWithSteps`, `setChatPage`). Never read real `.jig/` data
  for fixtures or proofs.
- Do not preserve deprecated behavior as a fallback. If the refactor
  makes the current `if i > 0 { for range itemSpacingBefore(...) }`
  block obsolete, delete it in the same change — do not leave a
  guarded second path.
- Format only changed Go files with `gofmt -w`. Do not rewrite
  unrelated files merely to run a formatting check.
- Root acceptance is `go build ./cmd/jig`, `go test ./...`, `go vet
  ./...`, `git diff --check`, plus a targeted TUI race
  (`go test -race ./internal/tui/...`) matching the pattern
  established by slices 02 and 03.

## Technical Considerations

### Scope of the code change

Production files touched by slice 04 are limited to two:

- `internal/tui/monitor/monitor_transcript.go` — add
  `isStructuralBlank` and `trimStructuralBlankEdges` next to the
  existing `stripBlankEdges` / `stripSGR` block. Update
  `stripBlankEdges`'s doc comment to name `trimStructuralBlankEdges` as
  its raw-bytes sibling so a future reader is not tempted to unify them.
- `internal/tui/monitor/monitor_transcript_items_view.go` — refactor
  `itemTranscriptBody` to (a) route each item's render through a
  per-iteration `strings.Builder`, (b) trim with
  `trimStructuralBlankEdges`, (c) skip zero-height items entirely
  (both separator and `chatItemLineRanges` entry), and (d) compute the
  separator against the last item that actually contributed content.
  Update the doc comment on `itemTranscriptBody` to mention the two
  invariants (zero-height guard, structural edge trim).

Related non-production file touched:

- `internal/tui/monitor/monitor_transcript_items.go` — update the doc
  comment on `itemSpacingBefore` (see FR-04.12) to state (a) that the
  separator only fires between two items that both contributed
  content, and (b) that a coordinate change remains a two-line gap
  because slice 12's boundary banner sits inside it. Do not change
  the function body.

### Tests to add or update

New tests belong beside their subjects:

| Test | File |
|---|---|
| `TestIsStructuralBlankRawBytesSemantics` | `internal/tui/monitor/monitor_transcript_test.go` (or a new `monitor_vertical_rhythm_test.go` next to the items view) |
| `TestTrimStructuralBlankEdges` | same as above |
| `TestTranscriptZeroHeightItemContributesNothing` | `internal/tui/monitor/monitor_transcript_items_view_test.go` (or new `_vertical_rhythm_test.go` sibling) |
| `TestTranscriptTrimsStructuralEdgeBlanksBetweenItems` | same |
| `TestTranscriptPreservesTintedPaddingRow` | same |
| `TestTranscriptConsecutiveTextItemsSameRoleNoGap` | same (may reuse existing fixture) |
| `TestTranscriptExecutionCoordinateGapPreserved` | same |
| `TestTranscriptLineRangesMatchRenderedRows` | same |
| Grep-based test that `trimStructuralBlankEdges` is called only from `itemTranscriptBody` and `stripBlankEdges` is called only from `renderNewCodeCard` / `fileBody` | new file or appended to existing `monitor_transcript_test.go`; use `os.ReadFile` on the two Monitor `.go` files |

Existing tests to re-run and confirm unchanged:

| Test | File | Expected outcome |
|---|---|---|
| `TestToolExchangeHeaderCardStatesAndWidths` | `monitor_transcript_items_view_test.go` | Passes unchanged — card output has no structural edge blanks. |
| `TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes` | same | Passes unchanged. |
| `TestTranscriptCardLineRangesCachedAndFresh` | `monitor_transcript_card_test.go` | The existing `{0, 1}` first-card range and the inter-item spacing assertions still hold because trimming a card that emits no blank edges is a no-op. |
| `TestTranscriptCardCacheLifecycleBounded` | same | Passes unchanged — cache eviction is untouched. |
| `TestTranscriptCardPageBoundsSearchExpansionAndClipboard` | same | Passes unchanged. |
| `TestSelectionAffordanceLineRangesStable` | `monitor_selection_affordance_test.go` | Passes unchanged — line ranges are unchanged for existing kinds. |

If any existing expected `lineRange` changes as a consequence of the
refactor, that is a signal the refactor is not behavior-preserving for
current fixtures; investigate and fix rather than update the expectation.
FR-04.10 records that constraint explicitly.

### Fixture for tinted padding preservation (FR-04.3 / G-04.c)

The tinted-padding assertion cannot be produced by any existing item
kind — no current renderer emits a blank-tinted row. The proof
constructs a synthetic item whose bytes are handed directly to the
trimmer or to a small test-only shim in `itemTranscriptBody`'s render
path. The safest construction is a table-driven case that:

1. Builds a rendered string of the shape
   `"\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) + "\x1b[49m\n" +
   "visible line\n" + "\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) +
   "\x1b[49m"` — a tinted-padding row above a visible line and a
   tinted-padding row below, matching the exact background sequence
   `card.go` emits for `CardPending`/`CardSuccess` (`\x1b[48;2;26;25;31m`
   / `\x1b[49m`).
2. Passes that string through `trimStructuralBlankEdges` and asserts
   byte-equality — no trimming occurred because neither padding row is
   structurally blank.
3. Optionally, in `TestTranscriptPreservesTintedPaddingRow`, injects a
   `transcriptItem` whose renderer emits those exact bytes via a
   test-only branch (e.g. a synthetic `transcriptItemText` whose
   glamour output is stubbed, or an inline construction in the test
   that seats an item at a specific index and asserts the final
   transcript body contains the padding sequence).

The background sequence is stable — `card.go`'s `finishRow` hard-codes
`"\x1b[48;2;26;25;31m"` for neutral cards and `"\x1b[48;2;42;26;30m"`
for error cards. The test may reference either sequence directly; if
the palette changes in a later slice, the test's assertion documents
the coupling and fails fast.

### Line accounting invariant

`chatItemLineRanges` is consumed by `n`/`N` navigation
(`monitor_layout.go:339`) and by search
(`monitor_search.go:322-327`). Both consumers treat `end - start + 1`
as the item's row count. The refactor guarantees that invariant by
computing `line += strings.Count(body, "\n") + 1` where `body` is the
trimmed bytes without its trailing newline; the `+ 1` accounts for the
explicit `"\n"` written after `body`. FR-04.9's test
(`TestTranscriptLineRangesMatchRenderedRows`) verifies this by
independently splitting the emitted transcript body on `"\n"` and
comparing each item's `lineRange` to the actual row indices its bytes
occupy.

### Cache and eviction unchanged

`transcriptRenderKey` is unchanged. `renderToolExchangeCard` continues
to invalidate old card variants on its own; no new cache eviction is
introduced. The scratch buffer refactor happens outside the cache
path: `renderToolExchangeCard` is called from a case arm inside the
per-item render helper, and its output is written to that item's
scratch buffer instead of directly to the transcript body. The bytes
returned by the cache are identical to today.

### Persistence-off

`itemTranscriptBody` is called from `chatBody`, which is not entered
when `chatEntries` is empty. Slice 04 does not change either call site.
The empty-state path in `chatBody` remains the correct rendering when
`RunDir == ""`.

### Deliberate deviation from external guidance

- omp's transcript container is the reference for the two discipline
  rules imported here. omp's flat one-blank-line separator is not
  imported; jig keeps `itemSpacingBefore` and its two-line coordinate
  gap. The source slice records this as "closer to correct than omp's"
  and slice 12 depends on it.
- omp's `trimBlankEdges` uses a `/\S/` regex; jig implements the same
  semantics with a byte loop over ASCII whitespace plus an explicit
  check for `\x1b`. The behavioral outcome is identical because a
  non-whitespace byte causes both predicates to classify the line as
  content.
- omp's structural rule that "components liberally prepend
  `new Spacer(1)`" is imported only as a discipline — jig components
  do not currently prepend spacers, and this slice does not add them.

## Security Considerations

Presentation-only work introduces no credentials, no external writes,
and no new input surfaces. The trimmer inspects the exact bytes the
existing renderers already write and drops empty ones; it does not
add new sanitization, does not interpret ANSI, and does not modify
transcript content. The transcript wire format is untouched (epic
CC-12). Proofs must use fabricated synthetic fixtures — never real
`.jig/` data or captured operator output — per repository standards.

## Success Metrics

1. `isStructuralBlank` and `trimStructuralBlankEdges` unit tables pass
   with the expected true/false outcomes for every listed input,
   including the tinted-padding case and the Glamour-shaped blank
   case (FR-04.1, FR-04.2, FR-04.3).
2. `TestTranscriptZeroHeightItemContributesNothing` asserts exactly
   `itemSpacingBefore(previous, next)` blank lines around the skipped
   item, no entry in `chatItemLineRanges` for the skipped item, and
   the neighbors' `lineRange` values match the no-skipped-item
   baseline (FR-04.5, FR-04.6, FR-04.9).
3. `TestTranscriptTrimsStructuralEdgeBlanksBetweenItems` asserts that
   an item with leading and trailing blanks produces the same number
   of blank rows around it as an item without them (FR-04.7).
4. `TestTranscriptPreservesTintedPaddingRow` asserts a tinted-padding
   row inside an item's rendered bytes survives the trim, byte-for-byte
   (FR-04.3).
5. `TestTranscriptConsecutiveTextItemsSameRoleNoGap` and
   `TestTranscriptExecutionCoordinateGapPreserved` assert
   `itemSpacingBefore` still returns 0 and 2 respectively at the
   relevant transitions (FR-04.8, FR-04.12).
6. `TestTranscriptLineRangesMatchRenderedRows` asserts every
   `chatItemLineRanges` entry maps to a slice of the transcript body
   whose newline count equals `end - start` (FR-04.9).
7. Every existing test in `internal/tui/monitor/` that observes
   `lineRange` values, card row counts, or prefix widths continues to
   pass without weakened expectations (FR-04.10, FR-04.11).
8. `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test
   -race ./internal/tui/...`, `gofmt -l <changed-go-files>` (empty),
   and `git diff --check` all pass on the recorded validation machine.
9. Manual acceptance: rendering the synthetic proof fixture at 80
   columns shows one blank line between adjacent same-coordinate items,
   two blank lines across a coordinate change, and no visible gap where
   the filtered/zero-height item sits, matching the baseline capture's
   layout except at the intended sites.

## Open Questions

The following are non-blocking. They record decisions the slice
consciously did not take and are not required for the FRs to hold.

1. **Q-04.1** Should `itemSpacingBefore` be re-derived from the item's
   rendered output rather than from its kind + coordinate pair? The
   source slice recommends this in principle; slice 04 does not do it
   because no current renderer produces spacing incompatible with the
   kind table. Revisit when slice 09 (message framing) or slice 05
   (tool detail sections) lands an item whose rendered rhythm cannot
   be predicted from its kind.
2. **Q-04.2** Is the two-line execution-coordinate gap still right
   once slice 12's boundary banner sits inside it? Slice 12 is the
   correct place to tune the value. Slice 04 preserves the gap
   verbatim.
3. **Q-04.3** Should the card API grow an explicit "structural
   padding" marker so the trimmer can consult a flag rather than
   raw-byte semantics? Deferred; the raw-bytes predicate is
   sufficient for slice 09's tinted bubble and slice 05's card body,
   and adds no coupling between the trimmer and the card primitive.
4. **Q-04.4** Should `stripBlankEdges` and `trimStructuralBlankEdges`
   be unified once every glamour output has migrated into card
   sections? Deferred — the two semantics are genuinely different for
   as long as any code path calls glamour outside a card body.
