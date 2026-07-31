package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"jig/internal/engine"
)

// newMonitorWithSteps returns a sized monitor primed with a three-step run so
// tests can exercise list navigation without the engine.
func newMonitorWithSteps(t *testing.T) monitorModel {
	t.Helper()
	m := newMonitorModel("run-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(engineEventMsg{event: engine.RunStarted{
		RunID:    "run-1",
		Workflow: "demo",
		Steps:    []string{"a", "b", "c"},
	}})
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// TestMonitorListNavigation checks that j/k move the selection cursor in
// modeList (without scrolling) and clamp at the list bounds.
func TestMonitorListNavigation(t *testing.T) {
	m := newMonitorWithSteps(t)

	if m.mode != modeList {
		t.Fatalf("expected modeList by default, got %v", m.mode)
	}
	if m.cursor != 0 {
		t.Fatalf("expected cursor 0, got %d", m.cursor)
	}

	m, _ = m.Update(key("k")) // already at top, stays
	if m.cursor != 0 {
		t.Fatalf("k at top moved cursor to %d", m.cursor)
	}

	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("j"))
	if m.cursor != 2 {
		t.Fatalf("expected cursor 2 after two j, got %d", m.cursor)
	}

	m, _ = m.Update(key("j")) // at bottom, clamps
	if m.cursor != 2 {
		t.Fatalf("j at bottom moved cursor to %d", m.cursor)
	}

	m, _ = m.Update(key("k"))
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1 after k, got %d", m.cursor)
	}

	// Selected row is marked in the list body.
	if !strings.Contains(m.body(), "> ") {
		t.Fatalf("list body missing cursor marker:\n%s", m.body())
	}
}

// TestMonitorEnterAndBack checks enter drills into a step's chat, esc returns
// to the list, and a second esc emits showRunsMsg.
func TestMonitorEnterAndBack(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("j")) // cursor → step "b"

	m, _ = m.Update(key("enter"))
	if m.mode != modeChat {
		t.Fatalf("enter did not enter modeChat, got %v", m.mode)
	}
	if m.chatStep != "b" {
		t.Fatalf("expected chatStep b, got %q", m.chatStep)
	}
	if !strings.Contains(m.body(), "chat") {
		t.Fatalf("chat body missing header:\n%s", m.body())
	}

	m, _ = m.Update(key("esc"))
	if m.mode != modeList {
		t.Fatalf("esc did not return to modeList, got %v", m.mode)
	}
	if m.chatStep != "" {
		t.Fatalf("expected chatStep cleared, got %q", m.chatStep)
	}

	_, cmd := m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("esc in modeList produced no command")
	}
	if _, ok := cmd().(showRunsMsg); !ok {
		t.Fatalf("esc in modeList did not emit showRunsMsg, got %T", cmd())
	}
}

// TestMonitorChatScrolls confirms j/k in modeChat are not intercepted as
// cursor moves (they fall through to the viewport).
func TestMonitorChatScrolls(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("j")) // cursor → 1
	m, _ = m.Update(key("enter"))
	if m.mode != modeChat {
		t.Fatalf("expected modeChat, got %v", m.mode)
	}
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j in modeChat moved the list cursor from %d to %d", before, m.cursor)
	}
}

// TestMonitorReviewForcesList verifies a review gate arriving while in modeChat
// pops back to the list (where the overlay renders) and that digit keys select
// a verdict.
func TestMonitorReviewForcesList(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("enter")) // into chat for step "a"
	if m.mode != modeChat {
		t.Fatalf("expected modeChat, got %v", m.mode)
	}

	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
	if m.mode != modeList {
		t.Fatalf("review gate did not force modeList, got %v", m.mode)
	}
	if !strings.Contains(m.body(), "Review required") {
		t.Fatalf("review overlay not shown:\n%s", m.body())
	}

	_, cmd := m.Update(key("1"))
	if cmd == nil {
		t.Fatal("digit key produced no verdict command")
	}
	vm, ok := cmd().(reviewVerdictMsg)
	if !ok {
		t.Fatalf("expected reviewVerdictMsg, got %T", cmd())
	}
	if vm.verdict != "approve" {
		t.Fatalf("expected verdict approve, got %q", vm.verdict)
	}
}

// TestMonitorReviewFreezesNavigation confirms j/k are swallowed (do not move
// the cursor) while a review overlay is up.
func TestMonitorReviewFreezesNavigation(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j moved cursor during review: %d → %d", before, m.cursor)
	}
}

// TestMonitorMessageCount shows StepMessage liveness events surface as a
// per-step message count in the list.
func TestMonitorMessageCount(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: 3,
	}})
	if got := m.msgCount["a"]; got != 3 {
		t.Fatalf("expected msgCount 3, got %d", got)
	}
	if !strings.Contains(m.body(), "3 msg") {
		t.Fatalf("list body missing message count:\n%s", m.body())
	}
	// Stale (lower) seq must not lower the count.
	m, _ = m.Update(engineEventMsg{event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: 2,
	}})
	if got := m.msgCount["a"]; got != 3 {
		t.Fatalf("stale seq lowered count to %d", got)
	}
}
