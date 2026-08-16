package harness

import (
	"context"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
)

// AcpHarness drives Claude over the Agent Client Protocol via Zed's npx
// adapter, reusing harness/acp's proven Connect/NewSession/Prompt connection
// code (see ADR 0010, ADR 0011, and Unit 1's spike) instead of
// re-implementing the handshake. This file and the harness/acp module it
// imports are the only places acp-go-sdk is imported (dependency
// confinement — see the spec's success metric 5).
type AcpHarness struct{}

// NewAcpHarness returns an AcpHarness ready to use.
func NewAcpHarness() *AcpHarness { return &AcpHarness{} }

func (*AcpHarness) Name() string { return "acp" }

// Capabilities advertises only what this harness actually implements: the
// real permission-callback round-trip. In-process MCP, session resume,
// structured output, and partial streaming are not implemented, so they are
// omitted rather than stubbed true — a step needing one of those fails
// closed via AgentExecutor's capability gate instead of silently degrading.
func (*AcpHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapPermissionCallback)
}

// Open spawns the adapter, opens a session at spec.Cwd, and starts the
// prompt turn in the background so Messages() can begin delivering events
// immediately. It rejects any capability-gated SessionSpec field this
// harness does not advertise, rather than silently ignoring it.
func (h *AcpHarness) Open(ctx context.Context, spec SessionSpec) (Session, error) {
	if len(spec.MCPServers) > 0 {
		return nil, fmt.Errorf("acp: in-process MCP servers not supported (CapInProcessMCP not advertised)")
	}
	if spec.Resume != "" {
		return nil, fmt.Errorf("acp: session resume not supported (CapSessionResume not advertised)")
	}
	if spec.Schema != nil {
		return nil, fmt.Errorf("acp: structured output not supported (CapStructuredOutput not advertised)")
	}

	events := make(chan Event, 16)
	sess := &acpSession{events: events}

	var decide acp.Decider
	if spec.Permission != nil {
		decide = func(tc acpsdk.ToolCallUpdate) bool {
			return spec.Permission(toolCallName(tc), toolCallInput(tc)).Allow
		}
	}

	conn, err := acp.Connect(ctx, decide, func(ev acp.Event) {
		for _, e := range translateEvent(ev) {
			events <- e
		}
	})
	if err != nil {
		return nil, fmt.Errorf("acp: %w", err)
	}
	sess.conn = conn

	sessionID, err := conn.NewSession(ctx, spec.Cwd)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acp: %w", err)
	}
	events <- Event{Type: EventSessionID, SessionID: sessionID}

	go sess.run(ctx, sessionID, spec.Prompt)
	return sess, nil
}

// acpSession adapts an acp.Conn's single-turn Prompt call to harness.Session:
// run streams translated events as they arrive and closes events with a
// terminal EventResult once the turn completes.
type acpSession struct {
	conn   *acp.Conn
	events chan Event
}

func (s *acpSession) Messages() <-chan Event { return s.events }

// Send is a no-op: ACP's Prompt already blocks for the whole turn (including
// any permission round-trips), so there is no mid-session injection point
// analogous to ClaudeHarness's AskUserQuestion tool-result channel.
func (s *acpSession) Send(_ context.Context, _ ToolResult) error {
	return fmt.Errorf("acp: mid-session Send not supported")
}

func (s *acpSession) Close() error {
	return s.conn.Close()
}

func (s *acpSession) run(ctx context.Context, sessionID, prompt string) {
	defer close(s.events)
	stopReason, err := s.conn.Prompt(ctx, sessionID, prompt)
	if err != nil {
		s.events <- Event{Type: EventResult, IsError: true, ErrText: err.Error(), SessionID: sessionID}
		return
	}
	s.events <- Event{Type: EventResult, SessionID: sessionID, Subtype: string(stopReason)}
}

// translateEvent maps one harness/acp.Event to zero or more harness.Events.
// Each acp.Event is flushed as its own single-block transcript entry
// (assistant-end/user-end immediately following) rather than buffered across
// multiple session/update notifications: ACP's message/thought chunks and
// tool-call/tool-call-update pairs do not carry the same "one block per SDK
// message" grouping Claude's message stream does, so grouping them would
// require guessing turn boundaries the protocol does not expose.
func translateEvent(ev acp.Event) []Event {
	switch ev.Kind {
	case acp.EventMessage:
		return []Event{{Type: EventText, Text: ev.Text}, {Type: EventAssistantEnd}}
	case acp.EventThought:
		return []Event{{Type: EventThinking, Text: ev.Text}, {Type: EventAssistantEnd}}
	case acp.EventToolCall:
		return []Event{
			{Type: EventToolUse, ToolUseID: ev.ToolID, Name: ev.Title},
			{Type: EventAssistantEnd},
		}
	case acp.EventToolCallUpdate:
		return []Event{
			{
				Type:      EventToolResult,
				ToolUseID: ev.ToolID,
				Content:   ev.Status,
				IsError:   ev.Status == "failed",
			},
			{Type: EventUserEnd},
		}
	default:
		return nil
	}
}

// toolCallName returns the human-readable tool name a permission decision is
// keyed on. ACP's ToolCallUpdate carries only a Title (no separate machine
// tool name), so Title stands in for both.
func toolCallName(tc acpsdk.ToolCallUpdate) string {
	if tc.Title != nil {
		return *tc.Title
	}
	return ""
}

// toolCallInput extracts the tool call's raw input as a map, matching
// PermissionFn's signature. A non-object RawInput (or none at all) yields an
// empty map rather than an error — the permission decision still runs, just
// without argument detail to inspect.
func toolCallInput(tc acpsdk.ToolCallUpdate) map[string]any {
	if m, ok := tc.RawInput.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

var (
	_ Harness = (*AcpHarness)(nil)
	_ Session = (*acpSession)(nil)
)
