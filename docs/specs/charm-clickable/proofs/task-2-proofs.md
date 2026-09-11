# Task 02 Proofs - Click selects the workflow row under the pointer

## Task Summary

This task proves a primary click on a visible workflow row moves the
selector's keyboard cursor to that row (the same as `j`/`k`), resolving by
rendered row position rather than by the previous keyboard cursor, without
opening the Detail overlay — and that this doesn't fire while the filter is
capturing text.

## What This Task Proves

- `Model.ItemAt` resolves a click to the item actually drawn at that row,
  independent of `list.SelectedItem()`.
- `Model.SelectItemAt` moves the list's keyboard cursor to that same item via
  `list.Model.Select`, without emitting `ShowDetailMsg` or calling
  `openDetailOverlay` (see the task-file note on the post-implementation
  revision: the feature originally opened Detail directly on click, matching
  the `d` key, but was changed on user direction so a click only selects —
  Detail-opening stays exclusively on `d`).
- A click while the list filter is actively capturing text is a no-op.

## Evidence Summary

- `TestItemAtClickIsIndependentOfKeyboardSelection`: default keyboard
  selection stays on the first item; a click on the third row still resolves
  to the third item.
- `TestHomeMouseClickSelectsRowWithoutOpeningDetail`: clicking the first
  workflow row selects it (`SelectedPath()` reflects it) and leaves the Detail
  overlay closed.
- `TestHomeMouseClickSelectsRowNotKeyboardSelection`: clicking the *second*
  row (while the keyboard cursor is still on the first, cold-start-selected
  item) moves the selection to the second workflow, not the first, and still
  does not open Detail.
- `TestHomeMouseClickNoOpCases/while_filtering`: a click during active
  filtering leaves Home unchanged.

## Artifact: Full click-to-select path

**What it proves:** End to end, a click on a workflow row reaches the correct
selection via the pane-coordinate translation → `SelectItemAt` →
`list.Model.Select` chain, without touching Detail.

**Why it matters:** This is the feature's primary user-facing behavior.

**Command:**

```bash
go test ./internal/tui/... ./internal/tui/selector/... -run \
  'TestItemAtClickIsIndependentOfKeyboardSelection|TestHomeMouseClickSelectsRowWithoutOpeningDetail|TestHomeMouseClickSelectsRowNotKeyboardSelection|TestHomeMouseClickNoOpCases' -v
```

**Result summary:** All pass, including the not-the-selected-row case and the
filtering no-op.

```
--- PASS: TestItemAtClickIsIndependentOfKeyboardSelection (0.00s)
--- PASS: TestHomeMouseClickSelectsRowWithoutOpeningDetail (0.01s)
--- PASS: TestHomeMouseClickSelectsRowNotKeyboardSelection (0.00s)
--- PASS: TestHomeMouseClickNoOpCases (0.04s)
    --- PASS: TestHomeMouseClickNoOpCases/while_filtering (0.01s)
PASS
```

## Reviewer Conclusion

Click-to-select works end to end and resolves by geometry, not prior keyboard
state, matching the spec's revised Unit 2 requirements: a click changes only
the selection, and the Detail overlay remains reachable exclusively through
the `d` key.
