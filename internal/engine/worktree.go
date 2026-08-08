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
func createWorktree(repoRoot, wtPath, branchName string) (baseSHA string, err error) {
	return createWorktreeAt(repoRoot, wtPath, branchName, "HEAD")
}

// createWorktreeAt is createWorktree with an explicit base ref: the new branch
// is created (reset) at ref rather than the repo's HEAD. Step worktrees pass the
// run branch as ref so they branch off the run branch's *current* HEAD at
// dispatch time (spec 06) — which is what lets a downstream step see the code an
// upstream step already integrated. The returned baseSHA is ref resolved to a
// sha, used later to diff the step's changes.
func createWorktreeAt(repoRoot, wtPath, branchName, ref string) (baseSHA string, err error) {
	out, err := gitCmd(repoRoot, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("rev-parse %s: %w — %s", ref, err, strings.TrimSpace(out))
	}
	baseSHA = strings.TrimSpace(out)

	if err = os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir worktree parent: %w", err)
	}

	out, err = gitCmd(repoRoot, "worktree", "add", "-B", branchName, wtPath, ref)
	if err != nil {
		// The branch may be checked out in a stale worktree left by a crashed run.
		// Remove it and retry once; also prune phantom entries (directory gone but
		// git metadata still present).
		if stale := parseStaleWorktreePath(out); stale != "" {
			_ = removeWorktree(repoRoot, stale)
			_, _ = gitCmd(repoRoot, "worktree", "prune")
			out, err = gitCmd(repoRoot, "worktree", "add", "-B", branchName, wtPath, ref)
		}
		if err != nil {
			return "", fmt.Errorf("git worktree add: %w — %s", err, strings.TrimSpace(out))
		}
	}
	return baseSHA, nil
}

// parseStaleWorktreePath extracts the worktree path from a git "is already used
// by worktree at '<path>'" error message, returning "" when the pattern is absent.
func parseStaleWorktreePath(gitOutput string) string {
	const marker = "is already used by worktree at '"
	i := strings.Index(gitOutput, marker)
	if i < 0 {
		return ""
	}
	rest := gitOutput[i+len(marker):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// removeWorktree removes the git worktree at wtPath. It does not delete the
// branch — callers decide whether to keep it (run branch, kept as integration
// history) or delete it (step branches, ephemeral per-run).
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

// squashMergeStep integrates a completed step's worktree into the run branch as
// exactly one commit tagged with the `jig-step: <stepID>` trailer, advancing the
// run branch HEAD by one. It runs on the scheduler goroutine (serialized), which
// is what keeps the run branch a single linear history.
//
// Agents leave their edits *uncommitted* in the step worktree, so this first
// commits them onto the step branch, then squash-merges that branch into the run
// branch (checked out in runWorktree) and commits the squash with the trailer.
//
// Returns:
//   - sha != "" on a successful integration (the new run-branch commit),
//   - sha == "", conflict == false when the step changed nothing (no commit),
//   - conflict == true when the squash-merge hit a conflict — the conflicted
//     state is LEFT in runWorktree for the operator-resolution flow (spec 06 A2);
//     the caller decides whether to park (gate) or clean up.
func squashMergeStep(repoRoot, runWorktree, stepWorktree, stepBranch, stepID string) (sha string, conflict bool, err error) {
	// 1. Commit the step's edits onto its branch. Nothing to commit ⇒ the step
	//    changed no files ⇒ no integration (treated like a read-only step).
	if out, cerr := gitCmd(stepWorktree, "add", "-A"); cerr != nil {
		return "", false, fmt.Errorf("git add in step worktree: %w — %s", cerr, strings.TrimSpace(out))
	}
	if out, cerr := gitCmd(stepWorktree, "commit", "-m", "jig step "+stepID); cerr != nil {
		if strings.Contains(out, "nothing to commit") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("git commit in step worktree: %w — %s", cerr, strings.TrimSpace(out))
	}

	// 2. Squash-merge the step branch into the run branch (stages the changes).
	//    A conflict leaves unmerged paths in runWorktree; surface it, don't error.
	if out, cerr := gitCmd(runWorktree, "merge", "--squash", stepBranch); cerr != nil {
		if len(mergeConflictPaths(runWorktree)) > 0 {
			return "", true, nil
		}
		return "", false, fmt.Errorf("git merge --squash %s: %w — %s", stepBranch, cerr, strings.TrimSpace(out))
	}

	// 3. Commit the squashed changes with the jig-step trailer. "nothing to
	//    commit" means the step's changes were already on the run branch (e.g. an
	//    idempotent loop iteration) — not an error, just no new commit.
	msg := stepID + "\n\njig-step: " + stepID
	if out, cerr := gitCmd(runWorktree, "commit", "-m", msg); cerr != nil {
		if strings.Contains(out, "nothing to commit") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("git commit squash for %q: %w — %s", stepID, cerr, strings.TrimSpace(out))
	}
	sha, err = currentHEAD(runWorktree)
	if err != nil {
		return "", false, err
	}
	return sha, false, nil
}

// finalMerge lands the run by merging runBranch into the user's working branch
// (spec 06 A3). repoRoot is the primary worktree, still checked out on base, so
// the merge advances base's HEAD to include the run's squash commits (a
// fast-forward when base hasn't moved). base is informational — the merge runs
// against repoRoot's current HEAD, which is base.
//
// On a merge conflict the half-applied merge is aborted so repoRoot's working
// tree is left clean (conflict == true, err == nil); the caller decides how to
// surface it. Discard is simply not calling this — the run branch is left in
// place and base is untouched.
func finalMerge(repoRoot, base, runBranch string) (conflict bool, err error) {
	if out, cerr := gitCmd(repoRoot, "merge", "--no-edit", runBranch); cerr != nil {
		if len(mergeConflictPaths(repoRoot)) > 0 {
			_, _ = gitCmd(repoRoot, "merge", "--abort")
			return true, nil
		}
		return false, fmt.Errorf("git merge %s onto %s: %w — %s", runBranch, base, cerr, strings.TrimSpace(out))
	}
	return false, nil
}

// mergeConflictPaths returns the unmerged (conflicted) paths in a worktree after
// a failed merge/squash, so the integration-conflict gate (spec 06 A2) can name
// exactly which files the operator must resolve.
func mergeConflictPaths(worktree string) []string {
	out, err := gitCmd(worktree, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

// stepCommitsFromLog rebuilds the stepID → sha map from the run branch history by
// reading the `jig-step:` trailer off each commit reachable from ref. It makes
// the in-memory stepCommits map reconstructable from git alone — the property the
// later reset foundation relies on. Newest commit wins if a step id ever repeats.
func stepCommitsFromLog(dir, ref string) (map[string]string, error) {
	out, err := gitCmd(dir, "log", "--format=%H%x09%(trailers:key=jig-step,valueonly)", ref)
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w — %s", ref, err, strings.TrimSpace(out))
	}
	m := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		sha, stepID := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if sha == "" || stepID == "" {
			continue
		}
		if _, ok := m[stepID]; !ok {
			m[stepID] = sha
		}
	}
	return m, nil
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
