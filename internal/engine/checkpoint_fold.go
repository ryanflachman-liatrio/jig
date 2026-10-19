package engine

import (
	"fmt"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// checkpointFold reduces a durable journal into the state Resume restores.
// Events apply in journal order: a FanOutExpanded must register its children
// before their later StepStatus events fold in.
type checkpointFold struct {
	runDir datastore.Run
	wf     *workflow.Workflow

	states              map[string]*step.State
	reviewRequests      map[string]ReviewRequest
	recoveryRequests    map[string]RecoveryRequest
	inputRequests       map[string]InputRequest
	questions           map[string][]AgentQuestion
	integrationRequests map[string]IntegrationConflictRequest
	verdicts            map[string]string
	recoverySkipped     map[string]bool
	interruptedRecovery map[string]bool
	lostInputRecovery   map[string]bool
	fanOut              fanOutRegistry
	started             *RunStarted
	lastStatusAt        time.Time
}

func newCheckpointFold(runDir datastore.Run, wf *workflow.Workflow) *checkpointFold {
	f := &checkpointFold{
		runDir:              runDir,
		wf:                  wf,
		states:              make(map[string]*step.State, len(wf.Steps)),
		reviewRequests:      make(map[string]ReviewRequest),
		recoveryRequests:    make(map[string]RecoveryRequest),
		inputRequests:       make(map[string]InputRequest),
		questions:           make(map[string][]AgentQuestion),
		integrationRequests: make(map[string]IntegrationConflictRequest),
		verdicts:            make(map[string]string),
		recoverySkipped:     make(map[string]bool),
		interruptedRecovery: make(map[string]bool),
		lostInputRecovery:   make(map[string]bool),
		fanOut:              newFanOutRegistry(),
	}
	for i := range wf.Steps {
		f.states[wf.Steps[i].ID] = &step.State{ID: wf.Steps[i].ID, Status: step.StatusPending}
	}
	return f
}

// apply folds one journal event.
func (f *checkpointFold) apply(event Event) error {
	switch event := event.(type) {
	case FanOutExpanded:
		return f.applyFanOutExpanded(event)
	case StepStatus:
		return f.applyStepStatus(event)
	case RunStarted:
		startedEvent := event
		f.started = &startedEvent
	case RunFinished:
		return fmt.Errorf("resume: %w", ErrRunFinished)
	case ReviewRequest:
		f.reviewRequests[event.StepID] = event
	case ReviewSubmitted:
		f.verdicts[event.StepID] = event.Verdict
		delete(f.reviewRequests, event.StepID)
	case RecoveryRequest:
		f.recoveryRequests[event.StepID] = event
	case InputRequest:
		f.inputRequests[event.StepID] = event
	case AgentQuestion:
		f.questions[event.StepID] = upsertQuestion(f.questions[event.StepID], event)
	case AgentQuestionResolved:
		f.questions[event.StepID] = removeQuestion(f.questions[event.StepID], event.RequestID)
	case IntegrationConflictRequest:
		f.integrationRequests[event.StepID] = event
	}
	return nil
}

func (f *checkpointFold) applyFanOutExpanded(event FanOutExpanded) error {
	// FanOutExpanded is the authoritative creation record for runtime
	// children (docs/plans/a8-dynamic-foreach-fan-out.md, "Durability,
	// replay, and crash reopen"): rebuild the family and its children
	// from the matching on-disk manifest — never by re-reading the
	// producer — and register their step.State entries before any
	// later StepStatus event for a child instance id is folded below.
	rebuilt, err := rebuildFanOutFamily(f.runDir, f.wf, event, f.states)
	if err != nil {
		return err
	}
	f.fanOut.addOrder(event.FamilyID)
	f.fanOut.families[event.FamilyID] = rebuilt.family
	for id, child := range rebuilt.children {
		f.fanOut.children[id] = child
	}
	for id, item := range rebuilt.items {
		f.fanOut.items[id] = item
	}
	return nil
}

func (f *checkpointFold) applyStepStatus(event StepStatus) error {
	if event.At.After(f.lastStatusAt) {
		f.lastStatusAt = event.At
	}
	state := f.states[event.StepID]
	if state == nil {
		return fmt.Errorf("resume: workflow no longer contains step %q", event.StepID)
	}
	state.Timing = event.ProjectTiming(state.Timing)
	state.Status = event.To
	state.Attempt = event.Attempt
	state.Iteration = event.Iteration
	state.Generation = event.Generation
	if event.Cost != nil {
		state.SpentUSD = *event.Cost
	}
	if event.Tokens > state.SpentTokens {
		state.SpentTokens = event.Tokens
	}
	if event.To.IsTerminal() {
		state.Result = loadPersistedResult(f.runDir, event.StepID, event.To, event.Err, event.Subtype)
	}
	if event.To == step.StatusFailed && event.RecoveryAction == RecoverSkip {
		f.recoverySkipped[event.StepID] = true
	} else if event.To != step.StatusFailed {
		delete(f.recoverySkipped, event.StepID)
	}
	if event.To == step.StatusAwaitingRecovery && event.RecoveryAction == processInterruptedRecoveryAction {
		f.interruptedRecovery[event.StepID] = true
	} else {
		delete(f.interruptedRecovery, event.StepID)
	}
	if event.To == step.StatusAwaitingRecovery && event.RecoveryAction == lostInputRecoveryAction {
		f.lostInputRecovery[event.StepID] = true
	} else {
		delete(f.lostInputRecovery, event.StepID)
	}
	if event.To != step.StatusAwaitingReview {
		delete(f.reviewRequests, event.StepID)
	}
	if event.To != step.StatusAwaitingRecovery {
		delete(f.recoveryRequests, event.StepID)
	}
	if event.To != step.StatusNeedsInput {
		delete(f.inputRequests, event.StepID)
		delete(f.questions, event.StepID)
	}
	if event.To != step.StatusAwaitingIntegration {
		delete(f.integrationRequests, event.StepID)
	}
	return nil
}

// classifyPark applies the same durable-status classification to any step
// id — static or a runtime fan-out child — so a child parked mid-flight
// reopens through exactly the same Specs 20/21 path as a static step (the
// plan's "Durability, replay, and crash reopen").
func (f *checkpointFold) classifyPark(c *unfinishedCheckpoint, id string) error {
	state := f.states[id]
	switch state.Status {
	case step.StatusPending, step.StatusSucceeded, step.StatusSkipped, step.StatusFailed:
	case step.StatusAwaitingReview:
		req, ok := f.reviewRequests[id]
		if !ok {
			return fmt.Errorf("resume: step %q has no durable review request", id)
		}
		sess, err := restoreReviewSession(f.runDir, req)
		if err != nil {
			return err
		}
		c.reviewSessions[id] = sess
	case step.StatusRunning, step.StatusValidating:
		c.interrupted[id] = true
	case step.StatusAwaitingRecovery:
		req, ok := f.recoveryRequests[id]
		if !ok {
			errText := "step was awaiting recovery when the jig process exited"
			if f.interruptedRecovery[id] {
				errText = processInterruptedErr
			} else if f.lostInputRecovery[id] {
				errText = "process exited while step needed input, but its durable input could not be restored"
			}
			req = RecoveryRequest{RunID: f.started.RunID, StepID: id, Err: errText}
		}
		c.recoveries[id] = req
	case step.StatusNeedsInput:
		switch {
		case len(f.questions[id]) > 0:
			c.questions[id] = f.questions[id]
		case f.inputRequests[id].StepID != "":
			c.inputs[id] = f.inputRequests[id]
		default:
			c.missingInputPayload[id] = "process exited while step needed input, but no durable input request was found"
		}
	case step.StatusAwaitingIntegration:
		c.integrations[id] = f.integrationRequests[id]
	case step.StatusStopped:
		c.stopped[id] = true
	default:
		return fmt.Errorf("resume: step %q has unsupported durable status %s", id, state.Status)
	}
	return nil
}
