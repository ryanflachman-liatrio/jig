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
| What happens after a step finishes | `postExecHandler` chain (`handlers.go`), assembled in `newScheduler` |
| How a step kind is dispatched | `stepDispatchStrategies` (`strategies.go`). `foreach` steps are handled earlier, in `dispatch`, before the strategy lookup |
| How a failure is handled | `failurePolicyStrategies` (`strategies.go`) |
| Out-of-process consumers | `Observer` (`observer.go`). Observers must not block |

## Adding a journaled event

1. Define the struct in `event.go` and give it the `isEvent()` marker.
2. Add its kind string to `eventKind` **and** add a decoder to `decoders` in
   `journal.go`. The two must agree. Display replay skips an unknown kind,
   and resume (`readJournalStrict`) rejects it with `ErrUnknownJournalKind`.
   So if you miss the decoder, every run that records the event fails to
   reopen.
3. If the event affects parked, resumable or interrupted state, fold it in
   `restoreUnfinishedCheckpoint` (`resume.go`). Check `ClassifyUnfinished`
   (`unfinished.go`) and `reconcileInterruptedRun` (`replay.go`) too, since
   each of those scans events on its own.
4. Done when `journal_test.go` round-trips the event and `resume_test.go` or
   `replay_test.go` covers reopen after a crash in the window the event
   records.

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

A parked status keeps a run alive with nothing runnable. Update these places
together:

- the parked checks in `anyPendingRunnable`
- `ClassifyUnfinished`
- the status switch in `restoreUnfinishedCheckpoint`
- the `reopen` switch in `rehydrateParks`
- the matching `WaitKind` in `observer.go`

None of these switches fails loudly on an unknown status.

## Errors and side effects

- Wrap with `%w` and keep the cause. Do not flatten an error into a string
  with `err.Error()` when a caller could branch on it.
- Check the errors from git cleanup and file writes. If cleanup really is
  best-effort, emit a `RunError` or a comment saying why ignoring the error
  is safe.
- The engine runs git only through the helpers in `worktree.go`. It does not
  import runner, harness or Bubble Tea.

## Files

`engine.go` is oversized. When you add code, put it in a file named after the
concern it belongs to (such as fan-out, worktree or security) rather than
making `engine.go` longer. Moving existing code out of it is welcome as a
separate, behavior-preserving change.

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
