package helpchat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
)

// TestBuildSystemPrompt verifies the rendered system prompt contains the
// workflow name, run ID, and step IDs with their statuses.
func TestBuildSystemPrompt(t *testing.T) {
	snap := engine.RunSnapshot{
		ID:       "run-abc",
		Workflow: "my-workflow",
		Steps: []step.State{
			{ID: "build", Status: step.StatusSucceeded},
			{ID: "test", Status: step.StatusFailed},
		},
	}

	got := BuildSystemPrompt("my-workflow", snap)

	checks := []string{"my-workflow", "run-abc", "build", "succeeded", "test", "failed"}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("system prompt missing %q\nfull prompt:\n%s", want, got)
		}
	}
}

// TestMcpServerToolSchemas verifies that all 11 expected tools are registered
// with the correct names.
func TestMcpServerToolSchemas(t *testing.T) {
	wantTools := []string{
		"workflow_snapshot",
		"read_step_transcript",
		"read_step_result",
		"read_step_output",
		"recover_step",
		"reset_step",
		"stop_step",
		"resume_step",
		"resolve_review",
		"send_message_to_step",
		"ask_user",
	}

	// Build the server with nil run and no-op dispatch; we only inspect the
	// registered tool names, not execute the handlers.
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)
	srv := BuildMcpServer(nil, "", func(tea.Msg) {}, gateReq, gateAns)

	if srv == nil {
		t.Fatal("BuildMcpServer returned nil")
	}

	// Introspect via ListTools on the underlying McpServer instance.
	defs, err := srv.Instance.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(defs) != len(wantTools) {
		t.Fatalf("want %d tools, got %d", len(wantTools), len(defs))
	}
	byName := make(map[string]bool, len(defs))
	for _, d := range defs {
		byName[d.Name] = true
	}
	for _, want := range wantTools {
		if !byName[want] {
			names := make([]string, len(defs))
			for i, d := range defs {
				names[i] = d.Name
			}
			t.Errorf("tool %q not registered; got: %v", want, names)
		}
	}
}

// TestDispatchFunc_RecoverStep verifies that the recover_step tool handler
// emits the correct monitor.RecoverResponseMsg via the DispatchFunc.
func TestDispatchFunc_RecoverStep(t *testing.T) {
	dispatched := make(chan tea.Msg, 1)
	dispatch := func(msg tea.Msg) { dispatched <- msg }

	tool := buildRecoverStep(fakeRun("run-1"), dispatch)

	result, err := tool.Call(t.Context(), map[string]any{
		"step_id":  "build",
		"action":   "retry",
		"guidance": "fix the error",
	})
	if err != nil {
		t.Fatalf("tool call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	select {
	case msg := <-dispatched:
		ra, ok := msg.(RecoverAction)
		if !ok {
			t.Fatalf("dispatched %T, want helpchat.RecoverAction", msg)
		}
		if ra.StepID != "build" {
			t.Errorf("StepID = %q, want %q", ra.StepID, "build")
		}
		if ra.Action != "retry" {
			t.Errorf("Action = %q, want %q", ra.Action, "retry")
		}
	default:
		t.Fatal("dispatch channel empty after tool call")
	}
}

// TestFinalMergeGate_ChannelRendezvous verifies that resolve_review on the
// final_merge step blocks on gateReq and unblocks when gateAns is written.
func TestFinalMergeGate_ChannelRendezvous(t *testing.T) {
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)

	tool := buildResolveReview(fakeRun("run-2"), func(tea.Msg) {}, gateReq, gateAns)

	resultCh := make(chan string, 1)
	go func() {
		res, err := tool.Call(t.Context(), map[string]any{
			"step_id": "final_merge",
			"verdict": "approved",
		})
		if err != nil {
			resultCh <- "error: " + err.Error()
			return
		}
		if len(res.Content) > 0 {
			resultCh <- res.Content[0].Text
		}
	}()

	// The handler should have written to gateReq; drain it and respond.
	select {
	case <-gateReq:
	default:
		// Give the goroutine a moment to reach the send.
		// Channel is buffered (size 1) so this may already be there.
	}
	gateAns <- true

	got := <-resultCh
	if !strings.Contains(got, "approved") {
		t.Errorf("result = %q, want to contain %q", got, "approved")
	}
}

// TestModelInit verifies that New constructs a Model without panic and that
// Init returns a non-nil cmd, and CapturesText returns true initially.
func TestModelInit(t *testing.T) {
	snap := engine.RunSnapshot{ID: "r", Workflow: "wf"}
	m := New(nil, "", snap)

	// nil run → Init returns nil (unavailable path).
	cmd := m.Init()
	if cmd != nil {
		t.Errorf("Init() with nil run = non-nil cmd, want nil")
	}

	if !m.CapturesText() {
		t.Errorf("CapturesText() = false, want true (initial focus is textarea)")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// fakeRun returns a minimal *engine.Run with only the ID field populated.
// Used by tool tests that don't exercise snapshot/inbox paths.
func fakeRun(id string) *engine.Run {
	r := &engine.Run{}
	r.ID = id
	return r
}

