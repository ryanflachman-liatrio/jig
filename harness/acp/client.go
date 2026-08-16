// Package acp is a standalone spike proving jig can drive Claude over the
// Agent Client Protocol (ACP) via Zed's npx adapter, independent of the root
// jig module (see ADR 0010, ADR 0011). It has no dependency on jig code.
package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"
)

// Decider decides whether a tool call should be allowed. It is invoked once
// per session/request_permission round-trip; the spike's security proof
// depends on this being a real decision, not an always-allow stub.
type Decider func(toolCall acpsdk.ToolCallUpdate) bool

// Event is one captured entry from the session/update notification stream,
// recorded in the order it was received.
type Event struct {
	Kind   string // "message", "thought", "tool_call", "tool_call_update", "plan", "user_message"
	Text   string
	ToolID string
	Title  string
	Status string
}

// Client implements acp.Client, capturing every session/update into an
// in-memory log and delegating permission decisions to Decide.
type Client struct {
	Decide Decider

	// OnUpdate, if set, is invoked synchronously with each Event as it is
	// captured — before SessionUpdate returns — so a caller can stream events
	// in real time instead of waiting for Events() after the turn ends (see
	// Conn.Prompt in conn.go, which streams into internal/harness/acp.go).
	OnUpdate func(Event)

	mu       sync.Mutex
	events   []Event
	requests []acpsdk.RequestPermissionRequest
}

var _ acpsdk.Client = (*Client)(nil)

// Events returns a copy of the captured session/update stream.
func (c *Client) Events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

// PermissionRequests returns every session/request_permission request seen.
func (c *Client) PermissionRequests() []acpsdk.RequestPermissionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]acpsdk.RequestPermissionRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

// RequestPermission implements acp.Client. It records the request, then asks
// Decide for a real allow/deny decision and replies with the matching option.
func (c *Client) RequestPermission(_ context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, params)
	c.mu.Unlock()

	allow := c.Decide != nil && c.Decide(params.ToolCall)

	var wantKinds []acpsdk.PermissionOptionKind
	if allow {
		wantKinds = []acpsdk.PermissionOptionKind{acpsdk.PermissionOptionKindAllowOnce, acpsdk.PermissionOptionKindAllowAlways}
	} else {
		wantKinds = []acpsdk.PermissionOptionKind{acpsdk.PermissionOptionKindRejectOnce, acpsdk.PermissionOptionKindRejectAlways}
	}
	for _, want := range wantKinds {
		for _, opt := range params.Options {
			if opt.Kind == want {
				return acpsdk.RequestPermissionResponse{
					Outcome: acpsdk.RequestPermissionOutcome{
						Selected: &acpsdk.RequestPermissionOutcomeSelected{OptionId: opt.OptionId},
					},
				}, nil
			}
		}
	}
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{}},
	}, nil
}

// SessionUpdate implements acp.Client, capturing the update as an Event.
func (c *Client) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	u := params.Update
	var ev Event
	switch {
	case u.AgentMessageChunk != nil:
		ev = Event{Kind: "message", Text: textOf(u.AgentMessageChunk.Content)}
	case u.AgentThoughtChunk != nil:
		ev = Event{Kind: "thought", Text: textOf(u.AgentThoughtChunk.Content)}
	case u.UserMessageChunk != nil:
		ev = Event{Kind: "user_message", Text: textOf(u.UserMessageChunk.Content)}
	case u.ToolCall != nil:
		ev = Event{Kind: "tool_call", ToolID: string(u.ToolCall.ToolCallId), Title: u.ToolCall.Title, Status: string(u.ToolCall.Status)}
	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		title := ""
		if u.ToolCallUpdate.Title != nil {
			title = *u.ToolCallUpdate.Title
		}
		ev = Event{Kind: "tool_call_update", ToolID: string(u.ToolCallUpdate.ToolCallId), Title: title, Status: status}
	case u.Plan != nil:
		ev = Event{Kind: "plan"}
	default:
		return nil
	}
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
	if c.OnUpdate != nil {
		c.OnUpdate(ev)
	}
	return nil
}

