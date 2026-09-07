package engine

import (
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

func TestRunWait_ReturnsFinalSnapshot(t *testing.T) {
	const toml = `
[workflow]
name = "wait-ok"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo a"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&testExec{outcomes: map[string]testOutcome{
		"a": {delay: time.Millisecond},
	}}, "")
	live, ctrl := mgr.Subscribe()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-live:
			case <-ctrl:
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stop) })

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	snap := run.Wait()
	if snap.ID != run.ID {
		t.Fatalf("snap.ID=%q run.ID=%q", snap.ID, run.ID)
	}
	if snap.Failed {
		t.Fatalf("want !Failed, got %+v", snap)
	}
}

func TestRunWait_AfterCancel(t *testing.T) {
	const toml = `
[workflow]
name = "wait-cancel"
version = "0.1"

[[step]]
id = "slow"
type = "command"
run = "echo slow"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&testExec{outcomes: map[string]testOutcome{
		"slow": {delay: time.Hour},
	}}, "")
	live, ctrl := mgr.Subscribe()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-live:
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stop) })

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
waitRunning:
	for {
		select {
		case e := <-ctrl:
			if ss, ok := e.(StepStatus); ok && ss.StepID == "slow" && ss.To == step.StatusRunning {
				break waitRunning
			}
			if _, ok := e.(RunFinished); ok {
				t.Fatal("run finished before cancel")
			}
		case <-deadline:
			t.Fatal("timeout waiting for running")
		}
	}
	run.Cancel()
	snap := run.Wait()
	if snap.ID != run.ID {
		t.Fatalf("snap.ID=%q want %q", snap.ID, run.ID)
	}
	// Snapshot().Done reflects step terminal statuses, not scheduler exit;
	// Cancel can leave a still-"running" step in the final memento. Wait
	// returning is the settle guarantee under test.
}
