# 25-spec-status-line-header-grammar.md

## Introduction/Overview

Give every Monitor tool-exchange header a shared, four-slot grammar
(`icon`, `title`, `description`, `meta`) built by one pure function in
`internal/tui/shared`, and carry execution state through the icon and the
card border instead of appended prose. Slice 01 already frames the header
inside a state-colored card; this slice turns the row inside that frame from
one styled string ("`◈ Read: config.toml · running`") into an intentional
composition (`icon` colored by state, `title` in `ToolTitle`, `description`
in `ToolDescription`, error hint moved to `meta`), so later slices (05 tool
detail sections, 07 diff badge, 08 grouped reads, 13 spinners, 15 inline
args) can plug into named slots instead of manipulating a string.

Source: [OMP transcript parity, slice 02](../../epics/omp-transcript-parity/slices/02-status-line-header-grammar.md).
Dependency: slice 01, delivered by
[docs/specs/25-spec-transcript-card-primitive](../25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md),
provides the card frame, the `Card.Header` slot, and header-only geometry.
This specification is Phase 1 output; it does not authorize implementing
later epic slices.

## Goals

- Replace `" failed"`, `" · running"`, and `" · incomplete"` prose in the
  header with a state-driven icon and border style, so tool state is a
  glyph/color change rather than a substring.
- Compose every tool-exchange header with one shared builder so slices 05,
  07, 08, 13, and 15 write into named slots rather than editing a fused
  string.
- Split `summarizeActivity`'s pre-fused `label`/`preview` at the render
  boundary so `title`, `description`, and `meta` receive their own styles.
- Add a per-tool signature glyph on settled successful exchanges, distinct
  from the generic running/pending glyph, without introducing per-frame
  glyph churn on state transitions (epic CC-4).
- Keep the error hint visible at collapse without occupying `title`.

## User Stories

- **As an operator scanning a long transcript**, I want tool titles and
  their arguments in visibly different styles so I can locate a failed
  `read` on `main.go` without reading every row.
- **As an operator watching a run in progress**, I want the running row's
  icon and border to change to primary — not its title to gain "` · running`"
  — so a running row stays the same width and does not flicker as it
  finishes.
- **As a slice 05/07/08/13/15 implementer**, I want a named header slot to
  write into (a detail body, a diff badge, a grouped-read tree, a spinner
  frame, an inline argument preview) so I do not have to reverse-engineer
  the current row's substring format.

## Demoable Units of Work

### Unit 1: Shared status-line grammar, status-icon resolver, and tool styles

**Purpose:** Demonstrate the pure-presentation builder with a synthetic
gallery covering every slot combination, state icon, and per-tool signature
glyph. This unit produces the primitive slice 02 owns; Unit 2 wires the
Monitor to it.

**Functional Requirements:**

- **FR-02.1:** The system shall provide `shared.RenderStatusLine(StatusLine)
  string` where `StatusLine` exposes `Icon`, `IconStyle`, `Title`,
  `TitleStyle`, `Description`, `DescriptionStyle`, `Badge`, `BadgeStyle`,
  and `Meta []string` (with an optional `MetaStyle`) fields. Empty values
  shall drop their slot without introducing separator artifacts.
- **FR-02.2:** The system shall introduce `Description` with `": "` and the
  `Meta` list with a single leading space. Meta entries shall join with
  `" · "` (the shared dot separator). Only entries that are non-empty after
  trimming surrounding whitespace shall participate in the join, so no row
  ends in a dangling separator.
- **FR-02.3:** The system shall replace every CR and LF in every slot with
  a space so a header is always exactly one visible row, and shall preserve
  caller-applied SGR styling and grapheme boundaries. Tabs remain the
  caller's responsibility per the epic slice.
- **FR-02.4:** The system shall not perform width truncation; it shall
  return an unbounded string, leaving clipping to the card frame's
  `TruncateTitle` (slice 01). `RenderStatusLine` shall document that
  boundary in its Go doc comment.
- **FR-02.5:** The system shall expose a status-icon resolver
  `shared.ToolStatusIcon(state, kind) (glyph string, style lipgloss.Style)`
  that maps `toolDisplayState`-equivalent values plus an optional tool kind
  to a `(glyph, style)` pair. On a settled success with a known tool kind
  the resolver shall return the kind's signature glyph; otherwise it shall
  return the generic state glyph. Unknown states shall resolve to the
  pending glyph and pending style, never to a success glyph.
