# internal/engine

This package owns run state. `Manager` starts, reopens and tracks runs. Each
run has one scheduler goroutine (`scheduler.runLoop`), which is the only code
that mutates scheduler fields. Everything else talks to it with messages.
Read [Graph engineering](../../docs/GRAPH_ENGINEERING.md) before changing
scheduling, retries, routes, fan-out, reset or reopen.

## Ownership rules

- **Only the scheduler goroutine mutates scheduler state.** Workers, reporters
  and the `Run` API send a `schedMsg` on the inbox. A message that changes
  state implements `command` (`commands.go`) and does its work in
  `execute(s)`. Never take a lock to read scheduler maps from another
  goroutine. Publish a snapshot or event instead.
- **Never block the loop.** Run anything slow, such as agents, git, network
  or human waits, in a goroutine that reports back with a message. Pass the
  run's context, not `context.Background()`, so stop and cancel reach it.
- **Journal before publication.** Write an event to the journal before
  observers or snapshots can see it. If the journal write fails, the run stops
  progressing. Live deltas (agent text, tool calls) are lossy previews.
  Finalized content comes from `transcript.jsonl`.
- **Identity checks.** A late `stepDoneMsg` from a stopped, reset, retried or
  escalation-reaped execution must not change the replacement execution
  (see `stepRuntime.reaped`). Keep the attempt/generation/epoch checks
  whenever you change stop, retry, reset or reopen.

## Extension points that already exist

Use these rather than adding more `if st.Type == ...` branches:

| Concern | Where |
|---|---|
| Inbox message handling | `command` implementations in `commands.go` |
| What a step type can do, and how it dispatches | `stepKinds` (`strategies.go`): `dispatch`, `agentSession`, `humanGate`. `foreach` steps are handled earlier, in `dispatch` |
| Whether a pending step may start | `readinessChecks` (`ready.go`), run in order |
| What happens after a step finishes | `postExecHandler` chain (`handlers.go`), assembled in `newScheduler` |
| How a failure is handled | `failurePolicyStrategies` (`strategies.go`) |
| Dependency-graph walks | `stepGraph` (`graph.go`). Walk the precomputed dependents index; don't rescan until nothing changes |
| Out-of-process consumers | `Observer` (`observer.go`). Observers must not block |

A new step type needs a `workflow.StepType`, a `stepKinds` entry
(`TestStepKindsCoverExecutableTypes` checks this) and a runner.

## Adding a journaled event

1. Define the struct in `event.go` with the `isEvent()` marker, and add one
   `entry[T]("kind")` row to `journalEvents` in `journal.go`. Encoding and
   decoding both come from that table. Never change an existing kind
   string: kinds are on-disk format, and `journal_kinds_test.go` pins them.
2. Add the event to `allEventInstances` and `journalKinds`. If you forget,
   `TestAllEventInstancesCoversEveryEvent` fails.
3. If the event affects parked, resumable or interrupted state, fold it in
   `restoreUnfinishedCheckpoint` (`resume.go`). Also check
   `ClassifyUnfinished` (`unfinished.go`) and `reconcileInterruptedRun`
   (`replay.go`), and extend `unfinished_golden_test.go`.

Display replay skips an unknown kind. Resume (`readJournalStrict`) rejects it
with `ErrUnknownJournalKind`.

## Adding per-step scheduler state

Put per-step execution state in a field on `stepRuntime` (`runtime.go`) and
access it through `s.rt(stepID)`. Don't add another `map[string]X` to
`scheduler`. A reset replaces the struct with `afterReset()`, which keeps only
the fields it copies explicitly. So a new field is cleared on reset unless you
carry it over and list it in `runtimeRetainedOnReset`
(`reset_state_test.go`).

A step-keyed map that has to stay on `scheduler` (because code iterates over
it or replaces it wholesale) must be cleared in `handleReset` or listed in
`retainedOnReset`. `TestHandleResetClearsEveryStepKeyedMap` fails until you
make that choice.

Capacity is charged through `acquireSlots`/`releaseSlots` (`capacity.go`).
Every path that ends a dispatched worker, including failures before the
worker starts, calls `releaseSlots` exactly once.

## Adding a parked status

A parked status keeps a run alive while nothing is runnable. Add the constant
to `step.Status`, then put it in exactly one of `IsInFlight`, `IsParked` or
`IsTerminal`. `TestStatusClassesPartitionEveryStatus` (in `internal/step`)
fails until you do. Liveness (`anyPendingRunnable`) and crash classification
(`ClassifyUnfinished`) read those methods, so they need no change.

Reopen needs one explicit case in each of `checkpointFold.classifyPark` and
`rehydrateParks`. Both return an error for a parked status they don't handle.

## Errors and side effects

- Wrap with `%w` and keep the cause. Do not flatten an error into a string
  with `err.Error()` when a caller could branch on it.
- Check the errors from git cleanup and file writes. If cleanup really is
  best-effort, emit a `RunError` or a comment saying why ignoring the error
  is safe.
- The engine runs git only through the helpers in `worktree.go`. It does not
  import runner, harness or Bubble Tea.

## Files

Each file owns one concern. Put new code in the file for its concern, and
start a new file rather than growing `engine.go`.

| File | Owns |
|---|---|
| `engine.go` | `scheduler`, `newScheduler`, the run loop, `nextReady` |
| `manager.go`, `run.go` | `Manager` (start, subscribe, registry) and the `Run` handle API |
| `commands.go` | Inbox message types and their `execute` methods |
| `runtime.go`, `capacity.go`, `graph.go` | Per-step state, slot accounting, DAG walks |
| `ready.go`, `conditions.go` | Readiness checks, liveness, guard evaluation, gates |
| `worker.go`, `handlers.go`, `strategies.go` | Dispatch, the post-exec chain, step kinds, failure policy |
| `validation.go`, `validation_process_*.go` | Async gate ownership, continuation, bounded diagnostics, subprocess cancellation |
| `reset.go`, `loop.go` | Operator reset and recovery; route intents and coalesced loops |
| `integration.go`, `worktree.go`, `execution.go` | Run branch, conflicts, final merge, workspaces |
| `review.go`, `interaction.go`, `security.go` | Review gates; questions, input, stop and resume; security findings |
| `publish.go`, `event.go`, `journal.go` | Transitions, journaling, event fan-out, and the event registry |
| `resume.go`, `checkpoint_fold.go`, `parks.go`, `replay.go`, `fold.go`, `unfinished.go` | Reopen, crash recovery, and journal readers |
| `fanout.go`, `workflow_snapshot.go` | `foreach` families; the persisted workflow snapshot |

## Tests

```bash
go test ./internal/engine -count=1
go test -race ./internal/engine
```

Tests are in package `engine`. Use the shared `testExec` in
`engine_fakes_test.go` for scripted per-step results (`outcomes`), custom
logic (`respond`), and recorded requests (`requestsFor`, `lastInputs`,
`callCount`). Write a bespoke fake only when a test needs `Reporter`
callbacks, a capability interface such as `SessionResumeSupport`, or a
dispatch held open on a channel. Lifecycle changes need an
adversarial case: a late result, a journal write failure, a crash between
journal and publication, or a missing artifact.
