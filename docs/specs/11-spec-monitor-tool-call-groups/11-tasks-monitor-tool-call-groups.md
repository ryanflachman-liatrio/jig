# 11-tasks-monitor-tool-call-groups.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_model.go` | Holds `blockKey`, model field declarations, `New()`, and `WithSnapshot()` — all new types and fields go here. |
| `internal/tui/monitor/monitor_transcript.go` | Contains `loadChat`, `chatBody`, `writeBlock`, `writeCollapsible`, `collapsible`, `reloadTranscript` — primary implementation site for grouping logic, render plan, and new helpers. |
| `internal/tui/monitor/monitor_update.go` | Contains `updateTranscript` with the Toggle, ExpandAll, NextBlock, PrevBlock handlers — needs dispatch on `chatItem.isGroup`. |
| `internal/tui/monitor/monitor_test.go` | Existing test suite — receives new table-driven tests for group detection, `primaryArg`, navigation, and edge cases; existing tests must not regress. |

### Notes

- No new files are needed — all changes are internal refactors of the four files above.
- Run tests with `go test ./internal/tui/monitor/...`.
- Quality gates before each commit: `go vet ./...` and `gofmt -l .` must produce no output.
- Follow the repository's value-receiver convention for `monitorModel`: new helpers that only read state use value receivers; methods that mutate state use pointer receivers.
- All new styles must reference `shared.Theme.*` tokens — no inline `lipgloss.NewStyle()` calls at render time.

---

## Tasks

### [x] 1.0 Group Detection and Collapsed Rendering

Introduce the group data structures, refactor `loadChat` to accumulate consecutive
tool blocks into `chatGroupHeaders`, implement `rebuildActiveState` to derive
`chatBlocks` and `chatRenderPlan`, write the `primaryArg` helper, implement
`writeGroupHeader`, and refactor `chatBody` to iterate the render plan.  After
this task the Transcript panel shows collapsed group summary lines instead of
individual `tool_use`/`tool_result` rows — no expand interaction yet.

#### 1.0 Proof Artifact(s)

- Screenshot: Transcript panel from a real run with ≥3 consecutive tool calls
  shows a single collapsed line `▸ N tool calls: Name(arg), …` instead of N
  individual block rows — demonstrates group detection and collapsed rendering.
- Screenshot: A run where one agent step has exactly one tool call shows
  `▸ 1 tool call: Name(arg)` — demonstrates singular form.
- Test: `go test ./internal/tui/monitor/...` passes with no regressions in
  existing thinking/text/command-output rendering — demonstrates no regression.

#### 1.0 Tasks

- [x] 1.1 In `monitor_model.go`, define the new union type `chatItem` (fields:
  `isGroup bool`, `key blockKey`, `group *toolGroup`) alongside the existing
  `blockKey` type; define `toolGroup` (fields: `blocks []blockKey`,
  `tools []toolLabel`, `count int`) and `toolLabel` (fields: `name`, `arg string`).

- [x] 1.2 In `monitor_model.go`, define `renderKind` (`int`) and the five
  `renderKind` constants (`renderEntrySep`, `renderEntryHeader`, `renderText`,
  `renderGroupHeader`, `renderBlock`); define `renderItem` (fields: `kind
  renderKind`, `sep string`, `key blockKey`, `blk *transcript.Block`, `role
  transcript.Role`, `group *toolGroup`).

- [x] 1.3 In `monitorModel`, add three new fields: `chatGroupHeaders []chatItem`
  (canonical nav list, stable across expansion state), `chatRenderPlan
  []renderItem` (materialized render sequence), and `chatGroupExpand
  map[blockKey]bool` (per-group expansion state, parallel to `chatExpand`).
  Change the type of `chatBlocks` from `[]blockKey` to `[]chatItem`.

- [x] 1.4 Update `New()` and `WithSnapshot()` in `monitor_model.go` to initialize
  `chatGroupExpand` with `make(map[blockKey]bool)` alongside the existing
  `chatExpand` initialization.

- [x] 1.5 In `monitor_transcript.go`, implement the pure helper
  `primaryArg(name string, input json.RawMessage) string`: unmarshal input into
  `map[string]any`; try keys in order (`file_path`, `path`, `command`, `pattern`,
  `query`, `url`, `description`, `content`) and return the first non-empty string
  value; fall back to sorted-key iteration; return `""` when no string value
  exists.  Truncate the result to 40 runes and append `…` when truncated.

