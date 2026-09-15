# 25-spec-tool-call-grouping.md

## Introduction/Overview

Replace repeated, individually navigable read exchanges in the Monitor transcript
with a compact read group that names every loaded target in a tree. The grouping
pass operates on the existing normalized `transcriptItem` sequence, preserves the
durable transcript as the source of truth, and makes a burst of adjacent reads one
cursor stop without hiding failures or search matches.

Source: [OMP transcript parity, Slice 08](../../epics/omp-transcript-parity/slices/08-tool-call-grouping.md).
Dependencies: Slice 02 supplies the shared status-line grammar and icon vocabulary;
Slice 06 supplies the shared truncation wording and live-key expansion hints. This
spec supersedes the historical `11-spec-monitor-tool-call-groups` design for this
surface: that design grouped every tool kind behind a count-only summary and its
render-plan implementation was removed as unreachable by Slice 00.

## Goals

- Collapse every run of at least two eligible adjacent reads at one execution
  coordinate into one stable transcript item and one cursor stop.
- Preserve useful collapsed-state information by rendering every loaded target,
  merged selectors, and an aggregate non-success state in a compact tree.
- Preserve existing tool use/result correlation, filtering, page-edge honesty,
  expansion behavior, cursor restoration, and `chatItemLineRanges` accuracy.
- Keep grouping isolated as a pure pass after `buildTranscriptItems` and before
  filtering; do not create another transcript scheduler or render-plan pipeline.
- Limit the initial allowlist to canonical `read` exchanges so tools with
  meaningful result bodies remain ordinary transcript items by default; the
  later opt-in extension below does not change automatic read grouping.

### Opt-in non-read extension

The implemented follow-up in
[`compact-tool-groups.md`](../../plans/compact-tool-groups.md) adds the
default-off `compact_tool_groups` preference. When enabled, adjacent settled,
successful exchanges of the same registered canonical non-read kind and the
same execution coordinate form a separate `transcriptItemToolGroup`. Its
expanded children are independently navigable bordered cards. For this
extension, settled reasoning hidden by the default view does not interrupt
adjacency; enabling the reasoning filter restores those boundaries. Execution
transitions, live reasoning, and items hidden only by search or other filters
remain boundaries. This extension
does not alter `transcriptItemReadGroup`: reads remain automatic, retain their
special selector-merging renderer, and remain one cursor stop when expanded.

## User Stories

- **As an operator scanning an agent transcript**, I want a burst of file reads
  to occupy one compact tree so that the agent's reasoning remains prominent.
- **As an operator investigating a failed read**, I want the failed target to be
  visible before expansion so that a quiet success row cannot conceal a problem.
- **As an operator navigating with the keyboard**, I want a read group to be one
  cursor stop and its expanded details to remain attached to that stop so that
  navigation density improves without losing evidence.
- **As an operator searching or filtering a transcript**, I want a match in any
  grouped member to retain the complete group so that grouping never makes a
  target or result undiscoverable.
- **As a maintainer**, I want grouping to consume the current page-local
  `transcriptItem` model so that correlation, ordering, retry/reset provenance,
  and persistence boundaries continue to have one owner.

## Demoable Units of Work

### Unit 1: Read-group normalization and stable identity

**Purpose:** Convert eligible runs in the normalized item sequence into immutable
group items while preserving existing exchange correlation and honest boundaries.

**Functional Requirements:**

- **FR-08.1:** After `buildTranscriptItems` has correlated tool uses and results,
  the system shall run one pure grouping pass over `[]transcriptItem`. A run of
  two or more eligible items shall become one `transcriptItemReadGroup`; a run of
  one shall remain the original ungrouped item so its rendering and behavior are
  byte-for-byte unchanged.
- **FR-08.2:** An item shall be eligible only when it is a tool exchange with a
  tool-use activity canonically classified as `read` and a non-empty local-file
  target extracted from `file_path` or `path`. Targetless reads and URI-like
  targets shall remain full standalone exchanges so resolved or otherwise
  informative content stays visible. Result-only items and unknown tools shall
  remain ungrouped. With `compact_tool_groups` disabled, `glob`, `grep`, edits,
  writes, shell calls, web calls, agent calls, and every other tool kind shall
  also remain ungrouped. Eligibility shall not be inferred from a title substring
  when the canonical activity kind identifies another tool.
- **FR-08.3:** Eligible exchanges shall group only while they are adjacent in the
  normalized item sequence and have identical generation, iteration, and attempt
  values. Tool-use IDs remain member identity and shall not affect adjacency.
- **FR-08.4:** Any intervening text, thinking, system, unsupported, result-only,
  or different-tool item shall end the current group. A change in generation,
  iteration, or attempt shall also end it.
- **FR-08.5:** A group key shall use the first member's `blockKey` anchor and a
  dedicated item kind. Member order shall match the normalized item order, and
  every member shall retain its original refs, display state, and coordinate.
