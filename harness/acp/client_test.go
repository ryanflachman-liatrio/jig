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
			want: Event{Kind: EventMessage, Text: "hello"},
		},
		{
			name: "agent thought chunk",
			notif: acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
				AgentThoughtChunk: &acpsdk.SessionUpdateAgentThoughtChunk{Content: textBlock("thinking...")},
			}},
			want: Event{Kind: EventThought, Text: "thinking..."},
		},
		{
			name: "tool call",
			notif: acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
				ToolCall: &acpsdk.SessionUpdateToolCall{
					ToolCallId: "call_1",
					Title:      "Read file.go",
					Status:     acpsdk.ToolCallStatusPending,
					RawInput:   map[string]any{"file_path": "/tmp/file.go"},
				},
			}},
			want: Event{Kind: EventToolCall, ToolID: "call_1", Title: "Read file.go", Status: "pending", Input: `{"file_path":"/tmp/file.go"}`},
		},
		{
			name: "tool call update",
			notif: acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
				ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
					ToolCallId: "call_1",
					Status:     statusPtr(acpsdk.ToolCallStatusCompleted),
					RawInput:   map[string]any{"file_path": "/tmp/file.go"},
				},
			}},
			want: Event{Kind: EventToolCallUpdate, ToolID: "call_1", Status: "completed", Input: `{"file_path":"/tmp/file.go"}`},
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

func TestCreateElicitationDelegatesAndRecords(t *testing.T) {
	req := acpsdk.UnstableCreateElicitationRequest{
		Form: &acpsdk.UnstableCreateElicitationForm{
			Mode:    "form",
			Message: "Choose",
			RequestedSchema: acpsdk.UnstableElicitationSchema{
				Properties: map[string]any{"answer": map[string]any{"type": "string"}},
			},
		},
	}
	c := &Client{Elicit: func(
		_ context.Context,
		got acpsdk.UnstableCreateElicitationRequest,
	) (acpsdk.UnstableCreateElicitationResponse, error) {
		if got.Form == nil || got.Form.Message != "Choose" {
			t.Fatalf("request = %+v", got)
		}
		resp := acpsdk.NewUnstableCreateElicitationResponseAccept()
		resp.Accept.Content = map[string]any{"answer": "yes"}
		return resp, nil
	}}
	resp, err := c.UnstableCreateElicitation(context.Background(), req)
	if err != nil {
		t.Fatalf("UnstableCreateElicitation() error = %v", err)
	}
	if resp.Accept == nil || resp.Accept.Content["answer"] != "yes" {
		t.Fatalf("response = %+v", resp)
	}
	if got := c.ElicitationRequests(); len(got) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(got))
	}
}

func TestCreateElicitationWithoutHandlerCancels(t *testing.T) {
	resp, err := (&Client{}).UnstableCreateElicitation(
		context.Background(),
		acpsdk.UnstableCreateElicitationRequest{Form: &acpsdk.UnstableCreateElicitationForm{}},
	)
	if err != nil {
		t.Fatalf("UnstableCreateElicitation() error = %v", err)
	}
	if resp.Cancel == nil {
		t.Fatalf("response = %+v, want cancel", resp)
	}
}

func TestElicitationCapabilityIsConditional(t *testing.T) {
	if got := clientCapabilities(nil); got.Elicitation != nil {
		t.Fatalf("non-interactive capabilities advertise elicitation: %+v", got)
	}
	elicit := Elicitor(func(
		context.Context,
		acpsdk.UnstableCreateElicitationRequest,
	) (acpsdk.UnstableCreateElicitationResponse, error) {
		return acpsdk.NewUnstableCreateElicitationResponseCancel(), nil
	})
	got := clientCapabilities(elicit)
	if got.Elicitation == nil || got.Elicitation.Form == nil {
		t.Fatalf("interactive capabilities = %+v, want elicitation.form", got)
	}
	if got.Elicitation.Url != nil {
		t.Fatalf("interactive capabilities unexpectedly advertise URL elicitation")
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
