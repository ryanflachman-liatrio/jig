package engine

import (
	"context"

	"jig/internal/step"
	"jig/internal/workflow"
)

// Executor is defined in engine (the consumer) and implemented in runner.
// Dependency inversion keeps engine free of os/exec and SDK imports.
type Executor interface {
	Execute(ctx context.Context, req StepRequest, report Reporter) (*step.Result, error)
}

// StepRequest is the data a worker needs to execute one step.
// @ref inputs are resolved to paths / inlined values before dispatch.
type StepRequest struct {
	RunID    string
	Step     *workflow.Step
	Inputs   []ResolvedInput
	Feedback string // loop feedback path, when re-running (Phase 3+)
	Worktree string // "" when isolation = none (Phase 5+)
}

// ResolvedInput pairs an original workflow.Input with its resolved value.
type ResolvedInput struct {
	Ref   workflow.Input
	Value string // resolved path, or inlined content when Ref.Inline is true
}

// Reporter carries live signals out of an in-flight execution back to the
// scheduler, which tags them with run/step IDs and routes them through emit().
type Reporter interface {
	Output(delta string)
	ToolCall(tool, detail string)
}
