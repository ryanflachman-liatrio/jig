# Task 02 Proofs - `MonitorAdapter` migrated off the Claude SDK onto `AcpHarness`

## Task Summary

Unit 2 removes the sentinel security classifier's last dependency on
`claude-agent-sdk-go`: `internal/runner/monitor.go`'s `MonitorAdapter` now
opens a plain `harness.Harness`-shaped session (in practice `AcpHarness`)
instead of a `claudecode.Client`, and its structured-output decoding no
longer assumes a wire-guaranteed JSON shape.

## What This Task Proves

- `MonitorAdapter.Dispatch` no longer imports or references the Claude SDK
  anywhere in `internal/runner`.
- The deny-all tool surface and JSON-schema request are expressed purely in
  terms of jig's own `harness.SessionSpec` (`AllowedTools=[]`,
  `DisallowedTools=[]`, a `PermissionFn` that always denies, and
  `Schema=monitorJSONSchema`), not the removed SDK's option list.
- `decodeMonitorVerdict` tolerates prose surrounding a JSON block (advisory
  schema, not wire-enforced) while still rejecting a malformed or
  policy-violating verdict just as strictly as before.
- `Dispatch` retries exactly once, on a fresh session, when a decode-shaped
  failure occurs — including when `AcpHarness`'s own internal
  structured-output retry loop is exhausted — and does not retry on
  connection/timeout/agent failures. Either way, `Launched=true` once a
  session was opened, so `internal/sentinel`'s existing fail-open handling
  sees a normal `Dispatch` error in every failure case.
- Cost/usage tracking stays nil/false on the ACP-backed path, matching spec
  12's existing convention (Open Question 2, resolved: no new cost tracking
  added).

## Evidence Summary

- `grep -rn "claudecode\|claude-agent-sdk-go" internal/runner/monitor.go
  internal/runner/monitor_test.go` returns nothing.
- `go build ./... && go vet ./...` pass with zero diagnostics.
- `go test ./internal/runner/... ./internal/sentinel/...` passes, including
  new tests for tolerant decoding and the decode-retry-then-fail-open path.
- `go test -race ./internal/engine ./internal/runner ./internal/harness` and
  a full `go test ./...` pass, with one pre-existing, unrelated failure in
  `internal/tui/monitor` reproduced identically on `main` before this unit's
  changes (see the dedicated artifact below).

## Artifact: SDK reference removed from the monitor seam

**What it proves:** The classifier dispatch path no longer imports the
Claude SDK at all.

**Why it matters:** This is the unit's own required proof artifact — the
grep that must return nothing.

**Command:**

```bash
grep -rn "claudecode\|claude-agent-sdk-go" internal/runner/monitor.go internal/runner/monitor_test.go
echo "exit=$?"
```

**Result summary:** No matches; grep exits 1 (no match found).

```text
exit=1
```

## Artifact: Build, vet, and full test suite

**What it proves:** The rewrite compiles cleanly, passes `go vet`, and does
not regress any existing package, including a race-detector run of the
packages `docs/TESTING.md` calls out for this kind of change.

**Why it matters:** This is the load-bearing-clean check for the whole
migration, not just the new tests.

**Command:**

```bash
go build ./... && go vet ./... && echo BUILD_VET_OK
go test ./internal/runner/... ./internal/sentinel/...
go test -race ./internal/engine ./internal/runner ./internal/harness
go test ./... 2>&1 | grep -v '^ok'
```

**Result summary:** Build and vet are clean; `internal/runner` and
`internal/sentinel` pass; the race run passes for all three packages; the
only failure in the full suite is `TestBoundaryBannerFoldsIntoClosingItemLineRange`
in `internal/tui/monitor`, which is a pre-existing TUI layout test failure
unrelated to this change (reproduced below).

```text
BUILD_VET_OK
ok  	jig/internal/runner	0.666s
ok  	jig/internal/sentinel	(cached)
ok  	jig/internal/engine	22.525s
ok  	jig/internal/runner	1.722s
ok  	jig/internal/harness	(cached)
?   	jig/internal/manifest	[no test files]
?   	jig/internal/review	[no test files]
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.00s)
FAIL	jig/internal/tui/monitor	2.003s
```

## Artifact: Pre-existing, unrelated failure confirmed on `main`

**What it proves:** `TestBoundaryBannerFoldsIntoClosingItemLineRange`'s
failure is not caused by this unit's changes.

**Why it matters:** A reviewer scanning the full test-suite output above
needs to know this failure was already present before Unit 2 touched
anything, so it does not block this task's completion.

**Command:**

```bash
git stash
go test ./internal/tui/monitor/... -run TestBoundaryBannerFoldsIntoClosingItemLineRange
git stash pop
```

