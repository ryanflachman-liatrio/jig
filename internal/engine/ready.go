package engine

import (
	"context"
	"fmt"
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

func (s *scheduler) budgetExhausted() (bool, string) {
	if cap := s.wf.Defaults.MaxCostUSD; cap > 0 {
		var spent float64
		for _, state := range s.states {
			spent += state.SpentUSD
		}
		if spent >= cap {
			return true, fmt.Sprintf("workflow cost budget exhausted ($%.2f of $%.2f)", spent, cap)
		}
	}
	if cap := s.wf.Defaults.MaxSecurityFindings; cap > 0 && s.securityFindings >= cap {
		return true, fmt.Sprintf("workflow security finding budget exhausted (%d of %d)", s.securityFindings, cap)
	}
	if cap := s.wf.Defaults.MaxNetworkRequests; cap > 0 && s.networkRequests >= cap {
		return true, fmt.Sprintf("workflow network request budget exhausted (%d of %d)", s.networkRequests, cap)
	}
	return false, ""
}

// depsReady reports whether all of st's dependencies have reached a state
// that allows st to proceed.  A dependency is ready when:
//   - its status is succeeded, or
//   - its status is failed and its on_failure policy is "continue" (the dep
//     explicitly opts in to letting dependents run despite its own failure).
//
// All other statuses (pending, running, validating, awaiting_review, skipped)
// are not ready — the step must wait.
func (s *scheduler) depsReady(st *workflow.Step) bool {
	for _, depID := range st.DependsOn {
		depState := s.states[depID]
		if depState.Status == step.StatusSucceeded {
			continue
		}
		// A guard-skipped dep is transparent when the dependent only uses it for
		// sequencing (no @ref input). If the dependent references output data from
		// the guard-skipped step it must still wait (and will be cascade-skipped).
		if depState.Status == step.StatusSkipped && s.rt(depID).skippedByGuard && !stepRefsDep(st, depID) {
			continue
		}
		// A failed dep is "ready" when it declared on_failure = "continue", or
		// when a human skipped it at the recovery gate (RecoverSkip) — both mean
		// "its failure does not block dependents."
		if depState.Status == step.StatusFailed {
			if s.rt(depID).skippedByOperator {
				continue
			}
			depStep := s.stepByID(depID)
			if depStep != nil && depStep.OnFailure == workflow.FailContinue {
				continue
			}
		}
		return false
	}
	return true
}

func (s *scheduler) resourceAvailable(st *workflow.Step) bool {
	return s.slots.admits(st, s.wf.Defaults)
}

// anyPendingRunnable reports whether any pending step could eventually become
// ready. Uses transitive failure propagation so A→B→C where A fails (abort
// policy) doesn't deadlock the scheduler.
//
// A failed step with on_failure = "continue" does NOT block its dependents —
// it is treated as a transparent node for the purposes of reachability.
func (s *scheduler) anyPendingRunnable() bool {
	// Seed the permanently-blocked set with abort-failed and skipped steps.
	// A step that failed with "continue" is NOT in this set — its failure is
	// known but it doesn't block dependents.
	var seeds []string
	for id, st := range s.states {
		switch st.Status {
		case step.StatusSkipped:
			// Guard-skipped steps are transparent optional branches — dependents
			// should not be considered blocked just because the guard didn't fire.
			if !s.rt(id).skippedByGuard {
				seeds = append(seeds, id)
			}
		case step.StatusFailed:
			// An operator-skipped step (RecoverSkip) is transparent like a
			// continue-failed one — it must not seed the blocked set.
			if s.rt(id).skippedByOperator {
				continue
			}
			if wfStep := s.stepByID(id); wfStep == nil || wfStep.OnFailure != workflow.FailContinue {
				seeds = append(seeds, id)
			}
		}
	}
	// Everything downstream of a hard-blocked step is blocked too.
	blocked := s.graph.descendants(nil, seeds...)
	for id, st := range s.states {
		if st.Status == step.StatusPending && !blocked[id] {
			return true
		}
		// A parked step (review, input, recovery, integration conflict, stop)
		// keeps the run alive: an operator decision will unblock it, so zero
		// in-flight workers is not end-of-run.
		if st.Status.IsParked() {
			return true
		}
	}
	return false
}

func (s *scheduler) anyFailed() bool {
	if s.aborted {
		return true
	}
	for _, st := range s.states {
		if st.Status == step.StatusFailed {
			return true
		}
	}
	return false
}

// cascadeSkip skips every pending step that has a transitive dependency on
// skipID (and whose dep chain doesn't pass through a on_failure=continue step).
// A skipped dep means the dependent's @ref inputs can never resolve.
func (s *scheduler) cascadeSkip(skipID string) {
	toSkip := s.graph.descendants(func(dep, dependent string) bool {
		if s.states[dependent].Status != step.StatusPending {
			return false
		}
		// Guard-skipped deps are transparent when the dependent only uses
		// them for sequencing (no @ref input). If the dependent references
		// output data from the guard-skipped step it must still be skipped.
		if s.rt(dep).skippedByGuard && !stepRefsDep(s.stepByID(dependent), dep) {
			return false
		}
		// A skipped dep cascades unless the dep opted into "continue".
		depStep := s.stepByID(dep)
		return depStep == nil || depStep.OnFailure != workflow.FailContinue
	}, skipID)
	for _, id := range s.graph.inOrder(toSkip) {
		if id == skipID {
			continue // already transitioned by nextReady
		}
		from := s.states[id].Status
		s.transition(id, from, step.StatusSkipped)
	}
}

// stepRefsDep reports whether any of st's inputs has Ref == depID, meaning st
// requires output data from depID (as opposed to a pure sequencing dependency).
func stepRefsDep(st *workflow.Step, depID string) bool {
	for _, inp := range st.Inputs {
		if inp.Ref == depID {
			return true
		}
	}
	return false
}
