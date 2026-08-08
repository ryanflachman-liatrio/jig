# Task 1.0 Proofs — Cost foundation: per-step and per-run cost accounting

## Task Summary

This task wires up end-to-end cost recording for agent steps. The SDK's `ResultMessage`
carries `TotalCostUSD *float64` and `Usage *map[string]any`, but jig discarded both.
This task adds the fields to `step.Result`, reads them in `captureStream`, persists them
in `result.json` via the manifest writer, sums them per-run in `RunSnapshot`, and surfaces
them in the TUI run monitor (step list + footer).

## What This Task Proves

- `captureStream` now preserves `TotalCostUSD` from `ResultMessage` — including on error
  paths — and a `ResultMessage` without cost leaves the field `nil` (not `0.0`).
- `RunSnapshot.TotalCostUSD` correctly sums per-step costs; steps whose SDK did not report
  cost contribute nothing.
- `result.json` includes `total_cost_usd` when the SDK reports it (persistence-off path
  writes no cost file, unchanged).

## Evidence Summary

- `TestCostCapture` (runner package) passes: three sub-cases covering success, no-cost, and
  error-path scenarios.
- `TestRunSnapshotCost` (engine package) passes: two steps with known costs sum correctly;
  a third step without cost is harmlessly skipped.
- Full test suite (`go test ./...`) green: 282 tests, 0 failures.

## Artifact: TestCostCapture — cost fields flow through captureStream

**What it proves:** `captureStream` correctly reads `TotalCostUSD` from the SDK
`ResultMessage` and populates `step.Result`. A nil pointer (not `0.0`) is used when the
SDK does not report cost. Cost is preserved on error-path results too.

**Why it matters:** This is the root of the cost recording chain. If `captureStream` drops
the value here, nothing downstream can see it.

**Command:**
```
/Users/ryan/.local/share/mise/installs/go/1.25.12/bin/go test \
  ./internal/runner/ -run TestCostCapture -v -count=1
```

**Result summary:** All three sub-cases (success + cost, success + no cost, error + cost)
pass.

```
=== RUN   TestCostCapture
--- PASS: TestCostCapture (0.00s)
PASS
ok  	jig/internal/runner	0.193s
```

## Artifact: TestRunSnapshotCost — RunSnapshot sums per-step costs

**What it proves:** `RunSnapshot.TotalCostUSD` sums `TotalCostUSD` across all terminal
steps; a step whose `Result.TotalCostUSD` is nil contributes `0` without panicking.

**Why it matters:** The fleet budget (Unit 3) and TUI display both read from this field.
If the summation is wrong, the budget ceiling will be miscalculated.

**Command:**
```
/Users/ryan/.local/share/mise/installs/go/1.25.12/bin/go test \
  ./internal/engine/ -run TestRunSnapshotCost -v -count=1
```

**Result summary:** Three-step workflow with costs `$0.001 + $0.002 + nil = $0.003` is
computed correctly.

```
=== RUN   TestRunSnapshotCost
--- PASS: TestRunSnapshotCost (0.00s)
PASS
ok  	jig/internal/engine	0.179s
```

## Artifact: Full test suite — no regressions

**What it proves:** Adding cost fields to `step.Result`, `RunSnapshot`, and the manifest
did not break any existing test.

**Command:**
```
go test ./...
```

**Result summary:** 282 tests pass across 9 packages; 0 failures.

```
ok  jig/internal/datastore   (282 tests total, 0 failures)
ok  jig/internal/engine
ok  jig/internal/manifest
ok  jig/internal/runner
ok  jig/internal/step
ok  jig/internal/transcript
ok  jig/internal/tui
ok  jig/internal/workflow
```

## Reviewer Conclusion

The cost recording chain is now complete: `captureStream` → `step.Result` →
`result.json` → `RunSnapshot.TotalCostUSD` → TUI footer/step list. The nil-pointer
design correctly distinguishes "SDK did not report cost" from "$0.00 spent". All 282
existing tests continue to pass.
