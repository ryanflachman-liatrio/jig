package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validBugfix is the worked example from docs/workflow-schema.md. Skill dirs and
// schema paths are written to disk by the test so file-existence checks pass.
const validBugfix = `
[workflow]
name = "bugfix"
version = "1"

[defaults]
permission_mode = "acceptEdits"

[[step]]
id = "triage"
type = "agent"
skill = "skills/triage"
inputs = ["reports/bug.md", { path = "conventions.md", inline = true }]
output = "triage.md"
allowed_tools = ["Read", "Grep", "Glob"]

[[step]]
id = "fix"
type = "agent"
depends_on = ["triage"]
skill = "skills/fix"
inputs = ["@triage"]
allowed_tools = ["Read", "Edit", "Write", "Bash"]

  [step.validate]
  command = "go test ./..."

[[step]]
id = "approve"
type = "review"
depends_on = ["fix"]
review = "diff"
output_type = { enum = ["approve", "revise"] }

  [step.loop]
  when = "approve == 'revise'"
  goto = "fix"
  max_iterations = 3
  feedback = "@approve"

[[step]]
id = "merge"
type = "command"
depends_on = ["approve"]
when = "approve == 'approve'"
run = "git merge --no-ff jig/bugfix/fix"
`

func TestDecodeValid(t *testing.T) {
	dir := t.TempDir()
	for _, skill := range []string{"skills/triage", "skills/fix"} {
		mustWrite(t, filepath.Join(dir, skill, "SKILL.md"), "# skill\n")
	}

	wf, err := Decode(validBugfix, dir)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if got := len(wf.Steps); got != 4 {
		t.Fatalf("steps = %d, want 4", got)
	}
	// Defaults applied.
	if wf.Defaults.MaxParallel != defaultMaxParallel {
		t.Errorf("max_parallel = %d, want %d", wf.Defaults.MaxParallel, defaultMaxParallel)
	}
	// fix has mutating tools -> worktree isolation defaulted on.
	if got := wf.Steps[wf.index["fix"]].Isolation; got != IsolationWorktree {
		t.Errorf("fix isolation = %q, want worktree", got)
	}
	// triage is read-only -> no worktree.
	if got := wf.Steps[wf.index["triage"]].Isolation; got != IsolationNone {
		t.Errorf("triage isolation = %q, want none", got)
	}
	// Mixed inputs parsed into ref + inline path.
	triage := wf.Steps[wf.index["triage"]]
	if triage.Inputs[0].Path != "reports/bug.md" || triage.Inputs[0].Ref != "" {
		t.Errorf("triage input[0] = %+v", triage.Inputs[0])
	}
	if !triage.Inputs[1].Inline || triage.Inputs[1].Path != "conventions.md" {
		t.Errorf("triage input[1] = %+v", triage.Inputs[1])
	}
	if fix := wf.Steps[wf.index["fix"]]; fix.Inputs[0].Ref != "triage" {
		t.Errorf("fix input[0].Ref = %q, want triage", fix.Inputs[0].Ref)
	}
}

