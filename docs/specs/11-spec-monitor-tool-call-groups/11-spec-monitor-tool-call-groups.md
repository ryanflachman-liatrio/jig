# 11-spec-monitor-tool-call-groups.md

## Introduction/Overview

The monitor's transcript panel currently renders every `tool_use` and `tool_result`
block as individually collapsible items in a flat list. When an agent makes a burst
of tool calls — reading files, running commands, editing code — the transcript fills
up with many individual collapsible blocks that obscure the agent's prose reasoning.

This feature groups consecutive `tool_use`/`tool_result` blocks between agent text
messages into a single **tool call group** — a collapsible section that shows a
one-line summary (tool count + names) when folded and reveals the individual
blocks with their existing expand/collapse behavior when unfolded. The interaction
model mirrors Claude Code's tool call grouping: the group is the primary navigation
unit when collapsed; individual blocks become navigable within an expanded group.

## Goals

- Group consecutive `tool_use` and `tool_result` blocks between text/thinking
  messages into a single collapsible unit in the transcript panel.
- Render collapsed groups as a one-line summary (e.g., `▸ 3 tool calls: Read(main.go), Edit(monitor.go), Bash(go test ./...)`).
- Let the user expand a group to see individual `tool_use`/`tool_result` blocks,
  each still independently expandable/collapsible as today.
- Preserve all existing keyboard navigation semantics (`n`/`N`, `space`/`enter`,
  `o` global toggle) with updated meaning at the group level.
- Keep the existing render cache and `chatBlocks` cursor logic correct after
  the structural change; no regressions in thinking-block or text-block rendering.

## User Stories

- **As a jig operator reviewing a run**, I want tool calls grouped into a collapsible
  section so that I can see the agent's reasoning without wading through a long list
  of individual tool call blocks.
- **As a user navigating the transcript**, I want to press `space` on a tool call
  group to expand it and see the individual calls, then `space` again on any single
  block to read its full input/output, so that I can drill down progressively.
- **As a user who wants to see everything**, I want pressing `o` to expand all tool
  call groups and all individual blocks inside them so that I can read the full
  transcript without manual navigation.
- **As a maintainer of the monitor**, I want the grouping logic isolated in one
  place (alongside the existing `loadChat` pass) so that the rendering and navigation
  code stays simple and the group abstraction is easy to extend.

## Demoable Units of Work

### Unit 1: Group Detection and Collapsed Rendering

**Purpose:** Introduce the group data structure and render collapsed groups as a
one-line summary. After this unit, the transcript panel shows fewer items by
default — all tool calls and results between text messages are hidden behind a
single summary line.

**Functional Requirements:**
- The system shall detect runs of `BlockToolUse`/`BlockToolResult` blocks between
  text or thinking blocks within the same entry or across consecutive entries, and
  treat each such run as one **tool call group**.
- The system shall render a collapsed group as a single line:
  `▸ N tool calls: Name1(arg), Name2(arg)[, …]` where `N` is the count of
  `tool_use` blocks in the group, `Name` is the block's `Name` field, and `arg`
  is the **primary argument** extracted from the block's `Input` JSON (see
  Technical Considerations for the extraction rule). The name list is truncated
  with `… (+K)` if it would exceed the available width, where `K` is the number
  of omitted entries.
- The system shall show the thick left bar (`▌`) in the same charple accent color
  as existing tool_use blocks when the group header is collapsed.
- A group with only one tool call shall render the singular form:
  `▸ 1 tool call: Name(arg)`.
- Tool call groups shall be collapsed by default (same default as existing
  individual tool blocks).
- The group header line shall be added to `chatBlocks` so the block cursor can
  reach it.
- The individual `tool_use`/`tool_result` blocks inside a collapsed group shall
  NOT appear in `chatBlocks` (they are not reachable until the group is expanded).

**Proof Artifacts:**
- Screenshot: transcript panel from a run with multiple agent tool calls shows
  collapsed group lines (e.g., `▸ 5 tool calls: Read(main.go), Read(go.mod), Edit(monitor.go), Bash(go test ./...), Bash(gofmt -l .)`)
  instead of individual block lines — demonstrates collapsed rendering.
- Screenshot: a group with a single call shows `▸ 1 tool call: Read(main.go)` —
  demonstrates singular form.
- `go test ./internal/tui/monitor/...` passes — demonstrates no regressions.

### Unit 2: Expand/Collapse Group with Individual Block Navigation

