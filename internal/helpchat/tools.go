package helpchat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/transcript"
)

// DispatchFunc enqueues a typed monitor message without blocking the tool handler.
// The monitor drains its dispatch channel and re-queues messages as tea.Cmd.
type DispatchFunc func(tea.Msg)

// BuildMcpServer registers all ten jig-help tools and returns the server config
// for use with claudecode.WithSdkMcpServer("jig-help", ...).
//
// gateReq/gateAns implement the rendezvous for the final-merge gate — the one
// action that requires a structural TUI confirmation rather than fire-and-forget.
// The tool handler writes to gateReq and blocks on gateAns; the monitor reads
// gateReq, shows a TUI prompt, and writes the operator's yes/no to gateAns.
func BuildMcpServer(
	run *engine.Run,
	runDir string,
	dispatch DispatchFunc,
	gateReq chan<- struct{},
	gateAns <-chan bool,
) *claudecode.McpSdkServerConfig {
	return claudecode.CreateSDKMcpServer("jig-help", "1.0.0",
		buildWorkflowSnapshot(run),
		buildReadStepTranscript(run, runDir),
		buildReadStepResult(run, runDir),
		buildReadStepOutput(run, runDir),
		buildRecoverStep(run, dispatch),
		buildResetStep(run, dispatch),
		buildStopStep(run, dispatch),
		buildResumeStep(run, dispatch),
		buildResolveReview(run, dispatch, gateReq, gateAns),
		buildSendMessageToStep(run, dispatch),
		buildAskUser(dispatch),
	)
}

// ── read-only tools ───────────────────────────────────────────────────────────

func buildWorkflowSnapshot(run *engine.Run) *claudecode.McpTool {
	return claudecode.NewTool(
		"workflow_snapshot",
		"Return a JSON snapshot of all step IDs and their current statuses.",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		func(_ context.Context, _ map[string]any) (*claudecode.McpToolResult, error) {
			snap := run.Snapshot()
			raw, err := json.Marshal(snap)
			if err != nil {
				return errResult(fmt.Sprintf("marshal snapshot: %v", err)), nil
			}
			return okResult(string(raw)), nil
		},
	)
}

func buildReadStepTranscript(run *engine.Run, runDir string) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to read transcript for"},
			"last_n":  map[string]any{"type": "integer", "description": "Maximum number of entries to return (0 = all)"},
		},
		"required": []any{"step_id"},
	}
	return claudecode.NewTool(
		"read_step_transcript",
		"Read the last N transcript entries for a step (agent conversation, tool calls, results).",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, ok := args["step_id"].(string)
			if !ok || stepID == "" {
				return errResult("step_id is required"), nil
			}
			n := 0
			if v, ok := args["last_n"].(float64); ok {
				n = int(v)
			}
			_ = run // ensure run is accessible for future validation
			tPath := datastore.TranscriptPath(runDir, stepID)
			r, err := transcript.Open(tPath)
			if err != nil {
				return errResult(fmt.Sprintf("open transcript: %v", err)), nil
			}
			var entries []transcript.Entry
			if n > 0 {
				entries, err = r.Tail(n)
			} else {
				entries, err = r.Window(0, 0)
			}
			if err != nil {
				return errResult(fmt.Sprintf("read transcript: %v", err)), nil
			}
			raw, err := json.Marshal(entries)
			if err != nil {
				return errResult(fmt.Sprintf("marshal entries: %v", err)), nil
			}
			return okResult(string(raw)), nil
		},
	)
}

func buildReadStepResult(run *engine.Run, runDir string) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to read result for"},
		},
		"required": []any{"step_id"},
	}
	return claudecode.NewTool(
		"read_step_result",
		"Read the result.json for a step (status, error, output path, cost).",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, ok := args["step_id"].(string)
			if !ok || stepID == "" {
				return errResult("step_id is required"), nil
			}
			_ = run
			path := datastore.ResultPath(runDir, stepID)
			data, err := os.ReadFile(path)
			if err != nil {
				return errResult(fmt.Sprintf("read result: %v", err)), nil
			}
			return okResult(string(data)), nil
		},
	)
}

