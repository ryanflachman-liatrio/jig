package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/workflow"
)

// buildScheduler constructs a minimal scheduler for testing pure DAG logic.
// No executor, no persistence, no git — only s.wf is populated.
func buildScheduler(t *testing.T, toml string) *scheduler {
	t.Helper()
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatalf("workflow.Decode: %v", err)
	}
	noop := func() {}
	inbox := make(chan schedMsg, 8)
	return newScheduler(wf, "test-run", inbox, nil, &testExec{}, noop, nil, "", "", "", func(RunSnapshot) {})
}

// TestResetClosure verifies closureOf returns the correct reset set.
func TestResetClosure(t *testing.T) {
	// Graph: A has no deps; B depends_on A; C depends_on B; D is independent.
	const toml = `
[workflow]
name = "reset-closure"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]

[[step]]
id = "c"
type = "command"
run = "echo c"
depends_on = ["b"]

[[step]]
id = "d"
type = "command"
run = "echo d"
`
	tests := []struct {
		target string
		want   []string
	}{
		// Resetting A pulls in B and C (both transitively depend on A); D is independent.
		{"a", []string{"a", "b", "c"}},
		// Resetting B pulls in C (depends on B); A and D are unaffected.
		{"b", []string{"b", "c"}},
		// Resetting C: nothing depends on C.
		{"c", []string{"c"}},
		// Resetting D: nothing depends on D.
		{"d", []string{"d"}},
	}
	s := buildScheduler(t, toml)
	for _, tc := range tests {
		got := s.closureOf(tc.target)
		if len(got) != len(tc.want) {
			t.Errorf("closureOf(%q) = %v; want %v", tc.target, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("closureOf(%q)[%d] = %q; want %q (full: %v)", tc.target, i, got[i], tc.want[i], got)
				break
			}
		}
	}
}

// TestRewindPlan verifies rewindPlan returns the correct rewind point and
// survivor list for a set of scripted run-branch commits.
func TestRewindPlan(t *testing.T) {
	// Graph: A (no deps), B (depends_on A), C (depends_on B), D (independent).
	const toml = `
[workflow]
name = "reset-plan"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"

[[step]]
id = "b"
type = "command"
run = "echo b"
depends_on = ["a"]

[[step]]
id = "c"
type = "command"
run = "echo c"
depends_on = ["b"]

[[step]]
id = "d"
type = "command"
run = "echo d"
`
	// Set up a real git repo with scripted run-branch commits so rewindPlan can
	// walk the git log. Commit order mirrors a real run where A ran first, then D
	// (independent, scheduled while A's downstream started), then B.
	repo := t.TempDir()
	initRepo(t, repo)

	baseSHA, err := currentHEAD(repo)
	if err != nil {
		t.Fatalf("currentHEAD: %v", err)
	}

	// Create a run worktree on a run branch rooted at baseSHA.
	runBranch := "jig/reset-plan/run-testrun"
	wtPath := filepath.Join(t.TempDir(), "run-wt")
	if _, err := createWorktreeAt(repo, wtPath, runBranch, "HEAD"); err != nil {
		t.Fatalf("createWorktreeAt: %v", err)
	}

	// Helper: write a file and commit with a jig-step trailer in the run worktree.
	commit := func(stepID, content string) string {
		f := filepath.Join(wtPath, stepID+".txt")
		if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
		for _, args := range [][]string{
			{"add", "-A"},
			{"commit", "-m", stepID + "\n\njig-step: " + stepID},
		} {
			if out, err := gitCmd(wtPath, args...); err != nil {
				t.Fatalf("git %v: %v — %s", args, err, strings.TrimSpace(out))
			}
		}
		sha, err := currentHEAD(wtPath)
		if err != nil {
			t.Fatalf("currentHEAD after commit: %v", err)
		}
		return sha
	}

	// Commit order: A, D, B (C not committed yet — run paused at quiescent gate).
	shaA := commit("a", "a output")
	shaD := commit("d", "d output")
	shaB := commit("b", "b output")

	// Build a scheduler with the run worktree and stepCommits populated.
	s := buildScheduler(t, toml)
	s.runWorktree = wtPath
	s.runBaseSHA = baseSHA
	s.stepCommits = map[string]string{
		"a": shaA,
		"d": shaD,
		"b": shaB,
	}

	// Reset of "a": earliest closure commit is A (index 0); survivors = [D].
	t.Run("fanout_reset_a", func(t *testing.T) {
		rewindTo, survivors := s.rewindPlan("a")
		if rewindTo != baseSHA {
			t.Errorf("rewindTo = %q; want runBaseSHA %q", rewindTo, baseSHA)
		}
		if len(survivors) != 1 || survivors[0] != shaD {
			t.Errorf("survivors = %v; want [%s]", survivors, shaD)
		}
	})

	// Reset of "b": B is the only committed closure step; D is before B but not
	// in closure, so rewindTo = D's SHA; no survivors after B.
	t.Run("linear_reset_b", func(t *testing.T) {
		rewindTo, survivors := s.rewindPlan("b")
		if rewindTo != shaD {
			t.Errorf("rewindTo = %q; want shaD %q", rewindTo, shaD)
		}
		if len(survivors) != 0 {
			t.Errorf("survivors = %v; want []", survivors)
		}
	})

	// Reset of "c": C has no commit yet (not in stepCommits). Nothing to rewind.
	t.Run("no_commit_read_only", func(t *testing.T) {
		rewindTo, survivors := s.rewindPlan("c")
		if rewindTo != "" || len(survivors) != 0 {
			t.Errorf("rewindPlan(c) = (%q, %v); want (\"\", nil) for step with no commit", rewindTo, survivors)
		}
	})

	// Reset of "d": D is independent, closure = {D}. D's commit is index 1,
	// rewindTo = shaA, no survivors after D (B comes after D but B is not
	// relevant since the closure is only {D}).
	t.Run("independent_reset_d", func(t *testing.T) {
		rewindTo, survivors := s.rewindPlan("d")
		if rewindTo != shaA {
			t.Errorf("rewindTo = %q; want shaA %q", rewindTo, shaA)
		}
		// B is after D and not in {D}'s closure, so it is a survivor.
		if len(survivors) != 1 || survivors[0] != shaB {
			t.Errorf("survivors = %v; want [%s]", survivors, shaB)
		}
	})
}