**Result summary:** The same test fails identically with this unit's changes
stashed out (i.e. against `main` at commit `b5d0cb8`), confirming it is
pre-existing.

```text
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.00s)
FAIL	jig/internal/tui/monitor	0.569s
```

## Artifact: New/updated `MonitorAdapter` test suite

**What it proves:** Tolerant decoding, the single decode-retry rule (including
when it is `AcpHarness`'s own internal structured-output exhaustion that
triggers it), and the non-retry cases (open failure, timeout, dropped
connection, non-decode agent error) all behave as specified — each asserted
by exact `Open`-call count, not just pass/fail.

**Why it matters:** This is the direct evidence for the unit's two named
proof-artifact tests (`TestDecodeMonitorVerdict_TolerantOfSurroundingProse`
and `TestMonitorAdapter_DecodeRetryThenFailOpen`, implemented here as
`TestDecodeMonitorVerdictTolerantOfSurroundingProse` and
`TestMonitorAdapterDecodeRetryThenFailOpen`) and for the fail-open contract
the sentinel supervisor depends on.

**Command:**

```bash
go test ./internal/runner/... ./internal/sentinel/... -v -run 'TestMonitor|TestDecodeMonitor'
```

**Result summary:** All `MonitorAdapter`-related subtests pass, including
the retry-boundary table (`TestMonitorAdapterStrictVerdictsRetryThenFail`)
and the five non-decode-vs-decode branches in
`TestMonitorAdapterTimeoutAndConnectFailure`.

```text
=== RUN   TestMonitorAdapterIsolationAndLifecycle
--- PASS: TestMonitorAdapterIsolationAndLifecycle (0.00s)
=== RUN   TestMonitorAdapterStrictVerdictsRetryThenFail
    --- PASS: TestMonitorAdapterStrictVerdictsRetryThenFail/missing (0.00s)
    --- PASS: TestMonitorAdapterStrictVerdictsRetryThenFail/malformed (0.00s)
    --- PASS: TestMonitorAdapterStrictVerdictsRetryThenFail/extra (0.00s)
    --- PASS: TestMonitorAdapterStrictVerdictsRetryThenFail/wrong_type (0.00s)
    --- PASS: TestMonitorAdapterStrictVerdictsRetryThenFail/unknown_severity (0.00s)
    --- PASS: TestMonitorAdapterStrictVerdictsRetryThenFail/invalid_unflagged (0.00s)
=== RUN   TestDecodeMonitorVerdictTolerantOfSurroundingProse
--- PASS: TestDecodeMonitorVerdictTolerantOfSurroundingProse (0.00s)
=== RUN   TestMonitorAdapterDecodeRetryThenFailOpen
--- PASS: TestMonitorAdapterDecodeRetryThenFailOpen (0.00s)
=== RUN   TestMonitorAdapterTimeoutAndConnectFailure
    --- PASS: TestMonitorAdapterTimeoutAndConnectFailure/timeout (0.01s)
    --- PASS: TestMonitorAdapterTimeoutAndConnectFailure/open_failure (0.00s)
    --- PASS: TestMonitorAdapterTimeoutAndConnectFailure/closed_without_result (0.00s)
    --- PASS: TestMonitorAdapterTimeoutAndConnectFailure/agent_error_result (0.00s)
    --- PASS: TestMonitorAdapterTimeoutAndConnectFailure/structured_output_exhausted_retries_with_acp_harness_itself (0.00s)
PASS
ok  	jig/internal/runner	0.430s
=== RUN   TestMonitorStateRoundTripAndPersistenceOff
--- PASS: TestMonitorStateRoundTripAndPersistenceOff (0.03s)
=== RUN   TestMonitorStateRejectsCorruption
--- PASS: TestMonitorStateRejectsCorruption (0.01s)
=== RUN   TestMonitorRoster
    --- PASS: TestMonitorRoster/prompt-injection_finding (0.04s)
    --- PASS: TestMonitorRoster/stuck-loop_finding (0.04s)
    --- PASS: TestMonitorRoster/exfil-pattern_finding (0.04s)
PASS
ok  	jig/internal/sentinel	0.917s
```

## Reviewer Conclusion

`MonitorAdapter` no longer touches the Claude SDK anywhere: it opens a
`harness.Harness`-shaped session, drives it with jig's own `SessionSpec`
fields, decodes its structured output tolerantly (defense-in-depth against
an advisory, not wire-guaranteed, schema), and retries exactly once on a
fresh session for decode-shaped failures only. `internal/sentinel`'s
fail-open handling needed no changes — it still sees ordinary `Dispatch`
errors — and the full test suite (including the race-detector packages
`docs/TESTING.md` names for this change) is green apart from one confirmed
pre-existing, unrelated TUI test failure.
