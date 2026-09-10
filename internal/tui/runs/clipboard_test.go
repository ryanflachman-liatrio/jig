package runs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/tui/monitor"
	"jig/internal/tui/shared"
)

// TestClipboardRunID verifies that y on the runs list emits a
// ClipboardRequest naming the selected run's full id.
func TestClipboardRunID(t *testing.T) {
	m := NewModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	for _, id := range []string{"20260731-100000-alpha1234", "20260731-120000-charlie7"} {
		m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
			RunID: id, Workflow: "wf", Steps: []string{"s"},
		}})
	}
	// Rows sort newest-first: cursor 0 is charlie7.
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	req, ok := cmd().(shared.ClipboardRequest)
	if !ok {
		t.Fatalf("first message = %T, want shared.ClipboardRequest", cmd())
	}
	if req.Target.Surface != shared.ClipboardSurfaceRunID {
		t.Fatalf("target surface = %q, want %q", req.Target.Surface, shared.ClipboardSurfaceRunID)
	}
	if req.Target.Label != "20260731-120000-charlie7" {
		t.Fatalf("target label = %q, want run id", req.Target.Label)
	}
	payload := req.Loader()
	if payload.Err != nil {
		t.Fatalf("loader err = %v", payload.Err)
	}
	if payload.Payload != "20260731-120000-charlie7" {
		t.Fatalf("payload = %q, want run id without newline", payload.Payload)
	}
	if strings.HasSuffix(payload.Payload, "\n") {
		t.Fatalf("payload has trailing newline: %q", payload.Payload)
	}

	// Move cursor and verify capturing the next id.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	req = cmd().(shared.ClipboardRequest)
	if req.Target.Label != "20260731-100000-alpha1234" {
		t.Fatalf("second target label = %q", req.Target.Label)
	}
}

// TestClipboardRunIDEmptyList verifies y is a no-op with no rows visible.
func TestClipboardRunIDEmptyList(t *testing.T) {
	m := NewModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd != nil {
		t.Fatalf("empty runs list produced cmd = %#v", cmd())
	}
}

// TestClipboardRunIDFilteredList verifies that filtering by workflow only
// exposes matching rows to y.
func TestClipboardRunIDFilteredList(t *testing.T) {
	m := NewModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "20260731-100000-a", Workflow: "alpha", Steps: []string{"s"},
	}})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "20260731-110000-b", Workflow: "beta", Steps: []string{"s"},
	}})
	m = m.WithWorkflowContext("beta", nil)
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("filtered y produced no command")
	}
	req := cmd().(shared.ClipboardRequest)
	if req.Target.Label != "20260731-110000-b" {
		t.Fatalf("filtered target = %q, want beta run", req.Target.Label)
	}
}
