package workflow

import (
	"os"
	"path/filepath"
)

// RepoRoot walks up from startDir looking for a .git entry and returns the
// directory that contains it — the project root. Command-step scripts are
// resolved relative to this root (not relative to the workflow file or the
// execution cwd), so a workflow can reference `scripts/test.sh`,
// `examples/scripts/test.sh`, etc. from a stable, project-wide anchor.
//
// If no .git is found (a non-git checkout, or a bare fixtures dir in tests),
// RepoRoot returns "" and callers fall back to their local base directory —
// mirroring the first-class persistence-off / non-git path elsewhere in jig.
func RepoRoot(startDir string) string {
	if startDir == "" {
		return ""
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached the filesystem root without finding .git
		}
		dir = parent
	}
}

// ScriptPath resolves a command step's `script` value to the path the runner
// should read. An absolute script is returned unchanged; a relative script is
// joined onto root (the project root). When root is "" the script is returned
// as-is, to be resolved against the execution cwd (persistence-off / tests).
//
// This is the single source of truth for script resolution: both the load-time
// validator (existence check) and the runtime runner call it, so the path that
// validates is exactly the path that runs.
func ScriptPath(root, script string) string {
	if script == "" || filepath.IsAbs(script) || root == "" {
		return script
	}
	return filepath.Join(root, script)
}
