package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExecutionPath resolves an authored runtime repository path beneath root.
// An empty root is the persistence-off path, where the process CWD remains the
// execution anchor. A non-empty root is a capability boundary: paths may not
// be absolute, traverse parents, or reach outside it through a symlink.
func ExecutionPath(root, authored string) (string, error) {
	if root == "" || authored == "" {
		return authored, nil
	}
	if filepath.IsAbs(authored) {
		return "", fmt.Errorf("runtime path %q must be relative to the execution directory", authored)
	}
	for _, component := range strings.FieldsFunc(authored, func(r rune) bool {
		return r == '/' || r == filepath.Separator
	}) {
		if component == ".." {
			return "", fmt.Errorf("runtime path %q must not traverse parent directories", authored)
		}
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve execution directory: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve execution directory %q: %w", root, err)
	}
	candidate := filepath.Join(rootAbs, filepath.Clean(authored))

	// The final path can be new (for an agent output), so validate the nearest
	// existing ancestor. If the final path already exists, this also prevents a
	// write through an escaping symlink.
	for existing := candidate; ; existing = filepath.Dir(existing) {
		if _, err := os.Lstat(existing); err != nil {
			if !os.IsNotExist(err) {
				return "", fmt.Errorf("inspect runtime path %q: %w", authored, err)
			}
			parent := filepath.Dir(existing)
			if parent == existing {
				return "", fmt.Errorf("resolve runtime path %q: no existing ancestor", authored)
			}
			continue
		}
		real, err := filepath.EvalSymlinks(existing)
		if err == nil {
			if !pathWithin(rootReal, real) {
				return "", fmt.Errorf("runtime path %q escapes execution directory", authored)
			}
			return candidate, nil
		}
		return "", fmt.Errorf("resolve runtime path %q: %w", authored, err)
	}
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
