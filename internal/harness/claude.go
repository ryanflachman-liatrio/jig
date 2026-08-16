package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// ClaudeHarness wraps github.com/severity1/claude-agent-sdk-go, translating
// SessionSpec into the SDK's functional options and lifecycle
// (NewClient/Connect/QueryStream/ReceiveMessages/Disconnect). It is the
// behavior-preserving extraction of the direct-SDK path agent.go used to run
// inline: same options, same permission/MCP/resume/structured-output
// handling, same transcript shape once translated back by AgentExecutor.
type ClaudeHarness struct{}

// NewClaudeHarness returns a ClaudeHarness ready to use.
func NewClaudeHarness() *ClaudeHarness { return &ClaudeHarness{} }

func (*ClaudeHarness) Name() string { return "claude" }

// Capabilities advertises all five capabilities: the Claude SDK path
// implements the permission callback, in-process MCP, resume, structured
// output, and partial streaming exactly as it did before extraction.
func (*ClaudeHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(
		CapPermissionCallback,
		CapInProcessMCP,
		CapSessionResume,
		CapStructuredOutput,
		CapPartialStreaming,
	)
}

// Open translates spec into SDK options, connects, and starts the query.
// Message capture happens in a background goroutine (claudeSession.pump) so
// Messages() can start delivering events immediately.
func (h *ClaudeHarness) Open(ctx context.Context, spec SessionSpec) (Session, error) {
	opts := claudeOptions(spec)

	client := claudecode.NewClient(opts...)
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("claude: connect: %w", err)
	}

	sendCh := make(chan claudecode.StreamMessage, 4)
	if err := client.QueryStream(ctx, sendCh); err != nil {
		close(sendCh)
		_ = client.Disconnect()
		return nil, fmt.Errorf("claude: query stream: %w", err)
	}

	if err := client.Query(ctx, spec.Prompt); err != nil {
		close(sendCh)
		_ = client.Disconnect()
		return nil, fmt.Errorf("claude: query: %w", err)
	}

	sess := &claudeSession{
		client: client,
		sendCh: sendCh,
		events: make(chan Event, 16),
	}
	go sess.pump(client.ReceiveMessages(ctx))
	return sess, nil
}

// claudeOptions translates a SessionSpec into SDK functional options. Ported
// from agent.go's buildOptions, with the guard/MCP/resume logic that used to
// live in Execute folded in so ClaudeHarness.Open is the single place that
// touches claudecode.Option.
func claudeOptions(spec SessionSpec) []claudecode.Option {
	var opts []claudecode.Option
	if spec.Partial {
		opts = append(opts, claudecode.WithIncludePartialMessages(true))
	}
	if spec.Model != "" {
		opts = append(opts, claudecode.WithModel(spec.Model))
	}
	if spec.FallbackModel != "" {
		opts = append(opts, claudecode.WithFallbackModel(spec.FallbackModel))
	}
	if spec.Effort != "" {
		opts = append(opts, claudecode.WithEffort(claudecode.EffortLevel(spec.Effort)))
	}
	if spec.MaxTurns > 0 {
		opts = append(opts, claudecode.WithMaxTurns(spec.MaxTurns))
	}
	if spec.MaxThinkingTokens > 0 {
		opts = append(opts, claudecode.WithMaxThinkingTokens(spec.MaxThinkingTokens))
	}
	if spec.MaxBudgetUSD > 0 {
		opts = append(opts, claudecode.WithMaxBudgetUSD(spec.MaxBudgetUSD))
	}
	if spec.PermissionMode != "" {
		opts = append(opts, claudecode.WithPermissionMode(claudecode.PermissionMode(spec.PermissionMode)))
	}
	if len(spec.AllowedTools) > 0 {
		// AskUserQuestion is not a Claude Code built-in; it is implemented as an
		// in-process MCP tool (registered below via MCPServers). Rewrite it to
		// the MCP-qualified name so the CLI recognises it.
		opts = append(opts, claudecode.WithAllowedTools(rewriteAskUserQuestion(spec.AllowedTools)...))
	}
	if len(spec.DisallowedTools) > 0 {
		opts = append(opts, claudecode.WithDisallowedTools(spec.DisallowedTools...))
	}
	if spec.Cwd != "" {
		opts = append(opts, claudecode.WithCwd(spec.Cwd))
	}
	if spec.Schema != nil {
		opts = append(opts, claudecode.WithJSONSchema(spec.Schema))
	}
	if spec.Resume != "" {
		opts = append(opts,
			claudecode.WithResume(spec.Resume),
			claudecode.WithContinueConversation(true),
		)
	}
	for _, srv := range spec.MCPServers {
		tools := make([]*claudecode.McpTool, 0, len(srv.Tools))
		for _, t := range srv.Tools {
			tools = append(tools, claudecode.NewTool(t.Name, t.Description, t.InputSchema, claudeToolHandler(t)))
		}
		opts = append(opts, claudecode.WithSdkMcpServer(srv.Name, claudecode.CreateSDKMcpServer(srv.Name, srv.Version, tools...)))
	}
	if spec.Permission != nil {
		// When a Tier-1 guard is active, force PermissionModeDefault so the SDK
		// invokes the callback below. acceptEdits auto-approves writes without
		// calling the callback (confirmed by SDK source — see seam probe),
		// unchanged by this extraction.
		opts = append(opts, claudecode.WithPermissionMode(claudecode.PermissionModeDefault))
		perm := spec.Permission
		opts = append(opts, claudecode.WithCanUseTool(func(
			_ context.Context,
			toolName string,
			input map[string]any,
			_ claudecode.ToolPermissionContext,
		) (claudecode.PermissionResult, error) {
			dec := perm(toolName, input)
			if dec.Allow {
				return claudecode.NewPermissionResultAllow(), nil
			}
			return claudecode.NewPermissionResultDeny(dec.Reason), nil
		}))
	}
	return opts
}

