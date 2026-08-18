package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

// AgentExecutor runs agent steps via a harness.Harness backend. Each Execute
// call resolves the step's backend/transport to a Harness and opens a fresh
// session so runs are independent and the executor itself holds no mutable
// session state.
type AgentExecutor struct {
	forHarness func(backend, transport string) (harness.Harness, error)
}

// NewAgentExecutor returns an AgentExecutor that resolves harnesses via
// forHarness (typically harness.For).
func NewAgentExecutor(forHarness func(backend, transport string) (harness.Harness, error)) *AgentExecutor {
	return &AgentExecutor{forHarness: forHarness}
}

// NewAgentExecutorFixed returns an AgentExecutor that always uses h, ignoring
// the step's backend/transport. Intended for tests that inject a FakeHarness.
func NewAgentExecutorFixed(h harness.Harness) *AgentExecutor {
	return NewAgentExecutor(func(string, string) (harness.Harness, error) { return h, nil })
}

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
	h, err := e.forHarness(req.Step.Backend, req.Step.Transport)
	if err != nil {
		return failResult(fmt.Sprintf("harness: %v", err), start), nil
	}
	caps := h.Capabilities()

	// block_on parks this step and resumes it later through a fresh Open call
	// with SessionSpec.Resume set. Reject now, at the step's first Open, rather
	// than letting it run to the pause point and only then discovering the
	// active harness can never honor the resume that block_on depends on.
	if req.Step.BlockOn != "" && !caps.Has(harness.CapSessionResume) {
		return failResult(fmt.Sprintf("step declares block_on but harness %q does not support session resume (CapSessionResume)", h.Name()), start), nil
	}

	// A step that explicitly declares [step.schema] has opted into structured
	// output and must fail closed if unsupported. The base schema every step
	// is merged with regardless of declaration is not a user request, so
	// buildSessionSpec omits it silently instead (see its doc comment).
	if req.Step.Schema != nil && !caps.Has(harness.CapStructuredOutput) {
		return failResult(fmt.Sprintf("step declares a schema but harness %q does not support structured output (CapStructuredOutput)", h.Name()), start), nil
	}

	spec, err := buildSessionSpec(req.Step, caps)
	if err != nil {
		return failResult(fmt.Sprintf("build options: %v", err), start), nil
	}

	spec.Prompt = buildAgentPrompt(req)
	if req.ResumeSessionID != "" {
		if !caps.Has(harness.CapSessionResume) {
			return failResult(fmt.Sprintf("resume requested but harness %q does not support session resume (CapSessionResume)", h.Name()), start), nil
		}
		spec.Prompt = req.Message
		spec.Resume = req.ResumeSessionID
	}
	if req.Worktree != "" {
		spec.Cwd = req.Worktree
	}
	// If the step enables AskUserQuestion, register the in-process MCP server
	// so the CLI recognises mcp__jig__AskUserQuestion (rewritten by the harness).
	if containsStr(req.Step.AllowedTools, "AskUserQuestion") {
		if !caps.Has(harness.CapInProcessMCP) {
			return failResult(fmt.Sprintf("step uses AskUserQuestion but harness %q does not support in-process MCP tools (CapInProcessMCP)", h.Name()), start), nil
		}
		spec.MCPServers = append(spec.MCPServers, buildAskUserQuestionServer(ctx, rep))
	}
	if req.Guard != nil {
		if !caps.Has(harness.CapPermissionCallback) {
			return failResult(fmt.Sprintf("step is guarded but harness %q does not support permission callbacks (CapPermissionCallback)", h.Name()), start), nil
		}
		guard := req.Guard
		spec.Permission = func(toolName string, input map[string]any) harness.Decision {
			// Findings and the SecurityFinding event are produced by captureStream
			// when it processes the buffered assistant blocks. The callback only
			// needs to return the decision so the backend feeds it back to the agent.
			dec := guard.Check(toolName, input)
			return harness.Decision{Allow: dec.Allow, Reason: dec.Reason}
		}
	}

	sess, err := h.Open(ctx, spec)
	if err != nil {
		return failResult(fmt.Sprintf("agent open: %v", err), start), nil
	}
	defer func() { _ = sess.Close() }()

	initialMsg := ""
	if req.ResumeSessionID != "" {
		initialMsg = req.Message
	}
	return captureStream(sess.Messages(), req, rep, start, initialMsg)
}

