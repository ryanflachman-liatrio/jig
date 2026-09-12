# 25-spec-tool-detail-sections.md

## Introduction/Overview

Move expanded Monitor tool details into the existing transcript card as
first-class sections. Today the card frames only the tool header while
`Locations`, `Input`, `Output`, `Content`, and edit fallback details are drawn
below it with a second indented `│ ` gutter; this feature removes that competing
frame, preserves the established detail vocabulary and bounds, and gives JSON
and shell commands content-aware rendering inside the card.

Source: [OMP transcript parity, slice 05](../../epics/omp-transcript-parity/slices/05-tool-detail-sections.md).
Dependencies: slice 01 provides `shared.Card` and `CardSection`; slice 02
provides the status-line header grammar. Slice 06 owns the shared truncation
vocabulary consumed by this feature, so its helper must exist before this
feature's truncation call sites are finalized.

## Goals

- Render every non-empty expanded tool detail inside the same full-width card
  as its tool header, with no nested indentation frame.
- Preserve the `Locations`, `Input`, `Output`, and `Content` vocabulary and
  ordering while omitting empty sections.
- Render valid JSON and bash commands as syntax-highlighted code without
  reflowing raw command output or other literal tool content as Markdown.
- Bound every detail at the card content width and retain the existing 4 KiB,
  12-row, and 3-tail-row display limits.
- Keep collapsed rendering, structured-edit fallback behavior, cache bounds,
  resize behavior, and transcript navigation correct.

## User Stories

- **As an operator scanning an expanded tool exchange**, I want the call's
  locations, input, output, and content contained by the same card as its
  header so the exchange reads as one coherent unit.
- **As an operator inspecting structured data**, I want valid JSON to be
  syntax-highlighted while invalid or plain output remains verbatim so I can
  read evidence without Markdown changing it.
- **As an operator inspecting a shell call**, I want to see the command as a
  shell-shaped body rather than a JSON object so the action is immediately
  recognizable.
- **As a keyboard user navigating a long transcript**, I want expansion and
  resize to preserve accurate item boundaries and bounded rendering so block
  navigation stays synchronized.
- **As an implementer of later diff rendering**, I want the card primitive's
  flush-body mode to remain supported so line-number gutters can meet the card
  border without another nested frame.

## Demoable Units of Work

### Unit 1: Card-owned detail section model

**Purpose:** Re-host the existing tool-detail vocabulary in card sections at
the correct geometry, while keeping the shared card primitive suitable for
ordinary padded content and future flush-gutter content.

**Functional Requirements:**

- **FR-05.1:** An expanded `transcriptItemToolExchange` shall render its header
  and all details through one `shared.RenderCard` call. No detail row shall be
  appended below the card.
- **FR-05.2:** The system shall build sections in this fixed source order:
  `Locations`, `Input`, `Output`, then each non-diff `Content` entry in its
  original order. Repeated content entries may produce repeated `Content`
  sections; their source order shall not change.
- **FR-05.3:** A section shall be omitted when its source value is absent or
  empty. The renderer shall not emit empty labeled dividers.
- **FR-05.4:** Section labels shall use
  `shared.Theme.Chat.TranscriptLabel`; section body presentation shall use
  existing semantic styles and shall not introduce inline color values or
  package-level styles.
- **FR-05.5:** Tool-card width shall remain the Monitor transcript inner width
  minus the stable two-cell item gutter. Detail bounding and code rendering
  shall use `shared.CardContentWidth` for that exact card width, replacing the
  old `m.transcriptInnerW-8` and inset-width constants.
- **FR-05.6:** `shared.Card` shall continue to support explicit zero left
  padding for content that owns a gutter. A shared card test shall prove the
  flush row starts immediately after the card's left border and still has the
  requested terminal-cell width.
- **FR-05.7:** A collapsed exchange shall render a header-only card with no
  section divider or body row. Expanding or collapsing shall continue to be a
  per-item/global view-state choice and shall not alter transcript data.

