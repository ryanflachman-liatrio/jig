package agentcfg

import (
	"reflect"
	"strings"
	"testing"
)

func TestModeTables(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"claude permission_mode", ClaudePermissionModes, []string{"default", "acceptEdits", "plan", "dontAsk", "bypassPermissions"}},
		{"codex mode", CodexModes, []string{"read-only", "agent", "agent-full-access"}},
		{"codex collaboration_mode", CodexCollaborationMode, []string{"default", "plan"}},
		{"cursor mode", CursorModes, []string{"agent", "plan", "ask"}},
		{"plan read-only tools", PlanReadOnlyTools, []string{"Read", "Grep", "Glob", "WebSearch", "WebFetch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Fatalf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		agent   Agent
		wantErr string
	}{
		{"claude zero value", ClaudeAgent{}, ""},
		{"claude every mode", ClaudeAgent{PermissionMode: "dontAsk", Effort: "high"}, ""},
		{"claude bad mode", ClaudeAgent{PermissionMode: "yolo"}, `claude agent has invalid permission_mode "yolo" (want default|acceptEdits|plan|dontAsk|bypassPermissions)`},
		{"claude bad effort", ClaudeAgent{Effort: "huge"}, `invalid effort "huge"`},
		{"claude negative limit", ClaudeAgent{MaxTurns: -1}, "must be >= 0"},
		{"claude ask user in tools", ClaudeAgent{Tools: []string{"Read", "AskUserQuestion"}}, `tools must not list "AskUserQuestion"`},
		{"claude ask user in disallowed", ClaudeAgent{DisallowedTools: []string{"AskUserQuestion"}}, `disallowed_tools must not list "AskUserQuestion"`},
		{"claude unknown tool", ClaudeAgent{Tools: []string{"Reed"}}, `unknown tool "Reed" (want bare names: Agent,`},
		{"claude rule syntax", ClaudeAgent{DisallowedTools: []string{"Bash(rm:*)"}}, `unknown tool "Bash(rm:*)"`},
		{"codex valid", CodexAgent{Mode: "read-only", CollaborationMode: "plan", Effort: "low"}, ""},
		{"codex bad mode", CodexAgent{Mode: "full"}, `codex agent has invalid mode "full" (want read-only|agent|agent-full-access)`},
		{"codex bad collaboration", CodexAgent{CollaborationMode: "pair"}, `codex agent has invalid collaboration_mode "pair" (want default|plan)`},
		{"cursor valid", CursorAgent{Mode: "ask"}, ""},
		{"cursor bad mode", CursorAgent{Mode: "read-only"}, `cursor agent has invalid mode "read-only" (want agent|plan|ask)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.agent)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidClaudeTool(t *testing.T) {
	for _, name := range []string{"Read", "Bash", "WebFetch", "Edit", "Write", "Grep", "Glob"} {
		if !ValidClaudeTool(name) {
			t.Errorf("ValidClaudeTool(%q) = false", name)
		}
	}
	for _, name := range []string{"", "Reed", "Bash(rm:*)", "read", AskUserQuestion} {
		if ValidClaudeTool(name) {
			t.Errorf("ValidClaudeTool(%q) = true", name)
		}
	}
}

func TestNeverPrompts(t *testing.T) {
	tests := []struct {
		name  string
		agent Agent
		want  bool
	}{
		{"claude default", ClaudeAgent{}, false},
		{"claude dontAsk", ClaudeAgent{PermissionMode: "dontAsk"}, false},
		{"claude bypass", ClaudeAgent{PermissionMode: "bypassPermissions"}, true},
		{"codex agent", CodexAgent{Mode: "agent"}, false},
		{"codex full access", CodexAgent{Mode: "agent-full-access"}, true},
		{"cursor agent", CursorAgent{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeverPrompts(tt.agent); got != tt.want {
				t.Fatalf("NeverPrompts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsMutating(t *testing.T) {
	tests := []struct {
		name  string
		agent Agent
		want  bool
	}{
		{"claude omitted tools", ClaudeAgent{}, true},
		{"claude empty tools", ClaudeAgent{Tools: []string{}}, false},
		{"claude read only", ClaudeAgent{Tools: []string{"Read", "Grep"}}, false},
		{"claude with edit", ClaudeAgent{Tools: []string{"Read", "Edit"}}, true},
		{"claude with bash", ClaudeAgent{Tools: []string{"Bash"}}, true},
		{"claude plan", ClaudeAgent{PermissionMode: "plan"}, false},
		{"claude dontAsk omitted tools", ClaudeAgent{PermissionMode: "dontAsk"}, true},
		{"claude dontAsk read only", ClaudeAgent{PermissionMode: "dontAsk", Tools: []string{"Read"}}, false},
		{"codex unset", CodexAgent{}, true},
		{"codex read-only", CodexAgent{Mode: "read-only"}, false},
		{"codex full access", CodexAgent{Mode: "agent-full-access"}, true},
		{"cursor unset", CursorAgent{}, true},
		{"cursor agent", CursorAgent{Mode: "agent"}, true},
		{"cursor plan", CursorAgent{Mode: "plan"}, false},
		{"cursor ask", CursorAgent{Mode: "ask"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMutating(tt.agent); got != tt.want {
				t.Fatalf("IsMutating = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPosture(t *testing.T) {
	tests := []struct {
		name  string
		agent Agent
		want  map[string]string
	}{
		{"claude defaults", ClaudeAgent{}, map[string]string{"backend": "claude", "permission_mode": "default", "tools": "*"}},
		{"claude restricted", ClaudeAgent{Tools: []string{"Read", "Grep"}, DisallowedTools: []string{"Bash"}, PermissionMode: "plan"},
			map[string]string{"backend": "claude", "permission_mode": "plan", "tools": "Read,Grep", "disallowed_tools": "Bash"}},
		{"claude empty tools", ClaudeAgent{Tools: []string{}}, map[string]string{"backend": "claude", "permission_mode": "default", "tools": ""}},
		{"codex defaults", CodexAgent{}, map[string]string{"backend": "codex", "mode": "agent", "collaboration_mode": "default"}},
		{"codex set", CodexAgent{Mode: "read-only", CollaborationMode: "plan"}, map[string]string{"backend": "codex", "mode": "read-only", "collaboration_mode": "plan"}},
		{"cursor defaults", CursorAgent{}, map[string]string{"backend": "cursor", "mode": "agent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Posture(tt.agent); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Posture = %v, want %v", got, tt.want)
			}
		})
	}
}