func buildReadStepOutput(run *engine.Run, runDir string) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to read output artifact for"},
		},
		"required": []any{"step_id"},
	}
	return claudecode.NewTool(
		"read_step_output",
		"Read the step's primary output artifact file (the agent's text response or command output).",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, ok := args["step_id"].(string)
			if !ok || stepID == "" {
				return errResult("step_id is required"), nil
			}
			_ = run
			resultPath := datastore.ResultPath(runDir, stepID)
			data, err := os.ReadFile(resultPath)
			if err != nil {
				return errResult(fmt.Sprintf("read result: %v", err)), nil
			}
			var result struct {
				OutputPath string `json:"output_path"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				return errResult(fmt.Sprintf("parse result: %v", err)), nil
			}
			if result.OutputPath == "" {
				return errResult("step has no output artifact"), nil
			}
			out, err := os.ReadFile(result.OutputPath)
			if err != nil {
				return errResult(fmt.Sprintf("read output: %v", err)), nil
			}
			return okResult(string(out)), nil
		},
	)
}

// ── action tools ─────────────────────────────────────────────────────────────

func buildRecoverStep(run *engine.Run, dispatch DispatchFunc) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id":  map[string]any{"type": "string", "description": "Step ID to recover"},
			"action":   map[string]any{"type": "string", "enum": []any{"retry", "resume", "abort"}, "description": "Recovery action"},
			"guidance": map[string]any{"type": "string", "description": "Optional guidance text for the resumed agent"},
		},
		"required": []any{"step_id", "action"},
	}
	return claudecode.NewTool(
		"recover_step",
		"Retry, resume, or abort a step in awaiting_recovery state.",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, _ := args["step_id"].(string)
			action, _ := args["action"].(string)
			guidance, _ := args["guidance"].(string)
			if stepID == "" || action == "" {
				return errResult("step_id and action are required"), nil
			}
			dispatch(RecoverAction{StepID: stepID, Action: action, Text: guidance})
			return okResult(fmt.Sprintf("recover action %q enqueued for step %q; call workflow_snapshot to verify transition", action, stepID)), nil
		},
	)
}

func buildResetStep(run *engine.Run, dispatch DispatchFunc) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to reset"},
		},
		"required": []any{"step_id"},
	}
	return claudecode.NewTool(
		"reset_step",
		"Reset a step and all its dependent steps back to pending. Destructive — confirm with the operator before calling.",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, _ := args["step_id"].(string)
			if stepID == "" {
				return errResult("step_id is required"), nil
			}
			dispatch(ResetAction{StepID: stepID})
			return okResult(fmt.Sprintf("reset enqueued for step %q and its dependents; call workflow_snapshot to verify", stepID)), nil
		},
	)
}

func buildStopStep(run *engine.Run, dispatch DispatchFunc) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to stop"},
		},
		"required": []any{"step_id"},
	}
	return claudecode.NewTool(
		"stop_step",
		"Stop a currently running step (parks it at stopped status).",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, _ := args["step_id"].(string)
			if stepID == "" {
				return errResult("step_id is required"), nil
			}
			dispatch(StopAction{StepID: stepID})
			return okResult(fmt.Sprintf("stop enqueued for step %q; call workflow_snapshot to verify", stepID)), nil
		},
	)
}

func buildResumeStep(run *engine.Run, dispatch DispatchFunc) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to resume"},
			"message": map[string]any{"type": "string", "description": "Optional message to pass to the resumed agent"},
		},
		"required": []any{"step_id"},
	}
	return claudecode.NewTool(
		"resume_step",
		"Resume a stopped step.",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, _ := args["step_id"].(string)
			message, _ := args["message"].(string)
			if stepID == "" {
				return errResult("step_id is required"), nil
			}
			dispatch(ResumeAction{StepID: stepID, Message: message})
			return okResult(fmt.Sprintf("resume enqueued for step %q; call workflow_snapshot to verify", stepID)), nil
		},
	)
}

func buildResolveReview(run *engine.Run, dispatch DispatchFunc, gateReq chan<- struct{}, gateAns <-chan bool) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to resolve, or \"final_merge\" for the final merge gate"},
			"verdict": map[string]any{"type": "string", "enum": []any{"approved", "rejected"}, "description": "Review verdict"},
		},
		"required": []any{"step_id", "verdict"},
	}
	return claudecode.NewTool(
		"resolve_review",
		"Resolve a review step with approved or rejected verdict. For the final merge gate, use step_id=\"final_merge\".",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, _ := args["step_id"].(string)
			verdict, _ := args["verdict"].(string)
			if stepID == "" || verdict == "" {
				return errResult("step_id and verdict are required"), nil
			}

			// Final-merge gate uses a rendezvous channel to block until the
			// operator confirms in the TUI — the one truly irreversible action.
			if strings.EqualFold(stepID, "final_merge") {
				gateReq <- struct{}{}
				approved := <-gateAns
				if approved {
					return okResult("final merge approved by operator"), nil
				}
				return okResult("final merge discarded by operator"), nil
			}

			dispatch(ReviewVerdict{StepID: stepID, Verdict: verdict})
			return okResult(fmt.Sprintf("verdict %q enqueued for step %q; call workflow_snapshot to verify", verdict, stepID)), nil
		},
	)
}

func buildSendMessageToStep(run *engine.Run, dispatch DispatchFunc) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step ID to send a message to"},
			"text":    map[string]any{"type": "string", "description": "Message text to send to the step"},
		},
		"required": []any{"step_id", "text"},
	}
	return claudecode.NewTool(
		"send_message_to_step",
		"Send a free-text message to a step waiting for reviewer input.",
		schema,
		func(_ context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			stepID, _ := args["step_id"].(string)
			text, _ := args["text"].(string)
			if stepID == "" || text == "" {
				return errResult("step_id and text are required"), nil
			}
			dispatch(ReviewMessage{StepID: stepID, Text: text})
			return okResult(fmt.Sprintf("message enqueued for step %q", stepID)), nil
		},
	)
}

func buildAskUser(dispatch DispatchFunc) *claudecode.McpTool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "The question to present to the operator.",
			},
			"options": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional list of choices. Omit for a free-text answer.",
			},
		},
		"required": []any{"question"},
	}
	return claudecode.NewTool(
		"ask_user",
		"Present a question to the operator and wait for their answer. "+
			"Provide options[] for a multiple-choice prompt; omit for free-text.",
		schema,
		func(ctx context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
			question, _ := args["question"].(string)
			if question == "" {
				return errResult("question is required"), nil
			}
			var options []string
			if raw, ok := args["options"].([]any); ok {
				for _, v := range raw {
					if s, ok := v.(string); ok {
						options = append(options, s)
					}
				}
			}
			ansC := make(chan string, 1)
			dispatch(QuestionRequestMsg{Question: question, Options: options, AnsC: ansC})
			select {
			case answer := <-ansC:
				return okResult(answer), nil
			case <-ctx.Done():
				return errResult("operator did not respond (context cancelled)"), nil
			}
		},
	)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func okResult(text string) *claudecode.McpToolResult {
	return &claudecode.McpToolResult{
		Content: []claudecode.McpContent{{Type: "text", Text: text}},
	}
}

func errResult(text string) *claudecode.McpToolResult {
	return &claudecode.McpToolResult{
		IsError: true,
		Content: []claudecode.McpContent{{Type: "text", Text: text}},
	}
}
