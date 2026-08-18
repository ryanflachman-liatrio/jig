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
	// Backend/transport default to claude/sdk when unset.
	for _, id := range []string{"triage", "fix"} {
		s := wf.Steps[wf.index[id]]
		if s.Backend != BackendClaude {
			t.Errorf("%s Backend = %q, want %q", id, s.Backend, BackendClaude)
		}
		if s.Transport != TransportSDK {
			t.Errorf("%s Transport = %q, want %q", id, s.Transport, TransportSDK)
		}
	}
}

func TestDecodeBackendTransport(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "skills/a", "SKILL.md"), "# skill\n")

	t.Run("defaults inherit and per-step override", func(t *testing.T) {
		toml := `
[workflow]
name = "x"
version = "1"
[defaults]
backend = "claude"
transport = "sdk"
[[step]]
id = "a"
type = "agent"
skill = "skills/a"
allowed_tools = ["Read"]
[[step]]
id = "b"
type = "agent"
skill = "skills/a"
depends_on = ["a"]
transport = "acp"
allowed_tools = ["Read"]
`
		wf, err := Decode(toml, dir)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		a := wf.Steps[wf.index["a"]]
		if a.Backend != BackendClaude || a.Transport != TransportSDK {
			t.Errorf("a = %s/%s, want claude/sdk", a.Backend, a.Transport)
		}
		b := wf.Steps[wf.index["b"]]
		if b.Backend != BackendClaude || b.Transport != TransportACP {
			t.Errorf("b = %s/%s, want claude/acp", b.Backend, b.Transport)
		}
	})

	t.Run("unknown backend", func(t *testing.T) {
		toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "skills/a"
backend = "cursor"
allowed_tools = ["Read"]
`
		_, err := Decode(toml, dir)
		if err == nil || !strings.Contains(err.Error(), "invalid backend") {
			t.Fatalf("error = %v, want invalid backend", err)
		}
	})

	t.Run("unknown transport", func(t *testing.T) {
		toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "skills/a"
transport = "grpc"
allowed_tools = ["Read"]
`
		_, err := Decode(toml, dir)
		if err == nil || !strings.Contains(err.Error(), "invalid transport") {
			t.Fatalf("error = %v, want invalid transport", err)
		}
	})
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
		{
			name: "max_messages negative",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "review"
review = "diff"
max_messages = -1
output_type = { enum = ["ok"] }`,
			want: "max_messages must be >= 0",
		},
		{
			name: "max_messages on agent step",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
allowed_tools = ["Read"]
max_messages = 5`,
			want: "max_messages is only valid on review steps",
		},
		{
			name: "max_messages on command step",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
max_messages = 3`,
			want: "max_messages is only valid on review steps",
		},
		{
			name: "inject_context on command step",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
inject_context = false`,
			want: "inject_context is only valid on agent steps",
		},
		{
			name: "context block with explicit inject_context false",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
inject_context = false
[step.context]
purpose = "why this step exists"`,
			want: "contradiction (the block would be inert)",
		},
		{
			name: "context block on command step",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
[step.context]
purpose = "why"`,
			want: "[step.context] is only valid on agent steps",
		},
		{
			name: "context purpose non-string",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
[step.context]
purpose = 5`,
			want: "incompatible types",
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

func TestMaxMessagesValid(t *testing.T) {
	toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "review"
review = "diff"
max_messages = 5
output_type = { enum = ["ok"] }
`
	if _, err := Decode(toml, ""); err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
}

