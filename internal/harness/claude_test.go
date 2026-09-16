package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/interaction"
	"jig/internal/toolcall"
)

func TestClaudeHarnessCapabilities(t *testing.T) {
	h := NewClaudeHarness()
	if h.Name() != "claude" {
		t.Fatalf("Name() = %q, want %q", h.Name(), "claude")
	}
	caps := h.Capabilities()
	for _, c := range []Capability{
		CapPermissionCallback,
		CapUserQuestion,
		CapSessionResume,
		CapStructuredOutput,
		CapPartialStreaming,
	} {
		if !caps.Has(c) {
			t.Errorf("ClaudeHarness.Capabilities() missing %v", c)
		}
	}
}

func TestClaudeOptionsTranslation(t *testing.T) {
	tests := []struct {
		name  string
		spec  SessionSpec
		check func(t *testing.T, o *claudecode.Options)
	}{
		{
			name: "model and effort",
			spec: SessionSpec{Model: "sonnet", Effort: "high"},
			check: func(t *testing.T, o *claudecode.Options) {
				if o.Model == nil || *o.Model != "sonnet" {
					t.Errorf("Model = %v, want sonnet", o.Model)
				}
				if o.Effort == nil || *o.Effort != "high" {
					t.Errorf("Effort = %v, want high", o.Effort)
				}
			},
		},
		{
			name: "cwd and limits",
			spec: SessionSpec{Cwd: "/tmp/work", MaxTurns: 5, MaxThinkingTokens: 100, MaxBudgetUSD: 2.5},
			check: func(t *testing.T, o *claudecode.Options) {
				if o.Cwd == nil || *o.Cwd != "/tmp/work" {
					t.Errorf("Cwd = %v, want /tmp/work", o.Cwd)
				}
				if o.MaxTurns != 5 {
					t.Errorf("MaxTurns = %d, want 5", o.MaxTurns)
				}
				if o.MaxThinkingTokens != 100 {
					t.Errorf("MaxThinkingTokens = %d, want 100", o.MaxThinkingTokens)
				}
				if o.MaxBudgetUSD == nil || *o.MaxBudgetUSD != 2.5 {
					t.Errorf("MaxBudgetUSD = %v, want 2.5", o.MaxBudgetUSD)
				}
			},
		},
		{
			name: "allowed tools rewrites AskUserQuestion",
			spec: SessionSpec{AllowedTools: []string{"Bash", "AskUserQuestion"}},
			check: func(t *testing.T, o *claudecode.Options) {
				want := []string{"Bash", "mcp__jig__AskUserQuestion"}
				got := o.AllowedTools
				if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
					t.Errorf("AllowedTools = %v, want %v", got, want)
				}
			},
		},
		{
			name: "disallowed tools pass through",
			spec: SessionSpec{DisallowedTools: []string{"WebSearch"}},
			check: func(t *testing.T, o *claudecode.Options) {
				if len(o.DisallowedTools) != 1 || o.DisallowedTools[0] != "WebSearch" {
					t.Errorf("DisallowedTools = %v, want [WebSearch]", o.DisallowedTools)
				}
			},
		},
		{
			name: "resume forces continue conversation",
			spec: SessionSpec{Resume: "sess-123"},
			check: func(t *testing.T, o *claudecode.Options) {
				if o.Resume == nil || *o.Resume != "sess-123" {
					t.Errorf("Resume = %v, want sess-123", o.Resume)
				}
				if !o.ContinueConversation {
					t.Errorf("ContinueConversation = false, want true")
				}
			},
		},
		{
			name: "schema sets output format",
			spec: SessionSpec{Schema: map[string]any{"type": "object"}},
			check: func(t *testing.T, o *claudecode.Options) {
				if o.OutputFormat == nil || o.OutputFormat.Schema["type"] != "object" {
					t.Errorf("OutputFormat = %+v, want schema type object", o.OutputFormat)
				}
			},
		},
		{
			name: "permission forces default mode and registers callback",
			spec: SessionSpec{Permission: func(string, map[string]any) Decision { return Decision{Allow: true} }},
			check: func(t *testing.T, o *claudecode.Options) {
				if o.PermissionMode == nil || *o.PermissionMode != claudecode.PermissionModeDefault {
					t.Errorf("PermissionMode = %v, want %v", o.PermissionMode, claudecode.PermissionModeDefault)
				}
				if o.CanUseTool == nil {
					t.Errorf("CanUseTool not set")
				}
			},
		},
		{
			name: "no permission leaves mode unset",
			spec: SessionSpec{},
			check: func(t *testing.T, o *claudecode.Options) {
				if o.PermissionMode != nil {
					t.Errorf("PermissionMode = %v, want nil", o.PermissionMode)
				}
				if o.CanUseTool != nil {
					t.Errorf("CanUseTool set, want nil")
				}
			},
		},
		{
			name: "question tool registered",
			spec: SessionSpec{
				Question: func(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
					return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionDecline}
				},
			},
			check: func(t *testing.T, o *claudecode.Options) {
				if _, ok := o.McpServers["jig"]; !ok {
					t.Errorf("McpServers = %v, want a \"jig\" entry", o.McpServers)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := claudeOptions(tt.spec)
			o := claudecode.NewOptions(opts...)
			tt.check(t, o)
		})
	}
}

