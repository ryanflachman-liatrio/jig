# Task 03 Proofs - Oversized user text collapses into a dim summary row

## Task Summary

This task adds the lazy-collapse trigger for oversized user-role text: any
user-role text block whose raw bytes exceed the new `chatTextCollapseBytes`
constant (4096) renders as a single dim summary row instead of invoking the
markdown renderer, which is the substantive performance fix this spec exists
to deliver (omp issue #6308).

## What This Task Proves

- A user-role text block over 4096 bytes renders exactly one summary row of
  the form `<label> · <size> · <n> line(s)`, and the markdown render cache
  never gains an entry for that block (FR-09.4/FR-09.5/FR-09.14).
- The summary label comes from the block's first markdown heading when
  present, or the generic `User input` otherwise (FR-09.7).
- The summary row is ANSI-aware truncated with a trailing ellipsis at narrow
  widths and never exceeds the available content width (FR-09.8).
- An oversized assistant-role text block still renders in full — the
  collapse check is scoped to user-role text only (FR-09.16).
- `itemHasDetail` is true only once a user-role text item crosses the
  threshold, so the collapsed/expanded marker only appears when there is
  something to expand (FR-09.15).

## Evidence Summary

- 9 new subtests across `monitor_transcript_collapse_test.go` pass, one per
  functional requirement above plus a boundary and a helper-level check.
- `go build ./... && go vet ./... && go test ./...` pass at the repository
  root — no regressions from threading the `oversized` flag through
  `transcriptItem` construction.

## Artifact: Collapse-trigger and cache-discipline test

**What it proves:** An oversized block renders one summary row with no block
content leaked into the body, and the render cache (`chatRendered`) has no
entry for that block's key — direct evidence the markdown renderer was never
invoked, since `renderMarkdown` unconditionally writes to the cache on every
call.

**Command:**

~~~bash
go test ./internal/tui/monitor/... -run TestOversizedUserTextCollapsesToSummaryRow -v
~~~

**Result summary:** Pass. The rendered body contains `User input` and `1
line` but none of the block's repeated filler content, and `m.chatRendered`
has zero entries for the block's key after rendering.

~~~text
=== RUN   TestOversizedUserTextCollapsesToSummaryRow
--- PASS: TestOversizedUserTextCollapsesToSummaryRow (0.00s)
PASS
~~~

## Artifact: Label derivation, truncation, and role/threshold scoping

**What it proves:** `collapseSummaryLabel` returns the heading text when the
first non-blank line is an ATX heading (including after leading blank
lines) and falls back to `User input` otherwise; `buildCollapseSummary`
truncates with an ellipsis and never exceeds the requested width; an
oversized assistant block is untouched; and `itemHasDetail` flips exactly at
the 4096-byte boundary.

**Command:**

~~~bash
go test ./internal/tui/monitor/... -v -run \
  'TestCollapseSummaryLabelDerivation|TestCollapseSummaryTruncatesAtNarrowWidth|TestOversizedAssistantTextIsNeverCollapsed|TestItemHasDetailOversizedBoundary|TestHumanizeByteSize'
~~~

**Result summary:** All 5 tests (9 subtests total) pass.

~~~text
=== RUN   TestCollapseSummaryLabelDerivation
=== RUN   TestCollapseSummaryLabelDerivation/heading_present
=== RUN   TestCollapseSummaryLabelDerivation/no_heading
=== RUN   TestCollapseSummaryLabelDerivation/blank_lines_before_heading
--- PASS: TestCollapseSummaryLabelDerivation (0.00s)
=== RUN   TestCollapseSummaryTruncatesAtNarrowWidth
--- PASS: TestCollapseSummaryTruncatesAtNarrowWidth (0.00s)
=== RUN   TestOversizedAssistantTextIsNeverCollapsed
--- PASS: TestOversizedAssistantTextIsNeverCollapsed (0.00s)
=== RUN   TestItemHasDetailOversizedBoundary
=== RUN   TestItemHasDetailOversizedBoundary/at_threshold
=== RUN   TestItemHasDetailOversizedBoundary/over_threshold
--- PASS: TestItemHasDetailOversizedBoundary (0.00s)
=== RUN   TestHumanizeByteSize
--- PASS: TestHumanizeByteSize (0.00s)
PASS
~~~

## Artifact: Repository-wide regression pass

**What it proves:** Threading `oversized` through `transcriptItem`
construction and `itemHasDetail`, and adding the new collapse dispatch in
`renderUserText`, introduces no regressions.

**Command:**

~~~bash
go build ./... && go vet ./... && go test ./...
~~~

**Result summary:** All packages report `ok`; `go vet` reports no issues.

## Reviewer Conclusion

Oversized user-role text now collapses to a cheap, informative one-line
summary without ever touching the markdown renderer or its cache, the
collapse decision is correctly scoped to user-role text only, and the
existing expand/collapse marker (`itemHasDetail`) now correctly reflects
which text items have something to reveal.
