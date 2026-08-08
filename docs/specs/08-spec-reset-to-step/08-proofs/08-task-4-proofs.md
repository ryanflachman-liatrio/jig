# Task 4.0 Proofs — TUI: Stop, Reset Trigger, Confirmation, Footer Hints

## Task Summary

This task wires the operator surface for spec 08 Unit C4: three new key
bindings (stop/reset/resume) in the monitor's Steps panel, a confirmation gate
entry that names the blast radius for mid-graph resets, and footer hints gated
by step eligibility. The root model routes the messages to the engine API.

## What This Task Proves

- `s` on a running step emits `stopStepMsg`, which the root routes to `Run.Stop`.
- `r` on a terminal/stopped step emits `requestResetMsg`; the root resolves the
  closure via `Run.ClosureOf` and either calls `Run.Reset` directly (linear tip)
  or injects a `showResetConfirmMsg` into the monitor.
- `showResetConfirmMsg` creates an `inputKindResetConfirm` gate entry that
  names all steps in the closure; `y` emits `resetStepMsg`, `n`/`esc` cancels.
- `ctrl+r` on a stopped step emits `resumeStepMsg`, routed to `Run.Resume`.
- Footer hints for stop/reset/resume are advertised only when the selected step
  is in the correct state (running / terminal-or-stopped / stopped).
- `r` and `s` on ineligible steps (running for reset, non-running for stop)
  produce no message.
- The settled-run guard (`m.done == true`) suppresses all three actions.

## Evidence Summary

All 3 targeted TUI tests pass. The tests drive the model with synthetic
snapshots and key presses, asserting on the emitted `tea.Cmd` results.

## Artifact: TestResetConfirmation

**What it proves:** Mid-graph reset confirmation gate works end-to-end:
`showResetConfirmMsg` creates the entry, tab navigates to gate focus, `y`
emits `resetStepMsg`, `n` clears the entry without emitting.

**Command:** `go test -v -count=1 -run TestResetConfirmation ./internal/tui/`

**Result summary:** PASS.

## Artifact: TestResetLinearTipTUI

**What it proves:** `r` on a terminal step emits `requestResetMsg`; `r` on a
running step and on a settled run emits nothing (eligibility guard).

**Command:** `go test -v -count=1 -run TestResetLinearTipTUI ./internal/tui/`

**Result summary:** PASS.

## Artifact: TestStopKey

**What it proves:** `s` on a running step emits `stopStepMsg`; `s` on a
pending step emits nothing.

**Command:** `go test -v -count=1 -run TestStopKey ./internal/tui/`

**Result summary:** PASS.

## Raw output

```
=== RUN   TestResetConfirmation
--- PASS: TestResetConfirmation (0.00s)
=== RUN   TestResetLinearTipTUI
--- PASS: TestResetLinearTipTUI (0.00s)
=== RUN   TestStopKey
--- PASS: TestStopKey (0.00s)
PASS
ok  	jig/internal/tui	0.251s
```

## Reviewer Conclusion

The full operator surface for spec 08 C4 is wired and tested. Stop, reset, and
resume keys work correctly in the Steps panel with proper eligibility gating.
The blast-radius confirmation gate follows the existing gate/entry pattern and
defaults to No (`n`/`esc`), matching the spec requirement.
