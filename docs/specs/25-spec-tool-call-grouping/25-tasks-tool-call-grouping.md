# 25-tasks-tool-call-grouping.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_model.go` | Extends `transcriptItemKind`, `transcriptItem`, and render-cache identity for a stable read-group item without adding a parallel state model. |
| `internal/tui/monitor/monitor_read_group.go` *(new)* | Owns pure read eligibility, adjacency grouping, target/selector extraction and merging, member traversal, and aggregate display state. |
| `internal/tui/monitor/monitor_read_group_test.go` *(new)* | Table-driven normalization, eligibility, break-condition, result-first, page-state, selector, and aggregate-state tests. |
| `internal/tui/monitor/monitor_transcript_items.go` | Keeps use/result correlation authoritative and delegates only the completed item slice to the new grouping pass; extends common member traversal. |
| `internal/tui/monitor/monitor_transcript.go` | Wires grouping between item construction and filtering/default expansion and preserves pruning, reload, and persistence-off contracts. |
| `internal/tui/monitor/monitor_read_group_view.go` *(new)* | Owns the flat group header/tree renderer and expanded per-member detail composition. |
| `internal/tui/monitor/monitor_read_group_view_test.go` *(new)* | Verifies header/tree shapes, no-card rendering, selector rows, state glyphs, widths, truncation, and collapsed/expanded line ranges. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Dispatches the new item kind through the group renderer while retaining `itemTranscriptBody` as the sole line-range owner. |
| `internal/tui/monitor/monitor_tool_summary.go` | Provides sanitized full read-target extraction while preserving the existing shortened display summary. |
| `internal/tui/monitor/clipboard.go` | Adds group-aware selected-item serialization for the visible header/tree and optional expanded member evidence. |
| `internal/tui/monitor/monitor_read_group_interaction_test.go` *(new)* | Drives Bubble Tea key messages to verify one-stop navigation, local/global expansion, and copy behavior. |
| `internal/tui/monitor/monitor_read_group_integrity_test.go` *(new)* | Covers member search/filter visibility, search-hit expansion, reload/resize state, persistence-off, and exact line-range accounting. |
| `internal/tui/monitor/monitor_read_group_gallery_test.go` *(new)* | Generates deterministic synthetic ANSI/HTML/PNG proof captures at representative widths. |
| `internal/tui/shared/icons.go` | Centralizes branch, last, and continuation glyphs under the shared icon vocabulary. |
| `internal/tui/shared/tree.go` *(new)* | Provides the reusable fixed-width tree-prefix primitive. |
| `internal/tui/shared/tree_test.go` *(new)* | Proves connector and continuation prefixes occupy the same visible width. |
| `internal/tui/shared/styles.go` | Adds semantic read-group connector/target styles using existing palette tokens if current `Chat.Tool*` styles are insufficient. |
| `docs/TUI.md` | Documents read groups as page-local transcript items and records their navigation/filtering contract. |
| `CONTEXT.md` | Adds the grouped-read term to the existing transcript-item vocabulary if implementation introduces that user-facing term. |
| `docs/specs/25-spec-tool-call-grouping/25-proofs/` *(new)* | Stores sanitized proof summaries, deterministic captures, command outputs, and the terminal-smoke record. |

### Notes

- Execute the parent tasks in numeric order. Task 2 consumes the immutable group
  model from Task 1; Task 3 consumes the renderer from Task 2; Task 4 validates
  the fully integrated behavior.
- Keep `buildTranscriptItems` responsible for tool use/result correlation and
  result-first ordering. `groupReadTranscriptItems` receives its completed slice
  and must not open a transcript reader or inspect live engine events.
- Reuse `chatItemExpand`, `chatItemExpandAll`, `chatItemRendered`, and
  `chatItemLineRanges`. Do not restore `chatRenderPlan`, `chatGroupHeaders`,
  `chatGroupExpand`, or other fields removed by Slice 00.
- Tests and proof generators must use fabricated paths and output. Never copy a
  real `.jig/` transcript, `.env` value, prompt, credential, or private local path
  into repository artifacts.
