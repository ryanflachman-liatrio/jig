package engine

import (
	"context"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// stepKind is how the scheduler treats one executable workflow step type.
// Branch on its capabilities rather than comparing st.Type, so a new step type
// is one stepKinds entry plus its runner. Subworkflow steps are expanded away
// at load time and never reach the engine.
type stepKind struct {
	// dispatch starts a step nextReady has confirmed is dependency-ready and
	// guard-passed.
	dispatch func(s *scheduler, ctx context.Context, st *workflow.Step)
	// agentSession: the step runs a backend agent session. That makes it
	// resumable, Tier-2 security monitored, eligible for injected workflow
	// context, and resolvable by the agent conflict resolver.
	agentSession bool
	// humanGate: the step parks on an operator verdict and never starts a
	// worker.
	humanGate bool
}

func dispatchToWorker(s *scheduler, ctx context.Context, st *workflow.Step) {
	s.dispatchWorker(ctx, st)
}

// stepKinds registers every executable step type. Agent, command and check
// steps share scheduler behavior; the type-specific work happens inside the
// Executor (see runner.Mux). It is filled in init because dispatch reaches
// code that reads it back.
var stepKinds map[workflow.StepType]stepKind

func init() {
	stepKinds = map[workflow.StepType]stepKind{
		workflow.StepAgent:   {dispatch: dispatchToWorker, agentSession: true},
		workflow.StepCommand: {dispatch: dispatchToWorker},
		workflow.StepCheck:   {dispatch: dispatchToWorker},
		workflow.StepReview: {
			dispatch:  func(s *scheduler, _ context.Context, st *workflow.Step) { s.dispatchReview(st) },
			humanGate: true,
		},
	}
}

// hasAgentSession reports whether st runs a backend agent session.
func hasAgentSession(st *workflow.Step) bool {
	return st != nil && stepKinds[st.Type].agentSession
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

// defaultMaxRetries applies when a step uses on_failure = "retry" without
// setting max_retries.
const defaultMaxRetries = 1

func (retryFailureStrategy) apply(s *scheduler, stepID string, wfStep *workflow.Step, from step.Status) {
	maxRetries := defaultMaxRetries
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
