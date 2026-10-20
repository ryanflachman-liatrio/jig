package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// applyFailurePolicy marks the step failed (or retries it) according to its
// on_failure policy.  Called from handle(stepDoneMsg) after exec or gate failure.
func (s *scheduler) applyFailurePolicy(stepID string, wfStep *workflow.Step) {
	policy := workflow.FailAbort // default
	if wfStep != nil && wfStep.OnFailure != "" {
		policy = wfStep.OnFailure
	}

	from := s.states[stepID].Status

	strat, ok := failurePolicyStrategies[policy]
	if !ok {
		strat = abortFailureStrategy{} // unrecognised policy — same fallback as before
	}
	strat.apply(s, stepID, wfStep, from)
}

// enterRecovery parks a failed step in step.StatusAwaitingRecovery and emits a
// RecoveryRequest so a human can retry (fresh), resume the failed agent session
// with the error fed back in, or abort. It replaces the previous s.cancel()
// teardown on unrecoverable failure: the run and any in-flight sibling steps
// keep running while the human decides. state.Result must already carry the
// failure (Err, and SessionID when an agent session ran).
func (s *scheduler) enterRecovery(stepID string) {
	state := s.states[stepID]
	if state.Result == nil {
		state.Result = &step.Result{Status: step.StatusFailed}
	}
	if s.rt(stepID).sessionResumeInFlight {
		// Spec 20 D19: a failed Recover(resume) must not keep advertising resume
		// of a likely-dead backend session.
		s.rt(stepID).failedSessionResume = true
		s.rt(stepID).sessionResumeInFlight = false
	}
	if !s.transition(stepID, state.Status, step.StatusAwaitingRecovery) {
		return
	}
	s.emit(RecoveryRequest{
		RunID:     s.runID,
		StepID:    stepID,
		Err:       state.Result.Err,
		CanResume: s.stepCanResume(stepID, state.Result.SessionID),
	})
}