- Use Go 1.25 and the pinned `charm.land/*/v2` APIs. Format only changed Go files,
  run focused model/render checks, then the root build/test/vet and targeted TUI
  race checks required by `docs/TESTING.md`.

## Tasks

### [~] 1.0 Normalize adjacent eligible reads into stable group items

Add the pure post-correlation grouping pass and group item representation so a
loaded run of eligible local-file reads at one execution coordinate becomes one
immutable transcript item. Preserve singleton behavior, interruption boundaries,
member order and evidence, page-edge honesty, state pruning, and stable reload
identity. This parent task covers FR-08.1 through FR-08.8.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'Test(GroupReadTranscriptItems|ReadGroupPageState|ReadGroupResultFirst)' -count=1` passes, demonstrating adjacency, eligibility, singleton equivalence, all break conditions, result-first correlation, member exposure, page-local behavior, pruning, and stable group identity for FR-08.1 through FR-08.8.
- Proof document: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-01-proofs.md` records the exact focused command and result, explains the synthetic fixtures, and maps each normalization case to FR-08.1 through FR-08.8.

#### 1.0 Tasks

- [x] 1.1 Add `transcriptItemReadGroup` and an ordered group-member field to the
  live `transcriptItem` model in `monitor_model.go`. Anchor the key and `primary`
  ref to the first member, retain the shared execution coordinate, and extend the
  cache surface/key only as needed for width-, selection-, state-, and
  expansion-sensitive group output (FR-08.5).
- [x] 1.2 In `monitor_read_group.go`, implement a pure sanitized read-target
  decoder over `toolcall.Activity.Input`: accept canonical `read` exchanges only
  when `file_path` or `path` is a non-empty local path; reject URI-like,
  targetless, result-only, and non-read items without relying on title substrings
  (FR-08.2).
- [x] 1.3 Implement `groupReadTranscriptItems([]transcriptItem,
  []transcript.Entry) []transcriptItem` as a linear accumulator after correlation.
  Flush on every ineligible/intervening item and coordinate change, preserve
  member order, emit a group only for runs of at least two, and return singleton
  items unchanged (FR-08.1, FR-08.3, FR-08.4, FR-08.6).
- [x] 1.4 Extend `itemMembers` and any item-description/state helpers to traverse
  every grouped exchange in member order while preserving each member's original
  use/result refs and display state. Do not flatten or invent missing evidence
  (FR-08.6, FR-08.7).
- [x] 1.5 Wire the grouping pass in `setChatPage` immediately after
  `buildTranscriptItems` and before default expansion, pruning, visible-item
  rebuilding, search reruns, and filtering. Confirm `defaultExpandEditCodeItems`
  ignores read groups and `prunePageState` treats group keys as ordinary loaded
  item keys (FR-08.1, FR-08.8, FR-08.20).
- [x] 1.6 Add table-driven normalization tests in
  `monitor_read_group_test.go` for adjacent reads; unchanged singleton identity;
  missing/URI targets; canonical-kind precedence; read-bash-read;
  read-text-read; read-thinking-read; system/unsupported/result-only boundaries;
  and each generation/iteration/attempt boundary (FR-08.1 through FR-08.6).
- [x] 1.7 Add result-first ACP ordering, use-only page-edge, member enumeration,
  surviving-group reload, and stale expansion/render/range pruning cases. Run
  `go test ./internal/tui/monitor -run 'Test(GroupReadTranscriptItems|ReadGroupPageState|ReadGroupResultFirst)' -count=1` and record the sanitized output and FR mapping in `25-proofs/25-task-01-proofs.md` (FR-08.5 through FR-08.8).

### [ ] 2.0 Render the compact read tree with merged selectors and visible state

