package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/runner"
)

// TestDiagnosticsOverlayToggle verifies the operator-facing notification
// diagnostics overlay opens on Ctrl+G, renders the injected snapshot text,
// and closes on the same chord or Esc. The overlay is root-owned so both the
// Home selector and Monitor screen surface the same view without duplicating
// state (Spec 23 FR-17 / Task 3.11).
func TestDiagnosticsOverlayToggle(t *testing.T) {
	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, t.TempDir())
	renders := 0
	renderer := DiagnosticsRendererFunc(func() string {
		renders++
		return "2026-09-11T00:00:00Z alias=ops run=r event=attention_required outcome=failed reason=timeout attempt=1"
	})
	m := New(context.Background(), mgr, WithDiagnostics(renderer)).(rootModel)
	m.width, m.height = 100, 30

	if m.showDiagnostics {
		t.Fatalf("overlay open before any key")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(rootModel)
	if !m.showDiagnostics {
		t.Fatalf("Ctrl+G did not open the diagnostics overlay")
	}

	rendered := m.View().Content
	if !strings.Contains(rendered, "Notification diagnostics") {
		t.Fatalf("overlay title missing from render: %q", rendered)
	}
	if !strings.Contains(rendered, "alias=ops") {
		t.Fatalf("renderer output missing from overlay: %q", rendered)
	}
	if renders == 0 {
		t.Fatalf("renderer was never invoked")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(rootModel)
	if m.showDiagnostics {
		t.Fatalf("Esc did not close the diagnostics overlay")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(rootModel)
	if !m.showDiagnostics {
		t.Fatalf("Ctrl+G did not re-open the overlay")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(rootModel)
	if m.showDiagnostics {
		t.Fatalf("Ctrl+G did not toggle the overlay closed")
	}
}

// TestDiagnosticsOverlayNilRenderer verifies the overlay degrades gracefully
// when notifications are not wired: it opens, shows a static message, and
// closes without asserting or crashing on the nil renderer.
func TestDiagnosticsOverlayNilRenderer(t *testing.T) {
	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, t.TempDir())
	m := New(context.Background(), mgr).(rootModel)
	m.width, m.height = 100, 30

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(rootModel)
	if !m.showDiagnostics {
		t.Fatalf("overlay did not open without a renderer")
	}
	rendered := m.View().Content
	if !strings.Contains(rendered, "not wired") {
		t.Fatalf("nil-renderer body missing from overlay: %q", rendered)
	}
}

// TestDiagnosticsOverlayEmptySnapshot verifies the overlay uses the fixed
// "no diagnostics recorded" line when the renderer returns an empty string.
func TestDiagnosticsOverlayEmptySnapshot(t *testing.T) {
	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, t.TempDir())
	m := New(context.Background(), mgr, WithDiagnostics(DiagnosticsRendererFunc(func() string { return "" }))).(rootModel)
	m.width, m.height = 100, 30

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = updated.(rootModel)
	rendered := m.View().Content
	if !strings.Contains(rendered, "No notification diagnostics") {
		t.Fatalf("empty-snapshot body missing from overlay: %q", rendered)
	}
}