- [x] 1.6 Refactor `loadChat` in `monitor_transcript.go` to a single-pass
  accumulator: for each block, if it is `BlockToolUse` or `BlockToolResult`,
  append to `pendingGroup`; otherwise flush `pendingGroup` as a group `chatItem`
  (if non-empty) then emit a standalone `chatItem` for `BlockThinking`.  Flush any
  remaining `pendingGroup` after all entries.  Store the result as
  `chatGroupHeaders`; call `rebuildActiveState(chatItem{})` at the end.

- [x] 1.7 Implement `rebuildActiveState(saved chatItem)` in
  `monitor_transcript.go`: in a single pass over `chatGroupHeaders`, build
  `chatBlocks` (always include every item; also append individual block `chatItem`
  values for any group that is expanded in `chatGroupExpand` or when
  `chatExpandAll` is true) and `chatRenderPlan` (emit `renderEntrySep`,
  `renderEntryHeader`, `renderGroupHeader`, `renderText`, and `renderBlock` items;
  suppress `renderEntryHeader` for tool-role entries absorbed into a group).
  After building `chatBlocks`, restore `chatBlockCursor` by linear-searching for
  the item matching `saved` (by `isGroup` + `key`); default to `0` when not found.

- [x] 1.8 Implement `writeGroupHeader(b *strings.Builder, item chatItem, expanded,
  cursored bool)` in `monitor_transcript.go`: render `▌` in
  `shared.Theme.Chat.BarToolCall`; render `▸`/`▾` marker; render `N tool call(s):`
  prefix; format the name list dynamically to fit `m.transcriptInnerW` minus the
  fixed prefix length, truncating with `… (+K)` for omitted entries; apply
  `shared.Theme.Chat.BlockCursor` when cursored.

- [x] 1.9 Refactor `chatBody` in `monitor_transcript.go` to iterate
  `m.chatRenderPlan` with a `switch` on `item.kind` instead of ranging over
  `chatEntries` directly. Route `renderText` and `renderBlock` to the existing
  `writeBlock` helper; route `renderGroupHeader` to `writeGroupHeader`; emit
  separator and header strings for `renderEntrySep` and `renderEntryHeader`.

- [x] 1.10 Update `writeCollapsible` in `monitor_transcript.go`: change the
  `cursored` check from comparing `chatBlocks[cursor] == key` (old `blockKey`
  equality) to comparing `!chatBlocks[cursor].isGroup && chatBlocks[cursor].key ==
  key`.

- [x] 1.11 Update `reloadTranscript` in `monitor_transcript.go` to reset
  `chatGroupExpand` alongside `chatExpand`:
  `m.chatGroupExpand = make(map[blockKey]bool)`.

---

### [x] 2.0 Expand/Collapse Group with Individual Block Navigation

Wire up the keyboard handlers so `space`/`enter` on a group header toggles it
expanded/collapsed, individual blocks within expanded groups are added to / removed
from `chatBlocks`, cursor stability is maintained across rebuilds, and `o`
(ExpandAll) expands all groups and their inner blocks simultaneously.

#### 2.0 Proof Artifact(s)

- Manual test (recorded step-by-step): cursor on collapsed group → `space` →
  header shows `▾ N tool calls: …` and individual blocks appear below → `n`
  moves to first inner block → `space` on block → block expands → `N` returns
  to group header → `space` on group → group collapses, cursor returns to header.
- Manual test: pressing `o` expands all groups and all inner blocks simultaneously;
  pressing `o` again collapses all — demonstrates ExpandAll semantics.
- Test: `go test ./internal/tui/monitor/...` passes — demonstrates navigation
  state-machine correctness.

#### 2.0 Tasks

- [x] 2.1 In `monitor_update.go`, update the `Toggle` handler in
  `updateTranscript`: save the current item as `saved := m.chatBlocks[cursor]`;
  if `saved.isGroup`, toggle `m.chatGroupExpand[saved.key]`; otherwise toggle
  `m.chatExpand[saved.key]`; call `m.rebuildActiveState(saved)` then
  `m.refreshPanels()`.

- [x] 2.2 In `monitor_update.go`, update the `ExpandAll` handler: capture `saved`
  from `chatBlocks` before toggling, call `m.rebuildActiveState(saved)` after
  flipping `m.chatExpandAll`, then `m.refreshPanels()`.

