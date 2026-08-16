package engine

import (
	"context"

	"jig/internal/step"
	"jig/internal/workflow"
)

// stepDispatchStrategy is the Strategy pattern's contract for starting a step
// nextReady() has already confirmed is dependency-ready and guard-passed.
// Step types dispatch differently — agent/command steps start a worker
// goroutine and occupy an inFlight slot, while review steps park on human
// input and never touch a worker — so this replaces what was an inline
// `if wfStep.Type == workflow.StepReview` branch with a lookup table any new
// step type registers into instead of editing.
type stepDispatchStrategy interface {
	dispatch(s *scheduler, ctx context.Context, st *workflow.Step)
}

// agentDispatchStrategy and commandDispatchStrategy currently share identical
// scheduler-level behavior: the type-specific work (agent SDK call vs. shell
// exec) happens inside the registered engine.Executor (see runner.Mux), not
// here. They are still distinct strategy values — not one shared value for
// both map keys — so a future scheduler-level difference between the two
// (e.g. agent-only worktree handling) has an obvious extension point.
type agentDispatchStrategy struct{}

func (agentDispatchStrategy) dispatch(s *scheduler, ctx context.Context, st *workflow.Step) {
	s.dispatchWorker(ctx, st)
}

type commandDispatchStrategy struct{}

func (commandDispatchStrategy) dispatch(s *scheduler, ctx context.Context, st *workflow.Step) {
	s.dispatchWorker(ctx, st)
}

// reviewDispatchStrategy wraps the existing dispatchReview rather than
// duplicating it; it ignores ctx because a review step never starts a
// cancellable worker goroutine.
type reviewDispatchStrategy struct{}

func (reviewDispatchStrategy) dispatch(s *scheduler, _ context.Context, st *workflow.Step) {
	s.dispatchReview(st)
}

// stepDispatchStrategies is the extension point that replaced the inline
// per-type branch: adding a new workflow.StepType means registering a new
// entry here, not editing dispatch().
var stepDispatchStrategies = map[workflow.StepType]stepDispatchStrategy{
	workflow.StepAgent:   agentDispatchStrategy{},
	workflow.StepCommand: commandDispatchStrategy{},
	workflow.StepReview:  reviewDispatchStrategy{},
}

// failurePolicyStrategy is the Strategy pattern's contract for what happens
// to a step after execution or gate failure, keyed by its on_failure policy.
// applyFailurePolicy looks up the strategy for wfStep's policy instead of
// branching inline, so a new on_failure value is a new map entry rather than
// a new switch case.
type failurePolicyStrategy interface {
	// apply carries out stepID's failure policy. from is its status before
	// this failure was recorded (state.Result already carries the failure).
	apply(s *scheduler, stepID string, wfStep *workflow.Step, from step.Status)
}

// retryFailureStrategy resets the step to pending (up to MaxRetries attempts)
// so the main scheduler loop redispatches it; once exhausted it hands off to
// the human recovery gate rather than failing the run outright.
type retryFailureStrategy struct{}

func (retryFailureStrategy) apply(s *scheduler, stepID string, wfStep *workflow.Step, from step.Status) {
	maxRetries := 1
	if wfStep != nil && wfStep.MaxRetries > 0 {
		maxRetries = wfStep.MaxRetries
	}
	state := s.states[stepID]
	if state.Attempt < maxRetries {
		state.Attempt++
		// Reset to pending so the main scheduler loop picks it up again and
		// calls dispatch(), which will increment inFlight.  handle() already
		// decremented inFlight at its top, so the count stays correct.
		s.transition(stepID, from, step.StatusPending)
		return
	}
	// Exhausted automatic retries — hand off to the human recovery gate rather
	// than tearing down the run.
	s.enterRecovery(stepID)
}

// continueFailureStrategy marks the step failed but does not cancel the run;
// dependents with depsReady() treat this step as a "satisfied" node and
// proceed.
type continueFailureStrategy struct{}

func (continueFailureStrategy) apply(s *scheduler, stepID string, _ *workflow.Step, from step.Status) {
	s.transition(stepID, from, step.StatusFailed)
}

// abortFailureStrategy pauses for a human recovery decision (retry / resume /
// abort) instead of the old hair-trigger s.cancel(): the run and any
// in-flight sibling steps stay alive. Choosing RecoverAbort at the gate
// performs the real teardown. This is also the fallback for an unrecognised
// on_failure value, matching applyFailurePolicy's prior default case.
type abortFailureStrategy struct{}

func (abortFailureStrategy) apply(s *scheduler, stepID string, _ *workflow.Step, _ step.Status) {
	s.enterRecovery(stepID)
}

// failurePolicyStrategies is the extension point that replaced the inline
// switch on workflow.FailurePolicy: adding a new policy value means
// registering a new entry here, not editing applyFailurePolicy(). Lookup
// misses (unrecognised policy) fall back to abortFailureStrategy, matching
// the prior switch's default case.
var failurePolicyStrategies = map[workflow.FailurePolicy]failurePolicyStrategy{
	workflow.FailRetry:    retryFailureStrategy{},
	workflow.FailContinue: continueFailureStrategy{},
	workflow.FailAbort:    abortFailureStrategy{},
}