Render grouped reads as a flat, unframed `Read (N)` tree using the shared status,
style, glyph, width, and truncation vocabulary. Preserve every distinct loaded
target, merge selectors by full sanitized path, omit success glyphs from rows,
and surface aggregate failures or incomplete work before expansion. This parent
task covers FR-08.9 through FR-08.16.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor ./internal/tui/shared -run 'Test(ReadGroup|TreePrefix)' -count=1` passes, demonstrating header/count grammar, target order, selector merging and elision, equal-width connectors, status precedence, no card chrome, and ANSI-safe narrow/wide rendering for FR-08.9 through FR-08.16.
- Terminal capture: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-02-read-group-gallery.txt` records synthetic all-success, failed, repeated-target, narrow, and wide group renders with per-row `lipgloss.Width` measurements, demonstrating the compact tree contract without using real run data.
- Screenshot: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-02-read-group-gallery.png`, with its source path and reproduction notes embedded in `25-task-02-proofs.md`, shows the same synthetic grouped-read gallery and demonstrates that targets and failure state remain scannable without expansion.
- Proof document: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-02-proofs.md` front-loads what each capture proves, embeds the screenshot inline, records the focused test result, and maps the evidence to FR-08.9 through FR-08.16.

#### 2.0 Tasks

- [ ] 2.1 Add shared branch, last, and continuation glyph names in
  `internal/tui/shared/icons.go`, plus pure `TreePrefix`/`TreeContinuationPrefix`
  helpers in `internal/tui/shared/tree.go`. Make every returned prefix exactly
  three visible columns including its trailing space, and table-test the widths
  with `lipgloss.Width` (FR-08.13).
- [ ] 2.2 Add semantic connector/target styles to `shared.Styles.Chat` only if the
  existing `ToolMeta`/`ToolDescription` tokens cannot express the specified dim
  connector and target emphasis. Initialize any additions in `DefaultTheme`
  from existing palette tokens; do not add render-time styles or hex literals
  (FR-08.9, FR-08.13, FR-08.16).
- [ ] 2.3 Implement selector decoding from positive numeric `offset`/`limit`
  input and `Location.Line` fallback, inclusive range formatting, duplicate
  removal, and first-two/ellipsis/last elision. Group rows by sanitized full path
  while preserving first-seen order, then derive the displayed target with
  `shortFile` (FR-08.10 through FR-08.12).
- [ ] 2.4 Implement deterministic member and group state aggregation with
  precedence error, running, warning/unknown, success. Suppress the member glyph
  for successful rows; render the winning shared glyph for failed, running, or
  warning/unknown rows and use the aggregate state for the header (FR-08.14,
  FR-08.15).
- [ ] 2.5 In `monitor_read_group_view.go`, compose the unframed group header
  through `shared.RenderStatusLine` as status icon + `Read` + dim member count,
  followed by one shared-prefix tree row per merged target. Do not call
  `RenderCard` for a group, and keep singleton reads on the existing exchange
  renderer (FR-08.1, FR-08.9, FR-08.10).
- [ ] 2.6 Bound every rendered header/tree row to `transcriptInnerW` with the
  existing ANSI-aware truncation primitives while reserving connector and
  non-success glyph columns. Include width, selection, expansion, aggregate
  state, and member-derived content in cache identity or invalidate the group
  cache on every page replacement (FR-08.16).
- [ ] 2.7 Dispatch `transcriptItemReadGroup` from `writeTranscriptItem` and add
  render tests for count-before-merge, target order, same-basename separation,
  selector merge/elision, no-card chrome, singleton byte equivalence, success
  glyph omission, exceptional-state glyphs, aggregate failure, ANSI/control
  sanitization, and very small/narrow/wide dimensions (FR-08.9 through FR-08.16).
- [ ] 2.8 Add `TestReadGroupGallery`, gated by
  `JIG_UI_SNAPSHOT_DIR`, to emit deterministic synthetic text/HTML/PNG captures
  and geometry notes. Run `JIG_UI_SNAPSHOT_DIR=docs/specs/25-spec-tool-call-grouping/25-proofs go test ./internal/tui/monitor -run TestReadGroupGallery -count=1`, embed the PNG in `25-task-02-proofs.md`, and record the focused Monitor/shared test outcomes and FR mapping.

### [ ] 3.0 Integrate group expansion, navigation, and copy behavior