// TestResetGuard verifies that Run.Reset is a no-op on a settled or in-flight run.
// Full integration sub-tests for this live in integration_test.go once the
// engine wires up handleReset. This placeholder file grows incrementally.

// TestResetPersistenceOff is a placeholder; see integration_test.go sub-tasks.

// note: TestResetFanOut and TestResetLinearTip live in integration_test.go
// because they need a real git repo + composeExec.

// closureOfForTest exposes closureOf for scheduler instances built outside the
// package. Not needed here (tests are in the same package) but kept for clarity.

// makeStepIDs extracts IDs from a slice of step.State for assertion helpers.
func makeStepIDs(states []*struct{ ID string }) []string {
	out := make([]string, len(states))
	for i, s := range states {
		out[i] = s.ID
	}
	return out
}

// joinIDs returns a comma-separated string of step IDs for readable error messages.
func joinIDs(ids []string) string { return strings.Join(ids, ", ") }

// stepIDsFromPath returns the step ids for every commit on path (helper for
// integration tests that need to verify run-branch contents).
func stepIDsFromPath(t *testing.T, dir string) []string {
	t.Helper()
	out, err := gitCmd(dir, "log", "--format=%(trailers:key=jig-step,valueonly)", "HEAD")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	return ids
}

// stepSHAsFromBranch returns SHA→stepID pairs for a branch (newest first).
func stepSHAsFromBranch(t *testing.T, dir, ref string) map[string]string {
	t.Helper()
	m, err := stepCommitsFromLog(dir, ref)
	if err != nil {
		t.Fatalf("stepCommitsFromLog: %v", err)
	}
	return m
}

// writeAndCommit writes content to a file in dir and commits it with the given message.
func writeAndCommit(t *testing.T, dir, filename, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("writeAndCommit: %v", err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", msg}} {
		if out, err := gitCmd(dir, args...); err != nil {
			t.Fatalf("git %v: %v — %s", args, err, strings.TrimSpace(out))
		}
	}
	sha, err := currentHEAD(dir)
	if err != nil {
		t.Fatalf("currentHEAD: %v", err)
	}
	return sha
}
