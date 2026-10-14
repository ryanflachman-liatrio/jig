package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type validationBarrierExec struct {
	entered, cancelled, release    chan struct{}
	mainDeadline, validateDeadline time.Time
}

func (e *validationBarrierExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	if req.Step.ID == "worker" {
		e.mainDeadline, _ = ctx.Deadline()
	}
	return &step.Result{Status: step.StatusSucceeded, SessionID: "synthetic-session", Structured: []byte(`{"main":"normalized"}`)}, nil
}
func (e *validationBarrierExec) Validate(ctx context.Context, _ StepRequest, _ Reporter) (bool, string, error) {
	e.validateDeadline, _ = ctx.Deadline()
	close(e.entered)
	<-ctx.Done()
	close(e.cancelled)
	<-e.release
	return false, "validator cancelled", ctx.Err()
}
func waitBarrier(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("barrier timeout")
	}
}

func TestD02ValidationRetainsCapacityUntilWorkerExit(t *testing.T) {
	for _, mode := range []string{"stop", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			deadline := ""
			if mode == "timeout" {
				deadline = "timeout='100ms'\n"
			}
			wf, err := workflow.Decode("[workflow]\nname='d02'\nversion='1'\n[defaults]\nmax_parallel=1\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n"+deadline+"[step.validate]\ncommand='true'\n[[step]]\nid='sibling'\ntype='command'\nrun='true'\n", "")
			if err != nil {
				t.Fatal(err)
			}
			e := &validationBarrierExec{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
			run, err := NewManager(e, "").Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { run.Cancel(); run.Wait() })
			released := false
			t.Cleanup(func() {
				if !released {
					close(e.release)
				}
			})
			waitBarrier(t, e.entered)
			snapshot := run.Snapshot()
			if snapshot.Steps[0].Status != step.StatusValidating || snapshot.Steps[0].Result.SessionID != "synthetic-session" || string(snapshot.Steps[0].Result.Structured) != `{"main":"normalized"}` {
				t.Fatalf("main result not preserved before validation: %+v", snapshot)
			}
			if mode == "stop" {
				run.Stop("worker")
			}
			waitBarrier(t, e.cancelled)
			response := make(chan RunSnapshot, 1)
			go func() { response <- run.Snapshot() }()
			select {
			case snapshot = <-response:
			case <-time.After(time.Second):
				t.Fatal("scheduler blocked by validator")
			}
			if snapshot.Steps[0].Status != step.StatusValidating || snapshot.Steps[1].Status != step.StatusPending {
				t.Fatalf("slot released before worker exit: %+v", snapshot)
			}
			if !e.mainDeadline.Equal(e.validateDeadline) {
				t.Fatal("validation received a new attempt deadline")
			}
			close(e.release)
			released = true
			want := step.StatusStopped
			if mode == "timeout" {
				want = step.StatusAwaitingRecovery
			}
			waitForStepStatus(t, run, "worker", want, 2*time.Second)
			waitForStepStatus(t, run, "sibling", step.StatusSucceeded, 2*time.Second)
			if mode == "timeout" {
				snap := run.Snapshot()
				if snap.Steps[0].Result.Err != "step timeout exceeded" {
					t.Fatalf("timeout conflated with stop: %+v", snap.Steps[0].Result)
				}
			}
		})
	}
}

