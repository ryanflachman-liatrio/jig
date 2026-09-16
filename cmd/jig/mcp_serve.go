package main

// jig mcp-serve is a hidden subcommand spawned by helpchat (see
// internal/helpchat) as an AcpHarness SessionSpec.MCPServers stdio entry. It
// speaks real MCP JSON-RPC 2.0 over its own stdin/stdout to the agent (the
// stdio transport is newline-delimited JSON, one message per line — not
// LSP-style Content-Length framing) and forwards each tools/call as a request
// over a loopback TCP connection to the main jig process, which holds the
// actual *engine.Run/tea.Msg state this subcommand does not have. See
// docs/specs/26-spec-acp-only-harness/26-spec-acp-only-harness.md Unit 3 for
// the design rationale, including the known, unresolved risk around whether
// agent binaries enforce their own MCP tool-call timeout (verified during
// Unit 3's task 3.1 spike: they do not, at least not below the multi-minute
// range this subcommand's blocking rendezvous tools need).
//
// Wire contract:
//   - Agent-facing (stdin/stdout): standard MCP JSON-RPC 2.0, one message per
//     line. Handles "initialize", "notifications/initialized", "tools/list",
//     and "tools/call"; anything else gets a JSON-RPC "method not found"
//     error.
//   - Jig-facing (loopback TCP, this process as the client): jig-owned,
//     newline-delimited JSON, not a public protocol. The first line this
//     process sends is {"token":"..."} (authMessage); the main jig process
//     replies {"ok":true} (authAck) or closes the connection on a bad token.
//     After that, each tools/call is forwarded as one line
//     {"id":"...","tool":"...","arguments":{...}} (forwardRequest) and
//     answered with one line {"id":"...","result":"...","is_error":bool}
//     (forwardResponse), correlated by id so concurrent in-flight calls (the
//     ask_user/final_merge rendezvous can block far longer than other calls)
//     do not need to be answered in send order.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ── agent-facing (MCP/JSON-RPC) wire types ────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// ── jig-facing (loopback TCP) wire types ──────────────────────────────────

type authMessage struct {
	Token string `json:"token"`
}

type authAck struct {
	OK bool `json:"ok"`
}

type forwardRequest struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

