package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// stopTestExec is a controllable executor for stop/resume tests. A "blocker"
// step blocks until its context is cancelled (mimicking a worker stopped mid-run
// by Run.Stop) and, on that cancel, returns a failed Result carrying
// sessionOnCancel — exactly as the real runner attaches the early-captured SDK
// session id on the connection-closed path. A step's first dispatch (Attempt==0)
// blocks; a resumed/restarted dispatch (Attempt>0) succeeds so a resume is
// observable — unless resumeBlocks is set, in which case the resumed dispatch
// blocks again (used to prove a run re-enters quiescence after stop→resume→stop).
// Every dispatch's StepRequest is recorded so tests can inspect ResumeSessionID.
type stopTestExec struct {
	mu              sync.Mutex
	reqs            map[string][]StepRequest
	blockers        map[string]bool
	sessionOnCancel string
	resumeBlocks    bool
}

func newStopTestExec() *stopTestExec {
	return &stopTestExec{
		reqs:     make(map[string][]StepRequest),
		blockers: make(map[string]bool),
	}
}

func (e *stopTestExec) requests(stepID string) []StepRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]StepRequest, len(e.reqs[stepID]))
	copy(out, e.reqs[stepID])
	return out
}

func (e *stopTestExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	e.reqs[req.Step.ID] = append(e.reqs[req.Step.ID], req)
	e.mu.Unlock()

	if e.blockers[req.Step.ID] && (req.Attempt == 0 || e.resumeBlocks) {
		<-ctx.Done()
		// The runner returns the early-captured session id even when the stream is
		// cut short by a cancel; mirror that so resume can continue the session.
		return &step.Result{Status: step.StatusFailed, Err: "stopped", SessionID: e.sessionOnCancel}, nil
	}
	time.Sleep(time.Millisecond)
	return &step.Result{Status: step.StatusSucceeded, SessionID: e.sessionOnCancel}, nil
}

// waitForStepStatus polls the run snapshot until stepID reaches want or timeout.
func waitForStepStatus(t *testing.T, run *Run, stepID string, want step.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		for _, st := range run.Snapshot().Steps {
			if st.ID == stepID && st.Status == want {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for step %q to reach %q; snapshot=%v", stepID, want, run.Snapshot().Steps)
		case <-time.After(time.Millisecond):
		}
	}
}

// assertNoRunFinished drains ch for dur, failing if a RunFinished arrives.
func assertNoRunFinished(t *testing.T, ch <-chan Event, dur time.Duration) {
	t.Helper()
	deadline := time.After(dur)
	for {
		select {
		case e := <-ch:
			if _, ok := e.(RunFinished); ok {
				t.Fatal("run finished after a stop; it should stay alive and quiescent")
			}
		case <-deadline:
			return
		}
	}
}

func stepStatus(run *Run, stepID string) step.Status {
	for _, st := range run.Snapshot().Steps {
		if st.ID == stepID {
			return st.Status
		}
	}
	return ""
}

// TestStopOneStep proves a stop is surgical: stopping one running step parks it
// at StatusStopped, leaves a parallel sibling running to completion, and leaves
// the run alive and quiescent (no RunFinished from the stop).
func TestStopOneStep(t *testing.T) {
	const toml = `
[workflow]
name = "stop-one"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "sleep 10"

[[step]]
id = "b"
type = "command"
run = "echo b"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := newStopTestExec()
	exec.blockers["a"] = true
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Both dispatch in parallel; wait for A to be running, then stop only A.
	waitForStepStatus(t, run, "a", step.StatusRunning, 2*time.Second)
	run.Stop("a")

	// A parks stopped; B runs to completion; the run stays alive.
	waitForStepStatus(t, run, "a", step.StatusStopped, 2*time.Second)
	waitForStepStatus(t, run, "b", step.StatusSucceeded, 2*time.Second)
	if snap := run.Snapshot(); snap.Done {
		t.Fatalf("run reported Done after a stop; want alive/quiescent: %+v", snap)
	}
	assertNoRunFinished(t, ch, 50*time.Millisecond)

	run.Cancel() // clean up the parked run
}

// TestStopNonRunningStep proves the guard path: stopping a step with no live
// worker (here an unknown id) is a no-op that neither panics nor disturbs the
// running step.
func TestStopNonRunningStep(t *testing.T) {
	const toml = `
[workflow]
name = "stop-guard"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "sleep 10"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := newStopTestExec()
	exec.blockers["a"] = true
	mgr := NewManager(exec, "")
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	waitForStepStatus(t, run, "a", step.StatusRunning, 2*time.Second)
	run.Stop("ghost") // no such step: must be a no-op

	// Give the no-op a moment; A must still be running (not stopped).
	time.Sleep(20 * time.Millisecond)
	if got := stepStatus(run, "a"); got != step.StatusRunning {
		t.Fatalf("step a status = %q after stopping an unknown id, want running", got)
	}
	run.Cancel()
}

