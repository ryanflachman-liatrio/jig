package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jig/internal/agentcfg"
)

const agentWFHeader = `
[workflow]
name = "agents"
version = "1"
`

// agentStep renders one agent step with the given extra body.
func agentStep(body string) string {
	return agentWFHeader + `
[[step]]
id = "research"
type = "agent"
skill = "skills/s"
` + body
}

// loadAgentFixture writes a workflow, an optional profile file and the skill
// it uses into a temp dir, then loads it.
func loadAgentFixture(t *testing.T, profiles, wf string) (*Workflow, error) {
	t.Helper()
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills", "s", "SKILL.md"), "Do the thing.")
	if profiles != "" {
		mustWrite(t, filepath.Join(dir, ".agents", "jig", "profiles", "p.toml"), profiles)
	}
	path := filepath.Join(dir, "wf.toml")
	mustWrite(t, path, wf)
	return Load(path)
}

func resolvedAgent(t *testing.T, wf *Workflow, id string) agentcfg.Agent {
	t.Helper()
	s := wf.Steps[wf.index[id]]
	a := s.ResolvedAgent()
	if a == nil {
		t.Fatalf("step %q has no resolved agent", id)
	}
	return a
}

func TestAgentUnion(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    agentcfg.Agent
		wantErr string
	}{
		{
			name: "claude keys decode",
			body: `[step.agent]
backend = "claude"
model = "m"
effort = "high"
tools = ["Read", "Grep"]
disallowed_tools = ["Bash"]
permission_mode = "plan"
max_turns = 3
max_budget_usd = 1.5
max_thinking_tokens = 100
fallback_model = "f"
append_system_prompt = "be brief"
`,
			want: agentcfg.ClaudeAgent{
				Common: agentcfg.Common{Model: "m", AppendSystemPrompt: "be brief"},
				Effort: "high", Tools: []string{"Read", "Grep"}, DisallowedTools: []string{"Bash"},
				PermissionMode: "plan", MaxTurns: 3, MaxBudgetUSD: 1.5, MaxThinkingTokens: 100, FallbackModel: "f",
			},
		},
		{
			name: "codex keys decode",
			body: `[step.agent]
backend = "codex"
effort = "low"
mode = "read-only"
collaboration_mode = "plan"
`,
			want: agentcfg.CodexAgent{Effort: "low", Mode: "read-only", CollaborationMode: "plan"},
		},
		{
			name: "cursor keys decode",
			body: `[step.agent]
backend = "cursor"
mode = "ask"
`,
			want: agentcfg.CursorAgent{Mode: "ask"},
		},
		{
			name:    "cross-backend key",
			body:    "[step.agent]\nbackend = \"codex\"\ntools = [\"Read\"]\n",
			wantErr: `step "research": codex agent has unknown key "tools"`,
		},
		{
			name:    "cursor rejects effort",
			body:    "[step.agent]\nbackend = \"cursor\"\neffort = \"low\"\n",
			wantErr: `cursor agent has unknown key "effort"`,
		},
		{
			name:    "claude rejects mode",
			body:    "[step.agent]\nmode = \"agent\"\n",
			wantErr: `claude agent has unknown key "mode"`,
		},
		{
			name:    "typo key",
			body:    "[step.agent]\nbackend = \"claude\"\ntols = [\"Read\"]\n",
			wantErr: `claude agent has unknown key "tols"`,
		},
		{
			name:    "bad backend",
			body:    "[step.agent]\nbackend = \"gemini\"\n",
			wantErr: `invalid backend "gemini" (want claude|codex|cursor)`,
		},
		{
			name:    "bad claude mode",
			body:    "[step.agent]\npermission_mode = \"yolo\"\n",
			wantErr: `claude agent has invalid permission_mode "yolo" (want default|acceptEdits|plan|dontAsk|bypassPermissions)`,
		},
		{
			name:    "bad codex mode",
			body:    "[step.agent]\nbackend = \"codex\"\nmode = \"full\"\n",
			wantErr: `codex agent has invalid mode "full" (want read-only|agent|agent-full-access)`,
		},
		{
			name:    "bad cursor mode",
			body:    "[step.agent]\nbackend = \"cursor\"\nmode = \"read-only\"\n",
			wantErr: `cursor agent has invalid mode "read-only" (want agent|plan|ask)`,
		},
		{
			name:    "ask user in tools",
			body:    "[step.agent]\ntools = [\"Read\", \"AskUserQuestion\"]\n",
			wantErr: `tools must not list "AskUserQuestion"; set ask_user on the step instead`,
		},
		{
			name:    "ask user in disallowed tools",
			body:    "[step.agent]\ndisallowed_tools = [\"AskUserQuestion\"]\n",
			wantErr: `disallowed_tools must not list "AskUserQuestion"`,
		},
		{
			name:    "unknown claude tool",
			body:    "[step.agent]\ntools = [\"Reed\"]\n",
			wantErr: `unknown tool "Reed" (want bare names: Agent, Bash,`,
		},
		{
			name:    "rule syntax",
			body:    "[step.agent]\ndisallowed_tools = [\"Bash(rm:*)\"]\n",
			wantErr: `unknown tool "Bash(rm:*)"`,
		},
		{
			name:    "wrong value type",
			body:    "[step.agent]\nmax_turns = \"three\"\n",
			wantErr: "agent `max_turns` must be an integer",
		},
		{
			name: "omitted tools",
			body: "[step.agent]\nbackend = \"claude\"\n",
			want: agentcfg.ClaudeAgent{},
		},
		{
			name: "explicit empty tools",
			body: "[step.agent]\ntools = []\n",
			want: agentcfg.ClaudeAgent{Tools: []string{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, err := Decode(agentStep(tt.body), "")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			got := resolvedAgent(t, wf, "research")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("agent = %#v, want %#v", got, tt.want)
			}
		})
	}

	t.Run("omitted and empty tools differ", func(t *testing.T) {
		omitted, _ := Decode(agentStep("[step.agent]\nbackend = \"claude\"\n"), "")
		empty, _ := Decode(agentStep("[step.agent]\ntools = []\n"), "")
		o := resolvedAgent(t, omitted, "research").(agentcfg.ClaudeAgent)
		e := resolvedAgent(t, empty, "research").(agentcfg.ClaudeAgent)
		if o.Tools != nil || e.Tools == nil || len(e.Tools) != 0 {
			t.Fatalf("omitted tools = %#v, empty tools = %#v", o.Tools, e.Tools)
		}
	})

	t.Run("ask_user in defaults is unknown", func(t *testing.T) {
		_, err := Decode(agentWFHeader+"[defaults]\nask_user = true\n"+`
[[step]]
id = "research"
type = "agent"
skill = "skills/s"
`, "")
		if err == nil || !strings.Contains(err.Error(), "unknown key(s) in workflow: defaults.ask_user") {
			t.Fatalf("error = %v, want unknown defaults.ask_user", err)
		}
	})
}

