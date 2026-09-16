package helpchat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/transcript"
)

// DispatchFunc enqueues a typed monitor message without blocking the tool handler.
// The monitor drains its dispatch channel and re-queues messages as tea.Cmd.
type DispatchFunc func(tea.Msg)

// ToolHandler executes one jig-help tool call and returns its result text and
// whether the call failed. Tool names/descriptions/JSON schemas now live in
// cmd/jig/mcp_serve.go's mcpToolDefs (the agent-facing MCP surface); this file
// only implements the dispatch-side behavior, since jig mcp-serve forwards
// tool calls by name, not by schema.
type ToolHandler func(ctx context.Context, args map[string]any) (result string, isError bool)

// BuildToolHandlers returns the ten jig-help tool handlers keyed by tool
// name, for the TCP server side (helpchatServer) to dispatch forwarded
// tools/call requests from the spawned jig mcp-serve subprocess against.
//
// gateReq/gateAns implement the rendezvous for the final-merge gate — the one
// action that requires a structural TUI confirmation rather than fire-and-forget.
// The handler writes to gateReq and blocks on gateAns; the monitor reads
// gateReq, shows a TUI prompt, and writes the operator's yes/no to gateAns.
func BuildToolHandlers(
	run *engine.Run,
	runDir string,
	dispatch DispatchFunc,
	gateReq chan<- struct{},
	gateAns <-chan bool,
) map[string]ToolHandler {
	return map[string]ToolHandler{
		"workflow_snapshot":    buildWorkflowSnapshot(run),
		"read_step_transcript": buildReadStepTranscript(run, runDir),
		"read_step_result":     buildReadStepResult(run, runDir),
		"read_step_output":     buildReadStepOutput(run, runDir),
		"recover_step":         buildRecoverStep(run, dispatch),
		"reset_step":           buildResetStep(run, dispatch),
		"stop_step":            buildStopStep(run, dispatch),
		"resume_step":          buildResumeStep(run, dispatch),
		"resolve_review":       buildResolveReview(run, dispatch, gateReq, gateAns),
		"ask_user":             buildAskUser(dispatch),
	}
}

// ── read-only tools ───────────────────────────────────────────────────────────

func buildWorkflowSnapshot(run *engine.Run) ToolHandler {
	return func(_ context.Context, _ map[string]any) (string, bool) {
		snap := run.Snapshot()
		raw, err := json.Marshal(snap)
		if err != nil {
			return fmt.Sprintf("marshal snapshot: %v", err), true
		}
		return string(raw), false
	}
}

func buildReadStepTranscript(run *engine.Run, runDir string) ToolHandler {
	return func(_ context.Context, args map[string]any) (string, bool) {
		stepID, ok := args["step_id"].(string)
		if !ok || stepID == "" {
			return "step_id is required", true
		}
		n := 0
		if v, ok := args["last_n"].(float64); ok {
			n = int(v)
		}
		_ = run // ensure run is accessible for future validation
		tPath := datastore.TranscriptPath(runDir, stepID)
		r, err := transcript.Open(tPath)
		if err != nil {
			return fmt.Sprintf("open transcript: %v", err), true
		}
		var entries []transcript.Entry
		if n > 0 {
			entries, err = r.Tail(n)
		} else {
			entries, err = r.Window(0, 0)
		}
		if err != nil {
			return fmt.Sprintf("read transcript: %v", err), true
		}
		raw, err := json.Marshal(entries)
		if err != nil {
			return fmt.Sprintf("marshal entries: %v", err), true
		}
		return string(raw), false
	}
}

func buildReadStepResult(run *engine.Run, runDir string) ToolHandler {
	return func(_ context.Context, args map[string]any) (string, bool) {
		stepID, ok := args["step_id"].(string)
		if !ok || stepID == "" {
			return "step_id is required", true
		}
		_ = run
		path := datastore.ResultPath(runDir, stepID)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("read result: %v", err), true
		}
		return string(data), false
	}
}

