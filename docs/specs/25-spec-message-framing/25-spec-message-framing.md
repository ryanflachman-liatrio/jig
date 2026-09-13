# 25-spec-message-framing.md

## Introduction/Overview

Replace the literal `"User"` label the Monitor transcript prepends above every
operator-authored text block with a background-tinted message bubble whose fill
spans the panel's inner width and includes one tinted padding row above and one
below the content. In the same slice, stop invoking the glamour markdown
renderer for large operator-authored text blocks on first paint: a block whose
raw size exceeds a named byte threshold shall render as one dim summary row
that names the block, its byte size, and its line count, and its full markdown
shall be produced only when the operator expands it through the existing
per-item toggle.

Today `writeTranscriptItem` handles the `transcriptItemText` case by rendering
markdown eagerly through `m.renderMarkdown(item.primary.key, block.Text)` and,
when the block's role is `transcript.RoleUser`, prepending
`shared.Theme.Chat.UserGuidance.Render("User")` on its own row
(`internal/tui/monitor/monitor_transcript_items_view.go:80-93`). The label is
the last piece of explicit role chrome in the panel and the eager markdown
build is a real performance hazard when a resumed-session prompt or a future
context-injection block carries kilobytes of prose. Slice 09 removes the label,
introduces a bordered-less bubble presentation reusing slice 01's SGR
stabilization discipline through a shared primitive, and defers markdown
construction for outsized user text.

Source: [OMP transcript parity, slice 09](../../epics/omp-transcript-parity/slices/09-message-framing.md).
Depends on: slice 04 (raw-bytes edge trimming). The tinted padding rows this
slice emits contain the shared card background SGR (`\x1b[48;2;…m`), so slice
04's `trimStructuralBlankEdges` predicate preserves them as content while a
plain `" "` row is still trimmed — FR-04.3 is the exact discipline slice 09
depends on. This document is Phase 1 output; it does not authorize implementing
slice 10 (inline thinking) or any other epic slice.

## Goals

- **G-09.a** An operator-authored text item shall render as a tinted bubble
  spanning the transcript panel's content width, with one tinted blank row
  above and one below the wrapped content, and no literal `"User"` label.
- **G-09.b** Assistant prose shall retain its current 1-column inset rendering
  with no background tint, at the same horizontal offset as an operator
  bubble's content, so the two roles are distinguished only by fill.
- **G-09.c** A user text block whose raw byte size exceeds a named collapse
  threshold shall render as one dim summary row (`<label> · <size> · <n>
  lines`) and shall not invoke `renderMarkdown` on that block's `blockKey`.
- **G-09.d** Expanding a collapsed user text item through the existing per-item
  toggle shall render its full markdown, populate `chatItemRendered` for the
  first time on that block, and keep line-range accounting accurate.
- **G-09.e** A tinted padding row emitted by the bubble shall survive slice
  04's per-item edge trimming and shall not merge with the inter-item
  separator; a hidden or filtered user item shall still leave no ghost gap.

## User Stories

- **As an operator scanning a conversation with `n`/`N`**, I want operator
  input to read as one visually distinct block rather than as a bare `User`
  word above a paragraph, so that I can skim role transitions without reading.
- **As an operator resuming a session with a large pasted document**, I want
  the transcript to open instantly and show me a one-line summary of the input
  I typed, so that the panel is not blocked on glamour laying out kilobytes of
  prose before the viewport clip.
- **As an operator inspecting a collapsed message**, I want the existing
  transcript toggle to reveal the full markdown of that message with correct
  line-range accounting, so that expansion behaves identically to every other
  disclosure in the panel.
- **As a maintainer**, I want the tinted bubble to reuse the shared card
  primitive's SGR stabilization pass rather than reimplementing it, so that
  slice 01's CC-9 resolution is applied consistently across every tinted
  surface.
- **As a maintainer implementing slice 10 (inline thinking)**, I want the
  bubble primitive to be a first-class shared component with a well-defined
  contract, so that thinking bubbles can share exactly the tinted-content
  discipline without reintroducing the label-and-prose grammar this slice
  removes.

## Demoable Units of Work

### Unit 1: Shared tinted-message bubble primitive

**Purpose:** Introduce a small, pure primitive in `internal/tui/shared` that
renders a background-tinted, borderless block whose width, padding, and body
wrapping semantics mirror `RenderCard` but whose output has no top/bottom
border row and no corner glyphs. Reuse `card.go`'s SGR stabilization pass so a
foreground-styled glamour paragraph does not punch holes through the tint. The
bubble knows nothing about transcript items or Monitor state.

**Functional Requirements:**

- **FR-09.1** The system shall provide a public struct
  `shared.MessageBubble` with fields `Content string`, `Width int`,
  `PadLeft *int`, `PadRight *int`, `PadTop *int`, `PadBottom *int`, and a
  `Style` field of type `lipgloss.Style` selecting the tint. Nil left padding
  defaults to `1`; nil right padding inherits the resolved left; nil top and
  bottom padding both default to `1`. Explicit zero on any padding field
  produces flush content on that side.
