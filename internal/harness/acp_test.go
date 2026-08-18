package harness

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
)

func TestAcpHarnessCapabilities(t *testing.T) {
	h := NewAcpHarness()
	if h.Name() != "acp" {
		t.Fatalf("Name() = %q, want %q", h.Name(), "acp")
	}
	caps := h.Capabilities()
	for _, c := range []Capability{CapPermissionCallback, CapStructuredOutput} {
		if !caps.Has(c) {
			t.Errorf("Capabilities() missing %v", c)
		}
	}
	for _, c := range []Capability{CapInProcessMCP, CapSessionResume, CapPartialStreaming} {
		if caps.Has(c) {
			t.Errorf("Capabilities() advertises unimplemented capability %v", c)
		}
	}
}

// drainEvents drains all events from ch into a slice (non-blocking).
func drainEvents(ch chan Event) []Event {
	var out []Event
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}

// newTestSession returns an acpSession wired to a buffered channel for testing
// onEvent without a live ACP connection.
func newTestSession() *acpSession {
	return &acpSession{events: make(chan Event, 64)}
}

func TestOnEvent_TextChunksGrouped(t *testing.T) {
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventMessage, Text: "Hello"})
	s.onEvent(acp.Event{Kind: acp.EventMessage, Text: ", world"})

	// No AssistantEnd yet — text chunks just accumulate.
	got := drainEvents(s.events)
	if len(got) != 2 {
		t.Fatalf("want 2 EventText events, got %d: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Type != EventText {
			t.Errorf("unexpected event type %v, want EventText", e.Type)
		}
	}
	if !s.hasTextSinceFlush {
		t.Error("hasTextSinceFlush should be true after text events")
	}

	// Explicit flush (as run() does after Prompt returns) closes the entry.
	s.flushText()
	flushed := drainEvents(s.events)
	if len(flushed) != 1 || flushed[0].Type != EventAssistantEnd {
		t.Errorf("flushText() = %+v, want [EventAssistantEnd]", flushed)
	}
	if s.hasTextSinceFlush {
		t.Error("hasTextSinceFlush should be false after flush")
	}
}

func TestOnEvent_ThinkingGrouped(t *testing.T) {
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventThought, Text: "reasoning"})
	got := drainEvents(s.events)
	if len(got) != 1 || got[0].Type != EventThinking {
		t.Fatalf("want [EventThinking], got %+v", got)
	}
	if !s.hasTextSinceFlush {
		t.Error("hasTextSinceFlush should be true after thought")
	}
}

func TestOnEvent_ToolCallFlushesText(t *testing.T) {
	s := newTestSession()
	// Text arrives before tool call.
	s.onEvent(acp.Event{Kind: acp.EventMessage, Text: "I'll search."})
	drainEvents(s.events) // consume text event

	// First EventToolCall for a new ID flushes the preceding text.
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "undefined"})
	got := drainEvents(s.events)
	if len(got) != 1 || got[0].Type != EventAssistantEnd {
		t.Fatalf("first new tool call should flush text via EventAssistantEnd, got %+v", got)
	}
	if s.hasTextSinceFlush {
		t.Error("hasTextSinceFlush should be false after flush")
	}
	// Title buffered.
	if s.pendingTools["A"] != "undefined" {
		t.Errorf("pendingTools[A] = %q, want %q", s.pendingTools["A"], "undefined")
	}
}

func TestOnEvent_ToolCallTitleUpdates(t *testing.T) {
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "undefined"})
	drainEvents(s.events) // consume AssistantEnd (empty flush since no prior text)

	// Second EventToolCall for same ID: only updates title, no extra flush.
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "Go 1.25 release date"})
	got := drainEvents(s.events)
	if len(got) != 0 {
		t.Fatalf("title update should emit nothing, got %+v", got)
	}
	if s.pendingTools["A"] != "Go 1.25 release date" {
		t.Errorf("pendingTools[A] = %q after update", s.pendingTools["A"])
	}
}

