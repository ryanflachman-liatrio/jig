# 25-spec-message-framing.md

## Introduction/Overview

The monitor transcript still renders operator-authored input under a literal
`User` label — the last piece of explicit role chrome in the panel. This spec
replaces that label with a background-tinted bubble (omp parity: roles are
distinguished by fill, not labels), and adds a lazy-collapse path so an
oversized user-role text block renders as one dim summary row without paying
any markdown layout cost until the operator expands it. The bubble is
cosmetic; the lazy build is a real performance fix (omp issue #6308: cold
opens blocked for tens of seconds laying out historical synthetic inputs).

This is the child spec of epic slice
`docs/epics/omp-transcript-parity/slices/09-message-framing.md`. Clarification
round 1 (`25-questions-1-message-framing.md`) resolved: collapse is triggered
by a size threshold only (no transcript-format change), applies to user-role
text items only, and the threshold is 4 KiB of raw block text.

## Goals

- Remove the literal `User` label so no role label remains in the transcript
  body; operator input is identified by a background tint alone.
- Render the user bubble across the panel's content width with one tinted
  blank row above and below the content, reusing the CC-9-proven SGR
  stabilization so glamour resets do not punch holes in the tint.
- Keep assistant prose byte-identical in shape: same horizontal offset, no
  tint, no collapse.
- Render any user-role text block larger than 4 KiB as a single dim summary
  row (`<label> · <size> · <n> lines`) that invokes no markdown rendering
  until expanded.
- Keep the collapse purely a TUI concern: no changes to the transcript format,
  writers, or the `transcriptItemSystem` verbatim path.

## User Stories

- **As an operator reading a run transcript**, I want my own input visually
  distinguished by a background fill instead of a `User` label so that the
  conversation reads as flush markdown with role carried by tint, matching the
  rest of the omp-parity presentation.
- **As an operator opening a step whose user turn carries a large injected or
  pasted document**, I want that turn to appear as a one-line summary so that
  the page paints immediately instead of waiting for glamour to lay out
  hundreds of kilobytes I did not ask to read.
- **As an operator who does want to read a collapsed input**, I want one
  keypress (the existing expand toggle) to render the full markdown body so
  that nothing is lost — only deferred.

## Demoable Units of Work

### Unit 1: User bubble framing

**Purpose:** Replace the `User` label with a full-content-width background
tint, completing the removal of explicit role chrome for operators reading
the transcript.

**Functional Requirements:**

- FR-09.1: The system shall render an operator-authored (user-role) text item
  with a background tint and no literal `User` label anywhere in its rendered
  output.
- FR-09.2: The system shall extend the tint across the transcript panel's
  content width — including trailing fill beyond the text — and shall emit one
  tinted blank row above and one below the content rows.
- FR-09.3: The system shall render assistant prose unchanged: same horizontal
  offset as user content, no background tint.
- FR-09.9: The system shall emit tinted padding rows that survive the slice-04
  edge trim in `itemTranscriptBody` (rows carrying SGR escapes are preserved;
  plain blank rows are trimmed).
- FR-09.10: The system shall re-apply the bubble background after any SGR
  sequence that resets the background (`0`, empty, `49`), using the same
  parameter-aware rule proven for cards (`sgrResetsBackground`), so fenced
  code and other glamour output inside the bubble does not punch holes in the
  tint.
- FR-09.11: The system shall style the bubble via a new `Theme.Chat.UserBubble`
  background style whose color is the existing `hexBBQ` (`#2D2C36`) palette
  token; no new palette token is added (the slice's suggested `hexBubble` was
  checked and `hexBBQ` already exists as bgLeastVisible).
- FR-09.12: The system shall remove the now-unused `Theme.Chat.UserGuidance`
  style field and its initialization (it has no remaining references once the
  label is gone).
- FR-09.13: When the item is selected, the system shall place the selection
  bar prefix on every bubble row (content and padding), following the
  `prefixCardRows` pattern, so selection and tint compose.

**Proof Artifacts:**

- Test: a user text item's rendered output contains no literal `User` and
  carries a background escape on its content rows and both padding rows,
  demonstrates FR-09.1/FR-09.2.
- Test: assistant and user text begin at the same column (equal leading
  offset on first content rune), demonstrates FR-09.3.
- Test: a user bubble containing a fenced code block has no visible cell
  between the first and last content column that lacks the bubble background
  after stabilization, demonstrates FR-09.10.
- Test: a rendered bubble passed through `trimStructuralBlankEdges` retains
  its top and bottom tinted rows, demonstrates FR-09.9.
