# Task 01 Proofs — Per-step cancellation reaches quiescence

## Task Summary

This task proves an operator can stop **one** running step without ending the
run. Each worker now gets its own child context (reversing the single run-context
at `engine.go:99`); `Run.Stop(stepID)` → `stopMsg` → `handleStop` cancels only
that worker; and the run loop treats a deliberately-stopped step as
quiescent-but-alive rather than end-of-run.

## What This Task Proves

- A stop is surgical: the targeted step parks at `StatusStopped` while a parallel
  sibling runs to completion.
- The run stays alive and quiescent after a stop — no `RunFinished` is emitted.
- Stopping a step with no live worker is a documented no-op (guard path).

## Evidence Summary

`TestStopOneStep` and `TestStopNonRunningStep` pass under `-race`.

## Artifact: stop/quiescence tests

**What it proves:** stop cancels only the targeted worker; the run stays alive;
the guard path is a no-op.

**Command:** `go test ./internal/engine -race -run 'TestStopOneStep|TestStopNonRunningStep' -v`

**Artifact path:** `B1.0-stop-one-step.txt`

**Result summary:** both tests PASS. `TestStopOneStep` asserts step `a`→stopped,
sibling `b`→succeeded, snapshot not Done, and no `RunFinished` for 50ms after the
stop. `TestStopNonRunningStep` asserts stopping an unknown id leaves the running
step untouched.

```
=== RUN   TestStopOneStep
--- PASS: TestStopOneStep (0.05s)
=== RUN   TestStopNonRunningStep
--- PASS: TestStopNonRunningStep (0.02s)
PASS
ok  	jig/internal/engine
```

## Reviewer Conclusion

Per-step stop is surgical and leaves the run quiescent-but-alive, satisfying
Success Metric 1 and the precondition Feature C's reset needs.
