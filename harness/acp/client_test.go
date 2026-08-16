package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func textBlock(s string) acpsdk.ContentBlock {
	return acpsdk.TextBlock(s)
}

func TestSessionUpdate_CapturesEachEventKind(t *testing.T) {
	tests := []struct {
		name  string
		notif acpsdk.SessionNotification
		want  Event
	}{
		{
			name: "agent message chunk",
			notif: acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
				AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{Content: textBlock("hello")},
			}},
			want: Event{Kind: "message", Text: "hello"},
		},
		{
			name: "agent thought chunk",
			notif: acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
				AgentThoughtChunk: &acpsdk.SessionUpdateAgentThoughtChunk{Content: textBlock("thinking...")},
			}},
			want: Event{Kind: "thought", Text: "thinking..."},
		},
		{
			name: "tool call",
			notif: acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
				ToolCall: &acpsdk.SessionUpdateToolCall{
					ToolCallId: "call_1",
					Title:      "Read file.go",
					Status:     acpsdk.ToolCallStatusPending,
				},
			}},
			want: Event{Kind: "tool_call", ToolID: "call_1", Title: "Read file.go", Status: "pending"},
		},
		{
			name: "tool call update",
			notif: acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
				ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
					ToolCallId: "call_1",
					Status:     statusPtr(acpsdk.ToolCallStatusCompleted),
				},
			}},
			want: Event{Kind: "tool_call_update", ToolID: "call_1", Status: "completed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{}
			if err := c.SessionUpdate(context.Background(), tt.notif); err != nil {
				t.Fatalf("SessionUpdate returned error: %v", err)
			}
			got := c.Events()
			if len(got) != 1 {
				t.Fatalf("Events() = %v, want exactly 1 event", got)
			}
			if got[0] != tt.want {
				t.Errorf("Events()[0] = %+v, want %+v", got[0], tt.want)
			}
		})
	}
}

func statusPtr(s acpsdk.ToolCallStatus) *acpsdk.ToolCallStatus { return &s }

func permissionRequest(options ...acpsdk.PermissionOption) acpsdk.RequestPermissionRequest {
	return acpsdk.RequestPermissionRequest{
		SessionId: "sess_1",
		ToolCall:  acpsdk.ToolCallUpdate{ToolCallId: "call_1"},
		Options:   options,
	}
}

func TestRequestPermission_AllowDecision(t *testing.T) {
	c := &Client{Decide: func(acpsdk.ToolCallUpdate) bool { return true }}
	req := permissionRequest(
		acpsdk.PermissionOption{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
		acpsdk.PermissionOption{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
	)

	resp, err := c.RequestPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestPermission returned error: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow" {
		t.Fatalf("Outcome = %+v, want Selected.OptionId=\"allow\"", resp.Outcome)
	}
	if got := c.PermissionRequests(); len(got) != 1 {
		t.Fatalf("PermissionRequests() = %v, want exactly 1 recorded request", got)
	}
}

func TestRequestPermission_DenyDecision(t *testing.T) {
	c := &Client{Decide: func(acpsdk.ToolCallUpdate) bool { return false }}
	req := permissionRequest(
		acpsdk.PermissionOption{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
		acpsdk.PermissionOption{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
	)

	resp, err := c.RequestPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestPermission returned error: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "reject" {
		t.Fatalf("Outcome = %+v, want Selected.OptionId=\"reject\" (deny must not fall through to allow)", resp.Outcome)
	}
}

func TestRequestPermission_NoMatchingOption_Cancels(t *testing.T) {
	c := &Client{Decide: func(acpsdk.ToolCallUpdate) bool { return true }}
	req := permissionRequest(
		acpsdk.PermissionOption{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
	)

	resp, err := c.RequestPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestPermission returned error: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("Outcome = %+v, want Cancelled when no allow option is offered", resp.Outcome)
	}
}

func TestRequestPermission_NilDecider_DeniesByDefault(t *testing.T) {
	c := &Client{}
	req := permissionRequest(
		acpsdk.PermissionOption{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
		acpsdk.PermissionOption{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
	)

	resp, err := c.RequestPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestPermission returned error: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "reject" {
		t.Fatalf("Outcome = %+v, want fail-closed default of Selected.OptionId=\"reject\"", resp.Outcome)
	}
}

func TestRun_FailsFastWhenNpxMissing(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := Run(context.Background(), ".", "hello", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want a fail-fast error when npx is not on PATH")
	}
}
