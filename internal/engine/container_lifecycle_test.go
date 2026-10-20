package engine

import (
	"context"
	"os"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

func d05Ownership(s *scheduler) step.ContainerPhase {
	return step.ContainerPhase{RunID: s.runID, StepID: "worker", Nonce: "0123456789abcdef0123456789abcdef", Phase: "main", Name: "synthetic-main", DaemonID: "synthetic-daemon", EndpointID: "synthetic-endpoint", ImageID: "synthetic-image", State: step.ContainerIntent}
}
func d05Report(s *scheduler, p step.ContainerPhase) error {
	reply := make(chan error, 1)
	containerLifecycleMsg{stepID: "worker", execution: 2, ownership: p, reply: reply}.execute(s)
	return <-reply
}
func TestD05ContainerAcknowledgements(t *testing.T) {
	s := validationSchedulerFixture(t)
	p := d05Ownership(s)
	s.executionNonces = map[string]string{"worker": p.Nonce}
	rd, err := datastore.RunDir(t.TempDir(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	s.writer, err = manifest.NewWriter(rd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.writer.Close() })
	for _, state := range []step.ContainerState{step.ContainerIntent, step.ContainerCreated, step.ContainerStopped, step.ContainerRemoved} {
		p.State = state
		if state == step.ContainerCreated {
			p.ContainerID = "synthetic-container"
		}
		if err := d05Report(s, p); err != nil {
			t.Fatal(err)
		}
		_, events, err := readJournal(rd)
		if err != nil {
			t.Fatal(err)
		}
		if events[len(events)-1].(ContainerLifecycle).Ownership != p {
			t.Fatal("ack preceded durable fact")
		}
		if err := d05Report(s, p); err == nil {
			t.Fatal("duplicate fact accepted")
		}
		if err := checkContainerReopen(events); (err == nil) != (state == step.ContainerRemoved) {
			t.Fatalf("reopen at %s: %v", state, err)
		}
	}
	if s.containerUncertain("worker") {
		t.Fatal("removed container fenced")
	}
	s.states["worker"].Status = step.StatusValidating
	p.Phase, p.Name, p.ContainerID, p.State = "validation", "synthetic-validator", "", step.ContainerIntent
	if err := d05Report(s, p); err != nil {
		t.Fatal(err)
	}
}
func TestD05ContainerRejectsUnsafeReports(t *testing.T) {
	for _, mode := range []string{"stale", "nonce", "coordinates", "journal", "validation before main", "conflicting ID", "uncertain is permanent"} {
		t.Run(mode, func(t *testing.T) {
			s := validationSchedulerFixture(t)
			p := d05Ownership(s)
			s.executionNonces = map[string]string{"worker": p.Nonce}
			switch mode {
			case "stale":
				s.activeExecutions["worker"] = 3
			case "nonce":
				p.Nonce = "abcdef0123456789abcdef0123456789"
			case "coordinates":
				p.Generation++
			case "journal":
				var err error
				s.writer, err = manifest.NewWriter(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				s.writer.Close()
			case "validation before main":
				p.Phase = "validation"
				s.states["worker"].Status = step.StatusValidating
			case "conflicting ID", "uncertain is permanent":
				if err := d05Report(s, p); err != nil {
					t.Fatal(err)
				}
				p.State, p.ContainerID = step.ContainerCreated, "exact-id"
				if err := d05Report(s, p); err != nil {
					t.Fatal(err)
				}
				if mode == "conflicting ID" {
					p.ContainerID = "replacement"
				} else {
					p.State = step.ContainerUncertain
					if err := d05Report(s, p); err != nil {
						t.Fatal(err)
					}
				}
				p.State = step.ContainerStopped
			}
			before := s.seq
			if err := d05Report(s, p); err == nil {
				t.Fatal("unsafe report accepted")
			}
			if s.seq != before {
				t.Fatal("rejected report published")
			}
		})
	}
}
func TestD05ContainerOwnershipFencesCompletionAndValidation(t *testing.T) {
	s := validationSchedulerFixture(t)
	p := d05Ownership(s)
	s.executionNonces = map[string]string{"worker": p.Nonce}
	if err := d05Report(s, p); err != nil {
		t.Fatal(err)
	}
	reply := make(chan error, 1)
	validationStartMsg{stepID: "worker", execution: 2, result: &step.Result{Status: step.StatusSucceeded}, reply: reply}.execute(s)
	if <-reply == nil {
		t.Fatal("validator permitted before stop")
	}
	stepDoneMsg{stepID: "worker", execution: 2, result: &step.Result{Status: step.StatusSucceeded, OutputPath: "unsafe"}}.execute(s)
	result := s.states["worker"].Result
	if !result.ExecutionUncertain || result.OutputPath != "" || s.inFlight != 1 {
		t.Fatal("ownership did not fence completion")
	}
}
func TestD05ContainerCallbackCoordinatesAndCancellation(t *testing.T) {
	inbox := make(chan schedMsg, 1)
	s := validationSchedulerFixture(t)
	req := StepRequest{RunID: s.runID, Step: s.stepByID("worker"), Generation: 4, Iteration: 3, Attempt: 2}
	if err := bindContainerLifecycle(&req, inbox, 9); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- req.ContainerLifecycle(context.Background(), step.ContainerPhase{RunID: "forged", Nonce: "forged"})
	}()
	msg := (<-inbox).(containerLifecycleMsg)
	p := msg.ownership
	if p.RunID != req.RunID || p.Nonce != req.ExecutionNonce || p.Generation != 4 || p.Iteration != 3 || p.Attempt != 2 || msg.execution != 9 {
		t.Fatal("unbound runtime coordinates")
	}
	msg.reply <- nil
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := req.ContainerLifecycle(ctx, p); err == nil {
		t.Fatal("cancelled acknowledgement succeeded")
	}
}
func TestD05ContainerClosedJournalDoesNotGrantCreate(t *testing.T) {
	// A failed write must leave no intent in memory or journal.
	s := validationSchedulerFixture(t)
	p := d05Ownership(s)
	s.executionNonces = map[string]string{"worker": p.Nonce}
	root := t.TempDir()
	var err error
	s.writer, err = manifest.NewWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	s.writer.Close()
	if err := d05Report(s, p); err == nil {
		t.Fatal("create granted")
	}
	data, err := os.ReadFile(datastore.JournalPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 || len(s.containerPhases) != 0 {
		t.Fatal("failed intent became visible")
	}
}