- **FR-09.2** The system shall provide a public function
  `shared.RenderMessageBubble(b MessageBubble) string` returning
  newline-separated rows without a trailing newline. When `b.Width <= 0`,
  when the resolved geometry leaves no content cell, or when `b.Content` is
  empty and every padding value is zero, the function shall return the empty
  string so callers can rely on it being a total function.
- **FR-09.3** The system shall provide a public function
  `shared.MessageBubbleContentWidth(width int, left, right *int) int` that
  reports the visible cell budget inside a bubble at the given width, using
  the same presence-aware padding resolver as `CardContentWidth`. This is the
  seam callers use to reflow their content through `ansi.Hardwrap` before
  handing it to `RenderMessageBubble`.
- **FR-09.4** For each visible-cell padded row, the bubble shall emit exactly
  `PadLeft` cells of tinted whitespace, the wrapped content padded to the
  content width with tinted fill, and `PadRight` cells of tinted whitespace.
  The top and bottom padding rows shall be full-width tinted whitespace with
  no borders. Every row shall end with a background-reset SGR
  (`\x1b[49m`) and shall begin with a background-set SGR reapplied through
  the same stabilization pass `card.go`'s `finishRow` uses (parameters
  matching `0` or `49` reintroduce the tint, `38/48/58` runs correctly skip
  their color arguments). RGB `0`s inside a `48;2;0;0;0` sequence shall not be
  treated as a background reset.
- **FR-09.5** The bubble primitive shall carry no border style, no corner
  glyph, no label, no state, and no divider. It shall not read from the
  `Theme.Card` sub-struct; its color comes exclusively from the `Style` field
  its caller supplies.
- **FR-09.6** `RenderMessageBubble` shall wrap `b.Content` at
  `MessageBubbleContentWidth(...)` using `ansi.Hardwrap` (already imported by
  `card.go`), preserving embedded newlines, trimming trailing whitespace per
  logical line, and never truncating a long unbroken word.

**Proof Artifacts:**

- Test: `TestRenderMessageBubbleGeometry` in
  `internal/tui/shared/bubble_test.go` tables the primitive over widths 40,
  60, and 90 columns and every padding permutation (nil/nil, nil/0, 0/nil,
  explicit asymmetric, oversized left that clamps, negative left that clamps
  to zero); asserts `lipgloss.Width` of every emitted row equals `Width`,
  that top and bottom padding row counts equal the resolved values, and that
  `Width < 3` returns the empty string.
- Test: `TestRenderMessageBubbleContentWidth` tables
  `MessageBubbleContentWidth` for widths 0, 1, 2, 3, 40, and 90 with every
  padding combination and asserts that the returned budget matches what
  `RenderMessageBubble` actually fills at those geometries.
- Test: `TestRenderMessageBubbleTintStabilization` feeds a synthetic content
  string containing `\x1b[0m`, `\x1b[49m`, `\x1b[m`, `\x1b[38;2;0;0;0m` (RGB
  zeros — not a reset), and a jig-themed glamour Go fence, walks every
  visible cell of every row with an SGR-state helper, and asserts the bubble
  tint is present at every cell of every content and padding row and absent
  from the sentinel row emitted after `RenderMessageBubble`.
- Test: `TestRenderMessageBubbleContent` covers embedded newlines, hard-wrap
  of unbroken words, leading indentation preservation, internal blank line
  preservation, and empty content with non-zero padding still emitting the
  padding rows.

### Unit 2: Bubble-based operator input rendering with lazy markdown

**Purpose:** Route `transcriptItemText` items whose role is
`transcript.RoleUser` through the bubble primitive; short bodies build glamour
prose inside the bubble as they do today, large bodies render as one dim
summary row and skip `renderMarkdown` entirely. Assistant prose is unchanged.

**Functional Requirements:**

- **FR-09.7** In `writeTranscriptItem`'s `transcriptItemText` arm, an item with
  `item.role == transcript.RoleUser` shall render through a new bubble render
  helper. The literal string `"User"` and the `shared.Theme.Chat.UserGuidance`
  style shall not appear on any user text row.
- **FR-09.8** An item with `item.role != transcript.RoleUser` (assistant
  prose) shall render exactly as today — glamour-rendered markdown at the
  panel's inner width with the existing selection-prefix landing behavior
  described by the current inline comment
  (`monitor_transcript_items_view.go:85-92`). No test that asserts assistant
  rendering shall need its expected bytes weakened.
- **FR-09.9** The bubble shall render at
  `available := m.transcriptInnerW - lipgloss.Width(prefix)` where `prefix`
  is the existing selection-affordance prefix (two visible cells whether or
  not the item is selected, slice 03). When `available < 3` the bubble shall
  emit no rows and the item shall contribute the empty string to the scratch
  buffer, so slice 04's zero-height guard drops it cleanly.
