# Task 03 Proofs - Item-keyed Monitor interaction

## Task Summary

Search, filters, selection, disclosure, live follow, and Gate context now use
stable page-local Transcript item identity.

## What This Task Proves

- A query against either member of a tool exchange retains and selects one item.
- Navigation and expansion pause follow without discarding bounded page state.
- Gate snapshots clone item expansion state before restoring it.

## Evidence Summary

Focused interaction tests and the race-enabled Monitor suite pass.

## Artifact: Search, navigation, follow, and Gate tests

**What it proves:** Item-keyed transitions preserve existing Monitor workflows.

**Why it matters:** This is the behavioral seam most likely to regress during a
presentation-only transcript migration.

**Command:**

~~~bash
go test ./internal/tui/monitor -run 'Test.*(Search|Filter|Navigation|Follow|GateContext)' -race -count=1
~~~

**Result summary:** The focused Monitor interaction coverage passes under the
race detector.

## Reviewer Conclusion

The active Monitor interaction path uses stable Transcript items and preserves
bounded, keyboard-first investigation behavior.
