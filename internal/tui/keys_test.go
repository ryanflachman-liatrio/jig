package tui

import (
	"errors"
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/workflow"
)

// TestHintStringSkipsDisabled locks in the property that makes footers unable to
// lie: a disabled binding never renders, so a hint cannot outlive the key it
// describes.
func TestHintStringSkipsDisabled(t *testing.T) {
	on := keybind.NewBinding(keybind.WithKeys("a"), keybind.WithHelp("a", "alpha"))
	off := keybind.NewBinding(keybind.WithKeys("b"), keybind.WithHelp("b", "beta"))
	off.SetEnabled(false)

	got := hintString(on, off, keyQuit)
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

// TestDetailFooterTracksRunAvailability verifies the footer is driven by the
// KeyMap: the "r run" hint is absent until a valid workflow loads (Run disabled)
// and present afterwards — the guard and the advertised help move as one unit.
func TestDetailFooterTracksRunAvailability(t *testing.T) {
	// The run hint renders as "r run"; match it with its trailing separator so the
	// assertion doesn't trip on the substring inside "enter runs".
	hasRun := func(s string) bool { return strings.Contains(s, "r run  •") }

	m := newDetailModel("path/to/wf.toml")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})

	if got := m.footerView(); hasRun(got) {
		t.Errorf("footer advertised run before a workflow loaded: %q", got)
	}

	// A load failure leaves wf nil, so run stays unavailable.
	m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{Name: "Flow"}, err: errors.New("boom")})
	if got := m.footerView(); hasRun(got) {
		t.Errorf("footer advertised run for an invalid workflow: %q", got)
	}

	// A successful load makes run available; the hint appears without any change
	// to the footer code.
	m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{Name: "Flow"}, wf: &workflow.Workflow{}})
	if got := m.footerView(); !hasRun(got) {
		t.Errorf("footer omitted run for a valid workflow: %q", got)
	}
}