// handleReset rewinds the run to before targetID's dependency closure (spec 08
// C2). It is only valid on an unfinished, quiescent run; settled runs and runs
// with a live worker are silent no-ops. The reset is a clean single-writer
// mutation: no lock is needed because the scheduler goroutine owns all state.
//
// Ordering is crash-consistent: the StepsReset audit event and the
// StepStatus(→pending) transitions are journaled *before* any destructive git
// or file operation. A crash after journaling leaves the journal (showing
// pending, no expected output) consistent with the deleted files.
func (s *scheduler) handleReset(m resetMsg) {
	reply := func(result ResetResult, err error) {
		if m.reply != nil {
			m.reply <- resetReply{result: result, err: err}
		}
	}
	// Guard: only on an unfinished, quiescent run with git persistence.
	if s.terminated {
		reply(ResetResult{}, &ResetError{Code: "settled", Target: m.stepID, Err: fmt.Errorf("run is already settled")})
		return
	}
	if s.slots.inFlight > 0 {
		reply(ResetResult{}, &ResetError{Code: "in_flight", Target: m.stepID, Err: fmt.Errorf("run has %d worker(s) in flight", s.slots.inFlight)})
		return
	}
	if s.branch.worktree == "" {
		reply(ResetResult{}, &ResetError{Code: "persistence_required", Target: m.stepID, Err: fmt.Errorf("run has no git worktree")})
		return
	}
	// A runtime fan-out child is not an independently resettable unit — it is
	// replaced wholesale on the family's next expansion. Point the operator at
	// the family instead of silently no-oping or resetting just one instance
	// out from under its siblings (docs/plans/a8-dynamic-foreach-fan-out.md,
	// "Reset, routes, worktrees, and budgets").
	if childState := s.states[m.stepID]; childState != nil && childState.ParentID != "" {
		reply(ResetResult{}, &ResetError{Code: "fanout_child", Target: m.stepID,
			Err: fmt.Errorf("%q is a fan-out child of %q; reset %q instead", m.stepID, childState.ParentID, childState.ParentID)})
		return
	}

	closure := s.closureOf(m.stepID)
	if len(closure) == 0 {
		reply(ResetResult{}, &ResetError{Code: "unknown_target", Target: m.stepID, Err: fmt.Errorf("step does not exist")})
		return
	}
	// closureOf/ResetClosure stay pure static-DAG operations (used for
	// operator previews); the actual scheduler-level reset must additionally
	// invalidate every family's current children — journal transitions,
	// artifact cleanup, worktree removal, and commit rewind all operate on
	// this expanded set.
	expanded := s.expandFanOutClosure(closure)

	rewindTo, survivors := s.rewindPlan(m.stepID)
	result := ResetResult{Target: m.stepID, Closure: append([]string(nil), expanded...), RewindTo: rewindTo}
	// A restored scheduler must not dispatch pending work between journal
	// invalidation and successful Git/artifact cleanup.
	s.restoredHold = true

	// Journal the audit event and StepStatus(→pending) transitions BEFORE any
	// destructive operation. A crash after this point leaves the journal in a
	// state consistent with the pending/empty-output files that follow.
	if err := s.emit(StepsReset{
		RunID:    s.runID,
		Target:   m.stepID,
		Closure:  expanded,
		RewindTo: rewindTo,
	}); err != nil {
		reply(ResetResult{}, &ResetError{Code: "journal", Target: m.stepID, Err: err})
		return
	}
	for _, id := range expanded {
		state := s.states[id]
		if state == nil {
			continue
		}
		if !s.transition(id, state.Status, step.StatusPending) {
			reply(ResetResult{}, &ResetError{Code: "journal", Target: m.stepID, Err: fmt.Errorf("persist pending transition for step %q", id)})
			return
		}
	}

	// Rewind the run branch and replay independent survivors.
	if rewindTo != "" {
		if out, err := gitCmd(s.branch.worktree, "reset", "--hard", rewindTo); err != nil {
			s.emit(RunError{RunID: s.runID,
				Err: fmt.Sprintf("reset: git reset --hard %s: %v — %s", rewindTo, err, strings.TrimSpace(out))})
			reply(ResetResult{}, &ResetError{Code: "git_reset", Target: m.stepID, Err: fmt.Errorf("git reset --hard %s: %w: %s", rewindTo, err, strings.TrimSpace(out))})
			return
		}
		for _, sha := range survivors {
			out, err := gitCmd(s.branch.worktree, "cherry-pick", sha)
			if err != nil {
				// Conflict: abort the cherry-pick and leave the run parked. The
				// conflicted paths are surfaced via IntegrationConflictRequest,
				// reusing the gate from Foundation A (spec 06 A2).
				_, _ = gitCmd(s.branch.worktree, "cherry-pick", "--abort")
				paths := mergeConflictPaths(s.branch.worktree)
				s.emit(IntegrationConflictRequest{RunID: s.runID, StepID: m.stepID, Paths: paths})
				s.emit(RunError{RunID: s.runID,
					Err: fmt.Sprintf("reset: cherry-pick %s: conflict — %s", sha, strings.TrimSpace(out))})
				reply(ResetResult{}, &ResetError{Code: "survivor_conflict", Target: m.stepID, Err: fmt.Errorf("cherry-pick %s: %w: %s", sha, err, strings.TrimSpace(out))})
				return
			}
		}
	}

	// Clear per-step derived outputs for the closure (result.json / output.*),
	// including every current fan-out child's own outputs. transcript.jsonl is
	// intentionally kept — the re-run appends a new generation. A family's own
	// fanout/generation-*.json manifests are untouched: ClearStepOutputs only
	// removes the fixed per-step artifact paths, never that subdirectory, so
	// older-generation manifests remain as immutable history.
	for _, id := range expanded {
		if err := s.runDir.Step(id).ClearOutputs(); err != nil {
			reply(ResetResult{}, &ResetError{Code: "artifact_cleanup", Target: m.stepID, Err: err})
			return
		}
	}

	// Reset in-memory state for each closure step (and every current fan-out
	// child) and purge stale routing maps.
	for _, id := range expanded {
		state := s.states[id]
		if state == nil {
			continue
		}
		state.Generation++
		state.Attempt = 0
		state.Iteration = 0
		state.Result = nil
		// status was already set to pending by the transition loop above

		// Remove the step worktree so re-dispatch creates a fresh one rooted at
		// the (now-rewound) run branch HEAD. Leaving a stale worktree would cause
		// squash-merge to diverge from the reset state.
		if wt, ok := s.worktrees[id]; ok {
			if err := removeWorktree(s.repoRoot, wt.path); err != nil {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("reset %q: remove worktree %s: %v", id, wt.path, err)})
			}
			if out, err := gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(id)); err != nil {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("reset %q: delete branch %s: %v — %s", id, s.stepBranchName(id), err, strings.TrimSpace(out))})
			}
			delete(s.worktrees, id)
		}

		delete(s.stepCommits, id)
		s.runtime[id] = s.rt(id).afterReset()
		delete(s.reviewSessions, id)
	}
	// Remove the current expansion from runtime scheduling for every family in
	// the (static) closure — a fresh FanOutExpanded/manifest is written the
	// next time the family becomes dependency-ready and dispatches (see
	// dispatchForEach). The now-orphaned child ids remain in s.states/
	// fanOut.children/fanOut.items as harmless historical residue (never
	// dispatched again, since they no longer appear in any family's Order) —
	// deleting the family entry itself is what stops RunSnapshot from folding
	// the just-reset children back in before the next expansion exists.
	for _, id := range closure {
		delete(s.foreach.families, id)
	}
	s.restoredHold = false
	reply(result, nil)
}

