# Task 02 Proofs - Click opens the workflow under the pointer

## Task Summary

This task proves a primary click on a visible workflow row opens that
workflow's Detail overlay, resolving by rendered row position rather than by
the keyboard cursor, and that this doesn't fire while the filter is capturing
text.

## What This Task Proves

- `Model.ItemAt` resolves a click to the item actually drawn at that row,
  independent of `list.SelectedItem()`.
- A click on a row opens the same Detail overlay the `d` key opens for that
  workflow (see the task-file note on the user-confirmed behavior: Home's
  keyboard Enter focuses Runs, not Detail, so "same as Enter" was resolved
  against the standalone selector's Enter/Open semantics, which do map to
  Detail).
- A click while the list filter is actively capturing text is a no-op.

## Evidence Summary

- `TestItemAtClickIsIndependentOfKeyboardSelection`: default keyboard
  selection stays on the first item; a click on the third row still resolves
  to the third item.
- `TestHomeMouseClickOpensDetailOverlaySameAsDKey`: clicking the first
  workflow row opens Detail with that workflow's content.
- `TestHomeMouseClickOpensRowNotKeyboardSelection`: clicking the *second* row
  (while the keyboard cursor is still on the first, cold-start-selected item)
  opens the second workflow's Detail view, not the first's.
- `TestHomeMouseClickNoOpCases/while_filtering`: a click during active
  filtering leaves Home unchanged.

## Artifact: Full click-to-open path

**What it proves:** End to end, a click on a workflow row reaches the correct
Detail overlay via the pane-coordinate translation → `ItemAt` → detail-open
chain.

**Why it matters:** This is the feature's primary user-facing behavior.

**Command:**

```bash
go test ./internal/tui/... ./internal/tui/selector/... -run \
  'TestItemAtClickIsIndependentOfKeyboardSelection|TestHomeMouseClickOpensDetailOverlaySameAsDKey|TestHomeMouseClickOpensRowNotKeyboardSelection|TestHomeMouseClickNoOpCases' -v
```

**Result summary:** All pass, including the not-the-selected-row case and the
filtering no-op.

```
--- PASS: TestItemAtClickIsIndependentOfKeyboardSelection (0.00s)
--- PASS: TestHomeMouseClickOpensDetailOverlaySameAsDKey (0.01s)
--- PASS: TestHomeMouseClickOpensRowNotKeyboardSelection (0.00s)
--- PASS: TestHomeMouseClickNoOpCases (0.03s)
    --- PASS: TestHomeMouseClickNoOpCases/while_filtering (0.00s)
PASS
```

## Reviewer Conclusion

Click-to-open works end to end and resolves by geometry, not keyboard state,
matching the spec's Unit 2 requirements and the user-confirmed destination
(Detail overlay, matching the `d` key).