func validationSchedulerFixture(t *testing.T) *scheduler {
	t.Helper()
	wf, err := workflow.Decode("[workflow]\nname='d02'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n[step.validate]\ncommand='true'\n", "")
	if err != nil {
		t.Fatal(err)
	}
	s := newScheduler(wf, "fixture", make(chan schedMsg, 8), nil, nil, func() {}, nil, "", "", "", func(RunSnapshot) {})
	s.states["worker"].Status = step.StatusRunning
	s.inFlight = 1
	s.readOnlyInFlight = 1
	s.activeExecutions = map[string]uint64{"worker": 2}
	s.stepCancels["worker"] = func() {}
	return s
}
func TestD02StaleReportsCannotMutateOrReleaseReplacement(t *testing.T) {
	s := validationSchedulerFixture(t)
	state := s.states["worker"]
	result := &step.Result{Status: step.StatusSucceeded, SessionID: "STALE"}
	stepDoneMsg{stepID: "worker", execution: 1, result: result}.execute(s)
	reply := make(chan error, 1)
	validationStartMsg{stepID: "worker", execution: 1, result: result, reply: reply}.execute(s)
	if <-reply == nil {
		t.Fatal("stale validation authorized")
	}
	if s.inFlight != 1 || s.readOnlyInFlight != 1 || s.stepCancels["worker"] == nil || state.Result != nil || state.Status != step.StatusRunning {
		t.Fatal("stale report altered replacement")
	}
	// Duplicate terminal reports cannot debit the already released slot again.
	// Park to avoid running success handlers in this isolated fixture.
	state.Status = step.StatusAwaitingRecovery
	stepDoneMsg{stepID: "worker", execution: 2, result: result}.execute(s)
	stepDoneMsg{stepID: "worker", execution: 2, result: result}.execute(s)
	if s.inFlight != 0 || s.readOnlyInFlight != 0 {
		t.Fatal("duplicate completion released capacity twice")
	}
}
func TestD02ValidationAcknowledgementFailsClosed(t *testing.T) {
	for _, mode := range []string{"journal", "session"} {
		t.Run(mode, func(t *testing.T) {
			s := validationSchedulerFixture(t)
			rd, err := datastore.RunDir(t.TempDir(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			s.runDir = rd
			if mode == "journal" {
				w, err := manifest.NewWriter(rd)
				if err != nil {
					t.Fatal(err)
				}
				s.writer = w
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				dir, err := datastore.StepDir(rd, "worker")
				if err != nil {
					t.Fatal(err)
				}
				// A nonempty directory cannot be replaced by session.json's atomic rename.
				if err := os.MkdirAll(filepath.Join(dir, "session.json"), 0700); err != nil {
					t.Fatal(err)
				}
				writeCaptureFixture(t, dir, "session.json/occupied", []byte("fixture"))
			}
			reply := make(chan error, 1)
			validationStartMsg{stepID: "worker", execution: 2, result: &step.Result{Status: step.StatusSucceeded, SessionID: "fixture"}, reply: reply}.execute(s)
			err = <-reply
			if err == nil || s.states["worker"].Status != step.StatusRunning || s.inFlight != 1 {
				t.Fatalf("validation authorized despite failed %s: %v", mode, err)
			}
			if mode == "session" && !strings.Contains(err.Error(), "persist session before validation") {
				t.Fatal(err)
			}
		})
	}
}
func TestD02UncertainExecutionFencesReplacementAndCleanup(t *testing.T) {
	s := validationSchedulerFixture(t)
	stepDoneMsg{stepID: "worker", execution: 2, result: &step.Result{Status: step.StatusFailed, ExecutionUncertain: true, Err: "managed termination uncertain"}}.execute(s)
	for _, action := range []string{RecoverRetry, RecoverResume, RecoverSkip} {
		s.handleRecover(recoverMsg{stepID: "worker", action: action})
	}
	if s.states["worker"].Status != step.StatusAwaitingRecovery || s.inFlight != 1 || s.stepCancels["worker"] == nil {
		t.Fatal("uncertain workload accepted/replaced or released")
	}
	s.runWorktree = "retained-fixture-root"
	s.cleanupWorktrees()
	if s.runWorktree == "" {
		t.Fatal("uncertain execution root deleted")
	}
}

func TestD02RunCancellationWaitsForValidatorExit(t *testing.T) {
	wf, err := workflow.Decode("[workflow]\nname='d02'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n[step.validate]\ncommand='true'\n", "")
	if err != nil {
		t.Fatal(err)
	}
	e := &validationBarrierExec{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	run, err := NewManager(e, "").Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			close(e.release)
		}
		run.Cancel()
		run.Wait()
	})
	waitBarrier(t, e.entered)
	run.Cancel()
	waitBarrier(t, e.cancelled)
	done := make(chan RunSnapshot, 1)
	go func() { done <- run.Wait() }()
	select {
	case <-done:
		t.Fatal("run teardown finished before validator exit")
	default:
	}
	close(e.release)
	released = true
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not finish after validator exit")
	}
}

func TestD02PreparationConsumesAttemptDeadline(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	wf, err := workflow.Decode("[workflow]\nname='d02-preparation'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\ntimeout='1ns'\n", "")
	if err != nil {
		t.Fatal(err)
	}
	e := newStopTestExec()
	mgr := NewManager(e, filepath.Join(repo, ".jig"))
	_, events := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	collected := collectEventsAborting(t, events, run, 5*time.Second)
	if len(e.requests("worker")) != 0 {
		t.Fatal("main executed after preparation deadline")
	}
	timeout := false
	for _, event := range collected {
		if status, ok := event.(StepStatus); ok && status.StepID == "worker" && status.Err == "step timeout exceeded" {
			timeout = true
		}
	}
	if !timeout {
		t.Fatal("preparation timeout lost classification")
	}
}

func TestD02CapacityReleaseIncludesFamilyAndClass(t *testing.T) {
	for _, isolation := range []workflow.Isolation{workflow.IsolationNone, workflow.IsolationWorktree} {
		t.Run(string(isolation), func(t *testing.T) {
			s := validationSchedulerFixture(t)
			st := s.stepByID("worker")
			st.Isolation = isolation
			st.ResourceClass = "bounded"
			s.classInFlight["bounded"] = 1
			s.states["worker"].ParentID = "family"
			s.fanOutFamilies["family"] = &fanOutFamily{InFlight: 1}
			if isolation == workflow.IsolationWorktree {
				s.readOnlyInFlight = 0
				s.mutatingInFlight = 1
			}
			s.releaseWorkerCapacity("worker")
			if s.inFlight != 0 || s.readOnlyInFlight != 0 || s.mutatingInFlight != 0 || s.classInFlight["bounded"] != 0 || s.fanOutFamilies["family"].InFlight != 0 {
				t.Fatal("capacity leak")
			}
		})
	}
}