**Proof Artifacts:**

- Test: table-driven card and Monitor render cases prove labeled divider order,
  empty-section omission, no second `│ ` body prefix, and exact terminal-cell
  width at narrow and normal layouts.
- Test: a flush-padding card fixture proves a gutter-owning section begins at
  the first content cell and does not create a nested border.
- Terminal capture:
  `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-1-section-gallery.txt`
  shows synthetic cards with zero, one, and multiple sections at 40, 60, and
  90 columns, including measured row widths.

### Unit 2: Content-aware, bounded, terminal-safe bodies

**Purpose:** Make the content inside each section readable for JSON and shell
commands while preserving literal output semantics and display bounds.

**Functional Requirements:**

- **FR-05.8:** Valid JSON in `activity.Input`, `activity.Output`, or
  `content.Raw` shall be pretty-printed by `fenceJSON` and rendered through the
  width-correct inset Glamour/Chroma renderer. Invalid JSON shall fall back to
  a verbatim body and shall not be interpreted as Markdown.
- **FR-05.9:** A bash-kind activity with a string `command` argument shall
  render that command as shell syntax-highlighted content in the `Input`
  section. The rendered body shall contain the command text and shall not show
  the enclosing `{"command": ...}` object. If the command cannot be extracted,
  the ordinary JSON-or-verbatim input path shall remain available so evidence
  is not discarded.
- **FR-05.10:** Plain `Output`, `Content.Text`, and non-JSON `Content.Raw`
  shall use a verbatim section path. The system shall preserve logical line
  breaks and shall not reflow these values as prose Markdown.
- **FR-05.11:** Before an untrusted raw value enters a card body, the display
  path shall remove terminal control sequences that could escape or repaint
  the frame while preserving printable Unicode, tabs, and line breaks.
  Syntax-highlighting escapes added by jig after sanitization shall remain
  intact. Durable transcript content shall not be modified.
- **FR-05.12:** Every section body shall pass through
  `boundTranscriptDetail` using the computed card content width. Byte limiting
  shall remain 4 KiB; row limiting shall remain 12 displayed rows with the
  existing three-row tail preservation for settled content.
- **FR-05.13:** When rows are hidden, the card shall use slice 06's shared,
  pluralized head-window wording (`… N more line(s)`) and live per-item expand
  hint rather than `… N lines hidden`. The indicator shall be a styled body row
  inside the affected section. This feature shall consume the shared helper,
  not add a competing formatter.

**Proof Artifacts:**

- Test: valid JSON input/output contains Chroma styling and pretty indentation;
  invalid JSON remains literal and is not Markdown-reflowed.
- Test: a bash activity renders its synthetic command as shell code and omits
  the JSON `command` wrapper; a missing/non-string command preserves the raw
  fallback.
- Test: long Unicode and ANSI/OSC-bearing synthetic payloads remain valid UTF-8,
  cannot emit source-controlled terminal escapes, wrap at the card content
  width, retain the 4 KiB/12-row/3-tail limits, and show the shared truncation
  hint only when rows are hidden.
- Terminal capture:
  `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-2-content-gallery.txt`
  shows highlighted JSON, a highlighted shell command, plain multiline output,
  and a bounded body with its expand hint.

### Unit 3: Monitor integration, edit fallback, and lifecycle regressions

**Purpose:** Complete the live Monitor integration and prove that moving body
rendering into the cached card does not regress exchange evidence, navigation,
or resize behavior.

**Functional Requirements:**

- **FR-05.14:** The effective details for a paired exchange shall retain
  call-side input and locations and result-side output/content when either
  normalized activity omits fields present on the other. The render path shall
  not change `toolcall.Activity` or the transcript wire format.
- **FR-05.15:** Structured edit content shall continue to short-circuit generic
  detail rendering and show the current `New code · <path>` representation,
  but that representation shall be hosted as content inside the exchange card.
  Computing or presenting a before/after diff remains slice 07 work.
