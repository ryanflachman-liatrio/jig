package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
)

func TestSecurityRecoveryWaitsForWorkerExit(t *testing.T) {
	for _, scenario := range []struct{ name, action, source string }{
		{"fanout_skip", RecoverSkip, singleChildTOML("")},
		{"fanout_retry", RecoverRetry, singleChildTOML("")},
		{"ordinary_skip", RecoverSkip, "[workflow]\nname='security'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n"},
		{"ordinary_retry", RecoverRetry, "[workflow]\nname='security'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			action := scenario.action
			wf, err := decodeEngineFixture(t, scenario.source)
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			cancelled := make(chan struct{})
			producer := oneTargetProducer()
			var calls atomic.Int32
			ex := captureExecFunc(func(ctx context.Context, r StepRequest, rep Reporter) (*step.Result, error) {
				if raw, ok := producer[r.Step.ID]; ok {
					return &step.Result{Status: step.StatusSucceeded, Structured: raw}, nil
				}
				starts := calls.Add(1)
				if starts > 1 {
					return &step.Result{Status: step.StatusSucceeded}, nil
				}
				rep.Finding(SecurityFinding{Tier: "guard", Monitor: "fixture", Severity: "critical", Action: "escalated", Fingerprint: "security-before-exit"})
				<-ctx.Done()
				close(cancelled)
				<-release
				return nil, ctx.Err()
			})
			manager := NewManager(ex, "")
			_, ctrl := manager.Subscribe()
			run, err := manager.Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { run.Cancel(); run.Wait() })
			// Releasing this barrier before cancelling avoids stranding our fixture.
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			var rr RecoveryRequest
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
		loop:
			for {
				select {
				case ev := <-ctrl:
					if e, ok := ev.(RecoveryRequest); ok {
						rr = e
						break loop
					}
				case <-timer.C:
					t.Fatal("no recovery park")
				}
			}
			select {
			case <-cancelled:
			case <-timer.C:
				t.Fatal("worker not cancelled")
			}
			run.Recover(rr.StepID, action, "")
			// Snapshot is a reliable inbox round trip after Recover: the command has
			// been processed, while the old worker is deliberately still alive.
			snap := run.Snapshot()
			for _, st := range snap.Steps {
				if st.ID == rr.StepID && st.Status != step.StatusAwaitingRecovery {
					t.Fatalf("recovery took effect before worker exit: %s", st.Status)
				}
			}
			close(release)
			snap = waitCaptureRun(t, run)
			if !snap.Done || (action == RecoverSkip) != snap.Failed {
				t.Fatalf("settlement=%+v", snap)
			}
			want := int32(1)
			if action == RecoverRetry {
				want = 2
			}
			if calls.Load() != want {
				t.Fatalf("starts=%d want=%d", calls.Load(), want)
			}
			t.Logf("%s: waited for cancelled worker, starts=%d; run settled", action, calls.Load())
		})
	}
}
