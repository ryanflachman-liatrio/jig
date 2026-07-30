package runner

import (
	"context"
	"strings"
	"testing"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// noopReporter satisfies engine.Reporter without doing anything.
type noopReporter struct{ deltas []string }

func (r *noopReporter) Output(delta string)          { r.deltas = append(r.deltas, delta) }
func (r *noopReporter) ToolCall(tool, detail string) {}

func TestCommandExecutor_Success(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "hi",
			Type: workflow.StepCommand,
			Run:  "echo hello",
		},
	}
	rep := &noopReporter{}
	result, err := exec.Execute(context.Background(), req, rep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Status != step.StatusSucceeded {
		t.Errorf("want succeeded, got %q (err: %q)", result.Status, result.Err)
	}
	// Output should have been streamed via the reporter.
	combined := strings.Join(rep.deltas, "")
	if !strings.Contains(combined, "hello") {
		t.Errorf("expected 'hello' in output, got %q", combined)
	}
}

func TestCommandExecutor_Failure(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "bad",
			Type: workflow.StepCommand,
			Run:  "exit 1",
		},
	}
	result, err := exec.Execute(context.Background(), req, &noopReporter{})
	if err != nil {
		t.Fatalf("unexpected error (want nil, step failure expressed in result): %v", err)
	}
	if result.Status != step.StatusFailed {
		t.Errorf("want failed, got %q", result.Status)
	}
}

func TestCommandExecutor_MultilineScript(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:     "script",
			Type:   workflow.StepCommand,
			Script: "echo line1\necho line2",
		},
	}
	rep := &noopReporter{}
	result, err := exec.Execute(context.Background(), req, rep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != step.StatusSucceeded {
		t.Errorf("want succeeded, got %q", result.Status)
	}
	combined := strings.Join(rep.deltas, "")
	if !strings.Contains(combined, "line1") || !strings.Contains(combined, "line2") {
		t.Errorf("expected both lines in output, got %q", combined)
	}
}

func TestCommandExecutor_ContextCancellation(t *testing.T) {
	exec := NewCommandExecutor("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:  "slow",
			Run: "sleep 10",
		},
	}
	result, err := exec.Execute(ctx, req, &noopReporter{})
	// Either an error or a failed result is acceptable; the important thing is
	// that Execute returns (not hangs) and does not report success.
	if err == nil && result != nil && result.Status == step.StatusSucceeded {
		t.Error("cancelled execution should not succeed")
	}
}

func TestCommandExecutor_NeitherRunNorScript(t *testing.T) {
	exec := NewCommandExecutor("")
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID:   "empty",
			Type: workflow.StepCommand,
		},
	}
	result, err := exec.Execute(context.Background(), req, &noopReporter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != step.StatusFailed {
		t.Errorf("want failed for empty command, got %q", result.Status)
	}
}
