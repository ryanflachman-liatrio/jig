package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"jig/internal/step"
	"jig/internal/workflow"
)

// planFixture mirrors the `plan` neighborhood of examples/feature.toml: three
// upstream agents, a review consumer that reviews @plan.summary, a guarded agent
// consumer that reads @plan.tasks/@plan.approach, plus a non-dependency sibling
// and a downstream command step (for the leak / non-agent assertions). Decoded
// with baseDir "" so on-disk skill dirs are not required.
const planFixture = `
[workflow]
name = "fixture"
version = "1"

[[step]]
id = "research_backend"
type = "agent"
skill = "skills/research"
on_failure = "continue"

[[step]]
id = "research_frontend"
type = "agent"
skill = "skills/research"

[[step]]
id = "security_scan"
type = "agent"
skill = "skills/research"

[[step]]
id = "sibling"
type = "agent"
depends_on = ["research_backend"]
skill = "skills/research"

[[step]]
id = "plan"
type = "agent"
depends_on = ["research_backend", "research_frontend", "security_scan"]
skill = "skills/plan"
inputs = ["@research_backend.summary", "@research_frontend.summary", "@security_scan.summary"]

  [step.schema]
  approach = "text"
  tasks = { list = "text" }

[[step]]
id = "plan_review"
type = "review"
depends_on = ["plan"]
review = "@plan.summary"
output_type = { enum = ["approve", "revise"] }

[[step]]
id = "implement"
type = "agent"
depends_on = ["plan_review", "plan"]
when = "plan_review == 'approve'"
skill = "skills/implement"
inputs = ["@plan.tasks", { ref = "@plan.approach", inline = true }]

[[step]]
id = "lint"
type = "command"
depends_on = ["implement"]
run = "echo hi"
`

// fixtureScheduler decodes toml (baseDir "" — no file checks) and returns a
// minimal scheduler suitable for buildRequest / buildStepContext.
func fixtureScheduler(t *testing.T, toml string) *scheduler {
	t.Helper()
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newScheduler(wf, "test", make(chan schedMsg, 4), nil, nil, cancel, nil, "", "", "", func(RunSnapshot) {})
}

// TestBuildRequestWorkflowContext dispatches `plan` and asserts the assembled
// preamble names its upstream deps with dispatch-time statuses and its two
// downstream consumers with kind labels, consumed fields, and the guard clause.
func TestBuildRequestWorkflowContext(t *testing.T) {
	s := fixtureScheduler(t, planFixture)
	s.states["research_backend"].Status = step.StatusSucceeded
	s.states["research_frontend"].Status = step.StatusSucceeded
	// A failed dep with on_failure = "continue" still dispatches `plan`; the
	// preamble must report its real (failed) status, not assume succeeded.
	s.states["security_scan"].Status = step.StatusFailed

	req := s.buildRequest(s.stepByID("plan"), "run1", "", "", "")
	got := req.WorkflowContext

	wantLines := []string{
		"You are step `plan` in workflow `fixture`.",
		"Upstream (already complete):",
		"- `research_backend` (succeeded)",
		"- `research_frontend` (succeeded)",
		"- `security_scan` (failed)",
		"Downstream (what your output feeds):",
		"- `plan_review` (human review) — a person reviews your `summary`",
		"- `implement` (agent) — consumes your `tasks`, `approach` (conditional on `plan_review == 'approve'`)",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Errorf("preamble missing line %q\n--- got ---\n%s", line, got)
		}
	}
	// First run: no iteration clause, no State line.
	if strings.Contains(got, "iteration") {
		t.Errorf("first-run preamble should have no iteration clause, got:\n%s", got)
	}
	if strings.Contains(got, "State:") {
		t.Errorf("first-run preamble should have no State line, got:\n%s", got)
	}
}

// TestBuildRequestNoSiblingLeak proves assembly is framing-only: no
// non-dependency sibling ids and no upstream artifact bodies bleed in.
func TestBuildRequestNoSiblingLeak(t *testing.T) {
	s := fixtureScheduler(t, planFixture)
	for _, id := range []string{"research_backend", "research_frontend", "security_scan"} {
		s.states[id].Status = step.StatusSucceeded
		// A plausible artifact body that must NOT be inlined into the preamble.
		s.states[id].Result = &step.Result{Structured: []byte(`{"summary":"SECRET_BODY_TEXT"}`)}
	}
	got := s.buildRequest(s.stepByID("plan"), "run1", "", "", "").WorkflowContext

	// `sibling` depends on research_backend (not plan) and `lint` depends on
	// implement (not plan): neither is a neighbor of plan.
	for _, absent := range []string{"sibling", "lint", "SECRET_BODY_TEXT"} {
		if strings.Contains(got, absent) {
			t.Errorf("preamble leaked %q (framing-only violated):\n%s", absent, got)
		}
	}
}

// TestBuildRequestNonAgentEmpty asserts command and review steps dispatch with
// an empty WorkflowContext (the preamble is agent-only).
func TestBuildRequestNonAgentEmpty(t *testing.T) {
	s := fixtureScheduler(t, planFixture)
	for _, id := range []string{"lint", "plan_review"} {
		req := s.buildRequest(s.stepByID(id), "run1", "", "", "")
		if req.WorkflowContext != "" {
			t.Errorf("non-agent step %q: WorkflowContext = %q, want empty", id, req.WorkflowContext)
		}
	}
}

// examplePreamblePath is the committed proof artifact for the assembled `plan`
// preamble of examples/feature.toml (first run form).
const examplePreamblePath = "../../docs/specs/03-spec-step-context-assembly/03-proofs/2.0-plan-preamble.txt"

// renderExamplePlanPreamble decodes the real example (baseDir "../../examples",
// so it validates exactly like `jig validate`), marks plan's three upstream deps
// succeeded, and renders the plan preamble.
func renderExamplePlanPreamble(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../examples/feature.toml")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	wf, err := workflow.Decode(string(data), "../../examples")
	if err != nil {
		t.Fatalf("decode example: %v", err)
	}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := newScheduler(wf, "test", make(chan schedMsg, 4), nil, nil, cancel, nil, "", "", "", func(RunSnapshot) {})
	for _, id := range []string{"research_backend", "research_frontend", "security_scan"} {
		s.states[id].Status = step.StatusSucceeded
	}
	return s.buildStepContext(s.stepByID("plan")).Render()
}

// TestBuildRequestPlanPreambleGolden locks the committed 2.0 proof artifact to
// the engine's actual render of the real example. Set UPDATE_PREAMBLE_GOLDEN=1
// to regenerate the file after an intentional format change.
func TestBuildRequestPlanPreambleGolden(t *testing.T) {
	got := renderExamplePlanPreamble(t)
	if os.Getenv("UPDATE_PREAMBLE_GOLDEN") != "" {
		if err := os.WriteFile(examplePreamblePath, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(examplePreamblePath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("plan preamble does not match %s\n--- got ---\n%s\n--- want ---\n%s", examplePreamblePath, got, string(want))
	}
}