func textOf(cb acpsdk.ContentBlock) string {
	if cb.Text != nil {
		return cb.Text.Text
	}
	return ""
}

// ReadTextFile, WriteTextFile, and the terminal methods are unused by the
// spike (no fs/terminal client capabilities are advertised in Initialize),
// but the acp.Client interface requires an implementation.

func (c *Client) ReadTextFile(_ context.Context, params acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	if !filepath.IsAbs(params.Path) {
		return acpsdk.ReadTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	b, err := os.ReadFile(params.Path)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, err
	}
	return acpsdk.ReadTextFileResponse{Content: string(b)}, nil
}

func (c *Client) WriteTextFile(_ context.Context, params acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	if !filepath.IsAbs(params.Path) {
		return acpsdk.WriteTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	return acpsdk.WriteTextFileResponse{}, nil
}

func (c *Client) CreateTerminal(_ context.Context, _ acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, fmt.Errorf("terminal support not implemented in spike client")
}

func (c *Client) KillTerminal(_ context.Context, _ acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, fmt.Errorf("terminal support not implemented in spike client")
}

func (c *Client) TerminalOutput(_ context.Context, _ acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, fmt.Errorf("terminal support not implemented in spike client")
}

func (c *Client) ReleaseTerminal(_ context.Context, _ acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, fmt.Errorf("terminal support not implemented in spike client")
}

func (c *Client) WaitForTerminalExit(_ context.Context, _ acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, fmt.Errorf("terminal support not implemented in spike client")
}

// Result is the outcome of a single Run: the full captured event stream plus
// the permission requests the agent made during the turn.
type Result struct {
	InitProtocolVersion int
	SessionID           string
	Events              []Event
	PermissionRequests  []acpsdk.RequestPermissionRequest
	StopReason          acpsdk.StopReason
}

// Run spawns `npx -y @zed-industries/claude-code-acp@latest`, drives
// Initialize -> NewSession -> Prompt for a single turn, and returns the
// captured event stream. It fails fast (rather than hanging) if npx or the
// adapter package is unavailable. It is a thin, single-shot wrapper over
// Connect/NewSession/Prompt (conn.go) — callers that need to stream events as
// they arrive (rather than in one batch after the turn ends) use those
// directly.
func Run(ctx context.Context, cwd, prompt string, decide Decider) (*Result, error) {
	conn, err := Connect(ctx, decide, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	sessionID, err := conn.NewSession(ctx, cwd)
	if err != nil {
		return nil, err
	}

	stopReason, err := conn.Prompt(ctx, sessionID, prompt)
	if err != nil {
		return nil, err
	}

	return &Result{
		InitProtocolVersion: conn.ProtocolVersion,
		SessionID:           sessionID,
		Events:              conn.client.Events(),
		PermissionRequests:  conn.PermissionRequests(),
		StopReason:          stopReason,
	}, nil
}

// WriteLog renders a Result as a readable transcript, one line per captured
// event, for use as a CLI-run-log proof artifact.
func WriteLog(w io.Writer, r *Result) error {
	if _, err := fmt.Fprintf(w, "protocol version: %d\nsession: %s\n\n", r.InitProtocolVersion, r.SessionID); err != nil {
		return err
	}
	for _, ev := range r.Events {
		if _, err := fmt.Fprintf(w, "[%s] title=%q status=%q tool_id=%q text=%q\n", ev.Kind, ev.Title, ev.Status, ev.ToolID, ev.Text); err != nil {
			return err
		}
	}
	for i, req := range r.PermissionRequests {
		if _, err := fmt.Fprintf(w, "\npermission request %d: tool_call_id=%s options=%d\n", i, req.ToolCall.ToolCallId, len(req.Options)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nstop reason: %s\n", r.StopReason)
	return err
}
