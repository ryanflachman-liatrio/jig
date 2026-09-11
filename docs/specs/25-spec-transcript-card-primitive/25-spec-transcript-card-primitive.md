# 25-spec-transcript-card-primitive.md

## Introduction/Overview

Add a reusable, state-colored transcript card to `internal/tui/shared` and
use it for the Monitor's tool-exchange header. A rounded frame with a quiet
success border and prominent running/error borders will make tool activity
easier to scan while establishing the presentation primitive needed by later
OMP-parity slices.

Source: [OMP transcript parity, slice 01](../../epics/omp-transcript-parity/slices/01-transcript-card-primitive.md).
Dependency: slice 00, committed as `1ab9ba9`, has removed the old render-plan
path. This specification is Phase 1 output; it does not authorize implementing
the other epic slices.

## Goals

- Render cards with exact terminal-cell widths, including at 40 columns.
- Keep successful work visually quiet; reserve primary borders for pending or
  running work and danger borders for errors.
- Preserve caller-styled labels and content through wrapping and tinting.
- Establish section and background behavior with synthetic component evidence
  before later slices move tool details into cards.
- Convert one item kind without changing transcript identity, paging, search,
  clipboard payloads, or expansion semantics.

## User Stories

- **As an operator reading a run**, I want failed and running tool exchanges to
  stand out so that I can locate activity requiring attention quickly.
- **As an operator using a narrow terminal**, I want long labels to shrink and
  card content to wrap so that borders remain aligned and content stays readable.
- **As a maintainer adding a transcript renderer**, I want one shared card
  primitive so that future details and diffs use consistent borders and spacing.

## Demoable Units of Work

### Unit 1: Shared card geometry and stable state presentation

**Purpose:** Demonstrate the complete reusable component using a synthetic
gallery with labeled headers, multiple sections, long content, and code output.

**Functional Requirements:**

- **FR-01.1:** The system shall render every card row at exactly the requested
  `Width` visible cells, measured with `lipgloss.Width`, for supported widths
  of 40 and above. The same rule shall hold for defensive narrow rendering
  described under Design Considerations.
- **FR-01.2:** The system shall place a nonempty header inside the top border
  following `╭─── `, with one space after the label and horizontal fill flush
  against `╮`. Only the border glyphs shall receive the border foreground;
  the header and optional metadata shall retain their caller-supplied styling.
  Nonempty header and metadata shall be joined with ` · `; either may appear
  alone without a leading or trailing separator.
- **FR-01.3:** The system shall use danger for error borders, warning for
  warning borders, primary for running/pending borders, and dim for success
  borders. `BorderMuted` shall explicitly override the border with the shared
  muted border style without changing the state-derived tint.
- **FR-01.4:** The system shall draw labeled section dividers using `├─── `,
  the styled label, one space, continuous horizontal fill, and `┤`. A labeled
  first section shall have a divider. An unlabeled section shall have a divider
  only when `Rule` is true and its index is greater than zero. Unlabeled top,
  divider, and bottom bars shall have no internal space gaps.
- **FR-01.5:** The system shall render headers and divider labels on one row,
  normalize embedded line breaks/tabs into spaces, and shorten overlong labels
  through `TruncateTitle` with an ellipsis. Truncation shall preserve valid ANSI
  sequences and grapheme boundaries, including wide characters, combining
  marks, and emoji. Zero title budget shall produce no label.
- **FR-01.6:** The system shall split embedded newlines in section lines and
  wrap content to `CardContentWidth` using ANSI-aware wrapping, including hard
  wrapping of long unbroken words. Content shall not be truncated by the card;
  existing upstream display budgets remain the caller's responsibility. Strip
  trailing whitespace before wrapping, preserve internal blank lines and
  leading indentation, and preserve content foreground styling across rows.
- **FR-01.7:** When `Tint` is true, the system shall apply the neutral recessed
  background to pending/running/success/warning cards and the error background
  to error cards. The base tint shall cover borders, padding, blank rows, and
  fill cells. Full/default-background SGR resets inside content shall restore
  the card tint before subsequent visible cells. Intentional inner background
  colors may remain until reset; no reset may expose the terminal background
  inside a row. Tint shall end at the row boundary and shall not leak into
  neighboring content. `Tint: false` shall add no card background.
