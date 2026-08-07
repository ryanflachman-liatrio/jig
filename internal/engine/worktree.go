package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// createWorktree creates a git worktree at wtPath on branch branchName, ensuring
// the parent directory exists first. It returns the HEAD SHA at creation time so
// callers can diff against it later.
//
// The branch name is stable across runs (jig/<workflow>/<step-id>) so downstream
// merge steps can reference it by a predictable name, and removeWorktree keeps
// the branch after the run. That makes a re-run collide with the leftover branch,
// so we use `-B` (reset-or-create) rather than `-b` (create-only): a re-run
// resets the step branch to the current HEAD — clean-slate scratch space for this
// step's work — instead of failing with "branch already exists". Any leftover
// commits on the old branch are discarded; a step's edits are meant to be merged
// within its run, not carried across runs.
func createWorktree(repoRoot, wtPath, branchName string) (baseSHA string, err error) {
	out, err := gitCmd(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w — %s", err, strings.TrimSpace(out))
	}
	baseSHA = strings.TrimSpace(out)

	if err = os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir worktree parent: %w", err)
	}

	if out, err = gitCmd(repoRoot, "worktree", "add", "-B", branchName, wtPath); err != nil {
		return "", fmt.Errorf("git worktree add: %w — %s", err, strings.TrimSpace(out))
	}
	return baseSHA, nil
}

// removeWorktree removes the git worktree at wtPath, keeping the branch so
// downstream merge steps can still reference it. The branch outlives the run by
// design; a subsequent run of the same workflow resets it via createWorktree's
// `-B` (see there).
func removeWorktree(repoRoot, wtPath string) error {
	out, err := gitCmd(repoRoot, "worktree", "remove", "--force", wtPath)
	if err != nil {
		return fmt.Errorf("git worktree remove: %w — %s", err, strings.TrimSpace(out))
	}
	return nil
}

// captureDiff captures all changes in the worktree relative to baseSHA,
// covering committed, staged, and unstaged changes so the diff is complete
// regardless of whether the agent committed its edits.
func captureDiff(wtPath, baseSHA string) string {
	var b strings.Builder

	// Committed changes pushed onto the branch since the base.
	if baseSHA != "" {
		if out, err := gitCmd(wtPath, "diff", baseSHA, "HEAD"); err == nil && len(out) > 0 {
			b.WriteString(out)
		}
	}

	// Staged changes not yet committed.
	if out, err := gitCmd(wtPath, "diff", "--cached"); err == nil && len(out) > 0 {
		b.WriteString(out)
	}

	// Unstaged changes.
	if out, err := gitCmd(wtPath, "diff"); err == nil && len(out) > 0 {
		b.WriteString(out)
	}

	return b.String()
}

// currentHEAD returns the HEAD commit SHA of the git repo or worktree at dir.
// It is the run-branch analogue of createWorktree's internal rev-parse: callers
// use it to branch a step worktree off the run branch's *current* HEAD at
// dispatch time (Task 2.x), which is what lets steps compose on each other's code.
func currentHEAD(dir string) (string, error) {
	out, err := gitCmd(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w — %s", err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// createBranchAt creates or resets branch to point at ref in the repo at
// repoRoot (`git branch -f`). Used to place the run branch at the working-branch
// HEAD; `-f` keeps a re-run idempotent against a leftover branch of the same name.
func createBranchAt(repoRoot, branch, ref string) error {
	if out, err := gitCmd(repoRoot, "branch", "-f", branch, ref); err != nil {
		return fmt.Errorf("git branch -f %s %s: %w — %s", branch, ref, err, strings.TrimSpace(out))
	}
	return nil
}

// runBranchName returns the per-run integration branch name
// jig/<workflow>/run-<runID> (Open Question 1: settled naming). Both segments
// are sanitized so arbitrary workflow names and run IDs map to legal branch names.
func runBranchName(workflow, runID string) string {
	return "jig/" + sanitizeBranchName(workflow) + "/run-" + sanitizeBranchName(runID)
}

// sanitizeBranchName replaces characters illegal in git branch names with
// hyphens so workflow and step IDs map cleanly to branch names.
func sanitizeBranchName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// gitCmd runs git with args in dir, returning combined stdout+stderr output.
func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
