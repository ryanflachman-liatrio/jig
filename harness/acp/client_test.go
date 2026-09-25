package acp

import (
	"context"
	"encoding/json"
	"reflect"
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
			want: Event{Kind: EventToolCall, ToolID: "call_1", Title: "Read file.go", Status: "pending", Input: json.RawMessage(`{"file_path":"/tmp/file.go"}`), HasTitle: true, HasStatus: true, HasKind: true, HasInput: true},
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
			want: Event{Kind: EventToolCallUpdate, ToolID: "call_1", Status: "completed", Input: json.RawMessage(`{"file_path":"/tmp/file.go"}`), HasStatus: true, HasInput: true},
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
			if !reflect.DeepEqual(got[0], tt.want) {
				t.Errorf("Events()[0] = %+v, want %+v", got[0], tt.want)
			}
		})
	}
}

func TestSessionUpdate_PreservesStructuredDiff(t *testing.T) {
	old := "before\n"
	c := &Client{}
	err := c.SessionUpdate(context.Background(), acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		ToolCallId: "edit-1", Status: statusPtr(acpsdk.ToolCallStatusCompleted),
		Content: []acpsdk.ToolCallContent{{Diff: &acpsdk.ToolCallContentDiff{Type: "diff", Path: "main.go", OldText: &old, NewText: "after\n"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got := c.Events()
	if len(got) != 1 || !got[0].HasContent || len(got[0].Content) != 1 || got[0].Content[0].Diff == nil {
		t.Fatalf("events = %+v", got)
	}
	diff := got[0].Content[0].Diff
	if diff.Path != "main.go" || diff.OldText == nil || *diff.OldText != old || diff.NewText != "after\n" {
		t.Fatalf("diff = %+v", diff)
	}
}

func TestSessionUpdate_SuppressesReplayBeforeCaptureAndForwarding(t *testing.T) {
	forwarded := 0
	c := &Client{OnUpdate: func(Event) { forwarded++ }}
	c.setReplaying(true)
	if err := c.SessionUpdate(context.Background(), acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
		AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{Content: textBlock("history")},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := c.Events(); len(got) != 0 || forwarded != 0 {
		t.Fatalf("replay captured or forwarded: %v, %d", got, forwarded)
	}
	c.setReplaying(false)
	if err := c.SessionUpdate(context.Background(), acpsdk.SessionNotification{Update: acpsdk.SessionUpdate{
		AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{Content: textBlock("fresh")},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := c.Events(); len(got) != 1 || got[0].Text != "fresh" || forwarded != 1 {
		t.Fatalf("fresh update = %v, forwarded %d", got, forwarded)
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
	c := &Client{Decide: func(context.Context, acpsdk.ToolCallUpdate) bool { return true }}
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
	c := &Client{Decide: func(context.Context, acpsdk.ToolCallUpdate) bool { return false }}
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

// TestRequestPermissionAllowOnce proves an allow selects only allow_once, and
// that a request offering no allow_once option is rejected (or cancelled when
// it offers no reject option either), so jig never persists an allow_always
// rule on the agent's side.
func TestRequestPermissionAllowOnce(t *testing.T) {
	allowAlways := acpsdk.PermissionOption{OptionId: "always", Name: "Always allow", Kind: acpsdk.PermissionOptionKindAllowAlways}
	allowOnce := acpsdk.PermissionOption{OptionId: "once", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce}
	rejectOnce := acpsdk.PermissionOption{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce}
	rejectAlways := acpsdk.PermissionOption{OptionId: "never", Name: "Always reject", Kind: acpsdk.PermissionOptionKindRejectAlways}
	for _, tc := range []struct {
		name         string
		options      []acpsdk.PermissionOption
		wantSelected string // empty means cancelled
	}{
		{name: "allow selects allow_once even when allow_always is listed first", options: []acpsdk.PermissionOption{allowAlways, allowOnce, rejectOnce}, wantSelected: "once"},
		{name: "allow_always only is rejected", options: []acpsdk.PermissionOption{allowAlways, rejectOnce}, wantSelected: "reject"},
		{name: "allow_always only falls back to reject_always", options: []acpsdk.PermissionOption{allowAlways, rejectAlways}, wantSelected: "never"},
		{name: "allow_always only with no reject option cancels", options: []acpsdk.PermissionOption{allowAlways}},
		{name: "no allow option is rejected", options: []acpsdk.PermissionOption{rejectOnce}, wantSelected: "reject"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{Decide: func(context.Context, acpsdk.ToolCallUpdate) bool { return true }}
			resp, err := c.RequestPermission(context.Background(), permissionRequest(tc.options...))
			if err != nil {
				t.Fatalf("RequestPermission returned error: %v", err)
			}
			switch {
			case tc.wantSelected == "":
				if resp.Outcome.Cancelled == nil {
					t.Fatalf("Outcome = %+v, want Cancelled", resp.Outcome)
				}
			case resp.Outcome.Selected == nil || string(resp.Outcome.Selected.OptionId) != tc.wantSelected:
				t.Fatalf("Outcome = %+v, want Selected.OptionId=%q", resp.Outcome, tc.wantSelected)
			}
		})
	}
}

// TestRequestPermissionPassesContext proves the Decider receives the
// request's own context, so a waiting decision observes cancellation.
func TestRequestPermissionPassesContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "request")
	var got any
	c := &Client{Decide: func(ctx context.Context, _ acpsdk.ToolCallUpdate) bool {
		got = ctx.Value(key{})
		return false
	}}
	if _, err := c.RequestPermission(ctx, permissionRequest()); err != nil {
		t.Fatal(err)
	}
	if got != "request" {
		t.Fatalf("Decider context value = %v, want the request context", got)
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

// TestSessionUpdateCarriesToolMeta proves a tool call's _meta (Claude's
// canonical toolName travels there) reaches the captured event.
func TestSessionUpdateCarriesToolMeta(t *testing.T) {
	c := &Client{}
	meta := map[string]any{"claudeCode": map[string]any{"toolName": "Bash"}}
	start := acpsdk.StartToolCall("call_1", "curl example")
	start.ToolCall.Meta = meta
	update := acpsdk.UpdateToolCall("call_1", acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted))
	update.ToolCallUpdate.Meta = meta
	for _, u := range []acpsdk.SessionUpdate{start, update} {
		if err := c.SessionUpdate(context.Background(), acpsdk.SessionNotification{SessionId: "sess_1", Update: u}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(c.Events()); got != 2 {
		t.Fatalf("captured %d events, want 2", got)
	}
	for _, ev := range c.Events() {
		if name, _ := ev.Meta["claudeCode"].(map[string]any)["toolName"].(string); name != "Bash" {
			t.Fatalf("%s event meta = %#v, want claudeCode.toolName", ev.Kind, ev.Meta)
		}
	}
}