// TestStopPersistenceOff proves the cancel-capture path no-ops cleanly when
// there is no run dir / worktree: a stop still parks the step and keeps the run
// alive without error.
func TestStopPersistenceOff(t *testing.T) {
	const toml = `
[workflow]
name = "stop-persist-off"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "sleep 10"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := newStopTestExec()
	exec.blockers["a"] = true
	mgr := NewManager(exec, "") // persistence off: root == ""
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	waitForStepStatus(t, run, "a", step.StatusRunning, 2*time.Second)
	run.Stop("a")
	waitForStepStatus(t, run, "a", step.StatusStopped, 2*time.Second)
	if snap := run.Snapshot(); snap.Done {
		t.Fatalf("run Done after stop with persistence off; want alive: %+v", snap)
	}
	run.Cancel()
}

// TestStoppedStepCapturesDiff proves diff capture runs on cancel (white-box):
// when a stopped step's worktree holds a modification, the scheduler records a
// non-empty diff — the partial work is preserved, not discarded. It drives
// handle(stepDoneMsg) directly with s.stopping set, since the captured diff is
// internal scheduler state.
func TestStoppedStepCapturesDiff(t *testing.T) {
	repo := t.TempDir()
	if out, err := gitCmd(repo, "init"); err != nil {
		t.Fatalf("git init: %v — %s", err, out)
	}
	// A committed identity keeps `git commit` happy in CI-like environments.
	_, _ = gitCmd(repo, "config", "user.email", "test@example.com")
	_, _ = gitCmd(repo, "config", "user.name", "test")
	tracked := filepath.Join(repo, "file.txt")
	if err := os.WriteFile(tracked, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := gitCmd(repo, "add", "."); err != nil {
		t.Fatalf("git add: %v — %s", err, out)
	}
	if out, err := gitCmd(repo, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v — %s", err, out)
	}
	baseSHA, err := currentHEAD(repo)
	if err != nil {
		t.Fatal(err)
	}
	// The partial work a stopped agent would have left behind: a modified file.
	if err := os.WriteFile(tracked, []byte("modified by the stopped step\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const toml = `
[workflow]
name = "diff-capture"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "true"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	s := newScheduler(wf, "run-diff", make(chan schedMsg, 8), nil, newStopTestExec(),
		func() {}, nil, "", "", repo, func(RunSnapshot) {})

	// Simulate a step that was dispatched into the worktree and then stopped.
	s.states["a"].Status = step.StatusRunning
	s.inFlight = 1
	s.worktrees["a"] = repo
	s.wtBaseSHAs["a"] = baseSHA
	s.stepCancels["a"] = func() {}
	s.stopping["a"] = true

	s.handle(stepDoneMsg{stepID: "a", result: &step.Result{Status: step.StatusFailed, Err: "stopped"}})

	if got := s.states["a"].Status; got != step.StatusStopped {
		t.Fatalf("step status = %q after stop, want stopped", got)
	}
	if s.diffs["a"] == "" {
		t.Fatal("captured diff is empty; capture did not run on cancel")
	}
	if s.inFlight != 0 {
		t.Fatalf("inFlight = %d after stop, want 0", s.inFlight)
	}
	if _, ok := s.stepCancels["a"]; ok {
		t.Fatal("stepCancels entry not released after the worker exited")
	}
}

