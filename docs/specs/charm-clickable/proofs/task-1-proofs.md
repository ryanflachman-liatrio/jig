# Task 01 Proofs - Root view requests mouse input, scoped to Home

## Task Summary

This task proves the root Bubble Tea view now requests cell-motion mouse
delivery, and that a mouse event never leaks navigation behavior into a
screen other than Home (Detail overlay open, or Monitor active).

## What This Task Proves

- `rootModel.View()` always sets `tea.View.MouseMode = tea.MouseModeCellMotion`,
  alongside the pre-existing `AltScreen`/`BackgroundColor` configuration.
- A `tea.MouseMsg` reaching the root while Monitor is active, or while the
  Detail overlay is open over Home, produces no navigation and no panic.

## Evidence Summary

- `TestRootViewMouseModeCellMotion` confirms the mouse mode is set on both the
  plain Home view and with the Detail overlay open.
- `TestHomeMouseClickNoOpCases/while_Monitor_is_active` and
  `.../while_Detail_overlay_is_already_open` confirm no cross-screen
  navigation occurs.

## Artifact: Targeted test run

**What it proves:** The root view's mouse-mode configuration and the
screen-scoping guard both work as specified.

**Why it matters:** This is the foundation the rest of the feature builds on —
if mouse events aren't delivered (or leak into the wrong screen), nothing
downstream can work correctly.

**Command:**

```bash
go test ./internal/tui/... -run 'TestRootViewMouseModeCellMotion|TestHomeMouseClick' -v
```

**Result summary:** All targeted subtests pass, including the Monitor-active
and Detail-overlay-open no-op cases.

```
=== RUN   TestRootViewMouseModeCellMotion
--- PASS: TestRootViewMouseModeCellMotion (0.01s)
=== RUN   TestHomeMouseClickSelectsRowWithoutOpeningDetail
--- PASS: TestHomeMouseClickSelectsRowWithoutOpeningDetail (0.01s)
=== RUN   TestHomeMouseClickSelectsRowNotKeyboardSelection
--- PASS: TestHomeMouseClickSelectsRowNotKeyboardSelection (0.00s)
=== RUN   TestHomeMouseClickNoOpCases
=== RUN   TestHomeMouseClickNoOpCases/non-primary_button
=== RUN   TestHomeMouseClickNoOpCases/release,_wheel,_and_motion_are_not_click_actions
=== RUN   TestHomeMouseClickNoOpCases/out_of_bounds_and_on_the_Runs_pane
=== RUN   TestHomeMouseClickNoOpCases/panel_border,_footer,_and_blank_rows
=== RUN   TestHomeMouseClickNoOpCases/empty_selector
=== RUN   TestHomeMouseClickNoOpCases/while_filtering
=== RUN   TestHomeMouseClickNoOpCases/while_Detail_overlay_is_already_open
=== RUN   TestHomeMouseClickNoOpCases/while_Monitor_is_active
--- PASS: TestHomeMouseClickNoOpCases (0.03s)
    --- PASS: TestHomeMouseClickNoOpCases/non-primary_button (0.00s)
    --- PASS: TestHomeMouseClickNoOpCases/release,_wheel,_and_motion_are_not_click_actions (0.00s)
    --- PASS: TestHomeMouseClickNoOpCases/out_of_bounds_and_on_the_Runs_pane (0.00s)
    --- PASS: TestHomeMouseClickNoOpCases/panel_border,_footer,_and_blank_rows (0.00s)
    --- PASS: TestHomeMouseClickNoOpCases/empty_selector (0.00s)
    --- PASS: TestHomeMouseClickNoOpCases/while_filtering (0.00s)
    --- PASS: TestHomeMouseClickNoOpCases/while_Detail_overlay_is_already_open (0.00s)
    --- PASS: TestHomeMouseClickNoOpCases/while_Monitor_is_active (0.00s)
PASS
ok  	jig/internal/tui	0.479s
```

## Reviewer Conclusion

Mouse delivery is correctly scoped: the root view opts into cell-motion mouse
events, and no screen other than Home's workflow pane can be reached by a
mouse message, matching the spec's Unit 1 requirements.
