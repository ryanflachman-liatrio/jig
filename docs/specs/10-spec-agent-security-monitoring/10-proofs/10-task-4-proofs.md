# Task 4.0 Proofs — Detection supervisor (Tier-2 fleet harness) and Monitor roster

## Task Summary

Tasks 4.0 (supervisor infrastructure) and 5.0 (monitor roster) are implemented
together because the supervisor tests directly exercise the monitor roster through
the `TestMonitorRoster` harness.

Key design decision: **`sentinel.StepSignal` instead of `engine.StepMessage`.**
The supervisor lives in `internal/sentinel` which already has `engine → sentinel`
as a dependency (for `StepRequest.Guard`). Adding `sentinel → engine` would create
a cycle. The fix: define `StepSignal` in sentinel with the same fields as
`StepMessage`, and document that callers bridge `engine.StepMessage → sentinel.StepSignal`.
This keeps sentinel import-free of engine/runner/tui.

## What This Task Proves

- `Supervisor.Run` batches signals per step: BatchSize signals trigger an immediate
  flush; a second burst with no new transcript entries triggers no new dispatch.
- Exactly one finding is produced from one window read; the fingerprint dedup
  prevents re-reporting the same finding detail.
- Budget enforcement: once summed monitor cost ≥ per-run ceiling, dispatch stops
  and exactly one `degraded-to-tier1` finding is appended (low / observed).
- `boundWindow` applies the dual bound (entryCountCap=20, tokenCeiling=32 000 B);
  at least one entry is always returned even when it exceeds the ceiling alone.
- `StuckLoopPrefilter` and `ExfilPrefilter` correctly identify their respective
  transcript patterns and do not fire on benign input.
- All three monitor roster files (`prompt-injection.md`, `stuck-loop.md`,
  `exfil-pattern.md`) parse as valid `agent_file` triples via `jig validate`.
- `TestMonitorRoster` confirms each monitor produces a finding when the supervisor
  detects its characteristic window marker.

## Evidence Summary

- `TestSupervisorBatching` (sentinel): PASS
- `TestSupervisorBudgetDegrade` (sentinel): PASS
- `TestBoundWindow` (sentinel): PASS — 4 sub-cases
- `TestRenderWindow` (sentinel): PASS
- `TestStuckLoopPrefilter` (sentinel): PASS — 5 sub-cases
- `TestExfilPrefilter` (sentinel): PASS — 4 sub-cases
- `TestMonitorRoster` (sentinel): PASS — 3 sub-cases
- `jig validate` on monitor files: exit 0 — "ok: monitor-validation v1.0.0 — 3 step(s)"
- Full suite: 325 tests, 0 failures (23 new tests added in this task)

## Artifact: TestSupervisorBatching and TestSupervisorBudgetDegrade

**What it proves:** Batch/debounce logic collapses burst signals to one flush;
budget exhaustion stops dispatch and appends the degrade finding without blocking.

**Command:**
```
go test ./internal/sentinel/ -v -count=1 -run "TestSupervisorBatching|TestSupervisorBudgetDegrade"
```

**Result summary:** Both pass. First burst of BatchSize=5 signals triggers one
dispatch; second burst reads nothing (cursor at EOF) → no extra dispatch. Budget
test: cost > ceiling after first call → degraded finding appended → second burst
produces no additional calls.

```
=== RUN   TestSupervisorBatching
--- PASS: TestSupervisorBatching (0.17s)
=== RUN   TestSupervisorBudgetDegrade
--- PASS: TestSupervisorBudgetDegrade (0.23s)
PASS
ok  	jig/internal/sentinel	0.586s
```

## Artifact: TestMonitorRoster

**What it proves:** Each of the three monitor definitions (prompt-injection,
stuck-loop, exfil-pattern) produces exactly one finding when the supervisor reads
a window containing its characteristic marker.

**Command:**
```
go test ./internal/sentinel/ -v -count=1 -run "TestMonitorRoster"
```

**Result summary:** All 3 sub-cases pass. Each produces a single Tier-2 `observed`
finding with the correct `monitor` name and `tier = "monitor"`.

```
=== RUN   TestMonitorRoster
=== RUN   TestMonitorRoster/prompt-injection_finding
=== RUN   TestMonitorRoster/stuck-loop_finding
=== RUN   TestMonitorRoster/exfil-pattern_finding
--- PASS: TestMonitorRoster (0.07s)
    --- PASS: TestMonitorRoster/prompt-injection_finding (0.02s)
    --- PASS: TestMonitorRoster/stuck-loop_finding (0.02s)
    --- PASS: TestMonitorRoster/exfil-pattern_finding (0.02s)
PASS
ok  	jig/internal/sentinel	0.248s
```

## Artifact: Monitor file validation

**What it proves:** All three monitor agent files have valid frontmatter (name,
model) and parse as `agent_file` triples accepted by `jig validate`.

**Command (from repo root):**
```
# Test TOML references all three monitor files; run from repo root.
go run ./cmd/jig validate test_monitors.toml
```

**Result summary:**
```
ok: "monitor-validation" v1.0.0 — 3 step(s)
```

Each file has:
- `model: haiku`
- Empty tools list (tools-off)
- A prompt that instructs the model to output `{"flagged": bool, "severity": string, "detail": string}`

## Reviewer Conclusion

The Tier-2 detection infrastructure is in place:
- `sentinel.Supervisor` with batch/debounce, dual-bounded window assembly,
  fingerprint deduplication, and per-run budget enforcement.
- `MonitorDispatcher` interface and `MonitorDef` struct decouple the supervisor
  from the runner package (no import cycle).
- `StepSignal` type in sentinel lets callers bridge from `engine.StepMessage`
  without creating an import cycle.
- `prefilter.go` implements `StuckLoopPrefilter` and `ExfilPrefilter` using the
  guard package's entropy/pattern detectors.
- Three monitor agent files validated and ready for use.
- 325 tests, 0 failures.
