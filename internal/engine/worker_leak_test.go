package engine

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// blockingExec parks every worker in Execute until its context is cancelled,
// then returns — so at cancel time every worker is in-flight and about to send
// its stepDoneMsg to the scheduler inbox.
type blockingExec struct{ running atomic.Int32 }

func (e *blockingExec) Execute(ctx context.Context, _ StepRequest, _ Reporter) (*step.Result, error) {
	e.running.Add(1)
	<-ctx.Done()
	return &step.Result{Status: step.StatusFailed, Err: "cancelled"}, nil
}

// TestNoWorkerLeakOnRunCancel proves that cancelling a run with more in-flight
// workers than the inbox buffer (64) does not leak worker goroutines. Each
// worker blocks in Execute, then tries to deliver its stepDoneMsg; once the
// scheduler has returned on ctx.Done nothing drains the inbox, so an
// unconditional send blocks the worker forever. The worker's send must select on
// the run context so it unwinds instead.
func TestNoWorkerLeakOnRunCancel(t *testing.T) {
	const n = 300 // well above the 64-slot inbox buffer

	var b strings.Builder
	b.WriteString("[workflow]\nname = \"leak\"\nversion = \"1.0\"\n")
	fmt.Fprintf(&b, "[defaults]\nmax_parallel = %d\n", n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "[[step]]\nid = \"s%d\"\ntype = \"command\"\nrun = \"true\"\n", i)
	}
	wf, err := workflow.Decode(b.String(), "")
	if err != nil {
		t.Fatal(err)
	}

	exec := &blockingExec{}
	mgr := NewManager(exec, "")
	_, _ = mgr.Subscribe()

	runtime.GC()
	base := runtime.NumGoroutine()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Wait until every worker is parked in Execute (all in-flight).
	deadline := time.After(5 * time.Second)
	for exec.running.Load() < n {
		select {
		case <-deadline:
			t.Fatalf("only %d/%d workers started", exec.running.Load(), n)
		case <-time.After(time.Millisecond):
		}
	}

	// Tear down the whole run; the scheduler stops draining the inbox.
	run.cancel()
	select {
	case <-run.done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not settle after cancel")
	}

	// Every worker goroutine must have exited; if the sends leaked we stay far
	// above baseline forever. Poll to give the exits time to be observed.
	leakDeadline := time.After(3 * time.Second)
	for {
		runtime.GC()
		got := runtime.NumGoroutine()
		if got <= base+20 {
			return // back to baseline: no leak
		}
		select {
		case <-leakDeadline:
			t.Fatalf("goroutine count stayed at %d (baseline %d) — ~%d worker sends leaked",
				got, base, got-base)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
