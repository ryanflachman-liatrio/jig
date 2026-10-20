package engine

import (
	"context"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/step"
)

const readyFixture = `
[workflow]
name = "ready"
version = "1"

[[step]]
id = "flag"
type = "command"
output_type = "bool"
run = "true"

[[step]]
id = "guarded"
type = "command"
depends_on = ["flag"]
when = "flag"
run = "true"

[[step]]
id = "gate"
type = "check"
depends_on = ["flag"]
applies_when = "flag"
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "true"

  [step.findings]
  schema_version = 1
  file = "gate.json"
  required_tools = ["sh"]

[[step.route]]
when = "gate == 'fail' || gate == 'error'"
goto = "flag"
max_iterations = 2
`

func TestCheckWhenGuardSkipsAndMarksGuardSkip(t *testing.T) {
	s := fixtureScheduler(t, readyFixture)
	s.states["flag"].Status = step.StatusSucceeded
	s.states["flag"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "false"}

	if got := checkWhenGuard(s, context.Background(), s.stepByID("guarded")); got != readinessSkip {
		t.Fatalf("verdict = %d, want readinessSkip", got)
	}
	if s.states["guarded"].Status != step.StatusSkipped || !s.rt("guarded").skippedByGuard {
		t.Fatalf("guarded = %s (guard skip %v), want skipped by guard", s.states["guarded"].Status, s.rt("guarded").skippedByGuard)
	}
}

func TestCheckAppliesWhenSettlesNonApplicableGateAsSkipVerdict(t *testing.T) {
	s := fixtureScheduler(t, readyFixture)
	s.states["flag"].Status = step.StatusSucceeded
	s.states["flag"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "false"}

	if got := checkAppliesWhen(s, context.Background(), s.stepByID("gate")); got != readinessSkip {
		t.Fatalf("verdict = %d, want readinessSkip", got)
	}
	st := s.states["gate"]
	if st.Status != step.StatusSucceeded || st.Result == nil || st.Result.Verdict != "skip" {
		t.Fatalf("gate = %s %+v, want succeeded with verdict skip", st.Status, st.Result)
	}
}

func TestCheckWhenGuardPassesWhenTrue(t *testing.T) {
	s := fixtureScheduler(t, readyFixture)
	s.states["flag"].Status = step.StatusSucceeded
	s.states["flag"].Result = &step.Result{Status: step.StatusSucceeded, Verdict: "true"}

	if got := checkWhenGuard(s, context.Background(), s.stepByID("guarded")); got != readinessPass {
		t.Fatalf("verdict = %d, want readinessPass", got)
	}
}