// handleRecover applies a human recovery decision to a step parked in
// step.StatusAwaitingRecovery.
func (s *scheduler) handleRecover(m recoverMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusAwaitingRecovery {
		return // stale or duplicate
	}

	switch m.action {
	case RecoverAbort:
		s.restoredHold = false
		s.aborted = true
		s.transition(m.stepID, state.Status, step.StatusFailed)
		s.cancel()

	case RecoverSkip:
		// Accept the failure and continue past it. The step stays failed, but
		// skippedByOperator marks it transparent so depsReady/anyPendingRunnable
		// treat it like an on_failure="continue" node — dependents proceed and the
		// run keeps going. No worker is re-dispatched; the recovery loop ends here.
		s.rt(m.stepID).skippedByOperator = true
		s.restoredHold = false
		s.transitionRecovery(m.stepID, state.Status, step.StatusFailed, RecoverSkip)

	case RecoverRetry, RecoverResume:
		if s.rt(m.stepID).recoverRounds >= maxRecoverRounds {
			s.emit(RunError{
				RunID: s.runID,
				Err:   fmt.Sprintf("step %q: exceeded maximum recovery rounds (%d)", m.stepID, maxRecoverRounds),
			})
			return // stay parked; the human can still abort
		}
		if m.action == RecoverResume {
			// Resume requires a captured session. Without one, the TUI should not
			// have offered resume; ignore rather than silently doing a fresh retry.
			if state.Result == nil || state.Result.SessionID == "" || !s.stepCanResume(m.stepID, state.Result.SessionID) {
				return
			}
			s.rt(m.stepID).resumeSession = state.Result.SessionID
			s.rt(m.stepID).message = composeRecoveryMessage(state.Result.Err, m.text)
		} else {
			// Spec 20 D20: fresh retry must not resume a stale backend session.
			s.rt(m.stepID).resumeSession = ""
			s.rt(m.stepID).message = ""
			s.rt(m.stepID).failedSessionResume = false
			if state.Result != nil {
				state.Result.SessionID = ""
			}
			if err := s.runDir.Step(m.stepID).ClearSession(); err != nil {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: clear session before retry: %v", m.stepID, err)})
				return
			}
			if err := s.discardMutationWorkspace(m.stepID); err != nil {
				s.emit(RunError{RunID: s.runID, Err: err.Error()})
				return
			}
		}
		s.rt(m.stepID).recoverRounds++
		state.Attempt++
		s.restoredHold = false
		s.transition(m.stepID, state.Status, step.StatusPending)
	}
}

