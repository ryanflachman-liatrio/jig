package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"jig/internal/interaction"
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
	gotAnswer  chan interaction.QuestionResponse
}

type concurrentQuestionExec struct {
	seen chan interaction.QuestionResponse
}

func (e *concurrentQuestionExec) Execute(ctx context.Context, _ StepRequest, rep Reporter) (*step.Result, error) {
	var wg sync.WaitGroup
	for _, id := range []string{"q1", "q2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			resp := rep.Question(ctx, interaction.QuestionRequest{
				ID: id,
				Fields: []interaction.QuestionField{{
					ID: "answer", Prompt: id + "?", Kind: interaction.FieldText,
				}},
			})
			e.seen <- resp
		}(id)
	}
	wg.Wait()
	return &step.Result{Status: step.StatusSucceeded}, nil
}

func TestConcurrentQuestionsRemainCorrelated(t *testing.T) {
	const toml = `
[workflow]
name = "concurrent-questions"
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
	exec := &concurrentQuestionExec{seen: make(chan interaction.QuestionResponse, 2)}
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	requests := make(map[string]interaction.QuestionRequest)
	for len(requests) < 2 {
		select {
		case ev := <-ctrl:
			if q, ok := ev.(AgentQuestion); ok {
				requests[q.Request.ID] = q.Request
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent question events")
		}
	}
	run.AnswerQuestion("ask", interaction.QuestionResponse{
		RequestID: "stale", Action: interaction.ActionDecline,
	})
	run.AnswerQuestion("ask", interaction.QuestionResponse{
		RequestID: "q1",
		Action:    interaction.ActionAccept,
		Answers:   map[string]interaction.Answer{"answer": {Values: []string{"one"}}},
	})
	first := <-exec.seen
	if first.RequestID != "q1" || first.Answers["answer"].Values[0] != "one" {
		t.Fatalf("first response = %+v", first)
	}
	snap := run.Snapshot()
	if len(snap.Steps) != 1 || snap.Steps[0].Status != step.StatusNeedsInput {
		t.Fatalf("status after one response = %+v, want needs_input", snap.Steps)
	}

	run.AnswerQuestion("ask", interaction.QuestionResponse{
		RequestID: "q2",
		Action:    interaction.ActionAccept,
		Answers:   map[string]interaction.Answer{"answer": {Values: []string{"two"}}},
	})
	second := <-exec.seen
	if second.RequestID != "q2" || second.Answers["answer"].Values[0] != "two" {
		t.Fatalf("second response = %+v", second)
	}
	drainUntilFinished(t, ctrl, 5*time.Second)
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
	ans := rep.Question(ctx, interaction.QuestionRequest{
		ID: "question-1",
		Fields: []interaction.QuestionField{{
			ID: "proceed", Prompt: "proceed?", Kind: interaction.FieldText,
		}},
	})
	e.gotAnswer <- ans
	return &step.Result{Status: step.StatusSucceeded, Err: "unwound"}, nil
}

// TestAgentQuestionAnswerRace verifies the request is registered before its
// event is emitted, so an immediate UI response cannot race channel setup.
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
		gotAnswer:  make(chan interaction.QuestionResponse, 1),
	}
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	<-exec.aboutToAsk
	for {
		ev := <-ctrl
		if q, ok := ev.(AgentQuestion); ok {
			run.AnswerQuestion("ask", interaction.QuestionResponse{
				RequestID: q.Request.ID,
				Action:    interaction.ActionAccept,
				Answers: map[string]interaction.Answer{
					"proceed": {Values: []string{"the answer"}},
				},
			})
			break
		}
	}

	select {
	case got := <-exec.gotAnswer:
		if got.Answers["proceed"].Values[0] != "the answer" {
			t.Errorf("Question returned %+v, want the answer", got)
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
		gotAnswer:  make(chan interaction.QuestionResponse, 1),
	}
	run, ctrl := startQuestionRun(t, exec)

	run.Stop("ask")

	select {
	case got := <-exec.gotAnswer:
		if got.Action != interaction.ActionCancel {
			t.Errorf("Question returned %+v, want cancel on stop", got)
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
		gotAnswer:  make(chan interaction.QuestionResponse, 1),
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
		if got.Action != interaction.ActionCancel {
			t.Errorf("Question returned %+v, want cancel on escalation", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Question never returned after escalation — worker leaked")
	}

	waitForStepStatus(t, run, "ask", step.StatusAwaitingRecovery, 5*time.Second)
	run.Recover("ask", RecoverAbort, "")
	drainUntilFinished(t, ctrl, 5*time.Second)
}