- **FR-01.8:** The system shall preserve rounded frames at width 40, shortening
  labels instead of overflowing or switching to a flat-row presentation.
- **FR-01.10:** The system shall share a single border-bar compositor with
  `PanelTopEdge`, parameterized internally for cap length and corner/tee
  glyphs. Existing panel callers shall retain their one-dash cap, title style,
  dimensions, and layout. Cards shall use a three-dash cap.
- **FR-01.11:** The public padding contract shall distinguish omitted padding
  from explicit zero: omitted left padding means one cell, omitted right
  padding inherits left, and explicit zero means no padding on that side.
  `CardContentWidth` and `RenderCard` shall resolve padding identically.
- **FR-01.12:** The system shall expose the five card states and the eight
  shared styles listed under Technical Considerations. Invalid state values
  shall use pending presentation rather than success presentation.

**Proof Artifacts:**

- Shared-package test output demonstrating exact width at 40/60/90, asymmetric
  and zero padding, empty content, styled long labels, Unicode, explicit
  newlines, unbroken words, cap spacing, and all divider cases.
- A synthetic card gallery with each state and success/error comparisons,
  captured with its terminal dimensions. Include sections containing both
  plain text and a Glamour code fence rendered with jig's Chroma formatter.
- A background-state assertion over rendered visible cells, demonstrating
  that full resets (`CSI m`, `CSI 0 m`) and background resets (`CSI 49 m`),
  including combined SGR parameters, leave no untinted gaps. Include a
  sentinel after the card to prove tint does not leak. A mere search for a
  background escape somewhere in each line is insufficient evidence.
- Existing panel/title behavior tests plus relevant new styled-title cases
  demonstrating that compositor reuse does not change ordinary panels.

### Unit 2: Tool-exchange header cards in the Monitor

**Purpose:** Show real Monitor integration while leaving detail-section
conversion to slice 05.

**Functional Requirements:**

- **FR-01.13:** The system shall use `RenderCard` for exactly
  `transcriptItemToolExchange`, including use-only exchanges represented by
  that kind. Orphan `transcriptItemToolResult`, text, system, thinking, and
  unsupported kinds shall retain their current presentation. The existing
  combined tool switch arm may be factored to share activity/label resolution,
  but shall not implicitly convert the orphan-result kind.
- **FR-01.14:** The header shall retain the current expansion marker, activity
  summary, preview, failure hint, and running/incomplete text. Selection shall
  retain its current cue and label emphasis, without overriding the
  state-derived border color. Header grammar changes belong to slice 02.
- **FR-01.15:** The integration shall map `toolDisplaySuccess` to `CardSuccess`,
  `toolDisplayError` to `CardError`, and `toolDisplayRunning` to `CardRunning`.
  Incomplete/unknown exchanges shall use `CardWarning` and retain the
  incomplete text. They shall never acquire a success border by default.
- **FR-01.16:** Tool-exchange headers shall use tinted, header-only cards with
  no sections. Such cards shall emit a top and bottom row with no synthetic
  empty body row. Expanded details shall still appear below the card through
  the existing detail writers, including the structured-edit new-code output
  and write-time truncation notice. Per-item and expand-all toggles shall
  preserve their current meaning.
- **FR-01.17:** Every emitted header-card row, including its existing outside
  selection/indent prefix, shall fit `transcriptInnerW`. Compute the available
  card width by subtracting the prefix's visible width once, and prefix every
  card row consistently. Do not count ANSI bytes as columns.
- **FR-01.18:** The item line range shall include the top and bottom of the
  header card and any expanded details, and shall exclude inter-item spacing.
  Cached and freshly rendered items shall have identical line ranges. Tests
  shall demonstrate navigation to successive cards, a tall expanded item,
  resize, and preservation of a manually selected item during a page refresh.
- **FR-01.19:** The system shall reuse `chatItemRendered` for header-card output
  with keys or invalidation covering every varying rendering input: item
  identity, width, effective expansion, selection, display state, and final
  styled header content. Page replacement, step changes, and resize shall not
  leave stale cards or an unbounded history of cached variants.