- Screenshot/capture: transcript showing a user bubble above untinted
  assistant prose at the same offset, demonstrates the end state.

### Unit 2: Lazy collapse of oversized user text

**Purpose:** Stop paying markdown layout cost for large user-role text blocks
the operator has not asked to read — the substantive win of this slice.

**Functional Requirements:**

- FR-09.4: The system shall render a user-role text item whose raw block text
  exceeds the collapse threshold as a single dim summary row stating a label,
  a human-readable byte size, and a line count
  (`<label> · <size> · <n> line(s)`).
- FR-09.5: The system shall not invoke the markdown renderer for a collapsed
  item, and shall not populate the `chatRendered` markdown cache (keyed by
  `blockKey`) with the summary row or any placeholder for that block.
- FR-09.6: The system shall render the item's full markdown, inside the Unit 1
  bubble, when the operator expands it with the existing expand toggle
  (`m.chatItemExpand[item.key]` / expand-all), and collapse it back when
  toggled again.
- FR-09.7: The system shall derive the summary label from the text of the
  block's first markdown heading when one exists, and use the generic label
  `User input` otherwise.
- FR-09.8: The system shall truncate the summary row to the available panel
  width with an ellipsis, using ANSI-aware truncation.
- FR-09.14: The system shall define the threshold as a named constant
  `chatTextCollapseBytes = 4096` beside the existing budgets
  (`chatExpandMax`, `chatWindowMax`) in `monitor_model.go`, measured against
  the raw block text in bytes.
- FR-09.15: The system shall report `itemHasDetail` as true for a collapsible
  (oversized user-role) text item so it shows the collapsed/expanded marker
  and participates in expansion; text items at or under the threshold remain
  non-expandable and render exactly as Unit 1 describes.
- FR-09.16: The system shall leave assistant, system, result, thinking, and
  tool items untouched by the collapse logic regardless of size.

**Proof Artifacts:**

- Test: a user text block over 4096 bytes renders one summary row and a
  counting fake renderer records zero `Render` calls for that block,
  demonstrates FR-09.4/FR-09.5.
- Test: expanding the collapsed item renders the full body (renderer invoked
  once, body content present in output), and the cache holds the real render,
  never the summary, demonstrates FR-09.6/FR-09.5.
- Test: a block beginning `# Session update` summarizes with label
  `Session update`; a heading-less block summarizes with `User input`,
  demonstrates FR-09.7.
- Test: at a narrow width the summary row is truncated with a trailing
  ellipsis and never exceeds the panel content width, demonstrates FR-09.8.
- Test: an assistant text block over 4096 bytes still renders full markdown,
  demonstrates FR-09.16.

## Non-Goals (Out of Scope)

1. **Reaction badge**: omp's emoji lifted into the bubble's top padding row is
   explicitly out of scope for this slice.
2. **OSC 133 prompt zones**: belongs to a later epic (NG2).
3. **`transcriptItemSystem` rendering**: the verbatim path for system/result
   output is already correct and must not change; no distinguishing fill for
   system output (slice Q-09.3 resolved as "not in this slice").
4. **Role labels or timestamps**: no reintroduction of the removed
   `#seq role … ts` header in any form.
5. **Transcript format or writer changes**: no provenance field on
   `transcript.Entry`; collapse is decided purely from rendered-side size
   (questions round 1, answer 1A).
6. **Collapsing assistant prose**: assistant text always renders in full
   (questions round 1, answer 2A).

## Design Considerations

- Both roles start at the same horizontal offset; only fill distinguishes
  them (omp reference: user `paddingX=1, paddingY=1` with tinted rows,
  assistant `paddingY=0`, no tint).
- Bubble color is `hexBBQ` `#2D2C36` — slightly lighter than the `hexPepper`
  base background, mirroring omp's `userMessageBg` being a near-background
  tint. Foreground inherits the default text color.
- Slice Q-09.2 (does a full-width tint read well for a one-word message?) is
  resolved toward omp parity: full content width, accepted as-is.
- The summary row uses the existing dim/hint styling (`Theme.Chat.Hint`
  family) so it reads as chrome, not content.

## Repository Standards

- Follow `docs/TUI.md` (Charm v2, transcript and review engineering) and
  `docs/CONVENTIONS.md`; only prose goes through glamour (`CLAUDE.md` rule) —
  the verbatim system path is untouched.
- Slice-04 discipline in `monitor_transcript_items_view.go` is the governing
  contract for row accounting: per-item scratch buffer, edge trim, zero-height
  guard. Bubble rows must be intentional content under that trim.