func buildReadStepOutput(run *engine.Run, runDir string) ToolHandler {
	return func(_ context.Context, args map[string]any) (string, bool) {
		stepID, ok := args["step_id"].(string)
		if !ok || stepID == "" {
			return "step_id is required", true
		}
		_ = run
		resultPath := datastore.ResultPath(runDir, stepID)
		data, err := os.ReadFile(resultPath)
		if err != nil {
			return fmt.Sprintf("read result: %v", err), true
		}
		var result struct {
			OutputPath string `json:"output_path"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Sprintf("parse result: %v", err), true
		}
		if result.OutputPath == "" {
			return "step has no output artifact", true
		}
		out, err := os.ReadFile(result.OutputPath)
		if err != nil {
			return fmt.Sprintf("read output: %v", err), true
		}
		return string(out), false
	}
}

// ── action tools ─────────────────────────────────────────────────────────────

func buildRecoverStep(run *engine.Run, dispatch DispatchFunc) ToolHandler {
	return func(_ context.Context, args map[string]any) (string, bool) {
		stepID, _ := args["step_id"].(string)
		action, _ := args["action"].(string)
		guidance, _ := args["guidance"].(string)
		if stepID == "" || action == "" {
			return "step_id and action are required", true
		}
		dispatch(RecoverAction{StepID: stepID, Action: action, Text: guidance})
		return fmt.Sprintf("recover action %q enqueued for step %q; call workflow_snapshot to verify transition", action, stepID), false
	}
}

func buildResetStep(run *engine.Run, dispatch DispatchFunc) ToolHandler {
	return func(_ context.Context, args map[string]any) (string, bool) {
		stepID, _ := args["step_id"].(string)
		if stepID == "" {
			return "step_id is required", true
		}
		dispatch(ResetAction{StepID: stepID})
		return fmt.Sprintf("reset enqueued for step %q and its dependents; call workflow_snapshot to verify", stepID), false
	}
}

func buildStopStep(run *engine.Run, dispatch DispatchFunc) ToolHandler {
	return func(_ context.Context, args map[string]any) (string, bool) {
		stepID, _ := args["step_id"].(string)
		if stepID == "" {
			return "step_id is required", true
		}
		dispatch(StopAction{StepID: stepID})
		return fmt.Sprintf("stop enqueued for step %q; call workflow_snapshot to verify", stepID), false
	}
}

func buildResumeStep(run *engine.Run, dispatch DispatchFunc) ToolHandler {
	return func(_ context.Context, args map[string]any) (string, bool) {
		stepID, _ := args["step_id"].(string)
		message, _ := args["message"].(string)
		if stepID == "" {
			return "step_id is required", true
		}
		dispatch(ResumeAction{StepID: stepID, Message: message})
		return fmt.Sprintf("resume enqueued for step %q; call workflow_snapshot to verify", stepID), false
	}
}

func buildResolveReview(run *engine.Run, dispatch DispatchFunc, gateReq chan<- struct{}, gateAns <-chan bool) ToolHandler {
	return func(ctx context.Context, args map[string]any) (string, bool) {
		stepID, _ := args["step_id"].(string)
		verdict, _ := args["verdict"].(string)
		if stepID == "" || verdict == "" {
			return "step_id and verdict are required", true
		}

		// Final-merge gate uses a rendezvous channel to block until the
		// operator confirms in the TUI — the one truly irreversible action.
		// ctx.Done() is also honored here (unlike the pre-Unit-4 in-process
		// version, which never needed it): ctx is cancelled if the local tool
		// server's connection to jig mcp-serve drops mid-wait, so a crashed
		// subprocess surfaces a clear error instead of blocking forever.
		if strings.EqualFold(stepID, "final_merge") {
			select {
			case gateReq <- struct{}{}:
			case <-ctx.Done():
				return "operator did not respond (connection lost)", true
			}
			select {
			case approved := <-gateAns:
				if approved {
					return "final merge approved by operator", false
				}
				return "final merge discarded by operator", false
			case <-ctx.Done():
				return "operator did not respond (connection lost)", true
			}
		}

		dispatch(ReviewVerdict{StepID: stepID, Verdict: verdict})
		return fmt.Sprintf("verdict %q enqueued for step %q; call workflow_snapshot to verify", verdict, stepID), false
	}
}

var helpQuestionSeq atomic.Uint64

func buildAskUser(dispatch DispatchFunc) ToolHandler {
	return func(ctx context.Context, args map[string]any) (string, bool) {
		question, _ := args["question"].(string)
		if question == "" {
			return "question is required", true
		}
		var options []string
		if raw, ok := args["options"].([]any); ok {
			for _, v := range raw {
				if s, ok := v.(string); ok {
					options = append(options, s)
				}
			}
		}
		field := interaction.QuestionField{
			ID:     "answer",
			Prompt: question,
			Kind:   interaction.FieldText,
		}
		if len(options) > 0 {
			field.Kind = interaction.FieldSingleSelect
			field.AllowCustom = true
			for _, option := range options {
				field.Options = append(field.Options, interaction.QuestionOption{Value: option, Label: option})
			}
		}
		req := interaction.QuestionRequest{
			ID:      fmt.Sprintf("help-question-%d", helpQuestionSeq.Add(1)),
			Message: question,
			Fields:  []interaction.QuestionField{field},
		}
		ansC := make(chan interaction.QuestionResponse, 1)
		dispatch(QuestionRequestMsg{Request: req, AnsC: ansC})
		select {
		case response := <-ansC:
			switch response.Action {
			case interaction.ActionCancel:
				return "operator cancelled the question", true
			case interaction.ActionDecline:
				return "operator declined to answer", false
			}
			answer := response.Answers["answer"]
			if answer.Custom != "" {
				return answer.Custom, false
			}
			return strings.Join(answer.Values, ", "), false
		case <-ctx.Done():
			return "operator did not respond (context cancelled)", true
		}
	}
}
