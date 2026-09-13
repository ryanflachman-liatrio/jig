# 25-task-01-proofs.md

Task 1 — Compute a unified diff and project it through `diffview`.

## Commands and outcomes

```
$ go test ./internal/tui/monitor -run 'TestComputeDiff|TestPresentationStats|TestActivityDiffStats' -count=1 -v
```

Result: PASS. Every canonical shape (single-line replacement,
multi-line replacement, insert-only, delete-only, no-change,
file-creation, oversize-byte, oversize-line, empty-path,
deterministic, round-trip through `diffview`, patch-string
determinism) produces the expected `*diffProjection` and
`computeOutcome`.

Full output is captured to
`25-task-4-acceptance/test.txt` for the whole-repo test run and
locally reproducible with:

```
go test ./internal/tui/monitor -run 'TestComputeDiff' -count=1 -v
```

## Artifacts

| Artifact | Where |
| --- | --- |
| ADR — Diff computation library | [`docs/adr/0013-diff-computation-library.md`](../../../adr/0013-diff-computation-library.md) |
| Compute seam | [`internal/tui/monitor/monitor_diff_compute.go`](../../../../internal/tui/monitor/monitor_diff_compute.go) |
| Compute tests | [`internal/tui/monitor/monitor_diff_compute_test.go`](../../../../internal/tui/monitor/monitor_diff_compute_test.go) |
| `go.mod` promotion | [`go.mod`](../../../../go.mod) — `github.com/hexops/gotextdiff v1.0.3` now a direct require |

## Requirement coverage

| Requirement | Test(s) |
| --- | --- |
| FR-07.1 (compute + project) | `TestComputeDiffSingleLine`, `TestComputeDiffMultiLine`, `TestComputeDiffInsertOnly`, `TestComputeDiffDeleteOnly`, `TestComputeDiffEmptyPathDefaultsToFile` |
| FR-07.2 (library choice) | ADR 0013 records the Option A choice, license (Apache-2.0), supply-chain assessment, and rejection reasons for Options B and C. |
| FR-07.3 (bounded input) | `TestComputeDiffOversizeByte`, `TestComputeDiffOversizeLine` |
| FR-07.4 (parse/emit error routing) | `TestComputeDiffNilDiff`, `TestComputeDiffFileCreation` (nil and file-creation route to the same fallback path exercised by Task 3). |
| FR-07.5 (determinism) | `TestComputeDiffDeterministic`, `TestComputeDiffPatchStringIsDeterministic`, `TestComputeDiffRoundTripsThroughDiffview` |

## Limitations

The parse-error injection case is exercised via Task 3's Monitor
wiring test `TestMonitorDiffComputeFailureFallback` rather than by
mocking `myers.ComputeEdits`: a legitimate `hexops/gotextdiff`
output does not produce a parse error, so the branch is covered at
its callsite where the outcome is routed to the resulting-source
fallback with the `DiffUnavailableHint` label.