- **FR-01.9:** With persistence off (`RunDir == ""`), normal transcript loading
  shall continue to produce the existing empty state without invoking the
  card path or creating filesystem paths. Tests that explicitly inject
  synthetic pages may still exercise the renderer without persistence.
- **FR-01.20:** The system shall preserve bounded transcript loading,
  normalization, search/filter membership, clipboard contents, and existing
  detail limits. This change shall not add backend-specific rendering or
  filesystem/network work to rendering.

**Proof Artifacts:**

- Monitor behavioral tests demonstrating header cards for successful, failed,
  running, and incomplete tool exchanges; unchanged orphan-result rendering;
  unchanged expansion/details; and persistence-off behavior.
- Screenshot of the Monitor on a synthetic run containing adjacent failed and
  successful exchanges, with danger and dim borders visible. Record terminal
  size, `transcriptInnerW`, selection, and expansion state. Supplement with
  narrow and wide captures; a component schematic alone is not integration
  evidence.
- Tests showing that a same-key exchange changes from running to completed or
  failed after page replacement, changed header content invalidates cached
  output, and repeated resize/toggle operations keep cache storage bounded.
- A reproducible benchmark with a 300-entry synthetic page, reporting cached
  repeated-render time and allocations. Record hardware/toolchain and compare
  against the epic's 100 ms repaint budget; avoid a timing assertion in unit
  tests that would be sensitive to CI load.
- Passing `go build ./...`, `go test ./...`, `go vet ./...`, formatting checks,
  and `git diff --check`. These are implementation acceptance checks, not
  results claimed by this Phase 1 document.

## Non-Goals (Out of Scope)

1. Header-slot grammar, status glyph redesign, selection-rail redesign,
   grouping, vertical rhythm, truncation vocabulary, or glyph presets.
2. Moving detail writers into card sections, syntax-highlighting JSON tool
   details, replacing new-code cards, or implementing diffs. In particular,
   [slice 05](../../epics/omp-transcript-parity/slices/05-tool-detail-sections.md)
   explicitly expects slice 01 to frame the header while details remain below.
3. Changes to transcript storage, tool correlation, harnesses, backend
   configuration, Steps/Gate/review layouts, or clipboard evidence.
4. Light themes, terminal-background probing, new dependencies, and an
   alternative borderless presentation mode.
5. Broad cleanup of other leftovers from slice 00. Only the four unused
   `Theme.Chat.Bar*` style fields identified below are part of this slice.

## Design Considerations

Cards use rounded outer corners and sharp section junctions. The header's
fixed decoration consumes seven cells: two corners, three dashes, and two
spaces. Remaining cells hold the label and fill. A label that consumes its
entire budget may leave zero fill dashes before the closing corner.

Default body padding consumes two additional cells inside the two vertical
borders, so the normal content width is `Width - 4`. Empty sections contain
no rows unless they request a divider or supply an explicit blank line.
`RenderCard` returns newline-separated rows without a trailing newline; the
Monitor appends one after each emitted card row so its existing newline-based
line accounting remains valid.

The Phase 1 geometry probe rendered synthetic cards at widths 40, 60, and 90
inside a four-cell panel frame. Card rows were measured at the exact requested
width; usable body widths were 36, 56, and 86. The long path label shortened at
40 and the sample response wrapped into three, two, and one body rows,
respectively. This supports retaining rounded cards as the default. It is a
geometry check, not a claim that the final Monitor appearance has passed a
visual review. The implementation screenshot remains required.

Defensive behavior below 40 is part of geometry robustness, not another
product mode: keep the frame for widths of at least three, reduce padding as
needed to leave a content cell, shorten the cap when necessary, and omit labels
that cannot fit. Widths zero or negative return an empty string; widths one
and two return empty rows of that width rather than malformed borders or
content. The Monitor shall avoid rendering a card when no meaningful frame
width is available. The helper's normal formula is
`max(1, width - 2 - padLeft - padRight)`; reduced padding and ultra-small
behavior must be documented and tested consistently.

The existing selected prefix is wider than the unselected prefix. Preserve
that behavior for this slice and account for it in card width/cache identity;
the epic's stable selection gutter is slice 03. Header cards do not add frame
columns to the existing detail writers in this slice, so their wrap widths and
the new-code inset renderer do not need to change here.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md), [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), and [Testing](../../TESTING.md).
- Keep the primitive in `internal/tui/shared` and integration in
  `internal/tui/monitor`; shared must not import monitor.
