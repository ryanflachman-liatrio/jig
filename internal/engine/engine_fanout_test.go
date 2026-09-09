package engine

// Scheduler-level integration tests for [step.foreach] runtime children
// (task 15 of docs/plans/a8-dynamic-foreach-fan-out.md, Phase 2): proving
// that ordinary per-step lifecycle machinery — retry, timeout, security
// escalation, the agent question gate, and human recovery — works unmodified
// when the step in question is a runtime fan-out child rather than a
// statically declared step, and that static dependents of a family never
// dispatch before every child has settled. This file is separate from the
// already-large engine_test.go per the task's own guidance.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"jig/internal/interaction"
	"jig/internal/step"
	"jig/internal/workflow"
)

// singleChildTOML declares a discover -> analyze family whose producer emits
// exactly one item, with extra appended verbatim after "skill =
// \"skills/analyze\"" (e.g. on_failure/timeout declarations) and before
// [step.foreach].
func singleChildTOML(extra string) string {
	return `
[workflow]
name = "foreach-lifecycle"
version = "1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
` + extra + `
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"
`
}

// oneTargetProducer is the discover step's scripted structured output for
// every test in this file: a single item, so exactly one runtime child is
// created and its instance id is deterministic.
func oneTargetProducer() map[string]json.RawMessage {
	return map[string]json.RawMessage{"discover": discoverStructured(target("api", "services/api"))}
}

// lifecycleExec drives a single fan-out child through one of several scripted
// per-child behaviors, selected per test. The producer step always succeeds
// with its scripted structured output.
type lifecycleExec struct {
	fanOutExec
	failFirstN  int  // fail the child's first N attempts, then succeed
	hangAll     bool // block on ctx.Done() forever (timeout test)
	askQuestion bool // call rep.Question and act on the response
	finding     *SecurityFinding
}

func (e *lifecycleExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	if raw, ok := e.producer[req.Step.ID]; ok {
		return &step.Result{Status: step.StatusSucceeded, Structured: raw}, nil
	}
	if req.FanOutItem == nil {
		return &step.Result{Status: step.StatusSucceeded}, nil
	}

	switch {
	case e.hangAll:
		<-ctx.Done()
		return nil, ctx.Err()

	case e.finding != nil:
		sf := *e.finding
		rep.Finding(sf)
		<-ctx.Done()
		return nil, ctx.Err()

	case e.askQuestion:
		resp := rep.Question(ctx, interaction.QuestionRequest{
			ID: "q1",
			Fields: []interaction.QuestionField{{
				ID: "proceed", Prompt: "proceed?", Kind: interaction.FieldText,
			}},
		})
		if resp.Action != interaction.ActionAccept {
			return &step.Result{Status: step.StatusFailed, Err: "question declined"}, nil
		}
		return &step.Result{Status: step.StatusSucceeded}, nil

	default:
		e.mu.Lock()
		if e.calls == nil {
			e.calls = make(map[string]int)
		}
		e.calls[req.Step.ID]++
		n := e.calls[req.Step.ID]
		e.mu.Unlock()
		if n <= e.failFirstN {
			return &step.Result{Status: step.StatusFailed, Err: "scripted transient failure"}, nil
		}
		return &step.Result{Status: step.StatusSucceeded}, nil
	}
}

// fanOutChildStatuses filters events down to StepStatus transitions for the
// (single, in these tests) fan-out child, returning its id and the ordered
// list of statuses it passed through.
func fanOutChildStatuses(events []Event) (childID string, statuses []step.Status) {
	for _, e := range events {
		ss, ok := e.(StepStatus)
		if !ok || !strings.Contains(ss.StepID, workflow.ForEachIDMarker) {
			continue
		}
		childID = ss.StepID
		statuses = append(statuses, ss.To)
	}
	return childID, statuses
}

