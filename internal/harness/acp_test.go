package harness

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
	"jig/internal/interaction"
)

func TestAcpHarnessCapabilities(t *testing.T) {
	h := NewAcpHarness()
	if h.Name() != "acp" {
		t.Fatalf("Name() = %q, want %q", h.Name(), "acp")
	}
	caps := h.Capabilities()
	for _, c := range []Capability{CapPermissionCallback, CapUserQuestion, CapPartialStreaming, CapStructuredOutput} {
		if !caps.Has(c) {
			t.Errorf("Capabilities() missing %v", c)
		}
	}
	for _, c := range []Capability{CapSessionResume} {
		if caps.Has(c) {
			t.Errorf("Capabilities() advertises unimplemented capability %v", c)
		}
	}
}

func TestACPQuestionTranslation(t *testing.T) {
	params := acpsdk.UnstableCreateElicitationRequest{
		Form: &acpsdk.UnstableCreateElicitationForm{
			Message: "Choose",
			Mode:    "form",
			RequestedSchema: acpsdk.UnstableElicitationSchema{
				Type: acpsdk.UnstableElicitationSchemaTypeObject,
				Properties: map[string]any{
					"question_0": map[string]any{
						"type":        "string",
						"title":       "Format",
						"description": "Choose a format",
						"oneOf": []any{
							map[string]any{"const": "json", "title": "JSON", "description": "Structured"},
							map[string]any{"const": "text", "title": "Text"},
						},
					},
					"question_0_custom": map[string]any{
						"type": "string",
						"_meta": map[string]any{
							"_askUserQuestionCustomAnswer": map[string]any{
								"questionId": "question_0", "isCustomAnswer": true,
							},
						},
					},
					"question_1": map[string]any{
						"type":        "array",
						"description": "Choose features",
						"items": map[string]any{
							"anyOf": []any{
								map[string]any{"const": "cache", "title": "Cache"},
								map[string]any{"const": "retry", "title": "Retry"},
							},
						},
					},
				},
			},
		},
	}
	var got interaction.QuestionRequest
	elicit := newACPElicitor(func(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
		got = req
		return interaction.QuestionResponse{
			RequestID: req.ID,
			Action:    interaction.ActionAccept,
			Answers: map[string]interaction.Answer{
				"question_0": {Custom: "yaml"},
				"question_1": {Values: []string{"cache", "retry"}},
			},
		}
	})
	resp, err := elicit(context.Background(), params)
	if err != nil {
		t.Fatalf("elicitor() error = %v", err)
	}
	if len(got.Fields) != 2 || got.Fields[0].Kind != interaction.FieldSingleSelect ||
		got.Fields[1].Kind != interaction.FieldMultiSelect || !got.Fields[0].AllowCustom {
		t.Fatalf("translated request = %+v", got)
	}
	if resp.Accept == nil {
		t.Fatalf("response = %+v, want accept", resp)
	}
	if resp.Accept.Content["question_0_custom"] != "yaml" {
		t.Fatalf("custom content = %+v", resp.Accept.Content)
	}
	values, ok := resp.Accept.Content["question_1"].([]string)
	if !ok || !reflect.DeepEqual(values, []string{"cache", "retry"}) {
		t.Fatalf("multi content = %#v", resp.Accept.Content["question_1"])
	}
}

