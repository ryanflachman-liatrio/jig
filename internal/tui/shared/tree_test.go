package shared

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTreePrefixWidths(t *testing.T) {
	for name, prefix := range map[string]string{
		"branch":             TreePrefix(false),
		"last":               TreePrefix(true),
		"continuation":       TreeContinuationPrefix(false),
		"final continuation": TreeContinuationPrefix(true),
	} {
		t.Run(name, func(t *testing.T) {
			if got := lipgloss.Width(prefix); got != 3 {
				t.Fatalf("width(%q) = %d, want 3", prefix, got)
			}
		})
	}
}
