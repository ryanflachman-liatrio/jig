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
// Mutating steps retain their worktree across retries and loop iterations so
// their edits accumulate. Read-only views are replaced for every dispatch so a
// retry observes the current integrated run state instead of a stale snapshot.
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
			workspace.Dir = existing
			workspace.BaseSHA = s.wtBaseSHAs[st.ID]
			return workspace, nil
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

// releaseExecutionWorkspaces removes reader views without touching mutating
// worktrees or the run worktree, which have distinct lifecycle requirements.
func (s *scheduler) releaseExecutionWorkspaces() {
	for stepID, workspace := range s.executionViews {
		s.releaseExecutionView(stepID, workspace)
	}
}