func TestClaudeOptionsPermissionCallbackDecision(t *testing.T) {
	tests := []struct {
		name   string
		decide Decision
	}{
		{"allow", Decision{Allow: true}},
		{"deny", Decision{Allow: false, Reason: "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := SessionSpec{Permission: func(string, map[string]any) Decision { return tt.decide }}
			opts := claudeOptions(spec)
			o := claudecode.NewOptions(opts...)
			if o.CanUseTool == nil {
				t.Fatalf("CanUseTool not set")
			}
			res, err := o.CanUseTool(context.Background(), "Bash", nil, claudecode.ToolPermissionContext{})
			if err != nil {
				t.Fatalf("CanUseTool() error = %v", err)
			}
			if res == nil {
				t.Fatalf("CanUseTool() returned nil result")
			}
		})
	}
}

func TestClaudeQuestionTranslation(t *testing.T) {
	req, prompts, err := parseClaudeQuestions(map[string]any{
		"questions": []any{
			map[string]any{
				"header":      "Format",
				"question":    "Choose a format",
				"multiSelect": false,
				"options": []any{
					map[string]any{"label": "JSON", "description": "Structured"},
					map[string]any{"label": "Text", "description": "Plain"},
				},
			},
			map[string]any{
				"question":    "Choose features",
				"multiSelect": true,
				"options": []any{
					map[string]any{"label": "Cache"},
					map[string]any{"label": "Retry"},
				},
			},
		},
	}, 7)
	if err != nil {
		t.Fatalf("parseClaudeQuestions() error = %v", err)
	}
	if req.ID != "claude-question-7" || len(req.Fields) != 2 {
		t.Fatalf("request = %+v", req)
	}
	if req.Fields[0].Kind != interaction.FieldSingleSelect || req.Fields[1].Kind != interaction.FieldMultiSelect {
		t.Fatalf("field kinds = %q, %q", req.Fields[0].Kind, req.Fields[1].Kind)
	}

	content, isError := encodeClaudeQuestionResponse(req, prompts, interaction.QuestionResponse{
		RequestID: req.ID,
		Action:    interaction.ActionAccept,
		Answers: map[string]interaction.Answer{
			"question_0": {Custom: "YAML"},
			"question_1": {Values: []string{"Cache", "Retry"}},
		},
	})
	if isError {
		t.Fatalf("encodeClaudeQuestionResponse() returned error content %q", content)
	}
	if content != `{"answers":{"Choose a format":"YAML","Choose features":"Cache, Retry"}}` {
		t.Fatalf("content = %s", content)
	}
}

func TestClaudeQuestionDeclineAndCancel(t *testing.T) {
	req := interaction.QuestionRequest{
		ID: "q1",
		Fields: []interaction.QuestionField{{
			ID: "answer", Prompt: "Answer?", Kind: interaction.FieldText,
		}},
	}
	declined, isError := encodeClaudeQuestionResponse(req, []string{"Answer?"}, interaction.QuestionResponse{
		RequestID: "q1", Action: interaction.ActionDecline,
	})
	if isError || declined != `{"answers":{}}` {
		t.Fatalf("decline = %q, error=%v", declined, isError)
	}
	cancelled, isError := encodeClaudeQuestionResponse(req, []string{"Answer?"}, interaction.QuestionResponse{
		RequestID: "q1", Action: interaction.ActionCancel,
	})
	if !isError || cancelled != "user cancelled the question" {
		t.Fatalf("cancel = %q, error=%v", cancelled, isError)
	}
}

// runClaudeSessionPumpBatch drives pump over one batch of messages against
// an existing session and returns the Events it emitted. Each call gets its
// own events channel (pump closes it on return) while s.tools/s.pending
// persist across calls, letting a test interleave on-disk file mutations
// between the ToolUseBlock batch and the ToolResultBlock batch the way the
// CLI interleaves tool execution between those two streamed messages.
func runClaudeSessionPumpBatch(t *testing.T, s *claudeSession, msgs []claudecode.Message) []Event {
	t.Helper()
	s.events = make(chan Event, 16)
	msgChan := make(chan claudecode.Message, len(msgs))
	for _, m := range msgs {
		msgChan <- m
	}
	close(msgChan)
	s.pump(msgChan)
	var out []Event
	for e := range s.events {
		out = append(out, e)
	}
	return out
}

