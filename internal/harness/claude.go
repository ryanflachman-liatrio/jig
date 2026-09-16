package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/interaction"
	"jig/internal/toolcall"
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

// Capabilities advertises the optional semantics implemented by the direct
// Claude SDK path.
func (*ClaudeHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(
		CapPermissionCallback,
		CapUserQuestion,
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
		client:  client,
		sendCh:  sendCh,
		events:  make(chan Event, 16),
		tools:   make(map[string]*toolcall.Activity),
		cwd:     spec.Cwd,
		pending: make(map[string]pendingEditDiff),
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
	if spec.Question != nil {
		var seq atomic.Uint64
		tool := claudeQuestionTool(spec.Question, &seq)
		opts = append(opts, claudecode.WithSdkMcpServer(
			"jig",
			claudecode.CreateSDKMcpServer("jig", "1.0.0", tool),
		))
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

func claudeQuestionTool(ask QuestionFn, seq *atomic.Uint64) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"header":      map[string]any{"type": "string"},
						"question":    map[string]any{"type": "string"},
						"multiSelect": map[string]any{"type": "boolean"},
						"options": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"label":       map[string]any{"type": "string"},
									"description": map[string]any{"type": "string"},
								},
								"required": []any{"label"},
							},
						},
					},
					"required": []any{"question"},
				},
			},
		},
		"required": []any{"questions"},
	}
	return claudecode.NewTool(
		"AskUserQuestion",
		"Ask the user one or more questions and wait for their answer before continuing.",
		schema,
		func(ctx context.Context, input map[string]any) (*claudecode.McpToolResult, error) {
			req, prompts, err := parseClaudeQuestions(input, seq.Add(1))
			if err != nil {
				return claudeQuestionResult(err.Error(), true), nil
			}
			resp := ask(ctx, req)
			content, isError := encodeClaudeQuestionResponse(req, prompts, resp)
			return claudeQuestionResult(content, isError), nil
		},
	)
}

