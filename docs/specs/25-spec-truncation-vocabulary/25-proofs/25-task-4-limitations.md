# 25-task-4-limitations.md

## Pre-existing engine and harness test timeouts (not caused by slice 06)

`go test ./...` reports failures in `jig/internal/engine` and
`jig/internal/harness`. All of them are `timeout waiting for RunFinished`
or `timeout waiting for ReviewRequest` errors from long-running
integration tests, plus one `TempDir RemoveAll cleanup: directory not
empty` race in `TestTier2ObservesEveryACPHarness/claude-acp`.

These failures reproduce on `origin/main` without any slice 06 changes
applied. Verification (recorded during Task 4):

```
$ git checkout origin/main
$ go test -count=1 ./internal/engine/
--- FAIL: TestReadOnlyStepReceivesRunExecutionView (18.52s)
--- FAIL: TestIntegrationConflictAbortFailsStep (18.52s)
--- FAIL: TestRunSnapshotFileReviewAcceptance (15.02s)
--- FAIL: TestScheduler_WorktreeBranchReuseAcrossRuns (13.04s)
--- FAIL: TestResetFanOut (15.11s)
--- FAIL: TestForEachReset_UpstreamProducerResetChangesCardinality (26.15s)
--- FAIL: TestResetLinearTip (26.18s)
--- FAIL: TestForEachReset_FamilyRemovesChildCommitsAndReExpands (26.24s)
--- FAIL: TestForEachReset_RejectsDirectChildReset (11.40s)
FAIL    jig/internal/engine    187.142s
```

Slice 06 touches only `internal/tui/shared` and `internal/tui/monitor`;
none of the failing packages depend on those changes. The targeted TUI
race run passes cleanly:

```
$ go test -race ./internal/tui/...
ok  jig/internal/tui           2.634s
ok  jig/internal/tui/chart     1.170s
ok  jig/internal/tui/chat      1.138s
ok  jig/internal/tui/detail    1.141s
ok  jig/internal/tui/diffview  1.116s
ok  jig/internal/tui/monitor   4.429s
ok  jig/internal/tui/palette   1.113s
ok  jig/internal/tui/prefs     1.011s
ok  jig/internal/tui/question  1.117s
ok  jig/internal/tui/review    1.633s
ok  jig/internal/tui/runs      1.163s
ok  jig/internal/tui/selector  1.164s
ok  jig/internal/tui/shared    1.252s
```

## Walkthrough artifact generation

Slice 06's visible outcomes are ANSI-styled TUI output. To produce the
PR screenshots I generated ANSI captures via a temporary opt-in test
(`monitor_truncation_ansi_capture_test.go`, deleted after use) that
called the same `writeTranscriptItem` / `writeStreamingOutput` code
paths as the checked-in gallery tests. Those `.ans` files were then
rendered to SVG with `charmbracelet/freeze` and rasterized to PNG via
headless `google-chrome`. The rendered PNGs are attached to the PR;
they mirror the deterministic text captures at
`docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-3-anchor-gallery.txt`.

No other acceptance check was skipped or blocked.