// TestDecodeInjectContext proves the [defaults]/per-step precedence of the
// inject_context toggle: an explicit per-step `= true` overrides a
// `[defaults].inject_context = false`, while a step that sets nothing inherits
// the (false) default. Exercises the effective read InjectContextEnabled().
func TestDecodeInjectContext(t *testing.T) {
	toml := `
[workflow]
name = "x"
version = "1"

[defaults]
inject_context = false

[[step]]
id = "override_on"
type = "agent"
skill = "s"
inject_context = true
allowed_tools = ["Read"]

[[step]]
id = "inherit_off"
type = "agent"
skill = "s"
allowed_tools = ["Read"]
`
	wf, err := Decode(toml, "") // "" skips skill-dir existence checks
	if err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
	if got := wf.Steps[wf.index["override_on"]]; !got.InjectContextEnabled() {
		t.Errorf("override_on: per-step inject_context = true must beat [defaults] = false (want enabled)")
	}
	if got := wf.Steps[wf.index["inherit_off"]]; got.InjectContextEnabled() {
		t.Errorf("inherit_off: unset step must inherit [defaults].inject_context = false (want disabled)")
	}
}

// TestDecodeStepContext proves a valid [step.context] block parses and its
// purpose/notes land on the step.
func TestDecodeStepContext(t *testing.T) {
	toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
allowed_tools = ["Read"]
[step.context]
purpose = "why a exists"
notes = "local guidance for a"
`
	wf, err := Decode(toml, "")
	if err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
	a := wf.Steps[wf.index["a"]]
	if a.Context == nil {
		t.Fatalf("step a: Context is nil, want parsed [step.context]")
	}
	if a.Context.Purpose != "why a exists" {
		t.Errorf("step a purpose = %q, want %q", a.Context.Purpose, "why a exists")
	}
	if a.Context.Notes != "local guidance for a" {
		t.Errorf("step a notes = %q, want %q", a.Context.Notes, "local guidance for a")
	}
}

func TestBlockOnValid(t *testing.T) {
	// block_on with a schema field reference — valid when condition references own step.
	toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "chat"
type = "agent"
skill = "skills/ask"
block_on = "chat.needs_input == 'true'"

  [step.schema]
  needs_input = { enum = ["true", "false"] }
`
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "skills/ask", "SKILL.md"), "# ask\n")
	if _, err := Decode(toml, dir); err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
}

func TestBlockOnInvalid(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "block_on references other step",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
block_on = "b.done == 'true'"
[[step]]
id = "b"
type = "command"
run = "true"`,
			want: "must reference this step's own output",
		},
		{
			name: "block_on field not in schema",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
block_on = "a.needs_input == 'true'"`,
			want: `schema has no field "needs_input"`,
		},
		{
			name: "block_on parse error",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
block_on = "not a valid condition"`,
			want: "block_on",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.toml, "")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// TestPermissionModeValid verifies each of the SDK's known permission modes
// passes validation, at both the [defaults] and per-step level.
func TestPermissionModeValid(t *testing.T) {
	toml := `
[workflow]
name = "x"
version = "1"
[defaults]
permission_mode = "acceptEdits"
[[step]]
id = "a"
type = "agent"
skill = "s"
[[step]]
id = "b"
type = "agent"
skill = "s"
permission_mode = "bypassPermissions"
`
	if _, err := Decode(toml, ""); err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
}

// TestPermissionModeInvalid verifies a typo'd permission_mode fails at load
// time rather than silently reaching the SDK as a no-op.
func TestPermissionModeInvalid(t *testing.T) {
	toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "s"
permission_mode = "accept-edits"
`
	_, err := Decode(toml, "")
	if err == nil {
		t.Fatal("expected error for invalid permission_mode, got nil")
	}
	if !strings.Contains(err.Error(), "permission_mode") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "permission_mode")
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
  sources = { list = { url = "text", relevance = "number" } }

[[step]]
id = "report"
type = "agent"
depends_on = ["research"]
skill = "skills/report"
inputs = ["@research.summary", { ref = "@research.status", inline = true }]
when = "research.status == 'succeeded'"
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
	// Only the declared fields are in Schema.Fields; base fields are injected
	// automatically at dispatch and are not stored on the step.
	gotNames := make([]string, len(research.Schema.Fields))
	for i, f := range research.Schema.Fields {
		gotNames[i] = f.Name
	}
	want := []string{"sources"}
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
	if req, _ := js["required"].([]any); len(req) != 1 {
		t.Errorf("required = %v, want 1 declared field (sources)", js["required"])
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
	    "priority": { "type": "string", "enum": ["low", "high"] }
	  },
	  "required": ["priority"]
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

