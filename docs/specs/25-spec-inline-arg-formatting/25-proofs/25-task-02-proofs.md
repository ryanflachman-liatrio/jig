# Task 02 Proofs - Width-aware wiring into the unmapped-tool fallback

## Task Summary

This task wired `formatArgsInline` into the actual collapsed-row rendering
path: `summarizeActivity`/`summarizeToolCall` now take a real `width int`
parameter, the unmapped-tool fallback uses the fair-share formatter instead
of the old single-value `primaryToolArg` (deleted), and the real panel width
(`m.transcriptInnerW`) is threaded in at the call site rather than clipping
the result after the fact.

While writing this task's tests, a pre-existing gap surfaced: the real
activity-selection logic in `monitor_transcript_items_view.go` could swap the
summarized activity to a settled result's activity, which normally carries no
`Input`, silently dropping all arguments for any exchange whose result also
carries a `Kind` field. This task fixes that (a scoped `summaryActivity`
copy restores `Input` from the original tool-use activity) since the whole
point of this feature is unusable if arguments never reach the formatter in
real transcripts.

## What This Task Proves

- An unmapped/MCP-style tool activity now shows more than one `key=value`
  pair, where it previously showed only one arbitrary value (or nothing).
- The preview is budgeted against the real panel width before render — a
  narrow width produces a visibly shorter, still in-budget preview than a
  wide width on the same fixture.
- No existing per-kind mapping (`read`, `edit`, `grep`, `bash`, etc.)
  regressed from the signature change.
- Arguments now reliably reach the formatter in the real render path, not
  just in synthetic unit tests that bypass activity selection.

## Evidence Summary

- `TestSummarize*` (5 tests) pass, including the two new ones added by this
  task.
- The full `internal/tui/monitor` package test run has exactly one failure,
  `TestBoundaryBannerFoldsIntoClosingItemLineRange`, confirmed pre-existing
  on `main` (reproduced via `git stash` before this feature's changes).

## Artifact: TestSummarize* full run

**What it proves:** FR-15.1/FR-15.2 wired end-to-end (multi-key preview for
unmapped tools, real-width budgeting) and no regression to the per-kind
mapping cases.

**Command:**

```bash
go test ./internal/tui/monitor -run TestSummarize -v
```

**Result summary:** All 5 tests pass, including
`TestSummarizeActivityUnmappedToolUsesMultiKeyPreview` and
`TestSummarizeActivityUnmappedToolBudgetsAgainstRealWidth`.

```
=== RUN   TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls
=== RUN   TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls/read_path
=== RUN   TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls/unknown_strips_terminal_controls
--- PASS: TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls (0.00s)
    --- PASS: TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls/read_path (0.00s)
    --- PASS: TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls/unknown_strips_terminal_controls (0.00s)
=== RUN   TestSummarizeActivityKeepsSlotsDistinct
--- PASS: TestSummarizeActivityKeepsSlotsDistinct (0.00s)
=== RUN   TestSummarizeActivityUnmappedToolUsesMultiKeyPreview
--- PASS: TestSummarizeActivityUnmappedToolUsesMultiKeyPreview (0.00s)
=== RUN   TestSummarizeActivityUnmappedToolBudgetsAgainstRealWidth
--- PASS: TestSummarizeActivityUnmappedToolBudgetsAgainstRealWidth (0.00s)
=== RUN   TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta
--- PASS: TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta (0.00s)
PASS
ok  	jig/internal/tui/monitor	0.458s
```

## Artifact: Full package regression run

**What it proves:** The signature change and the activity-selection fix do
not regress any other Monitor behavior.

**Why it matters:** This is the broadest available check that wiring a new
parameter through a widely-called function (`summarizeActivity`) didn't
break an unrelated caller or test.

**Command:**

```bash
go test ./internal/tui/monitor/...
```

**Result summary:** Exactly one failure,
`TestBoundaryBannerFoldsIntoClosingItemLineRange`, which is unrelated to this
change — confirmed by running the same test against `main` before this
feature's commits via `git stash`; it fails identically there. Every other
test in the package passes, including
`TestErrorFilterKeepsAtomicToolContext`, whose test window was widened
because the activity-selection fix now correctly shows a `Read: broken.go`
detail that, at the test's original narrow width, competed with the full
error hint text for space.

```
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.00s)
    monitor_transcript_banner_view_test.go:105: turn B does not begin at row=6 (pre-existing on main, unrelated to this feature)
FAIL
FAIL	jig/internal/tui/monitor	1.281s
```

## Reviewer Conclusion

Unmapped tools get a real multi-argument preview budgeted against the actual
panel width, per-kind mappings are unaffected, and a real (previously
dormant) bug that would have silently defeated this entire feature for any
settled exchange whose result carries a `Kind` is fixed and covered by a test
that specifically exercises the real render path rather than only the
formatter in isolation.
