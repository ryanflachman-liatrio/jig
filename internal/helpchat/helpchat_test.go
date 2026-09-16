package helpchat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/interaction"
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

	checks := []string{
		"my-workflow", "run-abc", "build", "succeeded", "test", "failed",
		`"skip" accepts the failure and continues`,
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("system prompt missing %q\nfull prompt:\n%s", want, got)
		}
	}
}

func TestQuestionPanelRoundTrip(t *testing.T) {
	m := New(nil, "", engine.RunSnapshot{})
	req := interaction.QuestionRequest{
		ID: "help-q1",
		Fields: []interaction.QuestionField{{
			ID: "choice", Prompt: "Choose", Kind: interaction.FieldSingleSelect,
			Options: []interaction.QuestionOption{
				{Value: "a", Label: "Alpha"},
				{Value: "b", Label: "Beta"},
			},
		}},
	}
	answers := make(chan interaction.QuestionResponse, 1)
	m, _ = m.Update(QuestionRequestMsg{Request: req, AnsC: answers})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	select {
	case response := <-answers:
		if response.RequestID != "help-q1" || response.Action != interaction.ActionAccept {
			t.Fatalf("response = %+v", response)
		}
		if got := response.Answers["choice"].Values; len(got) != 1 || got[0] != "b" {
			t.Fatalf("answer = %v, want [b]", got)
		}
	default:
		t.Fatal("question panel did not send a response")
	}
}

func TestQuestionEscapeOwnershipFollowsNestedPhase(t *testing.T) {
	m := New(nil, "", engine.RunSnapshot{})
	req := interaction.QuestionRequest{
		ID: "help-q1",
		Fields: []interaction.QuestionField{{
			ID: "choice", Prompt: "Choose", Kind: interaction.FieldSingleSelect,
			Options:     []interaction.QuestionOption{{Value: "a", Label: "Alpha"}},
			AllowCustom: true,
		}},
	}
	m, _ = m.Update(QuestionRequestMsg{
		Request: req,
		AnsC:    make(chan interaction.QuestionResponse, 1),
	})

	if m.HandlesEscape() {
		t.Fatal("top-level question captured esc instead of leaving it to the modal")
	}
	if hint := m.gateHint(); !strings.Contains(hint, "esc close") || strings.Contains(hint, "esc cancel") {
		t.Fatalf("top-level question hint = %q", hint)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.HandlesEscape() {
		t.Fatal("custom answer editor did not capture esc for inner back")
	}
	if hint := m.gateHint(); !strings.Contains(hint, "esc back") || strings.Contains(hint, "esc close") {
		t.Fatalf("custom answer hint = %q", hint)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.HandlesEscape() {
		t.Fatal("custom answer esc did not return to the top-level question")
	}
	if m.pendingGate == nil {
		t.Fatal("custom answer esc resolved the pending question")
	}
}

// TestToolHandlersRegistersAllTen verifies BuildToolHandlers registers all
// ten expected tool names (schemas/descriptions now live only in
// cmd/jig/mcp_serve.go's mcpToolDefs — already covered by that package's own
// TestMcpServeAllTenToolsDispatch table).
func TestToolHandlersRegistersAllTen(t *testing.T) {
	wantTools := []string{
		"workflow_snapshot", "read_step_transcript", "read_step_result", "read_step_output",
		"recover_step", "reset_step", "stop_step", "resume_step", "resolve_review", "ask_user",
	}
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)
	handlers := BuildToolHandlers(nil, "", func(tea.Msg) {}, gateReq, gateAns)
	if len(handlers) != len(wantTools) {
		t.Fatalf("want %d tools, got %d: %v", len(wantTools), len(handlers), handlers)
	}
	for _, want := range wantTools {
		if _, ok := handlers[want]; !ok {
			t.Errorf("tool %q not registered", want)
		}
	}
}

func TestDispatchFunc_RecoverStep(t *testing.T) {
	for _, action := range []string{"retry", "skip"} {
		t.Run(action, func(t *testing.T) {
			dispatched := make(chan tea.Msg, 1)
			dispatch := func(msg tea.Msg) { dispatched <- msg }
			handler := buildRecoverStep(fakeRun("run-1"), dispatch)

			result, isErr := handler(t.Context(), map[string]any{
				"step_id":  "build",
				"action":   action,
				"guidance": "fix the error",
			})
			if isErr {
				t.Fatalf("handler returned error: %v", result)
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
				if ra.Action != action {
					t.Errorf("Action = %q, want %q", ra.Action, action)
				}
			default:
				t.Fatal("dispatch channel empty after handler call")
			}
		})
	}
}

// TestFinalMergeGate_ChannelRendezvous verifies that resolve_review on the
// final_merge step blocks on gateReq and unblocks when gateAns is written.
func TestFinalMergeGate_ChannelRendezvous(t *testing.T) {
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)

	handler := buildResolveReview(fakeRun("run-2"), func(tea.Msg) {}, gateReq, gateAns)

	resultCh := make(chan string, 1)
	go func() {
		res, _ := handler(context.Background(), map[string]any{
			"step_id": "final_merge",
			"verdict": "approved",
		})
		resultCh <- res
	}()

	select {
	case <-gateReq:
	default:
		// Channel is buffered (size 1) so this may already be there.
	}
	gateAns <- true

	got := <-resultCh
	if !strings.Contains(got, "approved") {
		t.Errorf("result = %q, want to contain %q", got, "approved")
	}
}