- **FR-09.10** A user text block shall be classified as **collapsed** iff its
  raw `len(block.Text)` exceeds a named constant `chatUserBubbleCollapseBytes`
  declared next to `chatExpandMax`, `chatWindowMax`,
  `chatBoundaryContextMax`, and `outputMaxLines` in
  `internal/tui/monitor/monitor_model.go`; its initial value shall be `4096`,
  matching the existing `chatExpandMax` budget so a size we have already
  decided is worth eliding when expanded is also the size worth deferring on
  first paint. When the block is not collapsed, its markdown shall be built
  through the existing `m.renderMarkdown(item.primary.key, block.Text)` path
  and placed inside the bubble.
- **FR-09.11** A **collapsed** user text block shall render as one dim
  summary row of the form
  `<label> · <size> · <n> <line|lines>` inside the bubble. The label shall be
  the first Markdown ATX heading (`# `, `## `, `### `, up to six leading `#`
  followed by at least one space) that appears at the start of a line in the
  block's text, with the leading `#`s and spaces stripped; when no such
  heading exists the label shall be the string `"Message"`. Size shall be the
  block's raw byte length formatted as `<n> B` for `n < 1024` and
  `<n.n> KiB` for `n >= 1024` (one decimal place, no separator localization).
  Line count shall be `strings.Count(block.Text, "\n") + 1`. The three fields
  shall be joined by ` · `. When the composed row exceeds the bubble's
  content width, it shall be truncated using `shared.TruncateTitle` (which
  already threads through `ansi.Truncate` with an `…` cap).
- **FR-09.12** A collapsed user text item **shall not invoke `renderMarkdown`
  or `renderInsetMarkdown`** for the block's `blockKey`. In particular,
  `m.chatRendered[item.primary.key]` shall not be populated as a side effect
  of first paint or of any subsequent render at the same expansion state, and
  the summary row shall not be written into that cache.
- **FR-09.13** `itemHasDetail(item)` (`monitor_transcript_items_view.go:289`)
  shall return `true` for a collapsed user text item so the existing
  disclosure marker (`shared.CollapsedMarker` / `shared.ExpandedMarker`) is
  drawn and the existing per-item toggle key `m.keys.Toggle` is honored. It
  shall continue to return `false` for a non-collapsed user text item so
  short messages remain non-toggleable, exactly as today.
- **FR-09.14** Expanding a collapsed user text item — either through
  `m.chatItemExpand[item.key] = true` or through the global
  `m.chatItemExpandAll` override — shall populate the bubble's content with
  the full glamour render via `m.renderMarkdown`, and shall reuse the same
  `m.chatRendered` cache the rest of the transcript uses.
- **FR-09.15** The collapse threshold `chatUserBubbleCollapseBytes` shall be
  read on every render (through the constant reference, not a copy) so
  hot-swapping its value at build time immediately changes bubble behavior
  in tests without additional wiring.

**Proof Artifacts:**

- Test: `TestUserBubbleReplacesLiteralUserLabel` in
  `internal/tui/monitor/monitor_message_framing_test.go` seats a small user
  text item (100 bytes) and asserts that the rendered transcript body
  contains no `"User"` substring, that at least one row has visible-cell
  width equal to `available` (the bubble's outer width), that the top and
  bottom rows are tinted padding rows recognisable as
  `strings.Contains(row, "\x1b[48;")` and slice-04-non-blank per
  `!isStructuralBlank(row)`, and that the row containing the message content
  begins with the operator's prose at the same horizontal offset as an
  assistant row would.
- Test: `TestUserBubbleAssistantAlignment` renders one user text item and
  one assistant text item adjacent to each other and asserts that the
  starting column of the first visible character of each item's content
  (after slice-03 selection prefix) is equal.
- Test: `TestUserBubbleCollapsedSummary` uses a fake glamour renderer whose
  `Render` method increments a counter, seats a user text item whose body is
  `strings.Repeat("x ", 3000)` (~6 KiB), and asserts (a) the rendered body
  contains a `· <n> B|<n.n> KiB ·` size fragment and a line count, (b) the
  counter is exactly zero, and (c)
  `m.chatRendered[item.primary.key]` is not populated after the render.
- Test: `TestUserBubbleExpandRendersMarkdown` sets `chatItemExpand[key] =
  true`, renders again with the same fake renderer, and asserts the counter
  has incremented to exactly one and that `m.chatRendered[key]` now holds
  the rendered output. A follow-up call at the same expansion state re-uses
  the cache without incrementing the counter.
- Test: `TestUserBubbleSummaryLabelFromHeading` covers a block whose first
  line is `# Investigate flaky test`, a block whose first non-blank line is
  a heading, a block with no heading (falls back to `"Message"`), and a
  block whose heading is longer than the bubble's content width (truncated
  with `…`).
- Test: `TestUserBubbleTintedPaddingSurvivesEdgeTrim` seats a user text
  bubble between two assistant text items and asserts (via slice 04's
  `isStructuralBlank` predicate) that both the leading and trailing tinted
  padding rows survive `trimStructuralBlankEdges` byte-for-byte.

### Unit 3: Cached bubble rendering, line-range accounting, and lifecycle integrity

