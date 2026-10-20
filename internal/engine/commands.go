package engine

import (
	"strings"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/review"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// command is a schedMsg that applies itself to the scheduler.
// scheduler.handle type-asserts each inbox message to command and calls
// execute, so a new message type adds a method rather than editing a shared
// switch.
type command interface {
	schedMsg
	execute(s *scheduler)
}

// execute processes a worker's completion in four phases: release what the
// worker held, take an early exit if the step was parked or stopped while the
// worker ran, record the result, then route it through retry and failure
// policy or the post-exec chain.
func (m stepDoneMsg) execute(s *scheduler) {
	// Validation runs on the dispatch snapshot too, so defer release until every
	// post-execution handler has consumed it. A retry always acquires a fresh
	// view of the current run branch.
	defer s.releaseExecutionViewForStep(m.stepID)

	if !s.releaseWorker(m) {
		return
	}
	if s.exitParkedOrStopped(m) {
		return
	}
	wfStep := s.stepByID(m.stepID)
	if s.recordResult(m) {
		if s.scheduleAutomaticRetry(m.stepID, wfStep, m) {
			return
		}
		s.applyFailurePolicy(m.stepID, wfStep)
		return
	}
	s.runPostExecChain(m, wfStep)
}

// releaseWorker returns what the finished worker held: its capacity slots,
// its accrued spend, its open questions, and its cancel func. It reports
// false for a worker reaped by a security escalation, whose completion must
// not be processed further.
func (s *scheduler) releaseWorker(m stepDoneMsg) bool {
	if wfStep := s.stepByID(m.stepID); wfStep != nil {
		s.releaseSlots(wfStep)
	} else {
		s.releaseSlots(&workflow.Step{ID: m.stepID})
	}
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
	// A worker reaped by a security escalation already had its context
	// cancelled. Its step was parked when it was reaped and the operator may
	// since have skipped, retried, or aborted it, so the completion must not
	// drive the failure policy or touch a replacement worker's cancel func.
	if rt := s.rt(m.stepID); rt.reaped {
		rt.reaped = false
		if st := s.states[m.stepID]; st != nil && st.Status == step.StatusAwaitingRecovery && m.result != nil {
			st.Result = m.result
		}
		return false
	}
	s.rt(m.stepID).questions = nil
	// The worker has exited, so its CancelFunc has no further use.
	if cancel, ok := s.stepCancels[m.stepID]; ok {
		cancel()
		delete(s.stepCancels, m.stepID)
	}
	return true
}

// exitParkedOrStopped handles a completion for a step that left Running while
// its worker ran, reporting whether it did. Such a step skips the failure
// policy and the post-exec chain.
func (s *scheduler) exitParkedOrStopped(m stepDoneMsg) bool {
	state := s.states[m.stepID]
	// Escalated to recovery by a critical security finding: keep it parked
	// whatever the worker returned, recording the result so SessionID survives
	// for a resume.
	if state != nil && state.Status == step.StatusAwaitingRecovery {
		if m.result != nil {
			state.Result = m.result
		}
		return true
	}
	// A deliberately stopped worker (Run.Stop cancelled its context) is not a
	// failure and not end-of-run (spec 07 B1). Record its result so a resume
	// keeps the session id, keep its partial diff, and park it at
	// StatusStopped; the run stays alive and quiescent.
	if rt := s.rt(m.stepID); rt.stopping {
		rt.stopping = false
		if m.result != nil {
			state.Result = m.result
		}
		s.captureStepDiff(m.stepID)
		s.transition(m.stepID, state.Status, step.StatusStopped)
		return true
	}
	return false
}

// recordResult stores the worker's result, synthesizing a failed result for a
// timeout or a Go error, and reports whether execution failed.
func (s *scheduler) recordResult(m stepDoneMsg) (failed bool) {
	state := s.states[m.stepID]
	if m.result != nil {
		state.Result = m.result
	}
	if m.timedOut {
		if state.Result == nil {
			state.Result = &step.Result{}
		}
		state.Result.Status = step.StatusFailed
		state.Result.Err = "step timeout exceeded"
	}
	if m.err != nil {
		if state.Result == nil {
			state.Result = &step.Result{Status: step.StatusFailed}
		}
		if state.Result.Err == "" {
			state.Result.Err = m.err.Error()
		}
	}
	return m.err != nil || m.timedOut || (m.result != nil && m.result.Status == step.StatusFailed)
}

// runPostExecChain walks the post-execution handlers for a successful
// execution and applies their verdict.
func (s *scheduler) runPostExecChain(m stepDoneMsg, wfStep *workflow.Step) {
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
		if !s.transition(m.stepID, s.states[m.stepID].Status, step.StatusSucceeded) {
			return
		}
		if wfStep != nil {
			s.recordRoutes(m.stepID, wfStep, "")
		}
	}
}

