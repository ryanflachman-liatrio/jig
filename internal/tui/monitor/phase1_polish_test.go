package monitor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/tui/shared"
)

func TestStatusLineIdentityAndCost(t *testing.T) {
	m := newMonitorWithSteps(t)
	cost := 0.08
	m.totalTokens = 12400
	m.totalCost = cost
	m.workflow = "feature"
	m.RunID = "abcd1234ef"
	m.chatAutoScroll = true
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	status := ansiStrip(m.statusLineView())
	for _, want := range []string{"1234ef", "feature", "tok", "$0.08", "LIVE"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q:\n%s", want, status)
		}
	}
	// State words belong in the footer, not the status line (C1).
	for _, bad := range []string{"running", "awaiting", "failed", "done"} {
		if strings.Contains(status, bad) {
			t.Fatalf("status duplicated state word %q:\n%s", bad, status)
		}
	}
	footer := ansiStrip(m.footerView())
	if !strings.Contains(footer, "running") {
		t.Fatalf("footer missing state word:\n%s", footer)
	}
}

func TestStatusLinePendingGateStepWins(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "run-1", StepID: "b", Choices: []string{"approve"},
	}})
	status := ansiStrip(m.statusLineView())
	if !strings.Contains(status, "b") {
		t.Fatalf("pending gate step should win status step field:\n%s", status)
	}
}

func TestStatusLineHiddenWhenShort(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 11})
	layout := m.verticalLayout()
	if layout.statusH != 0 {
		t.Fatalf("statusH = %d, want 0 below height 12", layout.statusH)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	layout = m.verticalLayout()
	if layout.statusH < 1 {
		t.Fatalf("statusH = %d, want >= 1 at height 20", layout.statusH)
	}
}

func TestFocusBadgesExactlyOne(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	top := ansiStrip(firstRow(m.View()))
	if !strings.Contains(top, "[STEPS]") || strings.Contains(top, "[TRANSCRIPT]") {
		t.Fatalf("steps focus badges wrong:\n%s", top)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	top = ansiStrip(firstRow(m.View()))
	if !strings.Contains(top, "[TRANSCRIPT]") || strings.Contains(top, "[STEPS]") {
		t.Fatalf("transcript focus badges wrong:\n%s", top)
	}

	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "run-1", StepID: "a", Choices: []string{"approve"},
	}})
	m.focus = focusGate
	view := ansiStrip(m.View())
	if !strings.Contains(view, "[GATE]") {
		t.Fatalf("gate focus missing [GATE]:\n%s", view)
	}
	if strings.Contains(view, "[STEPS]") || strings.Contains(view, "[TRANSCRIPT]") {
		t.Fatalf("gate focus should clear panel badges:\n%s", view)
	}

	// Blurred pending gate uses unbracketed GATE · needs input.
	m.focus = focusSteps
	bar := ansiStrip(m.inputBarView())
	if !strings.Contains(bar, "GATE · needs input") || strings.Contains(bar, "[GATE]") {
		t.Fatalf("blurred pending gate chrome wrong:\n%s", bar)
	}
}

func TestFocusBadgeClearedWhenHelpOpen(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.helpOpen = true
	parts := m.stepsPanelTitleParts()
	if shared.IsFocusBadge(parts[len(parts)-1]) {
		t.Fatalf("help-open should clear badge: %v", parts)
	}
}

func TestTranscriptEmptyStateNoFakeCTA(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunDir = t.TempDir()
	m = enterChatStep(t, m, "a")
	// pending step with empty transcript
	if i, ok := m.index["a"]; ok {
		m.steps[i].status = step.StatusPending
	}
	body := ansiStrip(m.chatBody())
	if !strings.Contains(body, "Waiting for step to start") {
		t.Fatalf("pending empty transcript wrong:\n%s", body)
	}
	if strings.Contains(body, "search") || strings.Contains(body, "/") {
		t.Fatalf("empty pending transcript advertised search:\n%s", body)
	}
}
