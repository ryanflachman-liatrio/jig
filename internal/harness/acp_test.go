package harness

import (
	"reflect"
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
	if !caps.Has(CapPermissionCallback) {
		t.Errorf("Capabilities() missing CapPermissionCallback")
	}
	for _, c := range []Capability{CapInProcessMCP, CapSessionResume, CapStructuredOutput, CapPartialStreaming} {
		if caps.Has(c) {
			t.Errorf("Capabilities() advertises unimplemented capability %v", c)
		}
	}
}

func TestTranslateEvent(t *testing.T) {
	tests := []struct {
		name string
		in   acp.Event
		want []Event
	}{
		{
			name: "message chunk",
			in:   acp.Event{Kind: acp.EventMessage, Text: "hello"},
			want: []Event{{Type: EventText, Text: "hello"}, {Type: EventAssistantEnd}},
		},
		{
			name: "thought chunk",
			in:   acp.Event{Kind: acp.EventThought, Text: "thinking..."},
			want: []Event{{Type: EventThinking, Text: "thinking..."}, {Type: EventAssistantEnd}},
		},
		{
			name: "tool call",
			in:   acp.Event{Kind: acp.EventToolCall, ToolID: "call_1", Title: "Read file.go"},
			want: []Event{
				{Type: EventToolUse, ToolUseID: "call_1", Name: "Read file.go"},
				{Type: EventAssistantEnd},
			},
		},
		{
			name: "tool call update completed",
			in:   acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "call_1", Status: "completed"},
			want: []Event{
				{Type: EventToolResult, ToolUseID: "call_1", Content: "completed", IsError: false},
				{Type: EventUserEnd},
			},
		},
		{
			name: "tool call update failed",
			in:   acp.Event{Kind: acp.EventToolCallUpdate, ToolID: "call_1", Status: "failed"},
			want: []Event{
				{Type: EventToolResult, ToolUseID: "call_1", Content: "failed", IsError: true},
				{Type: EventUserEnd},
			},
		},
		{
			name: "unknown kind ignored",
			in:   acp.Event{Kind: acp.EventPlan},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateEvent(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("translateEvent() = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if !reflect.DeepEqual(got[i], tt.want[i]) {
					t.Errorf("translateEvent()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
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
