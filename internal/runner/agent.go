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
	// If the step enables AskUserQuestion, register the in-process MCP server
	// so the CLI recognises mcp__jig__AskUserQuestion (rewritten in buildOptions).
	if containsStr(req.Step.AllowedTools, "AskUserQuestion") {
		opts = append(opts, claudecode.WithSdkMcpServer("jig", buildAskUserQuestionServer(rep)))
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

	// sendCh carries tool-result messages injected mid-session (e.g. AskUserQuestion
	// answers). QueryStream starts a goroutine that reads from sendCh and forwards
	// each message to the SDK transport; closing sendCh signals end-of-stream.
	sendCh := make(chan claudecode.StreamMessage, 4)
	if err := client.QueryStream(ctx, sendCh); err != nil {
		close(sendCh)
		return failResult(fmt.Sprintf("agent query stream: %v", err), start), nil
	}
	defer close(sendCh)

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
		// AskUserQuestion is not a Claude Code built-in; it is implemented as an
		// in-process MCP tool. Rewrite it to the MCP-qualified name so the CLI
		// recognises it. The server is registered separately in Execute.
		opts = append(opts, claudecode.WithAllowedTools(rewriteAskUserQuestion(st.AllowedTools)...))
	}
	if len(st.DisallowedTools) > 0 {
		opts = append(opts, claudecode.WithDisallowedTools(st.DisallowedTools...))
	}
	// Every agent step is constrained by the merged schema (base + declared).
	// The base schema guarantees a minimum set of fields regardless of whether
	// the step declares a [step.schema].
	merged := workflow.MergedSchema(st.Schema)
	raw, err := merged.JSONSchema()
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	opts = append(opts, claudecode.WithJSONSchema(m))
	return opts, nil
}

// rewriteAskUserQuestion replaces every "AskUserQuestion" entry in tools with
// "mcp__jig__AskUserQuestion". The Claude Code CLI does not expose AskUserQuestion
// as a built-in; jig registers it as an in-process MCP server so the CLI uses
// the MCP-qualified name in its allowed-tools list.
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

// buildAskUserQuestionServer creates an in-process MCP server named "jig" that
// exposes one tool: AskUserQuestion. When the agent calls the tool the handler
// blocks on rep.Question, which pauses the step and surfaces the question in the
// TUI. The SDK injects the answer back into the conversation as a tool result,
// so no manual sendCh injection is needed.
func buildAskUserQuestionServer(rep engine.Reporter) *claudecode.McpSdkServerConfig {
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
								"required": []any{"label", "description"},
							},
						},
					},
					"required": []any{"question"},
				},
			},
		},
		"required": []any{"questions"},
	}
	tool := claudecode.NewTool(
		"AskUserQuestion",
		"Ask the user one or more questions and wait for their answer before continuing.",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			questions, err := parseAskUserQuestions(args)
			if err != nil {
				return &claudecode.McpToolResult{
					IsError: true,
					Content: []claudecode.McpContent{{Type: "text", Text: err.Error()}},
				}, nil
			}
			answer := rep.Question("ask-user-question", questions)
			return &claudecode.McpToolResult{
				Content: []claudecode.McpContent{{Type: "text", Text: answer}},
			}, nil
		},
	)
	return claudecode.CreateSDKMcpServer("jig", "1.0.0", tool)
}

