package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jig/internal/workflow"
)

// TestRunBranchCreatedAtWorkingHead verifies that starting a run in a git repo
// creates the per-run integration branch jig/<workflow>/run-<runID> rooted at the
// working-branch HEAD (spec 06, Unit A1 run-branch creation).
func TestRunBranchCreatedAtWorkingHead(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	wantHEAD, err := currentHEAD(repo)
	if err != nil {
		t.Fatalf("working HEAD: %v", err)
	}

	const toml = `
[workflow]
name = "compose"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo hi"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(&testExec{}, filepath.Join(repo, ".jig"))
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)

	branch := runBranchName("compose", run.ID)
	out, err := gitCmd(repo, "rev-parse", "--verify", branch)
	if err != nil {
		t.Fatalf("run branch %q not found after run: %s", branch, strings.TrimSpace(out))
	}
	if got := strings.TrimSpace(out); got != wantHEAD {
		t.Errorf("run branch tip = %s; want working HEAD %s", got, wantHEAD)
	}
}

// TestRunBranchNoopWhenNoRepoRoot verifies the persistence-off / non-git no-op
// path: with no repo root the run completes and no run branch is created
// (spec 06, Unit A1 no-op). Run under -race.
func TestRunBranchNoopWhenNoRepoRoot(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)

	const toml = `
[workflow]
name = "compose"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo hi"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	// root "" ⇒ repoRoot "" ⇒ no run branch, steps run in place.
	mgr := NewManager(&testExec{}, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)

	last := events[len(events)-1]
	if rf, ok := last.(RunFinished); !ok || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}; got %v", last)
	}
	if out, _ := gitCmd(repo, "branch", "--list", "jig/*"); strings.TrimSpace(out) != "" {
		t.Errorf("persistence-off path created a jig branch: %q", strings.TrimSpace(out))
	}
}
