package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"jig/internal/workflow"
)

func TestSchedulerAcquireExecutionWorkspaceLifecycle(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	jigRoot := filepath.Join(repo, ".jig")
	if err := os.MkdirAll(jigRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	wf, err := workflow.Decode(`
[workflow]
name = "execution-view"
version = "0.1"

[[step]]
id = "reader"
type = "command"
run = "true"

[[step]]
id = "mutator"
type = "command"
run = "true"
isolation = "worktree"
`, "")
	if err != nil {
		t.Fatal(err)
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newScheduler(wf, "run-1", make(chan schedMsg), nil, &testExec{}, cancel, nil, "", jigRoot, repo, nil)
	if err := s.setupRunBranch(); err != nil {
		t.Fatalf("setup run branch: %v", err)
	}
	defer s.cleanupWorktrees()

	reader := s.stepByID("reader")
	firstView, err := s.acquireExecutionWorkspace(reader)
	if err != nil {
		t.Fatalf("acquire first read-only view: %v", err)
	}
	if firstView.Kind != executionWorkspaceReadOnlyView || firstView.Dir == "" || firstView.BaseSHA == "" {
		t.Fatalf("first reader workspace = %#v, want a populated read-only view", firstView)
	}
	if want := filepath.Join(jigRoot, "worktrees", s.runID, "views", reader.ID); firstView.Dir != want {
		t.Errorf("reader view path = %q, want %q", firstView.Dir, want)
	}

	if err := os.WriteFile(filepath.Join(s.runWorktree, "integrated.txt"), []byte("new run state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, s.runWorktree, "add", "integrated.txt")
	mustGit(t, s.runWorktree, "commit", "-m", "integrate reader input")
	if err := os.WriteFile(filepath.Join(firstView.Dir, "stale-view.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	secondView, err := s.acquireExecutionWorkspace(reader)
	if err != nil {
		t.Fatalf("recreate read-only view: %v", err)
	}
	if secondView.BaseSHA == firstView.BaseSHA {
		t.Errorf("reader retry reused base SHA %q; want the current run branch state", secondView.BaseSHA)
	}
	if _, err := os.Stat(filepath.Join(secondView.Dir, "stale-view.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("recreated reader view retained a file from the prior dispatch: %v", err)
	}
	if out, err := gitCmd(repo, "rev-parse", "--verify", s.executionViewBranchName(reader.ID)); err != nil {
		t.Fatalf("recreated reader branch missing: %v — %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(secondView.Dir, "integrated.txt")); err != nil {
		t.Errorf("recreated reader view did not include integrated file: %v", err)
	}

	mutator := s.stepByID("mutator")
	firstMutation, err := s.acquireExecutionWorkspace(mutator)
	if err != nil {
		t.Fatalf("acquire mutation workspace: %v", err)
	}
	secondMutation, err := s.acquireExecutionWorkspace(mutator)
	if err != nil {
		t.Fatalf("reacquire mutation workspace: %v", err)
	}
	if firstMutation.Kind != executionWorkspaceMutation || firstMutation.Dir == "" {
		t.Fatalf("mutation workspace = %#v, want a populated mutation worktree", firstMutation)
	}
	if secondMutation != firstMutation {
		t.Errorf("mutation workspace was recreated: got %#v, want %#v", secondMutation, firstMutation)
	}

	s.releaseExecutionWorkspaces()
	if len(s.executionViews) != 0 {
		t.Errorf("reader views still tracked after release: %#v", s.executionViews)
	}
	if _, err := os.Stat(secondView.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("reader view remains after release: %v", err)
	}
	if _, err := gitCmd(repo, "rev-parse", "--verify", s.executionViewBranchName(reader.ID)); err == nil {
		t.Error("reader view branch remains after release")
	}
	if _, err := os.Stat(firstMutation.Dir); err != nil {
		t.Errorf("release removed mutation workspace: %v", err)
	}
}

func TestSchedulerAcquireExecutionWorkspaceNoopWithoutRunSnapshot(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "execution-view"
version = "0.1"

[[step]]
id = "reader"
type = "command"
run = "true"

[[step]]
id = "mutator"
type = "command"
run = "true"
isolation = "worktree"
`, "")
	if err != nil {
		t.Fatal(err)
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newScheduler(wf, "run-1", make(chan schedMsg), nil, &testExec{}, cancel, nil, "", "", "", nil)

	for _, id := range []string{"reader", "mutator"} {
		workspace, err := s.acquireExecutionWorkspace(s.stepByID(id))
		if err != nil {
			t.Fatalf("acquire %s without run snapshot: %v", id, err)
		}
		if workspace.Dir != "" || workspace.BaseSHA != "" {
			t.Errorf("workspace without run snapshot = %#v, want no filesystem workspace", workspace)
		}
	}
	if len(s.worktrees) != 0 || len(s.executionViews) != 0 {
		t.Errorf("persistence-off acquisition created tracked workspaces: worktrees=%#v views=%#v", s.worktrees, s.executionViews)
	}
}
