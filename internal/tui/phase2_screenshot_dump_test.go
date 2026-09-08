package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	domainreview "jig/internal/review"
	"jig/internal/step"
	"jig/internal/tui/monitor"
)

// TestPhase2ScreenshotDump writes ANSI View() frames for terminal screenshots.
func TestPhase2ScreenshotDump(t *testing.T) {
	dir := "/tmp/phase2-ui-frames"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, m monitor.Model) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(m.View()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(m.View()))
	}
	strip := func(s string) string {
		var b strings.Builder
		inEsc := false
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c == 0x1b {
				inEsc = true
				continue
			}
			if inEsc {
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					inEsc = false
				}
				continue
			}
			b.WriteByte(c)
		}
		return b.String()
	}

	base := func() monitor.Model {
		m := monitor.New("abcd1234ef")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
		m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
			RunID: "abcd1234ef", Workflow: "feature",
			Steps: []string{"plan", "implement", "review"},
		}})
		m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
			RunID: "abcd1234ef", StepID: "plan", To: step.StatusSucceeded,
		}})
		m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
			RunID: "abcd1234ef", StepID: "implement", To: step.StatusRunning,
		}})
		spent := 0.08
		m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
			RunID: "abcd1234ef", StepID: "plan", To: step.StatusSucceeded,
			Cost: &spent, Tokens: 12400,
		}})
		return m
	}

	// 2.2 slim titles
	m := base()
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	write("2.2-slim-titles.ansi", m)

	// 2.3 simple (default)
	write("2.3-footer-simple.ansi", m)

	// 2.3 advanced
	m = m.ToggleSimpleMode()
	write("2.3-footer-advanced.ansi", m)

	content := "Design notes for the golden path review workspace."
	docs := []domainreview.Document{{
		ID: "design", Label: "design.md", Source: "design.md", Format: "markdown",
		Content: content, SHA256: domainreview.Digest(content), LineCount: 1,
	}}

	// 2.1 wide: open a focused review workspace.
	m = base()
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "abcd1234ef", StepID: "review", Choices: []string{"accept", "revise"},
		Documents: docs,
	}})
	// First-wait auto-focuses Gate — Enter opens the full-width workspace.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 180, Height: 40})
	// Ensure gate focus
	for i := 0; i < 4; i++ {
		if strings.Contains(strip(m.View()), "[GATE]") {
			break
		}
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	wide := strip(m.View())
	if !strings.Contains(wide, "[REVIEW]") {
		t.Fatalf("wide dump missing [REVIEW]:\n%s", wide)
	}
	if strings.Contains(wide, "› Steps") || strings.Contains(wide, "[STEPS]") {
		t.Fatalf("wide dump retained Steps chrome:\n%s", wide)
	}
	write("2.1-review-wide.ansi", m)

	// 2.1 narrow
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	narrow := strip(m.View())
	if !strings.Contains(narrow, "[REVIEW]") {
		t.Fatalf("narrow dump missing [REVIEW]:\n%s", narrow)
	}
	write("2.1-review-narrow.ansi", m)
}
