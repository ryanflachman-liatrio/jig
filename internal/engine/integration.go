package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// runBranch is the run's integration branch (spec 06). Every field is "" on
// the persistence-off / non-git path, and integration keys off that.
type runBranch struct {
	name     string // per-run integration branch name
	worktree string // run worktree absolute path
	baseSHA  string // working-branch HEAD the run branch was rooted at
	base     string // user's working branch name (final-merge target)
}

// setupRunBranch creates the per-run integration branch at the working-branch
// HEAD and a run worktree checked out on it, recorded on the scheduler. It runs
// once at run start on the scheduler goroutine, before the dispatch loop.
//
// Persistence-off / non-git (repoRoot == "") is a first-class no-op: runBranch
// and runWorktree stay "" and every downstream integration step keys off that,
// so steps run in place exactly as before spec 06.
func (s *scheduler) setupRunBranch() error {
	if s.repoRoot == "" {
		return nil
	}
	// repoRoot can be set for persistence without the parent actually being a git
	// work tree (e.g. a plain temp dir in tests, or jig run outside a repo). That
	// is the non-git case, which spec 06 designates a first-class no-op: no run
	// branch, steps run in place. Probe for a real HEAD and degrade rather than
	// failing the run. The only hard error left is a genuine worktree-add failure
	// on a valid repo.
	head, err := currentHEAD(s.repoRoot)
	if err != nil {
		return nil
	}
	branch := runBranchName(s.wf.Meta.Name, s.runID)
	// createWorktree does `git worktree add -B <branch> <path>`, which creates the
	// branch at the repo's current HEAD (the user's working-branch HEAD) and checks
	// it out in a fresh worktree — exactly the run-branch root spec 06 requires.
	wtPath := filepath.Join(s.jigRoot, "worktrees", s.runID, "_run")
	if _, err := createWorktree(s.repoRoot, wtPath, branch); err != nil {
		return err
	}
	s.branch.name = branch
	s.branch.worktree = wtPath
	s.branch.baseSHA = head
	// Record the user's working branch as the final-merge target (spec 06 A3).
	// A detached HEAD yields "HEAD" here; the final merge still lands onto whatever
	// repoRoot has checked out, so the name is informational for the gate.
	if out, berr := gitCmd(s.repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); berr == nil {
		s.branch.base = strings.TrimSpace(out)
	}
	return nil
}

// cleanupWorktrees removes all active step worktrees (and their ephemeral
// per-run branches) plus the run worktree. It is idempotent: it clears
// s.worktrees and s.branch.worktree as it goes, so a second call is a no-op.
// Callers must invoke this BEFORE emitting RunFinished so that the git
// subprocesses finish before the caller's goroutine signals completion.
func (s *scheduler) cleanupWorktrees() {
	s.releaseExecutionWorkspaces()
	if s.repoRoot == "" {
		return
	}
	// Best-effort: this also runs as the deferred safety net after RunFinished,
	// when no further event may be journaled. Leftovers are reclaimed by the
	// stale worktree retry in createWorktreeAt.
	for stepID, wt := range s.worktrees {
		_ = removeWorktree(s.repoRoot, wt.path)
		_, _ = gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(stepID))
		delete(s.worktrees, stepID)
	}
	// Remove the run worktree but KEEP the run branch: it is the run's
	// integration history, left for inspection and for the final merge gate.
	if s.branch.worktree != "" {
		_ = removeWorktree(s.repoRoot, s.branch.worktree)
		s.branch.worktree = ""
	}
}

// stepBranchName returns the per-run, per-step branch name
// jig/<workflow>/<runID>/<stepID>. Including the runID (without the "run-"
// prefix used by run branches) mirrors the worktree filesystem layout and
// prevents concurrent runs of the same workflow from colliding on step
// branches. The "run-" prefix is intentionally absent so that git's ref
// storage does not conflict: run branch jig/<wf>/run-<id> is a ref file
// while step branches jig/<wf>/<id>/<step> live under a sibling directory.
// Both worktree creation and squash-merge use this function as the single
// source of truth.
func (s *scheduler) stepBranchName(stepID string) string {
	return "jig/" + sanitizeBranchName(s.wf.Meta.Name) + "/" + sanitizeBranchName(s.runID) + "/" + stepID
}