- **FR-02.6:** The system shall centralize new glyphs in
  `internal/tui/shared/icons.go` under stable names (no bare glyph
  constants at call sites) so slice 14's preset table can substitute them
  in one place (epic CC-7).
- **FR-02.7:** The system shall extend `shared/icons.go` with:
  - Per-state header icons (`IconStatusSuccess`, `IconStatusError`,
    `IconStatusRunning`, `IconStatusPending`, `IconStatusWarning`).
  - Per-tool signature glyphs (`IconToolRead`, `IconToolEdit`,
    `IconToolWrite`, `IconToolSearch`, `IconToolShell`, `IconToolWeb`,
    `IconToolAgent`, `IconToolAsk`, `IconToolTodo`).
  - Ordinary Unicode glyphs from the existing jig family (`◈`, `⌕`, `$`,
    `↗`, `⊙`, `?`, `●`, `○`, `✓`, `✗`, `!`), avoiding emoji and
    double-width runes; alternatives for slice 14's ASCII preset remain
    slice 14's responsibility.
- **FR-02.8:** The system shall split the read/edit/write signature icon:
  `IconToolRead` remains `◈`; `IconToolEdit` and `IconToolWrite` shall be
  distinct geometric marks (proposed `✎` for edit and `✏` avoided because
  it is emoji-presentation on many terminals; use `⨯`-free geometric
  substitutes documented in Design Considerations). Neither shall reuse a
  glyph already in the epic epic set with a different meaning.
- **FR-02.9:** The system shall add `Styles.Chat.ToolTitle`,
  `.ToolDescription`, `.ToolMeta`, and `.ToolBadge` fields to `Styles` in
  `internal/tui/shared/styles.go`, initialized in `DefaultTheme()` from
  existing tokens: `ToolTitle` from `fgBase`, `ToolDescription` from
  `fgMuted`, `ToolMeta` from `fgDim`, and `ToolBadge` from
  `Styles.Badge.Neutral` (bold `onPrimary` on `bgLess`). Per-state icon
  styles for the resolver shall reuse `Card.Border*` foregrounds so the
  header icon and card border read as one indicator.
- **FR-02.10:** The system shall keep every added style in `Styles` and
  every added palette hex string in `palette.go`; no package-level
  `lipgloss.NewStyle()` or inline hex may appear at renderer call sites
  (repository standard from CLAUDE.md).

**Proof Artifacts:**

