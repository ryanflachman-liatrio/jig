//go:build unix

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestScheduler_BackgroundValidationChildCleanedUp(t *testing.T) {
	fixture := newHeldValidation(t, func(wf *workflow.Workflow, _ *testExec) {
		// The shell exits without waiting, while the helper keeps its stdout
		// pipe and HTTP connection open. WaitDelay alone cannot kill it.
		wf.Steps[0].Validate.Command += " &"
	})
	waitRecoveryRequest(t, fixture.events, 5*time.Second)
	select {
	case <-fixture.disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("validation returned but its background child survived")
	}
}

func TestRunGateRejectsFIFOOutput(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "output.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		passed bool
		detail string
	}
	done := make(chan outcome, 1)
	finished := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		// Rescue a regressed implementation blocked on opening/reading the
		// FIFO so a failed assertion cannot leave a leaked test goroutine.
		select {
		case <-finished:
			return
		default:
		}
		file, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			_, _ = file.Write([]byte("expected"))
			_ = file.Close()
		}
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("FIFO validation goroutine did not exit after cleanup")
		}
	})
	go func() {
		defer close(finished)
		passed, detail := runGate(ctx, &workflow.Step{Output: fifo, Validate: &workflow.Validate{OutputContains: "expected"}}, "")
		done <- outcome{passed: passed, detail: detail}
	}()
	select {
	case result := <-done:
		if result.passed || !strings.Contains(result.detail, "regular file") {
			t.Fatalf("FIFO validation = %+v, want nonregular-file rejection", result)
		}
	case <-time.After(300 * time.Millisecond):
		cancel()
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
			t.Fatal("file validation blocked on a FIFO and ignored cancellation")
		}
		t.Fatal("file validation opened a FIFO instead of rejecting it")
	}
}
