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
output_type = { enum = ["approve", "revise"] }

[[step.route]]
when = "approve == 'revise'"
goto = "fix"
max_iterations = 3
feedback = "@approve"

[[step.review]]
source = "diff"
label = "Code changes"

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
		mustWriteSkill(t, filepath.Join(dir, skill, "SKILL.md"), "# Skill")
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

func TestDecodeReviewFileTargets(t *testing.T) {
	const header = `
[workflow]
name = "review-files"
version = "1"

[[step]]
id = "write"
type = "agent"
skill = "skills/write"
  [step.schema]
  spec_path = "text"
  count = "number"
`
	const review = `
[[step]]
id = "review"
type = "review"
depends_on = ["write"]
output_type = { enum = ["approve", "revise"] }
`

	t.Run("valid upstream text field", func(t *testing.T) {
		wf, err := Decode(header+review+`
[[step.review]]
source = "diff"
label = "Code changes"

[[step.review]]
file = "@write.spec_path"
label = "Specification"
`, "")
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		target := wf.Steps[wf.index["review"]].Review[1]
		if target.Kind() != ReviewTargetFile || target.Reference() != "@write.spec_path" {
			t.Fatalf("file target = %#v, want file reference", target)
		}
	})

	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "unknown producer",
			toml: header + review + `
[[step.review]]
file = "@missing.spec_path"
label = "Specification"
`,
			want: `file target "@missing.spec_path" references unknown step "missing"`,
		},
		{
			name: "producer is not a direct dependency",
			toml: header + `
[[step]]
id = "review"
type = "review"
output_type = { enum = ["approve", "revise"] }
[[step.review]]
file = "@write.spec_path"
label = "Specification"
`,
			want: `file target "@write.spec_path" must also list "write" in depends_on`,
		},
		{
			name: "non text field",
			toml: header + review + `
[[step.review]]
file = "@write.count"
label = "Count"
`,
			want: `file target "@write.count" must reference a text field, got "number"`,
		},
		{
			name: "bare producer reference",
			toml: header + review + `
[[step.review]]
file = "@write"
label = "Specification"
`,
			want: `file target "@write" must be an @step.field reference`,
		},
		{
			name: "literal path",
			toml: header + review + `
[[step.review]]
file = "docs/spec.md"
label = "Specification"
`,
			want: `file target "docs/spec.md" must be an @step.field reference`,
		},
		{
			name: "duplicate label across target forms",
			toml: header + review + `
[[step.review]]
source = "diff"
label = "Evidence"
[[step.review]]
file = "@write.spec_path"
label = "Evidence"
`,
			want: `duplicate review label "Evidence"`,
		},
		{
			name: "mixed source and file",
			toml: header + review + `
[[step.review]]
source = "@write.spec_path"
file = "@write.spec_path"
label = "Specification"
`,
			want: "sets both `source` and `file`; pick one",
		},
		{
			name: "neither source nor file",
			toml: header + review + `
[[step.review]]
label = "Specification"
`,
			want: "requires exactly one of `source` or `file`",
		},
		{
			name: "duplicate file target",
			toml: header + review + `
[[step.review]]
file = "@write.spec_path"
label = "Specification"
[[step.review]]
file = "@write.spec_path"
label = "Specification duplicate"
`,
			want: `duplicate review file "@write.spec_path"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.toml, "")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

func TestLoadExpandsSubworkflowWithTypedExports(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills", "collect", "SKILL.md"), "# collect")
	mustWriteSkill(t, filepath.Join(dir, "modules", "skills", "write", "SKILL.md"), "# write")
	mustWriteSkill(t, filepath.Join(dir, "skills", "consume", "SKILL.md"), "# consume")
	modulePath := filepath.Join(dir, "modules", "spec.toml")
	if err := os.WriteFile(modulePath, []byte(`
[module]
schema_version = 1

[module.inputs.request]
type = "text"

[module.exports.document]
ref = "@write.document"

[[step]]
id = "write"
type = "agent"
skill = "skills/write"
inputs = ["@module.request"]
  [step.schema]
  document = "text"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(dir, "workflow.toml")
	if err := os.WriteFile(rootPath, []byte(`
[workflow]
name = "modules"
version = "1"

[[step]]
id = "collect"
type = "agent"
skill = "skills/collect"
  [step.schema]
  request = "text"

[[step]]
id = "spec"
type = "subworkflow"
depends_on = ["collect"]
module = "modules/spec.toml"
with = { request = "@collect.request" }

[[step]]
id = "consume"
type = "agent"
depends_on = ["spec"]
skill = "skills/consume"
inputs = ["@spec.document"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	wf, err := Load(rootPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(wf.PublicSteps()); got != 3 {
		t.Fatalf("public steps = %d, want 3", got)
	}
	if got := len(wf.Steps); got != 3 {
		t.Fatalf("expanded steps = %d, want 3", got)
	}
	write := wf.Steps[wf.index["spec__write"]]
	if got := write.Inputs[0]; got.Ref != "collect" || strings.Join(got.RefField, ".") != "request" || !contains(write.DependsOn, "collect") {
		t.Fatalf("expanded module input = %+v depends_on=%v", got, write.DependsOn)
	}
	consume := wf.Steps[wf.index["consume"]]
	if got := consume.Inputs[0]; got.Ref != "spec__write" || strings.Join(got.RefField, ".") != "document" {
		t.Fatalf("expanded module export = %+v", got)
	}
	if !contains(consume.DependsOn, "spec__write") {
		t.Fatalf("consume dependencies = %v, want module terminal", consume.DependsOn)
	}
	if got := wf.ModuleSources(); len(got) != 1 || got[0].Path != modulePath || got[0].SHA256 == "" {
		t.Fatalf("module sources = %+v", got)
	}
}

func TestLoadRewritesModuleExportsInParentGuardsAndReviews(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills", "decision", "SKILL.md"), "# decision")
	mustWriteSkill(t, filepath.Join(dir, "skills", "gate", "SKILL.md"), "# gate")
	mustWrite(t, filepath.Join(dir, "module.toml"), `
[module]
schema_version = 1

[module.exports.ready]
ref = "@decision.ready"

[module.exports.summary]
ref = "@decision.overview"

[[step]]
id = "decision"
type = "agent"
skill = "skills/decision"
  [step.schema]
  ready = { enum = ["yes", "no"] }
  overview = "text"
`)
	root := filepath.Join(dir, "workflow.toml")
	mustWrite(t, root, `
[workflow]
name = "modules"
version = "1"

[[step]]
id = "gate"
type = "agent"
skill = "skills/gate"
  [step.schema]
  enabled = "bool"

[[step]]
id = "phase"
type = "subworkflow"
depends_on = ["gate"]
when = "gate.enabled"
module = "module.toml"

[[step]]
id = "continue"
type = "command"
depends_on = ["phase"]
when = "phase.ready == 'yes'"
run = "true"

[[step]]
id = "review"
type = "review"
depends_on = ["phase"]
output_type = { enum = ["approve"] }
  [[step.review]]
  source = "@phase.summary"
  label = "Phase summary"
`)

	wf, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	continueStep := wf.Steps[wf.index["continue"]]
	if got, want := continueStep.When, `phase__decision.ready == "yes"`; got != want {
		t.Fatalf("rewritten condition = %q, want %q", got, want)
	}
	decision := wf.Steps[wf.index["phase__decision"]]
	if got, want := decision.When, "gate.enabled"; got != want || !contains(decision.DependsOn, "gate") {
		t.Fatalf("module invocation guard was not propagated: when=%q depends_on=%v", got, decision.DependsOn)
	}
	review := wf.Steps[wf.index["review"]]
	if got, want := review.Review[0].Source, "@phase__decision.overview"; got != want {
		t.Fatalf("rewritten review source = %q, want %q", got, want)
	}
}

func TestLoadRebindsModuleFileReviewInput(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills", "write", "SKILL.md"), "# write")
	mustWrite(t, filepath.Join(dir, "review.toml"), `
[module]
schema_version = 1

[module.inputs.spec_path]
type = "text"

[module.exports.verdict]
ref = "@gate"

[[step]]
id = "gate"
type = "review"
output_type = { enum = ["approve"] }
  [[step.review]]
  file = "@module.spec_path"
  label = "Specification"
`)
	root := filepath.Join(dir, "workflow.toml")
	mustWrite(t, root, `
[workflow]
name = "module-file-review"
version = "1"

[[step]]
id = "write"
type = "agent"
skill = "skills/write"
  [step.schema]
  spec_path = "text"

[[step]]
id = "review"
type = "subworkflow"
depends_on = ["write"]
module = "review.toml"
with = { spec_path = "@write.spec_path" }
`)

	wf, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gate := wf.Steps[wf.index["review__gate"]]
	if got, want := gate.Review[0].File, "@write.spec_path"; got != want {
		t.Fatalf("file target = %q, want %q", got, want)
	}
	if !contains(gate.DependsOn, "write") {
		t.Fatalf("file review dependencies = %v, want write", gate.DependsOn)
	}
}

func TestLoadRebindsNestedModuleFileReviewInput(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills", "write", "SKILL.md"), "# write")
	mustWrite(t, filepath.Join(dir, "leaf.toml"), `
[module]
schema_version = 1
[module.inputs.spec_path]
type = "text"
[module.exports.verdict]
ref = "@gate"
[[step]]
id = "gate"
type = "review"
output_type = { enum = ["approve"] }
  [[step.review]]
  file = "@module.spec_path"
  label = "Specification"
`)
	mustWrite(t, filepath.Join(dir, "middle.toml"), `
[module]
schema_version = 1
[module.inputs.spec_path]
type = "text"
[module.exports.verdict]
ref = "@leaf.verdict"
[[step]]
id = "leaf"
type = "subworkflow"
module = "leaf.toml"
with = { spec_path = "@module.spec_path" }
`)
	root := filepath.Join(dir, "workflow.toml")
	mustWrite(t, root, `
[workflow]
name = "nested-module-file-review"
version = "1"
[[step]]
id = "write"
type = "agent"
skill = "skills/write"
  [step.schema]
  spec_path = "text"
[[step]]
id = "review"
type = "subworkflow"
depends_on = ["write"]
module = "middle.toml"
with = { spec_path = "@write.spec_path" }
`)

	wf, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gate := wf.Steps[wf.index["review__leaf__gate"]]
	if got, want := gate.Review[0].File, "@write.spec_path"; got != want {
		t.Fatalf("nested file target = %q, want %q", got, want)
	}
	if !contains(gate.DependsOn, "write") {
		t.Fatalf("nested file review dependencies = %v, want write", gate.DependsOn)
	}
}

func TestLoadRejectsInvalidSubworkflowContracts(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "workflow.toml")
	writeRoot := func(t *testing.T, body string) {
		t.Helper()
		mustWrite(t, root, "[workflow]\nname = \"modules\"\nversion = \"1\"\n\n"+body)
	}
	writeModule := func(t *testing.T, body string) {
		t.Helper()
		mustWrite(t, filepath.Join(dir, "module.toml"), body)
	}

	tests := []struct {
		name       string
		module     string
		root       string
		want       string
		additional func(t *testing.T)
	}{
		{
			name:   "unsupported version",
			module: "[module]\nschema_version = 2\n[module.exports.result]\nref = \"@run\"\n[[step]]\nid = \"run\"\ntype = \"command\"\nrun = \"true\"\n",
			root:   "[[step]]\nid = \"part\"\ntype = \"subworkflow\"\nmodule = \"module.toml\"\n",
			want:   "module.schema_version = 2",
		},
		{
			name:   "missing required input",
			module: "[module]\nschema_version = 1\n[module.inputs.request]\ntype = \"text\"\n[module.exports.result]\nref = \"@run\"\n[[step]]\nid = \"run\"\ntype = \"command\"\nrun = \"true\"\n",
			root:   "[[step]]\nid = \"part\"\ntype = \"subworkflow\"\nmodule = \"module.toml\"\n",
			want:   "module input \"request\" is required",
		},
		{
			name:   "unknown binding",
			module: "[module]\nschema_version = 1\n[module.exports.result]\nref = \"@run\"\n[[step]]\nid = \"run\"\ntype = \"command\"\nrun = \"true\"\n",
			root:   "[[step]]\nid = \"part\"\ntype = \"subworkflow\"\nmodule = \"module.toml\"\nwith = { unknown = \"value\" }\n",
			want:   "unknown module input binding \"unknown\"",
		},
		{
			name:   "illegal export",
			module: "[module]\nschema_version = 1\n[module.exports.result]\nref = \"@missing.result\"\n[[step]]\nid = \"run\"\ntype = \"command\"\nrun = \"true\"\n",
			root:   "[[step]]\nid = \"part\"\ntype = \"subworkflow\"\nmodule = \"module.toml\"\n",
			want:   "references unknown internal step \"missing\"",
		},
		{
			name:   "binding type mismatch",
			module: "[module]\nschema_version = 1\n[module.inputs.request]\ntype = \"text\"\n[module.exports.result]\nref = \"@run\"\n[[step]]\nid = \"run\"\ntype = \"command\"\nrun = \"true\"\n",
			root:   "[[step]]\nid = \"collect\"\ntype = \"command\"\noutput_type = \"bool\"\nrun = \"true\"\n\n[[step]]\nid = \"part\"\ntype = \"subworkflow\"\ndepends_on = [\"collect\"]\nmodule = \"module.toml\"\nwith = { request = \"@collect\" }\n",
			want:   "expects text but @collect is an untyped output",
		},
		{
			name:   "duplicate expanded id",
			module: "[module]\nschema_version = 1\n[module.exports.result]\nref = \"@run\"\n[[step]]\nid = \"run\"\ntype = \"command\"\nrun = \"true\"\n",
			root:   "[[step]]\nid = \"part\"\ntype = \"subworkflow\"\nmodule = \"module.toml\"\n\n[[step]]\nid = \"part__run\"\ntype = \"command\"\nrun = \"true\"\n",
			want:   "duplicate step id \"part__run\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeModule(t, tt.module)
			writeRoot(t, tt.root)
			_, err := Load(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsModuleCycle(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.toml"), `
[module]
schema_version = 1
[module.exports.result]
ref = "@b.result"
[[step]]
id = "b"
type = "subworkflow"
module = "b.toml"
`)
	mustWrite(t, filepath.Join(dir, "b.toml"), `
[module]
schema_version = 1
[module.exports.result]
ref = "@a.result"
[[step]]
id = "a"
type = "subworkflow"
module = "a.toml"
`)
	root := filepath.Join(dir, "workflow.toml")
	mustWrite(t, root, `
[workflow]
name = "cycle"
version = "1"
[[step]]
id = "entry"
type = "subworkflow"
module = "a.toml"
`)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "module cycle") {
		t.Fatalf("Load error = %v, want module cycle", err)
	}
}

func TestDecodeCheckRoutesAndResourceLimits(t *testing.T) {
	const source = `
[workflow]
name = "quality"
version = "1"

[defaults]
max_parallel = 4
resource_limits = { checks = 2 }

[[step]]
id = "ready"
type = "command"
output_type = "bool"
run = "true"

[[step]]
id = "quality"
type = "check"
depends_on = ["ready"]
applies_when = "ready"
resource_class = "checks"
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "go test ./..."

  [step.findings]
  schema_version = 1
  file = ".jig/quality.findings.json"
  required_tools = ["go"]

[[step.route]]
when = "quality == 'fail'"
goto = "quality"
max_iterations = 2

[[step.route]]
when = "quality == 'pass'"
goto = "quality"
max_iterations = 2

[[step.route]]
when = "quality == 'skip'"
goto = "quality"
max_iterations = 2

[[step.route]]
when = "quality == 'error'"
goto = "quality"
max_iterations = 2
`
	wf, err := Decode(source, "")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := len(wf.Steps[wf.index["quality"]].Routes); got != 4 {
		t.Fatalf("routes = %d, want 4", got)
	}
}

func TestDecodeRejectsCheckWithoutTypedOutcomes(t *testing.T) {
	const source = `
[workflow]
name = "quality"
version = "1"

[[step]]
id = "quality"
type = "check"
run = "true"
`
	_, err := Decode(source, "")
	if err == nil || !strings.Contains(err.Error(), "output_type must include") {
		t.Fatalf("Decode error = %v, want typed check outcome error", err)
	}
}

func TestDecodeRejectsCheckWithoutApplicabilityOrFindingsContract(t *testing.T) {
	const source = `
[workflow]
name = "quality"
version = "1"

[[step]]
id = "ready"
type = "command"
output_type = "bool"
run = "true"

[[step]]
id = "quality"
type = "check"
depends_on = ["ready"]
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "true"
`
	_, err := Decode(source, "")
	if err == nil || !strings.Contains(err.Error(), "requires `applies_when`") || !strings.Contains(err.Error(), "requires a [step.findings] interface") {
		t.Fatalf("Decode error = %v, want applicability and findings contract errors", err)
	}
}

func TestDecodeRejectsCheckWithoutAutomaticRemediationRoute(t *testing.T) {
	const source = `
[workflow]
name = "quality"
version = "1"

[[step]]
id = "ready"
type = "command"
output_type = "bool"
run = "true"

[[step]]
id = "quality"
type = "check"
depends_on = ["ready"]
applies_when = "ready"
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "true"

  [step.findings]
  schema_version = 1
  file = "quality.json"
  required_tools = ["sh"]
`
	_, err := Decode(source, "")
	if err == nil || !strings.Contains(err.Error(), "automatic remediation route") {
		t.Fatalf("Decode error = %v, want automatic remediation route error", err)
	}
}

func TestDecodeRejectsUndeclaredCheckArtifactInput(t *testing.T) {
	const source = `
[workflow]
name = "quality"
version = "1"

[[step]]
id = "ready"
type = "command"
output_type = "bool"
run = "true"

[[step]]
id = "quality"
type = "check"
depends_on = ["ready"]
applies_when = "ready"
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "true"

  [step.findings]
  schema_version = 1
  file = "quality.json"
  required_tools = ["sh"]

[[step.route]]
when = "quality != 'pass'"
goto = "ready"
max_iterations = 1

[[step]]
id = "consumer"
type = "command"
depends_on = ["quality"]
inputs = [{ artifact = "@quality.missing" }]
run = "true"
`
	_, err := Decode(source, "")
	if err == nil || !strings.Contains(err.Error(), "undeclared check artifact") {
		t.Fatalf("Decode error = %v, want undeclared check artifact error", err)
	}
}

func TestDecodeRejectsOverlappingRoutes(t *testing.T) {
	const source = `
[workflow]
name = "routes"
version = "1"

[[step]]
id = "gate"
type = "command"
output_type = { enum = ["pass", "fail", "skip"] }
run = "true"

[[step.route]]
when = "gate != 'pass'"
goto = "gate"
max_iterations = 1

[[step.route]]
when = "gate != 'fail'"
goto = "gate"
max_iterations = 1
`
	_, err := Decode(source, "")
	if err == nil || !strings.Contains(err.Error(), "not mutually exclusive") {
		t.Fatalf("Decode error = %v, want route exclusivity error", err)
	}
}

func TestDecodeRouteCompletionAndRemediationContracts(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "bool routes exhaust without fallback",
			src: `
[workflow]
name = "routes"
version = "1"
[[step]]
id = "gate"
type = "command"
output_type = "bool"
run = "true"
[[step.route]]
when = "gate == 'true'"
goto = "gate"
max_iterations = 1
[[step.route]]
when = "gate == 'false'"
goto = "gate"
max_iterations = 1`,
		},
		{
			name: "fallback unreachable after exhaustive enum",
			src: `
[workflow]
name = "routes"
version = "1"
[[step]]
id = "gate"
type = "command"
output_type = { enum = ["pass", "fail"] }
run = "true"
[[step.route]]
when = "gate == 'pass'"
goto = "gate"
max_iterations = 1
[[step.route]]
when = "gate == 'fail'"
goto = "gate"
max_iterations = 1
[[step.route]]
fallback = true
goto = "gate"
max_iterations = 1`,
			want: "fallback route is unreachable",
		},
		{
			name: "check cannot route forward to approval",
			src: `
[workflow]
name = "quality"
version = "1"
[[step]]
id = "ready"
type = "command"
output_type = "bool"
run = "true"
[[step]]
id = "quality"
type = "check"
depends_on = ["ready"]
applies_when = "ready"
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "true"
  [step.findings]
  schema_version = 1
  file = "findings.json"
  required_tools = ["sh"]
[[step.route]]
when = "quality != 'pass'"
goto = "approval"
max_iterations = 1
[[step]]
id = "approval"
type = "review"
depends_on = ["quality"]
output_type = { enum = ["continue"] }
[[step.review]]
source = "diff"
label = "Approval"`,
			want: "upstream remediation/review step",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.src, "")
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Decode error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodeBackendTransport(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills/a", "SKILL.md"), "# Skill")

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

	t.Run("cursor acp backend", func(t *testing.T) {
		toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "skills/a"
backend = "cursor"
transport = "acp"
allowed_tools = ["Read"]
`
		wf, err := Decode(toml, dir)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		a := wf.Steps[wf.index["a"]]
		if a.Backend != BackendCursor || a.Transport != TransportACP {
			t.Errorf("a = %s/%s, want cursor/acp", a.Backend, a.Transport)
		}
	})

	t.Run("codex defaults to acp", func(t *testing.T) {
		toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "skills/a"
backend = "codex"
allowed_tools = ["Read"]
`
		wf, err := Decode(toml, dir)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		a := wf.Steps[wf.index["a"]]
		if a.Backend != BackendCodex || a.Transport != TransportACP {
			t.Errorf("a = %s/%s, want codex/acp", a.Backend, a.Transport)
		}
	})

	t.Run("cursor defaults to acp", func(t *testing.T) {
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
		wf, err := Decode(toml, dir)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		a := wf.Steps[wf.index["a"]]
		if a.Backend != BackendCursor || a.Transport != TransportACP {
			t.Errorf("a = %s/%s, want cursor/acp", a.Backend, a.Transport)
		}
	})

	t.Run("codex sdk transport is invalid", func(t *testing.T) {
		toml := `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "skills/a"
backend = "codex"
transport = "sdk"
allowed_tools = ["Read"]
`
		_, err := Decode(toml, dir)
		if err == nil || !strings.Contains(err.Error(), "requires transport") {
			t.Fatalf("error = %v, want invalid Codex transport", err)
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
backend = "openai"
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
output_type = { enum = ["yes", "no"] }
[[step.review]]
source = "diff"
label = "Code changes"
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
output_type = { enum = ["ok", "redo"] }
[[step.route]]
when = "a == 'redo'"
goto = "a"
max_iterations = 0
[[step.review]]
source = "diff"
label = "Code changes"`,
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
[[step.review]]
source = "diff"
label = "Code changes"
`,
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
	mustWriteSkill(t, filepath.Join(dir, "skills/ask", "SKILL.md"), "# Ask")
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
		mustWriteSkill(t, filepath.Join(dir, skill, "SKILL.md"), "# Skill")
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
	mustWriteSkill(t, filepath.Join(dir, "skills/triage/SKILL.md"), "# Triage")
	mustWriteSkill(t, filepath.Join(dir, "skills/route/SKILL.md"), "# Route")
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

func TestDecodeSkillPrompt(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "skills/research/SKILL.md"), `---
name: research
description: Research the requested topic
---
Follow the evidence and cite every conclusion.`)

	wf, err := Decode(`
[workflow]
name = "x"
version = "1"

[[step]]
id = "research"
type = "agent"
skill = "skills/research"
`, dir)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if got, want := wf.Steps[wf.index["research"]].AgentPrompt(), "Follow the evidence and cite every conclusion."; got != want {
		t.Errorf("AgentPrompt = %q, want %q", got, want)
	}
}

func TestDecodeSkillPromptRejectsInvalidSkillFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "missing frontmatter", content: "Do the work.", want: "missing YAML frontmatter"},
		{name: "missing name", content: "---\ndescription: Test skill\n---\nDo the work.", want: "field `name` is required"},
		{name: "missing description", content: "---\nname: test\n---\nDo the work.", want: "field `description` is required"},
		{name: "missing body", content: "---\nname: test\ndescription: Test skill\n---\n", want: "instruction body is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "skills/test/SKILL.md"), tc.content)
			_, err := Decode(`
[workflow]
name = "x"
version = "1"

[[step]]
id = "test"
type = "agent"
skill = "skills/test"
`, dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
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
	mustWriteSkill(t, filepath.Join(dir, "s", "SKILL.md"), "# Skill")

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
output_type = "bool"
profile = "@interactive"
[[step.review]]
source = "diff"
label = "Code changes"
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

func mustWriteSkill(t *testing.T, path, body string) {
	t.Helper()
	name := filepath.Base(filepath.Dir(path))
	mustWrite(t, path, "---\nname: "+name+"\ndescription: Test skill fixture\n---\n"+body+"\n")
}

func TestProductionExecutionControls(t *testing.T) {
	valid := `
[workflow]
name = "controls"
version = "1"
[defaults]
max_read_only = 2
max_mutating = 1
max_cost_usd = 4.5
max_security_findings = 2
[[step]]
id = "publish"
type = "command"
run = "true"
timeout = "30s"
isolation = "worktree"
mutation_paths = ["cmd/**", "go.mod"]
secrets = ["release_token"]
idempotent = true
  [step.retry]
  max_attempts = 3
  backoff = "exponential"
  initial_backoff = "1s"
  retry_on = ["timeout", "temporary"]
`
	wf, err := Decode(valid, "")
	if err != nil {
		t.Fatal(err)
	}
	step := wf.Steps[0]
	if step.Timeout.Duration.String() != "30s" || step.Retry.MaxAttempts != 3 {
		t.Fatalf("controls not decoded: %+v", step)
	}

	for _, tc := range []struct {
		name, old, new, want string
	}{
		{"retry requires idempotency", "idempotent = true", "idempotent = false", "automatic retries require idempotent = true"},
		{"bad retry class", "\"temporary\"]", "\"unknown\"]", "unknown classification"},
		{"unsafe mutation path", "\"go.mod\"]", "\"../go.mod\"]", "repository-relative path or glob"},
		{"secret must be name", "\"release_token\"]", "\"bad secret\"]", "must be a name, not a value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := strings.Replace(valid, tc.old, tc.new, 1)
			if _, err := Decode(broken, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// foreachBase is the worked [step.foreach] example from
// docs/plans/a8-dynamic-foreach-fan-out.md, minus the downstream synthesize
// step so individual tests can append their own consumer/route.
const foreachBase = `
[workflow]
name = "foreach"
version = "1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"

  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 32
  max_parallel = 4

  [step.schema]
  finding = "text"
  severity = { enum = ["low", "medium", "high"] }
`

func TestDecodeForEachValid(t *testing.T) {
	wf, err := Decode(foreachBase, "")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	analyze := wf.Steps[wf.index["analyze"]]
	if analyze.ForEach == nil {
		t.Fatal("analyze.ForEach is nil")
	}
	if analyze.ForEach.Items != "@discover.targets" || analyze.ForEach.As != "target" ||
		analyze.ForEach.MaxItems != 32 || analyze.ForEach.MaxParallel != 4 {
		t.Fatalf("ForEach = %+v", analyze.ForEach)
	}

	// The aggregate reference schema exposes the fixed fan-out contract, not
	// the template's own declared schema.
	agg := analyze.ReferenceSchema()
	for _, name := range []string{"count", "succeeded", "failed", "all_succeeded", "results"} {
		if _, ok := agg.lookup([]string{name}); !ok {
			t.Errorf("aggregate schema missing field %q", name)
		}
	}
	if _, ok := agg.lookup([]string{"finding"}); ok {
		t.Errorf("aggregate schema unexpectedly exposes template field %q", "finding")
	}
	// The template's own schema is still reachable for block_on / own-output
	// checks via EffectiveSchema.
	if _, ok := analyze.EffectiveSchema().lookup([]string{"finding"}); !ok {
		t.Error("EffectiveSchema missing declared template field \"finding\"")
	}
}

func TestDecodeForEachAggregateFieldRefs(t *testing.T) {
	// A consumer may compare an aggregate field and consume the whole ordered
	// results list, but not the bare family scalar.
	valid := foreachBase + `
[[step]]
id = "synthesize"
type = "agent"
depends_on = ["analyze"]
skill = "skills/synthesize"
inputs = ["@analyze.results"]
when = "analyze.all_succeeded == \"true\""
`
	if _, err := Decode(valid, ""); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	bareFamily := foreachBase + `
[[step]]
id = "synthesize"
type = "agent"
depends_on = ["analyze"]
skill = "skills/synthesize"
when = "analyze"
`
	_, err := Decode(bareFamily, "")
	if err == nil || !strings.Contains(err.Error(), "foreach family") {
		t.Fatalf("error = %v, want foreach family rejection", err)
	}
}

func TestDecodeForEachInvalid(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "items scalar source",
			toml: `
[workflow]
name = "foreach"
version = "1"
[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  count = "number"
[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.count"
  as = "target"
  max_items = 8
`,
			want: "must reference a list field, got number",
		},
		{
			name: "items object source",
			toml: `
[workflow]
name = "foreach"
version = "1"
[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  meta = { owner = "text" }
[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.meta"
  as = "target"
  max_items = 8
`,
			want: "must reference a list field, got object",
		},
		{
			name: "unknown producer",
			toml: `
[workflow]
name = "foreach"
version = "1"
[[step]]
id = "analyze"
type = "agent"
skill = "skills/analyze"
  [step.foreach]
  items = "@missing.targets"
  as = "target"
  max_items = 8
`,
			want: `references unknown step "missing"`,
		},
		{
			name: "producer not a direct dependency",
			toml: `
[workflow]
name = "foreach"
version = "1"
[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = "text" }
[[step]]
id = "analyze"
type = "agent"
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
`,
			want: "must also appear in depends_on",
		},
		{
			name: "bare step ref",
			toml: `
[workflow]
name = "foreach"
version = "1"
[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = "text" }
[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover"
  as = "target"
  max_items = 8
`,
			want: "must be an exact @step.field reference",
		},
		{
			name: "non-ref literal path",
			toml: `
[workflow]
name = "foreach"
version = "1"
[[step]]
id = "analyze"
type = "agent"
skill = "skills/analyze"
  [step.foreach]
  items = "targets.json"
  as = "target"
  max_items = 8
`,
			want: "must be an exact @step.field reference",
		},
		{
			name: "max_items zero",
			toml: strings.Replace(foreachBase, "max_items = 32", "max_items = 0", 1),
			want: "max_items must be >= 1",
		},
		{
			name: "max_parallel negative",
			toml: strings.Replace(foreachBase, "max_parallel = 4", "max_parallel = -1", 1),
			want: "max_parallel must be >= 0",
		},
		{
			name: "missing as",
			toml: strings.Replace(foreachBase, `as = "target"`, "", 1),
			want: "requires `as`",
		},
		{
			name: "as reserved metadata name",
			toml: strings.Replace(foreachBase, `as = "target"`, `as = "index"`, 1),
			want: "reserved fan-out metadata",
		},
		{
			name: "as collides with input",
			toml: strings.Replace(foreachBase,
				"skill = \"skills/analyze\"\n\n  [step.foreach]",
				"skill = \"skills/analyze\"\ninputs = [{ from = \"user\", label = \"note\", as = \"target\" }]\n\n  [step.foreach]",
				1),
			want: "collides with an input's `as`",
		},
		{
			name: "unsupported step type: review",
			toml: `
[workflow]
name = "foreach"
version = "1"
[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = "text" }
[[step]]
id = "analyze"
type = "review"
depends_on = ["discover"]
output_type = { enum = ["a", "b"] }
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
[[step.review]]
source = "diff"
label = "x"
`,
			want: "is only valid on agent or command steps",
		},
		{
			name: "route on template rejected",
			toml: foreachBase + `
[[step.route]]
when = "analyze == \"x\""
goto = "analyze"
max_iterations = 1
fallback = true
`,
			want: "cannot declare [[step.route]]",
		},
		{
			name: "fixed output rejected",
			toml: strings.Replace(foreachBase, `skill = "skills/analyze"`, "skill = \"skills/analyze\"\noutput = \"analyze.md\"", 1),
			want: "cannot declare a fixed `output` path",
		},
		{
			name: "from=user input rejected",
			toml: strings.Replace(foreachBase,
				"skill = \"skills/analyze\"\n\n  [step.foreach]",
				"skill = \"skills/analyze\"\ninputs = [{ from = \"user\", label = \"note\", as = \"note\" }]\n\n  [step.foreach]",
				1),
			want: "cannot declare a from=\"user\" input",
		},
		{
			name: "reserved fan-out id marker",
			toml: strings.Replace(foreachBase, `id = "analyze"`, `id = "analyze.__fanout__.g000"`, -1),
			want: "reserved marker",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.toml, "")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

// TestLoadForEachModuleNamespacing proves foreach.items is rewritten the same
// way inputs/reviews/conditions are when a foreach template lives inside a
// module: an internal producer reference gets the module instance prefix.
func TestLoadForEachModuleNamespacing(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "modules", "skills", "discover", "SKILL.md"), "# discover")
	mustWriteSkill(t, filepath.Join(dir, "modules", "skills", "analyze", "SKILL.md"), "# analyze")
	modulePath := filepath.Join(dir, "modules", "fanout.toml")
	mustWrite(t, modulePath, `
[module]
schema_version = 1

[module.exports.finding]
ref = "@analyze.results"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = "text" }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
`)
	rootPath := filepath.Join(dir, "workflow.toml")
	mustWrite(t, rootPath, `
[workflow]
name = "modules"
version = "1"

[[step]]
id = "spec"
type = "subworkflow"
module = "modules/fanout.toml"
`)

	wf, err := Load(rootPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	analyze := wf.Steps[wf.index["spec__analyze"]]
	if analyze.ForEach == nil {
		t.Fatal("spec__analyze.ForEach is nil")
	}
	if analyze.ForEach.Items != "@spec__discover.targets" {
		t.Fatalf("ForEach.Items = %q, want namespaced ref", analyze.ForEach.Items)
	}
	if !contains(analyze.DependsOn, "spec__discover") {
		t.Fatalf("depends_on = %v, want namespaced producer", analyze.DependsOn)
	}
}

// TestLoadForEachClonedAcrossSnapshot proves cloneStep deep-copies ForEach so
// mutating one workflow's snapshot never aliases another's.
func TestLoadForEachClonedAcrossSnapshot(t *testing.T) {
	wf, err := Decode(foreachBase, "")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	clone := RestoreExpanded(wf.Meta, wf.Defaults, wf.PublicSteps(), wf.Steps, wf.ModuleSources())
	clone.Steps[clone.index["analyze"]].ForEach.As = "mutated"
	if wf.Steps[wf.index["analyze"]].ForEach.As != "target" {
		t.Fatalf("original ForEach.As mutated to %q; clone is aliased", wf.Steps[wf.index["analyze"]].ForEach.As)
	}
}

// TestLoadRejectsForEachModuleInputBinding proves a module cannot source
// foreach.items from a declared module input: collection-valued module
// inputs are out of scope for this delivery (no FieldList ModuleValue type).
func TestLoadRejectsForEachModuleInputBinding(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills", "collect", "SKILL.md"), "# collect")
	mustWriteSkill(t, filepath.Join(dir, "modules", "skills", "analyze", "SKILL.md"), "# analyze")
	modulePath := filepath.Join(dir, "modules", "fanout.toml")
	mustWrite(t, modulePath, `
[module]
schema_version = 1

[module.inputs.targets]
type = "text"

[module.exports.finding]
ref = "@analyze.results"

[[step]]
id = "analyze"
type = "agent"
skill = "skills/analyze"
  [step.foreach]
  items = "@module.targets"
  as = "target"
  max_items = 8
`)
	rootPath := filepath.Join(dir, "workflow.toml")
	mustWrite(t, rootPath, `
[workflow]
name = "modules"
version = "1"

[[step]]
id = "collect"
type = "agent"
skill = "skills/collect"
  [step.schema]
  targets = { list = "text" }

[[step]]
id = "spec"
type = "subworkflow"
depends_on = ["collect"]
module = "modules/fanout.toml"
with = { targets = "@collect.targets" }
`)

	_, err := Load(rootPath)
	if err == nil || !strings.Contains(err.Error(), "collection-valued module inputs are not supported") {
		t.Fatalf("error = %v, want collection-valued module input rejection", err)
	}
}
