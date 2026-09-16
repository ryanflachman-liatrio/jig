package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeJigServer stands in for the main jig process's TCP listener side (built
// by Unit 4, not this unit) so mcp_serve.go's client-side bridging can be
// exercised standalone, with no TUI or AcpHarness involvement, per this
// unit's proof artifacts.
type fakeJigServer struct {
	ln       net.Listener
	token    string
	requests chan forwardRequest

	mu   sync.Mutex
	conn net.Conn
}

func startFakeJigServer(t *testing.T, token string) *fakeJigServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fs := &fakeJigServer{ln: ln, token: token, requests: make(chan forwardRequest, 16)}
	go fs.acceptLoop(false)
	t.Cleanup(func() { _ = ln.Close() })
	return fs
}

// startRejectingFakeJigServer behaves identically but always closes the
// connection without an ack, standing in for the auth-token trust boundary
// (task 3.6's "reject connections that send no token or the wrong token").
func startRejectingFakeJigServer(t *testing.T) *fakeJigServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fs := &fakeJigServer{ln: ln, token: "expected-token-never-sent", requests: make(chan forwardRequest, 16)}
	go fs.acceptLoop(true)
	t.Cleanup(func() { _ = ln.Close() })
	return fs
}

func (fs *fakeJigServer) port() string {
	return fmt.Sprintf("%d", fs.ln.Addr().(*net.TCPAddr).Port)
}

func (fs *fakeJigServer) acceptLoop(alwaysReject bool) {
	conn, err := fs.ln.Accept()
	if err != nil {
		return
	}
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	var auth authMessage
	_ = json.Unmarshal([]byte(line), &auth)
	if alwaysReject || auth.Token != fs.token {
		_ = conn.Close()
		return
	}
	ackRaw, err := json.Marshal(authAck{OK: true})
	if err != nil {
		_ = conn.Close()
		return
	}
	if _, err := conn.Write(append(ackRaw, '\n')); err != nil {
		_ = conn.Close()
		return
	}
	fs.mu.Lock()
	fs.conn = conn
	fs.mu.Unlock()

	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			var freq forwardRequest
			if jsonErr := json.Unmarshal([]byte(line), &freq); jsonErr == nil {
				fs.requests <- freq
			}
		}
		if err != nil {
			return
		}
	}
}

func (fs *fakeJigServer) reply(t *testing.T, resp forwardResponse) {
	t.Helper()
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal forwardResponse: %v", err)
	}
	fs.mu.Lock()
	conn := fs.conn
	fs.mu.Unlock()
	if conn == nil {
		t.Fatal("fakeJigServer: no connection accepted yet")
	}
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write forwardResponse: %v", err)
	}
}

func (fs *fakeJigServer) closeConn() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.conn != nil {
		_ = fs.conn.Close()
	}
}

func (fs *fakeJigServer) waitRequest(t *testing.T) forwardRequest {
	t.Helper()
	select {
	case req := <-fs.requests:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for forwarded request")
		return forwardRequest{}
	}
}

// testMcpClient scripts the agent side of the stdio MCP session: it writes
// JSON-RPC request lines into the server's stdin and reads response lines
// from its stdout.
type testMcpClient struct {
	w      *io.PipeWriter
	r      *bufio.Reader
	nextID int
}

func newTestMcpClient(w *io.PipeWriter, r io.Reader) *testMcpClient {
	return &testMcpClient{w: w, r: bufio.NewReader(r)}
}

func (c *testMcpClient) call(t *testing.T, method string, params any) rpcResponse {
	t.Helper()
	c.nextID++
	id := c.nextID
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := c.w.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return c.readResponse(t)
}

func (c *testMcpClient) readResponse(t *testing.T) rpcResponse {
	t.Helper()
	line, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	return resp
}

