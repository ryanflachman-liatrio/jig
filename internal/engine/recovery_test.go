package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// recoveringExec fails its target step on the first failCalls dispatches (each
// failed result carries sessionID so the recovery gate can offer resume), then
// succeeds. It records every StepRequest for the target so tests can inspect the
// resume plumbing (ResumeSessionID / Message). Non-target steps succeed after
// otherDelay, letting a test keep a sibling in flight while the target fails.
type recoveringExec struct {
	stepID     string
	failCalls  int
	sessionID  string
	delay      time.Duration
	otherDelay time.Duration

	mu    sync.Mutex
	calls int
	reqs  []StepRequest
}

func (e *recoveringExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	if req.Step.ID != e.stepID {
		if err := sleepCtx(ctx, orDefault(e.otherDelay, time.Millisecond)); err != nil {
			return nil, err
		}
		return &step.Result{Status: step.StatusSucceeded}, nil
	}

	e.mu.Lock()
	n := e.calls
	e.calls++
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()

	if err := sleepCtx(ctx, orDefault(e.delay, time.Millisecond)); err != nil {
		return nil, err
	}
	if n < e.failCalls {
		return &step.Result{Status: step.StatusFailed, Err: "boom: branch exists", SessionID: e.sessionID}, nil
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

func (e *recoveringExec) request(i int) (StepRequest, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i < 0 || i >= len(e.reqs) {
		return StepRequest{}, false
	}
	return e.reqs[i], true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// TestScheduler_Recovery_ParksNotAborts verifies that an unrecoverable failure
// under the default (abort) policy parks the step in awaiting_recovery and emits
// a RecoveryRequest instead of tearing the run down — the run stays alive.
func TestScheduler_Recovery_ParksNotAborts(t *testing.T) {
	const toml = `
[workflow]
name = "recover-park"
version = "0.1"

[[step]]
id = "bad"
type = "command"
run = "false"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &recoveringExec{stepID: "bad", failCalls: 1000}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the RecoveryRequest; the run must NOT have finished.
	var rr *RecoveryRequest
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case e := <-ch:
			switch ev := e.(type) {
			case RecoveryRequest:
				got := ev
				rr = &got
				break loop
			case RunFinished:
				t.Fatal("run finished instead of parking for recovery")
			}
		case <-deadline:
			t.Fatal("timeout waiting for RecoveryRequest")
		}
	}

	if rr.StepID != "bad" {
		t.Errorf("RecoveryRequest.StepID = %q, want %q", rr.StepID, "bad")
	}
	if rr.Err == "" {
		t.Error("RecoveryRequest.Err should carry the failure reason")
	}
	if rr.CanResume {
		t.Error("command failure has no session; CanResume should be false")
	}

	// The step is parked, not failed; the run is still live.
	snap := run.Snapshot()
	if snap.Done {
		t.Error("run should still be live while parked for recovery")
	}
	var parked bool
	for _, s := range snap.Steps {
		if s.ID == "bad" && s.Status == step.StatusAwaitingRecovery {
			parked = true
		}
	}
	if !parked {
		t.Errorf("step bad should be awaiting_recovery; snapshot = %+v", snap.Steps)
	}

	run.Recover("bad", RecoverAbort, "")
	drainUntilFinished(t, ch, 3*time.Second)
}

// TestScheduler_Recovery_RetrySucceeds verifies that RecoverRetry re-dispatches a
// parked step fresh and the run completes successfully when the retry passes.
func TestScheduler_Recovery_RetrySucceeds(t *testing.T) {
	const toml = `
[workflow]
name = "recover-retry"
version = "0.1"

[[step]]
id = "bad"
type = "command"
run = "false"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	// Fails once, then succeeds.
	exec := &recoveringExec{stepID: "bad", failCalls: 1}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := driveRecovery(t, ch, run, RecoverRetry, "", 5*time.Second)

	got := findStatus(events, "bad")
	if len(got) == 0 || got[len(got)-1] != step.StatusSucceeded {
		t.Errorf("step bad should end succeeded after retry; got %v", got)
	}
	assertRunFinished(t, events, false)
	if exec.calls != 2 {
		t.Errorf("expected 2 dispatches (1 fail + 1 retry), got %d", exec.calls)
	}
}

// TestScheduler_Recovery_ResumeUsesFailedSession verifies that RecoverResume
// re-dispatches with the failed step's SessionID and a Message that folds in the
// captured error plus operator guidance — the "don't repeat the mistake" path.
func TestScheduler_Recovery_ResumeUsesFailedSession(t *testing.T) {
	const toml = `
[workflow]
name = "recover-resume"
version = "0.1"

[[step]]
id = "bad"
type = "agent"
skill = "chat"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &recoveringExec{stepID: "bad", failCalls: 1, sessionID: "sess-42"}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Assert CanResume on the first RecoveryRequest before answering.
	events := driveRecoveryChecked(t, ch, run, 5*time.Second, func(rr RecoveryRequest) (string, string) {
		if !rr.CanResume {
			t.Error("agent failure with a session id should set CanResume=true")
		}
		return RecoverResume, "use git switch -c instead of git branch"
	})

	assertRunFinished(t, events, false)

	// The second dispatch must carry the resume session and a message with both
	// the captured error and the operator guidance.
	req, ok := exec.request(1)
	if !ok {
		t.Fatal("expected a second (resume) dispatch")
	}
	if req.ResumeSessionID != "sess-42" {
		t.Errorf("resume dispatch ResumeSessionID = %q, want %q", req.ResumeSessionID, "sess-42")
	}
	if !strings.Contains(req.Message, "boom: branch exists") {
		t.Errorf("resume Message should include the captured error; got %q", req.Message)
	}
	if !strings.Contains(req.Message, "git switch -c") {
		t.Errorf("resume Message should include operator guidance; got %q", req.Message)
	}
}

// TestScheduler_Recovery_Abort verifies that RecoverAbort fails the step and
// tears the run down — the explicit teardown that used to be the default.
func TestScheduler_Recovery_Abort(t *testing.T) {
	const toml = `
[workflow]
name = "recover-abort"
version = "0.1"

[[step]]
id = "bad"
type = "command"
run = "false"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &recoveringExec{stepID: "bad", failCalls: 1000}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := driveRecovery(t, ch, run, RecoverAbort, "", 5*time.Second)

	got := findStatus(events, "bad")
	if len(got) == 0 || got[len(got)-1] != step.StatusFailed {
		t.Errorf("step bad should end failed after abort; got %v", got)
	}
	assertRunFinished(t, events, true)
}

// TestScheduler_Recovery_Skip verifies that RecoverSkip accepts a step's failure
// and lets the run continue past it: the skipped step ends failed, its dependent
// still runs, and the run finishes non-failed — the interactive equivalent of a
// static on_failure = "continue".
func TestScheduler_Recovery_Skip(t *testing.T) {
	const toml = `
[workflow]
name = "recover-skip"
version = "0.1"

[[step]]
id = "bad"
type = "command"
run = "false"

[[step]]
id = "after"
type = "command"
depends_on = ["bad"]
run = "echo after"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	// "bad" fails forever; the only way past it is a skip at the recovery gate.
	exec := &recoveringExec{stepID: "bad", failCalls: 1000}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	events := driveRecovery(t, ch, run, RecoverSkip, "", 5*time.Second)

	// The run completes without being torn down. Failed=true is honest — the
	// step did fail; skip only means "don't block dependents / don't abort",
	// matching on_failure="continue" semantics. The proof skip differs from
	// abort is that the dependent below actually ran.
	assertRunFinished(t, events, true)

	// "bad" stays failed (skip does not launder it into success).
	gotBad := findStatus(events, "bad")
	if len(gotBad) == 0 || gotBad[len(gotBad)-1] != step.StatusFailed {
		t.Errorf("skipped step bad should end failed; got %v", gotBad)
	}

	// The dependent ran despite bad's failure — the release valve worked.
	gotAfter := findStatus(events, "after")
	if len(gotAfter) == 0 || gotAfter[len(gotAfter)-1] != step.StatusSucceeded {
		t.Errorf("dependent after should run and succeed after skip; got %v", gotAfter)
	}
}

// TestScheduler_Recovery_SiblingsSurvive verifies that one step failing and
// parking for recovery does NOT cancel an unrelated in-flight sibling — the core
// regression: the old s.cancel() killed every worker on the first failure.
func TestScheduler_Recovery_SiblingsSurvive(t *testing.T) {
	const toml = `
[workflow]
name = "recover-siblings"
version = "0.1"

[defaults]
max_parallel = 2

[[step]]
id = "bad"
type = "command"
run = "false"

[[step]]
id = "ok"
type = "command"
run = "echo ok"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	// "bad" fails fast once then recovers; "ok" stays in flight for 80ms.
	exec := &recoveringExec{stepID: "bad", failCalls: 1, delay: time.Millisecond, otherDelay: 80 * time.Millisecond}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Retry "bad" at the gate; the run should then complete with both succeeded.
	events := driveRecovery(t, ch, run, RecoverRetry, "", 5*time.Second)

	assertRunFinished(t, events, false)

	// "ok" ran to completion — proof it was not cancelled when "bad" failed.
	gotOK := findStatus(events, "ok")
	if len(gotOK) == 0 || gotOK[len(gotOK)-1] != step.StatusSucceeded {
		t.Errorf("sibling ok should end succeeded (not cancelled); got %v", gotOK)
	}
}

// TestScheduler_Recovery_Cap verifies the recovery round-trip is bounded: after
// maxRecoverRounds retries the gate refuses further retries with a RunError,
// preserving the static termination guarantee. The human can still abort.
func TestScheduler_Recovery_Cap(t *testing.T) {
	const toml = `
[workflow]
name = "recover-cap"
version = "0.1"

[[step]]
id = "bad"
type = "command"
run = "false"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &recoveringExec{stepID: "bad", failCalls: 1 << 30} // never succeeds
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()

	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	sawCap := false
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			switch ev := e.(type) {
			case RecoveryRequest:
				run.Recover(ev.StepID, RecoverRetry, "")
			case RunError:
				if strings.Contains(ev.Err, "maximum recovery rounds") {
					sawCap = true
					run.Recover("bad", RecoverAbort, "")
				}
			case RunFinished:
				if !sawCap {
					t.Fatal("run finished without hitting the recovery cap")
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for recovery cap")
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// driveRecovery drains ch until RunFinished, answering the first RecoveryRequest
// with (action, text) and collecting all events.
func driveRecovery(t *testing.T, ch <-chan Event, run *Run, action, text string, timeout time.Duration) []Event {
	t.Helper()
	return driveRecoveryChecked(t, ch, run, timeout, func(RecoveryRequest) (string, string) {
		return action, text
	})
}

// driveRecoveryChecked drains ch until RunFinished, calling answer() on the first
// RecoveryRequest to decide the (action, text) delivered via Run.Recover.
func driveRecoveryChecked(t *testing.T, ch <-chan Event, run *Run, timeout time.Duration, answer func(RecoveryRequest) (string, string)) []Event {
	t.Helper()
	var events []Event
	answered := false
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			events = append(events, e)
			if rr, ok := e.(RecoveryRequest); ok && !answered {
				answered = true
				action, text := answer(rr)
				run.Recover(rr.StepID, action, text)
			}
			if _, ok := e.(RunFinished); ok {
				if !answered {
					t.Fatal("run finished without a RecoveryRequest")
				}
				return events
			}
		case <-deadline:
			t.Fatal("timeout waiting for RunFinished")
			return nil
		}
	}
}

func drainUntilFinished(t *testing.T, ch <-chan Event, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if _, ok := e.(RunFinished); ok {
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for RunFinished")
		}
	}
}

func assertRunFinished(t *testing.T, events []Event, wantFailed bool) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last := events[len(events)-1]
	rf, ok := last.(RunFinished)
	if !ok {
		t.Fatalf("last event is not RunFinished: %T", last)
	}
	if rf.Failed != wantFailed {
		t.Errorf("RunFinished.Failed = %v, want %v", rf.Failed, wantFailed)
	}
}