// execute appends the collected text as an inline ResolvedInput, then either
// fires the next queued prompt or promotes the step back to pending once every
// prompt has been answered.
func (m userInputMsg) execute(s *scheduler) {
	s.rt(m.stepID).collectedInputs = append(
		s.rt(m.stepID).collectedInputs,
		ResolvedInput{
			Ref:   workflow.Input{Inline: true, As: m.as},
			Value: m.text,
		},
	)
	// If more prompts are queued, fire the next one.
	if len(s.rt(m.stepID).pendingInputs) > 0 {
		next := s.rt(m.stepID).pendingInputs[0]
		s.rt(m.stepID).pendingInputs = s.rt(m.stepID).pendingInputs[1:]
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
	s.rt(m.stepID).preResolvedInputs = s.rt(m.stepID).collectedInputs
	if wfStep := s.stepByID(m.stepID); wfStep != nil {
		for _, input := range wfStep.Inputs {
			if !input.Once {
				continue
			}
			for _, resolved := range s.rt(m.stepID).collectedInputs {
				if resolved.Ref.As == input.As {
					s.rt(m.stepID).stickyInputs = append(s.rt(m.stepID).stickyInputs, resolved)
				}
			}
		}
	}
	s.rt(m.stepID).collectedInputs = nil
	s.rt(m.stepID).pendingInputs = nil
	s.transition(m.stepID, step.StatusAwaitingReview, step.StatusPending)
}

// execute delivers the human's verdict to an awaiting_review step.
func (m reviewSubmissionMsg) execute(s *scheduler) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusAwaitingReview {
		return // stale or duplicate verdict
	}
	s.finalizeReview(m.stepID, m.submission)
	if state.Status != step.StatusAwaitingReview {
		s.restoredHold = false
	}
}

func (m agentInputMsg) execute(s *scheduler)         { s.handleAgentInput(m) }
func (m recoverMsg) execute(s *scheduler)            { s.handleRecover(m) }
func (m resolveIntegrationMsg) execute(s *scheduler) { s.handleResolveIntegration(m) }
func (m resolveIntegrationWithAgentMsg) execute(s *scheduler) {
	s.handleResolveIntegrationWithAgent(m)
}
func (m integrationResolvedMsg) execute(s *scheduler) { s.handleIntegrationResolved(m) }
func (m finalMergeMsg) execute(s *scheduler)          { s.handleFinalMerge(m) }
func (m stopMsg) execute(s *scheduler)                { s.handleStop(m) }
func (m resumeMsg) execute(s *scheduler)              { s.handleResume(m) }
func (m resetMsg) execute(s *scheduler)               { s.handleReset(m) }
func (m securityFindingMsg) execute(s *scheduler)     { s.handleSecurityFinding(m.sf) }
func (m retryWakeMsg) execute(s *scheduler)           { s.rt(m.stepID).retryNotBefore = time.Time{} }
func (m networkRequestMsg) execute(s *scheduler) {
	s.networkRequests++
	cap := s.wf.Defaults.MaxNetworkRequests
	if cap <= 0 || s.networkRequests <= cap {
		return
	}
	if cancel, ok := s.stepCancels[m.stepID]; ok {
		cancel()
	}
}

func (s *scheduler) scheduleAutomaticRetry(stepID string, wfStep *workflow.Step, done stepDoneMsg) bool {
	if wfStep == nil || wfStep.Retry == nil || !retryMatches(wfStep.Retry, done) {
		return false
	}
	state := s.states[stepID]
	if state == nil || state.Attempt+1 >= wfStep.Retry.MaxAttempts {
		return false
	}
	state.Attempt++
	delay := retryDelay(wfStep.Retry, state.Attempt)
	s.rt(stepID).retryNotBefore = time.Now().Add(delay)
	from := state.Status
	s.transition(stepID, from, step.StatusPending)
	if delay == 0 {
		select {
		case s.inbox <- retryWakeMsg{stepID: stepID}:
		default:
		}
		return true
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		select {
		case s.inbox <- retryWakeMsg{stepID: stepID}:
		default:
		}
	}()
	return true
}

