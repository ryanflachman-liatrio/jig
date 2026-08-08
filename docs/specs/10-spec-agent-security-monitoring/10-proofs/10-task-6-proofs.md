# Task 6.0 Proofs — Security pane and human escalation

## Task Summary

This task makes security findings visible in the TUI and routes critical findings
to the existing recovery gate. Two key design decisions:

- **Escalation via `securityFindingMsg` inbox message**: `SecurityFinding` events
  already fan out to TUI subscribers via `fanOutCtrl`. For the scheduler to
  react (escalate to recovery), the `reporter.ev` closure also sends a
  `securityFindingMsg` to the scheduler inbox — the same pattern as
  `AgentQuestion` (fanOut + inbox notify). This keeps the must-not-drop guarantee
  and the scheduler reaction in one code path.

- **Early-exit in `stepDoneMsg` for `StatusAwaitingRecovery`**: When a critical
  finding parks a step that is still running, the step's worker eventually sends
  a `stepDoneMsg`. The handler checks `state.Status == StatusAwaitingRecovery`
  and keeps the step parked (records the result for a possible resume, but does
  not re-process through the post-exec chain or failure policy). This prevents
  a completed worker from accidentally un-parking the step.

## What This Task Proves

- `theme.Security.*` styles are built from existing `danger`/`warning`/`fgMuted`
  tokens — no bare hex colors.
- `SecurityFinding` ctrl events populate `monitorModel.secFindings` by reading
  from `findings.jsonl` (file is truth); the Security pane renders verbatim (not
  glamour) so redacted previews like `[aws-key:…MPLE]` display literally.
- The Security pane is only shown when at least one finding exists.
- Critical findings park the running step at `StatusAwaitingRecovery` and emit
  `RecoveryRequest`. `Run.Recover(abort)` tears down cleanly.
- Non-critical (high/medium/low) findings produce no `RecoveryRequest`.
- Duplicate fingerprints escalate at most once.
- Terminal and already-parked steps are not re-processed by security escalation.

## Evidence Summary

- `TestSecurityPane` (tui): PASS — 4 sub-cases: high row, critical row, header,
  empty-pane guard.
- `TestCriticalEscalation` (engine): PASS — 3 sub-cases: park+abort, non-critical,
  duplicate fingerprint.
- Full suite: 334 tests, 0 failures (9 new tests added).
- `gofmt` clean; `go vet` clean.

## Artifact: TestSecurityPane

**What it proves:** `SecurityFinding` events read full findings from disk and render
in the Security pane by severity via `theme.Security.*` styles (verbatim, not glamour).

**Command:**
```
go test ./internal/tui/ -v -count=1 -run "TestSecurityPane"
```

**Result summary:** High row contains "HIGH", monitor name, and redacted preview
`[aws-key:…MPLE]` verbatim. Critical row contains "CRITICAL" and "denied-shell".
Header "Security findings" renders. Empty model returns `""`.

```
=== RUN   TestSecurityPane
=== RUN   TestSecurityPane/high_severity_row_rendered
=== RUN   TestSecurityPane/critical_severity_row_rendered
=== RUN   TestSecurityPane/header_rendered
=== RUN   TestSecurityPane/empty_when_no_findings
--- PASS: TestSecurityPane (0.00s)
    --- PASS: TestSecurityPane/high_severity_row_rendered (0.00s)
    --- PASS: TestSecurityPane/critical_severity_row_rendered (0.00s)
    --- PASS: TestSecurityPane/header_rendered (0.00s)
    --- PASS: TestSecurityPane/empty_when_no_findings (0.00s)
PASS
ok  	jig/internal/tui	0.276s
```

## Artifact: TestCriticalEscalation

**What it proves:** Critical findings park running steps at `StatusAwaitingRecovery`;
non-critical findings are inert; duplicate fingerprints are rate-limited.

**Command:**
```
go test ./internal/engine/ -v -count=1 -run "TestCriticalEscalation" -timeout 30s
```

**Result summary:** All 3 sub-cases pass. Critical finding → `RecoveryRequest` arrives,
step is `StatusAwaitingRecovery`; `Run.Recover(abort)` cleans up. Non-critical high →
no `RecoveryRequest`, run finishes normally. Duplicate fingerprint → exactly 1 park.

```
=== RUN   TestCriticalEscalation
=== RUN   TestCriticalEscalation/critical_finding_on_running_step_→_StatusAwaitingRecovery_→_abort_cleans_up
=== RUN   TestCriticalEscalation/non-critical_finding_→_no_RecoveryRequest
=== RUN   TestCriticalEscalation/duplicate_fingerprints_→_park_once
--- PASS: TestCriticalEscalation (0.05s)
    --- PASS: TestCriticalEscalation/critical_finding_on_running_step_→_StatusAwaitingRecovery_→_abort_cleans_up (0.00s)
    --- PASS: TestCriticalEscalation/non-critical_finding_→_no_RecoveryRequest (0.00s)
    --- PASS: TestCriticalEscalation/duplicate_fingerprints_→_park_once (0.05s)
PASS
ok  	jig/internal/engine	0.238s
```

## Reviewer Conclusion

The security layer is now end-to-end observable and actionable:
- `theme.Security.*` styles render severity rows from existing palette tokens.
- `monitorModel.secFindings` populated from `findings.jsonl`; rendered verbatim.
- Critical findings escalate running steps to the existing recovery gate.
- Non-critical findings and duplicate fingerprints are silently recorded.
- `securityFindingMsg` inbox delivery + `StatusAwaitingRecovery` early-exit guard
  handle the race between worker completion and security escalation correctly.
