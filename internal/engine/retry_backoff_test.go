package engine

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// TestRetryExponentialBackoffDelaysRedispatch runs in a synctest bubble, whose
// fake clock drives both the readiness check (time.Now) and the backoff wake
// timer, so the test asserts exact delays without sleeping.
func TestRetryExponentialBackoffDelaysRedispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wf, err := workflow.Decode(`
[workflow]
name = "backoff"
version = "1"
[[step]]
id = "flaky"
type = "command"
run = "false"
idempotent = true
  [step.retry]
  max_attempts = 3
  backoff = "exponential"
  initial_backoff = "10s"
  retry_on = ["exit_failure"]
`, "")
		if err != nil {
			t.Fatal(err)
		}
		var (
			mu     sync.Mutex
			starts []time.Time
		)
		exec := &testExec{respond: func(_ context.Context, _ StepRequest, call int) (*step.Result, error) {
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			if call < 2 {
				return &step.Result{Status: step.StatusFailed, Err: "exit 1"}, nil
			}
			return &step.Result{Status: step.StatusSucceeded}, nil
		}}
		mgr := NewManager(exec, "")
		_, ch := mgr.Subscribe()
		if _, err := mgr.Start(wf); err != nil {
			t.Fatal(err)
		}
		for e := range ch {
			if rf, ok := e.(RunFinished); ok {
				if rf.Failed {
					t.Fatal("run failed; want success on the third attempt")
				}
				break
			}
		}

		mu.Lock()
		defer mu.Unlock()
		if len(starts) != 3 {
			t.Fatalf("attempts = %d, want 3", len(starts))
		}
		// attempt 2 waits initial_backoff, attempt 3 waits double that.
		for i, want := range []time.Duration{10 * time.Second, 20 * time.Second} {
			if got := starts[i+1].Sub(starts[i]); got != want {
				t.Errorf("delay before attempt %d = %v, want %v", i+2, got, want)
			}
		}
	})
}
