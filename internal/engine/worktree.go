package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// createWorktree creates a git worktree at wtPath on a new branch branchName,
// ensuring the parent directory exists first. It returns the HEAD SHA at
// creation time so callers can diff against it later.
func createWorktree(repoRoot, wtPath, branchName string) (baseSHA string, err error) {
	out, err := gitCmd(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w — %s", err, strings.TrimSpace(out))
	}
	baseSHA = strings.TrimSpace(out)

	if err = os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir worktree parent: %w", err)
	}

	if out, err = gitCmd(repoRoot, "worktree", "add", wtPath, "-b", branchName); err != nil {
		return "", fmt.Errorf("git worktree add: %w — %s", err, strings.TrimSpace(out))
	}
	return baseSHA, nil
}

// removeWorktree removes the git worktree at wtPath, keeping the branch so
// downstream merge steps can still reference it.
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