**Purpose:** Wire up the keyboard toggle so pressing `space`/`enter` on a group
header expands it to show individual blocks (each still independently toggleable)
and pressing again collapses it back to the summary line.

**Functional Requirements:**
- The system shall toggle a group between collapsed and expanded when the block
  cursor is on its group header and the user presses `space` or `enter`.
- When a group is expanded:
  - The group header line changes to `▾ N tool calls: Name1(arg), …`.
  - Each `tool_use` and `tool_result` block in the group appears below the header
    and is individually collapsible/expandable with the existing per-block
    `space`/`enter` behavior.
  - Each individual block within the expanded group is added to `chatBlocks` so
    `n`/`N` can reach it.
- When a group is collapsed, all individual blocks it contains are removed from
  `chatBlocks`; the cursor falls back to the group header.
- The `n`/`N` keys shall move the cursor:
  - Between group headers (and thinking blocks / individual blocks outside groups)
    when traversing the transcript.
  - Into and out of expanded groups naturally (i.e., after the last block in an
    expanded group, `n` moves to the next item after the group).
- Pressing `o` shall toggle `chatExpandAll`; when on, all groups are expanded and
  all individual blocks inside them are expanded.

**Proof Artifacts:**
- Screencast / manual test: cursor on collapsed group → `space` → group expands →
  `n` moves to first individual block → `space` on block → block expands → `N`
  returns to group header — demonstrates full expand/collapse and navigation cycle.
- `go test ./internal/tui/monitor/...` passes — demonstrates navigation correctness.

### Unit 3: Edge Cases and Robustness

**Purpose:** Ensure the grouping handles boundary conditions correctly so the
transcript panel never panics or displays garbled output.

**Functional Requirements:**
- A `tool_use` block with no corresponding `tool_result` (e.g., mid-stream or
  truncated transcript) shall still be included in the group; the missing result
  is not an error.
- A `tool_result` block with no preceding `tool_use` in the group shall be included
  as-is (orphaned result); jig shall not crash.
- Groups that span an entry boundary (tool call in one entry, result in the next)
  shall be merged into a single group if no text/thinking block intervenes.
- When the user changes the focused step (`chatStep` changes), all group expansion
  state shall reset (same behaviour as the existing `chatExpand` reset on step change).
- When a `WindowSizeMsg` triggers `rebuildRenderer`, group expansion state shall
  be preserved (only the glamour render cache is invalidated, not expansion state).

**Proof Artifacts:**
- `go test ./internal/tui/monitor/...` with explicit test cases for orphaned
  blocks, cross-entry groups, and resize — all pass.
- Manual test on a real run: resizing the terminal during transcript viewing
  preserves group expansion state — demonstrates resize robustness.

## Non-Goals (Out of Scope)

1. **Grouping thinking blocks**: `BlockThinking` keeps its existing individual
   expand/collapse behaviour and is not merged into tool call groups.
2. **Cross-step grouping**: groups are bounded to a single focused step's transcript;
   nothing changes about how steps are selected.
3. **Filtering or searching**: no search-within-group or filter-by-tool-name feature.
4. **Mouse interaction**: no mouse click support for group toggle (mouse is not
   currently wired in the monitor transcript panel).
5. **Persisting group expansion state**: expansion state is ephemeral per-step,
   as it is today; it is not saved to disk or across sessions.
6. **Custom group header format**: the `▸ N tool calls: Name1(arg), Name2(arg)` format is
   fixed for this feature; theming/configuration of the summary line is out of scope.

## Design Considerations

The visual language should match what the user already sees for collapsible blocks:

- **Collapsed group header:** thick left bar `▌` in charple + `▸` marker + count
  + name list — same visual rhythm as existing collapsed blocks.
- **Expanded group header:** `▾` marker replaces `▸`; bar and color unchanged.
- **Cursor highlight:** the group header is highlighted with `theme.SelectedLine`
  when the block cursor is on it — same as individual blocks today.
- **Indentation:** individual blocks inside an expanded group should NOT be
  indented relative to the group header (jig's transcript panel uses full-width
  content, not a tree-indent style).
- **Color:** charple accent for group bar (matching tool_use/tool_result blocks).

## Repository Standards

- All grouping logic belongs in the `loadChat` pass in `monitor_transcript.go`
  (or a dedicated helper called from there), not in the render path — keep
  rendering a pure function of pre-computed state.