// handleResolveIntegration applies a human decision to a step parked in
// step.StatusAwaitingIntegration after a squash-merge conflict (spec 06 A2).
//
//   - resolve (abort=false): the operator merged the conflict in the run
//     worktree; the engine finishes the integration by committing the resolved
//     tree with the jig-step trailer, records stepCommits, and succeeds the step.
//   - abort (abort=true): discard the half-applied squash and fail the step,
//     routing it to the recovery gate via applyFailurePolicy.
func (s *scheduler) handleResolveIntegration(m resolveIntegrationMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusAwaitingIntegration {
		return // stale or duplicate
	}
	if s.rt(m.stepID).resolvingSeq != 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent conflict resolution in progress; wait for it before resolving or aborting", m.stepID)})
		return
	}
	s.restoredHold = false

	if m.abort {
		// Discard the conflicted/staged squash so the run worktree is clean, then
		// fail the step through the normal failure policy (→ recovery gate).
		if out, err := gitCmd(s.branch.worktree, "reset", "--hard"); err != nil {
			// The run worktree still holds the conflicted squash; failing the step
			// now would let the next integration build on it. Stay parked.
			s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("abort integration for %q: reset run worktree: %v — %s", m.stepID, err, strings.TrimSpace(out))})
			return
		}
		res := s.states[m.stepID].Result
		if res == nil {
			res = &step.Result{}
			s.states[m.stepID].Result = res
		}
		res.Status = step.StatusFailed
		if res.Err == "" {
			res.Err = fmt.Sprintf("integration conflict for step %q aborted by operator", m.stepID)
		}
		s.applyFailurePolicy(m.stepID, s.stepByID(m.stepID))
		return
	}

	// Resolve: refuse while unmerged paths remain — the operator must `git add`
	// their resolution first (standard git conflict resolution).
	if len(mergeConflictPaths(s.branch.worktree)) > 0 {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q: unresolved conflicts remain in the run worktree", m.stepID),
		})
		return // stay parked
	}
	if out, err := gitCmd(s.branch.worktree, "add", "-A"); err != nil {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("stage resolved conflict for %q: %v — %s", m.stepID, err, strings.TrimSpace(out)),
		})
		return
	}
	msg := m.stepID + "\n\njig-step: " + m.stepID
	if out, err := gitCmd(s.branch.worktree, "commit", "-m", msg); err != nil {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("commit resolved conflict for %q: %v — %s", m.stepID, err, strings.TrimSpace(out)),
		})
		return // stay parked
	}
	if sha, err := currentHEAD(s.branch.worktree); err == nil {
		s.stepCommits[m.stepID] = sha
	}
	wfStep := s.stepByID(m.stepID)
	s.transition(m.stepID, step.StatusAwaitingIntegration, step.StatusSucceeded)
	if wfStep != nil {
		s.recordRoutes(m.stepID, wfStep, "")
	}
}

// handleResolveIntegrationWithAgent starts the agent conflict resolver for a
// step parked on an integration conflict. The resolver runs an agent, so it
// executes off the scheduler goroutine under the run context; its outcome
// returns as an integrationResolvedMsg. The step stays AwaitingIntegration
// throughout, so a crash mid-resolution reopens on the same gate.
func (s *scheduler) handleResolveIntegrationWithAgent(m resolveIntegrationWithAgentMsg) {
	state := s.states[m.stepID]
	st := s.stepByID(m.stepID)
	if state == nil || state.Status != step.StatusAwaitingIntegration || s.resolver == nil || !hasAgentSession(st) {
		return
	}
	if s.rt(m.stepID).resolvingSeq != 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent conflict resolution already in progress", m.stepID)})
		return
	}
	s.restoredHold = false
	paths := mergeConflictPaths(s.branch.worktree)
	if len(paths) == 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: no unresolved integration conflict remains", m.stepID)})
		return
	}
	s.integrationSeq++
	seq := s.integrationSeq
	s.rt(m.stepID).resolvingSeq = seq
	ctx, resolver, inbox := s.runCtx, s.resolver, s.inbox
	req := IntegrationResolutionRequest{RunID: s.runID, Step: st, Worktree: s.branch.worktree, Conflicts: paths}
	go func() {
		result, err := resolver.ResolveIntegration(ctx, req, conflictReporter{})
		select {
		case inbox <- integrationResolvedMsg{stepID: req.Step.ID, seq: seq, paths: paths, result: result, err: err}:
		case <-ctx.Done():
		}
	}()
}

