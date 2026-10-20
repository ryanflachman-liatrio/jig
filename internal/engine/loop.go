package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// loopIntent accumulates the contributions of every looper that fired toward a
// single goto target within one execution wave, so the coalesced rewind carries
// the union of their feedback rather than only the fastest sibling's.
type loopIntent struct {
	gotoID   string
	contribs []loopContribution
}

// loopContribution is one looper's fired back-edge: the source step, its type
// (so buildStepContext can name the reason), the resolved feedback content (""
// when the loop wires none), and the iteration at which it fired.
type loopContribution struct {
	source     string
	kind       workflow.StepType
	feedback   string
	iter       int
	max        int
	routeIndex int
	when       string
	// automaticCheckRoute stops a completed failing check from dispatching a
	// downstream approval while its remediation back-edge is waiting to fire.
	automaticCheckRoute bool
}

// recordRoutes chooses the first matching route (or explicit fallback) and
// records an engine-owned route event before any rewind state changes.
func (s *scheduler) recordRoutes(stepID string, wfStep *workflow.Step, submittedFeedback string) {
	if wfStep == nil {
		return
	}
	for i, route := range wfStep.Routes {
		if !route.Fallback {
			if !s.guardHolds(route.When) {
				continue
			}
		}
		s.recordRouteIntent(stepID, wfStep, route, i+1, submittedFeedback)
		return
	}
}

func (s *scheduler) recordRouteIntent(stepID string, wfStep *workflow.Step, route workflow.Route, routeIndex int, submittedFeedback string) {
	state := s.states[stepID]
	s.emit(RouteSelected{RunID: s.runID, StepID: stepID, RouteIndex: routeIndex, When: route.When, Goto: route.Goto, Iteration: state.Iteration + 1, Max: route.MaxIterations})

	if state.Iteration >= route.MaxIterations {
		s.emit(RouteCapExceeded{RunID: s.runID, StepID: stepID, RouteIndex: routeIndex, Goto: route.Goto, Iteration: state.Iteration + 1, Max: route.MaxIterations})
		s.aborted = true
		s.emit(RunError{
			RunID: s.runID, Err: fmt.Sprintf("step %q route %d exceeded max_iterations %d", stepID, routeIndex, route.MaxIterations),
		})
		s.cancel()
		return
	}

	// Resolve the feedback now, while the source step's result is current. A
	// document review supplies its rendered submission directly so comments are
	// preserved even when persistence is off and there is no output file to read.
	var content string
	if route.Feedback != "" {
		feedbackID := strings.TrimPrefix(route.Feedback, "@")
		if dot := strings.Index(feedbackID, "."); dot >= 0 {
			feedbackID = feedbackID[:dot]
		}
		if feedbackID == stepID && submittedFeedback != "" {
			content = submittedFeedback
		} else if fs := s.states[feedbackID]; fs != nil && fs.Result != nil {
			if fs.Result.Verdict != "" {
				content = fs.Result.Verdict
			} else if fs.Result.OutputPath != "" {
				if data, err := os.ReadFile(fs.Result.OutputPath); err == nil {
					content = string(data)
				}
			}
		}
	}

	intent := s.pendingLoops[route.Goto]
	if intent == nil {
		intent = &loopIntent{gotoID: route.Goto}
		s.pendingLoops[route.Goto] = intent
	}
	intent.contribs = append(intent.contribs, loopContribution{
		source:              stepID,
		kind:                wfStep.Type,
		feedback:            content,
		iter:                state.Iteration,
		max:                 route.MaxIterations,
		routeIndex:          routeIndex,
		when:                route.When,
		automaticCheckRoute: isFailedCheck(wfStep, state.Result),
	})
}

// isFailedCheck reports whether a check step's verdict is a failure, which
// makes its route an automatic remediation back-edge.
func isFailedCheck(wfStep *workflow.Step, result *step.Result) bool {
	return wfStep.Type == workflow.StepCheck && result != nil && (result.Verdict == "fail" || result.Verdict == "error")
}

// deferredByAutomaticCheckRoute prevents an evidence failure from racing into
// a downstream review while the scheduler waits for parallel checks in the
// same remediation wave to settle. The back-edge then resets its own body;
// unrelated branches continue normally.
func (s *scheduler) deferredByAutomaticCheckRoute(candidate *workflow.Step) bool {
	for _, intent := range s.pendingLoops {
		for _, contribution := range intent.contribs {
			if !contribution.automaticCheckRoute || contribution.source == candidate.ID {
				continue
			}
			for _, id := range s.closureOf(contribution.source) {
				if id == candidate.ID {
					return true
				}
			}
		}
	}
	return false
}

// fireReadyLoops rewinds every pending loop whose body has fully settled. It is
// called at the top of the run loop, so a fired rewind's freshly-pending body
// steps are dispatched in the same iteration. Targets are processed in workflow
// declaration order for determinism.
func (s *scheduler) fireReadyLoops() {
	if len(s.pendingLoops) == 0 {
		return
	}
	for i := range s.wf.Steps {
		g := s.wf.Steps[i].ID
		intent := s.pendingLoops[g]
		if intent == nil || !s.loopBarrierReady(intent) {
			continue
		}
		s.fireCoalescedLoop(intent)
		delete(s.pendingLoops, g)
	}
}

