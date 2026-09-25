package runner

import (
	"reflect"
	"testing"

	"jig/internal/agentcfg"
	"jig/internal/workflow"
)

// TestIntegrationResolverAgent pins the resolver's per-backend agent: it runs
// on the conflicted step's backend and model and can write to the worktree.
func TestIntegrationResolverAgent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent agentcfg.Agent
		want  agentcfg.Agent
	}{
		{
			name:  "claude keeps a narrow write-capable tool list with acceptEdits",
			agent: agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "haiku"}, Tools: []string{"Read"}, PermissionMode: "plan"},
			want: agentcfg.ClaudeAgent{
				Common:         agentcfg.Common{Model: "haiku"},
				Tools:          []string{"Read", "Grep", "Glob", "Write", "Edit", "Bash"},
				PermissionMode: "acceptEdits",
			},
		},
		{
			name:  "codex runs in agent mode on the conflicted model",
			agent: agentcfg.CodexAgent{Common: agentcfg.Common{Model: "fixture-model"}, Mode: "read-only", CollaborationMode: "plan"},
			want:  agentcfg.CodexAgent{Common: agentcfg.Common{Model: "fixture-model"}, Mode: "agent"},
		},
		{
			name:  "cursor runs in agent mode on the conflicted model",
			agent: agentcfg.CursorAgent{Common: agentcfg.Common{Model: "fixture-model"}, Mode: "ask"},
			want:  agentcfg.CursorAgent{Common: agentcfg.Common{Model: "fixture-model"}, Mode: "agent"},
		},
		{
			name: "a step without an agent defaults to claude",
			want: agentcfg.ClaudeAgent{
				Tools:          []string{"Read", "Grep", "Glob", "Write", "Edit", "Bash"},
				PermissionMode: "acceptEdits",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &workflow.Step{ID: "conflicted", Type: workflow.StepAgent}
			if tc.agent != nil {
				st.SnapshotAgent = &workflow.AgentSnapshot{Agent: tc.agent}
			}
			if got := resolverAgent(st); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolverAgent() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
