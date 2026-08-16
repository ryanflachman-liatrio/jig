package harness

import (
	"context"
	"testing"

	claudecode "github.com/severity1/claude-agent-sdk-go"
)

func TestClaudeHarnessCapabilities(t *testing.T) {
	h := NewClaudeHarness()
	if h.Name() != "claude" {
		t.Fatalf("Name() = %q, want %q", h.Name(), "claude")
	}
	caps := h.Capabilities()
	for _, c := range []Capability{
		CapPermissionCallback,
		CapInProcessMCP,
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
			name: "mcp server registered",
			spec: SessionSpec{
				MCPServers: []MCPServer{{
					Name:    "jig",
					Version: "1.0.0",
					Tools: []Tool{{
						Name:        "AskUserQuestion",
						Description: "ask",
						InputSchema: map[string]any{"type": "object"},
						Handler: func(context.Context, map[string]any) (ToolResult, error) {
							return ToolResult{}, nil
						},
					}},
				}},
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
