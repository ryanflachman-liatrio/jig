package scaffold

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateName normalizes a scaffold name while keeping it an identifier, not
// a path supplied by the caller.
func ValidateName(name string) (string, error) {
	name = strings.ToLower(name)
	if name == "" {
		return "", fmt.Errorf("name must not be empty")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("name %q must not be a dot segment", name)
	}
	if strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("name %q must be a single path segment", name)
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return "", fmt.Errorf("name %q may contain only lowercase letters, digits, dots, underscores, and hyphens", name)
	}
	return name, nil
}

// DefaultName derives the normalized workflow name from the target directory.
func DefaultName(dir string) string {
	return strings.ToLower(filepath.Base(filepath.Clean(dir)))
}