- **FR-08.6:** The grouping pass shall inspect only the currently loaded page and
  shall not read transcript files, query the event bus, or synthesize missing
  members. A group that reaches a page edge shall describe only the loaded
  members; use-only and result-only evidence shall retain the semantics already
  established by `buildTranscriptItems` and `completeToolBoundaryContext`.
- **FR-08.7:** `itemMembers` (or its replacement) shall expose all block refs from
  every group member, in member order, so filtering, searching, copying, and
  other item-level consumers can inspect the whole conversation unit.
- **FR-08.8:** Page-state pruning and same-step reload restoration shall treat the
  group key as a live item key. Expansion, render-cache, and line-range entries
  for groups no longer present on the page shall be removed; a surviving group
  anchored to the same first member shall retain its selection and expansion.

**Proof Artifacts:**

- Test: table-driven grouping cases cover three adjacent reads, singleton read,
  read-bash-read, read-text-read, read-thinking-read, result-only boundaries,
  and generation/iteration/attempt changes, demonstrating FR-08.1 through
  FR-08.6.
- Test: a result-first ACP fixture still correlates and orders its exchange at the
  use position before grouping, demonstrating that grouping extends rather than
  rewrites `buildTranscriptItems`.
- Test: page replacement and pruning fixtures demonstrate stable group identity,
  preserved state for surviving groups, and removal of stale group state.

### Unit 2: Compact read tree, selector merging, and aggregate state

**Purpose:** Render each group as a flat, unframed tree that names every target
and makes exceptional member state visible without expansion.

**Functional Requirements:**

- **FR-08.9:** A group shall render without transcript-card chrome. Its header
  shall use the shared status-line grammar to show the read status icon, bold
  `Read` title, and dim `(<member count>)` metadata. The count is the number of
  member exchanges before target-row merging, not the number of rendered rows.
- **FR-08.10:** The collapsed group body shall render one row for every distinct
  loaded target. Rows shall preserve first-seen target order and shall use the
  existing sanitized read summary/path extraction rather than rendering raw,
  unsanitized input.
- **FR-08.11:** Repeated reads of the same underlying path within one group shall
  merge into one target row. Merge identity shall use the unshortened sanitized
  path; display shall use the existing `shortFile` convention. Available line or
  range selectors shall be appended in first-seen order, duplicate selectors
  shall appear once, and more than three selectors shall render the first two,
  the shared ellipsis glyph, and the last selector.
- **FR-08.12:** Read selectors shall be derived only from data already present on
  the tool-use activity: a positive `offset` is a one-line selector when no
  positive `limit` is present, and `offset` plus positive `limit` is the inclusive
  range `offset-(offset+limit-1)`. A normalized `Location.Line` may supply a
  one-line selector when input has none. Missing, malformed, zero, or negative
  selector values shall be omitted without making the read ineligible.
- **FR-08.13:** Every target row shall begin with a shared tree prefix occupying
  exactly three visible columns: branch plus space for non-final rows, last plus
  space for the final row, and the same-width continuation prefix when details
  continue below a row. The branch, last, and continuation glyphs shall be owned
  by `internal/tui/shared` and measured with `lipgloss.Width` in tests.
- **FR-08.14:** A successful member row shall omit a member status glyph. A row
  containing a failed, warning/unknown, or running/pending member shall show the
  corresponding shared status glyph before its target; when several reads merge
  into one row, the most severe visible member state shall win.
- **FR-08.15:** The group header shall aggregate member state with deterministic
  precedence: error, running, warning/unknown, then success. A group containing
  any failed member shall therefore use the shared error glyph/style and be
  visibly distinguishable from an all-success group without expansion.
- **FR-08.16:** Tree paths, selectors, count metadata, and state glyphs shall be
  ANSI-safe and width-bounded by the current transcript inner width. Narrow-width
  truncation shall preserve the three-column tree prefix and use Slice 06 shared
  vocabulary/helpers; it shall never split an ANSI sequence or grapheme.

**Proof Artifacts:**

- Test: a three-read group renders a `Read (3)` header and aligned `├─`/`└─`
  target rows, with no card border and no success glyph on member rows.
- Test: repeated targets merge selectors into one row, de-duplicate selectors,
  and elide the middle after three selectors; malformed selector input falls back
  to an unqualified target without panic.
- Test: `lipgloss.Width` reports equal widths for branch, last, and continuation
  prefixes in Unicode and any already-supported fallback configuration.
- Test: all-success, failed, pending/running, and unknown-member fixtures verify
  row glyph suppression and group-state precedence.
- Terminal capture: a synthetic transcript at representative narrow and wide
  panel widths shows target preservation, selector merging, alignment, failure
  visibility, and bounded truncation.

### Unit 3: Expansion, navigation, filtering, and line-range integrity