- **FR-05.16:** A completed edit for which the adapter supplies no input,
  output, location, text, raw content, or structured diff shall render an
  `Edit` section containing `Adapter did not provide edit details.`
- **FR-05.17:** The error hint established by slice 02 shall remain in the
  header `Meta` slot. This feature shall not duplicate it as a leading error
  section.
- **FR-05.18:** `transcriptRenderKey` shall continue to distinguish item,
  width, expansion, selection, state, and composed header. Since the cached
  value now includes sections, page replacement, step change, and width change
  shall invalidate stale card bodies, and repeated same-state renders shall
  keep the cache bounded to the loaded items.
- **FR-05.19:** Rebuilding the Monitor on `WindowSizeMsg` shall rebuild the
  inset renderer for the current card content width, clear width-dependent
  render caches, and preserve meaningful selection and scroll state.
- **FR-05.20:** Card height shall remain reflected in
  `chatItemLineRanges`; `n`/`N` item navigation, search navigation, and
  scroll-to-item shall land on the intended exchange after sections add rows.
- **FR-05.21:** Persistence-off (`RunDir == ""`), unmatched tool results,
  text/system/thinking/unsupported items, and non-expanded exchanges shall
  retain their established behavior and shall not force filesystem, harness,
  or backend work.

**Proof Artifacts:**

- Test: a paired synthetic exchange with use-side locations/JSON input and
  result-side text output renders all three labeled dividers inside one card in
  the required order.
- Test: structured edit content is inside the outer exchange card and the
  no-detail completed-edit message remains visible; neither test claims a real
  diff.
- Test: expansion, page replacement, step change, and width changes refresh
  cached section bodies without unbounded cache growth; collapsed rendering is
  exactly a two-row header-only card.
- Test: expanded multi-section cards retain correct line ranges and `n`/`N`
  navigation at narrow and wide widths, including persistence-off and orphan
  regressions.
- Terminal capture and screenshot:
  `docs/specs/25-spec-tool-detail-sections/25-proofs/25-task-3-monitor-details.txt`
  plus a companion PNG show adjacent expanded JSON, bash, output, and edit
  cards in the real Monitor. Notes record terminal size,
  `transcriptInnerW`, selected item, expansion state, and observed wrapping.

## Non-Goals (Out of Scope)

1. **Real diff rendering:** before/after computation, hunk layout, line-number
   gutters, and intra-line highlighting belong to slice 07; this feature only
   moves the existing new-code representation inside the card.
2. **Grouped tool bodies:** grouped reads and grouped-call navigation belong to
   slice 08.
3. **Collapsed inline arguments:** fair-share argument previews belong to slice
   15; this feature changes expanded bodies only.
4. **Bespoke tool renderers:** tools other than bash continue to use the generic
   Locations/Input/Output/Content grammar.
5. **Truncation framework ownership:** slice 06 owns shared wording helpers,
   live-tail policy, and key-hint formatting. This feature only consumes that
   contract for its section bodies.
6. **Header redesign:** tool title, description, icon, badge, and error-meta
   placement remain governed by slice 02.
7. **Persistence or protocol changes:** no change to transcript storage,
   `toolcall.Activity`, runner/harness normalization, backend selection, or
   agent execution.

## Design Considerations

The card's outer `│` is the only vertical frame. Ordinary detail rows use the
card's default one-cell horizontal padding; labels appear as sharp
`├─── Label ───┤` dividers between the rounded top and bottom borders. No
`      Label:` row and no nested `│ ` body prefix remain.

`Locations` remains a body section even for one location. Although one
location could fit the header meta slot, keeping it in the body follows the
slice's fixed vocabulary and avoids a count-dependent layout change. The
slice-02 `toolErrorHint` remains header metadata because it must be visible
while collapsed.

The existing 12-row, three-tail-row, and 4 KiB limits remain the initial card
budgets. They may be revisited only with measured evidence in a later change;
this feature does not silently tune them. Explicit zero card padding remains a
supported geometry mode for future gutter-owning diff or numbered-code bodies,
but ordinary sections stay padded.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md), [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), [Testing](../../TESTING.md), and the domain
  terms in [CONTEXT.md](../../../CONTEXT.md).
