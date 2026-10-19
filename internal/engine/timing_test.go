package engine

import (
	"fmt"
	"os"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestTimingLiveSnapshotReplayAndRecovery(t *testing.T) {
	for _, persist := range []bool{false, true} {
		t.Run(fmt.Sprintf("persistence=%t", persist), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				wf, err := workflow.Decode(`[workflow]
name = "timing"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
[[step]]
id = "b"
type = "command"
run = "true"
`, "")
				if err != nil {
					t.Fatal(err)
				}
				runDir := ""
				var writer *manifest.Writer
				if persist {
					runDir = t.TempDir()
					writer, err = manifest.NewWriter(datastore.RunAt(runDir))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = writer.Close() })
				}
				cancel := func() {}
				ctrl := make(chan Event, 16)
				s := newScheduler(schedulerDeps{wf: wf, runID: "r", inbox: make(chan schedMsg, 1), subs: []sub{{ctrl: ctrl}}, exec: &testExec{}, cancel: cancel, writer: writer, runDir: datastore.RunAt(runDir), onDone: func(RunSnapshot) {}})
				if err := s.emit(RunStarted{RunID: "r", Workflow: "timing", Steps: []string{"a", "b"}}); err != nil {
					t.Fatal(err)
				}
				if !s.transition("b", step.StatusPending, step.StatusRunning) {
					t.Fatal("start b")
				}
				for _, status := range []step.Status{step.StatusRunning, step.StatusValidating, step.StatusSucceeded} {
					if !s.transition("a", s.states["a"].Status, status) {
						t.Fatalf("transition to %s failed", status)
					}
					time.Sleep(125 * time.Millisecond) // advances the fake clock, not wall time
				}
				var events []Event
				var live step.Timing
				for len(ctrl) > 0 {
					e := <-ctrl
					events = append(events, e)
					if status, ok := e.(StepStatus); ok && status.StepID == "a" {
						if status.At.IsZero() || status.Timing == nil {
							t.Fatalf("unstamped event: %+v", status)
						}
						live = status.ProjectTiming(live)
					}
				}
				if live.StartedAt.IsZero() || live.EndedAt.IsZero() || live.Elapsed != 250*time.Millisecond {
					t.Fatalf("no execution interval: %+v", live)
				}
				if got := s.snapshot().Steps[0].Timing; got != live {
					t.Fatalf("snapshot=%+v live=%+v", got, live)
				}
				if persist {
					events, err = ReplayJournalRaw(datastore.RunAt(runDir))
					if err != nil {
						t.Fatal(err)
					}
					var replay step.Timing
					for _, e := range events {
						if status, ok := e.(StepStatus); ok && status.StepID == "a" {
							replay = status.ProjectTiming(replay)
						}
					}
					if !reflect.DeepEqual(replay, live) {
						t.Fatalf("replay=%+v live=%+v", replay, live)
					}
				}
				checkpoint, err := restoreUnfinishedCheckpoint(datastore.RunAt(runDir), wf, events)
				if err != nil {
					t.Fatal(err)
				}
				if got := checkpoint.states["a"].Timing; got != live {
					t.Fatalf("restored=%+v live=%+v", got, live)
				}
				interrupted := checkpoint.states["b"].Timing
				if !interrupted.ActiveSince.IsZero() || interrupted.Duration(time.Now().Add(time.Hour)) != interrupted.Elapsed {
					t.Fatal("restored worker counts process-absence time")
				}
				// Recovery publishes its frozen projection. Folding the recovery event
				// must not reintroduce downtime from the old running event.
				s.states = checkpoint.states
				recovery := s.parkInterruptedWorker("b")[0].(StepStatus)
				var recovered step.Timing
				for _, e := range append(events, recovery) {
					if status, ok := e.(StepStatus); ok && status.StepID == "b" {
						recovered = status.ProjectTiming(recovered)
					}
				}
				if recovered != interrupted || s.snapshot().Steps[1].Timing != recovered {
					t.Fatalf("recovery timing changed: before=%+v after=%+v", interrupted, recovered)
				}
			})
		})
	}
}

func TestReplayTimingUsesOlderJournalTimestamps(t *testing.T) {
	runDir := t.TempDir()
	journal := `{"seq":1,"ts":"2026-10-01T10:00:00Z","kind":"step_status","data":{"RunID":"r","StepID":"a","To":"running"}}
{"seq":2,"ts":"2026-10-01T10:00:12Z","kind":"step_status","data":{"RunID":"r","StepID":"a","To":"succeeded"}}
`
	if err := os.WriteFile(datastore.RunAt(runDir).Journal(), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := ReplayJournalRaw(datastore.RunAt(runDir))
	if err != nil {
		t.Fatal(err)
	}
	var timing step.Timing
	for _, e := range events {
		timing = e.(StepStatus).ProjectTiming(timing)
	}
	if got := timing.Duration(time.Now()); got != 12*time.Second {
		t.Fatalf("duration=%v, want 12s", got)
	}
}