func TestDecodeInvalid(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string // substring expected in the aggregated error
	}{
		{
			name: "unknown key",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
runn = "typo"`,
			want: "unknown key",
		},
		{
			name: "missing dep",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
depends_on = ["ghost"]`,
			want: `depends_on unknown step "ghost"`,
		},
		{
			name: "ref not in depends_on",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
output = "a.md"
[[step]]
id = "b"
type = "command"
run = "true"
inputs = ["@a"]`,
			want: "must also appear in depends_on",
		},
		{
			name: "cycle",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
depends_on = ["b"]
[[step]]
id = "b"
type = "command"
run = "true"
depends_on = ["a"]`,
			want: "cycle",
		},
		{
			name: "bad when value",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "review"
review = "diff"
output_type = { enum = ["yes", "no"] }
[[step]]
id = "b"
type = "command"
run = "true"
depends_on = ["a"]
when = "a == 'maybe'"`,
			want: `"maybe" is not a valid value`,
		},
		{
			name: "unbounded loop",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "review"
review = "diff"
output_type = { enum = ["ok", "redo"] }
[step.loop]
when = "a == 'redo'"
goto = "a"
max_iterations = 0`,
			want: "max_iterations must be >= 1",
		},
		{
			name: "command with both run and script",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
script = "x.sh"`,
			want: "both `run` and `script`",
		},
		{
			name: "review missing verdict type",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "review"
review = "diff"`,
			want: "needs an output_type",
		},
		{
			name: "field ref to unknown field",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
allowed_tools = ["Read"]
[step.schema]
status = { enum = ["ok", "fail"] }
[[step]]
id = "b"
type = "agent"
skill = "s"
depends_on = ["a"]
when = "a.nope == 'ok'"`,
			want: `schema has no field "nope"`,
		},
		{
			name: "field enum illegal value",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
allowed_tools = ["Read"]
[step.schema]
status = { enum = ["ok", "fail"] }
[[step]]
id = "b"
type = "agent"
skill = "s"
depends_on = ["a"]
when = "a.status == 'maybe'"`,
			want: `is not a valid value for field "status"`,
		},
		{
			name: "both output_type and schema",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
allowed_tools = ["Read"]
output_type = { enum = ["x", "y"] }
[step.schema]
status = "text"`,
			want: "one output shape",
		},
		{
			name: "both schema and schema_file",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
allowed_tools = ["Read"]
schema_file = "x.json"
[step.schema]
status = "text"`,
			want: "pick one",
		},
		{
			name: "schema on command step",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
[step.schema]
status = "text"`,
			want: "only valid on agent steps",
		},
		{
			name: "invalid effort",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
effort = "turbo"
allowed_tools = ["Read"]`,
			want: "invalid effort",
		},
		{
			name: "negative budget",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
max_budget_usd = -1.0
allowed_tools = ["Read"]`,
			want: "max_budget_usd must be >= 0",
		},
		{
			name: "skill and agent_file both set",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
agent_file = "a.md"`,
			want: "both `skill` and `agent_file`",
		},
		{
			name: "neither skill nor agent_file",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
allowed_tools = ["Read"]`,
			want: "requires `skill` or `agent_file`",
		},
		{
			name: "agent-only field on command",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
append_system_prompt = "be terse"`,
			want: "belonging to another step type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.toml, "") // "" skips file-existence checks
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// validProducer exercises structured output: a research producer with an inline
// [step.schema], a downstream step that pulls a field into its inputs and
// guards on another field.
const validProducer = `
[workflow]
name = "research"
version = "1"

[[step]]
id = "research"
type = "agent"
skill = "skills/research"
allowed_tools = ["Read", "Grep"]

  [step.schema]
  summary    = "text"
  status     = { enum = ["complete", "partial", "blocked"] }
  confidence = "number"
  sources    = { list = { url = "text", relevance = "number" } }

[[step]]
id = "report"
type = "agent"
depends_on = ["research"]
skill = "skills/report"
inputs = ["@research.summary", { ref = "@research.status", inline = true }]
when = "research.status == 'complete'"
allowed_tools = ["Read"]
`

func TestDecodeProducerSchema(t *testing.T) {
	dir := t.TempDir()
	for _, skill := range []string{"skills/research", "skills/report"} {
		mustWrite(t, filepath.Join(dir, skill, "SKILL.md"), "# skill\n")
	}

	wf, err := Decode(validProducer, dir)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	research := wf.Steps[wf.index["research"]]
	if research.Schema == nil {
		t.Fatal("research.Schema is nil")
	}
	// Fields are name-sorted.
	gotNames := make([]string, len(research.Schema.Fields))
	for i, f := range research.Schema.Fields {
		gotNames[i] = f.Name
	}
	want := []string{"confidence", "sources", "status", "summary"}
	if strings.Join(gotNames, ",") != strings.Join(want, ",") {
		t.Errorf("field names = %v, want %v", gotNames, want)
	}

	// Nested list-of-object field path resolves.
	if f, ok := research.Schema.lookup([]string{"sources", "url"}); !ok {
		// sources is a list; lookup does not index into elements, so this is
		// expected to miss. Assert the element type via Elem instead.
		src, ok := research.Schema.lookup([]string{"sources"})
		if !ok || src.Type != FieldList || src.Elem == nil || src.Elem.Type != FieldObject {
			t.Errorf("sources = %+v, want list of object", src)
		}
	} else if f.Type != FieldText {
		t.Errorf("sources.url type = %s, want text", f.Type)
	}

	// The downstream input carries the field path.
	report := wf.Steps[wf.index["report"]]
	if got := report.Inputs[0]; got.Ref != "research" || strings.Join(got.RefField, ".") != "summary" {
		t.Errorf("report input[0] = %+v, want ref=research field=[summary]", got)
	}
	if got := report.Inputs[1]; got.Ref != "research" || strings.Join(got.RefField, ".") != "status" || !got.Inline {
		t.Errorf("report input[1] = %+v, want ref=research field=[status] inline", got)
	}

	// The compiled JSON Schema is a closed object with all fields required.
	raw, err := research.Schema.JSONSchema()
	if err != nil {
		t.Fatalf("JSONSchema: %v", err)
	}
	var js map[string]any
	if err := json.Unmarshal(raw, &js); err != nil {
		t.Fatalf("compiled schema is not valid JSON: %v", err)
	}
	if js["type"] != "object" || js["additionalProperties"] != false {
		t.Errorf("compiled schema = %s, want closed object", raw)
	}
	if req, _ := js["required"].([]any); len(req) != 4 {
		t.Errorf("required = %v, want 4 fields", js["required"])
	}
}

// TestDecodeSchemaFile checks that a raw JSON Schema file is parsed into the
// same Field model, so field-ref checks work against it too.
func TestDecodeSchemaFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "skills/triage/SKILL.md"), "# skill\n")
	mustWrite(t, filepath.Join(dir, "skills/route/SKILL.md"), "# skill\n")
	mustWrite(t, filepath.Join(dir, "schemas/triage.json"), `{
	  "type": "object",
	  "properties": {
	    "priority": { "type": "string", "enum": ["low", "high"] },
	    "summary":  { "type": "string" }
	  },
	  "required": ["priority", "summary"]
	}`)

	src := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "triage"