// newTestServer wires an mcpServer to a fakeJigServer over a real loopback
// TCP dial (matching production: mcp-serve dials out, the main jig process
// listens) and to in-process pipes standing in for stdin/stdout.
func newTestServer(t *testing.T, fs *fakeJigServer, token string) (*testMcpClient, func()) {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+fs.port())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	srv := newMcpServer(stdinR, stdoutW, conn)
	if err := srv.authenticate(token); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(ctx)
	}()
	cleanup := func() {
		_ = stdinW.Close()
		<-done
		cancel()
	}
	return newTestMcpClient(stdinW, stdoutR), cleanup
}

func decodeToolCallResult(t *testing.T, resp rpcResponse) toolCallResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var result toolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal toolCallResult: %v", err)
	}
	return result
}

// TestMcpServeAllTenToolsDispatch drives every registered tool through one
// mcpServer instance against a scripted TCP counterparty, demonstrating the
// server works independent of helpchat/AcpHarness (this unit's first proof
// artifact).
func TestMcpServeAllTenToolsDispatch(t *testing.T) {
	fs := startFakeJigServer(t, "spike-token")
	client, cleanup := newTestServer(t, fs, "spike-token")
	defer cleanup()

	initResp := client.call(t, "initialize", map[string]any{})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}

	listResp := client.call(t, "tools/list", map[string]any{})
	if listResp.Error != nil {
		t.Fatalf("tools/list error: %+v", listResp.Error)
	}
	raw, err := json.Marshal(listResp.Result)
	if err != nil {
		t.Fatalf("re-marshal tools/list result: %v", err)
	}
	var listed struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	if len(listed.Tools) != 10 {
		t.Fatalf("expected 10 tools, got %d: %+v", len(listed.Tools), listed.Tools)
	}

	wantNames := []string{
		"workflow_snapshot", "read_step_transcript", "read_step_result", "read_step_output",
		"recover_step", "reset_step", "stop_step", "resume_step", "resolve_review", "ask_user",
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"workflow_snapshot", map[string]any{}},
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
	if len(cases) != len(wantNames) {
		t.Fatalf("test table drift: %d cases vs %d want names", len(cases), len(wantNames))
	}
	for i, name := range wantNames {
		if listed.Tools[i].Name != name {
			t.Fatalf("tools/list[%d].Name = %q, want %q", i, listed.Tools[i].Name, name)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			respCh := make(chan rpcResponse, 1)
			go func() {
				respCh <- client.call(t, "tools/call", map[string]any{"name": tc.name, "arguments": tc.args})
			}()
			forwarded := fs.waitRequest(t)
			if forwarded.Tool != tc.name {
				t.Fatalf("forwarded tool = %q, want %q", forwarded.Tool, tc.name)
			}
			fs.reply(t, forwardResponse{ID: forwarded.ID, Result: "ok:" + tc.name})
			resp := <-respCh
			result := decodeToolCallResult(t, resp)
			if result.IsError {
				t.Fatalf("unexpected isError: %+v", result)
			}
			if len(result.Content) != 1 || result.Content[0].Text != "ok:"+tc.name {
				t.Fatalf("content = %+v", result.Content)
			}
		})
	}
}