func TestDecodeProfileValid(t *testing.T) {
	// minAgent is the smallest valid agent-step TOML, minus any profile.
	const hdr = `
[workflow]
name = "x"
version = "1"
`
	cases := []struct {
		name           string
		toml           string
		checkStep      string
		wantTools      []string
		wantDisallowed []string
	}{
		{
			name: "@interactive on step with no tools injects AskUserQuestion",
			toml: hdr + `
[[step]]
id = "ask"
type = "agent"
skill = "s"
profile = "@interactive"
`,
			checkStep: "ask",
			wantTools: []string{"AskUserQuestion"},
		},
		{
			name: "@interactive on step with explicit tools appends AskUserQuestion",
			toml: hdr + `
[[step]]
id = "ask"
type = "agent"
skill = "s"
profile = "@interactive"
allowed_tools = ["Read", "Grep"]
`,
			checkStep: "ask",
			wantTools: []string{"Read", "Grep", "AskUserQuestion"},
		},
		{
			name: "@interactive does not add AskUserQuestion twice",
			toml: hdr + `
[[step]]
id = "ask"
type = "agent"
skill = "s"
profile = "@interactive"
allowed_tools = ["AskUserQuestion", "Read"]
`,
			checkStep: "ask",
			wantTools: []string{"AskUserQuestion", "Read"},
		},
		{
			name: "@autonomous sets disallowed_tools",
			toml: hdr + `
[[step]]
id = "bot"
type = "agent"
skill = "s"
profile = "@autonomous"
`,
			checkStep:      "bot",
			wantDisallowed: []string{"AskUserQuestion"},
		},
		{
			name: "explicit disallowed_tools wins over @autonomous",
			toml: hdr + `
[[step]]
id = "bot"
type = "agent"
skill = "s"
profile = "@autonomous"
disallowed_tools = ["Bash"]
`,
			checkStep:      "bot",
			wantDisallowed: []string{"Bash"},
		},
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "s", "SKILL.md"), "# skill")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf, err := Decode(tc.toml, dir)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			s := wf.Steps[wf.index[tc.checkStep]]
			if tc.wantTools != nil {
				if got := strings.Join(s.AllowedTools, ","); got != strings.Join(tc.wantTools, ",") {
					t.Errorf("AllowedTools = %q, want %q", got, strings.Join(tc.wantTools, ","))
				}
			}
			if tc.wantDisallowed != nil {
				if got := strings.Join(s.DisallowedTools, ","); got != strings.Join(tc.wantDisallowed, ",") {
					t.Errorf("DisallowedTools = %q, want %q", got, strings.Join(tc.wantDisallowed, ","))
				}
			}
		})
	}
}

