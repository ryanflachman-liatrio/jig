package engine

import (
	"jig/internal/interaction"
	"jig/internal/step"
	"jig/internal/workflow"
)

// command is the Command pattern's contract: every schedMsg knows how to
// apply itself to the scheduler. scheduler.handle() no longer needs to know
// the concrete message types — it just type-asserts to command and delegates.
// This replaces a single ~200-line type-switch with one execute() method per
// message type, so adding a new schedMsg no longer means editing a shared
// switch statement.
type command interface {
	schedMsg
	execute(s *scheduler)
}

// execute processes a worker's completion: it records cost/token accrual,
// releases the worker's context, and then routes the result through the
// early-exit paths (parked for recovery, deliberately stopped) or the normal
// post-exec chain / failure-policy path.
func (m stepDoneMsg) execute(s *scheduler) {
	s.inFlight--
	delete(s.pendingQuestions, m.stepID)
	// Accrue this attempt's cost/tokens once, before any early-return branch
	// (stopped, recovery-parked, failed) — every executor invocation was paid
	// for, so retries, loop re-runs, and resumed sessions all add to the step's
	// cumulative spend rather than replacing it. This is the single accrual
	// point; transition() and snapshot() only read the accumulators.
	if st := s.states[m.stepID]; st != nil && m.result != nil {
		if m.result.TotalCostUSD != nil {
			st.SpentUSD += *m.result.TotalCostUSD
		}
		if tok, ok := m.result.TokenCount(); ok {
			st.SpentTokens += tok
		}
	}
	// Release the worker's child context and drop it from the registry — the
	// worker has exited, so its CancelFunc has no further use.
	if cancel, ok := s.stepCancels[m.stepID]; ok {
		cancel()
		delete(s.stepCancels, m.stepID)
	}

	// A step escalated to StatusAwaitingRecovery by a critical security finding
	// while it was still running: keep it parked regardless of the worker's final
	// result. Record the result (preserving SessionID for potential resume) but
	// do not re-process through the post-exec chain or failure policy.
	if s.states[m.stepID] != nil && s.states[m.stepID].Status == step.StatusAwaitingRecovery {
		if m.result != nil {
			s.states[m.stepID].Result = m.result
		}
		return
	}

	// A deliberately-stopped worker (Run.Stop cancelled its context) is not a
	// failure and not end-of-run (spec 07 B1). Record whatever result it
	// returned so a resume keeps its captured session id, preserve its partial
	// worktree diff on disk, and park it at StatusStopped — the run stays alive
	// and becomes quiescent. Skip the failure policy and the post-exec chain.
	if s.stopping[m.stepID] {
		delete(s.stopping, m.stepID)
		if m.result != nil {
			s.states[m.stepID].Result = m.result
		}
		// Capture the partial diff now: on the normal path phCaptureWorktreeDiff
		// does this, but a stopped worker never reaches the post-exec chain.
		if path, ok := s.worktrees[m.stepID]; ok {
			s.diffs[m.stepID] = captureDiff(path, s.wtBaseSHAs[m.stepID])
		}
		from := s.states[m.stepID].Status
		s.transition(m.stepID, from, step.StatusStopped)
		return
	}

	// Record result and synthesize an error result when the executor
	// returned a Go error rather than a failed Result.
	if m.result != nil {
		s.states[m.stepID].Result = m.result
	}
	if m.err != nil {
		res := s.states[m.stepID].Result
		if res == nil {
			res = &step.Result{Status: step.StatusFailed}
			s.states[m.stepID].Result = res
		}
		if res.Err == "" {
			res.Err = m.err.Error()
		}
	}

	// Raw execution failure short-circuits the chain.
	execFailed := m.err != nil || (m.result != nil && m.result.Status == step.StatusFailed)

	wfStep := s.stepByID(m.stepID)

	if execFailed {
		s.applyFailurePolicy(m.stepID, wfStep)
		return
	}

	// Run the post-execution handler chain.
	decision := decisionContinue
	for _, h := range s.postExecChain {
		if decision = h(s, m, wfStep); decision != decisionContinue {
			break
		}
	}

	switch decision {
	case decisionFailed:
		s.applyFailurePolicy(m.stepID, wfStep)
	case decisionNeedsInput:
		// handler already transitioned the step; nothing more to do
	default: // decisionContinue — all handlers passed → step succeeded
		curFrom := s.states[m.stepID].Status
		s.transition(m.stepID, curFrom, step.StatusSucceeded)
		if wfStep != nil && wfStep.Loop != nil {
			s.recordLoopIntent(m.stepID, wfStep)
		}
	}
}