// TestMcpServeAskUserFinalMergeRendezvous scripts a delayed reply (standing
// in for an operator taking time to answer) and confirms the call blocks
// until answered, with exactly one reply delivered — matching today's
// in-process channel semantics for ask_user/final_merge.
func TestMcpServeAskUserFinalMergeRendezvous(t *testing.T) {
	fs := startFakeJigServer(t, "rendezvous-token")
	client, cleanup := newTestServer(t, fs, "rendezvous-token")
	defer cleanup()

	respCh := make(chan rpcResponse, 1)
	go func() {
		respCh <- client.call(t, "tools/call", map[string]any{
			"name":      "resolve_review",
			"arguments": map[string]any{"step_id": "final_merge", "verdict": "approved"},
		})
	}()

	forwarded := fs.waitRequest(t)
	if forwarded.Tool != "resolve_review" {
		t.Fatalf("forwarded tool = %q", forwarded.Tool)
	}

	select {
	case <-respCh:
		t.Fatal("response delivered before the delayed reply was sent")
	case <-time.After(150 * time.Millisecond):
	}

	fs.reply(t, forwardResponse{ID: forwarded.ID, Result: "final merge approved by operator"})

	var resp rpcResponse
	select {
	case resp = <-respCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the delayed reply to be delivered")
	}
	result := decodeToolCallResult(t, resp)
	if result.IsError || len(result.Content) != 1 || result.Content[0].Text != "final merge approved by operator" {
		t.Fatalf("result = %+v", result)
	}

	select {
	case req := <-fs.requests:
		t.Fatalf("unexpected duplicate forwarded request: %+v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestMcpServeAuthRejection covers task 3.6/the auth-boundary proof artifact:
// a wrong token, and an empty ("no token") send, are both rejected by
// closing the connection rather than acking.
func TestMcpServeAuthRejection(t *testing.T) {
	t.Run("wrong token", func(t *testing.T) {
		fs := startFakeJigServer(t, "correct-token")
		conn, err := net.Dial("tcp", "127.0.0.1:"+fs.port())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		srv := newMcpServer(strings.NewReader(""), io.Discard, conn)
		if err := srv.authenticate("wrong-token"); err == nil {
			t.Fatal("expected authenticate to fail on a wrong token")
		}
	})
	t.Run("no token", func(t *testing.T) {
		fs := startFakeJigServer(t, "correct-token")
		conn, err := net.Dial("tcp", "127.0.0.1:"+fs.port())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		srv := newMcpServer(strings.NewReader(""), io.Discard, conn)
		if err := srv.authenticate(""); err == nil {
			t.Fatal("expected authenticate to fail when sending no token")
		}
	})
	t.Run("counterparty always rejects", func(t *testing.T) {
		fs := startRejectingFakeJigServer(t)
		conn, err := net.Dial("tcp", "127.0.0.1:"+fs.port())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		srv := newMcpServer(strings.NewReader(""), io.Discard, conn)
		if err := srv.authenticate("does-not-matter"); err == nil {
			t.Fatal("expected authenticate to fail against a rejecting counterparty")
		}
	})
}

// TestMcpServeSubprocessHelper is not a real test: it is re-executed as a
// subprocess (via exec.Command(os.Args[0], "-test.run=...")) by
// TestMcpServeSubprocessKillMidRequest, matching the JIG_ACP_FIXTURE helper
// pattern internal/harness/security_integration_test.go already uses for
// subprocess-boundary tests. Run normally (without the env var), it is a
// no-op so it does not affect `go test`.
func TestMcpServeSubprocessHelper(t *testing.T) {
	if os.Getenv("JIG_MCP_SERVE_HELPER") != "1" {
		return
	}
	os.Exit(runMcpServe(nil))
}

// TestMcpServeSubprocessKillMidRequest kills the real jig mcp-serve process
// while a tools/call is in flight and confirms the caller (this test, acting
// as the scripted MCP client that spawned it) observes a clean, prompt
// stdout closure rather than a hang — the crash-handling proof artifact.
func TestMcpServeSubprocessKillMidRequest(t *testing.T) {
	fs := startFakeJigServer(t, "kill-token")

	cmd := exec.Command(os.Args[0], "-test.run=^TestMcpServeSubprocessHelper$")
	cmd.Env = append(os.Environ(),
		"JIG_MCP_SERVE_HELPER=1",
		"JIG_MCP_PORT="+fs.port(),
		"JIG_MCP_TOKEN=kill-token",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: map[string]any{
		"name":      "ask_user",
		"arguments": map[string]any{"question": "will this hang?"},
	}}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Confirm the call actually reached the fake jig server (subprocess is
	// genuinely blocked waiting for a reply) before killing it.
	fs.waitRequest(t)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill subprocess: %v", err)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(stdout)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("reading stdout after kill: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("caller hung reading stdout after jig mcp-serve was killed mid-request")
	}
}
