package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// composeExec is a fake Executor for integration tests. For each step it records
// the files present in the step's worktree at execute time (proving code
// composition), then writes the step's scripted files into that worktree. It can
// also fail the first N attempts of a step to exercise retry re-integration.
type composeExec struct {
	mu        sync.Mutex
	writes    map[string]map[string]string // stepID → filename → content to write
	failUntil map[string]int               // stepID → fail while req.Attempt < n
	present   map[string]map[string]bool   // stepID → filename → present at execute time (recorded)
}

func (e *composeExec) Execute(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := req.Step.ID
	if e.present == nil {
		e.present = map[string]map[string]bool{}
	}
	seen := map[string]bool{}
	if req.Worktree != "" {
		if entries, err := os.ReadDir(req.Worktree); err == nil {
			for _, en := range entries {
				seen[en.Name()] = true
			}
		}
	}
	e.present[id] = seen

	if n, ok := e.failUntil[id]; ok && req.Attempt < n {
		return &step.Result{Status: step.StatusFailed, Err: "scripted failure"}, nil
	}
	for name, content := range e.writes[id] {
		if req.Worktree != "" {
			_ = os.WriteFile(filepath.Join(req.Worktree, name), []byte(content), 0o644)
		}
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

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

// TestStepsComposeOnCode proves code composition: A writes file_a, B (depends on
// A) branches off the run branch after A integrated and therefore sees file_a,
// and the run branch ends with one jig-step-tagged commit per mutating step
// (spec 06 A1: worktree-off-run-HEAD, squash-merge, ordering).
func TestStepsComposeOnCode(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)

	const toml = `
[workflow]
name = "compose"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
isolation = "worktree"

[[step]]
id = "b"
type = "command"
run = "echo b"
isolation = "worktree"
depends_on = ["a"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &composeExec{writes: map[string]map[string]string{
		"a": {"file_a": "from a"},
		"b": {"file_b": "from b"},
	}}
	mgr := NewManager(exec, filepath.Join(repo, ".jig"))
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 10*time.Second)

	// B branched off the run branch after A integrated → its worktree has file_a.
	if !exec.present["b"]["file_a"] {
		t.Errorf("step b's worktree did not contain file_a; composition failed. present=%v", exec.present["b"])
	}
	// A's worktree branched off the working HEAD, before it wrote file_a.
	if exec.present["a"]["file_a"] {
		t.Errorf("step a's worktree unexpectedly already had file_a")
	}

	br := runBranchName("compose", run.ID)
	commits, err := stepCommitsFromLog(repo, br)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits["a"] == "" || commits["b"] == "" {
		t.Errorf("want one jig-step commit each for a,b; got %v", commits)
	}
}

// TestReadOnlyStepProducesNoCommit proves a read-only step (isolation none)
// advances the run branch HEAD by zero commits (spec 06 A1).
func TestReadOnlyStepProducesNoCommit(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	wantHEAD, err := currentHEAD(repo)
	if err != nil {
		t.Fatal(err)
	}

	const toml = `
[workflow]
name = "ro"
version = "0.1"

[[step]]
id = "r"
type = "command"
run = "echo r"
isolation = "none"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&composeExec{}, filepath.Join(repo, ".jig"))
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)

	br := runBranchName("ro", run.ID)
	out, err := gitCmd(repo, "rev-parse", br)
	if err != nil {
		t.Fatalf("run branch missing: %s", out)
	}
	if got := strings.TrimSpace(out); got != wantHEAD {
		t.Errorf("read-only step advanced run branch: tip %s != working HEAD %s", got, wantHEAD)
	}
	if commits, _ := stepCommitsFromLog(repo, br); len(commits) != 0 {
		t.Errorf("read-only step produced commits: %v", commits)
	}
}

// TestStepCommitMapReconstructable proves the stepID→sha map is rebuildable from
// the run-branch jig-step trailers, with the last step's commit at the tip
// (spec 06 A1).
func TestStepCommitMapReconstructable(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)

	const toml = `
[workflow]
name = "compose"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
isolation = "worktree"

[[step]]
id = "b"
type = "command"
run = "echo b"
isolation = "worktree"
depends_on = ["a"]
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &composeExec{writes: map[string]map[string]string{
		"a": {"file_a": "a"},
		"b": {"file_b": "b"},
	}}
	mgr := NewManager(exec, filepath.Join(repo, ".jig"))
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 10*time.Second)

	br := runBranchName("compose", run.ID)
	commits, err := stepCommitsFromLog(repo, br)
	if err != nil {
		t.Fatal(err)
	}
	tip := strings.TrimSpace(mustGit(t, repo, "rev-parse", br))
	if commits["b"] != tip {
		t.Errorf("newest step b sha %s != run branch tip %s", commits["b"], tip)
	}
	if commits["a"] == "" || commits["a"] == tip {
		t.Errorf("step a sha should exist and precede the tip; got %q (tip %s)", commits["a"], tip)
	}
}

// TestStepReintegratesAfterRetry is the FLAG-1 regression guard: a step that
// fails once and succeeds on retry (reusing its worktree) still integrates
// exactly once onto the run branch (spec 06 A1 + audit FLAG 1).
func TestStepReintegratesAfterRetry(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)

	const toml = `
[workflow]
name = "retry"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
isolation = "worktree"
on_failure = "retry"
max_retries = 1
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &composeExec{
		writes:    map[string]map[string]string{"a": {"file_a": "a"}},
		failUntil: map[string]int{"a": 1}, // fail attempt 0, succeed attempt 1
	}
	mgr := NewManager(exec, filepath.Join(repo, ".jig"))
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 10*time.Second)
	if rf := events[len(events)-1].(RunFinished); rf.Failed {
		t.Fatalf("run failed despite successful retry")
	}

	br := runBranchName("retry", run.ID)
	commits, err := stepCommitsFromLog(repo, br)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits["a"] == "" {
		t.Errorf("retried step should integrate exactly once; got %v", commits)
	}
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCmd(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v — %s", args, err, out)
	}
	return out
}
