package notification

import (
	"sync"
	"testing"
	"time"

	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/step"
	"jig/internal/workflow"
)

type fakeDispatcher struct {
	mu   sync.Mutex
	sent []Notification
}

func (f *fakeDispatcher) Enqueue(n Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
}

func (f *fakeDispatcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeDispatcher) events() []workflow.NotificationEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]workflow.NotificationEvent, len(f.sent))
	for i, n := range f.sent {
		out[i] = n.Event
	}
	return out
}

func setupLifecycle(t *testing.T, policy workflow.NotificationPolicy) (*Lifecycle, *fakeDispatcher) {
	t.Helper()
	fakeD := &fakeDispatcher{}
	SetPolicyLookup(func(runID string) workflow.NotificationPolicy { return policy })
	t.Cleanup(func() { SetPolicyLookup(nil) })

	// Use a synchronous timer that fires immediately for deterministic tests.
	timer := func(d time.Duration, cb func()) StoppableTimer {
		go cb()
		return &noopTimer{}
	}

	lc := NewLifecycle(fakeD, NewDiagnosticStore(), func(p workflow.NotificationPolicy) []Binding {
		out := make([]Binding, 0, len(p.Routes))
		for _, r := range p.Routes {
			out = append(out, NewBinding(r.Destination, Webhook, r.Events, "https://example.invalid/x", ""))
		}
		return out
	}, WithLifecycleTimer(timer), WithLifecycleIDGen(func() string { return "notif-fixed" }))
	return lc, fakeD
}

type noopTimer struct{}

func (*noopTimer) Stop() bool { return true }

func waitUntil(t *testing.T, f func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waited %s and predicate never held", timeout)
}

func TestLifecycleAttentionOnReview(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired, workflow.RunFailed},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired, workflow.RunFailed}}},
	}
	lc, fake := setupLifecycle(t, policy)
	lc.RunRegistered(engine.RunRegistration{RunID: "r", Workflow: "wf", Epoch: 1})
	lc.Publish(engine.ReviewRequest{RunID: "r", StepID: "step-1", RoundID: "round-1"})
	waitUntil(t, func() bool { return fake.count() == 1 }, time.Second)
	if fake.sent[0].Event != workflow.AttentionRequired {
		t.Fatalf("first event=%s", fake.sent[0].Event)
	}
	if len(fake.sent[0].Attention) != 1 || fake.sent[0].Attention[0].StepID != "step-1" {
		t.Fatalf("unexpected attention: %+v", fake.sent[0].Attention)
	}
}

func TestLifecycleAttentionSuppressedWhenNotSelected(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.RunFailed},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.RunFailed}}},
	}
	lc, fake := setupLifecycle(t, policy)
	lc.RunRegistered(engine.RunRegistration{RunID: "r", Workflow: "wf", Epoch: 1})
	lc.Publish(engine.ReviewRequest{RunID: "r", StepID: "step-1"})
	time.Sleep(100 * time.Millisecond)
	if fake.count() != 0 {
		t.Fatalf("attention emitted despite not selected: %+v", fake.sent)
	}
}

func TestLifecycleAllSevenAttentionKinds(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired}}},
	}
	tests := []struct {
		name string
		emit func(*Lifecycle)
		want AttentionKind
	}{
		{"review", func(l *Lifecycle) { l.Publish(engine.ReviewRequest{RunID: "r", StepID: "a"}) }, AttentionReview},
		{"input", func(l *Lifecycle) { l.Publish(engine.InputRequest{RunID: "r", StepID: "a"}) }, AttentionInput},
		{"prompt", func(l *Lifecycle) { l.Publish(engine.PromptRequest{RunID: "r", StepID: "a"}) }, AttentionPrompt},
		{"question", func(l *Lifecycle) {
			l.Publish(engine.AgentQuestion{RunID: "r", StepID: "a", Request: interaction.QuestionRequest{ID: "q1"}})
		}, AttentionQuestion},
		{"recovery", func(l *Lifecycle) { l.Publish(engine.RecoveryRequest{RunID: "r", StepID: "a"}) }, AttentionRecovery},
		{"integration_conflict", func(l *Lifecycle) { l.Publish(engine.IntegrationConflictRequest{RunID: "r", StepID: "a"}) }, AttentionIntegrationConflict},
		{"final_merge", func(l *Lifecycle) { l.Publish(engine.FinalMergeRequest{RunID: "r"}) }, AttentionFinalMerge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lc, fake := setupLifecycle(t, policy)
			lc.RunRegistered(engine.RunRegistration{RunID: "r", Workflow: "wf", Epoch: 1})
			tc.emit(lc)
			waitUntil(t, func() bool { return fake.count() >= 1 }, time.Second)
			if fake.sent[0].Attention[0].Kind != tc.want {
				t.Fatalf("wanted %s, got %s", tc.want, fake.sent[0].Attention[0].Kind)
			}
		})
	}
}

