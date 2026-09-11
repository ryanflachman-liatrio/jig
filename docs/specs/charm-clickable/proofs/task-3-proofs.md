# Task 03 Proofs - Hit regions match rendered geometry exactly

## Task Summary

This task proves the click→item mapping tracks the *actual* rendered rows —
including a non-obvious discovery made while implementing it: the bubbles
list always reserves one row for its title/filter line and one for its
pagination line, even when both are blank, because `lipgloss.Height("")` is 1
rather than 0. Getting this wrong would silently misalign every click by a
row or two. The mapping was derived empirically (rendering a real list and
comparing predicted vs. actual row content) before being encoded, not just
inferred from reading the bubbles source.

## What This Task Proves

- Item rows are located correctly across: no filter, an applied filter
  (narrowed item set), a second pagination page, a resized pane, and
  Unicode/wide-glyph names and descriptions.
- Only a row's own interior matches; the row immediately above/below does not.
- Coordinates far out of bounds (including negative) never panic and never
  resolve to an item outside the currently visible page.
- Two different terminal widths render consistently with the tested hit
  regions (a regression fixture against future rendering changes).

## Evidence Summary

- 8 table-driven/targeted tests in `internal/tui/selector/hit_test.go`, plus a
  bounded fuzz target, all pass.
- The fuzz target ran ~280,000 executions over 10 seconds with zero failures.

## Artifact: Full geometry test suite

**What it proves:** Every FR-listed scenario (filtered, paginated, scrolled/
resized, Unicode-width, boundary rows) is covered and passing.

**Command:**

```bash
go test ./internal/tui/selector/... -v
```

**Result summary:** All 9 non-fuzz tests (including pre-existing
`TestDiscoverWorkflows`/`TestSelector`) pass.

```
=== RUN   TestItemAtUnfiltered
--- PASS: TestItemAtUnfiltered (0.00s)
=== RUN   TestItemAtClickIsIndependentOfKeyboardSelection
--- PASS: TestItemAtClickIsIndependentOfKeyboardSelection (0.00s)
=== RUN   TestItemAtPagination
--- PASS: TestItemAtPagination (0.00s)
=== RUN   TestItemAtFiltered
--- PASS: TestItemAtFiltered (0.00s)
=== RUN   TestItemAtResize
--- PASS: TestItemAtResize (0.00s)
=== RUN   TestItemAtUnicodeWidth
--- PASS: TestItemAtUnicodeWidth (0.00s)
=== RUN   TestItemAtNoOpCases
    --- PASS: TestItemAtNoOpCases/negative_and_far_out-of-bounds_coordinates (0.00s)
    --- PASS: TestItemAtNoOpCases/loading_state (0.00s)
    --- PASS: TestItemAtNoOpCases/error_state (0.00s)
    --- PASS: TestItemAtNoOpCases/empty_selector (0.00s)
=== RUN   TestItemAtBoundaryRows
--- PASS: TestItemAtBoundaryRows (0.00s)
=== RUN   TestItemAtFixedSizeFixture
--- PASS: TestItemAtFixedSizeFixture (0.00s)
=== RUN   TestDiscoverWorkflows
--- PASS: TestDiscoverWorkflows (0.05s)
=== RUN   TestSelector
--- PASS: TestSelector (0.00s)
=== RUN   FuzzItemAt
    --- PASS: FuzzItemAt/seed#0 (0.00s)
    --- PASS: FuzzItemAt/seed#1 (0.00s)
    --- PASS: FuzzItemAt/seed#2 (0.00s)
    --- PASS: FuzzItemAt/seed#3 (0.00s)
PASS
ok  	jig/internal/tui/selector	1.983s
```

## Artifact: Bounded fuzz run

**What it proves:** No panics and no out-of-window results across a large
number of arbitrary (bounded) coordinate pairs.

**Command:**

```bash
go test ./internal/tui/selector/ -run '^$' -fuzz FuzzItemAt -fuzztime 10s
```

**Result summary:** ~280,981 executions in 10s, zero failures.

```
fuzz: elapsed: 3s, execs: 85786 (28594/sec), new interesting: 7 (total: 11)
fuzz: elapsed: 10s, execs: 280981 (24561/sec), new interesting: 7 (total: 11)
PASS
ok  	jig/internal/tui/selector	10.459s
```

## Reviewer Conclusion

The hit-testing geometry was verified against real rendered output (not
assumed from reading library source alone), is covered for every scenario the
spec lists, and holds up under bounded fuzzing.