- Test: table-driven `RenderStatusLine` cases in
  `internal/tui/shared/status_line_test.go` covering icon
  present/absent × description present/absent × badge present/absent × meta
  0/1/N entries, empty-string meta filtering, embedded `\n`/`\r` in every
  slot, preserved SGR styling in every slot, and independent style
  application (measured by asserting each slot's rendered substring).
- Test: `ToolStatusIcon` mapping table asserting (state, kind) → (glyph,
  style-foreground) with explicit cases for every state (success, error,
  running, pending, warning), every documented tool kind, an unknown kind
  (falls back to generic), an unknown state (falls back to pending), and
  proof that the running/pending icon is identical (CC-4 anti-jitter).
- Test: a regression that no shared style contains a hardcoded hex string
  outside `palette.go` for the new `Tool*` fields, and that
  `shared/icons.go` is the sole owner of the new glyph constants (grep-
  based test similar to slice 06's vocabulary check pattern).
- Terminal capture:
  `docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-1-status-line-gallery.txt`
  recording a synthetic gallery of every state × kind × slot combination
  rendered at 40/60/90 columns with per-row `lipgloss.Width` measurements,
  matching the slice-01 gallery style.

### Unit 2: Monitor tool-exchange header uses `RenderStatusLine`

**Purpose:** Show the real render path adopting the shared grammar, prove
state prose is gone, and prove the error hint has moved into the meta slot
without regressing existing behavior. Detail-body relocation (slice 05),
diff badge (slice 07), grouped tree (slice 08), spinner frame (slice 13),
and inline args (slice 15) remain out of scope.

**Functional Requirements:**

- **FR-02.11:** `internal/tui/monitor/monitor_transcript_items_view.go`
  shall compose the tool-exchange header exclusively through
  `RenderStatusLine`. The current inline row construction
  (`row := marker + " " + label` + preview + hint concatenation at
  `items_view.go:76-96`) shall be removed.
- **FR-02.12:** The system shall carry execution state through the icon
  and its color plus the card border style, and shall not append
  `" failed"`, `" · running"`, or `" · incomplete"` to the title, meta, or
  any header slot. A test shall grep-assert that no rendered tool-exchange
  header row (with ANSI stripped) contains those literal substrings.
- **FR-02.13:** `summarizeActivity` shall continue to populate `icon`,
  `action`, and `detail` and shall stop pre-composing `label` and
  `preview`. The `label` and `preview` fields on `toolCallSummary` shall be
  removed (or, at the caller's option, kept and ignored by the renderer,
  provided the renderer never reads them). `summarizeToolCall` callers
  outside the Monitor renderer shall not depend on the fused fields.
- **FR-02.14:** The Monitor renderer shall map `toolCallSummary` fields
  into `StatusLine.Title = action`, `StatusLine.Description = detail`, and
  supply per-state icon/style via `ToolStatusIcon(displayState, kind)`.
  The expansion marker (`▸`/`▾`/`" "`) shall be prepended once outside the
  `RenderStatusLine` output (as it is today) so it does not participate in
  the composed grammar.
- **FR-02.15:** On a settled successful exchange with a known tool kind,
  the icon shall be the kind's signature glyph. Pending and running states
  shall render the same icon (a single running/pending glyph) so a row
  does not twitch as it settles (CC-4). The border color remains the sole
  cue distinguishing running from settled work.
- **FR-02.16:** An unmatched tool result (`item.toolUse == nil`) shall
  render with an explanatory title (`Result (unknown origin)`) via the
  same `RenderStatusLine` composition, using the warning state icon and
  the warning border style. This kind is not a `transcriptItemToolExchange`
  in slice 01; the flat path remains untouched but shall migrate to
  `RenderStatusLine` for a consistent header grammar. Should the flat
  orphan path prove too invasive within this slice, that migration is
  deferred and recorded as an explicit limitation; the exchange path
  migration is not optional.
- **FR-02.17:** `toolErrorHint` shall move from the appended-label
  position (`items_view.go:94-96`) into the `StatusLine.Meta` slot when
  the display state is `toolDisplayError`, so it is subject to the same
  meta styling and the card frame's truncation. `toolErrorHint` itself
  shall keep its current sanitization contract; the change is where its
  output goes, not what it produces.
- **FR-02.18:** `TranscriptActivity`, `TranscriptError`, and
  `TranscriptSelected` shall no longer wrap the entire row. Instead, the
  header shall compose per-slot styles: `TranscriptSelected` shall style
  only the `Title` when the row is selected (an emphasis boost, layered
  on top of `ToolTitle`); state color shall reach the row through the
  icon style and the card border only. The row-level `TranscriptError`
  style (currently applied to the whole label) shall be retired for
  exchanges; the border and icon suffice for state.
- **FR-02.19:** The line-range and cache identity contracts from slice 01
  shall continue to hold. `transcriptRenderKey.header` shall contain the
  final composed header string, so a change in any slot (state, icon, or
  meta) invalidates the cached card. No additional cache surface is
  introduced by this slice.
- **FR-02.20:** With persistence off (`RunDir == ""`), tool-exchange
  rendering shall retain the existing empty-state behavior; no
  `RenderStatusLine` call shall force filesystem or backend work.

**Proof Artifacts:**

- Test: table-driven Monitor cases (`monitor_transcript_items_view_test.go`
  and, where useful, `monitor_transcript_card_test.go`) that assert:
  - Header rows for success/error/running/incomplete states never contain
    `" failed"`, `" · running"`, or `" · incomplete"` (ANSI stripped).
  - The status-icon glyph observed for success/error/running/incomplete
    matches `ToolStatusIcon`'s table for the corresponding state.
  - On a settled successful `edit` exchange, the header icon is the edit
    signature glyph (not the generic tool glyph); on a running or pending
    exchange, the icon is the generic running/pending glyph (CC-4).
  - Selection style is applied only to the title, not to the full row.
  - Error hint text is present in the header row via meta and absent from
    the title.
- Test: cache invalidation regression demonstrating that a state
  transition from running → success (via `setChatPage`) produces a new
  cached header (different composed `header` key) whose icon differs
  (per-state icon), while a same-state re-render reuses the cached card.
- Test: orphan `Result (unknown origin)` renders through
  `RenderStatusLine` with the warning icon and warning border style, and
  no other transcript item kind (text, system, thinking, unsupported)
  gains a status-line composition (its existing rendering is unchanged).
  Or, when FR-02.16's migration is deferred, an explicit test locking the
  current orphan flat rendering with a documented limitation note in the
  proof directory.
- Terminal capture:
  `docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-2-monitor-headers.txt`
  and a companion PNG capturing the Monitor at a realistic width
  (matching slice-01's ~100-column primary review size) with adjacent
  success/error/running/incomplete headers; the notes record terminal
  size, `transcriptInnerW`, selection index, and observed state colors.

## Non-Goals (Out of Scope)

1. Diff badge (`⟦+12/-3⟧`) content — slice 07 supplies the numbers. This
   slice ships the `Badge` slot empty but wired.
2. Spinner frames — slice 13 supplies the ticker. This slice ships the
   `Icon` slot accepting a caller-supplied pre-rendered glyph.
3. Inline argument previews — slice 15 owns fair-share formatting.
4. Steps panel indicator changes; the epic's CC-4 anti-jitter rule applies
   to the Transcript only (NG6).
5. Detail-body sections (moving `toolErrorHint` into a body line, moving
   `writeItemDetail` output into card sections). Slice 05 owns that;
   `toolErrorHint` moves to meta within the header, not into a body row.
6. Grouped/collapsed reads. Slice 08 owns them; this slice renders one
   exchange per header.
7. Truncation vocabulary changes. Slice 06 owns "`… N more <items>`" wording.
8. New glyph presets (`ascii` fallback). Slice 14 owns the preset table;
   this slice adds unicode glyphs through `shared/icons.go` in a shape a
   preset can substitute.
9. Changing the transcript wire format, `toolcall.Activity` shape, harness,
   backend selection, or persistence contracts (epic NG5, CC-12).
10. Reworking the `summarizeActivity` heuristics (kind detection, argument
    extraction, sanitizer). This slice consumes the existing fields; it
    does not re-key or re-shape them.

## Design Considerations

### Slot grammar

```
<icon> <Title>: <Description> ⟦Badge⟧ <Meta · Meta · Meta>
  │       │           │            │        └─ dim, joined by " · "
  │       │           │            └─ bracketed, empty in this slice
  │       │           └─ muted, introduced by ": "
  │       └─ ToolTitle (plain foreground), bold on selection
  └─ status glyph, colored by state
```

Separators are asymmetric on purpose: `": "` binds the description to the
title; `" "` detaches the meta list. `Meta` entries join with `" · "`
(single space each side). This is omp's shape at
`packages/coding-agent/src/tui/status-line.ts:32-54`; the spec adopts it
verbatim.

### Per-slot styling defaults

| Slot | Style | Token derivation |
|---|---|---|
| Icon | `ToolStatusIcon` returns a style per state | `Card.Border*` foreground reused |
| Title | `Chat.ToolTitle` (plain foreground) | `fgBase` |
| Title, selected | `Chat.TranscriptSelected` layered on top | `Bold` + `primary` (existing) |
| Description | `Chat.ToolDescription` (muted) | `fgMuted` |
| Meta | `Chat.ToolMeta` (dim) | `fgDim` |
| Badge | `Chat.ToolBadge` (neutral pill) | reuses `Badge.Neutral` |

Open question Q-02.1 (accent vs. plain title) resolves to **plain
foreground for the title, with running rows expressing "running" through
the icon color and the card border only.** Rationale: at densities of
40–80 exchange rows in a run, a colored title on every row makes the
transcript loud; the icon and border already carry state, and
`TranscriptSelected` still emphasises the *current* row.

### Per-tool signature glyphs

Open question Q-02.2 resolves to **geometric ASCII-safe glyphs, not
emoji.** The set below extends the existing jig icon family so slice 14's
preset table has one place to substitute. Wide (emoji-presentation)
glyphs are avoided because they change column width across terminals and
would break the card frame width invariant proven in slice 01.

| Kind | Signature glyph | Rationale |
|---|---|---|
| read | `◈` | Existing jig glyph, preserved. |
| edit | `✎` | Pencil, geometric, single-width. |
| write | `✍` | Writing hand, geometric, single-width. If the terminal renders this double-width in practice, fall back to `✎` for both edit and write in this slice and record the constraint as a slice-14 input. |
| glob / find | `⌕` | Existing jig glyph, preserved. |
| grep / search | `⌕` | Same as glob; the description slot distinguishes them. |
| bash / shell | `$` | Existing jig glyph, preserved. |
| websearch | `↗` | Existing jig glyph, preserved. |
| webfetch | `↗` | Existing jig glyph, preserved. |
| task / agent | `⊙` | Existing jig glyph, preserved. |
| todo* | `⊙` | Existing jig glyph, preserved. |
| ask | `?` | Existing jig glyph, preserved. |
| unknown | `▸` | Existing `IconToolCall`, generic fallback. |

Column width for each candidate shall be verified with `lipgloss.Width` in
a dedicated width-audit test; any glyph that measures greater than one
cell in the pinned Charm libraries is rejected in favor of the generic
`▸` and recorded in the proof file. This is the mechanical check that
protects the slice-01 width invariant.

### Error hint placement

Open question Q-02.3 resolves to **`Meta`, not `Badge`, and not a body
line.** Rationale:

- A body line would push detail relocation into this slice (slice 05's
  scope).
- The badge slot is reserved for structured summaries such as diff stats
  (slice 07). An error-hint string is prose, not a summary.
- Meta is the correct home: it is dim, it is single-line, and the card
  frame truncates it exactly like other meta entries.

Slice 05 remains free to move the hint into a body line later; this slice
leaves the hint reachable in the current row without appending it to the
title.

### CC-4 anti-jitter rule

Running and pending exchanges shall use the same icon (a single
generic/pending glyph); the settled success case is the only glyph
transition, and it happens once. Errors flip both the icon and the border
color simultaneously on settling, which is not a per-frame transition.
This matches omp's stability guarantee at
`task/render.ts:963-979`.

### Backward compatibility of header identity

The composed header string ends up in `transcriptRenderKey.header` (slice
01). Any change to the slot grammar therefore invalidates cached cards on
resize, replacement, and re-selection exactly as it does today; no new
cache surface is required. `chatItemLineRanges` continues to count the
two header rows the card emits.

### Migration of the orphan `Result (unknown origin)` path

FR-02.16 requires the orphan path to reuse `RenderStatusLine`. That path
is not a card (slice 01 keeps orphans flat). The migration is a small
refactor: replace the ad-hoc `label = shared.IconToolResult + " Result
(unknown origin)"` at `items_view.go:81` with a `RenderStatusLine` call
that supplies a warning icon and the same title. If this proves invasive
enough to grow the diff beyond the exchange path, the migration is
deferred with a recorded limitation and the exchange path migration
still ships; deferring both is not acceptable.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md), [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), and [Testing](../../TESTING.md).
- Add every new style as a field of `Styles` initialized in
  `DefaultTheme()` from tokens in `internal/tui/shared/palette.go`. Do not
  introduce package-level `lipgloss.NewStyle()` variables in renderer
  files, do not pass styles as parameters, and do not hardcode hex
  strings outside `palette.go`.
- Route every new glyph through `internal/tui/shared/icons.go`. Renderer
  code must reference constants (`shared.IconStatusRunning`,
  `shared.IconToolEdit`, etc.), never bare Unicode runes.
- Keep pure presentation in `internal/tui/shared`; the shared package
  must not import `internal/tui/monitor`. The Monitor package consumes
  `shared` and owns Monitor-specific composition.
- Measure widths with `lipgloss.Width` and use the pinned
  `github.com/charmbracelet/x/ansi` truncation/wrapping helpers. Do not
  count bytes or runes to reason about visible cells.
- Preserve persistence-off behavior (`RunDir == ""`): loading an empty
  page must produce the existing empty state and must not invoke
  `RenderStatusLine`. Tests that inject synthetic pages may still exercise
  it directly.
- Table-driven synthetic tests only. Never capture real `.jig/` data in
  proofs.
- Follow [ADR 0001](../../adr/0001-manual-border-title-compositing.md):
  slice 01's shared border compositor still owns the frame. This slice
  does not build a second border.

## Technical Considerations

### Shared API surface

Add `internal/tui/shared/status_line.go` with:

```go
type StatusLine struct {
    Icon             string
    IconStyle        lipgloss.Style
    Title            string
    TitleStyle       lipgloss.Style
    Description      string
    DescriptionStyle lipgloss.Style
    Badge            string
    BadgeStyle       lipgloss.Style
    Meta             []string
    MetaStyle        lipgloss.Style
}

func RenderStatusLine(s StatusLine) string
```

`RenderStatusLine` is pure: it renders the exact composition its input
describes and never applies width limits. Callers (the card frame; slice
14 later) own truncation. Empty caller styles fall back to the
theme-level tool styles: `TitleStyle` defaults to
`Theme.Chat.ToolTitle`, `DescriptionStyle` to `Theme.Chat.ToolDescription`,
`MetaStyle` to `Theme.Chat.ToolMeta`. `IconStyle` and `BadgeStyle` have no
theme default (the resolver and the caller supply them) so an unstyled
icon renders in the terminal's default foreground rather than accidentally
picking up muted or dim.

The status-icon resolver lives beside the shared vocabulary:

```go
type ToolStatusInput struct {
    State toolDisplayState  // declared in monitor; the shared function
                            //   accepts an int-backed enum via type
                            //   parameter or a shared re-export
    Kind  string             // canonicalized tool kind, e.g. "read"
}

func ToolStatusIcon(state int, kind string) (glyph string, style lipgloss.Style)
```

Because `toolDisplayState` today lives in `internal/tui/monitor`, the
shared package must not import it. Two acceptable resolutions, in
preference order:

1. **Move `toolDisplayState` to `internal/tui/shared`** as
   `shared.ToolDisplayState`, re-export from monitor for existing
   callers, and let the resolver consume the shared type directly. The
   type is presentation-derived and has no reason to be Monitor-local.
2. **Introduce a small shared enum**
   (`shared.StatusPending`/`Running`/`Success`/`Warning`/`Error`) and
   translate at the Monitor boundary.

Prefer option 1 because it avoids a permanent parallel enum and matches
the shared-owns-presentation direction the codebase is already moving
(slice 01 moved `CardState` into shared). Record the actual choice in the
task file's Standards Evidence Table.

### `summarizeActivity` seam

`toolCallSummary` already exposes `icon`, `action`, and `detail`
(`monitor_tool_summary.go:16-19`). The `label` and `preview` fields are
derived (`toolSummary` at `:90-98`) and are consumed by exactly one call
site (`items_view.go:79-93`). Remove or ignore them at the renderer; keep
`icon`/`action`/`detail` as the exposed contract. `summarizeToolCall`
retains its signature.

### Monitor renderer changes

`itemTranscriptBody` at `items_view.go:76-96` currently produces
`row = marker + " " + label + preview` and appends `" failed"`,
`" · running"`, `" · incomplete"`, or a preview-suffixed error hint before
handing the row to `renderToolExchangeCard`. Replace that block with:

1. Resolve `(glyph, iconStyle)` via `ToolStatusIcon(displayState, kind)`.
2. Compose `StatusLine{Icon: glyph, IconStyle: iconStyle,
   Title: summary.action, Description: summary.detail}`.
3. For `toolDisplayError`, append the sanitized `toolErrorHint(m, item)`
   to `Meta`.
4. Style the title through `TranscriptSelected` when selected;
   otherwise the theme default takes over.
5. Feed the composed row to `renderToolExchangeCard`; the marker prefix
   is unchanged.

The signature of `renderToolExchangeCard` does not change; its `header`
argument is the `RenderStatusLine` output.

### Cache identity

`transcriptRenderKey` already includes `expanded`, `selected`, `state`,
and `header`. The composed `header` string is the sole identity carrier
for slot-level changes (icon glyph, meta content, badge presence). No new
cache field is required. Slice 01's tests (`TestTranscriptCardCache…`)
already assert that a changed `header` refreshes the cache; extend those
tests for the new prose-free composition.

### Current technology research

Primary documentation and source were consulted on 2026-09-11 against the
`go.mod` pins (Charm v2, Lip Gloss 2.0.5, Glamour 2.0.1, ANSI 0.11.7). No
dependency upgrade is required.

- [Lip Gloss API](https://pkg.go.dev/charm.land/lipgloss/v2): `.Render`
  composes styles left-to-right; nested styles are additive. Compose slot
  styles by rendering each slot independently and joining with plain
  separators; do not wrap the joined line in a further style (that would
  bleed one slot's foreground into another).
- [Charm ANSI API](https://pkg.go.dev/github.com/charmbracelet/x/ansi):
  `ansi.Strip` for the "no state prose" grep assertions in tests;
  `lipgloss.Width` for slot and row measurement.
- [OMP status-line source](https://raw.githubusercontent.com/can1357/oh-my-pi/main/packages/coding-agent/src/tui/status-line.ts):
  reference shape; line numbers may drift. Adopt the grammar, not
  additional omp signature glyphs beyond what jig's vocabulary permits.

### Sanitization contract

`sanitizeToolSummary` already runs on `icon`/`action`/`detail`
(`monitor_tool_summary.go:87-89`). Extend the shared composition with the
same handling by mapping every `\x1b` byte in each slot's *input* to a
space (or ignore it) before rendering. Do **not** strip ANSI from
`Meta` because meta entries are trusted (they originate from
`toolErrorHint` and its sanitizer) — but do apply the newline flattening
per FR-02.3 so a meta entry cannot break the row height.

## Security Considerations

Tool descriptions and meta strings are derived from agent-controlled
inputs. Two concerns remain from the epic slice:

- **Control-character / ANSI injection.** `sanitizeToolSummary` strips
  control characters today. `RenderStatusLine` must add newline
  flattening (FR-02.3) and, where a slot is not already sanitized (`Meta`
  when sourced outside `toolErrorHint`), the caller sanitizes at the
  Monitor boundary. Tests must include an input carrying an embedded
  `\x1b[31m` sequence and assert the composed row's `lipgloss.Width`
  equals the sum of its measured slot widths plus separator cells (no
  hidden second row, no smuggled color).
- **Path disclosure.** File paths already flow through
  `shortFile`/`shortHost`. This slice does not add or remove path
  disclosure.

Presentation-only work introduces no credentials or external writes.
Proofs must use fabricated activity content, never captured operator
transcripts or `.env` values.

## Success Metrics

1. `RenderStatusLine` and `ToolStatusIcon` pass every table-driven case
   in `status_line_test.go`, including slot filtering, state icon
   mapping, running/pending glyph equality (CC-4), and grapheme/ANSI
   preservation.
2. Grep-based Monitor tests confirm no rendered header row (ANSI
   stripped) contains `" failed"`, `" · running"`, or `" · incomplete"`.
3. A settled `edit` exchange renders with the edit signature glyph; a
   pending/running exchange renders with the generic pending/running
   glyph. Colors change; the glyph does not (except at settling).
4. Slice 01's card width, cache, and line-range assertions still pass
   with the new header content. `go build ./cmd/jig`, `go test ./...`,
   `go vet ./...`, `gofmt -l <changed>`, and `git diff --check` are
   clean.
5. Terminal captures under `25-proofs/` demonstrate visible weight
   differences between title, description, and meta at 40/60/90 columns.

## Open Questions

None blocking. The following are explicit assumptions for review:

1. `toolDisplayState` migrates to `internal/tui/shared` as
   `shared.ToolDisplayState`. If a reviewer prefers a parallel shared
   enum with a translation at the Monitor boundary, the task file's
   design section records that as an alternative and the implementation
   uses the alternative; either resolution keeps the shared package free
   of a Monitor import.
2. The write signature glyph (`✍`) is retained only if `lipgloss.Width`
   measures it at one cell in the pinned Charm libraries; otherwise
   write and edit share `✎` for this slice and slice 14 reintroduces
   width variants via its preset table.
3. The error hint lives in `Meta`. Slice 05 may later relocate it into a
   body row; this slice does not preempt that decision.