**Purpose:** Hook the bubble render into the existing item-render cache and
line-range accounting so navigation, filtering, page replacement, and
same-step resize behave identically to a tool exchange card. Ensure the lazy
build interacts correctly with `chatItemExpandAll`, `prunePageState`,
`reloadTranscript`, and persistence-off.

**Functional Requirements:**

- **FR-09.16** Add a `transcriptRenderBubble` value to the
  `transcriptRenderSurface` enum in
  `internal/tui/monitor/monitor_model.go:489-497` and cache each rendered
  bubble at key `transcriptRenderKey{itemKey: item.key, surface:
  transcriptRenderBubble, width: available, expanded, selected, state: 0,
  header: ""}`. On a miss, remove any prior `transcriptRenderBubble` entry
  for the same `itemKey` before writing the new one so cache size stays
  bounded by the loaded page (mirrors `renderToolExchangeCard`).
- **FR-09.17** The bubble cache shall be invalidated whenever `setChatPage`
  clears `chatItemRendered` (already true for `transcriptRenderCard`;
  extending to the new surface is one line) and whenever
  `rebuildRenderer` widens or narrows the transcript inner width. Same-step
  reload shall preserve the caller's expansion choice through
  `rebuildTranscriptItemState`'s saved-cursor path just like tool
  exchanges.
- **FR-09.18** `chatItemLineRanges` shall receive exactly one entry per
  user text item and its `end - start + 1` shall equal the number of
  physical rows emitted (top-pad rows + wrapped content rows +
  bottom-pad rows in the collapsed and expanded cases). The entry shall be
  computed from the actual bytes written by `itemTranscriptBody`, not from
  the item's kind, and shall remain correct after selection changes,
  local/global toggle, filter, search, and width change.
- **FR-09.19** `prunePageState` (`monitor_transcript.go:347`) shall not
  need modification: the new cache surface is keyed by `item.key` which
  already participates in the `loadedItems` set. A test shall
  independently prove that a bubble entry left over from a previous page
  is removed after `setChatPage` replaces the page.
- **FR-09.20** Persistence-off (`RunDir == ""`) shall retain the current
  empty-state path. The bubble helper shall not read from the filesystem,
  the event bus, or any Monitor field not already exposed to the item
  renderer. Slice 09 shall introduce no new persistence dependency.
- **FR-09.21** `chatItemExpandAll` (`o` key) shall continue to override
  per-item `chatItemExpand` without rewriting individual entries: an
  operator who expanded one user bubble, pressed `o`, and pressed `o`
  again shall find the same one bubble expanded (i.e. slice 08's
  `expand-all` semantics are preserved verbatim).
- **FR-09.22** `defaultExpandEditCodeItems`
  (`monitor_transcript.go:182-189`) shall continue to auto-expand
  structured edits and shall not auto-expand user text items, collapsed
  or not. `itemHasStructuredDiff` shall not return `true` for a
  `transcriptItemText` item.

**Proof Artifacts:**

- Test: `TestUserBubbleCacheLifecycle` seats one user text item, renders
  it, asserts exactly one `transcriptRenderBubble` entry exists in
  `chatItemRendered`; changes the width, re-renders, asserts the entry
  was replaced; toggles selection, re-renders, asserts the entry was
  replaced; toggles expansion, re-renders, asserts the entry was
  replaced; replaces the page, asserts the entry is gone.
- Test: `TestUserBubbleLineRangesMatchRenderedRows` renders a page with
  one short user bubble, one long collapsed user bubble, one expanded
  version of the same long bubble, and one assistant prose item; splits
  the emitted transcript body on `"\n"` and asserts that
  `chatItemLineRanges[itemKey].end - .start + 1` equals the actual row
  count for every item and matches the expected geometry (pad-top +
  content rows + pad-bottom).
- Test: `TestUserBubbleExpandAllRespectsPerItem` seats two user bubbles,
  sets `chatItemExpand[itemA.key] = true`, presses `o` to set
  `chatItemExpandAll = true`, presses `o` again to clear it, and asserts
  that `chatItemExpand[itemA.key]` is still `true` and
  `chatItemExpand[itemB.key]` is still absent (unchanged).
- Test: `TestUserBubblePersistenceOff` sets `RunDir = ""`, calls
  `reloadTranscript` and `loadChatTail`, and asserts the existing
  persistence-off empty state renders and the bubble path is never
  entered.
- Test: `TestUserBubbleDefaultExpandUnaffected` seats a page containing a
  long user bubble and a structured edit tool exchange, calls
  `defaultExpandEditCodeItems`, and asserts that `chatItemExpand`
  contains an entry only for the edit exchange (user bubble is not
  auto-expanded even though it is collapsed).

## Non-Goals (Out of Scope)

1. **The reaction badge** described at
   `packages/coding-agent/src/modes/components/user-message.ts:92-96`. jig's
   assistant reply grammar has no leading emoji contract; adding one would
   require a separate slice touching harness normalization.
2. **OSC 133 shell-integration prompt zones** — epic NG2.
3. **`transcriptItemSystem` rendering**, which is already correct via
   `writeVerbatim` (`monitor_transcript_items_view.go:94-96`).
