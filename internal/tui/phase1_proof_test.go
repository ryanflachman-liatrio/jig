package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/tui/monitor"
	"jig/internal/tui/palette"
	"jig/internal/tui/runs"
	"jig/internal/tui/shared"
)

func TestPhase1ProofFrames(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "plans", "tui-lazygit-polish", "phase-1", "proofs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(stripANSI(content)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

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
		RunID: "abcd1234ef", StepID: "plan", From: step.StatusSucceeded, To: step.StatusSucceeded,
		Cost: &spent, Tokens: 12400,
	}})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	if !strings.Contains(stripANSI(m.View()), "implement") {
		t.Fatalf("steps not populated:\n%s", stripANSI(m.View()))
	}
	write("1.1-status-line-steps-focus.txt", m.View())

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	write("1.2-focus-badge-transcript.txt", m.View())

	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "abcd1234ef", StepID: "review", Choices: []string{"approve", "revise", "abort"},
	}})
	// Tab until gate focused (Steps → Transcript → Gate).
	for i := 0; i < 3; i++ {
		view := stripANSI(m.View())
		if strings.Contains(view, "[GATE]") {
			break
		}
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	write("1.2-focus-badge-gate.txt", m.View())

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // leave gate
	write("1.2-blurred-gate-needs-input.txt", m.View())

	pal := palette.New().Show([]palette.Command{
		{ID: "1", Title: "reset step", Binding: "r", Key: "r", Enabled: true},
		{ID: "2", Title: "resume step", Binding: "ctrl+r", Key: "ctrl+r", Enabled: true},
		{ID: "3", Title: "leave monitor", Binding: "esc", Key: "esc", Enabled: true},
		{ID: "4", Title: "open transcript", Binding: "enter", Key: "enter", Enabled: true},
	})
	write("1.3-command-palette.txt", pal.View(m.View(), 100, 28))

	rm := runs.NewModel().WithWorkflowContext("hotfix", nil)
	rm, _ = rm.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	write("1.4-empty-runs-cta.txt", rm.View())

	write("1.4-empty-workflows.txt", shared.RenderEmptyState(shared.EmptyState{
		Title: "No workflows found",
		Body:  "Add a <name>.toml with a [workflow] table under .agents/jig.",
		CTA:   "?  open help",
	}))
}

func stripANSI(s string) string {
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
