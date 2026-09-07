package headless

import (
	"strings"
	"testing"

	"jig/internal/engine"
	"jig/internal/workflow"
)

func TestInventoryGates(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "hostile"
version = "0.1"

[[step]]
id = "prep"
type = "command"
run = "echo x"

[[step]]
id = "gate"
type = "review"
depends_on = ["prep"]
output_type = { enum = ["a", "b"] }

[[step.review]]
source = "diff"
label = "x"

[[step]]
id = "ask"
type = "agent"
skill = "ask"
profile = "@interactive"
depends_on = ["gate"]

[[step]]
id = "chat"
type = "agent"
skill = "chat"
block_on = "chat.needs_input"
depends_on = ["ask"]

  [step.schema]
  needs_input = "bool"

[[step]]
id = "prompted"
type = "agent"
skill = "p"
depends_on = ["chat"]

[[step.inputs]]
from = "user"
label = "Name"
as = "name"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	got := inventoryGates(wf)
	kinds := map[string]bool{}
	for _, g := range got {
		kinds[g.Kind] = true
	}
	for _, want := range []string{"review", "interactive", "block_on", "user_prompt"} {
		if !kinds[want] {
			t.Errorf("missing kind %q in %#v", want, got)
		}
	}
}

func TestPolicy_ReviewIsGate(t *testing.T) {
	p := &Policy{}
	err := p.Handle(nil, engine.ReviewRequest{StepID: "g", RunID: "r"})
	ge, ok := err.(*GateError)
	if !ok || ge.Code != "gate_review" {
		t.Fatalf("got %v", err)
	}
}

func TestPolicy_MergeRequiresFlags(t *testing.T) {
	p := &Policy{}
	err := p.Handle(nil, engine.FinalMergeRequest{RunID: "r"})
	ge, ok := err.(*GateError)
	if !ok || ge.Code != "gate_merge" {
		t.Fatalf("got %v", err)
	}
}

func TestEffectiveCIFlags(t *testing.T) {
	s := EffectiveCIFlags(OutputJSON, true, "45m0s")
	for _, part := range []string{"--output", "json", "--discard-merge", "--on-recovery", "abort", "--timeout"} {
		if !strings.Contains(s, part) {
			t.Fatalf("missing %q in %s", part, s)
		}
	}
}