type = "agent"
skill = "skills/triage"
schema_file = "schemas/triage.json"
allowed_tools = ["Read"]
[[step]]
id = "route"
type = "agent"
depends_on = ["triage"]
skill = "skills/route"
when = "triage.priority == 'high'"
allowed_tools = ["Read"]
`
	wf, err := Decode(src, dir)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	tr := wf.Steps[wf.index["triage"]]
	if tr.Schema == nil || tr.Schema.File != "schemas/triage.json" {
		t.Fatalf("triage.Schema = %+v, want loaded from file", tr.Schema)
	}
	if f, ok := tr.Schema.lookup([]string{"priority"}); !ok || f.Type != FieldEnum {
		t.Errorf("priority = %+v, want enum", f)
	}
}

// TestDecodeAgentFile covers the agent_file step form: frontmatter tools/model
// fold into the step, the body is captured as the system prompt, mutating tools
// flip worktree isolation on, and explicit step fields outrank the file.
func TestDecodeAgentFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "agents/reviewer.md"), `---
name: reviewer
description: Reviews the diff for security issues
tools: Read, Grep, Edit, Bash
model: opus
---
You are a meticulous security reviewer. Flag any risky change.`)

	src := `
[workflow]
name = "x"
version = "1"

[defaults]
effort         = "high"
fallback_model = "claude-sonnet-4-6"

[[step]]
id         = "review"
type       = "agent"
agent_file = "agents/reviewer.md"

[[step]]
id         = "override"
type       = "agent"
agent_file = "agents/reviewer.md"
model      = "claude-haiku-4-5-20251001"
effort     = "low"
`
	wf, err := Decode(src, dir)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	rv := wf.Steps[wf.index["review"]]
	if got := rv.agentPrompt; !strings.Contains(got, "security reviewer") {
		t.Errorf("agentPrompt = %q, want the file body", got)
	}
	if got, want := strings.Join(rv.AllowedTools, ","), "Read,Grep,Edit,Bash"; got != want {
		t.Errorf("AllowedTools = %q, want %q", got, want)
	}
	if rv.Model != "opus" {
		t.Errorf("Model = %q, want %q (from agent file)", rv.Model, "opus")
	}
	// Edit/Bash are mutating -> worktree isolation defaulted on.
	if rv.Isolation != IsolationWorktree {
		t.Errorf("Isolation = %q, want worktree (mutating tools from file)", rv.Isolation)
	}
	// Defaults still apply for knobs the file doesn't set.
	if rv.Effort != EffortHigh {
		t.Errorf("Effort = %q, want high (from defaults)", rv.Effort)
	}
	if rv.FallbackModel != "claude-sonnet-4-6" {
		t.Errorf("FallbackModel = %q, want inherited from defaults", rv.FallbackModel)
	}

	// Explicit step fields outrank both the file and defaults.
	ov := wf.Steps[wf.index["override"]]
	if ov.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("override Model = %q, want the explicit step value", ov.Model)
	}
	if ov.Effort != EffortLow {
		t.Errorf("override Effort = %q, want low", ov.Effort)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
