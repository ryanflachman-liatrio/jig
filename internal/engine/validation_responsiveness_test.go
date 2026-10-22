package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// TestValidationGateProcess is invoked by the real validation command. The
// HTTP response is a barrier controlled by the parent test, not a timed sleep.
func TestValidationGateProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--jig-validation-gate" {
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Get(os.Args[len(os.Args)-1])
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("gate response = %s, want 204", response.Status)
	}
}

type validationFixture struct {
	run          *Run
	events       <-chan Event
	release      func()
	disconnected <-chan struct{}
	exec         *testExec
}

func newHeldValidation(t *testing.T, configure func(*workflow.Workflow)) validationFixture {
	t.Helper()
	entered := make(chan struct{})
	disconnected := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(disconnected)
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The gate runs through sh -c in production. Quote paths rather than
	// assuming the test binary and temporary directories contain no spaces.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	command := quote(executable) + " -test.run=^TestValidationGateProcess$ -- --jig-validation-gate " + quote(server.URL)
	wf, err := workflow.Decode(fmt.Sprintf(`
[workflow]
name = "validation-responsiveness"
version = "1"

[[step]]
id = "work"
type = "command"
run = "true"
[step.validate]
command = %q
`, command), "")
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(wf)
	}
	executor := &testExec{outcomes: map[string]testOutcome{"work": {}}}
	manager := NewManager(executor, "")
	_, events := manager.Subscribe()
	run, err := manager.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Even a failing assertion must release the real subprocess and let
		// the scheduler exit, rather than leaking a hanging validation gate.
		unblock()
		run.Cancel()
		done := make(chan struct{})
		go func() { run.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("run did not exit after releasing validation")
		}
	})

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("validation subprocess did not reach the controlled gate")
	}

	return validationFixture{run: run, events: events, release: unblock, disconnected: disconnected, exec: executor}
}

func TestScheduler_SnapshotResponsiveDuringValidation(t *testing.T) {
	fixture := newHeldValidation(t, nil)
	run := fixture.run
	snapshots := make(chan RunSnapshot, 1)
	go func() { snapshots <- run.Snapshot() }()
	select {
	case snapshot := <-snapshots:
		if len(snapshot.Steps) != 1 || snapshot.Steps[0].Status != step.StatusValidating {
			t.Fatalf("snapshot during held validation = %+v, want one validating step", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Snapshot blocked while validation was held open; scheduler must remain responsive")
	}
}

// Stopping validation must kill the child of sh -c without releasing the
// server barrier, then park the step rather than treating Stop as a failure.
func TestScheduler_StopDuringValidation(t *testing.T) {
	fixture := newHeldValidation(t, nil)
	fixture.run.Stop("work")
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-fixture.events:
			if status, ok := event.(StepStatus); ok && status.StepID == "work" && status.To == step.StatusStopped {
				select {
				case <-fixture.disconnected:
				case <-deadline:
					t.Fatal("stopped gate subprocess is still connected")
				}
				return
			}
		case <-deadline:
			t.Fatal("Stop did not terminate validation and park the step")
		}
	}
}

func TestScheduler_CancelDuringValidation(t *testing.T) {
	fixture := newHeldValidation(t, nil)
	fixture.run.Cancel()
	done := make(chan RunSnapshot, 1)
	go func() { done <- fixture.run.Wait() }()
	select {
	case snapshot := <-done:
		if snapshotStep(snapshot, "work").Status == step.StatusSucceeded {
			t.Fatal("canceled validation must not mark the step succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel did not terminate the run while validation was held open")
	}
	select {
	case <-fixture.disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("validation subprocess survived run cancellation")
	}
	events := collectEvents(t, fixture.events, 5*time.Second)
	finished := events[len(events)-1].(RunFinished)
	if !finished.Failed || finished.Cause != CauseCancelled {
		t.Fatalf("terminal event = %+v, want failed cancellation", finished)
	}
}

func TestScheduler_ValidationRetainsCapacity(t *testing.T) {
	fixture := newHeldValidation(t, func(wf *workflow.Workflow) {
		wf.Defaults.MaxParallel = 1
		wf.Steps = append(wf.Steps, workflow.Step{ID: "later", Type: workflow.StepCommand, Run: "true"})
	})
	snapshot := fixture.run.Snapshot()
	if got := snapshotStep(snapshot, "later").Status; got != step.StatusPending {
		t.Fatalf("second step = %s while validation holds the only slot, want pending", got)
	}
	if got := fixture.exec.callCount("later"); got != 0 {
		t.Fatalf("second worker dispatched %d times before validation completed", got)
	}
	fixture.release()
	done := make(chan RunSnapshot, 1)
	go func() { done <- fixture.run.Wait() }()
	select {
	case snapshot := <-done:
		if !snapshot.Done || snapshot.Failed || snapshotStep(snapshot, "later").Status != step.StatusSucceeded {
			t.Fatalf("run after validation release = %+v, want both steps succeeded", snapshot)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after validation released capacity")
	}
}

func TestScheduler_ValidationUsesAttemptDeadline(t *testing.T) {
	fixture := newHeldValidation(t, func(wf *workflow.Workflow) {
		wf.Steps[0].Timeout = workflow.Duration{Duration: 2 * time.Second}
	})
	recovery := waitRecoveryRequest(t, fixture.events, 5*time.Second)
	if recovery.StepID != "work" || !strings.Contains(recovery.Err, "timeout exceeded during validation") {
		t.Fatalf("recovery = %+v, want validation timeout", recovery)
	}
	select {
	case <-fixture.disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("validation subprocess survived the attempt deadline")
	}
}