// execute appends the collected text as an inline ResolvedInput, then either
// fires the next queued prompt or promotes the step back to pending once every
// prompt has been answered.
func (m userInputMsg) execute(s *scheduler) {
	s.collectedUserInputs[m.stepID] = append(
		s.collectedUserInputs[m.stepID],
		ResolvedInput{
			Ref:   workflow.Input{Inline: true, As: m.as},
			Value: m.text,
		},
	)
	// If more prompts are queued, fire the next one.
	if len(s.pendingUserInputs[m.stepID]) > 0 {
		next := s.pendingUserInputs[m.stepID][0]
		s.pendingUserInputs[m.stepID] = s.pendingUserInputs[m.stepID][1:]
		s.emit(PromptRequest{
			RunID:  s.runID,
			StepID: m.stepID,
			Label:  next.Label,
			As:     next.As,
		})
		return
	}
	// All inputs collected — promote to preResolved and reset to pending
	// so nextReady picks it up for normal dispatch.
	s.preResolvedInputs[m.stepID] = s.collectedUserInputs[m.stepID]
	delete(s.collectedUserInputs, m.stepID)
	delete(s.pendingUserInputs, m.stepID)
	s.transition(m.stepID, step.StatusAwaitingReview, step.StatusPending)
}

// execute delivers the human's verdict to an awaiting_review step.
func (m verdictMsg) execute(s *scheduler) {
	state := s.states[m.stepID]
	if state.Status != step.StatusAwaitingReview {
		return // stale or duplicate verdict
	}
	state.Result = &step.Result{Status: step.StatusSucceeded, Verdict: m.verdict}
	wfStep := s.stepByID(m.stepID)
	s.transition(m.stepID, step.StatusAwaitingReview, step.StatusSucceeded)
	if wfStep != nil && wfStep.Loop != nil {
		s.recordLoopIntent(m.stepID, wfStep)
	}
}

func (m humanMessageMsg) execute(s *scheduler)       { s.handleHumanMessage(m) }
func (m agentInputMsg) execute(s *scheduler)         { s.handleAgentInput(m) }
func (m recoverMsg) execute(s *scheduler)            { s.handleRecover(m) }
func (m resolveIntegrationMsg) execute(s *scheduler) { s.handleResolveIntegration(m) }
func (m finalMergeMsg) execute(s *scheduler)         { s.handleFinalMerge(m) }
func (m stopMsg) execute(s *scheduler)               { s.handleStop(m) }
func (m resumeMsg) execute(s *scheduler)             { s.handleResume(m) }
func (m resetMsg) execute(s *scheduler)              { s.handleReset(m) }
func (m securityFindingMsg) execute(s *scheduler)    { s.handleSecurityFinding(m.sf) }

func (m agentQuestionRequestMsg) execute(s *scheduler) {
	state := s.states[m.stepID]
	if state == nil || (state.Status != step.StatusRunning && state.Status != step.StatusNeedsInput) {
		m.reply <- interaction.QuestionResponse{RequestID: m.request.ID, Action: interaction.ActionCancel}
		return
	}
	pending := s.pendingQuestions[m.stepID]
	if pending == nil {
		pending = make(map[string]pendingQuestion)
		s.pendingQuestions[m.stepID] = pending
	}
	if _, exists := pending[m.request.ID]; exists {
		m.reply <- interaction.QuestionResponse{RequestID: m.request.ID, Action: interaction.ActionCancel}
		return
	}
	pending[m.request.ID] = pendingQuestion{request: m.request, reply: m.reply}
	if state.Status == step.StatusRunning {
		s.transition(m.stepID, step.StatusRunning, step.StatusNeedsInput)
	}
	s.emit(AgentQuestion{RunID: s.runID, StepID: m.stepID, Request: m.request})
}

func (m agentQuestionAnswerMsg) execute(s *scheduler) {
	pending := s.pendingQuestions[m.stepID]
	item, ok := pending[m.response.RequestID]
	if !ok {
		return
	}
	if err := m.response.Validate(item.request); err != nil {
		return
	}
	delete(pending, m.response.RequestID)
	if len(pending) == 0 {
		delete(s.pendingQuestions, m.stepID)
	}
	s.emit(AgentQuestionResolved{
		RunID:     s.runID,
		StepID:    m.stepID,
		RequestID: m.response.RequestID,
		Action:    m.response.Action,
	})
	state := s.states[m.stepID]
	if state != nil && state.Status == step.StatusNeedsInput && len(pending) == 0 {
		s.transition(m.stepID, step.StatusNeedsInput, step.StatusRunning)
	}
	select {
	case item.reply <- m.response:
	default:
	}
}

func (m agentQuestionAbandonMsg) execute(s *scheduler) {
	pending := s.pendingQuestions[m.stepID]
	if _, ok := pending[m.requestID]; !ok {
		return
	}
	delete(pending, m.requestID)
	if len(pending) == 0 {
		delete(s.pendingQuestions, m.stepID)
	}
	s.emit(AgentQuestionResolved{
		RunID:     s.runID,
		StepID:    m.stepID,
		RequestID: m.requestID,
		Action:    interaction.ActionCancel,
	})
	state := s.states[m.stepID]
	if state != nil && state.Status == step.StatusNeedsInput && len(pending) == 0 {
		s.transition(m.stepID, step.StatusNeedsInput, step.StatusRunning)
	}
}

func (m snapshotReqMsg) execute(s *scheduler) { m.reply <- s.snapshot() }
func (m closureReqMsg) execute(s *scheduler)  { m.reply <- s.closureOf(m.stepID) }
