package harness

import (
	"encoding/json"
	"testing"

	"github.com/ryanflachman-liatrio/jig/harness/acp"
)

func TestACPExecutionSummarySurvivesUpdates(t *testing.T) {
	s := newTestSession()
	input := json.RawMessage(`{"command":"go test ./...","description":"Check tests"}`)
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "run", ToolKind: "execute", Title: "`go test ./...`", Input: input})
	events := drainEvents(s.events)
	if events[0].Tool.Summary != "Check tests" || events[0].Tool.Title != "`go test ./...`" || string(events[0].Tool.Input) != string(input) {
		t.Fatalf("initial tool lost metadata: %+v", events[0].Tool)
	}
	s.onEvent(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "run", HasContent: true, Content: []acp.Content{{Type: "text", Text: "output must not become a summary"}}})
	s.onEvent(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "run", Status: "completed"})
	events = drainEvents(s.events)
	if events[0].Tool.Summary != "Check tests" {
		t.Fatalf("summary lost on completion: %+v", events[0].Tool)
	}
}

func TestACPExecutionSummaryInputReplacement(t *testing.T) {
	for _, raw := range []string{`{"command":"pwd"}`, `{"description":42}`, `null`, `{"description":`} {
		s := newTestSession()
		s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "run", ToolKind: "execute", Input: json.RawMessage(`{"description":"Old description"}`)})
		drainEvents(s.events)
		s.onEvent(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "run", Input: json.RawMessage(raw), HasInput: true, Status: "completed"})
		if got := drainEvents(s.events)[0].Tool.Summary; got != "" {
			t.Errorf("replacement %s retained stale summary %q", raw, got)
		}
	}
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "read", ToolKind: "read", Input: json.RawMessage(`{"description":"Not an execution description"}`)})
	if got := drainEvents(s.events)[0].Tool.Summary; got != "" {
		t.Fatalf("non-execution summary = %q", got)
	}
}