**Purpose:** Make a group one navigable item whose expansion reveals existing
per-member evidence while preserving search/filter and scroll-to-item behavior.

**Functional Requirements:**

- **FR-08.17:** A read group shall contribute exactly one entry to
  `chatVisibleItems` and therefore one `n`/`N` cursor stop in both collapsed and
  expanded states. Individual members shall not become independent cursor stops.
- **FR-08.18:** Pressing the existing item toggle key on a selected group shall
  toggle only that group's expansion state. The global expand-all command shall
  expand or collapse groups through the existing `chatItemExpandAll` override
  without rewriting their per-item expansion values.
- **FR-08.19:** When expanded, the group shall retain its compact header/tree and
  reveal each member's existing detail presentation in member order beneath the
  corresponding target row. Use/result pairing, truncation hints, verbatim output,
  and backend-neutral activity data shall be reused rather than reimplemented.
- **FR-08.20:** Read members shall not contain structured edit diffs. The existing
  `defaultExpandEditCodeItems` behavior shall skip group items and shall not
  auto-expand a read group.
- **FR-08.21:** A search or active transcript filter that matches any block or
  activity field of any member shall retain the complete group. Filtering shall
  run after grouping and shall never split a group or discard its nonmatching
  members from rendering.
- **FR-08.22:** Copy-item behavior for a selected group shall include the visible
  group header/tree and, when expanded, its revealed member details, using the
  same rendered-item boundary as ordinary transcript items.
- **FR-08.23:** `chatItemLineRanges` shall contain exactly one entry keyed by the
  group item and shall span every row the group contributes, including expanded
  member details. Its start/end offsets shall remain accurate after selection,
  toggle, filtering, page reload, and width change.
- **FR-08.24:** Changing the focused step shall clear group expansion, render,
  and line-range state with the existing per-step transcript state. A renderer
  rebuild shall invalidate width-dependent group renders while preserving
  same-step expansion state.
- **FR-08.25:** Persistence-off (`RunDir == ""`) and empty transcripts shall
  retain their current behavior and shall not allocate, load, or render a group.

**Proof Artifacts:**

- Model test: keyboard input demonstrates one cursor stop per group, local
  expand/collapse, unchanged per-item state across global expand-all toggles,
  and navigation to the items immediately before and after the group.
- Test: a query matching only the third member and representative tool/error
  filters each retain the complete group and all target rows.
- Test: collapsed and expanded render fixtures compare `chatItemLineRanges`
  against actual rendered row offsets after selection, width change, filtering,
  and reload.
- Test: copy-item output uses the group range and contains every visible target;
  expanded output also contains member detail evidence.
- Test: `go test ./internal/tui/monitor/...` and targeted race tests pass,
  demonstrating integration with the current transcript model.

## Non-Goals (Out of Scope)

1. **Automatic grouping tools other than canonical `read`:** non-read groups
   require the explicit `compact_tool_groups` preference. Unknown, malformed,
   targetless, failed, running, incomplete, and `askuserquestion` exchanges
   remain standalone even when it is enabled.
2. **Displacement:** repeated poll or snapshot calls will not remove an earlier
   transcript item; the durable transcript remains append-only presentation input.
3. **Wire-format or harness changes:** this feature does not modify
   `internal/transcript`, `toolcall.Activity`, vendor adapters, or persisted data.
4. **Grouping across execution coordinates or interruptions:** generation,
   iteration, attempt, text, thinking, system content, unsupported content, and
   other tool kinds remain hard boundaries.
5. **Per-tool bespoke previews:** grouped reads reveal the existing member detail
   rendering; they do not add OMP's optional read-content preview renderer.
6. **Read member-level navigation:** expanding a read group reveals evidence but
   does not turn read member rows into additional `n`/`N` stops. The opt-in
   non-read extension deliberately exposes its bordered child cards as stops.
7. **Reintroducing the removed render-plan design:** no `chatRenderPlan`,
   `chatGroupHeaders`, count-only `writeGroupHeader`, or parallel expand-state
   model shall return.

## Design Considerations

- The group is a flat, unframed compact list, not a card inside the Transcript
  panel. Its quiet success state should recede; error and incomplete state must
  remain visible through the shared icon/style vocabulary.
- The header shape is `• Read (N)` in the all-success case. Two or more rows use
  three-column tree prefixes (`├─ ` and `└─ `); successful rows have no additional
  glyph. Expansion is visible through the presence of member details rather than
  a second nested navigation model.
- A singleton read is not wrapped in group presentation. It continues to use the
  ordinary tool-exchange card and status-line header exactly as before.
- Target rows should be compact but honest. They display the existing shortened
  file label while merging by the sanitized full target so equal basenames do not
  collapse into one logical row.
- Selection shall use the existing non-shifting transcript selection affordance.
  Selected and unselected group renders must occupy the same width and rows.
