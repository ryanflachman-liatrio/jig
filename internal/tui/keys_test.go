package tui

import (
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

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

func TestCompactHintKeepsBindingsAtomic(t *testing.T) {
	alpha := keybind.NewBinding(keybind.WithKeys("a"), keybind.WithHelp("a", "alpha"))
	beta := keybind.NewBinding(keybind.WithKeys("b"), keybind.WithHelp("b", "beta"))
	disabled := keybind.NewBinding(keybind.WithKeys("x"), keybind.WithHelp("x", "disabled"))
	disabled.SetEnabled(false)

	tests := []struct {
		name  string
		width int
		more  keybind.Binding
		want  string
	}{
		{name: "nothing fits", width: 5, more: shared.MoreHelpBinding(false), want: ""},
		{name: "only more", width: 6, more: shared.MoreHelpBinding(false), want: "? more"},
		{name: "one complete binding", width: 18, more: shared.MoreHelpBinding(false), want: "a alpha  •  ? more"},
		{name: "typing chord", width: 20, more: shared.MoreHelpBinding(true), want: "a alpha  •  F1 more"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shared.CompactHint(tc.width, tc.more, alpha, disabled, beta)
			if got != tc.want {
				t.Fatalf("CompactHint() = %q, want %q", got, tc.want)
			}
			if lipgloss.Width(got) > tc.width {
				t.Fatalf("hint width = %d, budget = %d", lipgloss.Width(got), tc.width)
			}
			if strings.Contains(got, "disabled") || strings.HasSuffix(got, "b bet") {
				t.Fatalf("hint contains a disabled or partial binding: %q", got)
			}
		})
	}
}

func TestGlobalHelpBindingsSwitchesTypingChord(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capturesText bool
		want         string
		notWant      string
	}{
		{name: "regular", want: "?", notWant: "F1"},
		{name: "typing", capturesText: true, want: "F1", notWant: "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hint := shared.HintString(shared.GlobalHelpBindings(tc.capturesText)...)
			if !strings.Contains(hint, tc.want+" help") || strings.Contains(hint, tc.notWant+" help") {
				t.Fatalf("global help = %q, want %q and not %q", hint, tc.want, tc.notWant)
			}
		})
	}
}