// rewriteAskUserQuestion replaces every "AskUserQuestion" entry in tools with
// "mcp__jig__AskUserQuestion", matching the in-process MCP server name jig
// registers it under.
func rewriteAskUserQuestion(tools []string) []string {
	out := make([]string, len(tools))
	copy(out, tools)
	for i, t := range out {
		if t == "AskUserQuestion" {
			out[i] = "mcp__jig__AskUserQuestion"
		}
	}
	return out
}

// claudeToolHandler adapts a harness.Tool's backend-agnostic handler to the
// SDK's McpToolHandler signature.
func claudeToolHandler(t Tool) claudecode.McpToolHandler {
	return func(ctx context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
		res, err := t.Handler(ctx, args)
		if err != nil {
			return &claudecode.McpToolResult{
				IsError: true,
				Content: []claudecode.McpContent{{Type: "text", Text: err.Error()}},
			}, nil
		}
		return &claudecode.McpToolResult{
			IsError: res.IsError,
			Content: []claudecode.McpContent{{Type: "text", Text: res.Content}},
		}, nil
	}
}

// claudeSession adapts a claudecode.Client connection to harness.Session.
type claudeSession struct {
	client claudecode.Client
	sendCh chan claudecode.StreamMessage
	events chan Event
}

func (s *claudeSession) Messages() <-chan Event { return s.events }

func (s *claudeSession) Send(ctx context.Context, result ToolResult) error {
	msg := claudecode.StreamMessage{
		Type: "user",
		Message: map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": result.ToolUseID,
				"content":     result.Content,
				"is_error":    result.IsError,
			}},
		},
	}
	select {
	case s.sendCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *claudeSession) Close() error {
	close(s.sendCh)
	return s.client.Disconnect()
}