- All new tree/status glyphs must come through `internal/tui/shared`; do not add
  literal connector glyphs at Monitor call sites. All styling belongs in
  `shared.Styles` and uses existing semantic palette tokens.

## Repository Standards

- Follow `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/CONVENTIONS.md`,
  `docs/TESTING.md`, `docs/TUI.md`, and the transcript vocabulary in `CONTEXT.md`.
- Keep `internal/transcript` as durable truth and Monitor grouping as a
  page-local presentation projection. Do not move orchestration or correlation
  decisions into rendering.
- Extend the live item pipeline in `monitor_transcript_items.go` and
  `monitor_transcript_items_view.go`; do not restore the unreachable mechanism
  removed by Slice 00 or duplicate `buildTranscriptItems` correlation logic.
- Use value receivers for read-only model helpers and pointer receivers for
  mutation. Preserve map ownership and whole-cache invalidation conventions.
- Use shared theme fields and palette tokens. No render-time
  `lipgloss.NewStyle()`, hardcoded hex colors, or unsanitized agent-controlled
  terminal text.
- Prefer table-driven unit tests with synthetic transcript/tool fixtures. Add
  model-message tests for interaction and terminal captures for visual behavior.
- Format only changed Go files with `gofmt -w`. Required verification includes
  targeted Monitor tests, targeted race tests for changed asynchronous surfaces,
  `go test ./...`, `go vet ./...`, and the separate nested ACP module checks only
  if that module is touched (it is not expected to be).
- Update user-facing TUI guidance or architecture documentation in the same
  change if the final behavior makes existing documentation inaccurate.

## Technical Considerations

- Introduce a dedicated `transcriptItemReadGroup` item kind and store ordered
  member `transcriptItem` values on the group item (or an equivalently immutable
  page-local representation). The group's `primary` ref and key anchor come from
  its first member; its coordinate is the shared member coordinate.
- Call the grouping helper exactly once from `setChatPage`, immediately after
  `buildTranscriptItems` and before `defaultExpandEditCodeItems`, pruning,
  `rebuildTranscriptItemState`, search reruns, and filtering. The normalizer must
  remain independently unit-testable and free of model state.
- Recurse through group members in item-level helpers instead of adding parallel
  filtering, copy, and range models. In particular, `itemMembers` is the common
  visibility seam and `itemTranscriptBody` remains the sole owner of line-range
  accounting.
- Reuse `summarizeActivity`, `decodeToolArgs`, `shortFile`, shared status icons,
  shared truncation helpers, and existing detail rendering. If small pure helpers
  are needed for read targets, selectors, aggregate state, or tree prefixes, keep
  them next to the item normalizer or in `internal/tui/shared` when they are truly
  presentation-generic.
- Cache keys for group rendering must include the stable group item key, width,
  selected state, expansion state, and aggregate display state. A member update
  or page replacement must not reuse stale rendered output.
- The grouping pass is linear in the number of loaded items and selector merging
  is linear in group members using bounded maps/slices. It must respect the
  existing `chatWindowMax` page bound and must not scan the full transcript.
- No latest-technology standards research is required for this spec. The feature
  introduces no new external dependency, protocol, storage format, or framework;
  the applicable current contracts are the repository's Go 1.25, Charm v2, TUI,
  and testing conventions already reviewed above.

## Security Considerations

- Grouped targets and selectors are projections of already-redacted transcript
  activity. Continue using the existing sanitized summary/argument path and do
  not read files named by a tool call during rendering.
- Treat tool titles, paths, selector values, output, and errors as untrusted
  terminal text. Strip or normalize control characters through the established
  summary/status-line helpers before width measurement or rendering.
- Do not include real `.jig/` transcripts, prompts, credentials, local paths, or
  environment contents in fixtures, terminal captures, or proof artifacts. Use
  synthetic paths and synthetic tool output only.
- Grouping must not reveal off-page or absent members, infer hidden targets, or
  bypass transcript filters; it changes presentation density, not data access.

## Success Metrics

1. **Density:** a run of 11 adjacent reads at one execution coordinate renders as
   one cursor stop, one header, and target rows rather than 11 cards.
2. **Information preservation:** every loaded distinct target remains named in
   the collapsed group, repeated selectors merge deterministically, and any failed
   member is visible without expansion.
3. **Behavioral integrity:** all FR-08.1 through FR-08.25 have deterministic test
   or terminal-capture evidence with no unknown coverage entries.
4. **Navigation integrity:** collapsed and expanded group line ranges match the
   rendered output exactly, and search/filter hits on any member retain the group.
5. **Quality:** targeted Monitor tests and race checks, root `go test ./...`,
   `go vet ./...`, and changed-file formatting checks pass with no regression in
   singleton tool exchange rendering.

## Open Questions

No open questions at this time.
