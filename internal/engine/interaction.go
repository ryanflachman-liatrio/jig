package engine

import (
	"context"
	"fmt"

	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// reporter routes live step signals through the scheduler's fan-out.
// It is created per-dispatch and passed to the executor; the executor
// may call it from its own goroutine, so fanOutLive must not touch scheduler state.
type reporter struct {
	subs     []sub
	ev       func(Event)
	stepID   string
	inbox    chan<- schedMsg
	security func(int)
}

func (r *reporter) Output(delta string) { r.ev(StepOutput{Delta: delta}) }

func (r *reporter) ToolCall(tool, detail string) { r.ev(StepToolCall{Tool: tool, Detail: detail}) }

func (r *reporter) Message(seq, iteration int) {
	r.ev(StepMessage{Seq: seq, Iteration: iteration})
	if r.security != nil {
		r.security(seq)
	}
}

// Finding routes a SecurityFinding through the ctrl channel (must-not-drop).
func (r *reporter) Finding(sf SecurityFinding) { r.ev(sf) }

func (r *reporter) Question(ctx context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
	if err := req.Validate(); err != nil {
		return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
	}
	reply := make(chan interaction.QuestionResponse, 1)
	select {
	case r.inbox <- agentQuestionRequestMsg{stepID: r.stepID, request: req, reply: reply}:
	case <-ctx.Done():
		return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
	}
	select {
	case response := <-reply:
		return response
	case <-ctx.Done():
		select {
		case r.inbox <- agentQuestionAbandonMsg{stepID: r.stepID, requestID: req.ID}:
		default:
		}
		return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
	}
}

type pendingQuestion struct {
	request  interaction.QuestionRequest
	reply    chan<- interaction.QuestionResponse
	restored bool
}

// handleStop cancels one running step's worker without ending the run (spec 07
// B1). It cancels only that step's child context — never the run context — so
// the worker exits, its stepDoneMsg is routed to StatusStopped (handle marks it
// via stepRuntime.stopping), and the run stays alive and quiescent. Stopping a step that
// has no live worker (pending, already terminal, parked without a worker, or an
// unknown id) is a documented no-op: the absence of a cancel-registry entry is
// exactly "no worker in flight".
func (s *scheduler) handleStop(m stopMsg) {
	cancel, ok := s.stepCancels[m.stepID]
	if !ok {
		return // no worker in flight for this step: nothing to stop
	}
	// Mark the step as being stopped before cancelling so the worker's inbound
	// stepDoneMsg (which may already be racing us) is routed to StatusStopped
	// rather than through the failure policy. The registry entry is cleared when
	// that stepDoneMsg is handled.
	s.rt(m.stepID).stopping = true
	cancel()
}

// handleResume brings a step parked at step.StatusStopped back up (spec 07 B2).
// With a captured session id it continues the agent conversation with a new
// message (reusing the stepRuntime resumeSession/message machinery that dispatch and
// the runner's WithResume/WithContinueConversation path already implement);
// without one it restarts the step fresh — a documented degrade, not an error.
// Either way the step returns to StatusPending so the dispatch loop re-runs it,
// reusing its existing worktree so partial edits accumulate. Resuming a step
// that is not stopped is a no-op.
func (s *scheduler) handleResume(m resumeMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusStopped {
		return // stale, duplicate, or not a stopped step
	}
	if state.Result != nil && state.Result.SessionID != "" {
		// Resume-as-continue: hand the session id + operator message to the next
		// dispatch. This continues the conversation; it does not recover the exact
		// interrupted turn (an SDK limitation).
		s.rt(m.stepID).resumeSession = state.Result.SessionID
		s.rt(m.stepID).message = m.message
	}
	// No captured session id → leave resumeSession unset: dispatch builds the
	// full prompt and the runner starts a fresh session (documented degrade).
	state.Attempt++
	s.restoredHold = false
	s.transition(m.stepID, step.StatusStopped, step.StatusPending)
}

// dispatchUserPrompt parks an agent step that needs from="user" inputs on
// StatusAwaitingReview and emits the first PromptRequest. Subsequent prompts
// are emitted by handle(userInputMsg) as each answer arrives.
func (s *scheduler) dispatchUserPrompt(st *workflow.Step) {
	var userInputs []workflow.Input
	for _, inp := range st.Inputs {
		if inp.From == "user" {
			userInputs = append(userInputs, inp)
		}
	}
	s.rt(st.ID).pendingInputs = userInputs[1:]
	s.rt(st.ID).collectedInputs = nil
	s.transition(st.ID, s.states[st.ID].Status, step.StatusAwaitingReview)
	s.emit(PromptRequest{
		RunID:  s.runID,
		StepID: st.ID,
		Label:  userInputs[0].Label,
		As:     userInputs[0].As,
	})
}

// handleAgentInput processes human input for an agent step blocked by block_on.
// It stashes the session resume info and resets the step to pending for re-dispatch.
func (s *scheduler) handleAgentInput(m agentInputMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusNeedsInput {
		return
	}
	if state.Result == nil || state.Result.SessionID == "" {
		return
	}
	const maxInputRounds = 20
	s.rt(m.stepID).inputRounds++
	if s.rt(m.stepID).inputRounds > maxInputRounds {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q: exceeded maximum input rounds (%d)", m.stepID, maxInputRounds),
		})
		s.rt(m.stepID).inputRounds--
		return
	}
	s.rt(m.stepID).resumeSession = state.Result.SessionID
	s.rt(m.stepID).message = m.text
	s.restoredHold = false
	s.transition(m.stepID, step.StatusNeedsInput, step.StatusPending)
}