func (s *scheduler) discardMutationWorkspace(stepID string) error {
	path := s.worktrees[stepID].path
	if path == "" {
		return nil
	}
	expected := filepath.Clean(filepath.Join(s.jigRoot, "worktrees", s.runID, stepID))
	if s.jigRoot == "" || filepath.Clean(path) != expected {
		return fmt.Errorf("step %q: refuse to discard unexpected worktree path %q", stepID, path)
	}
	leafInfo, leafErr := os.Lstat(path)
	if leafErr == nil && leafInfo.Mode()&os.ModeSymlink != 0 {
		// Never pass a leaf symlink to git worktree remove: git may resolve it to
		// a different registered tree. Remove only the canonical-path residue.
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("step %q: discard worktree symlink before retry: %w", stepID, err)
		}
		_, _ = gitCmd(s.repoRoot, "worktree", "prune")
	} else if leafErr != nil && !os.IsNotExist(leafErr) {
		return fmt.Errorf("step %q: inspect worktree before retry: %w", stepID, leafErr)
	} else if err := removeWorktree(s.repoRoot, path); err != nil {
		// A crash can leave a directory at the canonical path without valid git
		// worktree registration. Fresh retry explicitly discards that residue.
		info, statErr := os.Stat(path)
		switch {
		case statErr == nil && info.IsDir():
			if removeErr := os.RemoveAll(path); removeErr != nil {
				return fmt.Errorf("step %q: discard invalid worktree before retry: %w", stepID, removeErr)
			}
		case statErr != nil && !os.IsNotExist(statErr):
			return fmt.Errorf("step %q: inspect invalid worktree before retry: %w", stepID, statErr)
		}
		_, _ = gitCmd(s.repoRoot, "worktree", "prune")
	}
	delete(s.worktrees, stepID)
	s.rt(stepID).diff = ""
	// createWorktreeAt uses -B and therefore resets any surviving branch ref to
	// the current run tip. Branch deletion is only cleanup, not correctness.
	_, _ = gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(stepID))
	return nil
}