- Respect [ADR 0001](../../adr/0001-manual-border-title-compositing.md): share
  the manual border compositor rather than splice labels into a rendered box.
- Define presentation styles through `Styles`/`DefaultTheme` and color hex
  values only in `palette.go`. No new package-level style variables or theme
  mutation from renderers.
- Use the pinned Go and Charm v2 dependencies. Handle input with
  `tea.KeyPressMsg`. Measure display cells, preserve persistence-off semantics,
  and bound caches by the loaded page and current presentation.
- Use table-driven synthetic cases. Never capture real `.jig/` data in proofs.
  Format changed Go files; do not rewrite unrelated files merely to run a
  formatting check.

## Technical Considerations

### Shared API and border ownership

Expose `CardState` with `CardPending`, `CardRunning`, `CardSuccess`,
`CardWarning`, and `CardError`; `CardSection` with `Label`, `Lines`, and `Rule`;
and `Card` with `Header`, `HeaderMeta`, `Sections`, `State`, `Width`, padding,
`Tint`, and `BorderMuted`. `RenderCard` is pure presentation and owns neither
viewports nor transcript data.

Resolve the source slice's optional-padding ambiguity using presence-aware
values: `PadLeft` and `PadRight` are `*int`, with nil representing omission.
Use the same pointer parameters for `CardContentWidth`. Explicit negatives
clamp to zero. This follows the repository's guidance to distinguish absence
from an explicit zero and permits flush gutters in future slices.

Generalize the existing compositor inside `panel.go` with caller-supplied
glyphs/cap length and an already styled label. `PanelTopEdge` retains its public
signature and applies `Theme.Panel.Title` before calling that compositor;
cards pass their own pre-styled labels. Improve `TruncateTitle` using the
pinned ANSI-aware truncation library: its current rune loop counts escape
bytes as visible characters when truncating a styled string. Tests must cover
both the new styled callers and existing panel/breadcrumb callers.

### Theme and CC-9 resolution

Add `Styles.Card` fields `BorderPending`, `BorderRunning`, `BorderSuccess`,
`BorderWarning`, `BorderError`, `BorderMuted`, `TintNeutral`, and `TintError`.
Use existing primary/dim/warning/danger colors; use the existing subdued panel
border color for `BorderMuted`. Add `hexToolNeutralBg = "#1A191F"` and
`hexToolErrorBg = "#2A1A1E"` in `palette.go`, explicitly commented as jig-local
additions rather than upstream Charmtone tokens.

**CC-9 decision: support tint with a final SGR stabilization pass.** On
2026-09-11, a temporary Go probe used the repository's pinned Lip Gloss
v2.0.5 and Glamour v2.0.1. It wrapped three synthetic reset cases and a Go
code fence in `.Background(...)`, then tracked active background state over
visible characters. Results:

| Input | Characters without background, naive wrapper | After reset stabilization |
|---|---:|---:|
| `before` + `CSI 0 m` + `after` | 5 | 0 |
| `before` + `CSI m` + `after` | 5 | 0 |
| `before` + `CSI 49 m` + `after` | 5 | 0 |
| Glamour-rendered Go code fence, seven output rows | 124 | 0 |

The probe restored the tint after each reset and closed with a default
background reset. This establishes feasibility; it is not a production
implementation. Implementation must test jig's own `CodeBlockFormatter` as
well as synthetic reset cases and combined SGR sequences. Use an ANSI-aware
pass or equivalent careful SGR handling: zeroes inside RGB color parameters
are not reset attributes. Preserve syntax foregrounds, emphasis, and explicit
inner backgrounds; only restore the base tint where a reset would clear it.
Apply stabilization after composition, wrapping, and padding, so border and
label resets are covered too. Do not adopt a plain background wrapper and
silently weaken FR-01.7; any failure to meet this proven approach requires
revisiting the spec's tint decision explicitly.

