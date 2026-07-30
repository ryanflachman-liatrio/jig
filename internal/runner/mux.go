package runner

import (
	"context"
	"fmt"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// Mux is an engine.Executor that routes each step to the appropriate
// underlying executor based on the step's Type field.  cmd/jig registers
// executors at startup so the engine remains decoupled from concrete
// implementations.
type Mux struct {
	executors map[workflow.StepType]engine.Executor
}

// NewMux returns an empty Mux.  Call Register to add executor implementations
// before passing the Mux to engine.NewManager.
func NewMux() *Mux {
	return &Mux{executors: make(map[workflow.StepType]engine.Executor)}
}

// Register associates a step type with its executor.  Registering the same
// type twice replaces the previous entry.
func (m *Mux) Register(t workflow.StepType, e engine.Executor) {
	m.executors[t] = e
}

// Execute dispatches to the registered executor for req.Step.Type.  It returns
// a failed result (not an error) when no executor is registered, so the engine
// can apply the step's failure policy rather than crashing.
func (m *Mux) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	e, ok := m.executors[req.Step.Type]
	if !ok {
		return &step.Result{
			Status: step.StatusFailed,
			Err:    fmt.Sprintf("no executor registered for step type %q", req.Step.Type),
		}, nil
	}
	return e.Execute(ctx, req, rep)
}