func TestACPQuestionRejectsUnsupportedField(t *testing.T) {
	_, _, err := parseACPQuestion(acpsdk.UnstableCreateElicitationRequest{
		Form: &acpsdk.UnstableCreateElicitationForm{
			Message: "Number",
			RequestedSchema: acpsdk.UnstableElicitationSchema{
				Properties: map[string]any{"count": map[string]any{"type": "number"}},
			},
		},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), `unsupported field type "number"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestACPSingleQuestionUsesFormMessage(t *testing.T) {
	req, _, err := parseACPQuestion(acpsdk.UnstableCreateElicitationRequest{
		Form: &acpsdk.UnstableCreateElicitationForm{
			Message: "Which format?",
			RequestedSchema: acpsdk.UnstableElicitationSchema{
				Properties: map[string]any{
					"question_0": map[string]any{
						"type":  "string",
						"title": "Format",
						"oneOf": []any{map[string]any{"const": "json", "title": "JSON"}},
					},
				},
			},
		},
	}, 1)
	if err != nil {
		t.Fatalf("parseACPQuestion() error = %v", err)
	}
	if req.Fields[0].Prompt != "Which format?" || req.Fields[0].Header != "Format" {
		t.Fatalf("field = %+v", req.Fields[0])
	}
}

// ── onEvent tests ────────────────────────────────────────────────────────────

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
	if s.pendingTools["A"].title != "undefined" {
		t.Errorf("pendingTools[A].title = %q, want %q", s.pendingTools["A"].title, "undefined")
	}
}

func TestOnEvent_ToolCallTitleUpdates(t *testing.T) {
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "undefined", Input: `{"query":"Go 1.25"}`})
	drainEvents(s.events) // consume any flush events (empty since no prior text)

	// Second EventToolCall for same ID: only updates title, no extra flush.
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "Go 1.25 release date"})
	got := drainEvents(s.events)
	if len(got) != 0 {
		t.Fatalf("title update should emit nothing, got %+v", got)
	}
	if s.pendingTools["A"].title != "Go 1.25 release date" {
		t.Errorf("pendingTools[A].title = %q after update", s.pendingTools["A"].title)
	}
	if string(s.pendingTools["A"].input) != `{"query":"Go 1.25"}` {
		t.Errorf("pendingTools[A].input = %s after title-only update", s.pendingTools["A"].input)
	}
}

func TestOnEvent_ToolCallUpdateEmitsToolUseAndResult(t *testing.T) {
	s := newTestSession()
	s.onEvent(acp.Event{Kind: acp.EventToolCall, ToolID: "A", Title: "Go 1.25 release date"})
	drainEvents(s.events) // consume any flush events

	s.onEvent(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "A", Status: "completed", Input: `{"query":"Go 1.25"}`})
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
	if string(got[0].Input) != `{"query":"Go 1.25"}` {
		t.Errorf("EventToolUse.Input = %s, want raw query input", got[0].Input)
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

// ── structured output helpers ────────────────────────────────────────────────

func TestAppendSchemaPrompt(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{"type": "string"},
		},
	}
	got := appendSchemaPrompt("Do the task.", schema)

	if !strings.HasPrefix(got, "Do the task.") {
		t.Error("original prompt must be preserved at the start")
	}
	if !strings.Contains(got, "```json-schema") {
		t.Error("schema fence (```json-schema) missing")
	}
	if !strings.Contains(got, "```json") {
		t.Error("output fence (```json) missing")
	}
	if !strings.Contains(got, `"summary"`) {
		t.Error("schema property 'summary' missing from injected text")
	}
	if !strings.Contains(got, "last") {
		t.Error("instruction to place JSON last missing")
	}
}

func TestExtractJSONFromText(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantKey string
		wantErr bool
	}{
		{
			name:    "clean fenced block",
			text:    "Here is the output:\n```json\n{\"status\":\"ok\"}\n```",
			wantKey: "status",
		},
		{
			name:    "picks last of two fenced blocks",
			text:    "```json\n{\"a\":1}\n```\nActually:\n```json\n{\"status\":\"final\"}\n```",
			wantKey: "status",
		},
		{
			name:    "fallback: whole trimmed text is JSON",
			text:    `{"status":"ok"}`,
			wantKey: "status",
		},
		{
			name:    "prose before fallback JSON",
			text:    "Here you go: \n{\"status\":\"ok\"}",
			wantErr: true, // prose makes the fallback fail; fence required
		},
		{
			name:    "no JSON anywhere",
			text:    "I cannot comply.",
			wantErr: true,
		},
		{
			name:    "empty response",
			text:    "",
			wantErr: true,
		},
		{
			name:    "unclosed fence falls back to whole text (invalid)",
			text:    "```json\n{\"x\":1}",
			wantErr: true,
		},
		{
			name:    "fence with invalid JSON falls back to whole text (also invalid)",
			text:    "```json\nnot json\n```",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSONFromText(tt.text)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result: %s)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(got, &m); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			if _, ok := m[tt.wantKey]; !ok {
				t.Errorf("key %q missing from result: %s", tt.wantKey, got)
			}
		})
	}
}

func TestBuildStructuredRetryPrompt(t *testing.T) {
	msg := buildStructuredRetryPrompt(2, "unexpected end of JSON input")
	if !strings.Contains(msg, "attempt 2") {
		t.Error("attempt number missing")
	}
	if !strings.Contains(msg, "unexpected end of JSON input") {
		t.Error("parse error text missing")
	}
	if !strings.Contains(msg, "```json") {
		t.Error("format reminder missing")
	}
}

// ── toolCallName / toolCallInput ─────────────────────────────────────────────

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