Connect group items to the existing per-item and expand-all state so each group
remains one cursor stop while expansion reveals member detail in order. Reuse the
current tool-detail, truncation, copy, and keyboard paths; do not reintroduce
member-level navigation or the removed render-plan state model. This parent task
covers FR-08.17 through FR-08.20 and FR-08.22.

#### 3.0 Proof Artifact(s)

- Model test: `go test ./internal/tui/monitor -run 'TestReadGroup(Toggle|Navigation|ExpandAll|Copy)' -count=1` passes after driving real Bubble Tea key messages, demonstrating one cursor stop, local toggle behavior, global override semantics, ordered member details, no edit auto-expansion, and collapsed/expanded copy output for FR-08.17 through FR-08.20 and FR-08.22.
- Terminal capture: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-03-read-group-interaction.txt` records a synthetic collapsed group, expanded member details, and the items immediately before and after it, demonstrating the complete keyboard navigation and disclosure cycle.
- Proof document: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-03-proofs.md` records the exact interaction sequence and focused test result and maps each observation to FR-08.17 through FR-08.20 and FR-08.22.

#### 3.0 Tasks

- [ ] 3.1 Make `itemHasDetail` recognize group items while leaving the existing
  `chatItemExpand[item.key]` and `chatItemExpandAll` decision authoritative.
  Confirm the generic toggle and expand-all handlers need no group-specific
  state mutation; if dispatch is required, keep it in the existing transcript
  update branch rather than introducing another key path (FR-08.17, FR-08.18).
- [ ] 3.2 Render an expanded group under the same header/tree by associating each
  original member with its merged target row and invoking the existing
  tool-activity detail helpers in original member order. Preserve use/result
  pairing, verbatim output, detail anchors, truncation hints, and the flat
  unframed group boundary (FR-08.19).
- [ ] 3.3 Keep group members out of `chatVisibleItems` in both states. Verify
  `n`/`N` traverses from the item before the group to the group and then to the
  item after it, and that toggling changes rendered detail without changing the
  visible-item count or cursor key (FR-08.17).
- [ ] 3.4 Add a group-specific branch to selected-item copy capture: clone every
  member up front, serialize the visible sanitized header/tree, and include
  member detail evidence only when the captured group is expanded. Preserve the
  existing clipboard size/sanitization boundary and page-edge notes (FR-08.22).
- [ ] 3.5 Add Bubble Tea model tests for local toggle, repeated toggle, global
  expand-all override without per-item map rewrites, navigation across the group,
  resize-independent cursor identity, edit auto-expansion exclusion, and
  collapsed/expanded copy payloads (FR-08.17 through FR-08.20, FR-08.22).
- [ ] 3.6 Extend `TestReadGroupGallery` to emit the synthetic collapsed/expanded
  interaction capture at a recorded terminal width. Run
  `JIG_UI_SNAPSHOT_DIR=docs/specs/25-spec-tool-call-grouping/25-proofs go test
  ./internal/tui/monitor -run TestReadGroupGallery -count=1` and
  `go test ./internal/tui/monitor -run
  'TestReadGroup(Toggle|Navigation|ExpandAll|Copy)' -count=1`, then write
  `25-proofs/25-task-03-proofs.md` with the exact key sequence, output, and FR
  mapping.

### [ ] 4.0 Prove filtering, line ranges, lifecycle integrity, and regression safety

