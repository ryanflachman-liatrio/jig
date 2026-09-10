package scaffold

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrInvalidName identifies name errors that a CLI should report as usage.
var ErrInvalidName = errors.New("invalid scaffold name")

// ValidateName normalizes a scaffold name while keeping it an identifier, not
// a path supplied by the caller.
func ValidateName(name string) (string, error) {
	name = strings.ToLower(name)
	if name == "" {
		return "", fmt.Errorf("%w: name must not be empty", ErrInvalidName)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("%w: name %q must not be a dot segment", ErrInvalidName, name)
	}
	if strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("%w: name %q must be a single path segment", ErrInvalidName, name)
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return "", fmt.Errorf("%w: name %q may contain only lowercase letters, digits, dots, underscores, and hyphens", ErrInvalidName, name)
	}
	return name, nil
}

// DefaultName derives the normalized workflow name from the target directory.
func DefaultName(dir string) string {
	return strings.ToLower(filepath.Base(filepath.Clean(dir)))
}