func findToolResultDiff(t *testing.T, events []Event) *toolcall.Diff {
	t.Helper()
	for _, e := range events {
		if e.Type != EventToolResult || e.Tool == nil {
			continue
		}
		for _, c := range e.Tool.Content {
			if c.Diff != nil {
				return c.Diff
			}
		}
	}
	return nil
}

func TestClaudeSessionPumpSynthesizesEditDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	s := &claudeSession{tools: make(map[string]*toolcall.Activity), pending: make(map[string]pendingEditDiff)}
	toolUse := runClaudeSessionPumpBatch(t, s, []claudecode.Message{
		&claudecode.AssistantMessage{
			Content: []claudecode.ContentBlock{&claudecode.ToolUseBlock{
				ToolUseID: "t1",
				Name:      "Edit",
				Input: map[string]any{
					"file_path":  path,
					"old_string": "old",
					"new_string": "new",
				},
			}},
		},
	})

	// Simulate the CLI applying the edit between the streamed tool_use and
	// tool_result messages.
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("apply edit: %v", err)
	}

	toolResult := runClaudeSessionPumpBatch(t, s, []claudecode.Message{
		&claudecode.UserMessage{
			Content: []claudecode.ContentBlock{&claudecode.ToolResultBlock{
				ToolUseID: "t1",
				Content:   "OK",
			}},
		},
	})

	diff := findToolResultDiff(t, append(toolUse, toolResult...))
	if diff == nil {
		t.Fatalf("expected a synthesized Diff on the tool result")
	}
	if diff.Path != path {
		t.Errorf("Diff.Path = %q, want %q", diff.Path, path)
	}
	if diff.OldText == nil || *diff.OldText != "old\n" {
		t.Errorf("Diff.OldText = %v, want %q", diff.OldText, "old\n")
	}
	if diff.NewText != "new\n" {
		t.Errorf("Diff.NewText = %q, want %q", diff.NewText, "new\n")
	}
}

func TestClaudeSessionPumpSynthesizesWriteDiffAsFileCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new_file.go")

	s := &claudeSession{tools: make(map[string]*toolcall.Activity), pending: make(map[string]pendingEditDiff)}
	toolUse := runClaudeSessionPumpBatch(t, s, []claudecode.Message{
		&claudecode.AssistantMessage{
			Content: []claudecode.ContentBlock{&claudecode.ToolUseBlock{
				ToolUseID: "t1",
				Name:      "Write",
				Input: map[string]any{
					"file_path": path,
					"content":   "package foo\n",
				},
			}},
		},
	})

	if err := os.WriteFile(path, []byte("package foo\n"), 0o644); err != nil {
		t.Fatalf("apply write: %v", err)
	}

	toolResult := runClaudeSessionPumpBatch(t, s, []claudecode.Message{
		&claudecode.UserMessage{
			Content: []claudecode.ContentBlock{&claudecode.ToolResultBlock{
				ToolUseID: "t1",
				Content:   "OK",
			}},
		},
	})

	diff := findToolResultDiff(t, append(toolUse, toolResult...))
	if diff == nil {
		t.Fatalf("expected a synthesized Diff on the tool result")
	}
	if diff.OldText != nil {
		t.Errorf("Diff.OldText = %v, want nil (file creation)", *diff.OldText)
	}
	if diff.NewText != "package foo\n" {
		t.Errorf("Diff.NewText = %q, want %q", diff.NewText, "package foo\n")
	}
}

func TestClaudeSessionPumpSkipsDiffOnToolError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	s := &claudeSession{tools: make(map[string]*toolcall.Activity), pending: make(map[string]pendingEditDiff)}
	toolUse := runClaudeSessionPumpBatch(t, s, []claudecode.Message{
		&claudecode.AssistantMessage{
			Content: []claudecode.ContentBlock{&claudecode.ToolUseBlock{
				ToolUseID: "t1",
				Name:      "Edit",
				Input: map[string]any{
					"file_path":  path,
					"old_string": "old",
					"new_string": "new",
				},
			}},
		},
	})
	isError := true
	toolResult := runClaudeSessionPumpBatch(t, s, []claudecode.Message{
		&claudecode.UserMessage{
			Content: []claudecode.ContentBlock{&claudecode.ToolResultBlock{
				ToolUseID: "t1",
				Content:   "old_string not found",
				IsError:   &isError,
			}},
		},
	})

	if diff := findToolResultDiff(t, append(toolUse, toolResult...)); diff != nil {
		t.Errorf("expected no Diff on a failed tool call, got %+v", diff)
	}
	if _, pending := s.pending["t1"]; pending {
		t.Errorf("expected pending snapshot for t1 to be cleared after the tool result")
	}
}
