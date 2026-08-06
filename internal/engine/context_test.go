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

  [step.loop]
  when           = "plan_review == 'revise'"
  goto           = "plan"
  max_iterations = 3
  feedback       = "@plan_review"

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

// TestBuildRequestInjectContextOff proves the inject_context opt-out: an agent
// step with the effective toggle off dispatches with an empty WorkflowContext
// (byte-identical to the no-context baseline), while a sibling with the toggle
// on still gets its assembled preamble.
func TestBuildRequestInjectContextOff(t *testing.T) {
	const toml = `
[workflow]
name = "fixture"
version = "1"

[[step]]
id = "a"
type = "agent"
skill = "skills/research"

[[step]]
id = "b"
type = "agent"
depends_on = ["a"]
skill = "skills/plan"
inject_context = false
inputs = ["@a.summary"]
`
	s := fixtureScheduler(t, toml)
	s.states["a"].Status = step.StatusSucceeded

	if got := s.buildRequest(s.stepByID("b"), "run1", "", "", "").WorkflowContext; got != "" {
		t.Errorf("inject_context = false: WorkflowContext = %q, want empty", got)
	}
	// Sanity: `a` (inject_context defaulted on) still gets a non-empty preamble,
	// so the empty result above is the toggle, not an assembly bug.
	if got := s.buildRequest(s.stepByID("a"), "run1", "", "", "").WorkflowContext; got == "" {
		t.Errorf("step a (inject_context default on) should have a non-empty preamble")
	}
}

const (
	// examplePreamblePath is the committed 2.0 proof (assembled `plan` preamble,
	// first-run form); reviseLoopPreamblePath is the 3.0 proof (revise iteration).
	examplePreamblePath     = "../../docs/specs/03-spec-step-context-assembly/03-proofs/2.0-plan-preamble.txt"
	reviseLoopPreamblePath  = "../../docs/specs/03-spec-step-context-assembly/03-proofs/3.0-revise-loop-preamble.txt"
	updatePreambleGoldenEnv = "UPDATE_PREAMBLE_GOLDEN"
)

// exampleScheduler decodes the real example (baseDir "../../examples", so it
// validates exactly like `jig validate`) and marks plan's three upstream deps
// succeeded — the shared setup for the assembled-preamble golden tests.
func exampleScheduler(t *testing.T) *scheduler {
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
	return s
}