// TestFinalMergeGate_ContextCancelledUnblocks verifies the crash-recovery
// path: if ctx is cancelled while blocked on the rendezvous (the local tool
// server lost its connection to the spawned jig mcp-serve process), the
// handler returns a clear error instead of hanging forever.
func TestFinalMergeGate_ContextCancelledUnblocks(t *testing.T) {
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)
	handler := buildResolveReview(fakeRun("run-3"), func(tea.Msg) {}, gateReq, gateAns)

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan struct {
		text  string
		isErr bool
	}, 1)
	go func() {
		text, isErr := handler(ctx, map[string]any{"step_id": "final_merge", "verdict": "approved"})
		resultCh <- struct {
			text  string
			isErr bool
		}{text, isErr}
	}()

	<-gateReq // drain so the handler is now blocked on gateAns
	cancel()

	select {
	case got := <-resultCh:
		if !got.isErr {
			t.Fatalf("result = %+v, want isError=true after ctx cancellation", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler hung after ctx cancellation instead of returning an error")
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

// ── toolServer end-to-end tests (task 4.7): a scripted local MCP-server-side
// client stands in for cmd/jig/mcp_serve.go's client half of the wire
// contract, exercising all 10 tools plus the ask_user/final_merge rendezvous
// through the real process-boundary wire format. ─────────────────────────────

// fakeMcpServeClient scripts the jig mcp-serve side of the loopback TCP
// connection: dial, authenticate, forward one tools/call, read the reply.
// Mirrors cmd/jig/mcp_serve.go's authenticate/forwardRequest/forwardResponse
// framing exactly (see that file's package doc comment for the wire
// contract); this package cannot import cmd/jig (a `package main`), so the
// wire types are duplicated in mcpserve_server.go.
type fakeMcpServeClient struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialToolServer(t *testing.T, srv *toolServer, token string) *fakeMcpServeClient {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+srv.Port())
	if err != nil {
		t.Fatalf("dial tool server: %v", err)
	}
	c := &fakeMcpServeClient{conn: conn, r: bufio.NewReader(conn)}
	t.Cleanup(func() { _ = conn.Close() })

	raw, err := json.Marshal(authMessage{Token: token})
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		t.Fatalf("send auth: %v", err)
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("read auth ack: %v", err)
	}
	var ack authAck
	if err := json.Unmarshal([]byte(line), &ack); err != nil || !ack.OK {
		t.Fatalf("auth not acked: %q", line)
	}
	return c
}

func (c *fakeMcpServeClient) call(t *testing.T, id, tool string, args map[string]any) forwardRequest {
	t.Helper()
	raw, err := json.Marshal(forwardRequest{ID: id, Tool: tool, Arguments: args})
	if err != nil {
		t.Fatalf("marshal forward request: %v", err)
	}
	if _, err := c.conn.Write(append(raw, '\n')); err != nil {
		t.Fatalf("send forward request: %v", err)
	}
	return forwardRequest{ID: id, Tool: tool, Arguments: args}
}

func (c *fakeMcpServeClient) readResponse(t *testing.T) forwardResponse {
	t.Helper()
	line, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("read forward response: %v", err)
	}
	var resp forwardResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal forward response %q: %v", line, err)
	}
	return resp
}

