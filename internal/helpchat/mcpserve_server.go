package helpchat

// toolServer is the jig-facing counterpart to cmd/jig/mcp_serve.go's
// loopback TCP client: it binds a per-session loopback listener, generates a
// per-session auth token, and accepts the one connection from the spawned
// jig mcp-serve subprocess, dispatching its forwarded tools/call requests
// against the ToolHandler map built from live *engine.Run state.
//
// The wire types below duplicate cmd/jig/mcp_serve.go's unexported
// authMessage/authAck/forwardRequest/forwardResponse shapes (same JSON tags)
// rather than sharing them, since a `package main` command cannot be
// imported — this is the server half of the contract documented in that
// file's package doc comment.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
)

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

// toolServer binds one loopback listener for the lifetime of one help-chat
// session (matching AcpHarness's one-session-per-Open() convention for the
// mcp-serve subprocess it spawns each turn) and dispatches every forwarded
// tool call against handlers.
type toolServer struct {
	ln       net.Listener
	token    string
	handlers map[string]ToolHandler
}

// newToolServer binds a loopback TCP listener on an OS-assigned port and
// generates a random per-session auth token.
func newToolServer(handlers map[string]ToolHandler) (*toolServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind local tool server: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("generate auth token: %w", err)
	}
	return &toolServer{ln: ln, token: token, handlers: handlers}, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Port returns the OS-assigned loopback port as a string, for the
// JIG_MCP_PORT environment variable jig mcp-serve reads.
func (s *toolServer) Port() string {
	return strconv.Itoa(s.ln.Addr().(*net.TCPAddr).Port)
}

// Token returns this session's auth token, for the JIG_MCP_TOKEN
// environment variable jig mcp-serve reads.
func (s *toolServer) Token() string { return s.token }

// Close releases the listener. Safe to call once Serve has returned or
// concurrently with it (unblocks a pending Accept).
func (s *toolServer) Close() error { return s.ln.Close() }

// Serve accepts exactly one connection (the spawned jig mcp-serve process),
// authenticates it, and dispatches forwarded tool calls until the connection
// drops or parent is cancelled. It returns nil only when parent is cancelled
// (an expected, clean shutdown); any other return is an error worth
// surfacing to the operator (auth rejection, or the connection dropping
// mid-conversation — e.g. jig mcp-serve crashed).
//
// The ctx passed to each ToolHandler is cancelled when Serve returns for any
// reason, so a handler blocked on a rendezvous channel (ask_user,
// resolve_review's final_merge wait) unblocks instead of hanging forever if
// the subprocess dies mid-request.
func (s *toolServer) Serve(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	conn, err := s.acceptWithContext(ctx)
	if err != nil {
		if parent.Err() != nil {
			return nil
		}
		return fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()
	// A blocking net.Conn read does not observe ctx cancellation on its own;
	// force it to unblock (and let the loop below detect a clean shutdown)
	// when the caller cancels parent — e.g. when the help-chat session ends.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	r := bufio.NewReader(conn)
	if err := s.authenticate(conn, r); err != nil {
		return err
	}

	var writeMu sync.Mutex
	var wg sync.WaitGroup
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			wg.Add(1)
			go func(line string) {
				defer wg.Done()
				s.handleForward(ctx, conn, &writeMu, line)
			}(line)
		}
		if err != nil {
			// Cancel before waiting: a handler blocked in a rendezvous
			// (ask_user, resolve_review's final_merge wait) only unblocks by
			// observing ctx.Done(), so cancelling after wg.Wait() would
			// deadlock forever on a dropped/crashed connection.
			cancel()
			wg.Wait()
			if parent.Err() != nil {
				return nil
			}
			return fmt.Errorf("connection to jig mcp-serve lost: %w", err)
		}
	}
}

func (s *toolServer) acceptWithContext(ctx context.Context) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := s.ln.Accept()
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-ctx.Done():
		_ = s.ln.Close()
		<-ch
		return nil, ctx.Err()
	}
}

func (s *toolServer) authenticate(conn net.Conn, r *bufio.Reader) error {
	line, err := r.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read auth message: %w", err)
	}
	var auth authMessage
	ok := json.Unmarshal([]byte(line), &auth) == nil && auth.Token == s.token
	raw, marshalErr := json.Marshal(authAck{OK: ok})
	if marshalErr == nil {
		_, _ = conn.Write(append(raw, '\n'))
	}
	if !ok {
		return fmt.Errorf("jig mcp-serve: auth rejected")
	}
	return nil
}

func (s *toolServer) handleForward(ctx context.Context, conn net.Conn, writeMu *sync.Mutex, line string) {
	var req forwardRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return // malformed input carries no id to reply to
	}
	var resp forwardResponse
	resp.ID = req.ID
	handler, ok := s.handlers[req.Tool]
	if !ok {
		resp.IsError = true
		resp.Result = fmt.Sprintf("unknown tool %q", req.Tool)
	} else {
		resp.Result, resp.IsError = handler(ctx, req.Arguments)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	_, _ = conn.Write(append(raw, '\n'))
}