Extend item-level visibility and lifecycle seams so a member search/filter hit
keeps the whole group, line ranges cover collapsed and expanded output exactly,
step changes and renderer rebuilds preserve the intended state boundaries, and
persistence-off remains unchanged. Finish with focused, race, root, formatting,
vet, and final-diff checks plus sanitized acceptance evidence. This parent task
covers FR-08.21 and FR-08.23 through FR-08.25 and supplies regression evidence
for the complete specification.

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestReadGroup(Search|Filter|LineRanges|Reload|Resize|PersistenceOff)' -count=1` passes, demonstrating whole-group visibility, exact collapsed/expanded ranges, per-step reset, same-step resize preservation, and persistence-off behavior for FR-08.21 and FR-08.23 through FR-08.25.
- Race test: `go test -race ./internal/tui/... -count=1` passes, demonstrating that the changed TUI paths introduce no race detected by the exercised suite.
- Regression commands: `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, the explicit changed-file `gofmt -l` command in sub-task 4.5, and `git diff --check` complete successfully; their outputs are recorded under `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-04-acceptance/` and demonstrate repository-quality compliance.
- Terminal smoke: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-04-monitor-smoke.md` records terminal dimensions and the exact `n`/`N`, toggle, search/filter, and resize interactions observed against a synthetic persisted transcript, demonstrating end-to-end Monitor behavior without credentials or private run data.
- Proof document: `docs/specs/25-spec-tool-call-grouping/25-proofs/25-task-04-proofs.md` summarizes requirement coverage, links every acceptance artifact, records any blocked check accurately, and confirms the final diff does not leak into non-goals such as other tool kinds, displacement, wire formats, harnesses, or the removed render-plan design.

#### 4.0 Tasks

- [ ] 4.1 Verify and, only where necessary, extend `filteredTranscriptItems`,
  `rerunSearch`, `applyCurrentSearchHit`, and `ensureCurrentSearchHitVisible` to
  use recursive group membership. Add cases where only the third member's input,
  output, location, error, role, or retry coordinate matches and assert the full
  group remains visible and selected/expanded as one item (FR-08.21).
- [ ] 4.2 Add collapsed and expanded `chatItemLineRanges` tests that compare the
  stored range with actual rendered row indexes for the group header, every tree
  row, and all revealed detail rows. Repeat after selection changes, filter/search
  activation, local/global toggle, same-page reload, and width change (FR-08.23).
- [ ] 4.3 Add lifecycle tests proving a focused-step change clears group
  expansion/render/range state, a same-step renderer rebuild preserves expansion
  while invalidating width-dependent renders, a removed page group is pruned, and
  `RunDir == ""` retains the current persistence-off empty state (FR-08.24,
  FR-08.25).
- [ ] 4.4 Review `docs/TUI.md` and `CONTEXT.md` against the shipped behavior.
  Document the page-local grouped-read navigation/filtering contract and term
  only where current guidance would otherwise be incomplete; do not restate
  implementation details or alter unrelated epic-slice documentation.
- [ ] 4.5 Run the focused suites from Tasks 1–4, then
  `go test -race ./internal/tui/... -count=1`, `go build ./cmd/jig`,
  `go test ./...`, and `go vet ./...`. Run `gofmt -l` over the explicit paths
  `internal/tui/monitor/{monitor_model.go,monitor_read_group.go,monitor_read_group_test.go,monitor_read_group_view.go,monitor_read_group_view_test.go,monitor_read_group_interaction_test.go,monitor_read_group_integrity_test.go,monitor_read_group_gallery_test.go,monitor_transcript_items.go,monitor_transcript.go,monitor_transcript_items_view.go,monitor_tool_summary.go,clipboard.go}`
  and
  `internal/tui/shared/{icons.go,tree.go,tree_test.go,styles.go}`; record actual
  outcomes without describing blocked checks as passing.
- [ ] 4.6 Record command outputs as
  `25-proofs/25-task-04-acceptance/{build,test,vet,race-tui,gofmt,git-diff-check,credential-scan}.txt`.
  Run `git diff --check` and scan `25-proofs/` for private-key headers and common
  AWS, OpenAI, and GitHub token shapes; the credential scan artifact shall record
  only the patterns/categories checked and the zero-match outcome, never a
  matched secret. Write `25-task-04-proofs.md` with a complete
  FR-08.1–FR-08.25 evidence map plus an explicit final-diff check against every
  non-goal.
- [ ] 4.7 Perform the synthetic persisted-transcript terminal smoke at recorded
  dimensions: navigate into/out of the group with `n`/`N`, toggle local and
  global expansion, search for a third-member-only token, enable representative
  filters, resize narrow/wide, and confirm failed-member visibility. Save only
  sanitized observations and reproduction steps in
  `25-proofs/25-task-04-monitor-smoke.md`.