- Rendering caches follow the established surfaces: markdown in
  `chatRendered` keyed by `blockKey` (`monitor_transcript.go:589`), item-level
  surfaces in `chatItemRendered` keyed by `transcriptRenderKey`. Note the
  slice document's pointer to `chatItemRendered` for markdown caching is
  stale; `chatRendered` is the markdown cache.
- Tests live beside the code in `internal/tui/monitor` following existing
  transcript view test patterns (`docs/TESTING.md`: executable checks with
  behavior-specific evidence).

## Technical Considerations

- **CC-9 reuse (mandatory):** `internal/tui/shared/card.go` already solves
  tint-over-glamour (`finishRow` + `sgrResetsBackground`): re-apply the
  background after background-resetting SGR sequences, treating parameters
  `0`/empty/`49` as resets and skipping extended-color components. Extract
  that row-tinting logic into a shared helper (e.g. `shared.TintRow(row,
  background)`) consumed by both the card and the bubble rather than
  duplicating the SGR rules. Do not solve CC-9 twice and do not adopt a naive
  `.Background()` wrapper.
- **Where the change lands:** the `transcriptItemText` arm of
  `writeTranscriptItem` (`monitor_transcript_items_view.go`). The user branch
  builds tinted rows from the glamour output; the assistant branch keeps its
  current top-margin-trim behavior.
- **Collapse decision point:** size is checked against the raw
  `block.Text` before any render, so the collapsed path never touches the
  renderer. `itemHasDetail` currently returns `item.kind !=
  transcriptItemText` and needs access to block size for FR-09.15 — thread
  the oversized flag through `transcriptItem` construction or an equivalent
  page-local lookup rather than re-reading blocks in the view.
- **Cache discipline:** the collapsed path bypasses `renderMarkdown` entirely.
  Expansion calls `renderMarkdown` normally, so the first expansion populates
  `chatRendered` with the true render; the summary row is cheap enough to
  build per frame (or cache under a distinct `transcriptRenderKey` surface,
  never under the markdown `blockKey`).
- **Selection interplay:** selected items use the cursor-bar prefix; bubble
  and summary rows must render correctly in both selected and unselected
  states (the `prefixCardRows` pattern shows how multi-row surfaces carry the
  prefix).
- **Search:** transcript search filters on roles and matches raw block text
  (`monitor_search.go`), so a collapsed item remains searchable; a search hit
  landing on a collapsed item selects it as usual (expansion is not required
  for navigation, matching how tool details behave).
- **Threshold:** `chatTextCollapseBytes = 4096` (questions round 1, answer
  3A), documented beside `chatExpandMax` with a comment explaining bytes are
  measured on raw block text because rendered size is unknowable without the
  render this budget exists to skip.
- No external dependency changes; lipgloss v2 and glamour v2 stay as pinned.
  No latest-standards research beyond the repository's own dated CC-9 probe
  was needed.

## Security Considerations

- Operator input can contain typed secrets; it is already redacted at
  transcript write time (`redactTranscriptBlocks`), and this spec adds no new
  persistence or output channel.
- A full-width background makes trailing fill visible as tinted space. This
  reveals only the panel width, not previously hidden content — cosmetic, not
  an exposure.
- The summary row surfaces only a heading, byte size, and line count from
  already-redacted block text; no new data is exposed while collapsed.

## Success Metrics

1. **No role chrome remains**: zero literal role labels in the rendered
   transcript body across the test corpus (grep of rendered output in tests).
2. **Lazy build verified mechanically**: the counting-renderer test proves 0
   render calls for collapsed items on first paint, and exactly 1 after
   expansion.
3. **No regressions**: existing `internal/tui/monitor` and
   `internal/tui/shared` test suites pass unchanged apart from tests that
   asserted the old `User` label.

## Open Questions

1. Humanized size formatting for the summary row (e.g. `412.3 KB` vs
   `412 KiB`) is left to implementation; any unambiguous human-readable form
   is acceptable. Non-blocking.
2. Expanding a worst-case 256 KiB block renders its full markdown (FR-09.6)
   and may pause briefly; this is operator-initiated and matches omp, so no
   additional bound is added. Recorded as an accepted assumption.
3. The generic label `User input` (rather than omp's `Synthetic input`) is
   assumed because jig collapses by size, not provenance, so the content may
   well be operator-pasted. Rename freely during review if a better term
   emerges from `CONTEXT.md` vocabulary. Non-blocking.
