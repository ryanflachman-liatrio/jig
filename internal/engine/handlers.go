package engine

import (
	"jig/internal/step"
	"jig/internal/workflow"
)

// postExecDecision is the signal returned by a post-execution handler.
type postExecDecision uint8

const (
	decisionContinue   postExecDecision = iota // pass to next handler
	decisionFailed                             // step failed — apply failure policy
	decisionNeedsInput                         // step paused for human input (handler handled transition)
)

// postExecHandler is one stage of the post-execution pipeline.
// The first non-decisionContinue result stops the chain.
// All state mutation (transitions, events) is performed by the handler itself.
type postExecHandler func(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision

// phCaptureWorktreeDiff snapshots the worktree diff after every execution
// so downstream review steps always see the most-recent state.
func phCaptureWorktreeDiff(s *scheduler, m stepDoneMsg, _ *workflow.Step) postExecDecision {
	if path, ok := s.worktrees[m.stepID]; ok {
		s.diffs[m.stepID] = captureDiff(path, s.wtBaseSHAs[m.stepID])
	}
	return decisionContinue
}

// phRunValidateGate runs [step.validate] synchronously when present.
// It transitions the step to StatusValidating, emits GateResult, and
// records the gate detail as the failure reason when the gate rejects.
func phRunValidateGate(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision {
	if wfStep == nil || wfStep.Validate == nil {
		return decisionContinue
	}
	from := s.states[m.stepID].Status
	s.transition(m.stepID, from, step.StatusValidating)
	passed, detail := s.runGate(wfStep, s.worktrees[m.stepID])
	s.emit(GateResult{RunID: s.runID, StepID: m.stepID, Passed: passed, Detail: detail})
	if !passed {
		res := s.states[m.stepID].Result
		if res == nil {
			res = &step.Result{}
			s.states[m.stepID].Result = res
		}
		res.Status = step.StatusFailed
		if res.Err == "" {
			res.Err = detail
		}
		return decisionFailed
	}
	return decisionContinue
}

// phCheckBlockOn parks the step at StatusNeedsInput when the block_on
// condition evaluates true against the step's own structured output.
func phCheckBlockOn(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision {
	if wfStep == nil || wfStep.BlockOn == "" {
		return decisionContinue
	}
	if s.evalBlockOn(m.stepID, wfStep) {
		curFrom := s.states[m.stepID].Status
		s.transition(m.stepID, curFrom, step.StatusNeedsInput)
		s.emit(InputRequest{RunID: s.runID, StepID: m.stepID})
		return decisionNeedsInput
	}
	return decisionContinue
}
