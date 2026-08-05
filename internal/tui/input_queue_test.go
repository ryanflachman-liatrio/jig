package tui

import (
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"jig/internal/engine"
	"jig/internal/step"
)

// TestInputQueueIngest verifies that three InputRequest events for distinct steps
// produce len(inputQueue)==3 in arrival order, activeInputIdx==0, and m.focus
// equal to its pre-arrival value (no focus steal — Decision 6).
func TestInputQueueIngest(t *testing.T) {
	m := newMonitorWithSteps(t)
	preFocus := m.focus

	for _, stepID := range []string{"a", "b", "c"} {
		m, _ = m.Update(engineEventMsg{event: engine.InputRequest{
			RunID:  "run-1",
			StepID: stepID,
		}})
	}

	if got := len(m.inputQueue); got != 3 {
		t.Fatalf("expected len(inputQueue)==3, got %d", got)
	}
	// Arrival order preserved.
	for i, want := range []string{"a", "b", "c"} {
		if got := m.inputQueue[i].stepID; got != want {
			t.Fatalf("inputQueue[%d].stepID = %q, want %q", i, got, want)
		}
		if m.inputQueue[i].kind != inputKindRequest {
			t.Fatalf("inputQueue[%d].kind = %v, want inputKindRequest", i, m.inputQueue[i].kind)
		}
	}
	if m.activeInputIdx != 0 {
		t.Fatalf("activeInputIdx = %d, want 0", m.activeInputIdx)
	}
	// No focus steal on arrival.
	if m.focus != preFocus {
		t.Fatalf("focus changed from %v to %v on InputRequest arrival", preFocus, m.focus)
	}
}

// TestInputQueueMixedKinds verifies that a ReviewRequest and an AgentQuestion for
// two distinct steps coexist as two entries with the correct kinds.
func TestInputQueueMixedKinds(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve"},
	}})
	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:     "run-1",
		StepID:    "b",
		ToolUseID: "tu1",
		Questions: []engine.AgentQuestionItem{
			{Question: "Q?", Options: []engine.AgentQuestionOption{{Label: "A"}}},
		},
	}})

	if got := len(m.inputQueue); got != 2 {
		t.Fatalf("expected 2 entries, got %d", got)
	}
	if m.inputQueue[0].kind != inputKindReview {
		t.Fatalf("inputQueue[0].kind = %v, want inputKindReview", m.inputQueue[0].kind)
	}
	if m.inputQueue[0].stepID != "a" {
		t.Fatalf("inputQueue[0].stepID = %q, want \"a\"", m.inputQueue[0].stepID)
	}
	if m.inputQueue[1].kind != inputKindQuestion {
		t.Fatalf("inputQueue[1].kind = %v, want inputKindQuestion", m.inputQueue[1].kind)
	}
	if m.inputQueue[1].stepID != "b" {
		t.Fatalf("inputQueue[1].stepID = %q, want \"b\"", m.inputQueue[1].stepID)
	}
}

