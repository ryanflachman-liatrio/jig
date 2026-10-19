package engine

import (
	"context"
	"sync"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// testExec is the package's shared Executor fake. Reach for it before writing
// a bespoke one; bespoke fakes are for tests that drive Reporter callbacks or
// hold dispatches open on channels.
//
// Each step's result comes from outcomes (a step without one succeeds after
// 1ms). respond, when set, replaces outcomes entirely and receives the step's
// zero-based call index. Every request is recorded either way.
type testExec struct {
	outcomes map[string]testOutcome
	respond  func(ctx context.Context, req StepRequest, call int) (*step.Result, error)

	mu    sync.Mutex
	reqs  []StepRequest
	calls map[string]int
}

// testOutcome scripts one step's result. The zero value succeeds immediately.
type testOutcome struct {
	delay      time.Duration
	fail       bool     // StatusFailed with failErr (default "scripted failure")
	failErr    string   //
	verdict    string   // Result.Verdict
	sessionID  string   // Result.SessionID
	structured []string // Result.Structured per call; the last is reused once exhausted
	outputPath string   // Result.OutputPath
	cost       float64  // Result.TotalCostUSD when non-zero
	tokens     int      // Result.Usage split across input/output buckets when non-zero
}

func (e *testExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	if e.calls == nil {
		e.calls = make(map[string]int)
	}
	call := e.calls[req.Step.ID]
	e.calls[req.Step.ID]++
	e.mu.Unlock()

	if e.respond != nil {
		return e.respond(ctx, req, call)
	}
	out, ok := e.outcomes[req.Step.ID]
	if !ok {
		out = testOutcome{delay: time.Millisecond}
	}
	return out.result(ctx, call)
}

func (out testOutcome) result(ctx context.Context, call int) (*step.Result, error) {
	if out.delay > 0 {
		select {
		case <-time.After(out.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r := &step.Result{Status: step.StatusSucceeded}
	if out.fail {
		r.Status = step.StatusFailed
		r.Err = out.failErr
		if r.Err == "" {
			r.Err = "scripted failure"
		}
	}
	r.Verdict = out.verdict
	r.SessionID = out.sessionID
	r.OutputPath = out.outputPath
	if n := len(out.structured); n > 0 {
		r.Structured = []byte(out.structured[min(call, n-1)])
	}
	if out.cost != 0 {
		cost := out.cost
		r.TotalCostUSD = &cost
	}
	if out.tokens != 0 {
		// Split the way backends report usage; TokenCount sums the buckets.
		usage := map[string]any{
			"input_tokens":  float64(out.tokens - out.tokens/4),
			"output_tokens": float64(out.tokens / 4),
		}
		r.Usage = &usage
	}
	return r, nil
}

// requestsFor returns every request dispatched for stepID, in dispatch order.
func (e *testExec) requestsFor(stepID string) []StepRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []StepRequest
	for _, r := range e.reqs {
		if r.Step.ID == stepID {
			out = append(out, r)
		}
	}
	return out
}

// lastInputs returns the Inputs of stepID's most recent dispatch.
func (e *testExec) lastInputs(stepID string) []ResolvedInput {
	reqs := e.requestsFor(stepID)
	if len(reqs) == 0 {
		return nil
	}
	return reqs[len(reqs)-1].Inputs
}

// callCount returns how many times stepID was dispatched.
func (e *testExec) callCount(stepID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[stepID]
}