func TestOnEvent_ToolCallUpdateEmitsToolUseAndResult(t *testing.T) {
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "Go 1.25 release date"})
	drainEvents(s.events) // consume any flush events

	s.onEvent(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "A", Status: "completed"})
	got := drainEvents(s.events)
	// Expect: EventToolUse, EventAssistantEnd, EventToolResult, EventUserEnd.
	want := []EventType{EventToolUse, EventAssistantEnd, EventToolResult, EventUserEnd}
	if len(got) != len(want) {
		t.Fatalf("want %d events, got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Type != w {
			t.Errorf("[%d] type = %v, want %v", i, got[i].Type, w)
		}
	}
	if got[0].Name != "Go 1.25 release date" {
		t.Errorf("EventToolUse.Name = %q, want %q", got[0].Name, "Go 1.25 release date")
	}
	if got[0].ToolUseID != "A" {
		t.Errorf("EventToolUse.ToolUseID = %q, want A", got[0].ToolUseID)
	}
	if got[2].IsError {
		t.Error("completed status should not be an error")
	}
	if _, ok := s.pendingTools["A"]; ok {
		t.Error("tool A should be removed from pendingTools after update")
	}
}

func TestOnEvent_ToolCallUpdateFailed(t *testing.T) {
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "B", Title: "some tool"})
	drainEvents(s.events)

	s.onEvent(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "B", Status: "failed"})
	got := drainEvents(s.events)
	// [EventToolUse, EventAssistantEnd, EventToolResult(isError=true), EventUserEnd]
	if len(got) < 3 {
		t.Fatalf("too few events: %+v", got)
	}
	if !got[2].IsError {
		t.Error("failed status should set IsError=true")
	}
}

func TestOnEvent_FullTurnSequence(t *testing.T) {
	// Text chunks → tool call (with title streaming) → tool result → final text.
	s := newTestSession()
	var allEvents []Event
	collect := func() { allEvents = append(allEvents, drainEvents(s.events)...) }

	s.onEvent(acp.Event{Kind: acp.EventMessage, Text: "I'll search."})
	collect()
	s.onEvent(acp.Event{Kind: acp.EventMessage, Text: " Stand by."})
	collect()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "undefined"})
	collect()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "Web search"})
	collect()
	s.onEvent(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "A", Status: "completed"})
	collect()
	s.onEvent(acp.Event{Kind: acp.EventMessage, Text: "Found it."})
	collect()
	s.flushText() // simulates run() after Prompt returns
	collect()

	// Expected sequence:
	// EventText("I'll search.")          — chunk 1
	// EventText(" Stand by.")            — chunk 2
	// EventAssistantEnd                  — flush before first tool call
	// EventToolUse(A, "Web search")      — emitted on update
	// EventAssistantEnd                  — end of tool-use entry
	// EventToolResult(A, completed)
	// EventUserEnd
	// EventText("Found it.")             — final text
	// EventAssistantEnd                  — flush after Prompt returns
	wantTypes := []EventType{
		EventText, EventText,
		EventAssistantEnd,
		EventToolUse, EventAssistantEnd,
		EventToolResult, EventUserEnd,
		EventText,
		EventAssistantEnd,
	}
	if len(allEvents) != len(wantTypes) {
		t.Fatalf("got %d events, want %d:\n%+v", len(allEvents), len(wantTypes), allEvents)
	}
	for i, wt := range wantTypes {
		if allEvents[i].Type != wt {
			t.Errorf("[%d] type=%v, want %v", i, allEvents[i].Type, wt)
		}
	}
}

func TestToolCallInput(t *testing.T) {
	title := "Bash"
	tests := []struct {
		name       string
		tc         acpsdk.ToolCallUpdate
		wantName   string
		wantFields int
	}{
		{
			name:       "with title and object input",
			tc:         acpsdk.ToolCallUpdate{Title: &title, RawInput: map[string]any{"command": "ls"}},
			wantName:   "Bash",
			wantFields: 1,
		},
		{
			name:       "no title, non-object input",
			tc:         acpsdk.ToolCallUpdate{RawInput: "not an object"},
			wantName:   "",
			wantFields: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolCallName(tt.tc); got != tt.wantName {
				t.Errorf("toolCallName() = %q, want %q", got, tt.wantName)
			}
			if got := toolCallInput(tt.tc); len(got) != tt.wantFields {
				t.Errorf("toolCallInput() = %v, want %d field(s)", got, tt.wantFields)
			}
		})
	}
}
