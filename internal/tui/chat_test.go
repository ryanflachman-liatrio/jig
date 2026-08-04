package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newReadyChat builds a chatModel, sizes it (making it ready), and returns the
// concrete type so tests can inspect/set unexported streaming state directly.
// The Claude client is never connected — the paneled layout renders from the
// model's presentation state alone, which is all these tests exercise.
func newReadyChat(t *testing.T) chatModel {
	t.Helper()
	m := newChatModel(context.Background(), true).(chatModel)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	cm, ok := updated.(chatModel)
	if !ok {
		t.Fatalf("Update returned %T, want chatModel", updated)
	}
	if !cm.ready {
		t.Fatalf("chat not ready after WindowSizeMsg")
	}
	return cm
}

// TestChatPanels asserts both panel titles render and that toggling focus moves
// which panel's border uses the primary (Charple) style.
func TestChatPanels(t *testing.T) {
	m := newReadyChat(t)
	// Mark connected so the Conversation title drops "connecting…" and reads a
	// plain "Conversation".
	m.connected = true

	view := m.View().Content
	if !strings.Contains(view, "Conversation") {
		t.Fatalf("view missing Conversation panel title:\n%s", view)
	}
	if !strings.Contains(view, "Message") {
		t.Fatalf("view missing Message panel title:\n%s", view)
	}

	// Default focus is the input (Message) panel: the "Message" title line carries
	// the primary border color, the "Conversation" title line does not.
	if m.focus != focusInput {
		t.Fatalf("expected default focusInput, got %v", m.focus)
	}
	messageTop := titleRow(view, "Message")
	convTop := titleRow(view, "Conversation")
	if !strings.Contains(messageTop, primaryBorderSeq) {
		t.Fatalf("focused Message panel border should be primary:\n%q", messageTop)
	}
	if strings.Contains(convTop, primaryBorderSeq) {
		t.Fatalf("blurred Conversation panel border should NOT be primary:\n%q", convTop)
	}

	// esc moves focus from the input to the output (Conversation) panel; the
	// primary border must follow.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(chatModel)
	if m.focus != focusOutput {
		t.Fatalf("esc did not move focus to output, got %v", m.focus)
	}
	view2 := m.View().Content
	convTop2 := titleRow(view2, "Conversation")
	messageTop2 := titleRow(view2, "Message")
	if !strings.Contains(convTop2, primaryBorderSeq) {
		t.Fatalf("focused Conversation panel border should be primary after esc:\n%q", convTop2)
	}
	if strings.Contains(messageTop2, primaryBorderSeq) {
		t.Fatalf("blurred Message panel border should NOT be primary after esc:\n%q", messageTop2)
	}
}

// TestChatStreamingTitle asserts the streaming indicator and turn info fold into
// the panel titles: "Message · responding…" while streaming (plain "Message"
// when idle) and "Conversation · Turn N of M" once more than one turn exists.
func TestChatStreamingTitle(t *testing.T) {
	m := newReadyChat(t)
	m.connected = true

	// Idle, single turn: plain titles.
	m.turns = []turn{{question: "hi", answer: "hello"}}
	m.activeTurn = 0
	m.streaming = false
	if got := m.messageTitle(); got != "Message" {
		t.Fatalf("idle input title = %q, want %q", got, "Message")
	}
	idle := m.View().Content
	if strings.Contains(idle, "responding…") {
		t.Fatalf("idle view should not show responding:\n%s", idle)
	}
	if strings.Contains(idle, "Turn ") {
		t.Fatalf("single-turn view should not carry turn info:\n%s", idle)
	}

	// Streaming, multiple turns: input title shows responding; Conversation title
	// carries "Turn 2 of 2".
	m.turns = []turn{
		{question: "one", answer: "a"},
		{question: "two", answer: "b"},
	}
	m.activeTurn = 1
	m.streaming = true
	m.streamingTurn = 1

	if got := m.messageTitle(); got != "Message · responding…" {
		t.Fatalf("streaming input title = %q, want %q", got, "Message · responding…")
	}
	if got := m.conversationTitle(); got != "Conversation · Turn 2 of 2" {
		t.Fatalf("multi-turn conversation title = %q, want %q", got, "Conversation · Turn 2 of 2")
	}
	streaming := m.View().Content
	if !strings.Contains(streaming, "responding…") {
		t.Fatalf("streaming view should show responding in the Message title:\n%s", streaming)
	}
	if !strings.Contains(streaming, "Turn 2 of 2") {
		t.Fatalf("streaming view should carry turn info in the Conversation title:\n%s", streaming)
	}
}

// TestChatConnectingTitle asserts the transient "connecting…" title before the
// client connects, and that it drops once connected (never a persistent
// "Connected").
func TestChatConnectingTitle(t *testing.T) {
	m := newReadyChat(t)
	if got := m.conversationTitle(); got != "Conversation · connecting…" {
		t.Fatalf("pre-connect title = %q, want %q", got, "Conversation · connecting…")
	}
	m.connected = true
	if got := m.conversationTitle(); got != "Conversation" {
		t.Fatalf("post-connect title = %q, want %q (never persistent 'Connected')", got, "Conversation")
	}
	if strings.Contains(m.conversationTitle(), "Connected") {
		t.Fatalf("title must never read 'Connected'")
	}
}

// titleRow returns the rendered row containing want on a panel's top edge (the
// first row that contains the title text), so a test can inspect that panel's
// border color independent of the other panel.
func titleRow(view, want string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(ansiStrip(line), want) {
			return line
		}
	}
	return ""
}
