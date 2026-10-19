package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// TestDispatchUnknownStepTypeFailsClosed proves a step whose type has no
// dispatch strategy fails into recovery instead of hanging the run. Validation
// rejects such workflows, so the test mutates a decoded one.
func TestDispatchUnknownStepTypeFailsClosed(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "unknown-type"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	wf.Steps[0].Type = "bogus"

	mgr := NewManager(&testExec{}, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			if rr, ok := e.(RecoveryRequest); ok && rr.StepID == "a" {
				if !strings.Contains(rr.Err, "unsupported step type") {
					t.Errorf("recovery reason = %q, want unsupported step type", rr.Err)
				}
				run.Recover("a", RecoverAbort, "")
			}
			if rf, ok := e.(RunFinished); ok {
				if !rf.Failed {
					t.Error("run with an undispatchable step should fail")
				}
				return
			}
		case <-deadline:
			t.Fatal("step with unknown type hung instead of failing")
		}
	}
}
