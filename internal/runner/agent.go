package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

// AgentExecutor runs agent steps via the Claude Agent SDK.
// Each Execute call opens a fresh connection so runs are independent and the
// executor itself holds no mutable state.
type AgentExecutor struct{}

// NewAgentExecutor returns an AgentExecutor ready to use.
func NewAgentExecutor() *AgentExecutor { return &AgentExecutor{} }

// Execute runs one agent step. It:
//  1. Builds the prompt from the step's skill/agent-file body and inputs.
//  2. Captures the complete message stream — text, reasoning, tool calls with
//     inputs, and tool results — to the per-step transcript, emitting liveness
//     signals via rep.Message and a live-typing tail via rep.Output.
//  3. Derives the output artifact (if step.Output is set) from the final
//     assistant text blocks — one capture path, no drift from the transcript.
//  4. Returns a Result that summarises the outcome.
func (e *AgentExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	start := time.Now()

	opts, err := buildOptions(req.Step)
	if err != nil {
		return failResult(fmt.Sprintf("build options: %v", err), start), nil
	}
	if req.Worktree != "" {
		opts = append(opts, claudecode.WithCwd(req.Worktree))
	}
	if req.ResumeSessionID != "" {
		opts = append(opts,
			claudecode.WithResume(req.ResumeSessionID),
			claudecode.WithContinueConversation(true),
		)
	}
	client := claudecode.NewClient(opts...)
	if err := client.Connect(ctx); err != nil {
		return failResult(fmt.Sprintf("agent connect: %v", err), start), nil
	}
	defer func() { _ = client.Disconnect() }()

	msgChan := client.ReceiveMessages(ctx)

	query := buildAgentPrompt(req)
	if req.ResumeSessionID != "" {
		query = req.Message
	}
	if err := client.Query(ctx, query); err != nil {
		return failResult(fmt.Sprintf("agent query: %v", err), start), nil
	}

	initialMsg := ""
	if req.ResumeSessionID != "" {
		initialMsg = req.Message
	}
	return captureStream(msgChan, req, rep, start, initialMsg)
}

// buildOptions translates a step's already-defaulted model/tool/permission
// fields (resolved from [defaults] by workflow.applyDefaults before this ever
// runs) into SDK options. Zero-value fields are simply omitted so the SDK's
// own defaults apply.
func buildOptions(st *workflow.Step) ([]claudecode.Option, error) {
	opts := []claudecode.Option{
		claudecode.WithIncludePartialMessages(true),
	}
	if st.Model != "" {
		opts = append(opts, claudecode.WithModel(st.Model))
	}
	if st.FallbackModel != "" {
		opts = append(opts, claudecode.WithFallbackModel(st.FallbackModel))
	}
	if st.Effort != "" {
		opts = append(opts, claudecode.WithEffort(claudecode.EffortLevel(st.Effort)))
	}
	if st.MaxTurns > 0 {
		opts = append(opts, claudecode.WithMaxTurns(st.MaxTurns))
	}
	if st.MaxThinkingTokens > 0 {
		opts = append(opts, claudecode.WithMaxThinkingTokens(st.MaxThinkingTokens))
	}
	if st.MaxBudgetUSD > 0 {
		opts = append(opts, claudecode.WithMaxBudgetUSD(st.MaxBudgetUSD))
	}
	if st.PermissionMode != "" {
		opts = append(opts, claudecode.WithPermissionMode(claudecode.PermissionMode(st.PermissionMode)))
	}
	if len(st.AllowedTools) > 0 {
		opts = append(opts, claudecode.WithAllowedTools(st.AllowedTools...))
	}
	if len(st.DisallowedTools) > 0 {
		opts = append(opts, claudecode.WithDisallowedTools(st.DisallowedTools...))
	}
	if st.Schema != nil {
		raw, err := st.Schema.JSONSchema()
		if err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		opts = append(opts, claudecode.WithJSONSchema(m))
	}
	return opts, nil
}

