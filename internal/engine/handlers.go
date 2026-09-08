package engine

import (
	"path"
	"strings"

	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/workflow"
)

// postExecDecision is the Chain of Responsibility pattern's contract between
// links in scheduler.postExecChain (built in newScheduler, engine.go): each
// handler either passes the message to the next link (decisionContinue) or
// short-circuits the chain with a final verdict (decisionFailed,
// decisionNeedsInput). A handler that short-circuits is responsible for
// performing whatever state transition that verdict implies — the chain
// runner in (stepDoneMsg).execute (commands.go) only decides whether to keep
// walking or stop.
type postExecDecision uint8

const (
	decisionContinue   postExecDecision = iota // pass to next handler
	decisionFailed                             // step failed — apply failure policy
	decisionNeedsInput                         // step paused for human input (handler handled transition)
)

// postExecHandler is one link in the Chain of Responsibility: a stage of the
// post-execution pipeline. The first non-decisionContinue result stops the
// chain. All state mutation (transitions, events) is performed by the
// handler itself, not by the chain runner.
type postExecHandler func(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision

// phCaptureWorktreeDiff snapshots the worktree diff after every execution
// so downstream review steps always see the most-recent state.
func phCaptureWorktreeDiff(s *scheduler, m stepDoneMsg, _ *workflow.Step) postExecDecision {
	if path, ok := s.worktrees[m.stepID]; ok {
		s.diffs[m.stepID] = captureDiff(path, s.wtBaseSHAs[m.stepID])
		if result := s.states[m.stepID].Result; result != nil {
			result.ChangedFiles = changedFiles(path, s.wtBaseSHAs[m.stepID])
		}
	}
	return decisionContinue
}

// phValidateMutationPaths blocks integration before git add/merge when a
// worktree changed a path outside its declared allowlist. Workflows that omit
// mutation_paths keep their established integration behavior; declaring it
// opts into this fail-closed gate.
func phValidateMutationPaths(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision {
	if wfStep == nil || wfStep.Isolation != workflow.IsolationWorktree || len(wfStep.MutationPaths) == 0 {
		return decisionContinue
	}
	result := s.states[m.stepID].Result
	if result == nil || len(result.ChangedFiles) == 0 {
		return decisionContinue
	}
	var unexpected []string
	for _, changed := range result.ChangedFiles {
		if !mutationPathAllowed(changed, wfStep.MutationPaths) {
			unexpected = append(unexpected, changed)
		}
	}
	if len(unexpected) == 0 {
		return decisionContinue
	}
	result.Status = step.StatusFailed
	result.Err = "unexpected worktree changes outside mutation_paths: " + strings.Join(unexpected, ", ")
	return decisionFailed
}

func mutationPathAllowed(changed string, allowed []string) bool {
	for _, pattern := range allowed {
		if prefix, ok := strings.CutSuffix(pattern, "/**"); ok && (changed == prefix || strings.HasPrefix(changed, prefix+"/")) {
			return true
		}
		if matched, _ := path.Match(pattern, changed); matched {
			return true
		}
	}
	return false
}

// phRunValidateGate runs [step.validate] synchronously when present.
// It transitions the step to StatusValidating, emits GateResult, and
// records the gate detail as the failure reason when the gate rejects.
func phRunValidateGate(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision {
	if wfStep == nil || wfStep.Validate == nil {
		return decisionContinue
	}
	// Spec 20 D9: flush SessionID to session.json before entering validating so a
	// crash in this narrow window still leaves a durable resume identity.
	if res := s.states[m.stepID].Result; res != nil && res.SessionID != "" && s.runDir != "" {
		if err := datastore.WriteSession(s.runDir, m.stepID, datastore.SessionInfo{
			SessionID:  res.SessionID,
			Backend:    wfStep.Backend,
			Transport:  wfStep.Transport,
			Attempt:    s.states[m.stepID].Attempt,
			Iteration:  s.states[m.stepID].Iteration,
			Generation: s.states[m.stepID].Generation,
		}); err != nil {
			res.Status = step.StatusFailed
			res.Err = "persist session before validation: " + err.Error()
			return decisionFailed
		}
	}
	from := s.states[m.stepID].Status
	s.transition(m.stepID, from, step.StatusValidating)
	passed, detail := s.runGate(wfStep, s.executionDirForStep(m.stepID))
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
		// Surface the conflict to a human (spec 06 A2) instead of failing or
		// auto-resolving. The conflicted state is left in the run worktree for the
		// operator to resolve; the step parks but the run stays alive. Resolution
		// arrives via Run.ResolveIntegration → handleResolveIntegration.
		paths := mergeConflictPaths(s.runWorktree)
		from := s.states[m.stepID].Status
		s.transition(m.stepID, from, step.StatusAwaitingIntegration)
		s.emit(s.integrationConflictRequest(m.stepID, paths, ""))
		return decisionNeedsInput
	}
	if sha != "" {
		s.stepCommits[m.stepID] = sha
	}
	return decisionContinue
}

func (s *scheduler) integrationConflictRequest(stepID string, paths []string, resolution string) IntegrationConflictRequest {
	st := s.stepByID(stepID)
	return IntegrationConflictRequest{
		RunID: s.runID, StepID: stepID, Paths: paths, Worktree: s.runWorktree,
		CanAgentResolve: s.resolver != nil && st != nil && st.Type == workflow.StepAgent,
		Resolution:      resolution,
	}
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