- New state fields go on `monitorModel` in `monitor_model.go`, following the
  existing naming pattern (`chatBlocks`, `chatExpand`, `chatExpandAll`, …).
- Navigation changes go in `monitor_update.go` alongside the existing `n`/`N`/
  `space` handlers.
- Styles follow `internal/tui/shared/styles.go` conventions: no inline
  `lipgloss.NewStyle()` calls at render time; reference `theme.*` tokens.
- Table-driven tests for group detection logic; integration tests for navigation
  state transitions.
- `go vet ./...` and `gofmt -l .` must be clean before committing.

## Technical Considerations

**Group data structure:** Introduce a `toolGroup` value type in
`monitor_transcript.go` (or a small internal type file) that holds the slice of
`blockKey` values for the `tool_use`/`tool_result` blocks in the group, the list
of tool names, and the total count. `chatBlocks` entries must be extended to carry
either a raw `blockKey` (existing behaviour) or a `toolGroupKey` that references
a `toolGroup`.

**Two-pass load:** The existing `loadChat` already iterates entries and blocks to
build `chatBlocks`. Extend this to a two-pass or single-pass-with-accumulator
approach: accumulate consecutive tool blocks into a pending group; flush the group
(emit one group header entry in `chatBlocks`) when a non-tool block is seen or the
entry list ends.

**Expansion state for groups:** Add `chatGroupExpand map[groupKey]bool` to
`monitorModel` (parallel to `chatExpand`). When a group is expanded, its member
`blockKey` values are appended to the active `chatBlocks` slice immediately after
the group header entry. When collapsed, they are removed. To avoid costly slice
rebuilds, consider maintaining a `chatBlocksActive []chatItem` derived from the
current expansion state, rebuilt lazily on any toggle.

**`chatExpandAll` semantics:** When `chatExpandAll` is true, all groups are
treated as expanded in `writeBlock`, and all individual blocks within them are
treated as expanded — no change to the `o` key handler, only the render logic
that reads `chatExpandAll`.

**Primary argument extraction:** The Claude Code SDK returns `Name` (already
formatted, e.g. `"Read"`, `"Bash"`) and `Input` (raw JSON object). There is no
dedicated summary field in the API response; the label is derived locally.
Implement a pure helper `primaryArg(name string, input json.RawMessage) string`
that unmarshals the input into a `map[string]any` and applies this ordered
lookup to find the most informative single string argument:

1. Try well-known keys in order: `file_path`, `path`, `command`, `pattern`,
   `query`, `url`, `description`, `content` — return the first non-empty string
   value found.
2. If none match, iterate the JSON object keys in sorted order and return the
   first string value found.
3. If the input is empty, nil, or contains no string values, return `""` and
   omit the parens entirely (render `Name` not `Name()`).

The primary arg is truncated to 40 runes in the label to avoid overflowing the
summary line; the full value is still visible in the expanded block.

**No external library changes needed:** This is purely an internal refactor of the
`monitor_transcript.go` and `monitor_model.go`/`monitor_update.go` files.

## Security Considerations

No specific security considerations identified. Tool names and input content are
already rendered in the existing transcript panel; this feature changes layout
only, not data access.

## Success Metrics

1. **Visual density:** a transcript with 10+ tool calls between two text messages
   renders as a single collapsed group line by default, not 10+ individual block
   entries.
2. **No regressions:** `go test ./...` passes clean; existing thinking/text/
   command-output rendering is unaffected.
3. **Navigation correctness:** `n`/`N` reaches every group header and, within
   expanded groups, every individual block — no unreachable blocks.
4. **Resize safety:** expanding a group and then resizing the terminal leaves the
   group expanded and the content correctly reflowed.

## Resolved Decisions

1. **Group header cursor position after collapse:** When the cursor is on an
   individual block inside an expanded group and the user collapses the group,
   the cursor lands on the **group header**. This is cleaner — the header is always
   a valid navigation target and the user can re-expand from there.

2. **Name list truncation strategy:** The list is truncated mid-list with a count
   suffix: `Read(main.go), Edit(monitor.go), … (+3)`. The individual primary arg
   within each label is capped at 40 runes. Both decisions confirmed by user.

3. **Mixed-entry groups:** If a text block is followed immediately by tool blocks
   within the same entry, the group starts at the **first tool block** — no need
   to wait for the next entry. This is the simplest rule and is confirmed as the
   desired behavior.

## Open Questions

No open questions at this time.