// TestResumeContinuesSession proves resume-as-continue: a stopped step that
// captured a session id is re-dispatched with ResumeSessionID and the operator
// message set, and runs to completion.
func TestResumeContinuesSession(t *testing.T) {
	const toml = `
[workflow]
name = "resume-continue"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "sleep 10"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := newStopTestExec()
	exec.blockers["a"] = true
	exec.sessionOnCancel = "sess-A"
	mgr := NewManager(exec, "")
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	waitForStepStatus(t, run, "a", step.StatusRunning, 2*time.Second)
	run.Stop("a")
	waitForStepStatus(t, run, "a", step.StatusStopped, 2*time.Second)

	run.Resume("a", "keep going")
	waitForStepStatus(t, run, "a", step.StatusSucceeded, 2*time.Second)

	reqs := exec.requests("a")
	if len(reqs) != 2 {
		t.Fatalf("want 2 dispatches (initial + resume), got %d", len(reqs))
	}
	if reqs[1].ResumeSessionID != "sess-A" {
		t.Errorf("resume dispatch ResumeSessionID = %q, want sess-A", reqs[1].ResumeSessionID)
	}
	if reqs[1].Message != "keep going" {
		t.Errorf("resume dispatch Message = %q, want %q", reqs[1].Message, "keep going")
	}
}

// TestResumeWithoutSessionRestarts proves the documented degrade: a stopped step
// with no captured session id is re-dispatched fresh (no ResumeSessionID) and
// still completes — a restart, not an error.
func TestResumeWithoutSessionRestarts(t *testing.T) {
	const toml = `
[workflow]
name = "resume-restart"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "sleep 10"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := newStopTestExec()
	exec.blockers["a"] = true
	exec.sessionOnCancel = "" // SDK surfaced no session id at cancel time
	mgr := NewManager(exec, "")
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	waitForStepStatus(t, run, "a", step.StatusRunning, 2*time.Second)
	run.Stop("a")
	waitForStepStatus(t, run, "a", step.StatusStopped, 2*time.Second)

	run.Resume("a", "retry please")
	waitForStepStatus(t, run, "a", step.StatusSucceeded, 2*time.Second)

	reqs := exec.requests("a")
	if len(reqs) != 2 {
		t.Fatalf("want 2 dispatches (initial + restart), got %d", len(reqs))
	}
	if reqs[1].ResumeSessionID != "" {
		t.Errorf("fresh restart dispatch ResumeSessionID = %q, want empty", reqs[1].ResumeSessionID)
	}
}

// TestStopResumeStopReentersQuiescence proves resume is not a one-shot terminal
// transition: a step can be stopped, resumed, and stopped again, and the run
// stays alive and re-enters quiescence each time (no RunFinished from a stop).
func TestStopResumeStopReentersQuiescence(t *testing.T) {
	const toml = `
[workflow]
name = "stop-resume-stop"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "sleep 10"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := newStopTestExec()
	exec.blockers["a"] = true
	exec.resumeBlocks = true // the resumed worker blocks again so we can re-stop it
	exec.sessionOnCancel = "sess-A"
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// First stop.
	waitForStepStatus(t, run, "a", step.StatusRunning, 2*time.Second)
	run.Stop("a")
	waitForStepStatus(t, run, "a", step.StatusStopped, 2*time.Second)

	// Resume → runs again → stop again.
	run.Resume("a", "keep going")
	waitForStepStatus(t, run, "a", step.StatusRunning, 2*time.Second)
	run.Stop("a")
	waitForStepStatus(t, run, "a", step.StatusStopped, 2*time.Second)

	if snap := run.Snapshot(); snap.Done {
		t.Fatalf("run Done after stop→resume→stop; want alive: %+v", snap)
	}
	assertNoRunFinished(t, ch, 50*time.Millisecond)
	run.Cancel()
}
