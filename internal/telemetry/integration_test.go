package telemetry

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// stubExecutor is a minimal engine.Executor used by the integration test.
// Every step succeeds immediately; this proves the exporter drains events
// off a real Manager without a full test scaffold.
type stubExecutor struct{}

func (stubExecutor) Execute(ctx context.Context, req engine.StepRequest, _ engine.Reporter) (*step.Result, error) {
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// TestAttachDrainsRealManager wires the exporter to a live Manager, runs a
// small workflow, and asserts the exporter recorded run + step metrics
// without deadlocking the scheduler.
func TestAttachDrainsRealManager(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	p := TestProviderWithReader(Config{Mode: ModeProm}, reader)
	p.tracer = tracenoop.NewTracerProvider()
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	ex, err := NewEventExporter(p)
	if err != nil {
		t.Fatalf("NewEventExporter: %v", err)
	}

	mgr := engine.NewManager(stubExecutor{}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stop := ex.Attach(ctx, mgr)
	defer stop()

	wf, err := workflow.Decode(`
[workflow]
name = "smoke"
version = "1"

[[step]]
id = "a"
type = "command"
run = "true"

[[step]]
id = "b"
type = "command"
depends_on = ["a"]
run = "true"
`, "")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	ex.RegisterWorkflow(run.ID, wf)

	waited := make(chan struct{})
	go func() {
		run.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
		t.Fatal("run did not finish before ctx expired")
	}

	// Allow the exporter's drain goroutine one more tick to consume the
	// terminal events before Collect().
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sumForCounter(t, reader, "jig.run.finished") == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := sumForCounter(t, reader, "jig.run.started"); got != 1 {
		t.Errorf("jig.run.started = %d, want 1", got)
	}
	if got := sumForCounter(t, reader, "jig.run.finished"); got != 1 {
		t.Errorf("jig.run.finished = %d, want 1", got)
	}
	if got := sumForCounter(t, reader, "jig.step.finished"); got != 2 {
		t.Errorf("jig.step.finished = %d, want 2 (a + b)", got)
	}
}

// TestAttachIsSafeWithNilManager guards the "always safe to construct" contract.
func TestAttachIsSafeWithNilManager(t *testing.T) {
	ex, _, _ := newTestExporter(t)
	stop := ex.Attach(context.Background(), nil)
	if stop == nil {
		t.Fatal("Attach returned nil stop func")
	}
	stop()
}

// TestNilExporterDoesNotPanic guards the nil-exporter safety contract.
func TestNilExporterDoesNotPanic(t *testing.T) {
	var ex *EventExporter
	ex.RegisterWorkflow("r", nil)
	ex.RecordDropped(context.Background(), "kind")
	stop := ex.Attach(context.Background(), nil)
	stop()
}