const extendsProfiles = `
[[agent]]
id = "@reader"
backend = "claude"
model = "base-model"
tools = ["Read", "Grep"]
max_turns = 5

[[agent]]
id = "@narrow"
extends = "@reader"
tools = ["Read"]

[[agent]]
id = "@codex-ro"
backend = "codex"
mode = "read-only"
effort = "low"

[[agent]]
id = "@loop-a"
extends = "@loop-b"

[[agent]]
id = "@loop-b"
extends = "@loop-a"
`

func TestAgentExtends(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    agentcfg.Agent
		wantErr string
	}{
		{
			name: "profile reference",
			body: `agent = "@reader"`,
			want: agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "base-model"}, Tools: []string{"Read", "Grep"}, MaxTurns: 5},
		},
		{
			name: "profile extends profile, list replaces",
			body: `agent = "@narrow"`,
			want: agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "base-model"}, Tools: []string{"Read"}, MaxTurns: 5},
		},
		{
			name: "inline child wins and inherits backend",
			body: "[step.agent]\nextends = \"@reader\"\nmodel = \"child-model\"\n",
			want: agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "child-model"}, Tools: []string{"Read", "Grep"}, MaxTurns: 5},
		},
		{
			name: "explicit empty list replaces parent list",
			body: "[step.agent]\nextends = \"@reader\"\ntools = []\n",
			want: agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "base-model"}, Tools: []string{}, MaxTurns: 5},
		},
		{
			name: "codex backend inherited",
			body: "[step.agent]\nextends = \"@codex-ro\"\neffort = \"high\"\n",
			want: agentcfg.CodexAgent{Effort: "high", Mode: "read-only"},
		},
		{
			name:    "inherited backend still checks keys",
			body:    "[step.agent]\nextends = \"@codex-ro\"\ntools = [\"Read\"]\n",
			wantErr: `codex agent has unknown key "tools"`,
		},
		{
			name:    "backend mismatch",
			body:    "[step.agent]\nextends = \"@codex-ro\"\nbackend = \"cursor\"\n",
			wantErr: `backend "cursor" does not match "codex" from extends "@codex-ro"`,
		},
		{
			name:    "unknown parent",
			body:    "[step.agent]\nextends = \"@missing\"\n",
			wantErr: `unknown agent profile "@missing"`,
		},
		{
			name:    "cycle",
			body:    `agent = "@loop-a"`,
			wantErr: "extends cycle: @loop-a -> @loop-b -> @loop-a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, err := loadAgentFixture(t, extendsProfiles, agentStep(tt.body))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := resolvedAgent(t, wf, "research"); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("agent = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAgentResolution(t *testing.T) {
	const profiles = `
[[agent]]
id = "@codex-ro"
backend = "codex"
mode = "read-only"
`
	tests := []struct {
		name      string
		wf        string
		agentFile string
		want      agentcfg.Agent
		wantErr   string
	}{
		{
			name: "string agent",
			wf:   agentStep(`agent = "@codex-ro"`),
			want: agentcfg.CodexAgent{Mode: "read-only"},
		},
		{
			name: "table agent",
			wf:   agentStep("[step.agent]\nbackend = \"cursor\"\nmode = \"plan\"\n"),
			want: agentcfg.CursorAgent{Mode: "plan"},
		},
		{
			name: "defaults agent taken whole",
			wf: agentWFHeader + "[defaults.agent]\nbackend = \"claude\"\nmodel = \"d\"\ntools = [\"Read\"]\n" + `
[[step]]
id = "research"
type = "agent"
skill = "skills/s"
`,
			want: agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "d"}, Tools: []string{"Read"}},
		},
		{
			name: "step agent wins whole over defaults",
			wf: agentWFHeader + "[defaults.agent]\nmodel = \"d\"\ntools = [\"Read\"]\n" + `
[[step]]
id = "research"
type = "agent"
skill = "skills/s"
agent = "@codex-ro"
`,
			want: agentcfg.CodexAgent{Mode: "read-only"},
		},
		{
			name: "implicit claude default",
			wf:   agentStep(""),
			want: agentcfg.ClaudeAgent{},
		},
		{
			name:      "agent_file is the base layer",
			wf:        agentWFHeader + "[[step]]\nid = \"research\"\ntype = \"agent\"\nagent_file = \"a.md\"\n",
			agentFile: "---\nname: a\ntools: Read, Grep\nmodel: file-model\n---\nPrompt.\n",
			want:      agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "file-model"}, Tools: []string{"Read", "Grep"}},
		},
		{
			name:      "step wins over agent_file",
			wf:        agentWFHeader + "[[step]]\nid = \"research\"\ntype = \"agent\"\nagent_file = \"a.md\"\n[step.agent]\ntools = [\"Read\"]\n",
			agentFile: "---\nname: a\ntools: Read, Grep\nmodel: file-model\n---\nPrompt.\n",
			want:      agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "file-model"}, Tools: []string{"Read"}},
		},
		{
			name:      "agent_file rejects non-claude agent",
			wf:        agentWFHeader + "[[step]]\nid = \"research\"\ntype = \"agent\"\nagent_file = \"a.md\"\nagent = \"@codex-ro\"\n",
			agentFile: "---\nname: a\n---\nPrompt.\n",
			wantErr:   `step "research": agent_file requires a claude agent, got codex`,
		},
		{
			name:    "ask_user on command step",
			wf:      agentWFHeader + "[[step]]\nid = \"c\"\ntype = \"command\"\nrun = \"true\"\nask_user = true\n",
			wantErr: "step \"c\": `ask_user` is only valid on agent steps",
		},
		{
			name:    "agent on command step",
			wf:      agentWFHeader + "[[step]]\nid = \"c\"\ntype = \"command\"\nrun = \"true\"\nagent = \"@codex-ro\"\n",
			wantErr: "step \"c\": `agent` is only valid on agent steps",
		},
		{
			name:    "agent string without @",
			wf:      agentStep(`agent = "codex"`),
			wantErr: `agent "codex" must be a profile reference starting with '@'`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWriteSkill(t, filepath.Join(dir, "skills", "s", "SKILL.md"), "Do the thing.")
			mustWrite(t, filepath.Join(dir, ".agents", "jig", "profiles", "p.toml"), profiles)
			if tt.agentFile != "" {
				mustWrite(t, filepath.Join(dir, "a.md"), tt.agentFile)
			}
			path := filepath.Join(dir, "wf.toml")
			mustWrite(t, path, tt.wf)
			wf, err := Load(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := resolvedAgent(t, wf, "research"); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("agent = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSnapshotAgentJSON(t *testing.T) {
	agents := map[string]agentcfg.Agent{
		"claude omitted tools": agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "m"}, PermissionMode: "plan", MaxTurns: 2},
		"claude empty tools":   agentcfg.ClaudeAgent{Tools: []string{}, DisallowedTools: []string{"Bash"}},
		"codex":                agentcfg.CodexAgent{Mode: "read-only", CollaborationMode: "plan", Effort: "low"},
		"cursor":               agentcfg.CursorAgent{Common: agentcfg.Common{AppendSystemPrompt: "p"}, Mode: "ask"},
	}
	for name, agent := range agents {
		t.Run(name, func(t *testing.T) {
			in := Step{ID: "s", Type: StepAgent, SnapshotAgent: &AgentSnapshot{Agent: agent}}
			data, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"backend":"`+agent.Backend()+`"`) {
				t.Fatalf("snapshot JSON lacks backend discriminator: %s", data)
			}
			var out Step
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			if got := out.ResolvedAgent(); !reflect.DeepEqual(got, agent) {
				t.Fatalf("round trip = %#v, want %#v", got, agent)
			}
		})
	}
	t.Run("unknown backend rejected", func(t *testing.T) {
		var out Step
		err := json.Unmarshal([]byte(`{"ID":"s","snapshot_agent":{"backend":"gemini"}}`), &out)
		if err == nil || !strings.Contains(err.Error(), `unknown backend "gemini"`) {
			t.Fatalf("error = %v, want unknown backend", err)
		}
	})
}

func TestProfileDiscoveryRoot(t *testing.T) {
	const profile = "[[agent]]\nid = \"@ro\"\nbackend = \"cursor\"\nmode = \"ask\"\n"
	wfBody := agentStep(`agent = "@ro"`)

	t.Run("git root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		wfDir := filepath.Join(root, ".agents", "jig")
		mustWrite(t, filepath.Join(wfDir, "profiles", "p.toml"), profile)
		mustWriteSkill(t, filepath.Join(wfDir, "skills", "s", "SKILL.md"), "Do the thing.")
		path := filepath.Join(wfDir, "wf.toml")
		mustWrite(t, path, wfBody)
		wf, err := Load(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got := resolvedAgent(t, wf, "research"); !reflect.DeepEqual(got, agentcfg.CursorAgent{Mode: "ask"}) {
			t.Fatalf("agent = %#v", got)
		}
	})

	t.Run("non-git fallback", func(t *testing.T) {
		wf, err := loadAgentFixture(t, profile, wfBody)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got := resolvedAgent(t, wf, "research"); !reflect.DeepEqual(got, agentcfg.CursorAgent{Mode: "ask"}) {
			t.Fatalf("agent = %#v", got)
		}
	})
}