func retryMatches(policy *workflow.RetryPolicy, done stepDoneMsg) bool {
	if done.timedOut {
		return containsRetryClass(policy.RetryOn, workflow.RetryTimeout)
	}
	if done.err != nil {
		return containsRetryClass(policy.RetryOn, workflow.RetryTemporary)
	}
	if done.result == nil || done.result.Status != step.StatusFailed {
		return false
	}
	if strings.HasPrefix(done.result.Subtype, "error_") {
		return containsRetryClass(policy.RetryOn, workflow.RetryAgentError)
	}
	return containsRetryClass(policy.RetryOn, workflow.RetryExitFailure)
}

func containsRetryClass(classes []string, want string) bool {
	for _, class := range classes {
		if class == want {
			return true
		}
	}
	return false
}

func retryDelay(policy *workflow.RetryPolicy, attempt int) time.Duration {
	if policy.Backoff == workflow.RetryBackoffNone {
		return 0
	}
	delay := policy.Initial.Duration
	if policy.Backoff == workflow.RetryBackoffExponential {
		for i := 1; i < attempt; i++ {
			delay *= 2
		}
	}
	return delay
}

func (m agentQuestionRequestMsg) execute(s *scheduler) {
	state := s.states[m.stepID]
	if state == nil || (state.Status != step.StatusRunning && state.Status != step.StatusNeedsInput) {
		m.reply <- interaction.QuestionResponse{RequestID: m.request.ID, Action: interaction.ActionCancel}
		return
	}
	pending := s.rt(m.stepID).questions
	if pending == nil {
		pending = make(map[string]pendingQuestion)
		s.rt(m.stepID).questions = pending
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
	pending := s.rt(m.stepID).questions
	item, ok := pending[m.response.RequestID]
	if !ok {
		return
	}
	if err := m.response.Validate(item.request); err != nil {
		// The question stays pending, but the UI already dismissed its panel on
		// submit. Re-announce it so the operator can answer again instead of the
		// step sitting in needs_input with nothing on screen.
		s.emit(AgentQuestion{RunID: s.runID, StepID: m.stepID, Request: item.request})
		return
	}
	delete(pending, m.response.RequestID)
	if len(pending) == 0 {
		s.rt(m.stepID).questions = nil
	}
	s.emit(AgentQuestionResolved{
		RunID:     s.runID,
		StepID:    m.stepID,
		RequestID: m.response.RequestID,
		Action:    m.response.Action,
	})
	state := s.states[m.stepID]
	if state != nil && state.Status == step.StatusNeedsInput && len(pending) == 0 {
		if item.restored {
			s.rt(m.stepID).restoredResponses = append(s.rt(m.stepID).restoredResponses, m.response)
			if state.Result == nil || !s.stepCanResume(m.stepID, state.Result.SessionID) {
				return
			}
			s.rt(m.stepID).resumeSession = state.Result.SessionID
			s.rt(m.stepID).message = restoredQuestionMessage(s.rt(m.stepID).restoredResponses)
			s.rt(m.stepID).restoredResponses = nil
			s.restoredHold = false
			s.transition(m.stepID, step.StatusNeedsInput, step.StatusPending)
		} else {
			s.transition(m.stepID, step.StatusNeedsInput, step.StatusRunning)
		}
	} else if item.restored {
		s.rt(m.stepID).restoredResponses = append(s.rt(m.stepID).restoredResponses, m.response)
	}
	if item.reply != nil {
		select {
		case item.reply <- m.response:
		default:
		}
	}
}

func (m agentQuestionAbandonMsg) execute(s *scheduler) {
	pending := s.rt(m.stepID).questions
	if _, ok := pending[m.requestID]; !ok {
		return
	}
	delete(pending, m.requestID)
	if len(pending) == 0 {
		s.rt(m.stepID).questions = nil
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

type schedMsg interface{ isSchedMsg() }

type stepDoneMsg struct {
	stepID   string
	result   *step.Result
	err      error
	timedOut bool
}

type reviewSubmissionMsg struct {
	stepID     string
	submission review.Submission
}

type userInputMsg struct {
	stepID string
	as     string
	text   string
}

type snapshotReqMsg struct {
	reply chan<- RunSnapshot
}

type closureReqMsg struct {
	stepID string
	reply  chan<- []string
}

type agentInputMsg struct {
	stepID string // agent step blocked by block_on
	text   string
}

type agentQuestionRequestMsg struct {
	stepID  string
	request interaction.QuestionRequest
	reply   chan<- interaction.QuestionResponse
}

type agentQuestionAnswerMsg struct {
	stepID   string
	response interaction.QuestionResponse
}

type agentQuestionAbandonMsg struct {
	stepID    string
	requestID string
}

// maxRecoverRounds bounds how many times a single step may be retried/resumed
// through the recovery gate, preserving jig's static termination guarantee even
// though the gate is human-driven. The human can always abort instead.
const maxRecoverRounds = 20

type recoverMsg struct {
	stepID string
	action string // RecoverRetry | RecoverResume | RecoverSkip | RecoverAbort
	text   string // optional guidance, used by RecoverResume
}

// resolveIntegrationMsg carries a human decision for a step parked on an
// integration conflict: resolve (finish the merge) or abort (fail the step).
type resolveIntegrationMsg struct {
	stepID string
	abort  bool
}

type resolveIntegrationWithAgentMsg struct{ stepID string }

// integrationResolvedMsg delivers the agent conflict resolver's outcome back to
// the scheduler goroutine. seq ties it to the request that started it.
type integrationResolvedMsg struct {
	stepID string
	seq    uint64
	paths  []string
	result *step.Result
	err    error
}

// finalMergeMsg carries the operator's decision for the final-merge gate (spec
// 06 A3): approve lands the run branch onto the base, discard leaves it.
type finalMergeMsg struct {
	approve bool
}

// stopMsg asks the scheduler to stop one running step's worker without ending
// the run (spec 07 B1). handleStop cancels that step's child context only.
type stopMsg struct {
	stepID string
}

// resumeMsg asks the scheduler to resume a stopped step (spec 07 B2). When the
// step has a captured session id the agent session is continued with message;
// otherwise the step restarts fresh (documented degrade, not an error).
type resumeMsg struct {
	stepID  string
	message string
}

// resetMsg asks the scheduler to reset the run to targetID (spec 08 C2).
// handleReset rewinds the run branch to before the target's dependency closure,
// replays survivors, and returns the closure to pending for re-dispatch.
type resetMsg struct {
	stepID string
	reply  chan<- resetReply
}

type resetReply struct {
	result ResetResult
	err    error
}

// retryWakeMsg wakes the scheduler when a bounded backoff expires. The step
// remains pending while waiting, but nextReady observes retryNotBefore.
type retryWakeMsg struct{ stepID string }

type networkRequestMsg struct{ stepID string }

func (stepDoneMsg) isSchedMsg() {}

func (reviewSubmissionMsg) isSchedMsg() {}

func (userInputMsg) isSchedMsg() {}

func (snapshotReqMsg) isSchedMsg() {}

func (closureReqMsg) isSchedMsg() {}

func (agentInputMsg) isSchedMsg() {}

func (agentQuestionRequestMsg) isSchedMsg() {}

func (agentQuestionAnswerMsg) isSchedMsg() {}

func (agentQuestionAbandonMsg) isSchedMsg() {}

func (recoverMsg) isSchedMsg() {}

func (resolveIntegrationMsg) isSchedMsg() {}

func (resolveIntegrationWithAgentMsg) isSchedMsg() {}

func (integrationResolvedMsg) isSchedMsg() {}

func (finalMergeMsg) isSchedMsg() {}

func (stopMsg) isSchedMsg() {}

func (resumeMsg) isSchedMsg() {}

func (resetMsg) isSchedMsg() {}

func (retryWakeMsg) isSchedMsg() {}

func (networkRequestMsg) isSchedMsg() {}

// securityFindingMsg delivers a SecurityFinding to the scheduler inbox so the
// scheduler can escalate critical findings to the recovery gate without
// blocking the ctrl fan-out path.
type securityFindingMsg struct{ sf SecurityFinding }

func (securityFindingMsg) isSchedMsg() {}
