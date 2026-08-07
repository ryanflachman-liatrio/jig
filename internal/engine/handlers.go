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

// phSquashMergeIntegration squash-merges a successfully-completed mutating step's
// worktree into the run branch as one `jig-step:`-tagged commit and records
// stepCommits[stepID] = sha (spec 06 A1). It runs last in the chain so only a
// step that passed its validate gate and did not park on block_on integrates.
//
// This handler runs on the single-writer scheduler goroutine (stepDoneMsg is
// delivered through the inbox), so even though steps execute in parallel their
// integrations are serialized — that serialization is exactly what keeps the run
// branch a single linear history addressable by commit.
//
// No-ops when there is no run branch (persistence-off / non-git) or the step has
// no worktree (read-only step): nothing to integrate.
func phSquashMergeIntegration(s *scheduler, m stepDoneMsg, _ *workflow.Step) postExecDecision {
	if s.runBranch == "" {
		return decisionContinue
	}
	stepWorktree, ok := s.worktrees[m.stepID]
	if !ok {
		return decisionContinue // read-only step: no worktree, no commit
	}

	sha, conflict, err := squashMergeStep(
		s.repoRoot, s.runWorktree, stepWorktree, s.stepBranchName(m.stepID), m.stepID,
	)
	if err != nil {
		res := ensureResult(s, m.stepID)
		res.Status = step.StatusFailed
		if res.Err == "" {
			res.Err = "integrate step " + m.stepID + ": " + err.Error()
		}
		return decisionFailed
	}
	if conflict {
		// Task 3.0 replaces this with the integration-conflict gate. Until then,
		// clean the run worktree of the half-applied squash and fail the step.
		_, _ = gitCmd(s.runWorktree, "reset", "--hard")
		res := ensureResult(s, m.stepID)
		res.Status = step.StatusFailed
		if res.Err == "" {
			res.Err = "integration conflict merging step " + m.stepID
		}
		return decisionFailed
	}
	if sha != "" {
		s.stepCommits[m.stepID] = sha
	}
	return decisionContinue
}

// ensureResult returns the step's Result, creating a failed-status one if absent
// so a handler can record a failure reason without a nil check at each call site.
func ensureResult(s *scheduler, stepID string) *step.Result {
	res := s.states[stepID].Result
	if res == nil {
		res = &step.Result{Status: step.StatusFailed}
		s.states[stepID].Result = res
	}
	return res
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
