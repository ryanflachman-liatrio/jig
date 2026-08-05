package tui

import (
	"testing"

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
