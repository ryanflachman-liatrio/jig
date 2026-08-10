package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// coalesceExec drives the parallel-loopers scenario: `implement` always
// succeeds; the gates `a` and `b` return a distinct "redo" verdict on the first
// wave (iteration 0) so their loops fire, then a passing verdict once re-run, so
// the run terminates. otherDelay staggers the two gates to prove the rewind
// waits for the slower sibling. Every StepRequest is recorded for inspection.
type coalesceExec struct {
	mu   sync.Mutex
	reqs []StepRequest
}

func (e *coalesceExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()

	switch req.Step.ID {
	case "a":
		if req.Iteration == 0 {
			_ = sleepCtx(ctx, time.Millisecond) // finishes first
			return &step.Result{Status: step.StatusSucceeded, Verdict: "redo-a"}, nil
		}
		return &step.Result{Status: step.StatusSucceeded, Verdict: "ok"}, nil
	case "b":
		if req.Iteration == 0 {
			_ = sleepCtx(ctx, 40*time.Millisecond) // the slow sibling
			return &step.Result{Status: step.StatusSucceeded, Verdict: "redo-b"}, nil
		}
		return &step.Result{Status: step.StatusSucceeded, Verdict: "ok"}, nil
	default: // implement
		return &step.Result{Status: step.StatusSucceeded}, nil
	}
}

func (e *coalesceExec) reqsFor(id string) []StepRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []StepRequest
	for _, r := range e.reqs {
		if r.Step.ID == id {
			out = append(out, r)
		}
	}
	return out
}

// TestScheduler_ParallelLoopsCoalesce proves that two parallel gates that both
// loop back to the same step (a) trigger exactly ONE rewind — not one per gate —
// and (b) feed the union of both gates' feedback into the re-run, not just the
// fastest sibling's. This is the coalescing barrier + feedback aggregation
// (options 1 & 2): the rewind waits for the slow sibling and merges outputs.
func TestScheduler_ParallelLoopsCoalesce(t *testing.T) {
	const toml = `
[workflow]
name = "coalesce"
version = "1"

[defaults]
max_parallel = 4

[[step]]
id = "implement"
type = "command"
run = "echo impl"

[[step]]
id = "a"
type = "command"
depends_on = ["implement"]
run = "echo a"
output_type = { enum = ["redo-a", "ok"] }

  [step.loop]
  when           = "a == 'redo-a'"
  goto           = "implement"
  max_iterations = 3
  feedback       = "@a"

[[step]]
id = "b"
type = "command"
depends_on = ["implement"]
run = "echo b"
output_type = { enum = ["redo-b", "ok"] }

  [step.loop]
  when           = "b == 'redo-b'"
  goto           = "implement"
  max_iterations = 3
  feedback       = "@b"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &coalesceExec{}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, ch, 10*time.Second)

	// The run terminates cleanly (both gates pass on the second wave).
	last := events[len(events)-1]
	if rf, ok := last.(RunFinished); !ok || rf.Failed {
		t.Fatalf("want clean RunFinished; got %v", last)
	}

	// implement ran exactly twice: the initial wave and ONE coalesced rewind — not
	// twice-rewound (which is the bug this fixes: each gate firing its own reset).
	implReqs := exec.reqsFor("implement")
	if len(implReqs) != 2 {
		t.Fatalf("implement dispatched %d times, want 2 (one coalesced rewind)", len(implReqs))
	}

	// The rewind carries BOTH gates' feedback, not just the faster one's.
	fb := implReqs[1].Feedback
	if !strings.Contains(fb, "From `a`") || !strings.Contains(fb, "redo-a") {
		t.Errorf("rewind feedback missing gate a's contribution:\n%s", fb)
	}
	if !strings.Contains(fb, "From `b`") || !strings.Contains(fb, "redo-b") {
		t.Errorf("rewind feedback missing gate b's contribution:\n%s", fb)
	}

	// Exactly one LoopFired per contributing gate for the single rewind (2 total),
	// all at iteration 1 — no double-rewind to iteration 2.
	var fires []LoopFired
	for _, e := range events {
		if lf, ok := e.(LoopFired); ok {
			fires = append(fires, lf)
		}
	}
	if len(fires) != 2 {
		t.Fatalf("want 2 LoopFired (one per gate, one rewind); got %d: %+v", len(fires), fires)
	}
	for _, lf := range fires {
		if lf.Iteration != 1 {
			t.Errorf("LoopFired at iteration %d, want 1 (single rewind)", lf.Iteration)
		}
	}
}
