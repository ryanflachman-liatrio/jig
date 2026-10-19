package engine

import (
	"context"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// slowUnwindEscalationExec reports a critical finding, then returns only after
// the test releases it, so the worker's completion is guaranteed to arrive
// after the operator has already decided the parked step.
type slowUnwindEscalationExec struct{ release chan struct{} }

func (e *slowUnwindEscalationExec) Execute(ctx context.Context, _ StepRequest, rep Reporter) (*step.Result, error) {
	rep.Finding(SecurityFinding{Tier: "guard", Monitor: "secret-in-write", Severity: "critical", Action: "escalated", Fingerprint: "fp-reaped"})
	<-ctx.Done()
	<-e.release
	return nil, ctx.Err()
}

// TestReapedWorkerCompletionDoesNotReparkSettledStep proves a worker cancelled
// by a security escalation cannot re-park its step when its completion lands
// after the operator already skipped the step.
func TestReapedWorkerCompletionDoesNotReparkSettledStep(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "reaped"
version = "1"
[[step]]
id = "a"
type = "agent"
skill = "a"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &slowUnwindEscalationExec{release: make(chan struct{})}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	recoveries := 0
	released := false
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			switch e := e.(type) {
			case RecoveryRequest:
				recoveries++
				if recoveries == 1 {
					run.Recover("a", RecoverSkip, "")
				}
			case StepStatus:
				if e.StepID == "a" && e.To == step.StatusFailed && !released {
					released = true
					close(exec.release)
				}
			case RunFinished:
				if recoveries != 1 {
					t.Fatalf("step parked for recovery %d times, want 1: the reaped worker's late completion re-parked it", recoveries)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timeout (recoveries=%d); the reaped worker's completion re-parked the skipped step", recoveries)
		}
	}
}