func TestLifecycleTerminalCauses(t *testing.T) {
	tests := []struct {
		name  string
		cause engine.CompletionCause
		want  workflow.NotificationEvent
	}{
		{"failed", engine.CauseFailed, workflow.RunFailed},
		{"timeout", engine.CauseTimeout, workflow.RunFailed},
		{"policy_rejection", engine.CausePolicyRejection, workflow.RunFailed},
		{"cancelled", engine.CauseCancelled, ""},
		{"succeeded", engine.CauseSucceeded, workflow.RunSucceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := workflow.NotificationPolicy{
				Events: []workflow.NotificationEvent{workflow.RunFailed, workflow.RunSucceeded},
				Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.RunFailed, workflow.RunSucceeded}}},
			}
			lc, fake := setupLifecycle(t, policy)
			lc.RunRegistered(engine.RunRegistration{RunID: "r", Workflow: "wf", Epoch: 1})
			lc.Publish(engine.RunFinished{RunID: "r", Cause: tc.cause})
			time.Sleep(80 * time.Millisecond)
			if tc.want == "" {
				if fake.count() != 0 {
					t.Fatalf("expected no notification, got %+v", fake.sent)
				}
			} else {
				if fake.count() != 1 || fake.sent[0].Event != tc.want {
					t.Fatalf("expected %s, got %+v", tc.want, fake.events())
				}
			}
		})
	}
}

func TestLifecycleResolveClearsAttention(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired}}},
	}
	lc, fake := setupLifecycle(t, policy)
	lc.RunRegistered(engine.RunRegistration{RunID: "r", Workflow: "wf", Epoch: 1})
	lc.Publish(engine.ReviewRequest{RunID: "r", StepID: "step-1", RoundID: "round-1"})
	waitUntil(t, func() bool { return fake.count() == 1 }, time.Second)
	// Resolve the wait; a subsequent status change should not add a new attention.
	lc.Publish(engine.ReviewSubmitted{RunID: "r", StepID: "step-1", RoundID: "round-1"})
	lc.Publish(engine.StepStatus{RunID: "r", StepID: "step-1", To: step.StatusSucceeded})
	time.Sleep(80 * time.Millisecond)
	if fake.count() != 1 {
		t.Fatalf("resolution caused extra send: %d", fake.count())
	}
}

func TestLifecycleReopenSeedsRestoredSummary(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired}}},
	}
	lc, fake := setupLifecycle(t, policy)
	lc.RunRegistered(engine.RunRegistration{
		RunID:    "r",
		Workflow: "wf",
		Epoch:    2,
		Reopen:   true,
		Policy:   policy,
		UnresolvedWaits: []engine.UnresolvedWait{
			{StepID: "step-1", Kind: engine.WaitReview},
			{StepID: "step-2", Kind: engine.WaitInput},
		},
	})
	waitUntil(t, func() bool { return fake.count() >= 1 }, time.Second)
	if len(fake.sent[0].Attention) != 2 {
		t.Fatalf("restored summary had %d items", len(fake.sent[0].Attention))
	}
	if !fake.sent[0].IsRestored {
		t.Fatalf("restored flag missing")
	}
}

// TestLifecycleReopenReplayNoDuplicate proves the reopen contract in the
// spec: exactly one filtered restored summary per (run, epoch, destination),
// even though the scheduler immediately re-publishes the parked ReviewRequest
// and other durable park events via emitBatch(reopenEvents). The seeded
// UnresolvedWait carries the same Nonce (review round id) as the replayed
// live event, so addWait deduplicates and no second attention flushes.
func TestLifecycleReopenReplayNoDuplicate(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired}}},
	}
	lc, fake := setupLifecycle(t, policy)
	lc.RunRegistered(engine.RunRegistration{
		RunID:    "r",
		Workflow: "wf",
		Epoch:    3,
		Reopen:   true,
		Policy:   policy,
		UnresolvedWaits: []engine.UnresolvedWait{
			{StepID: "gate", Kind: engine.WaitReview, Nonce: "round-1"},
		},
	})
	waitUntil(t, func() bool { return fake.count() >= 1 }, time.Second)
	if !fake.sent[0].IsRestored {
		t.Fatalf("first send was not the restored summary: %+v", fake.sent[0])
	}
	if got := len(fake.sent[0].Attention); got != 1 {
		t.Fatalf("restored summary had %d items", got)
	}

	lc.Publish(engine.ReviewRequest{RunID: "r", StepID: "gate", RoundID: "round-1"})
	time.Sleep(80 * time.Millisecond)
	if fake.count() != 1 {
		t.Fatalf("replay of parked ReviewRequest emitted a second attention: sent=%d events=%v", fake.count(), fake.events())
	}
}

// TestLifecycleReopenReplayMultipleDestinations proves the reopen contract
// also holds when several destinations select AttentionRequired: each
// destination receives one restored summary, and the subsequent replay does
// not fan out a second notification to any of them.
func TestLifecycleReopenReplayMultipleDestinations(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired},
		Routes: []workflow.NotificationRoute{
			{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired}},
			{Destination: "leads", Events: []workflow.NotificationEvent{workflow.AttentionRequired}},
		},
	}
	lc, fake := setupLifecycle(t, policy)
	lc.RunRegistered(engine.RunRegistration{
		RunID:    "r",
		Workflow: "wf",
		Epoch:    4,
		Reopen:   true,
		Policy:   policy,
		UnresolvedWaits: []engine.UnresolvedWait{
			{StepID: "gate", Kind: engine.WaitReview, Nonce: "round-1"},
		},
	})
	waitUntil(t, func() bool { return fake.count() >= 1 }, time.Second)
	if got := len(fake.sent[0].Destinations); got != 2 {
		t.Fatalf("restored summary destinations = %d, want 2", got)
	}
	if !fake.sent[0].IsRestored {
		t.Fatalf("restored flag missing")
	}

	lc.Publish(engine.ReviewRequest{RunID: "r", StepID: "gate", RoundID: "round-1"})
	time.Sleep(80 * time.Millisecond)
	if fake.count() != 1 {
		t.Fatalf("replay produced a second notification: %d", fake.count())
	}
}