// composeRecoveryMessage builds the resume prompt for RecoverResume: the failed
// step's captured error plus any operator guidance, framed so the agent revisits
// its approach instead of repeating the mistake. Crash reopen uses a distinct
// preamble so the agent knows the process died mid-flight (Spec 20).
func composeRecoveryMessage(errText, guidance string) string {
	var b strings.Builder
	if strings.TrimSpace(errText) == processInterruptedErr {
		b.WriteString("The jig process exited while this step was running (")
		b.WriteString(processInterruptedErr)
		b.WriteString("). Partial transcript may exist on disk. Continue from the prior agent session; do not redo completed work unless necessary.\n\n")
	} else {
		b.WriteString("Your previous attempt failed with this error:\n\n")
		if strings.TrimSpace(errText) != "" {
			b.WriteString(errText)
		} else {
			b.WriteString("(no error detail was captured)")
		}
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(guidance) != "" {
		b.WriteString("Additional guidance from the operator:\n")
		b.WriteString(guidance)
		b.WriteString("\n\n")
	}
	b.WriteString("Review what went wrong and take a different approach to complete the task.")
	return b.String()
}

// closureOf returns the reset set for targetID: the target itself plus every
// step that transitively depends on it, in workflow declaration order.
// Independent parallel branches (steps with no transitive dependency on
// targetID) are excluded — they are survivors in a subsequent rewindPlan call.
func (s *scheduler) closureOf(targetID string) []string {
	if s.stepByID(targetID) == nil {
		return nil
	}
	return s.graph.inOrder(s.graph.descendants(nil, targetID))
}

// ResetClosure returns target plus every transitive dependent in workflow
// declaration order. It performs no scheduler, filesystem, lock, or Git work.
func ResetClosure(wf *workflow.Workflow, targetID string) []string {
	if wf == nil || targetID == "" {
		return nil
	}
	found := false
	for i := range wf.Steps {
		if wf.Steps[i].ID == targetID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	g := newStepGraph(wf)
	return g.inOrder(g.descendants(nil, targetID))
}

// expandFanOutClosure returns closure with every foreach family member
// replaced by itself plus its current runtime children (in source order),
// immediately after itself. closureOf/ResetClosure stay pure static-DAG
// operations for operator previews (docs/plans/a8-dynamic-foreach-fan-out.md,
// "Reset, routes, worktrees, and budgets"); this expansion happens only here,
// at the point scheduler-level reset performs destructive work — journal
// transitions, artifact cleanup, worktree removal, and commit rewind must all
// account for the family's actual current children, not just its declared id.
func (s *scheduler) expandFanOutClosure(closure []string) []string {
	out := make([]string, 0, len(closure))
	for _, id := range closure {
		out = append(out, id)
		if fam := s.foreach.families[id]; fam != nil {
			out = append(out, fam.Order...)
		}
	}
	return out
}

// rewindPlan computes the git operations needed to reset the run branch for a
// reset of targetID. It returns:
//
//   - rewindTo: the run-branch commit the caller should "git reset --hard" to
//     (the commit just before the earliest closure commit). Empty string when
//     there is nothing to rewind (no git repo, or no closure step has a commit).
//   - survivors: the ordered list of run-branch commit SHAs that are NOT in the
//     closure but sit after the rewind point; the caller cherry-picks these back
//     after the reset to preserve independent parallel branches.
func (s *scheduler) rewindPlan(targetID string) (rewindTo string, survivors []string) {
	if s.branch.worktree == "" || len(s.stepCommits) == 0 {
		return "", nil
	}

	// Every child integration commit belongs to its family for rewind purposes
	// (docs/plans/a8-dynamic-foreach-fan-out.md, "rewindPlan must treat every
	// child integration commit as belonging to its family"), so reset removes
	// all affected child commits together with the family's own and correctly
	// replays independent survivors in original run-branch order.
	closure := s.expandFanOutClosure(s.closureOf(targetID))
	closureSet := make(map[string]bool, len(closure))
	for _, id := range closure {
		closureSet[id] = true
	}

	// Collect the SHA of every closure step that has a commit on the run branch.
	closureSHAs := make(map[string]bool)
	for id, sha := range s.stepCommits {
		if closureSet[id] {
			closureSHAs[sha] = true
		}
	}
	if len(closureSHAs) == 0 {
		return "", nil // all closure steps are read-only (no commits); nothing to rewind
	}

	// Walk the run-branch commits oldest-first. Using runBaseSHA..HEAD keeps
	// us in the run branch's own history without touching the user's branch.
	out, err := gitCmd(s.branch.worktree, "log", "--format=%H", "--reverse",
		s.branch.baseSHA+"..HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", nil
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			commits = append(commits, sha)
		}
	}

	// Find the earliest commit that belongs to the closure.
	firstIdx := -1
	for i, sha := range commits {
		if closureSHAs[sha] {
			firstIdx = i
			break
		}
	}
	if firstIdx < 0 {
		return "", nil
	}

	// Rewind to the commit just before the earliest closure commit.
	// If the earliest closure commit is the very first run-branch commit,
	// rewind all the way back to the base SHA (the run-branch root).
	if firstIdx == 0 {
		rewindTo = s.branch.baseSHA
	} else {
		rewindTo = commits[firstIdx-1]
	}

	// Survivors: run-branch commits after the rewind point that are not in the
	// closure. The caller cherry-picks them (in order) to restore independent work.
	for _, sha := range commits[firstIdx+1:] {
		if !closureSHAs[sha] {
			survivors = append(survivors, sha)
		}
	}
	return rewindTo, survivors
}
