# Task 01 Proofs - Home pointer-targeted list navigation

## Task Summary

Home now routes plain primary clicks and vertical wheels to the rendered Workflows or Runs pane while retaining keyboard-owned activation.

## What This Task Proves

- Workflow and run clicks select actual rendered rows and focus only the clicked pane.
- List wheels move selection by three items with clamping while preserving keyboard focus.
- Filtering, pagination, viewport offsets, row gaps, chrome, invalid geometry, modifiers, and unsupported mouse messages remain bounded.

## Evidence Summary

The selector, Runs, and root Home tests pass using synthetic fixtures at wide and stacked dimensions. Pointer inputs emit no Detail, Monitor, or run action message; workflow selection retains only its existing asynchronous synchronization command.

## Artifact: Focused automated tests

**What it proves:** Owner-local hit testing, three-row movement, visibility, and root routing work together.

**Why it matters:** This is the executable regression boundary for FR-01 through FR-04.

**Command:**

~~~bash
GOCACHE=<temporary-cache> go test ./internal/tui/selector ./internal/tui/runs ./internal/tui -run 'Test(ItemAt|MoveSelection|RunsPointer|HomeMouse|RootViewMouse)'
~~~

**Result summary:** All three packages passed.

~~~text
ok  jig/internal/tui/selector
ok  jig/internal/tui/runs
ok  jig/internal/tui
~~~

## Artifact: Wide and stacked terminal-state capture

**What it proves:** The same routing contract applies at 120x35 and 60x24, and keyboard activation remains available after pointer-only selection.

**Why it matters:** It demonstrates mouse remains optional and never becomes an activation path.

**Artifact path:** `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-1-home-mouse-navigation.txt`

**Result summary:** Synthetic Workflows and Runs selections changed only the intended owner; wheel input retained prior focus and keyboard Enter remained the opening action.

## Reviewer Conclusion

The combined evidence demonstrates Home's pointer-targeted selection contract without adding mouse configuration or activation behavior.
