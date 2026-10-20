package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type d05AttemptFactory struct {
	mode                        string
	entered, release            chan struct{}
	bound, finalized, consumers atomic.Int32
	publish                     atomic.Bool
}

func (f *d05AttemptFactory) Execute(context.Context, StepRequest, Reporter) (*step.Result, error) {
	panic("unbound executor")
}
func (f *d05AttemptFactory) NewAttempt(req StepRequest) (Executor, error) {
	f.bound.Add(1)
	return &d05Attempt{owner: f, id: req.Step.ID}, nil
}

type d05Attempt struct {
	owner *d05AttemptFactory
	id    string
	main  bool
}

func (a *d05Attempt) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	a.main = true
	if a.owner.mode == "live container" {
		if err := req.ContainerLifecycle(ctx, step.ContainerPhase{Phase: "main", Name: "synthetic-main", DaemonID: "daemon", EndpointID: "endpoint", ImageID: "image", State: step.ContainerIntent}); err != nil {
			return nil, err
		}
	}
	if a.id == "consumer" {
		a.owner.consumers.Add(1)
	}
	if a.owner.mode == "main error" && a.id == "producer" {
		return nil, errors.New("main failed")
	}
	if a.owner.mode == "cancel" && a.id == "producer" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}
func (a *d05Attempt) Validate(context.Context, StepRequest, Reporter) (bool, string, error) {
	if !a.main {
		panic("validator lost attempt")
	}
	if a.owner.mode == "validator failure" {
		return false, "rejected", nil
	}
	if a.owner.mode == "uncertain" {
		return false, "unknown", &ExecutionUncertainError{}
	}
	return true, "accepted", nil
}
func (a *d05Attempt) Finalize(ctx, shutdown context.Context, _ StepRequest, _ Reporter, result *step.Result, publish bool) (*step.Result, error) {
	if !a.main {
		panic("finalized without main")
	}
	if shutdown.Err() != nil {
		panic("shutdown inherited cancellation")
	}
	if _, ok := shutdown.Deadline(); !ok {
		panic("unbounded shutdown")
	}
	if a.id == "consumer" {
		return result, nil
	}
	a.owner.finalized.Add(1)
	a.owner.publish.Store(publish)
	close(a.owner.entered)
	<-a.owner.release
	if a.owner.mode == "finalizer failure" {
		return result, errors.New("publication failed")
	}
	// Even a faulty finalizer must not erase uncertainty or turn a rejected
	// validation into success.
	return &step.Result{Status: step.StatusSucceeded, OutputPath: "published"}, nil
}
func TestD05AttemptFinalization(t *testing.T) {
	for _, mode := range []string{"success", "no validator", "main error", "validator failure", "finalizer failure", "uncertain", "cancel", "live container"} {
		t.Run(mode, func(t *testing.T) {
			gate := "[step.validate]\ncommand='true'\n"
			if mode == "no validator" || mode == "live container" {
				gate = ""
			}
			timeout := ""
			if mode == "cancel" {
				timeout = "timeout='20ms'\n"
			}
			wf, err := workflow.Decode("[workflow]\nname='attempt'\nversion='1'\n[[step]]\nid='producer'\ntype='command'\nrun='true'\n"+timeout+gate+"[[step]]\nid='consumer'\ntype='command'\nrun='true'\ndepends_on=['producer']\n", "")
			if err != nil {
				t.Fatal(err)
			}
			f := &d05AttemptFactory{mode: mode, entered: make(chan struct{}), release: make(chan struct{})}
			run, err := NewManager(f, "").Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			released := false
			t.Cleanup(func() {
				if !released {
					close(f.release)
				}
				run.Cancel()
				run.Wait()
			})
			waitBarrier(t, f.entered)
			if f.consumers.Load() != 0 || f.bound.Load() != 1 {
				t.Fatal("consumer dispatched before finalization")
			}
			wantPublish := mode == "success" || mode == "no validator" || mode == "finalizer failure"
			if f.publish.Load() != wantPublish {
				t.Fatalf("publish=%v, want %v", f.publish.Load(), wantPublish)
			}
			close(f.release)
			released = true
			want := step.StatusAwaitingRecovery
			if mode == "success" || mode == "no validator" {
				want = step.StatusSucceeded
			}
			waitForStepStatus(t, run, "producer", want, 3*time.Second)
			if want == step.StatusSucceeded {
				waitForStepStatus(t, run, "consumer", step.StatusSucceeded, 3*time.Second)
				if run.Snapshot().Steps[0].Result.OutputPath != "published" {
					t.Fatal("final output lost")
				}
			} else {
				result := run.Snapshot().Steps[0].Result
				if result.OutputPath != "" || f.consumers.Load() != 0 {
					t.Fatal("failed attempt published output")
				}
				if (mode == "uncertain" || mode == "live container") && !result.ExecutionUncertain {
					t.Fatal("finalizer erased uncertainty")
				}
				if mode == "validator failure" && result.Err != "rejected" {
					t.Fatal("finalizer erased validation failure")
				}
			}
			if f.finalized.Load() != 1 {
				t.Fatal("finalizer not called exactly once")
			}
		})
	}
}

func TestD05ValidationOwnsMainResult(t *testing.T) {
	cost := 1.0
	usage := map[string]any{"nested": map[string]any{"tokens": float64(2)}}
	result := &step.Result{Status: step.StatusSucceeded, Artifacts: map[string]string{"binary": "original"}, ChangedFiles: []string{"original"}, TotalCostUSD: &cost, Usage: &usage}
	inbox := make(chan schedMsg, 1)
	done := make(chan error, 1)
	go func() { done <- acknowledgeValidation(context.Background(), inbox, "producer", 1, result) }()
	msg := (<-inbox).(validationStartMsg)
	msg.reply <- nil
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	result.Artifacts["binary"] = "changed"
	result.ChangedFiles[0] = "changed"
	*result.TotalCostUSD = 9
	(*result.Usage)["nested"].(map[string]any)["tokens"] = float64(9)
	if msg.result.Artifacts["binary"] != "original" || msg.result.ChangedFiles[0] != "original" || *msg.result.TotalCostUSD != 1 || (*msg.result.Usage)["nested"].(map[string]any)["tokens"] != float64(2) {
		t.Fatal("finalization can mutate the acknowledged main result")
	}
}
