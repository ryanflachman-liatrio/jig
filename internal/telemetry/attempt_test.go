package telemetry_test

import (
	"context"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/runner"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/telemetry"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type d05Factory struct{}

func (*d05Factory) Execute(context.Context, engine.StepRequest, engine.Reporter) (*step.Result, error) {
	panic("unbound")
}
func (*d05Factory) NewAttempt(engine.StepRequest) (engine.Executor, error) { return &d05Handle{}, nil }

type d05Handle struct{ executed, validated bool }

func (h *d05Handle) Execute(context.Context, engine.StepRequest, engine.Reporter) (*step.Result, error) {
	h.executed = true
	return &step.Result{Status: step.StatusSucceeded}, nil
}
func (h *d05Handle) Validate(context.Context, engine.StepRequest, engine.Reporter) (bool, string, error) {
	h.validated = h.executed
	return h.validated, "", nil
}
func (h *d05Handle) Finalize(_ context.Context, _ context.Context, _ engine.StepRequest, _ engine.Reporter, result *step.Result, publish bool) (*step.Result, error) {
	if h.validated && publish {
		result.OutputPath = "finalized"
	}
	return result, nil
}
func TestD05MuxPreservesAttempt(t *testing.T) {
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, &d05Factory{})
	metric := telemetry.NewMetricMux(mux, nil, nil)
	req := engine.StepRequest{Step: &workflow.Step{ID: "command", Type: workflow.StepCommand}}
	first, err := engine.BindAttempt(metric, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.BindAttempt(metric, req)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result, err := first.Execute(ctx, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	passed, _, err := first.(engine.Validator).Validate(ctx, req, nil)
	if err != nil || !passed {
		t.Fatal("bound validation lost main state", err)
	}
	passed, _, _ = second.(engine.Validator).Validate(ctx, req, nil)
	if passed {
		t.Fatal("attempts share state")
	}
	result, err = first.(engine.AttemptFinalizer).Finalize(ctx, ctx, req, nil, result, true)
	if err != nil || result.OutputPath != "finalized" {
		t.Fatal("finalization capability lost", err)
	}
}
