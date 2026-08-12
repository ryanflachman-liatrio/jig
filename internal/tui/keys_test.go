package tui

import (
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"

	"jig/internal/tui/shared"
)

// TestHintStringSkipsDisabled locks in the property that makes footers unable to
// lie: a disabled binding never renders, so a hint cannot outlive the key it
// describes.
func TestHintStringSkipsDisabled(t *testing.T) {
	on := keybind.NewBinding(keybind.WithKeys("a"), keybind.WithHelp("a", "alpha"))
	off := keybind.NewBinding(keybind.WithKeys("b"), keybind.WithHelp("b", "beta"))
	off.SetEnabled(false)

	got := shared.HintString(on, off, shared.KeyQuit)
	if strings.Contains(got, "beta") {
		t.Errorf("disabled binding leaked into hint: %q", got)
	}
	if !strings.Contains(got, "a alpha") {
		t.Errorf("enabled binding missing from hint: %q", got)
	}
	if !strings.Contains(got, "ctrl+c quit") {
		t.Errorf("global quit missing from hint: %q", got)
	}
}
