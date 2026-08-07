# Manual reset rewinds the run branch and replays survivors, scoped to the dependency closure

Status: proposed.

An operator may **reset** a run to an earlier step ("the implementation is wrong — go
back to planning"). Reset must undo both workflow state *and* the code changes made
since the target, while sparing independent parallel branches (the transitive-`depends_on`
scope).

We decided reset is a **git rewind plus survivor replay over the target's dependency
closure**. Compute the reset set = the target ∪ its transitive `depends_on` closure;
`git reset --hard` the run branch to the commit **just before the earliest reset-set
commit**; then **cherry-pick, in original order, every later commit that is *not* in the
reset set** (the independent "survivors"); and return the reset set to `pending` to
re-run. In a linear workflow the reset set is a contiguous tail, so this degenerates to a
plain rewind with nothing to replay. Reset builds on the per-run commit history from
[ADR 0007](0007-run-integration-branch-model.md).

Reset is a **quiescent** operation — it never mutates while a worker is live. A run
reaches quiescence at a gate or by **stopping** the running step. Reset is available only
on an **unfinished** run; a fully-settled run is locked (reopening a finished run from
disk to reset it is a deferred follow-up).

## Why these choices

- **Rewind + replay, not reset-by-time.** A bare `git reset --hard` to the target's
  commit discards independent parallel work merged afterward. Replaying the survivors
  preserves exactly the branches that do not depend on the target.
- **The reset event is a journaled *audit* record, not a replay-correctness mechanism.**
  `StepStatus` transitions are already journaled, so folding the journal reconstructs
  post-reset state from the status stream alone. `StepsReset{target, closure}` records the
  operator's *chosen target and blast radius* — provenance the status stream cannot
  express — and is written *before* any destructive git/file operation so a crash leaves
  disk and journal consistent.
- **A `Generation` counter, not `Attempt`.** The automatic-retry budget is keyed on
  `state.Attempt`, so reusing `Attempt` to mark a manual re-run would silently spend that
  budget. `Generation` is a third provenance axis (alongside `Attempt` = failure-retries,
  `Iteration` = loop passes) that marks manual re-runs and gives the transcript a legible
  boundary.

## Consequences / trade-offs

- **Cherry-pick conflicts** arise when a survivor touched the same lines as a dropped
  commit; they route to the same integration-conflict gate — no auto-resolution.
- Re-runs are non-deterministic (agents), so a reset produces *new* downstream output, by
  design.
- Undoing changes already landed on the user's working branch by the **final** gated
  merge is out of scope — that is history the user owns.

## Rejected alternatives

- **`git reset --hard` by time only.** Over-reverts independent parallel branches.
- **Delete `steps/<id>/` and rely on file deletion for state.** Breaks never-truncate and
  desyncs disk from the journal fold.
- **Bump `Attempt` to mark the re-run.** Corrupts the automatic-retry budget.
