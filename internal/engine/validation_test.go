package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestRunGateBoundsCommandDiagnostics(t *testing.T) {
	// Produce twice the diagnostic limit and fail, exercising the real command
	// pipe rather than only the output buffer's Write method.
	passed, detail := runGate(context.Background(), &workflow.Step{Validate: &workflow.Validate{
		Command: `i=0; while [ "$i" -lt 1024 ]; do printf '%0128d' 0; i=$((i+1)); done; exit 1`,
	}}, "")
	if passed {
		t.Fatal("nonzero validation command must fail")
	}
	if !strings.Contains(detail, "[validation output truncated]") {
		t.Fatal("oversized diagnostic must report truncation")
	}
	if len(detail) > maxGateOutput+200 {
		t.Fatalf("diagnostic grew to %d bytes, want bounded output", len(detail))
	}
}

func TestValidationCompletionIgnoresSupersededWorker(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "stale-validation"
version = "1"
[[step]]
id = "work"
type = "command"
run = "true"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	s := newScheduler(schedulerDeps{wf: wf, inbox: make(chan schedMsg, 1)})
	s.states["work"].Status = step.StatusValidating
	current := &validationWork{}
	s.rt("work").validation = current
	s.acquireSlots(s.stepByID("work"))
	(validationDoneMsg{stepID: "work", owner: &validationWork{}, passed: true}).execute(s)
	if s.slots.inFlight != 1 || s.rt("work").validation != current || s.states["work"].Status != step.StatusValidating {
		t.Fatal("superseded validation changed the current execution or its capacity")
	}
}