// TestInputQueuePruneOnStatus verifies that a StepStatus moving a queued step to
// StatusRunning removes exactly that step's entry and leaves activeInputIdx within
// [0, len(inputQueue)); pruning on an empty queue does not panic.
func TestInputQueuePruneOnStatus(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Queue entries for all three steps.
	for _, stepID := range []string{"a", "b", "c"} {
		m, _ = m.Update(engineEventMsg{event: engine.InputRequest{
			RunID:  "run-1",
			StepID: stepID,
		}})
	}
	if got := len(m.inputQueue); got != 3 {
		t.Fatalf("setup: expected 3 entries, got %d", got)
	}

	// Prune step "b" (index 1).
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
		RunID:  "run-1",
		StepID: "b",
		From:   step.StatusNeedsInput,
		To:     step.StatusRunning,
	}})

	if got := len(m.inputQueue); got != 2 {
		t.Fatalf("expected 2 entries after prune, got %d", got)
	}
	// Remaining entries are "a" and "c".
	if m.inputQueue[0].stepID != "a" {
		t.Fatalf("inputQueue[0].stepID = %q, want \"a\"", m.inputQueue[0].stepID)
	}
	if m.inputQueue[1].stepID != "c" {
		t.Fatalf("inputQueue[1].stepID = %q, want \"c\"", m.inputQueue[1].stepID)
	}
	// activeInputIdx is within range.
	if m.activeInputIdx < 0 || m.activeInputIdx >= len(m.inputQueue) {
		t.Fatalf("activeInputIdx %d out of range [0, %d)", m.activeInputIdx, len(m.inputQueue))
	}

	// Prune the remaining two.
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
		RunID: "run-1", StepID: "a", From: step.StatusNeedsInput, To: step.StatusRunning,
	}})
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
		RunID: "run-1", StepID: "c", From: step.StatusNeedsInput, To: step.StatusRunning,
	}})

	if got := len(m.inputQueue); got != 0 {
		t.Fatalf("expected empty queue after all prunes, got %d", got)
	}

	// Pruning on an already-empty queue must not panic.
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
		RunID: "run-1", StepID: "a", From: step.StatusRunning, To: step.StatusSucceeded,
	}})
	// No panic = pass.
}

// TestGateFixedHeight verifies that the Steps and Transcript panel heights
// (derived from View()) are the same before and after an InputRequest arrives.
// This proves the layout does not shift when the gate transitions from
// empty-placeholder to an active entry (Spec Unit 2, Success Metric 4).
func TestGateFixedHeight(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Capture heights with an empty queue.
	gateHBefore := lipgloss.Height(m.gateStrip())
	footerHBefore := lipgloss.Height(m.footerView())
	totalHBefore := lipgloss.Height(m.View())
	panelHBefore := totalHBefore - footerHBefore - gateHBefore

	if gateHBefore == 0 {
		t.Fatal("gate strip height is 0 — gate is not rendering when queue is empty")
	}

	// Send an InputRequest so the gate switches from placeholder to active.
	m, _ = m.Update(engineEventMsg{event: engine.InputRequest{
		RunID:  "run-1",
		StepID: "a",
	}})

	gateHAfter := lipgloss.Height(m.gateStrip())
	footerHAfter := lipgloss.Height(m.footerView())
	totalHAfter := lipgloss.Height(m.View())
	panelHAfter := totalHAfter - footerHAfter - gateHAfter

	if gateHAfter != gateHBefore {
		t.Fatalf("gate height changed on InputRequest arrival: %d → %d", gateHBefore, gateHAfter)
	}
	if panelHAfter != panelHBefore {
		t.Fatalf("panel height changed on InputRequest arrival: %d → %d", panelHBefore, panelHAfter)
	}

	// Capture empty-strip View() as artifact.
	m2 := newMonitorWithSteps(t)
	emptyView := m2.View()
	_ = os.MkdirAll("../../docs/specs/02-spec-tui-persistent-agent-input/artifacts", 0o755)
	_ = os.WriteFile(
		"../../docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit2-empty-strip.txt",
		[]byte(stripANSI(emptyView)),
		0o644,
	)
}

// stripANSI removes ANSI escape codes from s for plain-text artifact storage.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // consume 'm'
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestCycleFocusSkipsEmptyGate verifies that with an empty queue, cycling focus
// from focusTranscript wraps back to focusSteps without landing on focusGate
// (Unit 2: the empty gate is inert, not focusable via tab).
func TestCycleFocusSkipsEmptyGate(t *testing.T) {
	m := newMonitorWithSteps(t)

	if m.hasGate() {
		t.Fatal("expected empty gate at start")
	}

	// Start at focusTranscript and cycle forward (+1).
	m.focus = focusTranscript
	next := m.cycleFocus(+1)
	if next == focusGate {
		t.Fatalf("cycleFocus(+1) landed on focusGate with empty queue")
	}
	if next != focusSteps {
		t.Fatalf("cycleFocus(+1) from focusTranscript with empty queue: got %v, want focusSteps", next)
	}
}
