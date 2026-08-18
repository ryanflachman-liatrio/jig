package harness

import (
	"context"
	"encoding/json"
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

// Capabilities advertises what this harness actually implements.
// CapStructuredOutput is supported via prompt injection: the JSON schema is
// appended to the prompt as instructions, and the accumulated assistant text
// is parsed as JSON after the turn completes. A single retry with a
// corrective prompt is attempted if the first response is not valid JSON.
func (*AcpHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapPermissionCallback, CapStructuredOutput)
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

	prompt := spec.Prompt
	hasSchema := spec.Schema != nil
	if hasSchema {
		// ACP has no native schema parameter; inject the JSON Schema into the
		// prompt so the model knows the required output shape. The accumulated
		// assistant text is parsed as JSON after the turn (see run).
		prompt = injectSchemaPrompt(prompt, spec.Schema)
	}

	events := make(chan Event, 32)
	sess := &acpSession{events: events, hasSchema: hasSchema, schema: spec.Schema}

	var decide acp.Decider
	if spec.Permission != nil {
		decide = func(tc acpsdk.ToolCallUpdate) bool {
			return spec.Permission(toolCallName(tc), toolCallInput(tc)).Allow
		}
	}

	conn, err := acp.Connect(ctx, decide, func(ev acp.Event) {
		sess.onEvent(ev)
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

	go sess.run(ctx, sessionID, prompt)
	return sess, nil
}

// acpSession adapts an acp.Conn's single-turn Prompt call to harness.Session.
// When hasSchema is true, run() parses the accumulated assistant text as JSON
// and retries once with a corrective prompt if the first response is invalid.
//
// Event translation is stateful: text chunks accumulate until a natural
// boundary (first new tool call or end of turn), at which point a single
// EventAssistantEnd is emitted — mirroring how ClaudeHarness groups all blocks
// in one AssistantMessage into one transcript entry. Tool calls are buffered by
// ID and emitted once via EventToolCallUpdate (which carries the final title),
// eliminating duplicate entries from the ACP adapter's streaming title updates.
//
// All fields written by onEvent and read by run() are safe from concurrent
// access: the ACP SDK invokes onUpdate synchronously, and all callbacks
// complete before Prompt returns — so run() reads these fields only after the
// last callback has fired.
type acpSession struct {
	conn      *acp.Conn
	events    chan Event
	hasSchema bool
	schema    map[string]any

	// lastText accumulates EventMessage chunks for structured-output extraction.
	// Reset when the first new tool call ID is seen so only the final text
	// response (after all tool results) is captured.
	lastText string

	// pendingTools maps tool_id → most-recently-seen title. The ACP adapter
	// streams tool call starts and title updates as separate EventToolCall
	// events; we buffer them here and emit a single EventToolUse only when the
	// corresponding EventToolCallUpdate (the result) arrives.
	pendingTools map[string]string

	// hasTextSinceFlush is true whenever text or thinking events have been
	// emitted since the last EventAssistantEnd. Used to decide whether to flush
	// before the first new tool call, and to flush any trailing text after
	// Prompt returns.
	hasTextSinceFlush bool
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

	s.lastText = ""
	stopReason, err := s.conn.Prompt(ctx, sessionID, prompt)
	if err != nil {
		s.events <- Event{Type: EventResult, IsError: true, ErrText: err.Error(), SessionID: sessionID}
		return
	}
	// Flush any trailing text from this turn into a single assistant entry.
	s.flushText()

	if s.hasSchema {
		parsed := extractJSON(s.lastText)
		if !json.Valid(parsed) {
			// First response is not valid JSON; retry once with a corrective prompt
			// that restates the schema requirement. The retry turn's events are
			// forwarded through the same callback, so the transcript captures both
			// attempts.
			retry := retrySchemaPrompt(s.schema)
			s.lastText = ""
			stopReason, err = s.conn.Prompt(ctx, sessionID, retry)
			if err != nil {
				s.events <- Event{Type: EventResult, IsError: true, ErrText: err.Error(), SessionID: sessionID}
				return
			}
			s.flushText()
			parsed = extractJSON(s.lastText)
			if !json.Valid(parsed) {
				s.events <- Event{
					Type:      EventResult,
					IsError:   true,
					ErrText:   "structured output: response is not valid JSON after retry",
					SessionID: sessionID,
				}
				return
			}
		}
		s.events <- Event{Type: EventResult, SessionID: sessionID, Subtype: string(stopReason), Structured: parsed}
		return
	}

	s.events <- Event{Type: EventResult, SessionID: sessionID, Subtype: string(stopReason)}
}

// flushText emits EventAssistantEnd if any text or thinking has been emitted
// since the last flush, grouping all preceding chunks into one transcript entry.
func (s *acpSession) flushText() {
	if s.hasTextSinceFlush {
		s.events <- Event{Type: EventAssistantEnd}
		s.hasTextSinceFlush = false
	}
}

// onEvent translates one ACP event into harness events with stateful grouping:
//
//   - Text and thinking chunks (EventMessage, EventThought) accumulate in
//     captureStream's buffer until flushed — no EventAssistantEnd per chunk.
//   - The first new tool call ID flushes any preceding text (one AssistantEnd),
//     then buffers the tool's title. Subsequent EventToolCall events for the
//     same ID only update the buffered title — no extra flush, no extra entry.
//   - EventToolCallUpdate (the tool result) emits the buffered EventToolUse
//     with the final title, then AssistantEnd, then the result and UserEnd.
//   - After Prompt returns, run() calls flushText() to close the final turn.
//
// This mirrors ClaudeHarness.pump, which groups all blocks within one SDK
// AssistantMessage into a single transcript entry via a single AssistantEnd.
func (s *acpSession) onEvent(ev acp.Event) {
	switch ev.Kind {
	case acp.EventMessage:
		if s.hasSchema {
			s.lastText += ev.Text
		}
		s.events <- Event{Type: EventText, Text: ev.Text}
		s.hasTextSinceFlush = true

	case acp.EventThought:
		s.events <- Event{Type: EventThinking, Text: ev.Text}
		s.hasTextSinceFlush = true

	case acp.EventToolCall:
		if s.pendingTools == nil {
			s.pendingTools = make(map[string]string)
		}
		_, existed := s.pendingTools[ev.ToolID]
		s.pendingTools[ev.ToolID] = ev.Title
		if !existed {
			// First time seeing this tool ID: flush any preceding text so it
			// lands in its own assistant entry, separate from the tool calls.
			s.flushText()
			if s.hasSchema {
				// Reset accumulator so only the final text response (after all
				// tool results) is captured for JSON extraction.
				s.lastText = ""
			}
		}
		// Subsequent EventToolCall events for the same ID are title updates;
		// they update pendingTools[id] and do nothing else. The EventToolUse
		// is emitted once, when EventToolCallUpdate arrives with the result.

	case acp.EventToolCallUpdate:
		title := ""
		if s.pendingTools != nil {
			title = s.pendingTools[ev.ToolID]
			delete(s.pendingTools, ev.ToolID)
		}
		s.events <- Event{Type: EventToolUse, ToolUseID: ev.ToolID, Name: title}
		s.events <- Event{Type: EventAssistantEnd}
		s.events <- Event{Type: EventToolResult, ToolUseID: ev.ToolID, Content: ev.Status, IsError: ev.Status == "failed"}
		s.events <- Event{Type: EventUserEnd}
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
