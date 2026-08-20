package acp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

// Conn is a live connection to a spawned
// `npx -y @zed-industries/claude-code-acp@latest` process, split out of Run's
// Initialize/NewSession/Prompt sequence so a caller can drive a session and
// receive session/update events as they arrive rather than waiting for a
// whole turn to finish and reading them back afterward.
type Conn struct {
	cmd    *exec.Cmd
	rpc    *acpsdk.ClientSideConnection
	client *Client

	// ProtocolVersion is the version the adapter reported during Initialize.
	ProtocolVersion int
}

// Connect spawns the adapter and performs the ACP Initialize handshake,
// failing fast (rather than hanging) if npx or the adapter package is
// unavailable. decide and onUpdate are wired into the connection's Client
// exactly as Run does; onUpdate additionally fires synchronously as each
// event is captured, for callers that stream rather than batch.
func Connect(ctx context.Context, decide Decider, onUpdate func(Event), elicit Elicitor) (*Conn, error) {
	npxPath, err := exec.LookPath("npx")
	if err != nil {
		return nil, fmt.Errorf("npx not found on PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, npxPath, "-y", "@agentclientprotocol/claude-agent-acp@0.70.0")
	// Capture stderr rather than forwarding it to os.Stderr: npm/npx prints
	// deprecation warnings and progress lines that would corrupt a TUI's
	// alt-screen display. The buffer is included in the error message if
	// Initialize fails, preserving diagnostics without polluting the terminal.
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	// Put npx and all its children (including the Node.js adapter) in their own
	// process group so Close() can kill the entire tree with one signal. Without
	// this, Kill() only sends SIGKILL to the npx PID; Node.js inherits the
	// stderr pipe write-end and keeps it open, which wedges the copy goroutine
	// started by cmd.Stderr (a non-*os.File writer) and blocks cmd.Wait() forever.
	configureProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude-code-acp: %w", err)
	}

	client := &Client{Decide: decide, OnUpdate: onUpdate, Elicit: elicit}
	rpc := acpsdk.NewClientSideConnection(client, stdin, stdout)

	initResp, err := rpc.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion:    acpsdk.ProtocolVersionNumber,
		ClientCapabilities: clientCapabilities(elicit),
	})
	if err != nil {
		_ = killProcess(cmd)
		_ = cmd.Wait()
		if msg := strings.TrimSpace(stderrBuf.String()); msg != "" {
			return nil, fmt.Errorf("initialize: %w\nadapter output: %s", err, msg)
		}
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return &Conn{cmd: cmd, rpc: rpc, client: client, ProtocolVersion: int(initResp.ProtocolVersion)}, nil
}

func clientCapabilities(elicit Elicitor) acpsdk.ClientCapabilities {
	caps := acpsdk.ClientCapabilities{}
	if elicit != nil {
		caps.Elicitation = &acpsdk.ElicitationCapabilities{
			Form: &acpsdk.ElicitationFormCapabilities{},
		}
	}
	return caps
}

// NewSession creates a new ACP session rooted at cwd and returns its id.
func (c *Conn) NewSession(ctx context.Context, cwd string) (string, error) {
	resp, err := c.rpc.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: cwd, McpServers: []acpsdk.McpServer{}})
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	return string(resp.SessionId), nil
}

// Prompt sends one user turn and blocks until the agent finishes responding,
// returning the stop reason. session/update notifications are delivered to
// the onUpdate callback passed to Connect as they arrive on the connection's
// own read loop, concurrently with this call being in flight — Prompt
// blocking does not delay event delivery.
func (c *Conn) Prompt(ctx context.Context, sessionID, text string) (acpsdk.StopReason, error) {
	resp, err := c.rpc.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(sessionID),
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(text)},
	})
	if err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	return resp.StopReason, nil
}

// PermissionRequests returns every session/request_permission request seen
// on this connection so far.
func (c *Conn) PermissionRequests() []acpsdk.RequestPermissionRequest {
	return c.client.PermissionRequests()
}

// Close terminates the adapter subprocess and all its children.
func (c *Conn) Close() error {
	_ = killProcess(c.cmd)
	return c.cmd.Wait()
}

// ConnectCursor spawns `cursor-agent acp` and performs the ACP Initialize +
// Authenticate handshake. Cursor requires an explicit authenticate call (with
// methodId "cursor_login") before any session can be created; this is the only
// protocol-level difference from Connect (which spawns the Zed npx adapter and
// requires no auth step). If CURSOR_API_KEY is set in the environment Cursor
// treats itself as already authenticated, but calling Authenticate is still
// safe (it is a no-op when already authenticated).
func ConnectCursor(ctx context.Context, decide Decider, onUpdate func(Event)) (*Conn, error) {
	agentPath, err := exec.LookPath("cursor-agent")
	if err != nil {
		return nil, fmt.Errorf("cursor-agent not found on PATH (run: cursor-agent --version to verify install): %w", err)
	}

	cmd := exec.CommandContext(ctx, agentPath, "acp")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cursor-agent acp: %w", err)
	}

	client := &Client{Decide: decide, OnUpdate: onUpdate}
	rpc := acpsdk.NewClientSideConnection(client, stdin, stdout)

	initResp, err := rpc.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	if _, err := rpc.Authenticate(ctx, acpsdk.AuthenticateRequest{MethodId: "cursor_login"}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("cursor authenticate: %w (run: cursor-agent login)", err)
	}

	return &Conn{cmd: cmd, rpc: rpc, client: client, ProtocolVersion: int(initResp.ProtocolVersion)}, nil
}
