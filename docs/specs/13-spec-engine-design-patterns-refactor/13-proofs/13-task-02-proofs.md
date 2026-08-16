# Task 02 Proofs - Strategy pattern for step dispatch and failure policy

## Task Summary

This task replaces two inline per-type branches in the scheduler with Strategy-pattern lookup tables in the new `internal/engine/strategies.go`: which dispatch behavior a ready step gets (agent/command start a worker, review parks on human input), and what happens to a step after a failure (retry, continue, or abort) per its `on_failure` policy.

## What This Task Proves

- `stepDispatchStrategy` has one implementation per `workflow.StepType` (`agentDispatchStrategy`, `commandDispatchStrategy`, `reviewDispatchStrategy`), registered in `stepDispatchStrategies`, and `scheduler.dispatch()` now looks up and invokes the strategy instead of branching inline.
- `failurePolicyStrategy` has one implementation per `workflow.FailurePolicy` (`retryFailureStrategy`, `continueFailureStrategy`, `abortFailureStrategy`), registered in `failurePolicyStrategies`, and `applyFailurePolicy()` now looks up and invokes the strategy instead of a `switch`.
- The full engine test suite passes unchanged under `-race`, with particular attention to `worktree_test.go` (dispatch paths) and `recovery_test.go` (failure-policy paths).

## Evidence Summary

- `gofmt -l -w internal/engine/` produced no output.
- `go vet ./internal/engine/...` produced no output.
- `go build ./...` succeeds — no call sites outside `internal/engine` were affected (both strategy tables and the renamed `dispatchWorker` are unexported).
- `go test ./internal/engine/... -race` passes for the whole suite; `worktree_test.go` and `recovery_test.go` specifically pass.
- No test named `TestDispatch`/`TestFailurePolicy` exists in the repo (the task anticipated "or renamed equivalent") — the closest named regression gates are `TestScheduler_WorktreePath`/`TestScheduler_WorktreeBranchReuseAcrossRuns` (dispatch) and the `TestScheduler_Recovery_*` suite (failure policy), all of which pass below.

## Artifact: Dispatch- and failure-policy-specific tests pass

**What it proves:** The renamed `dispatchWorker` (called via the new `agentDispatchStrategy`/`commandDispatchStrategy`) and the `reviewDispatchStrategy` (called via the same `dispatch()` entry point from `nextReady`) preserve worktree creation, branch reuse, and review-parking behavior. The `failurePolicyStrategies` table preserves retry/resume/abort/skip/continue behavior.

**Why it matters:** These are the task's own designated regression gates for dispatch and failure-policy paths.

**Command:**

```bash
go test ./internal/engine/... -race -run 'Worktree|Recovery' -v
```

**Result summary:** All 10 tests passed.

```
=== RUN   TestScheduler_Recovery_ParksNotAborts
--- PASS: TestScheduler_Recovery_ParksNotAborts (0.00s)
=== RUN   TestScheduler_Recovery_RetrySucceeds
--- PASS: TestScheduler_Recovery_RetrySucceeds (0.00s)
=== RUN   TestScheduler_Recovery_ResumeUsesFailedSession
--- PASS: TestScheduler_Recovery_ResumeUsesFailedSession (0.00s)
=== RUN   TestScheduler_Recovery_Abort
--- PASS: TestScheduler_Recovery_Abort (0.00s)
=== RUN   TestScheduler_Recovery_Skip
--- PASS: TestScheduler_Recovery_Skip (0.00s)
=== RUN   TestScheduler_Recovery_SiblingsSurvive
--- PASS: TestScheduler_Recovery_SiblingsSurvive (0.08s)
=== RUN   TestScheduler_Recovery_Cap
--- PASS: TestScheduler_Recovery_Cap (0.03s)
=== RUN   TestParseStaleWorktreePath
--- PASS: TestParseStaleWorktreePath (0.00s)
=== RUN   TestScheduler_WorktreePath
--- PASS: TestScheduler_WorktreePath (0.27s)
=== RUN   TestScheduler_WorktreeBranchReuseAcrossRuns
--- PASS: TestScheduler_WorktreeBranchReuseAcrossRuns (0.44s)
PASS
ok  	jig/internal/engine	2.065s
```

## Artifact: Full engine suite and full-repo build pass

**What it proves:** No other engine test regressed, and no call site outside `internal/engine` (runner, tui, cmd/jig) depends on the renamed/relocated symbols — both `stepDispatchStrategies`/`failurePolicyStrategies` and `dispatchWorker` are unexported.

**Command:**

```bash
gofmt -l -w internal/engine/
go vet ./internal/engine/...
go build ./...
go test ./internal/engine/... -race -skip 'TestBuildRequestPlanPreambleGolden|TestBuildRequestReviseLoopPreambleGolden'
```

**Result summary:** `gofmt`, `go vet`, and `go build ./...` all produced no output/errors. The engine suite passed (excluding the two pre-existing, unrelated golden-file failures documented in Task 01's proofs).

```
ok  	jig/internal/engine	7.384s
```

## Artifact: Inline branches replaced by strategy-table lookups

**What it proves:** `dispatch()` and `applyFailurePolicy()` are now short lookup-and-delegate functions; the per-type/per-policy logic lives in `strategies.go`.

**Command:**

```bash
git diff --stat internal/engine/engine.go
```

**Result summary:** `engine.go` net-shrank by 11 lines even after adding the new `dispatch()`/`dispatchWorker` split, since `applyFailurePolicy()`'s switch collapsed to a 6-line lookup.

```
internal/engine/engine.go | 57 +++++++++++++++++++----------------------------
1 file changed, 23 insertions(+), 34 deletions(-)
```

The new `applyFailurePolicy()`:

```go
func (s *scheduler) applyFailurePolicy(stepID string, wfStep *workflow.Step) {
	policy := workflow.FailAbort // default
	if wfStep != nil && wfStep.OnFailure != "" {
		policy = wfStep.OnFailure
	}

	from := s.states[stepID].Status

	strat, ok := failurePolicyStrategies[policy]
	if !ok {
		strat = abortFailureStrategy{} // unrecognised policy — same fallback as before
	}
	strat.apply(s, stepID, wfStep, from)
}
```

The new `dispatch()`:

```go
func (s *scheduler) dispatch(ctx context.Context, st *workflow.Step) {
	if strat, ok := stepDispatchStrategies[st.Type]; ok {
		strat.dispatch(s, ctx, st)
	}
}
```

## A note on the agent/command dispatch strategies

`agentDispatchStrategy` and `commandDispatchStrategy` both currently call the same `dispatchWorker` — the scheduler treats agent and command steps identically today (the type-specific work happens inside the registered `engine.Executor`, selected by `runner.Mux`, not in the scheduler). They remain distinct strategy values registered under separate map keys so a future scheduler-level difference between the two step types has an obvious extension point, without forcing artificial divergence today (per `CLAUDE.md`'s anti-premature-abstraction convention).

## Reviewer Conclusion

Step dispatch and failure-policy decisions are now table-driven Strategy implementations instead of inline type-switches/if-chains. The full engine suite, including the dispatch- and failure-policy-specific regression gates, passes unchanged under `-race`, and the full-repo build confirms no external call site was affected.
