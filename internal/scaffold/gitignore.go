package scaffold

import (
	"bytes"
	"strings"
)

// NeedsJigIgnore reports whether the root .gitignore needs an entry for jig's
// runtime state directory.
func NeedsJigIgnore(existing []byte) bool {
	for _, line := range strings.Split(string(existing), "\n") {
		switch strings.TrimSpace(line) {
		case ".jig", ".jig/":
			return false
		}
	}
	return true
}

// AppendJigIgnore returns .gitignore contents with one trailing .jig/ entry.
func AppendJigIgnore(existing []byte) []byte {
	result := bytes.Clone(existing)
	if !NeedsJigIgnore(result) {
		return result
	}
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	return append(result, ".jig/\n"...)
}
