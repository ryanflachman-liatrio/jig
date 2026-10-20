package engine

import (
	"context"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// readiness is one readiness check's verdict on a pending step.
type readiness int

const (
	readinessPass     readiness = iota // go on to the next check
	readinessSkip                      // leave or settle the step; scan on
	readinessDispatch                  // hand the step to a worker now
	readinessHalt                      // a journal write failed; stop scanning
)

// readinessChecks run in order on each pending static step during nextReady.
// The first non-pass verdict wins; a step that passes every check is
// dispatched. Order matters: waiting conditions come before guards, so a
// guard is only evaluated once the step's dependencies have settled.
var readinessChecks = []func(s *scheduler, ctx context.Context, st *workflow.Step) readiness{
	checkBudget,
	checkRetryBackoff,
	checkResources,
	checkDependencies,
	checkAutomaticCheckDefer,
	checkWhenGuard,
	checkAppliesWhen,
	checkUserInputs,
	checkHumanGate,
}

// checkBudget fails the step once the run's cost or token budget is spent.
func checkBudget(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	blocked, reason := s.budgetExhausted()
	if !blocked {
		return readinessPass
	}
	s.states[st.ID].Result = &step.Result{Status: step.StatusFailed, Err: reason}
	if !s.transition(st.ID, step.StatusPending, step.StatusFailed) {
		return readinessHalt
	}
	return readinessSkip
}

func checkRetryBackoff(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	if until := s.rt(st.ID).retryNotBefore; !until.IsZero() && time.Now().Before(until) {
		return readinessSkip
	}
	return readinessPass
}

func checkResources(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	if !s.resourceAvailable(st) {
		return readinessSkip
	}
	return readinessPass
}

func checkDependencies(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	if !s.depsReady(st) {
		return readinessSkip
	}
	return readinessPass
}

func checkAutomaticCheckDefer(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	if s.deferredByAutomaticCheckRoute(st) {
		return readinessSkip
	}
	return readinessPass
}

// checkWhenGuard skips a step whose when= guard is false and cascades the
// skip to its dependents.
func checkWhenGuard(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	if st.When == "" || s.guardHolds(st.When) {
		return readinessPass
	}
	if !s.transition(st.ID, s.states[st.ID].Status, step.StatusSkipped) {
		return readinessHalt
	}
	s.rt(st.ID).skippedByGuard = true
	s.cascadeSkip(st.ID)
	return readinessSkip
}

// checkAppliesWhen settles a non-applicable deterministic gate as successful
// evidence with a typed skip verdict, not a missing command or a failed
// dependency.
func checkAppliesWhen(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	if st.Type != workflow.StepCheck || st.AppliesWhen == "" || s.guardHolds(st.AppliesWhen) {
		return readinessPass
	}
	state := s.states[st.ID]
	state.Result = &step.Result{Status: step.StatusSucceeded, Verdict: "skip"}
	if !s.transition(st.ID, state.Status, step.StatusSucceeded) {
		return readinessHalt
	}
	return readinessSkip
}

// checkUserInputs parks a step with uncollected from="user" inputs on a
// prompt. preResolvedInputs stops it re-intercepting once every input is
// collected and the step is pending again; once=true answers from an earlier
// iteration are reused without prompting.
func checkUserInputs(s *scheduler, _ context.Context, st *workflow.Step) readiness {
	if !hasUserInputs(st) || len(s.rt(st.ID).preResolvedInputs) > 0 {
		return readinessPass
	}
	if sticky := s.rt(st.ID).stickyInputs; len(sticky) > 0 {
		s.rt(st.ID).preResolvedInputs = append([]ResolvedInput(nil), sticky...)
		return readinessDispatch
	}
	s.dispatchUserPrompt(st)
	return readinessSkip
}

// checkHumanGate parks a human gate on operator input; it never takes a
// worker slot.
func checkHumanGate(s *scheduler, ctx context.Context, st *workflow.Step) readiness {
	if !stepKinds[st.Type].humanGate {
		return readinessPass
	}
	s.dispatch(ctx, st)
	return readinessSkip
}

// guardHolds evaluates a when-style condition. Conditions are validated at
// load, so a parse failure here is impossible; it reads as false.
func (s *scheduler) guardHolds(expr string) bool {
	cond, err := workflow.ParseCondition(expr)
	if err != nil {
		return false
	}
	return s.evalGuard(cond)
}