func TestDecodeProfileInvalid(t *testing.T) {
	const hdr = `
[workflow]
name = "x"
version = "1"
`
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "unknown profile",
			toml: hdr + `
[[step]]
id = "a"
type = "agent"
skill = "s"
profile = "@nonexistent"
`,
			want: `unknown profile "@nonexistent"`,
		},
		{
			name: "profile without @ prefix",
			toml: hdr + `
[[step]]
id = "a"
type = "agent"
skill = "s"
profile = "interactive"
`,
			want: `profile "interactive" must start with '@'`,
		},
		{
			name: "profile on command step",
			toml: hdr + `
[[step]]
id = "a"
type = "command"
run = "true"
profile = "@interactive"
`,
			want: "fields belonging to another step type",
		},
		{
			name: "profile on review step",
			toml: hdr + `
[[step]]
id = "a"
type = "review"
review = "diff"
output_type = "bool"
profile = "@interactive"
`,
			want: "fields belonging to another step type",
		},
		{
			name: "@interactive combined with block_on",
			toml: hdr + `
[[step]]
id = "a"
type = "agent"
skill = "s"
profile = "@interactive"
block_on = "a.needs_input"
[step.schema]
needs_input = "bool"
`,
			want: "overlapping purposes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.toml, "")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// TestSecurityConfig proves: zero-config workflow has security on by default;
// valid [defaults.security] blocks parse and cascade to steps; invalid values
// are rejected at load time.
func TestSecurityConfig(t *testing.T) {
	t.Run("zero config has security enabled by default", func(t *testing.T) {
		toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
`
		wf, err := Decode(toml, "")
		if err != nil {
			t.Fatalf("expected valid, got error: %v", err)
		}
		// Security.Enabled nil means "engine default on"; it is never set to false
		// by the loader when the block is absent.
		if wf.Defaults.Security.Enabled != nil {
			t.Errorf("Defaults.Security.Enabled = %v, want nil (engine default on)", wf.Defaults.Security.Enabled)
		}
	})

	t.Run("valid security config loads and cascades", func(t *testing.T) {
		falseVal := false
		_ = falseVal
		toml := `
[workflow]
name = "x"
version = "1"

[defaults.security]
fleet_budget_usd = 0.10
outbound_allowlist = ["api.github.com", "storage.googleapis.com"]
concurrency_cap = 2

[[step]]
id = "a"
type = "command"
run = "true"

[[step]]
id = "b"
type = "command"
run = "true"

[step.security]
enabled = false
`
		wf, err := Decode(toml, "")
		if err != nil {
			t.Fatalf("expected valid, got error: %v", err)
		}
		if wf.Defaults.Security.FleetBudgetUSD != 0.10 {
			t.Errorf("FleetBudgetUSD = %g, want 0.10", wf.Defaults.Security.FleetBudgetUSD)
		}
		if len(wf.Defaults.Security.OutboundAllowlist) != 2 {
			t.Errorf("OutboundAllowlist len = %d, want 2", len(wf.Defaults.Security.OutboundAllowlist))
		}
		// Step b has explicit security.enabled=false; step a inherits nil from defaults.
		b := wf.Steps[wf.index["b"]]
		if b.Security.Enabled == nil || *b.Security.Enabled != false {
			t.Errorf("step b Security.Enabled = %v, want explicit false", b.Security.Enabled)
		}
		// Step a inherits the allowlist from [defaults.security].
		a := wf.Steps[wf.index["a"]]
		if len(a.Security.OutboundAllowlist) != 2 {
			t.Errorf("step a inherited OutboundAllowlist len = %d, want 2", len(a.Security.OutboundAllowlist))
		}
	})

	// Invalid cases.
	invalidCases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "negative fleet_budget_usd",
			toml: `
[workflow]
name = "x"
version = "1"
[defaults.security]
fleet_budget_usd = -1.0
[[step]]
id = "a"
type = "command"
run = "true"`,
			want: "fleet_budget_usd must be >= 0",
		},
		{
			name: "negative concurrency_cap",
			toml: `
[workflow]
name = "x"
version = "1"
[defaults.security]
concurrency_cap = -1
[[step]]
id = "a"
type = "command"
run = "true"`,
			want: "concurrency_cap must be >= 1",
		},
		{
			name: "invalid hostname in outbound_allowlist",
			toml: `
[workflow]
name = "x"
version = "1"
[defaults.security]
outbound_allowlist = ["https://api.example.com"]
[[step]]
id = "a"
type = "command"
run = "true"`,
			want: "not a valid hostname",
		},
		{
			name: "invalid hostname in step security override",
			toml: `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
[step.security]
outbound_allowlist = ["bad host with spaces"]`,
			want: "not a valid hostname",
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.toml, "")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
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