4. **Role-based per-item timestamps.** jig removed the `#seq role … ts`
   header path in slice 00 and this slice does not reintroduce it.
5. **A provenance field on `transcript.Entry` distinguishing
   operator-typed from engine-injected user text.** Adding an attribution
   attribute is a wire-format change and is bounded by epic NG5. Slice 09
   uses a size threshold instead, and Open Question Q-09.5 records the
   trigger that would motivate the wire-format change.
6. **A `system` / command-output fill.** omp tints `customMessageBg`; jig's
   verbatim path is already visually distinct and reserving a second
   background for it exceeds this slice's charter (slice 09 Out of Scope
   bullet in the source).
7. **Inline thinking / streaming pulse** — slice 10. Slice 10 will consume
   this slice's `MessageBubble` primitive; slice 09 shall not preempt its
   contract.
8. **Any refactor of `renderMarkdown` or the `chatRendered` cache.** The
   lazy-build discipline is achieved by not calling `renderMarkdown` at
   the collapsed call site, not by changing what the renderer does when
   invoked.
9. **Boundary banners, per-step metadata rows, liveness spinners, glyph
   presets, or inline argument formatting** — slices 11 through 15.
10. **Changing the transcript wire format**, `toolcall.Activity`, or
    `chatItems` normalization — epic NG5 / CC-12.

## Design Considerations

### Bubble shape

omp's `renderOutputBlock` reference at
`packages/coding-agent/src/tui/output-block.ts:66-200` builds a full-width
tinted card, spelling `paddingX = 1`, `paddingY = 1`, and running every
content and padding row through `applyBackgroundToLine(row, width, bgFn)`
after a `sgrSequence.ReplaceAllStringFunc` stabilization pass. jig's `card.go`
already implements the identical stabilization pass (`finishRow`,
`sgrResetsBackground`) for tool-exchange cards; the bubble reuses that pass by
factoring it — not by copy-pasting it — into a small internal helper both
`RenderCard` and `RenderMessageBubble` call.

The bubble intentionally omits the `╭╮╰╯` border row so the panel does not
draw a border-inside-a-border. This matches omp's variant
(`applyBackgroundToLine` without a frame) referenced in the epic's Risks
table as the narrow-width mitigation. It also keeps CC-6's manual-border
compositor (`shared/panel.go:196-204`) unused for user text — CC-6 is a
card-only contract.

### Palette and theme

The tint reuses `hexBBQ = "#2D2C36"` in `internal/tui/shared/palette.go:23`
— the `bgLeastVisible` step in the Charmtone Pantera background ladder —
which is already the `lipgloss.Color(hexBBQ)` value bound to `bgLeast` inside
`DefaultTheme` (`styles.go:237`) and already reused by `Chat.CodeBlock`
(`styles.go:315-318`). Adding a new palette token for this slice would
duplicate `hexBBQ` and is deliberately not done.

Add exactly one style field to `Styles.Chat` in `styles.go`:

- `UserBubble lipgloss.Style` — a `lipgloss.NewStyle().Background(bgLeast)`
  in `DefaultTheme`. Foreground is not set here so glamour's inherited
  foregrounds continue to paint the prose; the stabilization pass keeps the
  background alive across glamour's `\x1b[0m` resets. The
  `Chat.UserGuidance` field is removed in the same change (its sole caller
  disappears), and its removal is verified by `rg 'UserGuidance'` returning
  no matches under `internal/`.

The bubble's `Style` field receives `Theme.Chat.UserBubble` at the Monitor
call site so `RenderMessageBubble` remains pure and callable by future
consumers (slice 10) with a different token.

### Threshold semantics

The source slice (Q-09.1) asks whether jig's transcript already distinguishes
operator-typed from engine-injected `RoleUser` text. Investigation of
`internal/transcript/transcript.go:34-42` and `internal/runner/agent.go:264,
310-311, 379-381` establishes that:

- `RoleUser` in the wire format means "tool results fed back to the model"
  and, on `EventUserEnd` (`agent.go:378-381`), carries `BlockToolResult`
  blocks — not `BlockText`.
- The only path that emits `RoleUser + BlockText` in production today is
  `agent.go:310-311`, which persists the operator's resume message
  (`initialUserMsg`) at session open. That block is always operator-typed.

There is no attribution field distinguishing operator input from
context-injected content in the transcript today; adding one is a wire-format
change and out of scope (NG5). Slice 09 therefore falls back to the size
threshold the source slice offers as its alternative, and pins the constant
next to the existing budgets in `monitor_model.go`.

The threshold value `chatUserBubbleCollapseBytes = 4096` matches
`chatExpandMax = 4096` deliberately: a body that already elides its middle
inside the expand view (`monitor_transcript.go:665-675`) is a body worth
deferring on first paint as well. Q-09.4 records this coupling explicitly.

### Rendering order and slice-04 interaction

The item render helper writes the following bytes for a user text item:

