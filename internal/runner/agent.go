package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/engine"
	"jig/internal/step"
)

// AgentExecutor runs agent steps via the Claude Agent SDK.
// Each Execute call opens a fresh connection so runs are independent and the
// executor itself holds no mutable state.
type AgentExecutor struct{}

// NewAgentExecutor returns an AgentExecutor ready to use.
func NewAgentExecutor() *AgentExecutor { return &AgentExecutor{} }

// Execute runs one agent step. It:
//  1. Builds the prompt from the step's skill/agent-file body and inputs.
//  2. Streams partial messages back through rep.Output / rep.ToolCall.
//  3. Captures the final assistant message and, if step.Output is set, writes
//     it to that path (the "final message becomes the artifact" contract).
//  4. Returns a Result that summarises the outcome.
func (e *AgentExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	start := time.Now()

	opts := []claudecode.Option{claudecode.WithIncludePartialMessages(true)}
	if req.Worktree != "" {
		opts = append(opts, claudecode.WithCwd(req.Worktree))
	}
	client := claudecode.NewClient(opts...)
	if err := client.Connect(ctx); err != nil {
		return failResult(fmt.Sprintf("agent connect: %v", err), start), nil
	}
	defer func() { _ = client.Disconnect() }()

	msgChan := client.ReceiveMessages(ctx)

	prompt := buildAgentPrompt(req)
	if err := client.Query(ctx, prompt); err != nil {
		return failResult(fmt.Sprintf("agent query: %v", err), start), nil
	}

	var text strings.Builder
	for msg := range msgChan {
		switch m := msg.(type) {
		case *claudecode.StreamEvent:
			if delta, ok := agentTextDelta(m); ok {
				text.WriteString(delta)
				rep.Output(delta)
			}
			if tool, detail, ok := agentToolCall(m); ok {
				rep.ToolCall(tool, detail)
			}
		case *claudecode.ResultMessage:
			if m.IsError {
				errStr := "unknown agent error"
				if m.Result != nil {
					errStr = *m.Result
				}
				return failResult(errStr, start), nil
			}
			// Turn complete: write content artifact if configured.
			result := &step.Result{
				Status:   step.StatusSucceeded,
				Duration: time.Since(start),
			}
			if req.Step.Output != "" && text.Len() > 0 {
				outPath := req.Step.Output
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err == nil {
					_ = os.WriteFile(outPath, []byte(text.String()), 0o644)
				}
				result.OutputPath = outPath
			}
			return result, nil
		}
	}

	// msgChan closed before ResultMessage — connection dropped.
	return failResult("agent connection closed unexpectedly", start), nil
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
		b.WriteString("\n[Previous iteration feedback: ")
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

// agentToolCall extracts tool-use metadata from a content_block_start event,
// returning ("", "", false) for non-tool events.
func agentToolCall(ev *claudecode.StreamEvent) (tool, detail string, ok bool) {
	if ev.Event["type"] != "content_block_start" {
		return "", "", false
	}
	cb, isMap := ev.Event["content_block"].(map[string]any)
	if !isMap || cb["type"] != "tool_use" {
		return "", "", false
	}
	name, _ := cb["name"].(string)
	if name == "" {
		return "", "", false
	}
	// input may not be populated yet at start; provide an empty detail.
	return name, "", true
}

// Ensure AgentExecutor satisfies engine.Executor at compile time.
var _ engine.Executor = (*AgentExecutor)(nil)