// buildSessionSpec translates a step's already-defaulted model/tool/permission
// fields (resolved from [defaults] by workflow.applyDefaults before this ever
// runs) into a harness.SessionSpec. Zero-value fields are simply omitted so
// the harness's own defaults apply. Prompt/Cwd/Resume/Permission/MCPServers
// are filled in separately by Execute, which has the request-scoped context
// this function does not.
//
// Partial and Schema are capability-gated: every agent step wants them (base
// schema enforcement, live-typing deltas) regardless of what the user
// declared, but neither is a hard requirement a step depends on, so a harness
// that lacks the capability simply doesn't receive the field — unlike Guard/
// AskUserQuestion/Resume in Execute, which the user explicitly opted into and
// which fail closed instead of silently degrading.
func buildSessionSpec(st *workflow.Step, caps harness.CapabilitySet) (harness.SessionSpec, error) {
	spec := harness.SessionSpec{
		Partial:           caps.Has(harness.CapPartialStreaming),
		Model:             st.Model,
		FallbackModel:     st.FallbackModel,
		Effort:            string(st.Effort),
		MaxTurns:          st.MaxTurns,
		MaxThinkingTokens: st.MaxThinkingTokens,
		MaxBudgetUSD:      st.MaxBudgetUSD,
		PermissionMode:    st.PermissionMode,
		AllowedTools:      st.AllowedTools,
		DisallowedTools:   st.DisallowedTools,
	}

	if !caps.Has(harness.CapStructuredOutput) {
		return spec, nil
	}

	// Every agent step is constrained by the merged schema (base + declared).
	// The base schema guarantees a minimum set of fields regardless of whether
	// the step declares a [step.schema].
	merged := workflow.MergedSchema(st.Schema)
	raw, err := merged.JSONSchema()
	if err != nil {
		return harness.SessionSpec{}, fmt.Errorf("schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return harness.SessionSpec{}, fmt.Errorf("schema: %w", err)
	}
	spec.Schema = m
	return spec, nil
}

// buildAskUserQuestionServer creates an in-process MCP server named "jig" that
// exposes one tool: AskUserQuestion. When the agent calls the tool the handler
// blocks on rep.Question, which pauses the step and surfaces the question in the
// TUI. The harness feeds the returned ToolResult back into the conversation, so
// no manual send-channel injection is needed.
//
// stepCtx is the executing step's context; it is threaded into rep.Question so a
// Stop/cancel of the step unblocks a pending AskUserQuestion instead of hanging
// the handler goroutine forever.
func buildAskUserQuestionServer(stepCtx context.Context, rep engine.Reporter) harness.MCPServer {
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
	tool := harness.Tool{
		Name:        "AskUserQuestion",
		Description: "Ask the user one or more questions and wait for their answer before continuing.",
		InputSchema: schema,
		Handler: func(_ context.Context, args map[string]any) (harness.ToolResult, error) {
			questions, err := parseAskUserQuestions(args)
			if err != nil {
				return harness.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			answer := rep.Question(stepCtx, "ask-user-question", questions)
			return harness.ToolResult{Content: answer}, nil
		},
	}
	return harness.MCPServer{Name: "jig", Version: "1.0.0", Tools: []harness.Tool{tool}}
}

// captureStream consumes the harness event stream, appending each turn to the
// per-step transcript and emitting Phase 2 liveness signals, and returns the
// step Result. It is split out of Execute so tests can drive it with a scripted
// channel — no live backend connection required (see agent_test.go).
//
// The transcript file is the durable source of truth; rep.Output deltas are an
// ephemeral preview of the not-yet-finalized assistant bubble and are never
// persisted. When req.TranscriptPath is empty (persistence off) no transcript
// is written and no rep.Message signals are emitted.
func captureStream(
	events <-chan harness.Event,
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

	// Open the findings sink when the guard is active and persistence is on.
	// A nil guard or empty FindingsPath leaves fw nil (no-op path).
	var fw *sentinel.Writer
	if req.Guard != nil && req.FindingsPath != "" {
		var err error
		fw, err = sentinel.NewWriter(req.FindingsPath)
		if err != nil {
			return failResult(fmt.Sprintf("findings sink: %v", err), start), nil
		}
		defer func() { _ = fw.Close() }()
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

	// Capture the backend session id as early as it is surfaced (spec 07 B2).
	// With partial streaming on, an EventSessionID may fire well before the
	// terminal EventResult. Recording it here means a step stopped mid-turn
	// still returns a resumable session id (see the connection-closed path
	// below), where capturing only on EventResult would leave a cancelled step
	// with SessionID == "".
	sessionID := ""
	noteSession := func(id string) {
		if id != "" && sessionID == "" {
			sessionID = id
		}
	}

	// lastAssistantText is the concatenated text from the most recent flushed
	// assistant turn. The engine writes this to raw_result.md on success — no
	// agent Write tool required; the engine owns all non-code file writes.
	var lastAssistantText string

	// buf accumulates Text/Thinking/ToolUse/ToolResult blocks between flush
	// boundaries (EventAssistantEnd/EventUserEnd), mirroring the "one Entry per
	// SDK message" grouping the pre-harness capture path produced.
	var buf []transcript.Block

	for ev := range events {
		switch ev.Type {
		case harness.EventSessionID:
			noteSession(ev.SessionID)
		case harness.EventText:
			buf = append(buf, transcript.Block{Type: transcript.BlockText, Text: ev.Text})
		case harness.EventThinking:
			// Thinking may be empty or redacted; capture whatever is present.
			buf = append(buf, transcript.Block{Type: transcript.BlockThinking, Text: ev.Text})
		case harness.EventToolUse:
			buf = append(buf, transcript.Block{
				Type:      transcript.BlockToolUse,
				ToolUseID: ev.ToolUseID,
				Name:      ev.Name,
				Input:     ev.Input,
			})
		case harness.EventToolResult:
			buf = append(buf, transcript.Block{
				Type:      transcript.BlockToolResult,
				ToolUseID: ev.ToolUseID,
				Content:   ev.Content,
				IsError:   ev.IsError,
			})
		case harness.EventAssistantEnd:
			blocks := guardBlocks(buf, req, rep, fw)
			buf = nil
			appendEntry(transcript.RoleAssistant, blocks)
			// Track the last substantive text turn. Tool-call-only turns yield no
			// text blocks and leave lastAssistantText unchanged.
			var sb strings.Builder
			for _, b := range blocks {
				if b.Type == transcript.BlockText {
					sb.WriteString(b.Text)
				}
			}
			if t := sb.String(); t != "" {
				lastAssistantText = t
			}
		case harness.EventUserEnd:
			blocks := buf
			buf = nil
			appendEntry(transcript.RoleUser, blocks)
		case harness.EventSystemText:
			appendEntry(transcript.RoleSystem, []transcript.Block{{Type: transcript.BlockText, Text: ev.Text}})
		case harness.EventTextDelta:
			// Live-typing tail only; the finalized EventText above is
			// authoritative and is what lands in the transcript.
			rep.Output(ev.Text)
		case harness.EventResult:
			if ev.IsError {
				appendEntry(transcript.RoleResult, []transcript.Block{{Type: transcript.BlockText, Text: ev.ErrText}})
				res := failResult(ev.ErrText, start)
				res.Subtype = ev.Subtype
				res.TotalCostUSD = ev.TotalCostUSD
				res.Usage = ev.Usage
				// Retain the session id even on failure so the engine can offer a
				// recovery that resumes this exact conversation (feeding the error
				// back in) rather than starting over blind.
				res.SessionID = ev.SessionID
				return res, nil
			}
			result := &step.Result{
				Status:       step.StatusSucceeded,
				Duration:     time.Since(start),
				SessionID:    ev.SessionID,
				Subtype:      ev.Subtype,
				TotalCostUSD: ev.TotalCostUSD,
				Usage:        ev.Usage,
			}

			// Structured output carries only brief metadata fields; large prose
			// lives in raw_result.md, written by the engine from the agent's text
			// response below.
			if ev.Structured != nil {
				result.Structured = ev.Structured
			}

			// Auto-capture to the canonical step directory whenever persistence
			// is on. output.json holds the full structured envelope; output.md
			// holds a rich markdown rendering of metadata fields for TUI display.
			// raw_result.md holds the agent's prose response — written by the
			// engine from lastAssistantText so no agent Write tool is required.
			if req.TranscriptPath != "" {
				stepDir := filepath.Dir(req.TranscriptPath)
				if result.Structured != nil {
					_ = os.WriteFile(filepath.Join(stepDir, "output.json"), result.Structured, 0o644)
					if md := structuredToMarkdown(result.Structured); md != "" {
						_ = os.WriteFile(filepath.Join(stepDir, "output.md"), []byte(md), 0o644)
					}
				}
				if lastAssistantText != "" {
					rawPath := filepath.Join(stepDir, "raw_result.md")
					if err := os.WriteFile(rawPath, []byte(lastAssistantText), 0o644); err == nil {
						result.OutputPath = rawPath
						// Also write to the step's declared output path when set.
						if req.Step.Output != "" {
							if err := os.MkdirAll(filepath.Dir(req.Step.Output), 0o755); err == nil {
								_ = os.WriteFile(req.Step.Output, []byte(lastAssistantText), 0o644)
							}
						}
					}
				} else if md := structuredToMarkdown(result.Structured); md != "" {
					result.OutputPath = filepath.Join(stepDir, "output.md")
				}
			}

			return result, nil
		}
	}

	// events closed before EventResult — the connection dropped or the step's
	// context was cancelled (a deliberate stop, spec 07 B1). Carry the
	// early-captured session id on the result so the engine can resume this
	// conversation; without an early EventSessionID the backend may not have
	// surfaced one, in which case SessionID stays "" and resume degrades to a
	// fresh restart.
	res := failResult("agent connection closed unexpectedly", start)
	res.SessionID = sessionID
	return res, nil
}

// guardBlocks scans every tool_use block's input for policy violations when
// the guard is active. For each violation it produces a Finding (written to fw
// and emitted as a SecurityFinding ctrl event), and redacts the block's Input
// so that no raw secret lands in transcript.jsonl.
//
// When req.Guard is nil blocks is returned unchanged.
func guardBlocks(blocks []transcript.Block, req engine.StepRequest, rep engine.Reporter, fw *sentinel.Writer) []transcript.Block {
	if req.Guard == nil {
		return blocks
	}
	for i, b := range blocks {
		if b.Type != transcript.BlockToolUse || b.Input == nil {
			continue
		}
		var input map[string]any
		if err := json.Unmarshal(b.Input, &input); err != nil {
			continue
		}
		// Redact secrets before the block is appended to transcript.jsonl.
		if redacted := sentinel.RedactJSON(b.Name, b.Input); !bytes.Equal(redacted, b.Input) {
			blocks[i].Input = redacted
		}
		dec := req.Guard.Check(b.Name, input)
		if !dec.Allow {
			evidenceKey := "tool:" + b.Name + ":" + b.ToolUseID
			fp := sentinel.NewFingerprint(req.Step.ID, dec.Monitor, evidenceKey)
			sev := sentinel.SeverityHigh
			if dec.Action == sentinel.ActionEscalated {
				sev = sentinel.SeverityCritical
			}
			f := sentinel.Finding{
				Ts:          time.Now().UTC(),
				RunID:       req.RunID,
				StepID:      req.Step.ID,
				Iteration:   req.Iteration,
				Tier:        sentinel.TierGuard,
				Monitor:     dec.Monitor,
				Severity:    sev,
				Action:      dec.Action,
				Detail:      dec.Reason,
				Evidence:    evidenceKey,
				Fingerprint: fp,
			}
			if fw != nil {
				_ = fw.Append(f)
			}
			rep.Finding(engine.SecurityFinding{
				RunID:       req.RunID,
				StepID:      req.Step.ID,
				Tier:        string(sentinel.TierGuard),
				Monitor:     dec.Monitor,
				Severity:    string(sev),
				Action:      string(dec.Action),
				Fingerprint: fp,
			})
		}
	}
	return blocks
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

	// Inject output framing when persistence is on. The engine captures the
	// agent's text response as raw_result.md — no Write tool required. When the
	// step declares an output_template, its markdown skeleton follows so the agent
	// fills each section in its response.
	if req.TranscriptPath != "" {
		b.WriteString("## Output\n\nYour text response is captured as the primary artifact (`raw_result.md`) for downstream steps. Keep structured output fields (`summary`, `confidence`, `status`, etc.) brief.\n\n")
		if tmpl := req.Step.OutputTemplateBody(); tmpl != "" {
			b.WriteString("Structure your response using the following template:\n\n")
			b.WriteString(tmpl)
			b.WriteString("\n\n")
		}
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