// handleIntegrationResolved applies the agent conflict resolver's outcome. A
// result for a superseded request (reset or abort while the agent ran) is
// dropped.
func (s *scheduler) handleIntegrationResolved(m integrationResolvedMsg) {
	if s.rt(m.stepID).resolvingSeq != m.seq {
		return
	}
	s.rt(m.stepID).resolvingSeq = 0
	state := s.states[m.stepID]
	st := s.stepByID(m.stepID)
	if state == nil || state.Status != step.StatusAwaitingIntegration || st == nil {
		return
	}
	if m.err != nil || m.result == nil || m.result.Status == step.StatusFailed {
		detail := "agent conflict resolver failed"
		if m.err != nil {
			detail += ": " + m.err.Error()
		} else if m.result != nil && m.result.Err != "" {
			detail += ": " + m.result.Err
		}
		s.emit(RunError{RunID: s.runID, Err: detail})
		return
	}
	if unresolved := mergeConflictPaths(s.branch.worktree); len(unresolved) > 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent left unresolved conflicts: %s", m.stepID, strings.Join(unresolved, ", "))})
		return
	}
	if len(st.MutationPaths) > 0 {
		changed := strings.Fields(strings.TrimSpace(gitOutput(s.branch.worktree, "diff", "--name-only", "HEAD")))
		for _, path := range changed {
			if !mutationPathAllowed(path, st.MutationPaths) {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent changed %q outside mutation_paths", m.stepID, path)})
				return
			}
		}
	}
	s.emit(s.integrationConflictRequest(m.stepID, m.paths, "agent proposal staged; review the diff, then finalize"))
}

func gitOutput(dir string, args ...string) string {
	out, _ := gitCmd(dir, args...)
	return out
}

type conflictReporter struct{}

func (conflictReporter) Output(string) {}

func (conflictReporter) ToolCall(string, string) {}

func (conflictReporter) Message(int, int) {}

func (conflictReporter) Finding(SecurityFinding) {}

func (conflictReporter) Question(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
	return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
}

// requestFinalMergeIfNeeded presents the final-merge gate the first time the run
// reaches terminal with a non-empty run branch (spec 06 A3), returning true to
// keep the run parked-but-alive. It returns false — finish normally — on the
// persistence-off / non-git path, when the run failed (nothing clean to land), or
// when the run branch gained no commits beyond its base. Idempotent: once the
// gate is emitted it keeps returning true until Run.FinalMerge settles the run.
func (s *scheduler) requestFinalMergeIfNeeded() bool {
	if s.awaitingFinalMerge {
		return true // gate already presented; still waiting on the operator
	}
	if s.branch.name == "" || s.branch.worktree == "" || s.anyFailed() {
		return false
	}
	head, err := currentHEAD(s.branch.worktree)
	if err != nil || head == s.branch.baseSHA {
		return false // run branch is empty — nothing to merge
	}
	s.awaitingFinalMerge = true
	s.emit(FinalMergeRequest{RunID: s.runID, RunBranch: s.branch.name, Base: s.branch.base})
	return true
}

// handleFinalMerge applies the operator's final-merge decision (spec 06 A3).
// Approve merges the run branch onto the base working branch; discard leaves the
// run branch in place. Either outcome settles the run: it emits RunFinished and
// sets terminated so run()'s loop returns. This is a pre-RunFinished completion
// step — there is deliberately no RunResumed and no post-finish re-entry.
func (s *scheduler) handleFinalMerge(m finalMergeMsg) {
	if !s.awaitingFinalMerge {
		return // stale or duplicate
	}
	if m.approve {
		conflict, err := finalMerge(s.repoRoot, s.branch.base, s.branch.name)
		if err != nil {
			s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("final merge: %v", err)})
			return // stay parked; the operator can retry or discard
		}
		if conflict {
			// The run branch conflicts with concurrent work on the base. finalMerge
			// aborted the half-applied merge, so the working tree is clean; surface it
			// and stay parked so the operator can discard (or fix base and re-approve).
			s.emit(RunError{
				RunID: s.runID,
				Err:   fmt.Sprintf("final merge conflicts with %s; run branch %s left for manual merge", s.branch.base, s.branch.name),
			})
			return
		}
	}
	// Approved-and-merged, or discarded: the run settles here.
	s.awaitingFinalMerge = false
	s.terminated = true
	s.cleanupWorktrees()
	s.emit(RunFinished{RunID: s.runID, Failed: s.anyFailed(), Cause: s.terminalCause()})
}
