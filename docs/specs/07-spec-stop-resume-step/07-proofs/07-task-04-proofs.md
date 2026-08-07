# Task 04 Proofs — Run.Resume: resume-as-continue with fresh-restart fallback

## Task Summary

This task proves `Run.Resume(stepID, message)` brings a stopped step back up,
reusing the existing `resumeSessions`/`stepMessage` + `WithResume`/
`WithContinueConversation` machinery. With a captured session id it continues the
conversation; without one it restarts fresh (documented degrade). Resume is not a
one-shot terminal transition — a step can be stopped, resumed, and stopped again.

## What This Task Proves

- Resume-as-continue: a stopped step with a session id is re-dispatched with
  `ResumeSessionID` and the operator message set, and runs to completion.
- Fresh-restart fallback: a stopped step with no session id is re-dispatched with
  an empty `ResumeSessionID` and still completes — a restart, not an error.
- Stop → resume → stop keeps the run alive and re-enters quiescence each time.

## Evidence Summary

`TestResumeContinuesSession`, `TestResumeWithoutSessionRestarts`, and
`TestStopResumeStopReentersQuiescence` pass under `-race`.

## Artifact: resume tests

**Command:** `go test ./internal/engine -race -run 'TestResumeContinuesSession|TestResumeWithoutSessionRestarts|TestStopResumeStopReentersQuiescence' -v`

**Artifact path:** `B2.0-resume-continues-session.txt`

**Result summary:** all PASS. The continue test asserts the second dispatch's
`ResumeSessionID == "sess-A"` and `Message == "keep going"`; the restart test
asserts an empty `ResumeSessionID`; the re-entry test asserts the run is never
`Done` and emits no `RunFinished` across stop→resume→stop.

```
--- PASS: TestResumeContinuesSession (0.00s)
--- PASS: TestResumeWithoutSessionRestarts (0.00s)
--- PASS: TestStopResumeStopReentersQuiescence (0.05s)
```

## Reviewer Conclusion

A stopped step continues its session when possible and restarts cleanly when not
— Success Metric 3 — and resume composes with stop indefinitely.