// pump consumes the SDK message stream and translates each message into
// harness.Events, closing s.events once the stream ends (a ResultMessage, or
// the connection dropping). It is the extraction of agent.go's captureStream
// message-to-block logic, targeting harness.Event instead of writing the
// transcript directly — that move is now AgentExecutor's job, operating on
// whatever harness produced the events.
func (s *claudeSession) pump(msgChan <-chan claudecode.Message) {
	defer close(s.events)
	for m := range msgChan {
		switch msg := m.(type) {
		case *claudecode.SystemMessage:
			if id, ok := msg.Data["session_id"].(string); ok && id != "" {
				s.events <- Event{Type: EventSessionID, SessionID: id}
			}
		case *claudecode.AssistantMessage:
			for _, cb := range msg.Content {
				switch b := cb.(type) {
				case *claudecode.TextBlock:
					s.events <- Event{Type: EventText, Text: b.Text}
				case *claudecode.ThinkingBlock:
					s.events <- Event{Type: EventThinking, Text: b.Thinking}
				case *claudecode.ToolUseBlock:
					var input json.RawMessage
					if raw, err := json.Marshal(b.Input); err == nil {
						input = raw
					}
					s.events <- Event{Type: EventToolUse, ToolUseID: b.ToolUseID, Name: b.Name, Input: input}
				}
			}
			s.events <- Event{Type: EventAssistantEnd}
			if msg.HasError() {
				s.events <- Event{Type: EventSystemText, Text: fmt.Sprintf("assistant error: %s", msg.GetError())}
			}
		case *claudecode.UserMessage:
			if blocks, ok := msg.Content.([]claudecode.ContentBlock); ok {
				for _, cb := range blocks {
					tr, ok := cb.(*claudecode.ToolResultBlock)
					if !ok {
						continue
					}
					s.events <- Event{
						Type:      EventToolResult,
						ToolUseID: tr.ToolUseID,
						Content:   toolResultContent(tr.Content),
						IsError:   tr.IsError != nil && *tr.IsError,
					}
				}
			}
			s.events <- Event{Type: EventUserEnd}
		case *claudecode.StreamEvent:
			if msg.SessionID != "" {
				s.events <- Event{Type: EventSessionID, SessionID: msg.SessionID}
			}
			if delta, ok := agentTextDelta(msg); ok {
				s.events <- Event{Type: EventTextDelta, Text: delta}
			}
		case *claudecode.ResultMessage:
			if msg.IsError {
				s.events <- Event{
					Type:         EventResult,
					IsError:      true,
					ErrText:      subtypeErrText(msg),
					Subtype:      msg.Subtype,
					SessionID:    msg.SessionID,
					TotalCostUSD: msg.TotalCostUSD,
					Usage:        msg.Usage,
				}
				return
			}
			var structured json.RawMessage
			if msg.StructuredOutput != nil {
				if raw, err := json.Marshal(msg.StructuredOutput); err == nil {
					structured = raw
				}
			}
			s.events <- Event{
				Type:         EventResult,
				SessionID:    msg.SessionID,
				Subtype:      msg.Subtype,
				TotalCostUSD: msg.TotalCostUSD,
				Usage:        msg.Usage,
				Structured:   structured,
			}
			return
		}
	}
}

// toolResultContent renders a tool_result's content as a string: a string
// passes through; structured content is JSON-encoded (the transcript schema
// stores content as a string).
func toolResultContent(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	}
}

// subtypeErrText returns a descriptive error message for a failed
// ResultMessage. Policy-limit subtypes (error_max_turns, error_max_budget_usd)
// get a clear human-readable prefix so operators can distinguish them from API
// failures at a glance. All other subtypes fall through to resultErrorText.
func subtypeErrText(m *claudecode.ResultMessage) string {
	var prefix string
	switch m.Subtype {
	case "error_max_turns":
		prefix = "agent reached the maximum turn limit"
	case "error_max_budget_usd":
		prefix = "agent exceeded the maximum USD budget"
	default:
		return resultErrorText(m)
	}
	var parts []string
	if m.Result != nil && *m.Result != "" {
		parts = append(parts, *m.Result)
	}
	parts = append(parts, m.Errors...)
	if len(parts) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(parts, "; ")
}

// resultErrorText builds the failure message for an errored ResultMessage,
// combining the summary Result string with the more granular Errors list when
// present. Falls back to a generic message if the SDK supplied neither.
func resultErrorText(m *claudecode.ResultMessage) string {
	var parts []string
	if m.Result != nil && *m.Result != "" {
		parts = append(parts, *m.Result)
	}
	parts = append(parts, m.Errors...)
	if len(parts) == 0 {
		return "unknown agent error"
	}
	return strings.Join(parts, "; ")
}

// agentTextDelta extracts the text from a content_block_delta StreamEvent,
// returning ("", false) for non-text deltas (thinking, input_json, etc.).
func agentTextDelta(ev *claudecode.StreamEvent) (string, bool) {
	if ev.Event["type"] != claudecode.StreamEventTypeContentBlockDelta {
		return "", false
	}
	delta, ok := ev.Event["delta"].(map[string]any)
	if !ok || delta["type"] != "text_delta" {
		return "", false
	}
	text, ok := delta["text"].(string)
	return text, ok
}

var (
	_ Harness = (*ClaudeHarness)(nil)
	_ Session = (*claudeSession)(nil)
)