func parseClaudeQuestions(input map[string]any, id uint64) (interaction.QuestionRequest, []string, error) {
	raw, ok := input["questions"]
	if !ok {
		return interaction.QuestionRequest{}, nil, fmt.Errorf("AskUserQuestion: missing questions field")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return interaction.QuestionRequest{}, nil, fmt.Errorf("AskUserQuestion: marshal questions: %w", err)
	}
	var items []struct {
		Header      string `json:"header"`
		Question    string `json:"question"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return interaction.QuestionRequest{}, nil, fmt.Errorf("AskUserQuestion: unmarshal questions: %w", err)
	}
	req := interaction.QuestionRequest{
		ID:      fmt.Sprintf("claude-question-%d", id),
		Message: "Please answer the following questions.",
		Fields:  make([]interaction.QuestionField, len(items)),
	}
	prompts := make([]string, len(items))
	seenPrompts := make(map[string]struct{}, len(items))
	for i, item := range items {
		if _, exists := seenPrompts[item.Question]; exists {
			return interaction.QuestionRequest{}, nil, fmt.Errorf("AskUserQuestion: duplicate question %q", item.Question)
		}
		seenPrompts[item.Question] = struct{}{}
		prompts[i] = item.Question
		field := interaction.QuestionField{
			ID:          fmt.Sprintf("question_%d", i),
			Header:      item.Header,
			Prompt:      item.Question,
			Kind:        interaction.FieldText,
			AllowCustom: false,
		}
		if len(item.Options) > 0 {
			field.Kind = interaction.FieldSingleSelect
			if item.MultiSelect {
				field.Kind = interaction.FieldMultiSelect
			}
			field.AllowCustom = true
			field.Options = make([]interaction.QuestionOption, len(item.Options))
			for j, option := range item.Options {
				field.Options[j] = interaction.QuestionOption{
					Value:       option.Label,
					Label:       option.Label,
					Description: option.Description,
				}
			}
		}
		req.Fields[i] = field
	}
	if len(req.Fields) == 1 {
		req.Message = req.Fields[0].Prompt
	}
	if err := req.Validate(); err != nil {
		return interaction.QuestionRequest{}, nil, fmt.Errorf("AskUserQuestion: %w", err)
	}
	return req, prompts, nil
}

func encodeClaudeQuestionResponse(
	req interaction.QuestionRequest,
	prompts []string,
	resp interaction.QuestionResponse,
) (string, bool) {
	if err := resp.Validate(req); err != nil {
		return "AskUserQuestion: invalid user response: " + err.Error(), true
	}
	if resp.Action == interaction.ActionCancel {
		return "user cancelled the question", true
	}
	answers := make(map[string]string, len(resp.Answers))
	if resp.Action == interaction.ActionAccept {
		for i, field := range req.Fields {
			answer, ok := resp.Answers[field.ID]
			if !ok {
				continue
			}
			text := strings.TrimSpace(answer.Custom)
			if text == "" {
				text = strings.Join(answer.Values, ", ")
			}
			if text != "" {
				answers[prompts[i]] = text
			}
		}
	}
	data, err := json.Marshal(map[string]any{"answers": answers})
	if err != nil {
		return "AskUserQuestion: encode response: " + err.Error(), true
	}
	return string(data), false
}

func claudeQuestionResult(content string, isError bool) *claudecode.McpToolResult {
	return &claudecode.McpToolResult{
		IsError: isError,
		Content: []claudecode.McpContent{{Type: "text", Text: content}},
	}
}

// claudeSession adapts a claudecode.Client connection to harness.Session.
type claudeSession struct {
	client claudecode.Client
	sendCh chan claudecode.StreamMessage
	events chan Event
	tools  map[string]*toolcall.Activity

	// cwd resolves a tool's relative file_path against the session's working
	// directory when building an edit/write diff snapshot.
	cwd string
	// pending holds the pre-execution file snapshot for an in-flight
	// Edit/MultiEdit/Write call, keyed by tool_use_id, so the matching
	// tool_result can pair it with a post-execution read and attach a
	// toolcall.Diff (pump is single-goroutine, so no lock is needed).
	pending map[string]pendingEditDiff
}

// pendingEditDiff is the pre-execution snapshot captured when an
// Edit/MultiEdit/Write ToolUseBlock arrives, kept until the paired
// tool_result lets the session read the post-execution content.
type pendingEditDiff struct {
	path   string
	before *string
}

// editDiffTools lists the built-in tool names whose file_path argument
// identifies a file this harness can snapshot before and after execution to
// synthesize a toolcall.Diff for the transcript's diff renderer.
var editDiffTools = map[string]bool{
	"Edit":      true,
	"MultiEdit": true,
	"Write":     true,
}

// snapshotEditDiff reads the current content of an Edit/MultiEdit/Write
// call's target file before the SDK executes it. A missing file (Write
// creating a new one) yields a nil before-text, matching toolcall.Diff's
// file-creation convention; any other read failure or unrecognized tool
// returns ok=false so the transcript falls back to raw input/output.
func (s *claudeSession) snapshotEditDiff(name string, input map[string]any) (pendingEditDiff, bool) {
	if !editDiffTools[name] {
		return pendingEditDiff{}, false
	}
	path, _ := input["file_path"].(string)
	if path == "" {
		return pendingEditDiff{}, false
	}
	resolved := path
	if !filepath.IsAbs(resolved) && s.cwd != "" {
		resolved = filepath.Join(s.cwd, resolved)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return pendingEditDiff{path: path}, true
	}
	before := string(data)
	return pendingEditDiff{path: path, before: &before}, true
}

// buildEditDiff re-reads the pending snapshot's file after execution and
// pairs it with the pre-execution snapshot to produce a toolcall.Diff. It
// returns nil when the post-execution read fails, leaving the transcript to
// fall back to raw input/output for that exchange.
func (s *claudeSession) buildEditDiff(pd pendingEditDiff) *toolcall.Diff {
	resolved := pd.path
	if !filepath.IsAbs(resolved) && s.cwd != "" {
		resolved = filepath.Join(s.cwd, resolved)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil
	}
	return &toolcall.Diff{Path: pd.path, OldText: pd.before, NewText: string(data)}
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
					tool := &toolcall.Activity{ID: b.ToolUseID, Title: b.Name, Input: input}
					s.tools[b.ToolUseID] = tool.Clone()
					if pd, ok := s.snapshotEditDiff(b.Name, b.Input); ok {
						s.pending[b.ToolUseID] = pd
					}
					s.events <- Event{Type: EventToolUse, Tool: tool.Clone()}
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
					tool := s.tools[tr.ToolUseID].Clone()
					if tool == nil {
						tool = &toolcall.Activity{ID: tr.ToolUseID}
					}
					tool.Status = "completed"
					isError := tr.IsError != nil && *tr.IsError
					if isError {
						tool.Status = "failed"
					}
					tool.Content = []toolcall.Content{{Type: "text", Text: toolResultContent(tr.Content)}}
					if pd, ok := s.pending[tr.ToolUseID]; ok {
						delete(s.pending, tr.ToolUseID)
						if !isError {
							if diff := s.buildEditDiff(pd); diff != nil {
								tool.Content = append(tool.Content, toolcall.Content{Diff: diff})
							}
						}
					}
					s.events <- Event{Type: EventToolResult, Tool: tool, IsError: isError}
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
