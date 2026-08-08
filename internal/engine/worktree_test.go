package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// TestParseStaleWorktreePath verifies the parser that extracts the stale worktree
// path from a git "is already used by worktree at '<path>'" error message.
func TestParseStaleWorktreePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "real git error",
			input: "fatal: 'jig/feature/implement' is already used by worktree at '/Users/ryan/.jig/worktrees/old-run/_run'\n",
			want:  "/Users/ryan/.jig/worktrees/old-run/_run",
		},
		{
			name:  "no match",
			input: "fatal: something else went wrong",
			want:  "",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
	}
	for _, tc := range tests {
		got := parseStaleWorktreePath(tc.input)
		if got != tc.want {
			t.Errorf("%s: parseStaleWorktreePath(%q) = %q; want %q", tc.name, tc.input, got, tc.want)
		}
	}
}

// TestSanitizeBranchName verifies the sanitize helper used for git branch names.
func TestSanitizeBranchName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"feature", "feature"},
		{"my workflow", "my-workflow"},
		{"with/slash", "with-slash"},
		{"UPPER_case", "UPPER_case"},
		{"---leading-trailing---", "leading-trailing"},
		{"v1.2.3", "v1.2.3"},
		{"", ""},
	}
	for _, tc := range tests {
		got := sanitizeBranchName(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeBranchName(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// initRepo creates a bare-minimum git repository in dir with an initial commit.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.email", "test@jig.test"},
		{"git", "-C", dir, "config", "user.name", "Jig Test"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v — %s", args, err, out)
		}
	}
	// Seed with a file so the repo has a commit to branch from.
	seedPath := filepath.Join(dir, "seed.txt")
	if err := os.WriteFile(seedPath, []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "init"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v — %s", args, err, out)
		}
	}
}

// captureExec is a test Executor that records the StepRequest it receives and
// optionally writes a file into the worktree so there is a diff to capture.
type captureExec struct {
	requests map[string]StepRequest // stepID → last received request
}

func newCaptureExec() *captureExec {
	return &captureExec{requests: make(map[string]StepRequest)}
}

func (e *captureExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.requests[req.Step.ID] = req
	// Modify the tracked seed.txt so the diff is non-empty (new untracked files
	// don't appear in git-diff without staging; modifying tracked files does).
	if req.Worktree != "" {
		_ = os.WriteFile(filepath.Join(req.Worktree, "seed.txt"), []byte("modified by agent"), 0o644)
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// TestScheduler_WorktreePath verifies that a step with isolation = "worktree"
// receives a populated StepRequest.Worktree pointing to a real directory.
func TestScheduler_WorktreePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	jigRoot := filepath.Join(repoDir, ".jig")
	if err := os.MkdirAll(jigRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	const toml = `
[workflow]
name = "wt-path-test"
version = "0.1"

[[step]]
id = "mutate"
type = "agent"
isolation = "worktree"
allowed_tools = ["Write"]
skill = "skills/mutate"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	exec := newCaptureExec()
	mgr := NewManager(exec, jigRoot)
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := driveFinalMerge(t, ch, run, false)

	// The mutate step must have received a non-empty worktree path.
	req, ok := exec.requests["mutate"]
	if !ok {
		t.Fatal("mutate step was never dispatched")
	}
	if req.Worktree == "" {
		t.Error("StepRequest.Worktree must be non-empty for isolation=worktree step")
	}

	// Run must finish successfully.
	last := events[len(events)-1]
	rf, ok2 := last.(RunFinished)
	if !ok2 || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}; got %v", last)
	}
}

// TestScheduler_ReviewDiff verifies that a review step with review = "diff"
// receives a non-empty Diff in its ReviewRequest when a predecessor made changes
// in its worktree.
func TestScheduler_ReviewDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	jigRoot := filepath.Join(repoDir, ".jig")
	if err := os.MkdirAll(jigRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	const toml = `
[workflow]
name = "diff-review-test"
version = "0.1"

[[step]]
id = "mutate"
type = "agent"
isolation = "worktree"
allowed_tools = ["Write"]
skill = "skills/mutate"

[[step]]
id = "check"
type = "review"
depends_on = ["mutate"]
review = "diff"
output_type = { enum = ["approve", "reject"] }
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	exec := newCaptureExec()
	mgr := NewManager(exec, jigRoot)
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	var diffSeen string
	var events []Event
	deadline := time.After(10 * time.Second)
	resolved := false
	for {
		select {
		case e := <-ch:
			events = append(events, e)
			if rr, ok2 := e.(ReviewRequest); ok2 && rr.StepID == "check" && !resolved {
				resolved = true
				diffSeen = rr.Diff
				run.Resolve("check", "approve")
			}
			if _, ok2 := e.(FinalMergeRequest); ok2 {
				run.FinalMerge(false)
			}
			if _, ok2 := e.(RunFinished); ok2 {
				goto done
			}
		case <-deadline:
			t.Fatal("timeout waiting for ReviewRequest")
		}
	}
done:
	if !resolved {
		t.Error("ReviewRequest was never emitted for step 'check'")
	}
	if diffSeen == "" {
		t.Error("ReviewRequest.Diff must be non-empty when predecessor made worktree changes")
	}

	// Run must end not-failed.
	last := events[len(events)-1]
	rf, ok2 := last.(RunFinished)
	if !ok2 || rf.Failed {
		t.Errorf("want RunFinished{Failed:false}; got %v", last)
	}
}

// TestScheduler_WorktreeBranchReuseAcrossRuns verifies that sequential and
// concurrent runs of the same workflow don't collide on step branches. Step
// branches are now scoped per-run (jig/<workflow>/run-<runID>/<step>), so each
// run gets its own branches with no reset-or-create races.
func TestScheduler_WorktreeBranchReuseAcrossRuns(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	repoDir := t.TempDir()
	initRepo(t, repoDir)
	jigRoot := filepath.Join(repoDir, ".jig")
	if err := os.MkdirAll(jigRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	const toml = `
[workflow]
name = "rerun-test"
version = "0.1"

[[step]]
id = "mutate"
type = "agent"
isolation = "worktree"
allowed_tools = ["Write"]
skill = "skills/mutate"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(newCaptureExec(), jigRoot)
	_, ch := mgr.Subscribe()

	// Run the same workflow twice. Both must succeed with no recovery gate (which
	// is where a branch collision would surface).
	for i := 0; i < 2; i++ {
		run, err := mgr.Start(wf)
		if err != nil {
			t.Fatalf("run %d start: %v", i+1, err)
		}
		deadline := time.After(10 * time.Second)
	drain:
		for {
			select {
			case e := <-ch:
				switch ev := e.(type) {
				case RecoveryRequest:
					t.Fatalf("run %d entered recovery (branch collision not handled): %s", i+1, ev.Err)
				case FinalMergeRequest:
					run.FinalMerge(false)
				case RunFinished:
					if ev.Failed {
						t.Fatalf("run %d finished failed; want success on re-run", i+1)
					}
					break drain
				}
			case <-deadline:
				t.Fatalf("run %d: timeout waiting for RunFinished", i+1)
			}
		}
	}

	// Both run branches must be present (kept as integration history).
	out, err := gitCmd(repoDir, "branch", "--list", "jig/rerun-test/run-*")
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if strings.Count(strings.TrimSpace(string(out)), "\n")+1 < 2 {
		t.Errorf("expected 2 run branches (one per run), got: %s", out)
	}
}

// contains checks whether sub appears in s.
func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && func() bool {
		for i := range s {
			if i+len(sub) <= len(s) && s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