// loopBarrierReady reports whether a coalesced rewind to gotoID can fire: no
// step in the rewind body is still able to contribute to (or be corrupted by)
// the rewind. A body step blocks the barrier while it is running, parked on a
// human gate, or pending-and-dispatchable (still going to run this wave). A
// pending body step whose deps can never be satisfied is treated as settled, so
// a permanently-blocked branch does not stall the rewind.
func (s *scheduler) loopBarrierReady(intent *loopIntent) bool {
	for _, id := range s.staticRouteBody(intent.gotoID) {
		st := s.states[id]
		switch st.Status {
		case step.StatusRunning,
			step.StatusAwaitingReview,
			step.StatusNeedsInput,
			step.StatusAwaitingRecovery,
			step.StatusAwaitingIntegration:
			return false
		case step.StatusPending:
			candidate := s.stepByID(id)
			if s.depsReady(candidate) && !s.deferredByAutomaticCheckRoute(candidate) {
				return false
			}
		}
	}
	return true
}

// staticRouteBody includes every route that shares a target only while the
// coalescing barrier waits for parallel contributors. The actual reset uses
// bodyUnion below, which deliberately contains only routes that fired.
func (s *scheduler) staticRouteBody(gotoID string) []string {
	seen := map[string]bool{}
	for i := range s.wf.Steps {
		looper := &s.wf.Steps[i]
		for _, route := range looper.Routes {
			if route.Goto != gotoID {
				continue
			}
			for _, id := range s.loopBody(gotoID, looper.ID) {
				seen[id] = true
			}
		}
	}
	var out []string
	for i := range s.wf.Steps {
		if seen[s.wf.Steps[i].ID] {
			out = append(out, s.wf.Steps[i].ID)
		}
	}
	return out
}

// bodyUnion is the reset set for the routes that fired in one execution wave.
// Routes that merely share a target must not join the body until they fire: a
// pending downstream review otherwise blocks automatic check remediation.
func (s *scheduler) bodyUnion(intent *loopIntent) []string {
	seen := map[string]bool{}
	for _, contribution := range intent.contribs {
		for _, id := range s.loopBody(intent.gotoID, contribution.source) {
			seen[id] = true
		}
	}
	var out []string
	for i := range s.wf.Steps {
		if seen[s.wf.Steps[i].ID] {
			out = append(out, s.wf.Steps[i].ID)
		}
	}
	return out
}

// fireCoalescedLoop performs one route-driven rewind and resets the affected
// body at the next iteration.
func (s *scheduler) fireCoalescedLoop(intent *loopIntent) {
	// Deterministic order: sort contributors by workflow declaration index.
	declIndex := func(id string) int {
		for i := range s.wf.Steps {
			if s.wf.Steps[i].ID == id {
				return i
			}
		}
		return len(s.wf.Steps)
	}
	sort.SliceStable(intent.contribs, func(i, j int) bool {
		return declIndex(intent.contribs[i].source) < declIndex(intent.contribs[j].source)
	})

	// New iteration is one past the highest iteration among contributors (they
	// share a wave, so this is just wave+1).
	newIter := 0
	for _, c := range intent.contribs {
		if c.iter+1 > newIter {
			newIter = c.iter + 1
		}
	}
	for _, id := range s.bodyUnion(intent) {
		if state := s.states[id]; state != nil && state.Iteration+1 > newIter {
			newIter = state.Iteration + 1
		}
	}

	// Compose feedback. A single contributor writes its content verbatim (so the
	// single-looper case is byte-identical to before). Multiple contributors are
	// concatenated, each labeled by source, so the rewound step sees every
	// looper's findings — not just the fastest one's.
	var feedbacks []loopContribution
	for _, c := range intent.contribs {
		if c.feedback != "" {
			feedbacks = append(feedbacks, c)
		}
	}
	switch len(feedbacks) {
	case 0:
		// no feedback wired by any contributor — leave stepFeedback untouched
	case 1:
		s.rt(intent.gotoID).feedback = feedbacks[0].feedback
	default:
		var b strings.Builder
		for i, c := range feedbacks {
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "From `%s`:\n%s", c.source, c.feedback)
		}
		s.rt(intent.gotoID).feedback = b.String()
	}

	// Name the re-run reason from the first contributor (declaration order).
	s.rt(intent.gotoID).rerunSource = intent.contribs[0].source
	s.rt(intent.gotoID).rerunMax = intent.contribs[0].max

	// Reset every step in the body to pending with the new iteration count.
	for _, id := range s.bodyUnion(intent) {
		bodyState := s.states[id]
		bodyState.Iteration = newIter
		s.transition(id, bodyState.Status, step.StatusPending)
	}
}

// loopBody returns the IDs of all steps on any path from gotoID to loopID
// (inclusive) in the DAG, in declaration order. These are the steps to reset
// when the loop fires.
func (s *scheduler) loopBody(gotoID, loopID string) []string {
	fwd := s.graph.descendants(nil, gotoID)
	bwd := s.graph.ancestors(loopID)
	body := make(map[string]bool)
	for id := range fwd {
		if bwd[id] {
			body[id] = true
		}
	}
	return s.graph.inOrder(body)
}
