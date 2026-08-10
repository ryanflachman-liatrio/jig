package engine

import (
	"context"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// questionExec drives a single step through reporter.Question. It closes
// aboutToAsk immediately before the (lazy) answerCh assignment inside Question,
// so a test can deliver an answer concurrently with that write — reproducing the
// unsynchronized cross-goroutine access to reporter.answerCh (write on the
// executor goroutine at engine.go Question, read on the scheduler goroutine in
// handle(agentQuestionAnswerMsg)).
type questionExec struct {
	stepID     string
	preAskWait time.Duration     // sleep after aboutToAsk, before Question (widens the drop window)
	emit       []SecurityFinding // findings emitted from a side goroutine while Question blocks
	aboutToAsk chan struct{}
	gotAnswer  chan string
}

func (e *questionExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	if req.Step.ID != e.stepID {
		return &step.Result{Status: step.StatusSucceeded}, nil
	}
	// Signal *before* calling Question. The only happens-before edge this creates
	// to the test goroutine precedes the answerCh write inside Question, so the
	// subsequent answer delivery is genuinely unordered against that write.
	close(e.aboutToAsk)
	if e.preAskWait > 0 {
		time.Sleep(e.preAskWait)
	}
	// A Tier-2 monitor reports findings from its own goroutine, concurrent with the
	// blocked Question — mirror that so escalation races the parked worker.
	if len(e.emit) > 0 {
		go func() {
			time.Sleep(10 * time.Millisecond)
			for _, sf := range e.emit {
				rep.Finding(sf)
			}
		}()
	}
	ans := rep.Question(ctx, "tool-1", []AgentQuestionItem{{Question: "proceed?"}})
	e.gotAnswer <- ans
	return &step.Result{Status: step.StatusSucceeded, Err: "unwound"}, nil
}

// TestAgentQuestionAnswerRace reproduces the data race on reporter.answerCh and
// the resulting dropped-answer hang. Run with `go test -race`.
//
// The executor lazily creates answerCh on its own goroutine while the scheduler
// reads that same field to deliver the human's answer. With no synchronization
// ordering the write against the read, -race flags the field access; and when
// the read observes a nil answerCh the answer is dropped, so Question never
// returns and the agent step hangs forever.
func TestAgentQuestionAnswerRace(t *testing.T) {
	const toml = `
[workflow]
name = "agent-question"
version = "1.0"
[[step]]
id = "ask"
type = "command"
run = "true"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}

	exec := &questionExec{
		stepID:     "ask",
		preAskWait: 50 * time.Millisecond,
		aboutToAsk: make(chan struct{}),
		gotAnswer:  make(chan string, 1),
	}
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	// Answer as soon as the executor is about to ask — concurrently with the
	// answerCh write, without first observing the AgentQuestion ctrl event (which
	// would otherwise establish an accidental happens-before edge and mask the bug).
	<-exec.aboutToAsk
	run.AnswerQuestion("ask", "tool-1", "the answer")

	select {
	case got := <-exec.gotAnswer:
		if got != "the answer" {
			t.Errorf("Question returned %q, want %q", got, "the answer")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Question never returned — answer was dropped (answerCh read as nil)")
	}

	drainUntilFinished(t, ctrl, 5*time.Second)
}

// startQuestionRun starts a single-step run whose worker blocks in Question, and
// returns once the step has reached StatusNeedsInput.
func startQuestionRun(t *testing.T, exec *questionExec) (*Run, <-chan Event) {
	t.Helper()
	const toml = `
[workflow]
name = "agent-question"
version = "1.0"
[[step]]
id = "ask"
type = "command"
run = "true"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	waitForStepStatus(t, run, "ask", step.StatusNeedsInput, 5*time.Second)
	return run, ctrl
}

// TestQuestionUnblockedByStop proves that stopping a step whose worker is parked
// in Question unblocks the worker (Question returns "" via ctx.Done) rather than
// leaking the goroutine forever, and parks the step at StatusStopped.
func TestQuestionUnblockedByStop(t *testing.T) {
	exec := &questionExec{
		stepID:     "ask",
		aboutToAsk: make(chan struct{}),
		gotAnswer:  make(chan string, 1),
	}
	run, ctrl := startQuestionRun(t, exec)

	run.Stop("ask")

	select {
	case got := <-exec.gotAnswer:
		if got != "" {
			t.Errorf("Question returned %q, want \"\" on stop", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Question never returned after Stop — worker leaked")
	}

	waitForStepStatus(t, run, "ask", step.StatusStopped, 5*time.Second)
	// A stop does not end the run; it stays alive and quiescent.
	assertNoRunFinished(t, ctrl, 200*time.Millisecond)
}

// TestQuestionUnblockedByEscalation proves that a critical security finding
// against a step parked in Question unblocks the worker, parks the step at
// StatusAwaitingRecovery, and does not leak the goroutine or double-count
// inFlight — a later RecoverAbort then finishes the run cleanly.
func TestQuestionUnblockedByEscalation(t *testing.T) {
	exec := &questionExec{
		stepID:     "ask",
		aboutToAsk: make(chan struct{}),
		gotAnswer:  make(chan string, 1),
		emit: []SecurityFinding{{
			Tier: "monitor", Monitor: "secret-in-write",
			Severity: "critical", Action: "escalated", Fingerprint: "fp-q-1",
		}},
	}
	run, ctrl := startQuestionRun(t, exec)

	// The side goroutine emits the critical finding; the worker's Question must
	// unwind via ctx.Done.
	select {
	case got := <-exec.gotAnswer:
		if got != "" {
			t.Errorf("Question returned %q, want \"\" on escalation", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Question never returned after escalation — worker leaked")
	}

	waitForStepStatus(t, run, "ask", step.StatusAwaitingRecovery, 5*time.Second)
	run.Recover("ask", RecoverAbort, "")
	drainUntilFinished(t, ctrl, 5*time.Second)
}
