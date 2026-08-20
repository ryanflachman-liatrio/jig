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

// ── acpTranslator tests ──────────────────────────────────────────────────────

// newTestTranslator returns a translator wired to a buffered channel for
// inspection, and the channel itself.
func newTestTranslator() (*acpTranslator, <-chan Event) {
	ch := make(chan Event, 64)
	return &acpTranslator{out: ch}, ch
}

// drainTranslator collects all currently-buffered events without blocking.
func drainTranslator(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestAcpTranslatorMessageChunks(t *testing.T) {
	tr, ch := newTestTranslator()
	tr.handle(acp.Event{Kind: acp.EventMessage, Text: "Hello"})
	tr.handle(acp.Event{Kind: acp.EventMessage, Text: ", "})
	tr.handle(acp.Event{Kind: acp.EventMessage, Text: "world"})
	tr.flushAll()

	got := drainTranslator(ch)
	want := []Event{
		{Type: EventTextDelta, Text: "Hello"},
		{Type: EventTextDelta, Text: ", "},
		{Type: EventTextDelta, Text: "world"},
		{Type: EventText, Text: "Hello, world"},
		{Type: EventAssistantEnd},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestAcpTranslatorToolCallFlushesText(t *testing.T) {
	tr, ch := newTestTranslator()
	tr.handle(acp.Event{Kind: acp.EventMessage, Text: "Let me check."})
	tr.handle(acp.Event{Kind: acp.EventToolCall, ToolID: "call_1", Title: "Read"})

	got := drainTranslator(ch)
	want := []Event{
		{Type: EventTextDelta, Text: "Let me check."},
		{Type: EventText, Text: "Let me check."},
		{Type: EventAssistantEnd},
		{Type: EventToolUse, ToolUseID: "call_1", Name: "Read"},
		{Type: EventAssistantEnd},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestAcpTranslatorToolCallUpdate(t *testing.T) {
	tr, ch := newTestTranslator()
	tr.handle(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "call_1", Status: "completed"})
	tr.handle(acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "call_2", Status: "failed"})

	got := drainTranslator(ch)
	want := []Event{
		{Type: EventToolResult, ToolUseID: "call_1", Content: "completed", IsError: false},
		{Type: EventUserEnd},
		{Type: EventToolResult, ToolUseID: "call_2", Content: "failed", IsError: true},
		{Type: EventUserEnd},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestAcpTranslatorThoughtChunks(t *testing.T) {
	tr, ch := newTestTranslator()
	tr.handle(acp.Event{Kind: acp.EventThought, Text: "Let me think"})
	tr.handle(acp.Event{Kind: acp.EventThought, Text: " about this."})
	// Switching to text flushes the accumulated thought.
	tr.handle(acp.Event{Kind: acp.EventMessage, Text: "Answer"})
	tr.flushAll()

	got := drainTranslator(ch)
	want := []Event{
		{Type: EventThinking, Text: "Let me think about this."},
		{Type: EventAssistantEnd},
		{Type: EventTextDelta, Text: "Answer"},
		{Type: EventText, Text: "Answer"},
		{Type: EventAssistantEnd},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestAcpTranslatorUnknownEventIgnored(t *testing.T) {
	tr, ch := newTestTranslator()
	tr.handle(acp.Event{Kind: acp.EventPlan})
	if got := drainTranslator(ch); len(got) != 0 {
		t.Fatalf("expected no events for EventPlan, got %+v", got)
	}
}

func TestAccumulatedText(t *testing.T) {
	tr, ch := newTestTranslator()
	tr.handle(acp.Event{Kind: acp.EventMessage, Text: "hello"})
	tr.handle(acp.Event{Kind: acp.EventMessage, Text: " world"})

	// AccumulatedText must return the full buffer without clearing it.
	if got := tr.AccumulatedText(); got != "hello world" {
		t.Fatalf("AccumulatedText() = %q, want %q", got, "hello world")
	}

	// Drain the two EventTextDelta events emitted by handle.
	drainTranslator(ch)

	// flushAll must still emit EventText (buffer was NOT cleared by AccumulatedText).
	tr.flushAll()
	evs := drainTranslator(ch)
	found := false
	for _, ev := range evs {
		if ev.Type == EventText && ev.Text == "hello world" {
			found = true
		}
	}
	if !found {
		t.Errorf("EventText not emitted after AccumulatedText(); events: %+v", evs)
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
