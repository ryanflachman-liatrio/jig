package shared

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// OpenLocationOwner identifies the Monitor instance and recorded selection.
// Value-only messages cannot be redirected by later navigation or page loads.
type OpenLocationOwner struct {
	Instance                       uint64
	RunID, StepID                  string
	Generation, Iteration, Attempt int
	Seq, Block                     int
	ItemKind                       int
}

// OpenLocationTarget is a literal local file path with optional positive,
// one-based coordinates. Zero means no position was supplied.
type OpenLocationTarget struct {
	Path         string
	Line, Column int
}

type OpenLocationRequest struct {
	Owner  OpenLocationOwner
	Target OpenLocationTarget
	// Unavailable is a fixed explanation from selection, never tool text.
	Unavailable string
}

type OpenLocationResult struct {
	ID     uint64
	Owner  OpenLocationOwner
	Notice string
}

// ValidateOpenLocationPath checks a resolved target without opening or creating it.
func ValidateOpenLocationPath(path string) error {
	if path == "" || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) {
		return errors.New("invalid local file path")
	}
	if !filepath.IsAbs(path) {
		return errors.New("location is not an absolute local path; originating directory unavailable")
	}
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("file unavailable; it may have moved or been deleted")
	}
	if !info.Mode().IsRegular() {
		return errors.New("target is not a regular file")
	}
	return nil
}