- Keep pure card geometry and reusable presentation helpers in
  `internal/tui/shared`; keep transcript-item interpretation and tool-specific
  body construction in `internal/tui/monitor`. Shared code must not import the
  Monitor.
- Add styles only through `shared.Styles`/`DefaultTheme()` and existing palette
  tokens. Do not add inline hex colors or package-level style singletons.
- Measure terminal cells with Lip Gloss/ANSI-aware helpers. Derive frame and
  content widths exactly once; do not replace the old `-8` with another magic
  number.
- Use table-driven tests with synthetic activities and transcript entries.
  Never use or commit real `.jig/` run content, credentials, prompts, or tool
  output.
- Format only changed Go files, run change-specific TUI tests and race checks,
  then run the root build/test/vet checks and `git diff --check`. The nested ACP
  module is unaffected unless implementation unexpectedly crosses that
  boundary.

## Technical Considerations

`renderToolExchangeCard` currently caches a header-only `shared.Card`, while
`writeToolActivityDetails` appends uncached rows afterward. The implementation
should construct the effective activity, bounded/rendered sections, and card
before the cache lookup, then cache the complete card. Existing whole-cache
invalidation in `setChatPage`, `reloadTranscript`, and `rebuildRenderer` must
remain the authority for new page content, step identity, and width.

The exact content width is
`shared.CardContentWidth(availableCardWidth, nil, nil)`, where
`availableCardWidth` is the transcript inner width minus the stable item
gutter. The inset Glamour renderer bakes that width in at construction and must
be rebuilt on resize. Glamour is appropriate only for fenced JSON/shell code;
raw output continues through a non-Markdown path.

The implementation may introduce small, pure helpers for effective-activity
merging, detail sanitization, JSON/shell rendering, and section construction.
It shall not add a dependency, a second card primitive, a parallel rendering
cache, or a renderer that reads the live event bus.

No external technology research materially changes this feature: it uses the
repository's pinned Charm v2 stack and established card/renderer APIs, and the
OMP source behavior is already captured in the epic slice. Installed APIs and
repository tests remain authoritative for implementation details.

## Security Considerations

Tool input, output, paths, and content are agent- and environment-controlled.
Upstream redaction in `internal/runner` and write-time clamping in
`internal/transcript` must remain intact, and the Monitor must retain its
independent 4 KiB display clamp. Synthetic tests and proof captures must not
contain real run data or credential-shaped values.

Raw terminal control sequences must not be allowed to break the card frame,
inject hyperlinks, move the cursor, or repaint later rows. Sanitize untrusted
raw values before adding jig-owned syntax styling; do not sanitize the durable
transcript file in place or strip the renderer's own safe SGR styling after it
is generated.

## Success Metrics

1. **One visual unit:** every expanded paired tool exchange in the covered
   fixtures has one outer card and zero legacy `      Label:`/nested `│ ` rows.
2. **Complete ordered evidence:** non-empty Locations, Input, Output, and
   Content sources appear in that order, while empty sources add no divider.
3. **Content-aware rendering:** valid JSON and extractable bash commands use
   syntax-highlighted code paths; invalid/plain output remains readable
   verbatim.
4. **Bounded and stable layout:** all card rows match their requested
   terminal-cell width, detail limits remain enforced, cache size stays bounded
   by loaded items, and block navigation remains synchronized after resize and
   expansion.
5. **Quality gates:** focused TUI tests and race checks plus `go build
   ./cmd/jig`, `go test ./...`, `go vet ./...`, formatting checks, and
   `git diff --check` pass. These are implementation acceptance targets, not
   results claimed by this Phase 1 document.

## Open Questions

1. The exact visual balance of padded JSON/shell bodies at very narrow widths
   may be tuned during the required synthetic gallery review, provided the
   fixed card/content width contract and existing detail budgets do not change.