- [x] 2.3 Add `TestGroupNavigation` in `monitor_test.go`: write a transcript with
  one group (two tool calls) followed by a thinking block; expand the group so
  `chatBlocks` = [group header, block0, block1, thinking]; press `n` four times
  from the group header and assert cursor visits block0 → block1 → thinking →
  wraps back to group header — demonstrates that after the last inner block `n`
  moves naturally to the next outer item and wraps correctly.

- [x] 2.4 Add `TestGroupToggle` in `monitor_test.go`: write a transcript with
  three consecutive `tool_use`/`tool_result` blocks; enter the step; assert
  `chatBlocks` has one item (group header); press `space`; assert `chatBlocks`
  grows to four items (header + three individual blocks); assert `chatBody()`
  contains `▾`; press `space` again; assert `chatBlocks` is back to one item and
  `chatBody()` contains `▸`.

- [x] 2.5 Add `TestGroupCursorStability` in `monitor_test.go`: expand a group,
  navigate `n` to the second inner block, press `space` on the group header (via
  navigating back with `N`), assert `chatBlockCursor` lands on the group header
  index (0), not a stale inner-block index.

- [x] 2.6 Add `TestGroupExpandAll` in `monitor_test.go`: two consecutive tool calls
  (with distinct content) followed by a thinking block; press `o`; assert
  `chatBody()` contains `▾` for the group header and `▾` for the thinking block;
  also assert that the full content of each inner tool block is rendered (i.e.,
  `chatBody()` contains the tool input/result text, proving individual blocks
  inside the group are also expanded, not just the group header); press `o` again
  and assert the group and thinking block collapse back to `▸`.

---

### [x] 3.0 Edge Cases and Robustness

Validate boundary conditions: orphaned `tool_use` (no result), orphaned
`tool_result` (no preceding `tool_use`), groups that span an entry boundary,
correct reset of `chatGroupExpand` on step change, and preservation of group
expansion state across terminal resize (`WindowSizeMsg`).

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor/...` with explicit table-driven cases for
  orphaned blocks, cross-entry groups, step-change reset, and resize — all pass.
- Manual test: expand a group, resize the terminal (`tea.WindowSizeMsg`), confirm
  the group stays expanded and content reflows correctly.
- CLI: `go vet ./...` and `gofmt -l .` produce no output — demonstrates quality
  gates pass.

#### 3.0 Tasks

- [x] 3.1 Add `TestPrimaryArg` in `monitor_test.go` as a table-driven test
  covering: `file_path` key present → returns value; `path` key only → returns
  value; `command` key only → returns value; no known key → returns first
  string value from sorted keys; empty input → returns `""`; input has only
  non-string values → returns `""`; arg exceeds 40 runes → truncated to 40 runes
  ending with `…`.

- [x] 3.2 Add `TestLoadChatGroupDetection` in `monitor_test.go` as a table-driven
  test covering: consecutive `tool_use` + `tool_result` in one entry → one group
  in `chatGroupHeaders`; thinking block between tool blocks → two groups; text
  block between tool blocks → two groups; cross-entry group (`tool_use` in
  assistant entry, `tool_result` in following user entry, no text between) → single
  group spanning both entries; orphaned `tool_use` (no `tool_result`) → included
  in group, no panic; orphaned `tool_result` (no preceding `tool_use`) → included
  in group, no panic.

- [x] 3.3 Add `TestGroupExpandReset` in `monitor_test.go`: build a monitor with
  two steps each having tool-call transcripts; navigate to step A, expand a group
  (`chatGroupExpand` is non-empty); navigate to step B (triggering
  `reloadTranscript`); assert `chatGroupExpand` is empty.

- [x] 3.4 Add `TestGroupExpandPreservedOnResize` in `monitor_test.go`: navigate
  to a step, expand a group; send a `tea.WindowSizeMsg`; assert the group remains
  expanded (`chatGroupExpand[groupKey]` is true) and `chatBody()` still contains
  `▾`.

- [x] 3.5 Add empty-transcript guard assertion to `TestLoadChatGroupDetection`: when
  the transcript has no entries (or only text entries), `chatGroupHeaders` and
  `chatBlocks` must both be empty slices after `loadChat`, and `chatBlockCursor`
  must be 0 — guards against a panic in callers that access `chatBlocks[chatBlockCursor]`
  on a zero-length slice.

- [x] 3.6 Run `go vet ./...` and `gofmt -l .` and fix any issues they report.