// TestForEachChild_AutomaticRetryUnmodified proves a fan-out child retries
// exactly like an ordinary step under on_failure="retry": it fails once, is
// reset to pending, and succeeds on its second attempt — settling the family
// with the retried child counted as succeeded.
func TestForEachChild_AutomaticRetryUnmodified(t *testing.T) {
	wf, err := workflow.Decode(singleChildTOML("on_failure = \"retry\"\nmax_retries = 2\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &lifecycleExec{failFirstN: 1}
	exec.producer = oneTargetProducer()
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatalf("last event = %+v, want successful RunFinished (child retried to success)", events[len(events)-1])
	}
	childID, statuses := fanOutChildStatuses(events)
	if childID == "" {
		t.Fatal("no fan-out child observed")
	}
	for _, s := range statuses {
		if s == step.StatusFailed {
			t.Fatalf("automatic retry must never leave the child terminally failed; statuses = %v", statuses)
		}
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 1 || agg.Succeeded != 1 || !agg.AllSucceeded {
		t.Fatalf("aggregate = %+v, want the retried child counted as succeeded", agg)
	}
}

// TestForEachChild_TimeoutUnmodified proves a fan-out child's own [step]
// timeout fails just that child (as "step timeout exceeded") without
// affecting the family's ability to settle once it does.
func TestForEachChild_TimeoutUnmodified(t *testing.T) {
	wf, err := workflow.Decode(singleChildTOML("timeout = \"5ms\"\non_failure = \"continue\"\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &lifecycleExec{hangAll: true}
	exec.producer = oneTargetProducer()
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	rf, ok := events[len(events)-1].(RunFinished)
	if !ok || !rf.Failed {
		t.Fatalf("last event = %+v, want RunFinished{Failed:true} (the child timed out)", events[len(events)-1])
	}
	var timedOutErr string
	for _, e := range events {
		if ss, ok := e.(StepStatus); ok && strings.Contains(ss.StepID, workflow.ForEachIDMarker) && ss.To == step.StatusFailed {
			timedOutErr = ss.Err
		}
	}
	if !strings.Contains(timedOutErr, "timeout") {
		t.Errorf("child failure reason = %q, want it to name the timeout", timedOutErr)
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 1 || agg.Failed != 1 || agg.AllSucceeded {
		t.Fatalf("aggregate = %+v, want the timed-out child counted as failed", agg)
	}
}

// TestForEachChild_SecurityEscalationUnmodified proves a critical Tier-1/
// Tier-2 security finding on an in-flight fan-out child parks that exact
// child (by its full instance id) at StatusAwaitingRecovery, not the family —
// and that an operator skip decision lets the family settle with that child
// counted as an accepted failure.
func TestForEachChild_SecurityEscalationUnmodified(t *testing.T) {
	wf, err := workflow.Decode(singleChildTOML(""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &lifecycleExec{finding: &SecurityFinding{
		Tier: "guard", Monitor: "secret-in-write", Severity: "critical",
		Action: "escalated", Fingerprint: "fp-fanout-1",
	}}
	exec.producer = oneTargetProducer()
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	var rr RecoveryRequest
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case e := <-ctrl:
			if got, ok := e.(RecoveryRequest); ok {
				rr = got
				break loop
			}
			if _, ok := e.(RunFinished); ok {
				t.Fatal("run finished before RecoveryRequest from the child's critical finding")
			}
		case <-deadline:
			t.Fatal("timeout waiting for RecoveryRequest")
		}
	}
	if !strings.Contains(rr.StepID, workflow.ForEachIDMarker) {
		t.Fatalf("RecoveryRequest.StepID = %q, want the child's full instance id, not the family", rr.StepID)
	}
	snap := run.Snapshot()
	var parked bool
	for _, s := range snap.Steps {
		if s.ID == rr.StepID && s.Status == step.StatusAwaitingRecovery {
			parked = true
		}
		if s.ID == "analyze" && s.Status != step.StatusRunning {
			t.Fatalf("family status = %v, want it still running while its child is parked", s.Status)
		}
	}
	if !parked {
		t.Fatalf("child %q should be parked at awaiting_recovery", rr.StepID)
	}

	run.Recover(rr.StepID, RecoverSkip, "")
	events := collectEvents(t, ctrl, 5*time.Second)
	rf, ok := events[len(events)-1].(RunFinished)
	if !ok || !rf.Failed {
		t.Fatalf("last event = %+v, want RunFinished{Failed:true} (skipped child still failed)", events[len(events)-1])
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 1 || agg.Failed != 1 {
		t.Fatalf("aggregate = %+v, want the skipped child counted as an accepted failure", agg)
	}
}

// TestForEachChild_QuestionGateUnmodified proves AgentQuestion/AnswerQuestion
// address a fan-out child by its full instance id, exactly like an ordinary
// agent step, and that answering it lets the family settle successfully.
func TestForEachChild_QuestionGateUnmodified(t *testing.T) {
	wf, err := workflow.Decode(singleChildTOML(""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &lifecycleExec{askQuestion: true}
	exec.producer = oneTargetProducer()
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	var q AgentQuestion
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case e := <-ctrl:
			if got, ok := e.(AgentQuestion); ok {
				q = got
				break loop
			}
		case <-deadline:
			t.Fatal("timeout waiting for AgentQuestion")
		}
	}
	if !strings.Contains(q.StepID, workflow.ForEachIDMarker) {
		t.Fatalf("AgentQuestion.StepID = %q, want the child's full instance id", q.StepID)
	}

	run.AnswerQuestion(q.StepID, interaction.QuestionResponse{
		RequestID: q.Request.ID,
		Action:    interaction.ActionAccept,
		Answers:   map[string]interaction.Answer{"proceed": {Values: []string{"yes"}}},
	})
	events := collectEvents(t, ctrl, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatalf("last event = %+v, want successful RunFinished", events[len(events)-1])
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 1 || agg.Succeeded != 1 {
		t.Fatalf("aggregate = %+v, want the answered child counted as succeeded", agg)
	}
}

// TestForEachChild_HumanRecoveryRetryUnmodified proves the human recovery
// gate's retry decision re-dispatches a failed fan-out child by its instance
// id, exactly like an ordinary step, and lets the family settle once it
// succeeds.
func TestForEachChild_HumanRecoveryRetryUnmodified(t *testing.T) {
	wf, err := workflow.Decode(singleChildTOML(""), "") // default on_failure = abort → recovery gate
	if err != nil {
		t.Fatal(err)
	}
	exec := &lifecycleExec{failFirstN: 1}
	exec.producer = oneTargetProducer()
	mgr := NewManager(exec, "")
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}

	var rr RecoveryRequest
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case e := <-ctrl:
			if got, ok := e.(RecoveryRequest); ok {
				rr = got
				break loop
			}
		case <-deadline:
			t.Fatal("timeout waiting for RecoveryRequest")
		}
	}
	if !strings.Contains(rr.StepID, workflow.ForEachIDMarker) {
		t.Fatalf("RecoveryRequest.StepID = %q, want the child's full instance id", rr.StepID)
	}
	run.Recover(rr.StepID, RecoverRetry, "")
	events := collectEvents(t, ctrl, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatalf("last event = %+v, want successful RunFinished after recovery retry", events[len(events)-1])
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 1 || agg.Succeeded != 1 {
		t.Fatalf("aggregate = %+v, want the recovered child counted as succeeded", agg)
	}
}

// TestForEach_DependentsNeverStartBeforeFamilySettles proves a static
// dependent of a foreach family never dispatches before every child has
// reached a terminal status and the family itself has transitioned to
// succeeded — even when children finish at very different times.
func TestForEach_DependentsNeverStartBeforeFamilySettles(t *testing.T) {
	const toml = `
[workflow]
name = "foreach-dependents-wait"
version = "1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"

[[step]]
id = "synthesize"
type = "command"
depends_on = ["analyze"]
run = "echo done"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{
		producer: map[string]json.RawMessage{"discover": discoverStructured(
			target("a", "a"), target("b", "b"), target("c", "c"),
		)},
		delayFn: func(item *FanOutItem) time.Duration {
			// Staggered: last-declared item finishes soonest, first-declared
			// item finishes last, so completion order is the reverse of
			// source/dispatch order.
			return time.Duration(3-item.Index) * 15 * time.Millisecond
		},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatal("run should succeed")
	}

	familySucceededIdx, synthesizeRunningIdx := -1, -1
	lastChildTerminalIdx := -1
	for i, e := range events {
		ss, ok := e.(StepStatus)
		if !ok {
			continue
		}
		switch {
		case ss.StepID == "analyze" && ss.To == step.StatusSucceeded:
			familySucceededIdx = i
		case ss.StepID == "synthesize" && ss.To == step.StatusRunning:
			synthesizeRunningIdx = i
		case strings.Contains(ss.StepID, workflow.ForEachIDMarker) &&
			(ss.To == step.StatusSucceeded || ss.To == step.StatusFailed || ss.To == step.StatusSkipped):
			lastChildTerminalIdx = i
		}
	}
	if familySucceededIdx == -1 || synthesizeRunningIdx == -1 || lastChildTerminalIdx == -1 {
		t.Fatalf("missing expected events: family=%d synthesize=%d lastChild=%d", familySucceededIdx, synthesizeRunningIdx, lastChildTerminalIdx)
	}
	if lastChildTerminalIdx > familySucceededIdx {
		t.Fatalf("family succeeded (event %d) before its last child settled (event %d)", familySucceededIdx, lastChildTerminalIdx)
	}
	if synthesizeRunningIdx < familySucceededIdx {
		t.Fatalf("synthesize started (event %d) before the family it depends on succeeded (event %d)", synthesizeRunningIdx, familySucceededIdx)
	}
}