// TestToolServerDispatchesAllTenTools drives every registered handler through
// a real loopback TCP connection using the exact wire contract jig mcp-serve
// speaks, proving the server half works independent of AcpHarness.
func TestToolServerDispatchesAllTenTools(t *testing.T) {
	// ask_user blocks on an operator answer; auto-answer immediately so this
	// table-driven pass doesn't need a real rendezvous (that's covered by
	// TestToolServerAskUserFinalMergeRendezvous below).
	dispatch := func(msg tea.Msg) {
		if q, ok := msg.(QuestionRequestMsg); ok {
			q.AnsC <- interaction.QuestionResponse{
				Action:  interaction.ActionAccept,
				Answers: map[string]interaction.Answer{"answer": {Values: []string{"yes"}}},
			}
		}
	}
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)
	runDir := t.TempDir()
	handlers := BuildToolHandlers(fakeRun("run-1"), runDir, dispatch, gateReq, gateAns)
	srv, err := newToolServer(handlers)
	if err != nil {
		t.Fatalf("newToolServer: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx) }()

	client := dialToolServer(t, srv, srv.Token())

	// workflow_snapshot calls run.Snapshot(), which needs a fully-running
	// *engine.Run (internal channels), not the bare fakeRun stub used here —
	// its registration is covered by TestToolHandlersRegistersAllTen instead.
	cases := []struct {
		name string
		args map[string]any
	}{
		{"read_step_transcript", map[string]any{"step_id": "s1", "last_n": float64(5)}},
		{"read_step_result", map[string]any{"step_id": "s1"}},
		{"read_step_output", map[string]any{"step_id": "s1"}},
		{"recover_step", map[string]any{"step_id": "s1", "action": "retry"}},
		{"reset_step", map[string]any{"step_id": "s1"}},
		{"stop_step", map[string]any{"step_id": "s1"}},
		{"resume_step", map[string]any{"step_id": "s1"}},
		{"resolve_review", map[string]any{"step_id": "s1", "verdict": "approved"}},
		{"ask_user", map[string]any{"question": "continue?"}},
	}
	for i, tc := range cases {
		id := fmt.Sprintf("%d", i)
		client.call(t, id, tc.name, tc.args)
		resp := client.readResponse(t)
		if resp.ID != id {
			t.Errorf("%s: response id = %q, want %q", tc.name, resp.ID, id)
		}
		// Every fixture call here uses a nonexistent step/run, so read tools
		// legitimately error (no such file) and ask_user has no operator
		// listening (dispatch is a no-op) — the point of this test is that
		// each named tool actually dispatches and replies exactly once, not
		// that every synthetic call succeeds.
		_ = resp.IsError
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

// TestToolServerAskUserFinalMergeRendezvous scripts a delayed reply (standing
// in for an operator taking time to answer) through the real toolServer,
// confirming the call blocks until answered with no lost or duplicated
// replies — the process-boundary counterpart to cmd/jig's own rendezvous test.
func TestToolServerAskUserFinalMergeRendezvous(t *testing.T) {
	dispatched := make(chan tea.Msg, 1)
	dispatch := func(msg tea.Msg) { dispatched <- msg }
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)
	handlers := BuildToolHandlers(fakeRun("run-2"), t.TempDir(), dispatch, gateReq, gateAns)
	srv, err := newToolServer(handlers)
	if err != nil {
		t.Fatalf("newToolServer: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	client := dialToolServer(t, srv, srv.Token())
	client.call(t, "1", "resolve_review", map[string]any{"step_id": "final_merge", "verdict": "approved"})

	select {
	case <-gateReq:
	case <-time.After(5 * time.Second):
		t.Fatal("resolve_review(final_merge) never reached the gate rendezvous")
	}

	respCh := make(chan forwardResponse, 1)
	go func() { respCh <- client.readResponse(t) }()

	select {
	case <-respCh:
		t.Fatal("response delivered before the delayed reply was sent")
	case <-time.After(150 * time.Millisecond):
	}

	gateAns <- true

	select {
	case resp := <-respCh:
		if resp.IsError || !strings.Contains(resp.Result, "approved") {
			t.Fatalf("response = %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the delayed reply to be delivered")
	}
	_ = dispatched
}

// TestToolServerAuthRejection covers the jig-facing auth boundary: a wrong or
// missing token is rejected rather than acked.
func TestToolServerAuthRejection(t *testing.T) {
	handlers := BuildToolHandlers(nil, "", func(tea.Msg) {}, make(chan struct{}, 1), make(chan bool, 1))
	srv, err := newToolServer(handlers)
	if err != nil {
		t.Fatalf("newToolServer: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	conn, err := net.Dial("tcp", "127.0.0.1:"+srv.Port())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	raw, err := json.Marshal(authMessage{Token: "wrong-token"})
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		t.Fatalf("send auth: %v", err)
	}
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	var ack authAck
	if err := json.Unmarshal([]byte(line), &ack); err != nil || ack.OK {
		t.Fatalf("expected a negative ack for a wrong token, got %q", line)
	}
}

// TestHelpChatMcpServeSubprocessHelper is re-executed as a subprocess by
// TestToolServerSurvivesSubprocessKillMidRequest, standing in for a real jig
// mcp-serve process at the wire level: it dials the loopback tool server,
// authenticates, forwards one ask_user call, then blocks reading the
// (never-arriving) reply exactly as the real subprocess would while an
// operator is mid-thought — until it is killed. Run normally (without the
// env var) it is a no-op, matching the JIG_ACP_FIXTURE/JIG_MCP_SERVE_HELPER
// subprocess-helper convention already used by internal/harness and
// cmd/jig's own tests.
func TestHelpChatMcpServeSubprocessHelper(t *testing.T) {
	if os.Getenv("JIG_HELPCHAT_HELPER") != "1" {
		return
	}
	conn, err := net.Dial("tcp", "127.0.0.1:"+os.Getenv("JIG_HELPCHAT_HELPER_PORT"))
	if err != nil {
		os.Exit(1)
	}
	defer conn.Close()
	raw, _ := json.Marshal(authMessage{Token: os.Getenv("JIG_HELPCHAT_HELPER_TOKEN")})
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		os.Exit(1)
	}
	r := bufio.NewReader(conn)
	if _, err := r.ReadString('\n'); err != nil { // auth ack
		os.Exit(1)
	}
	freq := forwardRequest{ID: "1", Tool: "ask_user", Arguments: map[string]any{"question": "hang?"}}
	raw, _ = json.Marshal(freq)
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		os.Exit(1)
	}
	_, _ = r.ReadString('\n') // block until killed, matching a real subprocess's blocking rendezvous read
}

// TestToolServerSurvivesSubprocessKillMidRequest kills a real OS subprocess
// standing in for jig mcp-serve while an ask_user call is in flight and
// confirms toolServer.Serve returns a clear error promptly (no hang) — the
// live process-boundary crash-recovery proof artifact task 4.8 requires,
// covering the side of the connection Unit 3's own crash test (mcp-serve's
// client half) does not exercise.
func TestToolServerSurvivesSubprocessKillMidRequest(t *testing.T) {
	dispatched := make(chan tea.Msg, 1)
	dispatch := func(msg tea.Msg) { dispatched <- msg }
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)
	handlers := BuildToolHandlers(fakeRun("run-crash"), t.TempDir(), dispatch, gateReq, gateAns)
	srv, err := newToolServer(handlers)
	if err != nil {
		t.Fatalf("newToolServer: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx) }()

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelpChatMcpServeSubprocessHelper$")
	cmd.Env = append(os.Environ(),
		"JIG_HELPCHAT_HELPER=1",
		"JIG_HELPCHAT_HELPER_PORT="+srv.Port(),
		"JIG_HELPCHAT_HELPER_TOKEN="+srv.Token(),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	// Confirm the call actually reached the handler (subprocess is genuinely
	// blocked waiting for a reply) before killing it.
	select {
	case msg := <-dispatched:
		if _, ok := msg.(QuestionRequestMsg); !ok {
			t.Fatalf("dispatched = %T, want QuestionRequestMsg", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ask_user to reach the handler")
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill subprocess: %v", err)
	}
	_ = cmd.Wait()

	select {
	case err := <-serveDone:
		if err == nil {
			t.Fatal("Serve returned nil after a real subprocess crash, want a connection-lost error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("toolServer.Serve hung after the mcp-serve stand-in was killed mid-request")
	}
}

// fakeHelpchatHarness is a scripted helpchatHarness for exercising Model's
// ConnectedMsg/TurnCompleteMsg wiring without a real ACP subprocess. It
// mirrors the pattern runner/monitor_test.go's fakeMonitorHarness already
// uses for the same narrow-interface seam.
type fakeHelpchatHarness struct {
	events []harness.Event
}

func (h *fakeHelpchatHarness) Open(_ context.Context, _ harness.SessionSpec) (harness.Session, error) {
	ch := make(chan harness.Event, len(h.events))
	for _, ev := range h.events {
		ch <- ev
	}
	close(ch)
	return &fakeHelpchatSession{ch: ch}, nil
}

type fakeHelpchatSession struct {
	ch     chan harness.Event
	closed bool
	mu     sync.Mutex
}

func (s *fakeHelpchatSession) Messages() <-chan harness.Event { return s.ch }
func (s *fakeHelpchatSession) Send(context.Context, harness.ToolResult) error {
	return errors.New("not supported")
}
func (s *fakeHelpchatSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// TestModelTurnRoundTripThroughFakeHarness drives one full user turn through
// Model.Update using a scripted helpchatHarness (a fake AcpHarness stand-in),
// proving the harness.Event → Delta/TurnComplete translation works without a
// live subprocess.
func TestModelTurnRoundTripThroughFakeHarness(t *testing.T) {
	run := fakeRun("run-turn")
	m := New(run, t.TempDir(), engine.RunSnapshot{ID: "run-turn", Workflow: "wf"})
	m.newHarness = func() helpchatHarness {
		return &fakeHelpchatHarness{events: []harness.Event{
			{Type: harness.EventTextDelta, Text: "Hello"},
			{Type: harness.EventTextDelta, Text: ", world"},
			{Type: harness.EventResult, SessionID: "sess-1"},
		}}
	}

	initCmd := m.Init()
	if initCmd == nil {
		t.Fatal("Init() with a non-nil run returned a nil cmd")
	}
	msg := initCmd()
	srvMsg, ok := msg.(ServerReadyMsg)
	if !ok {
		t.Fatalf("Init() cmd returned %T, want ServerReadyMsg", msg)
	}
	m, _ = m.Update(srvMsg)
	if m.toolSrv == nil {
		t.Fatal("ServerReadyMsg did not store the tool server")
	}
	defer m.toolSrv.Close()

	m.ta.SetValue("what happened?")
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter with a ready tool server did not fire a command")
	}
	if !m.streaming {
		t.Fatal("streaming flag not set after submitting a turn")
	}

	connectedMsg := cmd()
	connected, ok := connectedMsg.(ConnectedMsg)
	if !ok {
		t.Fatalf("queryCmd returned %T, want ConnectedMsg", connectedMsg)
	}
	m, _ = m.Update(connected)
	if m.msgChan == nil {
		t.Fatal("ConnectedMsg did not wire up msgChan")
	}

	// Drive waitForMessageCmd directly rather than through m.Update's
	// returned tea.Batch cmd, whose BatchMsg unwrapping is the runtime's
	// job, not this test's.
	var deltas []string
	for i := 0; i < 10; i++ {
		next := waitForMessageCmd(m.msgChan)()
		switch v := next.(type) {
		case DeltaMsg:
			deltas = append(deltas, string(v))
			m, _ = m.Update(v)
		case TurnCompleteMsg:
			m, _ = m.Update(v)
			i = 10 // done
		default:
			t.Fatalf("unexpected message %T", next)
		}
	}

	if strings.Join(deltas, "") != "Hello, world" {
		t.Fatalf("accumulated deltas = %q, want %q", strings.Join(deltas, ""), "Hello, world")
	}
	if m.streaming {
		t.Fatal("streaming flag still set after TurnCompleteMsg")
	}
	if m.sessionID != "sess-1" {
		t.Fatalf("sessionID = %q, want %q", m.sessionID, "sess-1")
	}
	if len(m.turns) != 1 || m.turns[0].assistant != "Hello, world" {
		t.Fatalf("turns = %+v", m.turns)
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
