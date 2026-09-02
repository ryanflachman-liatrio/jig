package engine

import (
	"fmt"
	"path/filepath"

	"jig/internal/workflow"
)

type executionWorkspaceKind uint8

const (
	executionWorkspaceMutation executionWorkspaceKind = iota
	executionWorkspaceReadOnlyView
)

// executionWorkspace is the repository view chosen by the scheduler at
// dispatch time. Readers receive an isolated view of the integrated run state;
// their directories must never enter the mutation-worktree lifecycle.
type executionWorkspace struct {
	Dir     string
	BaseSHA string
	Kind    executionWorkspaceKind
}

// acquireExecutionWorkspace selects the repository view for one dispatch.
// Mutating steps retain their worktree only while the run branch has not moved.
// Once an iteration integrates, its squash commit deliberately has different
// ancestry from the step branch; a routed iteration must start from the new run
// tip or its already-integrated edits would be merged a second time. Read-only
// views are replaced for every dispatch so a retry observes the current
// integrated run state instead of a stale snapshot.
func (s *scheduler) acquireExecutionWorkspace(st *workflow.Step) (executionWorkspace, error) {
	kind := executionWorkspaceReadOnlyView
	if st.Isolation == workflow.IsolationWorktree {
		kind = executionWorkspaceMutation
	}
	workspace := executionWorkspace{Kind: kind}

	// An empty run worktree is the persistence-off/non-git path. There is no
	// durable repository state to snapshot, so callers retain their existing
	// configured-CWD behavior.
	if s.runWorktree == "" {
		return workspace, nil
	}

	if kind == executionWorkspaceMutation {
		if existing, ok := s.worktrees[st.ID]; ok {
			tip, err := currentHEAD(s.runWorktree)
			if err != nil {
				return executionWorkspace{}, fmt.Errorf("read run branch tip for step %q: %w", st.ID, err)
			}
			if s.wtBaseSHAs[st.ID] == tip {
				workspace.Dir = existing
				workspace.BaseSHA = tip
				return workspace, nil
			}
			if err := removeWorktree(s.repoRoot, existing); err != nil {
				return executionWorkspace{}, fmt.Errorf("replace stale mutation workspace for step %q: %w", st.ID, err)
			}
			if _, err := gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(st.ID)); err != nil {
				return executionWorkspace{}, fmt.Errorf("delete stale mutation branch for step %q: %w", st.ID, err)
			}
			delete(s.worktrees, st.ID)
			delete(s.wtBaseSHAs, st.ID)
		}

		path := filepath.Join(s.jigRoot, "worktrees", s.runID, st.ID)
		baseSHA, err := createWorktreeAt(s.repoRoot, path, s.stepBranchName(st.ID), s.runBranch)
		if err != nil {
			return executionWorkspace{}, fmt.Errorf("create mutation workspace for step %q: %w", st.ID, err)
		}
		s.worktrees[st.ID] = path
		s.wtBaseSHAs[st.ID] = baseSHA
		workspace.Dir = path
		workspace.BaseSHA = baseSHA
		return workspace, nil
	}

	if existing, ok := s.executionViews[st.ID]; ok {
		s.releaseExecutionView(st.ID, existing)
	}

	path := filepath.Join(s.jigRoot, "worktrees", s.runID, "views", st.ID)
	baseSHA, err := createWorktreeAt(s.repoRoot, path, s.executionViewBranchName(st.ID), s.runBranch)
	if err != nil {
		return executionWorkspace{}, fmt.Errorf("create read-only execution view for step %q: %w", st.ID, err)
	}
	workspace.Dir = path
	workspace.BaseSHA = baseSHA
	s.executionViews[st.ID] = workspace
	return workspace, nil
}

func (s *scheduler) executionViewBranchName(stepID string) string {
	return "jig/" + sanitizeBranchName(s.wf.Meta.Name) + "/" + sanitizeBranchName(s.runID) + "/view/" + stepID
}

func (s *scheduler) releaseExecutionView(stepID string, workspace executionWorkspace) {
	if workspace.Dir != "" && s.repoRoot != "" {
		_ = removeWorktree(s.repoRoot, workspace.Dir)
		_, _ = gitCmd(s.repoRoot, "branch", "-D", s.executionViewBranchName(stepID))
	}
	delete(s.executionViews, stepID)
}

func (s *scheduler) releaseExecutionViewForStep(stepID string) {
	if workspace, ok := s.executionViews[stepID]; ok {
		s.releaseExecutionView(stepID, workspace)
	}
}

// executionDirForStep returns the dispatch snapshot still held for a running
// or just-completed step. Reader views live outside the mutation-worktree map
// so validation cannot accidentally fall back to the caller's checkout.
func (s *scheduler) executionDirForStep(stepID string) string {
	st := s.stepByID(stepID)
	if st != nil && st.Isolation == workflow.IsolationWorktree {
		return s.worktrees[stepID]
	}
	if workspace, ok := s.executionViews[stepID]; ok {
		return workspace.Dir
	}
	return ""
}

// releaseExecutionWorkspaces removes reader views without touching mutating
// worktrees or the run worktree, which have distinct lifecycle requirements.
func (s *scheduler) releaseExecutionWorkspaces() {
	for stepID, workspace := range s.executionViews {
		s.releaseExecutionView(stepID, workspace)
	}
}
