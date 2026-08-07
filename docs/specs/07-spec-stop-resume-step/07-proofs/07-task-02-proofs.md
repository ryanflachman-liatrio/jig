# Task 02 Proofs — Partial work preserved on cancel + StatusStopped

## Task Summary

This task proves a stopped step's partial work is preserved (diff captured on
cancel, transcript appended-to on resume) and introduces `step.StatusStopped`, a
parked-but-alive status eligible for resume and reset.

## What This Task Proves

- Diff capture runs **on cancel** — a stopped mutating step's captured diff is
  non-empty on disk, not discarded.
- The cancel-capture path no-ops cleanly with persistence off (`runDir == ""`).
- `StatusStopped` exists, stringifies to `"stopped"`, and is distinct from every
  other status.
- The transcript is a log a resume **appends** to, never truncates.

## Evidence Summary

`TestStoppedStepCapturesDiff`, `TestStopPersistenceOff`, the `internal/step`
status tests, and `TestCaptureStream_ResumeAppends` all pass under `-race`.

## Artifact: capture-on-cancel + status tests

**What it proves:** partial diff captured on cancel; persistence-off no-op;
`StatusStopped` present and distinct.

**Command:** `go test ./internal/engine -race -run 'TestStoppedStepCapturesDiff|TestStopPersistenceOff' -v` and `go test ./internal/step -race -v`

**Artifact path:** `B1.0-partial-work-preserved.txt`

**Result summary:** all PASS. `TestStoppedStepCapturesDiff` asserts a non-empty
`s.diffs["a"]` after a stopped step's worktree held a modification, plus cleanup
of `inFlight`/`stepCancels`.

```
--- PASS: TestStoppedStepCapturesDiff (0.15s)
--- PASS: TestStopPersistenceOff (0.00s)
--- PASS: TestStatusValues
--- PASS: TestStatusStoppedDistinct
```

## Artifact: transcript append (resume invariant)

**What it proves:** a resumed turn is appended after the preserved first turn.

**Command:** `go test ./internal/runner -race -run TestCaptureStream_ResumeAppends -v`

**Artifact path:** `B2.0-resume-continues-session.txt` (append section)

**Result summary:** PASS — entry count grows, entry 0 is still `"turn one"`, and
the resumed human message + answer appear afterward. Backed by
`transcript.Create`'s `O_APPEND` open mode.

## Reviewer Conclusion

A stop never discards work: the partial diff is captured on cancel and the
transcript is preserved and appended-to on resume — Success Metric 2.
