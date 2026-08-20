package harness

import (
	"context"
	"testing"
)

func TestCapabilitySetHas(t *testing.T) {
	all := NewCapabilitySet(
		CapPermissionCallback,
		CapUserQuestion,
		CapSessionResume,
		CapStructuredOutput,
		CapPartialStreaming,
	)

	tests := []struct {
		name string
		set  CapabilitySet
		cap  Capability
		want bool
	}{
		{"empty set lacks permission callback", CapabilitySet(0), CapPermissionCallback, false},
		{"empty set lacks user questions", CapabilitySet(0), CapUserQuestion, false},
		{"empty set lacks session resume", CapabilitySet(0), CapSessionResume, false},
		{"empty set lacks structured output", CapabilitySet(0), CapStructuredOutput, false},
		{"empty set lacks partial streaming", CapabilitySet(0), CapPartialStreaming, false},
		{"full set has permission callback", all, CapPermissionCallback, true},
		{"full set has user questions", all, CapUserQuestion, true},
		{"full set has session resume", all, CapSessionResume, true},
		{"full set has structured output", all, CapStructuredOutput, true},
		{"full set has partial streaming", all, CapPartialStreaming, true},
		{"single-capability set excludes others", NewCapabilitySet(CapPermissionCallback), CapUserQuestion, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.set.Has(tt.cap); got != tt.want {
				t.Errorf("Has(%v) = %v, want %v", tt.cap, got, tt.want)
			}
		})
	}
}

func TestFakeHarnessRoundTrip(t *testing.T) {
	scripted := []Event{
		{Type: EventText, Text: "hello"},
		{Type: EventToolUse, Name: "Bash", ToolUseID: "t1"},
		{Type: EventToolResult, ToolUseID: "t1", Content: "ok"},
	}
	sess := NewFakeSession(scripted)
	h := &FakeHarness{
		NameVal: "fake",
		Caps:    NewCapabilitySet(CapPermissionCallback, CapUserQuestion),
		Sess:    sess,
	}

	if got := h.Name(); got != "fake" {
		t.Fatalf("Name() = %q, want %q", got, "fake")
	}
	if !h.Capabilities().Has(CapPermissionCallback) {
		t.Fatalf("expected CapPermissionCallback advertised")
	}
	if h.Capabilities().Has(CapSessionResume) {
		t.Fatalf("did not expect CapSessionResume advertised")
	}

	ctx := context.Background()
	spec := SessionSpec{Prompt: "do the thing"}
	s, err := h.Open(ctx, spec)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if h.OpenSpec.Prompt != "do the thing" {
		t.Fatalf("OpenSpec.Prompt = %q, want %q", h.OpenSpec.Prompt, "do the thing")
	}

	var got []Event
	for e := range s.Messages() {
		got = append(got, e)
	}
	if len(got) != len(scripted) {
		t.Fatalf("got %d events, want %d", len(got), len(scripted))
	}
	for i, e := range got {
		if e.Type != scripted[i].Type {
			t.Errorf("event %d Type = %q, want %q", i, e.Type, scripted[i].Type)
		}
	}

	if err := s.Send(ctx, ToolResult{ToolUseID: "t1", Content: "answer"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(sess.Sent) != 1 || sess.Sent[0].Content != "answer" {
		t.Fatalf("Send() not recorded, got %+v", sess.Sent)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !sess.Closed {
		t.Fatalf("expected Close() to mark session closed")
	}
}

func TestFakeHarnessOpenErr(t *testing.T) {
	h := &FakeHarness{OpenErr: context.DeadlineExceeded}
	if _, err := h.Open(context.Background(), SessionSpec{}); err == nil {
		t.Fatalf("expected Open() to return the configured error")
	}
}
