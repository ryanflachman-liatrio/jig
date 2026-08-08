# Task 3.0 Proofs — Reset Execution: Rewind, Replay, State Reset, Re-Dispatch

## Task Summary

This task proves the live engine behavior: `Run.Reset(stepID)` correctly
rewinds the run branch to before the target's dependency closure, replays
independent survivor commits, clears stale per-step state, and bumps
`Generation` on every reset step — while leaving independent parallel branches
completely untouched.

## What This Task Proves

- `handleReset` is guarded to unfinished, quiescent runs with git persistence;
  settled and in-flight runs are silent no-ops.
- The journal receives `StepsReset` + `StepStatus(→pending)` events *before*
  any destructive git/file operation (crash-consistent ordering).
- The run branch is rewound to before the earliest closure commit; independent
  survivor commits are cherry-picked back.
- Reset steps have their step worktrees removed so re-dispatch creates a fresh
  one rooted at the post-reset run branch HEAD (prevents squash-merge divergence).
- Per-step derived outputs (`result.json`, `output.md`, `output.json`) are
  deleted; `transcript.jsonl` is kept (append-only; re-run adds a new generation).
- `Generation` is incremented for each reset step; `Attempt`, `Iteration`,
  `Result`, and all stale routing maps are zeroed.
- Independent steps retain their exact status, `Generation=0`, and commit SHA
  (the cherry-picked copy has the same tree diff as the original).
- Persistence-off (`runDir == ""`) is a clean no-op.

## Evidence Summary

All 4 targeted tests pass. The fan-out integration test verifies the end-to-end
reset+re-run with a real git repo; the linear-tip test verifies partial reset;
the guard tests verify the safety boundaries.

## Artifact: TestResetFanOut — fan-out reset with git branch verification

**What it proves:** `Run.Reset("a")` on a fan-out workflow (A→B→gate with D
independent) re-runs A, B, gate with `Generation=1` while preserving D's
`Generation=0` and ensuring D's code is on the post-reset run branch.

**Command:** `go test -v -count=1 -run TestResetFanOut ./internal/engine/`

**Key assertions:**
- D: `Status=Succeeded`, `Generation=0` (untouched)
- A, B: `Generation=1` after re-run
- Pre-reset run branch: 3 commits (a, b, d)
- Post-reset run branch: 3 commits (d cherry-picked, new a, new b)

**Result summary:** PASS.

## Artifact: TestResetLinearTip — linear-tip reset

**What it proves:** `Run.Reset("b")` on A→B→gate re-runs only B and gate;
A's `Generation=0` and commit SHA are unchanged.

**Command:** `go test -v -count=1 -run TestResetLinearTip ./internal/engine/`

**Result summary:** PASS — A's SHA unchanged, B gets a new commit, B's
`Generation=1`, A's `Generation=0`.

## Artifact: TestResetGuard and TestResetPersistenceOff — safety guards

**What it proves:** Reset is silently rejected on a settled run and on a
persistence-off run; no state changes and no panics.

**Command:** `go test -v -count=1 -run "TestResetGuard|TestResetPersistenceOff" ./internal/engine/`

**Result summary:** PASS — both guards confirmed.

## Raw output

```
=== RUN   TestResetFanOut
=== PAUSE TestResetFanOut
=== RUN   TestResetLinearTip
=== PAUSE TestResetLinearTip
=== RUN   TestResetGuard
--- PASS: TestResetGuard (0.00s)
=== RUN   TestResetPersistenceOff
--- PASS: TestResetPersistenceOff (0.01s)
=== CONT  TestResetFanOut
=== CONT  TestResetLinearTip
--- PASS: TestResetLinearTip (0.33s)
--- PASS: TestResetFanOut (0.49s)
PASS
ok  	jig/internal/engine	0.691s
```

## Reviewer Conclusion

The reset execution layer is fully operational. The git rewind + survivor
cherry-pick, state reset, worktree cleanup, and Generation bump all work
correctly end-to-end. The quiescence guard, persistence-off guard, and
journal-before-destruction ordering are all verified.