// checkPreambleGolden compares got to the committed proof at path, regenerating
// it when UPDATE_PREAMBLE_GOLDEN is set.
func checkPreambleGolden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv(updatePreambleGoldenEnv) != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("preamble does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// TestBuildRequestPlanPreambleGolden locks the committed 2.0 proof to the
// engine's actual first-run render of the real example.
func TestBuildRequestPlanPreambleGolden(t *testing.T) {
	s := exampleScheduler(t)
	got := s.buildStepContext(s.stepByID("plan")).Render()
	checkPreambleGolden(t, examplePreamblePath, got)
}

// gateFixture: an agent gate (`qa`, output_type bool) loops back to `build` with
// no feedback ref — proving rerunSource is recorded outside the feedback block.
const gateFixture = `
[workflow]
name = "gatefix"
version = "1"

[[step]]
id = "build"
type = "agent"
skill = "skills/build"

[[step]]
id = "qa"
type = "agent"
depends_on = ["build"]
skill = "skills/qa"
output_type = "bool"

  [step.loop]
  when           = "qa == 'false'"
  goto           = "build"
  max_iterations = 2
`

// twoLoopsFixture: two loops (a review `qa1` and an agent gate `qa2`) whose goto
// both target `build` — the multiple-loops-target-one-step case (audit FLAG-1).
const twoLoopsFixture = `
[workflow]
name = "twoloops"
version = "1"

[[step]]
id = "build"
type = "agent"
skill = "skills/build"

[[step]]
id = "qa1"
type = "review"
depends_on = ["build"]
review = "@build.summary"
output_type = { enum = ["approve", "revise"] }

  [step.loop]
  when           = "qa1 == 'revise'"
  goto           = "build"
  max_iterations = 3

[[step]]
id = "qa2"
type = "agent"
depends_on = ["build"]
skill = "skills/qa"
output_type = "bool"

  [step.loop]
  when           = "qa2 == 'false'"
  goto           = "build"
  max_iterations = 2
`

// TestWorkflowContextReviseIteration drives a plan_review == 'revise' loop fire so
// `plan` re-dispatches at Iteration 1, then asserts the iteration clause and the
// revise State line.
func TestWorkflowContextReviseIteration(t *testing.T) {
	s := fixtureScheduler(t, planFixture)
	for _, id := range []string{"research_backend", "research_frontend", "security_scan"} {
		s.states[id].Status = step.StatusSucceeded
	}
	s.states["plan"].Status = step.StatusSucceeded
	s.states["plan_review"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "revise"}

	s.fireLoop("plan_review", s.stepByID("plan_review"))

	got := s.buildRequest(s.stepByID("plan"), "run1", "", "", "").WorkflowContext
	if !strings.Contains(got, "(iteration 2 of 3)") {
		t.Errorf("want `(iteration 2 of 3)`, got:\n%s", got)
	}
	if !strings.Contains(got, "State: re-running because `plan_review` requested revisions on the previous iteration. Address the reviewer feedback in your inputs.") {
		t.Errorf("want revise State line, got:\n%s", got)
	}
}

// TestWorkflowContextGateRerun proves an agent/command gate loop yields the
// gate-failure phrasing, and that rerunSource is recorded even with no feedback.
func TestWorkflowContextGateRerun(t *testing.T) {
	s := fixtureScheduler(t, gateFixture)
	s.states["build"].Status = step.StatusSucceeded
	s.states["qa"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "false"}

	s.fireLoop("qa", s.stepByID("qa"))

	if s.rerunSource["build"] != "qa" {
		t.Fatalf("rerunSource[build] = %q, want qa (recorded without a feedback ref)", s.rerunSource["build"])
	}
	got := s.buildStepContext(s.stepByID("build")).Render()
	if !strings.Contains(got, "State: re-running because the `qa` gate reported a failure. Address the feedback in your inputs.") {
		t.Errorf("want gate-failure State line, got:\n%s", got)
	}
	if !strings.Contains(got, "(iteration 2 of 2)") {
		t.Errorf("want `(iteration 2 of 2)`, got:\n%s", got)
	}
}

// TestWorkflowContextFirstRun asserts a first-run step (no rerunSource entry)
// renders no State line and no iteration clause.
func TestWorkflowContextFirstRun(t *testing.T) {
	s := fixtureScheduler(t, planFixture)
	for _, id := range []string{"research_backend", "research_frontend", "security_scan"} {
		s.states[id].Status = step.StatusSucceeded
	}
	got := s.buildStepContext(s.stepByID("plan")).Render()
	if strings.Contains(got, "iteration") {
		t.Errorf("first run must omit the iteration clause, got:\n%s", got)
	}
	if strings.Contains(got, "State:") {
		t.Errorf("first run must omit the State line, got:\n%s", got)
	}
}

// TestWorkflowContextMultipleLoops hardens the multiple-loops-target-one-step
// case (audit FLAG-1): two loops whose goto both target `build`, fired in
// sequence — the last-fired source drives RerunReason (last-write-wins).
func TestWorkflowContextMultipleLoops(t *testing.T) {
	s := fixtureScheduler(t, twoLoopsFixture)
	s.states["build"].Status = step.StatusSucceeded
	s.states["qa1"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "revise"}
	s.states["qa2"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "false"}

	// Fire the review loop, then the gate loop: the gate fired last.
	s.fireLoop("qa1", s.stepByID("qa1"))
	s.fireLoop("qa2", s.stepByID("qa2"))

	if s.rerunSource["build"] != "qa2" {
		t.Fatalf("rerunSource[build] = %q, want qa2 (last-fired wins)", s.rerunSource["build"])
	}
	got := s.buildStepContext(s.stepByID("build")).Render()
	if !strings.Contains(got, "State: re-running because the `qa2` gate reported a failure") {
		t.Errorf("want qa2 gate phrasing (last-fired), got:\n%s", got)
	}
	// qa1 legitimately appears as a downstream consumer of build; the assertion is
	// that its superseded *revise* phrasing does not drive the State line.
	if strings.Contains(got, "requested revisions") {
		t.Errorf("must not show the superseded qa1 revise phrasing in State, got:\n%s", got)
	}
	if !strings.Contains(got, "(iteration 2 of 2)") {
		t.Errorf("want qa2's cap `(iteration 2 of 2)`, got:\n%s", got)
	}
}

// TestBuildRequestReviseLoopPreambleGolden locks the committed 3.0 proof to the
// engine's render of the real example after a plan_review-triggered revise fire.
func TestBuildRequestReviseLoopPreambleGolden(t *testing.T) {
	s := exampleScheduler(t)
	s.states["plan"].Status = step.StatusSucceeded
	s.states["plan_review"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "revise"}
	s.fireLoop("plan_review", s.stepByID("plan_review"))

	got := s.buildStepContext(s.stepByID("plan")).Render()
	checkPreambleGolden(t, reviseLoopPreamblePath, got)
}