1. `prefix` (two visible cells — slice 03).
2. `marker + " "` if `itemHasDetail(item)` (a collapsed body only).
3. The bubble rows produced by `RenderMessageBubble` at
   `available = transcriptInnerW - lipgloss.Width(prefix)`; each row is
   prefixed with `prefix` on carriage-return join, matching
   `prefixCardRows`'s pattern.
4. One trailing `"\n"` outside the bubble so the next-item separator lands
   on its own line, exactly as `renderToolExchangeCard`'s caller does today.

Slice 04's `trimStructuralBlankEdges` inspects raw bytes, so the tinted
top and bottom padding rows survive edge trimming (each contains
`\x1b[48;2;…m` bytes, which `isStructuralBlank` classifies as content). A
filtered or zero-height user item still contributes the empty string and
is dropped by slice 04's zero-height guard, preserving G-09.e.

### Interaction with the summary row

The summary row is a **fallback** for large blocks, not a card section. It
lives inside the bubble at the same 1-cell inset as full content: for a
collapsed body the bubble's `Content` is exactly the summary row (no
markdown), and `RenderMessageBubble` renders it as a single logical line
with `ansi.Hardwrap` at the content width. When the operator expands the
item, the same bubble is re-rendered with `Content` set to the glamour
output; the padding and stabilization discipline remains identical.

This is deliberately simpler than adding a "summary mode" to the bubble
primitive: the bubble takes a string, and the caller decides whether that
string is a summary or a rendered document. Keeping the primitive
content-agnostic lets slice 10 reuse it for streaming thinking bodies with
no interface change.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md), [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), and [Testing](../../TESTING.md).
- Add the bubble primitive next to `card.go` in
  `internal/tui/shared/bubble.go`; factor the SGR stabilization
  (`sgrResetsBackground` and the `sgrSequence.ReplaceAllStringFunc` pass)
  into an internal helper both `card.go` and `bubble.go` call. Do not add
  a package-level `var …Style = lipgloss.NewStyle()`, a new hex constant,
  or a new theme mutation at a renderer call site.
- Add `Chat.UserBubble` to `Styles` and initialize it in `DefaultTheme`
  from `bgLeast`. Remove `Chat.UserGuidance` in the same change (its
  only consumer is the branch this slice retires) and verify no other
  caller remains via `rg 'UserGuidance'` under `internal/`.
- Do not preserve deprecated behavior as a fallback. If retiring the
  `"User"` label breaks a test's expectation, update the test to the new
  expectation (a tinted row that contains no `"User"` substring); do not
  guard both paths with a build-time toggle.
- Use pinned Go (1.25.12) and Charm v2 dependencies. Measure display cells
  with `lipgloss.Width` and hard-wrap through `ansi.Hardwrap` from the
  already-imported `github.com/charmbracelet/x/ansi` module.
- Preserve persistence-off semantics: with `RunDir == ""` the transcript
  renders the empty state and the item loop is not entered. `RenderMessageBubble`
  performs no I/O.
- Use table-driven synthetic transcript fixtures
  (`syntheticExchange`, `syntheticExchangePage`,
  `syntheticTranscriptCardVisualPage`, `newMonitorWithSteps`,
  `setChatPage`). Never read real `.jig/` data for fixtures or proofs.
- Format only changed Go files with `gofmt -w`. Do not rewrite unrelated
  files merely to run a formatting check.
- Root acceptance is `go build ./cmd/jig`, `go test ./...`, `go vet ./...`,
  `git diff --check`, plus a targeted TUI race
  (`go test -race ./internal/tui/...`) matching the pattern established by
  slices 04, 07, and 08.

## Technical Considerations

### Scope of the code change

Production files touched by slice 09 are limited to five:

- `internal/tui/shared/bubble.go` — new file: `MessageBubble`, `RenderMessageBubble`,
  `MessageBubbleContentWidth`, and any newly-exported padding resolver
  shared with `card.go`.
- `internal/tui/shared/card.go` — factor the private SGR stabilization
  pass (`sgrSequence.ReplaceAllStringFunc` + `sgrResetsBackground`) so
  the bubble reuses it verbatim; no observable behavior change for
  cards. Keep `finishRow` as the card-specific caller.
- `internal/tui/shared/styles.go` — add `Chat.UserBubble` and initialize
  it in `DefaultTheme`; remove `Chat.UserGuidance` and its initializer.
- `internal/tui/monitor/monitor_model.go` — add
  `chatUserBubbleCollapseBytes = 4096` next to the existing chat
  budgets; add `transcriptRenderBubble` to the
  `transcriptRenderSurface` enum.
- `internal/tui/monitor/monitor_transcript_items_view.go` — replace the
  `transcriptItemText` arm with a role-aware dispatch: assistant prose
  unchanged, user prose routed through a new
  `renderUserMessageBubble` helper that implements FR-09.7 through
  FR-09.15 and reuses `chatItemRendered`.

Related non-production files touched:

- `internal/tui/monitor/monitor_transcript_items_view.go` — update the
  `itemHasDetail` predicate (FR-09.13) and its doc comment.
