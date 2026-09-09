package workflow

import (
	"path/filepath"
	"testing"
)

func TestLoadRewritesCompoundModuleConditionsAndCombinesRootGuards(t *testing.T) {
	dir := t.TempDir()
	mustWriteSkill(t, filepath.Join(dir, "skills", "gate", "SKILL.md"), "# gate")
	mustWriteSkill(t, filepath.Join(dir, "module-skills", "left", "SKILL.md"), "# left")
	mustWriteSkill(t, filepath.Join(dir, "module-skills", "right", "SKILL.md"), "# right")
	mustWrite(t, filepath.Join(dir, "module.toml"), `
[module]
schema_version = 1
[module.exports.ready]
ref = "@left.ready"
[module.exports.count]
ref = "@right.count"

[[step]]
id = "left"
type = "agent"
skill = "module-skills/left"
when = "gate.secondary"
  [step.schema]
  ready = "bool"

[[step]]
id = "right"
type = "agent"
skill = "module-skills/right"
  [step.schema]
  count = "number"
`)
	mustWrite(t, filepath.Join(dir, "workflow.toml"), `
[workflow]
name = "modules"
version = "1"

[[step]]
id = "gate"
type = "agent"
skill = "skills/gate"
  [step.schema]
  primary = "bool"
  secondary = "bool"

[[step]]
id = "toggle"
type = "command"
output_type = "bool"
run = "true"

[[step]]
id = "phase"
type = "subworkflow"
depends_on = ["gate", "toggle"]
when = "gate.primary || toggle"
module = "module.toml"

[[step]]
id = "consumer"
type = "command"
depends_on = ["phase"]
when = "phase.ready && phase.count >= 2"
run = "true"
`)

	wf, err := Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	left := wf.Steps[wf.index["phase__left"]]
	if want := "(gate.primary || toggle) && gate.secondary"; left.When != want {
		t.Fatalf("combined root guard = %q, want %q", left.When, want)
	}
	consumer := wf.Steps[wf.index["consumer"]]
	if want := "phase__left.ready && phase__right.count >= 2"; consumer.When != want {
		t.Fatalf("rewritten condition = %q, want %q", consumer.When, want)
	}
	for _, dep := range []string{"phase__left", "phase__right"} {
		if !contains(consumer.DependsOn, dep) {
			t.Errorf("consumer dependencies %v do not include %q", consumer.DependsOn, dep)
		}
	}
	if reparsed, err := ParseCondition(consumer.When); err != nil || reparsed.String() != consumer.When {
		t.Fatalf("rewritten condition did not round trip: parsed=%v err=%v", reparsed, err)
	}
}
