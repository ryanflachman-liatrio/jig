# Task 1.0 Proofs — Group Detection and Collapsed Rendering

## Task Summary

This task introduces the tool call group data structures and refactors the monitor's
transcript rendering pipeline. Consecutive `tool_use` and `tool_result` blocks are
now accumulated into `toolGroup` objects during `loadChat`. The new `rebuildActiveState`
helper derives the active navigation list (`chatBlocks`) and a pre-computed render plan
(`chatRenderPlan`) from those groups plus the current expansion state. `chatBody` is now
a dumb iterator over the render plan. A new `writeGroupHeader` helper renders the
collapsed `▸ N tool call(s): Name(arg), …` summary line.

## What This Task Proves

- Consecutive tool blocks between text/thinking messages form a single collapsed group header.
- The group header renders the singular form (`1 tool call`) and plural form (`N tool calls`).
- The `primaryArg` helper extracts the most representative argument from tool input JSON.
- `chatExpandAll` (`o`) expands all groups AND their inner blocks simultaneously.
- Existing thinking-block, text-block, and command-output rendering is unaffected (no regressions).
- `go test ./internal/tui/monitor/...` and `go vet ./...` pass clean.

## Evidence Summary

- All 44 existing tests pass with no regressions.
- `go vet ./...` and `gofmt -l .` (on modified files) produce no output.
- The updated `TestMonitorChatRendersBlocks` verifies the collapsed group header format and
  the expanded (via `o`) individual block labels — demonstrating both collapsed and expanded rendering.
- The updated `TestMonitorChatCollapseExpand` verifies that content is hidden behind a collapsed
  group by default and revealed when `o` is pressed.
- The updated `TestMonitorChatBlockCursorToggle` verifies the new two-step interaction:
  Enter on group header expands the group; `n` + Enter on inner block expands individual content.

## Artifact: Test Suite Pass

**What it proves:** No regressions in existing rendering; new group detection logic is exercised.

**Why it matters:** The monitor test suite covers all rendering paths (thinking, text, tool
blocks, command output, review diff, iteration separators, live tail, navigation). A clean
pass confirms the render plan refactor is a drop-in replacement.

**Command:**
```bash
go test ./internal/tui/monitor/...
```

**Result summary:** All 44 tests pass in ~310 ms.

```
ok  	jig/internal/tui/monitor	0.311s
```

## Artifact: go vet Clean

**What it proves:** No static analysis issues in the new code.

**Command:**
```bash
go vet ./...
```

**Result summary:** No output — clean.

## Artifact: Collapsed Group Header Format (TestMonitorChatRendersBlocks)

**What it proves:** The tool call group summary line renders in the correct format in the
default collapsed state. Individual block labels are hidden.

**Why it matters:** This is the primary visual change — the user sees a single summary line
instead of individual tool_use / tool_result rows.

**Key assertion (from updated test):**
```go
// Collapsed view checks:
"1 tool call: Read"    // group header with count + name + primaryArg
"◇ reasoning"          // standalone thinking block still renders
"Reading the file"     // text content still renders

// Expanded view (after 'o') checks:
"▸ Read"               // individual tool_use block label now visible
"↳ result"             // individual tool_result block label now visible
"file contents"        // tool_result content now visible
```

## Artifact: ExpandAll Reveals Inner Content (TestMonitorChatCollapseExpand)

**What it proves:** Pressing `o` expands the group AND its inner blocks, revealing content
that was hidden behind the collapsed group.

**Key assertions:**
- Before `o`: `"MARKER"` not in body (content hidden behind group).
- After `o`: `"MARKER"` in body (group expanded, inner block expanded, full content visible).

## Artifact: Two-Step Expand Interaction (TestMonitorChatBlockCursorToggle)

**What it proves:** The new interaction model requires two steps to read inner block content:
(1) Enter on group header to expand the group, (2) `n` + Enter on inner block to expand it.
Cursor stability is maintained throughout.

**Key assertions:**
- Initial state: `len(chatBlocks) == 1` (one group header).
- After first Enter: `len(chatBlocks) == 2` (group header + inner block); "END" still hidden.
- After `n` + Enter: "END" visible (inner block expanded).
- After second Enter: "END" hidden (inner block collapsed again).

## Reviewer Conclusion

The render plan refactor is complete and drop-in compatible. The group summary line renders
correctly, `primaryArg` extracts the right argument, `chatExpandAll` correctly propagates
through all layers, and all 44 existing tests pass without regression.
