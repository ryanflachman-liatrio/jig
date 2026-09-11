package monitor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
)

type fakeDiagRenderer struct {
	calls int
	text  string
}

func (f *fakeDiagRenderer) RenderDiagnostics() string {
	f.calls++
	return f.text
}

func readyDiagModel() Model {
	m := New("run1")
	m.ready = true
	m.width, m.height = 100, 30
	return m
}

// TestDiagnosticsOverlayToggle proves the Monitor's own "Notification
// diagnostics" command (spec 23 FR-17 / task 3.11) opens on ctrl+g, renders
// the injected snapshot text, and closes on the same chord or Esc — a
// command discoverable through the Monitor's palette catalog
// (PaletteSections), not a global root-owned overlay.
func TestDiagnosticsOverlayToggle(t *testing.T) {
	renderer := &fakeDiagRenderer{text: "2026-09-11T00:00:00Z alias=ops run=r event=attention_required outcome=failed reason=timeout attempt=1"}
	m := readyDiagModel().WithDiagnostics(renderer)

	if m.showDiagnostics {
		t.Fatalf("overlay open before any key")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated
	if !m.showDiagnostics {
		t.Fatalf("ctrl+g did not open the diagnostics overlay")
	}

	rendered := m.View()
	if !strings.Contains(rendered, "Notification diagnostics") {
		t.Fatalf("overlay title missing from render: %q", rendered)
	}
	if !strings.Contains(rendered, "alias=ops") {
		t.Fatalf("renderer output missing from overlay: %q", rendered)
	}
	if renderer.calls == 0 {
		t.Fatalf("renderer was never invoked")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated
	if m.showDiagnostics {
		t.Fatalf("esc did not close the diagnostics overlay")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated
	if !m.showDiagnostics {
		t.Fatalf("ctrl+g did not re-open the overlay")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated
	if m.showDiagnostics {
		t.Fatalf("ctrl+g did not toggle the overlay closed")
	}
}

// TestDiagnosticsOverlayNilRenderer verifies the overlay degrades gracefully
// when notifications are not wired: it opens, shows a static message, and
// closes without asserting or crashing on the nil renderer.
func TestDiagnosticsOverlayNilRenderer(t *testing.T) {
	m := readyDiagModel()

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated
	if !m.showDiagnostics {
		t.Fatalf("overlay did not open without a renderer")
	}
	rendered := m.View()
	if !strings.Contains(rendered, "not wired") {
		t.Fatalf("nil-renderer body missing from overlay: %q", rendered)
	}
}

// TestDiagnosticsOverlayEmptySnapshot verifies the overlay uses the fixed
// "no diagnostics recorded" line when the renderer returns an empty string.
func TestDiagnosticsOverlayEmptySnapshot(t *testing.T) {
	m := readyDiagModel().WithDiagnostics(&fakeDiagRenderer{text: ""})

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated
	rendered := m.View()
	if !strings.Contains(rendered, "No notification diagnostics") {
		t.Fatalf("empty-snapshot body missing from overlay: %q", rendered)
	}
}

// TestDiagnosticsOverlayDiscoverableInPalette proves the command is exposed
// through the Monitor's own action/command surface (PaletteSections) rather
// than only a raw key binding, satisfying FR-17's "named action" requirement.
func TestDiagnosticsOverlayDiscoverableInPalette(t *testing.T) {
	m := readyDiagModel()
	var found bool
	for _, sec := range m.PaletteSections() {
		for _, b := range sec.Bindings {
			if b.Help().Desc == "notification diagnostics" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("Notification diagnostics command missing from PaletteSections")
	}
}

// TestDiagnosticsOverlayPreservesGateFocus proves opening/closing the
// overlay never steals Gate focus or disturbs the pending input queue — an
// arriving diagnostic must not interrupt an operator mid-review.
func TestDiagnosticsOverlayPreservesGateFocus(t *testing.T) {
	m := readyDiagModel().WithDiagnostics(&fakeDiagRenderer{text: "alias=ops outcome=failed"})
	m.focus = focusGate
	m.inputQueue = []pendingInputEntry{{
		kind:   inputKindReview,
		stepID: "step-1",
		review: &engine.ReviewRequest{RunID: "run1", StepID: "step-1"},
	}}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated
	if !m.showDiagnostics {
		t.Fatalf("overlay did not open")
	}
	if m.focus != focusGate {
		t.Fatalf("opening the overlay changed focus: %v", m.focus)
	}
	if !m.hasGate() || len(m.inputQueue) != 1 {
		t.Fatalf("opening the overlay disturbed the pending input queue: %+v", m.inputQueue)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated
	if m.showDiagnostics {
		t.Fatalf("esc did not close the overlay")
	}
	if m.focus != focusGate {
		t.Fatalf("closing the overlay changed focus: %v", m.focus)
	}
}

// TestDiagnosticsOverlayNeverAutoOpens proves an ordinary engine event never
// opens the overlay by itself — only a deliberate ctrl+g/palette command
// does (FR-17: "never auto-opened by an arriving diagnostic").
func TestDiagnosticsOverlayNeverAutoOpens(t *testing.T) {
	m := readyDiagModel().WithDiagnostics(&fakeDiagRenderer{text: "alias=ops outcome=failed"})
	m, _ = m.handleEngineEvent(engine.StepStatus{RunID: "run1", StepID: "step-1", To: "running"})
	if m.showDiagnostics {
		t.Fatalf("an ordinary engine event auto-opened the diagnostics overlay")
	}
}