// captureStream consumes the SDK message stream, appending each message to the
// per-step transcript and emitting Phase 2 liveness signals, and returns the
// step Result. It is split out of Execute so tests can drive it with a scripted
// channel — no live SDK connection required (see agent_test.go).
//
// The transcript file is the durable source of truth; rep.Output deltas are an
// ephemeral preview of the not-yet-finalized assistant bubble and are never
// persisted. When req.TranscriptPath is empty (persistence off) no transcript
// is written and no rep.Message signals are emitted.
func captureStream(msgChan <-chan claudecode.Message, req engine.StepRequest, rep engine.Reporter, start time.Time, initialUserMsg string) (*step.Result, error) {
	var w *transcript.Writer
	if req.TranscriptPath != "" {
		var err error
		w, err = transcript.Create(req.TranscriptPath)
		if err != nil {
			return failResult(fmt.Sprintf("transcript open: %v", err), start), nil
		}
		defer func() { _ = w.Close() }()
	}

	// append writes one entry and nudges the TUI. Empty-block entries (e.g. a
	// user message that is only prompt text, no tool results) are skipped so the
	// transcript stays a record of substantive turns.
	appendEntry := func(role transcript.Role, blocks []transcript.Block) {
		if w == nil || len(blocks) == 0 {
			return
		}
		seq, err := w.Append(transcript.Entry{
			Iteration: req.Iteration,
			Attempt:   req.Attempt,
			Role:      role,
			Blocks:    blocks,
		})
		if err == nil {
			rep.Message(seq, req.Iteration)
		}
	}

	// If this is a resumed session, record the human message that triggered it
	// so the transcript shows the full exchange including the human's turn.
	if initialUserMsg != "" {
		appendEntry(transcript.RoleUser, []transcript.Block{{Type: transcript.BlockText, Text: initialUserMsg}})
	}

	// finalText holds the text of the most recent assistant message that carried
	// any — it becomes the output artifact (the agent's final answer). The
	// transcript, not this buffer, is the durable record.
	var finalText string

	for msg := range msgChan {
		switch m := msg.(type) {
		case *claudecode.AssistantMessage:
			blocks, text := assistantBlocks(m)
			if text != "" {
				finalText = text
			}
			appendEntry(transcript.RoleAssistant, blocks)
			if m.HasError() {
				appendEntry(transcript.RoleSystem, []transcript.Block{{
					Type: transcript.BlockText,
					Text: fmt.Sprintf("assistant error: %s", m.GetError()),
				}})
			}
		case *claudecode.UserMessage:
			appendEntry(transcript.RoleUser, toolResultBlocks(m))
		case *claudecode.StreamEvent:
			// Live-typing tail only; the finalized AssistantMessage above is
			// authoritative and is what lands in the transcript.
			if delta, ok := agentTextDelta(m); ok {
				rep.Output(delta)
			}
		case *claudecode.ResultMessage:
			if m.IsError {
				errStr := resultErrorText(m)
				appendEntry(transcript.RoleResult, []transcript.Block{{Type: transcript.BlockText, Text: errStr}})
				res := failResult(errStr, start)
				res.Subtype = m.Subtype
				return res, nil
			}
			result := &step.Result{
				Status:    step.StatusSucceeded,
				Duration:  time.Since(start),
				SessionID: m.SessionID,
				Subtype:   m.Subtype,
			}
			if m.StructuredOutput != nil {
				if raw, err := json.Marshal(m.StructuredOutput); err == nil {
					result.Structured = raw
				}
			}
			if req.Step.Output != "" && finalText != "" {
				outPath := req.Step.Output
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err == nil {
					_ = os.WriteFile(outPath, []byte(finalText), 0o644)
				}
				result.OutputPath = outPath
			}
			return result, nil
		}
	}

	// msgChan closed before ResultMessage — connection dropped.
	return failResult("agent connection closed unexpectedly", start), nil
}

// assistantBlocks maps an assistant message's content blocks to transcript
// blocks (thinking, text, tool_use) in their original order, and returns the
// concatenated text for artifact derivation. Unknown block types are skipped so
// an SDK addition never breaks capture.
func assistantBlocks(m *claudecode.AssistantMessage) ([]transcript.Block, string) {
	var blocks []transcript.Block
	var text strings.Builder
	for _, cb := range m.Content {
		switch b := cb.(type) {
		case *claudecode.TextBlock:
			blocks = append(blocks, transcript.Block{Type: transcript.BlockText, Text: b.Text})
			text.WriteString(b.Text)
		case *claudecode.ThinkingBlock:
			// Thinking may be empty or redacted; capture whatever is present.
			blocks = append(blocks, transcript.Block{Type: transcript.BlockThinking, Text: b.Thinking})
		case *claudecode.ToolUseBlock:
			// Input is the tool arguments; store the raw JSON. A marshal failure
			// is non-fatal — we still record the call name and correlation ID.
			var input json.RawMessage
			if raw, err := json.Marshal(b.Input); err == nil {
				input = raw
			}
			blocks = append(blocks, transcript.Block{
				Type:      transcript.BlockToolUse,
				ToolUseID: b.ToolUseID,
				Name:      b.Name,
				Input:     input,
			})
		}
	}
	return blocks, text.String()
}

// toolResultBlocks extracts tool_result blocks from a user message. Messages
// that are plain prompt text (Content is a string, e.g. the initial query
// echo) yield none, correlating results to calls by ToolUseID.
func toolResultBlocks(m *claudecode.UserMessage) []transcript.Block {
	blocks, ok := m.Content.([]claudecode.ContentBlock)
	if !ok {
		return nil
	}
	var out []transcript.Block
	for _, cb := range blocks {
		tr, ok := cb.(*claudecode.ToolResultBlock)
		if !ok {
			continue
		}
		out = append(out, transcript.Block{
			Type:      transcript.BlockToolResult,
			ToolUseID: tr.ToolUseID,
			Content:   toolResultContent(tr.Content),
			IsError:   tr.IsError != nil && *tr.IsError,
		})
	}
	return out
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

// buildAgentPrompt constructs the prompt sent to Claude. It concatenates:
//   - the agent-file body (if any) from the workflow step
//   - the append_system_prompt annotation (if any)
//   - the resolved inputs (file paths or inlined content)
//   - the feedback artifact (if the step is re-running inside a loop)
func buildAgentPrompt(req engine.StepRequest) string {
	var b strings.Builder

	if body := req.Step.AgentPrompt(); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}

	if req.Step.AppendSystemPrompt != "" {
		b.WriteString(req.Step.AppendSystemPrompt)
		b.WriteString("\n\n")
	}

	for _, inp := range req.Inputs {
		if inp.Ref.Inline {
			b.WriteString(inp.Value)
			b.WriteString("\n\n")
		} else {
			b.WriteString(inp.Value)
			b.WriteString("\n")
		}
	}

	if req.Feedback != "" {
		b.WriteString("\n[Reviewer feedback: ")
		b.WriteString(req.Feedback)
		b.WriteString("]\n")
	}

	return strings.TrimSpace(b.String())
}

func failResult(msg string, start time.Time) *step.Result {
	return &step.Result{
		Status:   step.StatusFailed,
		Err:      msg,
		Duration: time.Since(start),
	}
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

// Ensure AgentExecutor satisfies engine.Executor at compile time.
var _ engine.Executor = (*AgentExecutor)(nil)
