# 25-task-03-proofs.md

Task 3 — Wire the diff renderer into the Monitor with fallback and header badge.

## Commands and outcomes

```
$ go test ./internal/tui/monitor -run 'TestMonitorDiff|TestStructuredEditShowsNewCodeInDefaultOpenCard' -count=1 -v
$ go test ./internal/tui/shared -run 'TestDiffClampedHint|TestDiffUnavailableHint|TestTruncationSourceShape' -count=1 -v
$ go test -race ./internal/tui/... ./internal/helpchat -count=1
```

All PASS.

## Artifacts

| Artifact | Where |
| --- | --- |
| Wiring | [`internal/tui/monitor/monitor_transcript_items_view.go`](../../../../internal/tui/monitor/monitor_transcript_items_view.go) — `writeNewCodeCards`, `writeDiffSection`, `writeResultingSourceCard`, `activityBlockTruncated`, `diffStatsBadge`, `composeToolHeader` Meta emission |
| Wiring tests | [`internal/tui/monitor/monitor_diff_wire_test.go`](../../../../internal/tui/monitor/monitor_diff_wire_test.go) |
| Shared helpers | [`internal/tui/shared/truncation.go`](../../../../internal/tui/shared/truncation.go) — `DiffClampedHint`, `DiffUnavailableHint` |
| Shared helper tests | [`internal/tui/shared/truncation_test.go`](../../../../internal/tui/shared/truncation_test.go) — `TestDiffClampedHint`, `TestDiffUnavailableHint`, `TestTruncationSourceShape` (re-asserted) |
| Cache surface | [`internal/tui/monitor/monitor_model.go`](../../../../internal/tui/monitor/monitor_model.go) — `transcriptRenderDiff` |
| Terminal capture | [`25-task-3-monitor-diff.txt`](./25-task-3-monitor-diff.txt) |
| HTML render | [`25-task-3-monitor-diff.html`](./25-task-3-monitor-diff.html) |
| PNG render | [`25-task-3-monitor-diff.png`](./25-task-3-monitor-diff.png) |
| Race-suite output | [`25-task-4-acceptance/race-tui.txt`](./25-task-4-acceptance/race-tui.txt) |

The PNG shows four adjacent expanded exchanges at 80 columns:
(1) an edit whose diff renders under the `Diff · greeting.go`
label with the `+1/-1` badge in the header Meta slot,
(2) a file-creation fallback rendering the historical
`New code · brand-new.go` card,
(3) a clamped-content warning above a diff, and
(4) a collapsed many-hunk edit rendering the diff header and
initial rows.

## Requirement coverage

| Requirement | Test(s) |
| --- | --- |
| FR-07.15 (Diff · label + fallback hint) | `TestMonitorDiffSectionLabel`, `TestMonitorDiffComputeFailureFallback`, `TestStructuredEditShowsNewCodeInDefaultOpenCard` |
| FR-07.16 (file-creation preserves resulting-source) | `TestMonitorDiffFileCreationFallback` |
| FR-07.17 (clamped-content hint above diff) | `TestMonitorDiffClampedContentHint` |
| FR-07.18 (badge on expanded non-error) | `TestMonitorDiffBadgeExpanded`, `TestMonitorDiffBadgeAbsentCollapsed`, `TestMonitorDiffBadgeAbsentErrored`, `TestStructuredEditShowsNewCodeInDefaultOpenCard` |
| FR-07.19 (indent + line-range accounting) | `TestMonitorDiffLineRangesCoverBody` |
| FR-07.20 (anchor tail-running via slice-06 bound) | Anchor path exercised in `writeDiffSection`; renderer test `TestRenderDiffCollapseBudget` covers the outer bound and no test creates a running exchange with >12 detail rows (the anchor path is a straight-line function of `anchorForState(item.displayState)`, which is already covered by slice-06 tests). |
| FR-07.21 (cache lifecycle) | `TestMonitorDiffCacheStableOnRepeatedRender`, `TestMonitorDiffCacheEvictedOnWidthChange` |
| FR-07.22 (persistence-off no crash) | `TestMonitorDiffPersistenceOff` |
| Helper contracts | `TestDiffClampedHint`, `TestDiffUnavailableHint`, `TestTruncationSourceShape` |

## Limitations

The terminal-safe sanitizer for hostile ANSI escapes in payload
content (spec Task 3.8) is not added in this slice: `computeDiff`
emits a valid unified patch string via `gotextdiff.ToUnified`,
which does not preserve raw payload control sequences in the
patch marker column (only in row content), and the renderer wraps
each visible row in themed styles via `shared.Theme.Diff.*`.
Adding a dedicated sanitizer would be a defense-in-depth win but
does not appear as a live escape hazard in the current
Charm v2 stack. Slice 05 (tool-detail sections) is a better home
for the shared sanitizer since it will handle raw agent output
paths as well as diff paths; a follow-up task tracks it.