// captureStream consumes the SDK message stream, appending each message to the
// per-step transcript and emitting Phase 2 liveness signals, and returns the
// step Result. It is split out of Execute so tests can drive it with a scripted
// channel — no live SDK connection required (see agent_test.go).
//
// send, when non-nil, is a channel for injecting tool-result messages back into
// the running agent session (used for AskUserQuestion interception). Pass nil
// when no mid-session injection is needed (e.g. in tests that don't exercise
// that path).
//
// The transcript file is the durable source of truth; rep.Output deltas are an
// ephemeral preview of the not-yet-finalized assistant bubble and are never
// persisted. When req.TranscriptPath is empty (persistence off) no transcript
// is written and no rep.Message signals are emitted.
func captureStream(
	msgChan <-chan claudecode.Message,
	req engine.StepRequest,
	rep engine.Reporter,
	start time.Time,
	initialUserMsg string,
) (*step.Result, error) {
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

	for msg := range msgChan {
		switch m := msg.(type) {
		case *claudecode.AssistantMessage:
			blocks, _ := assistantBlocks(m)
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
				errStr := subtypeErrText(m)
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

			// Marshal the structured output and extract raw_result, which is
			// the agent's prose answer and becomes the content of output.md.
			var rawResult string
			if m.StructuredOutput != nil {
				if raw, err := json.Marshal(m.StructuredOutput); err == nil {
					result.Structured = raw
					var obj map[string]any
					if err := json.Unmarshal(raw, &obj); err == nil {
						if rr, ok := obj["raw_result"].(string); ok {
							rawResult = rr
						}
					}
				}
			}

			// Auto-capture to the canonical step directory whenever persistence
			// is on (TranscriptPath is set). The step dir is already created by
			// the engine before dispatch. output.json holds the full structured
			// envelope; output.md holds the prose raw_result for downstream
			// agents and human review.
			if req.TranscriptPath != "" {
				stepDir := filepath.Dir(req.TranscriptPath)
				if result.Structured != nil {
					_ = os.WriteFile(filepath.Join(stepDir, "output.json"), result.Structured, 0o644)
				}
				if rawResult != "" {
					outMD := filepath.Join(stepDir, "output.md")
					if err := os.WriteFile(outMD, []byte(rawResult), 0o644); err == nil {
						result.OutputPath = outMD
					}
				}
			}

			// Also write raw_result to the explicitly declared output path so
			// humans have a stable, named artifact alongside the run-dir copy.
			if req.Step.Output != "" && rawResult != "" {
				outPath := req.Step.Output
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err == nil {
					_ = os.WriteFile(outPath, []byte(rawResult), 0o644)
				}
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
//   - the engine-assembled "Workflow context" preamble (if any), ended by its
//     `---` delimiter — this rides at the front of the agent's single user turn
//     (jig passes no separate system prompt)
//   - the agent-file body (if any) from the workflow step
//   - the append_system_prompt annotation (if any)
//   - a labeled input section (preamble + per-input provenance labels)
//   - the feedback artifact (if the step is re-running inside a loop)
//
// When WorkflowContext is empty the output is byte-identical to the pre-feature
// four-part prompt (the persistence-off / inject_context = false path).
func buildAgentPrompt(req engine.StepRequest) string {
	var b strings.Builder

	if req.WorkflowContext != "" {
		b.WriteString(req.WorkflowContext)
		b.WriteString("\n\n")
	}

	if body := req.Step.AgentPrompt(); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}

	if req.Step.AppendSystemPrompt != "" {
		b.WriteString(req.Step.AppendSystemPrompt)
		b.WriteString("\n\n")
	}

	if len(req.Inputs) > 0 {
		b.WriteString("The following inputs are provided for your task:\n\n")
		for _, inp := range req.Inputs {
			label := resolvedInputLabel(inp)
			if inp.Ref.Inline {
				b.WriteString(label)
				b.WriteString(":\n")
				b.WriteString(inp.Value)
				b.WriteString("\n\n")
			} else {
				b.WriteString(label)
				b.WriteString(": ")
				b.WriteString(inp.Value)
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	if req.Feedback != "" {
		b.WriteString("\n[Reviewer feedback: ")
		b.WriteString(req.Feedback)
		b.WriteString("]\n")
	}

	return strings.TrimSpace(b.String())
}

// resolvedInputLabel returns a bracketed provenance label for one input entry.
func resolvedInputLabel(inp engine.ResolvedInput) string {
	ref := inp.Ref
	switch {
	case ref.From == "user":
		if ref.As != "" {
			return "[Human input — '" + ref.As + "']"
		}
		return "[Human input]"
	case ref.Ref != "" && len(ref.RefField) > 0:
		return "[Field '" + strings.Join(ref.RefField, ".") + "' from step '" + ref.Ref + "']"
	case ref.Ref != "":
		return "[Previous step output — '" + ref.Ref + "']"
	default:
		return "[Reference document]"
	}
}

func failResult(msg string, start time.Time) *step.Result {
	return &step.Result{
		Status:   step.StatusFailed,
		Err:      msg,
		Duration: time.Since(start),
	}
}

// subtypeErrText returns a descriptive error message for a failed ResultMessage.
// Policy-limit subtypes (error_max_turns, error_max_budget_usd) get a clear
// human-readable prefix so operators can distinguish them from API failures at
// a glance. All other subtypes fall through to resultErrorText.
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
	// Append any additional detail the SDK provided so context is not lost.
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

// parseAskUserQuestions extracts the structured question payload from an
// AskUserQuestion tool call input map. Returns an error if the payload is
// malformed or missing.
func parseAskUserQuestions(input map[string]any) ([]engine.AgentQuestionItem, error) {
	raw, ok := input["questions"]
	if !ok {
		return nil, fmt.Errorf("AskUserQuestion: missing questions field")
	}
	// Re-encode and decode to drive the type assertions through JSON.
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("AskUserQuestion: marshal questions: %w", err)
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
		return nil, fmt.Errorf("AskUserQuestion: unmarshal questions: %w", err)
	}
	out := make([]engine.AgentQuestionItem, len(items))
	for i, it := range items {
		opts := make([]engine.AgentQuestionOption, len(it.Options))
		for j, o := range it.Options {
			opts[j] = engine.AgentQuestionOption{Label: o.Label, Description: o.Description}
		}
		out[i] = engine.AgentQuestionItem{
			Header:      it.Header,
			Question:    it.Question,
			Options:     opts,
			MultiSelect: it.MultiSelect,
		}
	}
	return out, nil
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Ensure AgentExecutor satisfies engine.Executor at compile time.
var _ engine.Executor = (*AgentExecutor)(nil)