type forwardResponse struct {
	ID      string `json:"id"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// mcpToolDefs mirrors the ten tools internal/helpchat/tools.go registers
// today (name, description, and JSON Schema kept identical) so the agent
// sees no behavior change from the schema it saw when helpchat registered
// these in-process.
var mcpToolDefs = []mcpTool{
	{
		Name:        "workflow_snapshot",
		Description: "Return a JSON snapshot of all step IDs and their current statuses.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		Name:        "read_step_transcript",
		Description: "Read the last N transcript entries for a step (agent conversation, tool calls, results).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id": map[string]any{"type": "string", "description": "Step ID to read transcript for"},
				"last_n":  map[string]any{"type": "integer", "description": "Maximum number of entries to return (0 = all)"},
			},
			"required": []any{"step_id"},
		},
	},
	{
		Name:        "read_step_result",
		Description: "Read the result.json for a step (status, error, output path, cost).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id": map[string]any{"type": "string", "description": "Step ID to read result for"},
			},
			"required": []any{"step_id"},
		},
	},
	{
		Name:        "read_step_output",
		Description: "Read the step's primary output artifact file (the agent's text response or command output).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id": map[string]any{"type": "string", "description": "Step ID to read output artifact for"},
			},
			"required": []any{"step_id"},
		},
	},
	{
		Name:        "recover_step",
		Description: "Retry, resume, skip, or abort a step in awaiting_recovery state.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id":  map[string]any{"type": "string", "description": "Step ID to recover"},
				"action":   map[string]any{"type": "string", "enum": []any{"retry", "resume", "skip", "abort"}, "description": "Recovery action"},
				"guidance": map[string]any{"type": "string", "description": "Optional guidance text for the resumed agent"},
			},
			"required": []any{"step_id", "action"},
		},
	},
	{
		Name:        "reset_step",
		Description: "Reset a step and all its dependent steps back to pending. Destructive — confirm with the operator before calling.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id": map[string]any{"type": "string", "description": "Step ID to reset"},
			},
			"required": []any{"step_id"},
		},
	},
	{
		Name:        "stop_step",
		Description: "Stop a currently running step (parks it at stopped status).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id": map[string]any{"type": "string", "description": "Step ID to stop"},
			},
			"required": []any{"step_id"},
		},
	},
	{
		Name:        "resume_step",
		Description: "Resume a stopped step.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id": map[string]any{"type": "string", "description": "Step ID to resume"},
				"message": map[string]any{"type": "string", "description": "Optional message to pass to the resumed agent"},
			},
			"required": []any{"step_id"},
		},
	},
	{
		Name:        "resolve_review",
		Description: "Resolve a review step with approved or rejected verdict. For the final merge gate, use step_id=\"final_merge\".",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"step_id": map[string]any{"type": "string", "description": "Step ID to resolve, or \"final_merge\" for the final merge gate"},
				"verdict": map[string]any{"type": "string", "enum": []any{"approved", "rejected"}, "description": "Review verdict"},
			},
			"required": []any{"step_id", "verdict"},
		},
	},
	{
		Name: "ask_user",
		Description: "Present a question to the operator and wait for their answer. " +
			"Provide options[] for a multiple-choice prompt; omit for free-text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The question to present to the operator."},
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional list of choices. Omit for a free-text answer.",
				},
			},
			"required": []any{"question"},
		},
	},
}

// mcpServer bridges one MCP stdio session to one authenticated TCP
// connection to the main jig process. It owns no application state itself —
// every tool call is a pure forward/wait/translate.
type mcpServer struct {
	in  *bufio.Reader
	out io.Writer

	outMu sync.Mutex

	conn        net.Conn
	connR       *bufio.Reader
	connWriteMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan forwardResponse

	nextID atomic.Uint64
}

func newMcpServer(in io.Reader, out io.Writer, conn net.Conn) *mcpServer {
	return &mcpServer{
		in:      bufio.NewReader(in),
		out:     out,
		conn:    conn,
		connR:   bufio.NewReader(conn),
		pending: make(map[string]chan forwardResponse),
	}
}

// authenticate sends this session's token as the connection's first message
// and waits for an ack. A closed connection or a missing/negative ack means
// the main jig process rejected the token (or never received one) — a fatal
// startup condition, not a per-call error.
func (s *mcpServer) authenticate(token string) error {
	raw, err := json.Marshal(authMessage{Token: token})
	if err != nil {
		return fmt.Errorf("encode auth message: %w", err)
	}
	if _, err := s.conn.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("send token: %w", err)
	}
	_ = s.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := s.connR.ReadString('\n')
	_ = s.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("no ack (connection rejected or closed): %w", err)
	}
	var ack authAck
	if err := json.Unmarshal([]byte(line), &ack); err != nil || !ack.OK {
		return fmt.Errorf("auth rejected")
	}
	return nil
}

// Run reads MCP requests from stdin until it closes (the agent ended the
// session — a clean shutdown, not an error) or ctx is cancelled, dispatching
// each on its own goroutine so a slow tools/call (the ask_user/final_merge
// rendezvous) never blocks unrelated requests. A dedicated goroutine drains
// the TCP connection for the lifetime of Run; if that connection drops, every
// still-pending tools/call fails with a clear error instead of hanging.
func (s *mcpServer) Run(ctx context.Context) {
	tcpDone := make(chan struct{})
	go func() {
		defer close(tcpDone)
		s.drainTCP()
	}()

	var wg sync.WaitGroup
	for {
		line, err := s.in.ReadString('\n')
		if len(line) > 0 {
			wg.Add(1)
			go func(line string) {
				defer wg.Done()
				s.handleLine(ctx, line)
			}(line)
		}
		if err != nil {
			break
		}
	}
	wg.Wait()
	_ = s.conn.Close()
	<-tcpDone
}

func (s *mcpServer) drainTCP() {
	for {
		line, err := s.connR.ReadString('\n')
		if len(line) > 0 {
			var resp forwardResponse
			if jsonErr := json.Unmarshal([]byte(line), &resp); jsonErr == nil {
				s.deliver(resp)
			}
		}
		if err != nil {
			s.failAllPending(err)
			return
		}
	}
}

func (s *mcpServer) deliver(resp forwardResponse) {
	s.pendingMu.Lock()
	ch, ok := s.pending[resp.ID]
	if ok {
		delete(s.pending, resp.ID)
	}
	s.pendingMu.Unlock()
	if ok {
		ch <- resp
	}
}

func (s *mcpServer) failAllPending(err error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, ch := range s.pending {
		ch <- forwardResponse{
			ID:      id,
			Result:  fmt.Sprintf("jig mcp-serve: connection to jig lost: %v", err),
			IsError: true,
		}
		delete(s.pending, id)
	}
}

func (s *mcpServer) handleLine(ctx context.Context, line string) {
	var req rpcRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return // malformed input carries no id to reply to
	}
	if req.ID == nil {
		return // a notification (e.g. notifications/initialized): no response
	}
	switch req.Method {
	case "initialize":
		s.writeResult(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "jig-help", "version": "1.0.0"},
		})
	case "tools/list":
		s.writeResult(req.ID, map[string]any{"tools": mcpToolDefs})
	case "tools/call":
		s.handleToolCall(ctx, req)
	default:
		s.writeError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *mcpServer) handleToolCall(ctx context.Context, req rpcRequest) {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, -32602, fmt.Sprintf("invalid params: %v", err))
		return
	}

	id := fmt.Sprintf("%d", s.nextID.Add(1))
	ch := make(chan forwardResponse, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	raw, err := json.Marshal(forwardRequest{ID: id, Tool: params.Name, Arguments: params.Arguments})
	if err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		s.writeError(req.ID, -32603, fmt.Sprintf("encode forward request: %v", err))
		return
	}

	s.connWriteMu.Lock()
	_, writeErr := s.conn.Write(append(raw, '\n'))
	s.connWriteMu.Unlock()
	if writeErr != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		s.writeResult(req.ID, toolCallResult{
			IsError: true,
			Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("jig mcp-serve: forward failed: %v", writeErr)}},
		})
		return
	}

	// No timeout here by design: ask_user/final_merge can legitimately block
	// on a human for minutes, and Unit 3's task 3.1 spike found no agent-side
	// enforcement in that range. A dropped TCP connection still resolves this
	// select via failAllPending closing over ch through deliver's channel
	// send, and ctx cancellation (session teardown) resolves it explicitly.
	select {
	case resp := <-ch:
		s.writeResult(req.ID, toolCallResult{
			IsError: resp.IsError,
			Content: []mcpContent{{Type: "text", Text: resp.Result}},
		})
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		s.writeResult(req.ID, toolCallResult{
			IsError: true,
			Content: []mcpContent{{Type: "text", Text: "jig mcp-serve: session cancelled"}},
		})
	}
}

func (s *mcpServer) writeResult(id json.RawMessage, result any) {
	s.writeResponse(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *mcpServer) writeError(id json.RawMessage, code int, message string) {
	s.writeResponse(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (s *mcpServer) writeResponse(resp rpcResponse) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return // a marshal failure here has no sensible recovery; drop silently
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = s.out.Write(raw)
	_, _ = s.out.Write([]byte("\n"))
}

// runMcpServe is cmd/jig/main.go's entry point for the hidden "mcp-serve"
// subcommand. It dials the loopback TCP port and authenticates with the
// token both supplied via environment variables (populated by helpchat
// through McpServerStdio.Env when it spawns this subcommand), then bridges
// stdin/stdout to that connection until the agent ends the session.
func runMcpServe(args []string) int {
	port := os.Getenv("JIG_MCP_PORT")
	token := os.Getenv("JIG_MCP_TOKEN")
	if port == "" || token == "" {
		fmt.Fprintln(os.Stderr, "jig mcp-serve: JIG_MCP_PORT and JIG_MCP_TOKEN must be set")
		return 2
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jig mcp-serve: dial: %v\n", err)
		return 1
	}
	defer conn.Close()

	srv := newMcpServer(os.Stdin, os.Stdout, conn)
	if err := srv.authenticate(token); err != nil {
		fmt.Fprintf(os.Stderr, "jig mcp-serve: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.Run(ctx)
	return 0
}
