# Task 2.0 Proofs — Expand/Collapse Group with Individual Block Navigation

## Task Summary

This task wires up the keyboard interaction for tool call groups: `space`/`enter` on a
group header toggles it expanded/collapsed; `n`/`N` traverses group headers and individual
blocks within expanded groups; `o` expands all groups and all inner blocks simultaneously.
The toggle and ExpandAll handlers were already implemented in Task 1.0 (required for
compilation); this task adds the four tests that prove the interaction model is correct.

## What This Task Proves

- Pressing `enter` on a collapsed group header expands the group (chatBlocks grows).
- Pressing `enter` again on the group header collapses the group (chatBlocks shrinks).
- The cursor can be navigated into an expanded group with `n`/`N` and back to the header.
- After the last inner block, `n` moves to the next outer item (thinking block), then wraps.
- Pressing `o` expands all groups AND all individual inner blocks (full content rendered).
- Pressing `o` again collapses all groups and inner blocks.

## Evidence Summary

- All 48 tests pass (44 pre-existing + 4 new group interaction tests).
- `TestGroupToggle` proves the expand/collapse state machine via chatBlocks length.
- `TestGroupCursorStability` proves cursor position is maintained after collapse.
- `TestGroupNavigation` proves `n` crosses group boundary correctly and wraps.
- `TestGroupExpandAll` proves `o` propagates through group headers and inner blocks.

## Artifact: Test Suite Pass

**What it proves:** Expand/collapse navigation is correct across all interaction paths.

**Command:**
```bash
go test ./internal/tui/monitor/...
```

**Result summary:** All 48 tests pass.

```
ok  	jig/internal/tui/monitor	0.702s
```

## Artifact: TestGroupToggle — Expand/Collapse State Machine

**What it proves:** `enter` on a group header toggles chatBlocks length correctly and the
▸/▾ marker reflects the state.

**Key assertions:**
- Before expand: `len(chatBlocks) == 1` (header only); body shows `"3 tool calls"`.
- After expand: `len(chatBlocks) == 4` (header + 3 inner blocks); body shows `▾`.
- After collapse: `len(chatBlocks) == 1`; body shows `▸`.

## Artifact: TestGroupCursorStability — Cursor Tracks Group Header After Collapse

**What it proves:** After navigating into a group and back to the header, collapsing
the group leaves cursor at 0 (group header index).

**Key assertions:**
- After expand: `chatBlockCursor == 0`.
- After `n,n`: `chatBlockCursor == 2` (second inner block).
- After `N,N`: `chatBlockCursor == 0` (group header).
- After Enter (collapse): `len(chatBlocks) == 1`, `chatBlockCursor == 0`.

## Artifact: TestGroupNavigation — n Crosses Group Boundary and Wraps

**What it proves:** With a 2-tool group followed by a thinking block, `n` pressed 4 times
from the group header visits: b0(1), b1(2), thinking(3), header(0) — demonstrating that
after the last inner block `n` moves to the next outer item naturally.

**Key assertions:**
- `len(chatBlocks) == 4` after expand.
- After 4 `n` presses from cursor=0: positions = [1, 2, 3, 0].

## Artifact: TestGroupExpandAll — o Expands Groups and Inner Block Content

**What it proves:** `o` (chatExpandAll) expands all groups and all individual blocks inside
them, making inner block content visible. `o` again collapses everything.

**Key assertions:**
- After `o`: body contains `▾`, `"UNIQUE_READ_PATH"`, `"UNIQUE_EDIT_PATH"`, `"THINKING_CONTENT"`.
- After second `o`: body does not contain inner block content or thinking content.

## Reviewer Conclusion

The expand/collapse interaction is fully wired. The four new tests confirm the state machine
(expand/collapse chatBlocks), cursor stability, cross-boundary navigation, and ExpandAll
propagation. All 48 tests pass.