- No change is required to `monitor_transcript.go`, `monitor_read_group.go`,
  `monitor_transcript_items.go`, or `clipboard.go`. Clipboard capture
  already consumes the rendered item slice
  (`clipboard.go:296`) so a bubble is copied by the same code path that
  copies any other item.

### Tests to add or update

New tests belong beside their subjects:

| Test | File |
|---|---|
| `TestRenderMessageBubbleGeometry` | `internal/tui/shared/bubble_test.go` |
| `TestRenderMessageBubbleContentWidth` | same |
| `TestRenderMessageBubbleTintStabilization` | same |
| `TestRenderMessageBubbleContent` | same |
| `TestUserBubbleReplacesLiteralUserLabel` | `internal/tui/monitor/monitor_message_framing_test.go` (new) |
| `TestUserBubbleAssistantAlignment` | same |
| `TestUserBubbleCollapsedSummary` | same |
| `TestUserBubbleExpandRendersMarkdown` | same |
| `TestUserBubbleSummaryLabelFromHeading` | same |
| `TestUserBubbleTintedPaddingSurvivesEdgeTrim` | same |
| `TestUserBubbleCacheLifecycle` | `internal/tui/monitor/monitor_message_framing_cache_test.go` (new) |
| `TestUserBubbleLineRangesMatchRenderedRows` | same |
| `TestUserBubbleExpandAllRespectsPerItem` | same |
| `TestUserBubblePersistenceOff` | same |
| `TestUserBubbleDefaultExpandUnaffected` | same |

Existing tests to re-run and confirm still pass without weakened
expectations:

| Test | File | Expected outcome |
|---|---|---|
| `TestToolExchangeHeaderCardStatesAndWidths` | `monitor_transcript_items_view_test.go` | Unchanged — slice 09 does not touch tool cards. |
| `TestSelectionAffordanceLineRangesStable` | `monitor_selection_affordance_test.go` | Unchanged — user bubble width equals card width so the two-cell prefix rule is preserved. |
| `TestTranscriptCardLineRangesCachedAndFresh` | `monitor_transcript_card_test.go` | Unchanged — bubble cache is a separate surface. |
| `TestTranscriptZeroHeightItemContributesNothing` | `monitor_vertical_rhythm_test.go` | Unchanged — bubble emits a non-empty scratch or `""`. |
| `TestTranscriptPreservesTintedPaddingRow` | same | Unchanged — the discipline is exactly this slice's producer. |
| Every test that expected the literal string `"User"` in transcript output | across `internal/tui/monitor` | Updated in the same change to match the bubble; no test may be weakened without a documented reason. |

Any existing test that today writes `"User"` to a `wantBody` fixture will
fail; update those fixtures to the tinted bubble form.

### Cache identity

`transcriptRenderKey` already accommodates the bubble surface. Set `state
= 0` (a valid `toolDisplayState` value that means "unassigned" for
non-tool items) and `header = ""` — the header string is a card-only slot
and mixing it with an unrelated bubble field would blur cache identity.
The `expanded` and `selected` flags carry the meaningful distinctions.

### Fake glamour renderer for FR-09.12 proof

FR-09.12 requires proving that `renderMarkdown` is not invoked for
collapsed items. The cleanest proof injects a counting fake in place of
`m.renderer` / `m.insetRenderer`, then reads the counter after the bubble
render. The `glamour.TermRenderer` type is not an interface, so the fake
is a small wrapper struct that implements the same
`Render(text string) (string, error)` shape and is threaded into
`renderMarkdown` via a test-only seam already used by
`monitor_transcript_card_test.go`. The counter is a package-private
`sync/atomic` int on the wrapper.

If the render seam does not exist in the current test file yet, the
implementation task adds it as the minimum-invasive wrapper: an
unexported `type textRenderer interface{ Render(string) (string, error) }`
in `monitor_model.go`, an assignment
`var _ textRenderer = (*glamour.TermRenderer)(nil)` next to it, and a
`renderMarkdown` change that reads through the interface. Doing this in
the same task is acceptable because it is strictly narrower than the
lazy-build behavior it serves.

### Deliberate deviation from external guidance

- omp's user-message component tints padding rows with the `userMessageBg`
  theme token (`packages/coding-agent/src/modes/theme/tokens.ts`). jig
  reuses `hexBBQ` because the Charmtone Pantera dark theme already
  places that token at the `bgLeastVisible` step, which is the exact
  semantic omp uses.
- omp collapses **by provenance** (synthetic vs operator input), not by
  size. jig cannot reproduce that discipline without a wire-format
  attribution change (NG5); the size threshold is the safe alternative
  the source slice explicitly authorizes.
- omp lifts the assistant reply's opening emoji into the bubble's top
  padding row as a "reaction badge." jig has no analog and this slice
  does not introduce one (Non-Goal 1).

## Security Considerations

- Operator input can contain secrets the operator typed. It is already
  subject to the same redaction as the rest of the transcript
  (`internal/runner/agent.go:196-201` writes redacted `input.md`; the
  transcript blocks pass through `redactTranscriptBlocks` at
  `agent.go:295`). Slice 09 does not change redaction.