Q-01.4 is resolved mechanically: `BarThinking`, `BarToolCall`, `BarToolResult`,
and `BarError` occur only as declarations and assignments in `styles.go` after
slice 00. Remove those four fields and their obsolete comment/initialization
when adding the card styles. Other shared styles are outside this cleanup.

### Integration, caches, and line accounting

The production path is `setChatPage` → `buildTranscriptItems` →
`rebuildTranscriptItemState` → `itemTranscriptBody`. Keep its immutable,
page-local item identity. Tool exchange and orphan result currently share a
switch arm; preserve common summary preparation while applying the card only
to the exchange kind.

`transcriptRenderKey` currently contains only `itemKey`, `surface`, and
`width`; `chatItemRendered` is initialized/pruned but is not yet used by the
live item renderer. Add a card surface and the rendering-input identity
required by FR-01.19. The map's existence is not evidence of a working cache.
Since replacement pages can change a same-key tool's result/content, clear
card output on `setChatPage` as well as step changes. Clear affected item
renders on resize; the existing `rebuildRenderer` only resets `chatRendered`.
Keep at most the current card variant per loaded item, replacing earlier
width/selection/expansion variants, rather than accumulating them forever.

Only header-card output needs caching in this slice. Existing detail writers
remain below it, so their content and new-code rendering do not need to be
re-hosted. A header-only render can be retrieved before repeating border,
wrapping, and tint work. Keep line ranges outside the cache and derive them
from the actual emitted bytes on every render.

### Current technology research

Primary documentation and source were consulted on 2026-09-11. These are
living documents; the installed module versions listed above govern this
implementation, and no dependency upgrade is required.

- [Lip Gloss API](https://pkg.go.dev/charm.land/lipgloss/v2): use cell-based
  measurement and shared style construction. Inspection of pinned `style.go`
  and the runtime probe established that nesting a background does not itself
  restore it after content resets.
- [Charm ANSI API](https://pkg.go.dev/github.com/charmbracelet/x/ansi): use
  ANSI-aware wrapping and truncation rather than splitting raw styled strings
  by bytes or individual runes. Verify resulting row widths with Lip Gloss.
- [Glamour documentation](https://github.com/charmbracelet/glamour): configure
  a deterministic renderer with explicit styles and word-wrap width; leave
  terminal color downsampling to the existing application output layer.
- [OMP output-block source](https://raw.githubusercontent.com/can1357/oh-my-pi/main/packages/coding-agent/src/tui/output-block.ts):
  independently confirms reset stabilization and separation of border glyph
  styling from supplied labels. Its line numbers have shifted since the
  slice was written. Treat the local slice as the scope contract rather than
  importing additional current OMP capabilities such as inline graphics.

The local probe resolves the material styling risk; repository source and
slice 05 resolve the integration boundary. There is no outstanding technology
choice requiring a questions round.

## Security Considerations

Presentation-only work introduces no credentials or external writes. Treat
tool content as untrusted: retain existing transcript normalization, display
bounds, and control-handling contracts. Style-aware labels are generated by
trusted renderers, not a new channel for interpreting arbitrary terminal
control commands. Do not strip all ANSI from generated code highlighting to
solve tinting. Proofs must use fabricated paths and outputs, never captured
operator secrets or real run transcripts.

## Success Metrics

1. All card geometry and tint assertions pass, including width 40, styled
   labels, Unicode, reset-bearing code output, and no background leakage.
2. Monitor screenshots distinguish successful and failed tool exchanges by
   their dim/danger borders; running/incomplete labels remain truthful.
3. Expansion, line-range navigation, cache invalidation, and persistence-off
   tests pass without changing other item kinds or detail content.
4. Root build/tests/vet and whitespace/formatting checks pass, and the measured
   cached 300-entry render is below the 100 ms repaint budget on the recorded
   validation machine.

## Open Questions

No blocking questions remain. The following are explicit assumptions for
review, with fixed requirements for this slice:

1. The rounded nested frame remains the default at 40 columns and above.
   The geometry probe supports this; final visual review may motivate a later
   design change, but no borderless variant is planned here.
2. The integration converts the exchange kind only and frames its header only,
   following the slice's single-kind limit and slice 05's explicit staging.
3. Pointer padding fields are the concrete interpretation of the suggested
   API's distinction between default one-cell padding and explicit zero.
