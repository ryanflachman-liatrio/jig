package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jig/internal/agentcfg"
	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/workflow"
)

const agentSnapshotTOML = `
[workflow]
name = "agents"
version = "1"

[[step]]
id = "claude"
type = "agent"
skill = "s"
  [step.agent]
  model = "claude-fixture"
  tools = ["Read", "Grep"]
  disallowed_tools = ["Bash"]
  permission_mode = "plan"

[[step]]
id = "codex"
type = "agent"
skill = "s"
  [step.agent]
  backend = "codex"
  model = "codex-fixture"
  mode = "read-only"
  collaboration_mode = "plan"

[[step]]
id = "cursor"
type = "agent"
skill = "s"
agent = { backend = "cursor", mode = "ask" }
`

func loadAgentSnapshotFixture(t *testing.T) *workflow.Workflow {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.toml")
	if err := os.WriteFile(path, []byte(agentSnapshotTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s", "SKILL.md"), []byte("---\nname: s\ndescription: fixture\n---\nDo it."), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return wf
}

func TestWorkflowSnapshotAgentRoundTrip(t *testing.T) {
	wf := loadAgentSnapshotFixture(t)
	runDir := t.TempDir()
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatalf("persist: %v", err)
	}
	restored, err := loadWorkflowSnapshot(runDir)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	want := map[string]agentcfg.Agent{
		"claude": agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "claude-fixture"}, Tools: []string{"Read", "Grep"}, DisallowedTools: []string{"Bash"}, PermissionMode: "plan"},
		"codex":  agentcfg.CodexAgent{Common: agentcfg.Common{Model: "codex-fixture"}, Mode: "read-only", CollaborationMode: "plan"},
		"cursor": agentcfg.CursorAgent{Mode: "ask"},
	}
	for id, agent := range want {
		orig := wf.Steps[indexOf(t, wf, id)].ResolvedAgent()
		if !reflect.DeepEqual(orig, agent) {
			t.Fatalf("%s loaded agent = %#v, want %#v", id, orig, agent)
		}
		st := restored.Steps[indexOf(t, restored, id)]
		if got := st.ResolvedAgent(); !reflect.DeepEqual(got, agent) {
			t.Errorf("%s restored agent = %#v, want %#v", id, got, agent)
		}
		if st.Backend != agent.Backend() || st.Model != agent.Base().Model {
			t.Errorf("%s restored Backend/Model = %q/%q, want %q/%q", id, st.Backend, st.Model, agent.Backend(), agent.Base().Model)
		}
		if st.Isolation != workflow.IsolationNone {
			t.Errorf("%s restored isolation = %q, want none (non-mutating agent)", id, st.Isolation)
		}
	}
}

func TestWorkflowSnapshotRejectsPreAgentSchema(t *testing.T) {
	const toml = "# pre-agent workflow\n"
	data := []byte(`{
  "sha256": "` + sha256Hex(toml) + `",
  "toml": "# pre-agent workflow\n",
  "meta": {"Name": "old", "Version": "1"},
  "expanded_steps": [{"ID": "worker", "Type": "agent", "Skill": "s", "Backend": "claude", "AllowedTools": ["Read"]}]
}`)
	runDir := t.TempDir()
	if err := os.WriteFile(datastore.WorkflowSnapshotPath(runDir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := loadWorkflowSnapshot(runDir)
	if err == nil {
		t.Fatalf("restore succeeded with agent %#v, want pre-agent schema error", wf.Steps[0].ResolvedAgent())
	}
	if !strings.Contains(err.Error(), `workflow snapshot predates the agent schema (step "worker"); start a new run`) {
		t.Fatalf("error = %v", err)
	}
}

func TestStepTerminalAgentFields(t *testing.T) {
	wf := loadAgentSnapshotFixture(t)
	s := &scheduler{wf: wf, states: map[string]*step.State{}}
	for _, st := range wf.Steps {
		s.states[st.ID] = &step.State{}
	}
	term := func(id string) (backend, model string, tools []string, posture map[string]string) {
		t.Helper()
		m := s.terminalManifest(StepStatus{StepID: id, From: step.StatusRunning, To: step.StatusSucceeded})
		if m == nil {
			t.Fatalf("%s: no terminal manifest", id)
		}
		return m.Backend, m.Model, m.ToolPolicy, m.AgentPosture
	}

	backend, model, tools, posture := term("claude")
	if backend != "claude" || model != "claude-fixture" {
		t.Errorf("claude Backend/Model = %q/%q", backend, model)
	}
	if !reflect.DeepEqual(tools, []string{"Read", "Grep", "Bash"}) {
		t.Errorf("claude ToolPolicy = %v", tools)
	}
	if posture["permission_mode"] != "plan" || posture["tools"] != "Read,Grep" || posture["disallowed_tools"] != "Bash" {
		t.Errorf("claude posture = %v", posture)
	}

	backend, model, tools, posture = term("codex")
	if backend != "codex" || model != "codex-fixture" || len(tools) != 0 {
		t.Errorf("codex Backend/Model/ToolPolicy = %q/%q/%v", backend, model, tools)
	}
	if posture["mode"] != "read-only" || posture["collaboration_mode"] != "plan" {
		t.Errorf("codex posture = %v", posture)
	}

	// An unset Codex mode reports the applied default.
	wf.Steps[indexOf(t, wf, "codex")].SnapshotAgent = &workflow.AgentSnapshot{Agent: agentcfg.CodexAgent{Common: agentcfg.Common{Model: "codex-fixture"}}}
	if _, _, _, posture = term("codex"); posture["mode"] != "agent" {
		t.Errorf("codex default posture = %v, want mode=agent", posture)
	}
}

func indexOf(t *testing.T, wf *workflow.Workflow, id string) int {
	t.Helper()
	for i, st := range wf.Steps {
		if st.ID == id {
			return i
		}
	}
	t.Fatalf("no step %q", id)
	return -1
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