- A full-width background tint makes any trailing whitespace on a wrapped
  content line visible as tinted space. This is cosmetic, not an
  exposure: `RenderMessageBubble` right-trims each logical line
  (FR-09.6) before wrapping, so no line-shape metadata leaks through the
  tint.
- The lazy-build discipline reduces the number of bytes that reach
  glamour on first paint, which shrinks the exposure surface for any
  future glamour-parsing bug operating on operator-controlled content.
- Slice 09 introduces no new I/O, no new network surface, and no new
  external dependency. `RenderMessageBubble` is pure.

## Success Metrics

1. `TestRenderMessageBubbleGeometry`,
   `TestRenderMessageBubbleContentWidth`,
   `TestRenderMessageBubbleTintStabilization`, and
   `TestRenderMessageBubbleContent` pass at widths 40, 60, and 90
   columns, covering FR-09.1 through FR-09.6.
2. `TestUserBubbleReplacesLiteralUserLabel`,
   `TestUserBubbleAssistantAlignment`, and
   `TestUserBubbleTintedPaddingSurvivesEdgeTrim` pass, covering
   FR-09.7, FR-09.8, FR-09.9, and G-09.a/e.
3. `TestUserBubbleCollapsedSummary`, `TestUserBubbleExpandRendersMarkdown`,
   and `TestUserBubbleSummaryLabelFromHeading` pass, covering FR-09.10
   through FR-09.14 and G-09.c/d — including the assertion that the
   glamour renderer counter is zero for a collapsed body.
4. `TestUserBubbleCacheLifecycle`,
   `TestUserBubbleLineRangesMatchRenderedRows`, and
   `TestUserBubbleExpandAllRespectsPerItem` pass, covering FR-09.16
   through FR-09.22 and the navigation/line-range invariant.
5. `TestUserBubblePersistenceOff` and `TestUserBubbleDefaultExpandUnaffected`
   pass, proving the empty-state and auto-expand contracts are
   unchanged.
6. Every prior test in `internal/tui/monitor/` that asserts line-range
   values, prefix widths, or specific user-text bytes continues to pass
   after fixture updates that reflect the removed `"User"` label; no
   test's expectation is weakened.
7. `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race
   ./internal/tui/...`, `gofmt -l <changed-go-files>` (empty), and
   `git diff --check` all pass on the recorded validation machine.
8. Manual acceptance: rendering a synthetic transcript at 80 columns
   shows a tinted user bubble whose top and bottom rows are visibly
   filled with the panel's recessed background, no literal `"User"`
   text on any row, and — when the fixture includes a >4 KiB user body —
   a single `<label> · <size> · <n> lines` summary row inside the
   bubble that expands into full markdown when the operator presses
   the toggle key.

## Open Questions

The following are non-blocking. They record decisions the slice
consciously did not take and are not required for the FRs to hold.

1. **Q-09.1** Should jig extend `transcript.Entry` with a provenance
   attribute (`operator | injected | agent-attributed`) so the collapse
   discipline can match omp's exactly? Deferred; this is a wire-format
   change (NG5). The size threshold is a safe alternative until a
   context-injection or context-assembly step lands that emits large
   `RoleUser + BlockText` blocks with a distinct origin.
2. **Q-09.2** Should the bubble adapt its width for very short bodies —
   e.g. tint only `min(2 + lipgloss.Width(content) + 2,
   transcriptInnerW)` cells instead of the full inner width — so a
   one-word `"yes"` reply does not read as a lopsided banner? The
   source slice suggests prototyping. This slice ships the full-width
   variant because it matches omp and slice 03's stable-column
   discipline. Revisit if terminal captures at ≥100 columns look
   unbalanced; the change is a single conditional inside
   `renderUserMessageBubble` and does not affect the primitive's
   contract.
3. **Q-09.3** Should system/command output also acquire a
   distinguishing fill? omp tints custom messages
   (`customMessageBg`). Deferred — the verbatim path is already
   visually distinct and adding a second background here would
   crowd the color budget.
4. **Q-09.4** Is `chatUserBubbleCollapseBytes = 4096` the right initial
   value? It matches `chatExpandMax` deliberately, but the two
   constants describe different phenomena (elision vs first-paint
   deferral) and could diverge if a future measurement shows glamour
   layout latency crosses 100 ms at a different byte count. Revisit
   with a `BenchmarkUserBubbleFirstPaint` measurement if operator
   feedback flags a specific message that felt slow.
5. **Q-09.5** Should the summary row derive its label from something
   richer than the first markdown heading — e.g. the block's
   `activity.Title` or a fenced-`skill://` prefix? Deferred. jig's
   transcript today attaches no metadata to `BlockText`; a metadata
   channel would be a wire-format change. If future work
   (context-assembly, injection) adds a labeled origin, the summary
   label rule becomes: `origin_label`, else first heading, else
   `"Message"`. The rule is a one-line change in
   `renderUserMessageBubble`.
