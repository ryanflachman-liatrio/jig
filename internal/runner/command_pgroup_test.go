//go:build unix

package runner

import (
	"context"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// pidReporter captures streamed output under a mutex so the test goroutine can
// scan it for the child PID while the drain goroutine writes concurrently.
type pidReporter struct {
	mu  sync.Mutex
	buf []byte
}

func (r *pidReporter) Output(delta string) {
	r.mu.Lock()
	r.buf = append(r.buf, delta...)
	r.mu.Unlock()
}
func (r *pidReporter) ToolCall(string, string)        {}
func (r *pidReporter) Message(int, int)               {}
func (r *pidReporter) Finding(engine.SecurityFinding) {}
func (r *pidReporter) Question(context.Context, string, []engine.AgentQuestionItem) string {
	return ""
}

func (r *pidReporter) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// alive reports whether pid names a live process. Signal 0 performs error
// checking without delivering a signal: nil → alive, ESRCH → gone.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// TestCommandExecutor_KillsProcessGroupOnCancel proves that cancelling the step
// context kills the shell's whole process group, not just sh — so a backgrounded
// child does not outlive a Run.Stop. Without the process-group cancel the child
// is reparented to init and survives.
func TestCommandExecutor_KillsProcessGroupOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exec := NewCommandExecutor("")
	// Spawn a long-lived background child, print its pid, then block so the step
	// stays running until we cancel.
	req := engine.StepRequest{
		Step: &workflow.Step{ID: "bg", Type: "command", Run: "sleep 60 & echo PID:$!; wait"},
	}
	rep := &pidReporter{}

	done := make(chan *step.Result, 1)
	go func() {
		res, _ := exec.Execute(ctx, req, rep)
		done <- res
	}()

	// Read the child's pid from the streamed output.
	pidRe := regexp.MustCompile(`PID:(\d+)`)
	var childPID int
	deadline := time.After(5 * time.Second)
	for childPID == 0 {
		if m := pidRe.FindStringSubmatch(rep.text()); m != nil {
			childPID, _ = strconv.Atoi(m[1])
			break
		}
		select {
		case <-deadline:
			t.Fatalf("never saw child PID in output; got %q", rep.text())
		case <-time.After(5 * time.Millisecond):
		}
	}

	if !alive(childPID) {
		t.Fatalf("child %d should be alive before cancel", childPID)
	}

	cancel()

	// Execute should return promptly once the group is killed.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return after cancel — Wait wedged")
	}

	// The backgrounded child must be gone (allow a moment for reaping).
	killDeadline := time.After(3 * time.Second)
	for alive(childPID) {
		select {
		case <-killDeadline:
			t.Fatalf("child %d survived cancel — process group was not killed", childPID)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
