// Package runner implements the engine.Executor interface. It contains the
// CommandExecutor (os/exec), AgentExecutor (Claude SDK), and FakeExecutor (tests
// and dry-run). Only FakeExecutor is wired in Phase 1.
package runner

import (
	"context"
	"time"

	"jig/internal/engine"
	"jig/internal/step"
)

// FakeOutcome describes the scripted result for one step.
type FakeOutcome struct {
	// Delay is how long to simulate execution. Zero uses DefaultDelay.
	Delay time.Duration
	// Fail makes the step fail instead of succeed.
	Fail bool
	// Deltas are optional streaming text fragments emitted before the result.
	Deltas []string
	// Result overrides the computed result when set.
	Result *step.Result
}

// DefaultDelay is the simulated execution time when FakeOutcome.Delay is zero.
const DefaultDelay = 500 * time.Millisecond

// FakeExecutor implements engine.Executor with scripted outcomes. It is the
// primary test double for the engine and the executor used in TUI dry-run mode.
// Per-step outcomes take precedence over the default; steps not in the map use
// def.
type FakeExecutor struct {
	outcomes map[string]FakeOutcome
	def      FakeOutcome
}

// NewFakeExecutor returns a FakeExecutor. outcomes maps step IDs to scripted
// results; def is used for any step ID not in the map.
func NewFakeExecutor(outcomes map[string]FakeOutcome, def FakeOutcome) *FakeExecutor {
	if outcomes == nil {
		outcomes = make(map[string]FakeOutcome)
	}
	return &FakeExecutor{outcomes: outcomes, def: def}
}

// Execute simulates running one step: streams any scripted deltas, waits for
// the scripted delay, then returns the scripted result.
func (f *FakeExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	out := f.def
	if o, ok := f.outcomes[req.Step.ID]; ok {
		out = o
	}

	// Emit streaming deltas before the step "finishes".
	for _, delta := range out.Deltas {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			rep.Output(delta)
		}
	}

	delay := out.Delay
	if delay <= 0 {
		delay = DefaultDelay
	}
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if out.Result != nil {
		return out.Result, nil
	}
	if out.Fail {
		return &step.Result{Status: step.StatusFailed, Err: "scripted failure"}, nil
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}
