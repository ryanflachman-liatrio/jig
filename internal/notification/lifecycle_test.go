package notification

import (
	"encoding/json"
	"fmt"
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

// TestAttentionCoalescingBurst proves FR-14/task-3.4's core contract: roughly
// a hundred attention arrivals for one run/epoch/destination inside the
// coalescing window collapse into exactly one delivered notification, a
// resolved wait vanishes from that summary instead of being sent, and
// duplicate/overlapping destination routes still enqueue at most one logical
// notification per destination. Bounding to ten displayed descriptors with a
// total/omitted count is proved separately by TestBuildOutboundPayloadCoalescingAndBounds
// against the raw Attention list this test produces.
func TestAttentionCoalescingBurst(t *testing.T) {
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.AttentionRequired},
		Routes: []workflow.NotificationRoute{
			{Destination: "ops", Events: []workflow.NotificationEvent{workflow.AttentionRequired}},
		},
	}

	fakeD := &fakeDispatcher{}
	SetPolicyLookup(func(runID string) workflow.NotificationPolicy { return policy })
	t.Cleanup(func() { SetPolicyLookup(nil) })

	// Capture the flush callback instead of firing it, so the test controls
	// exactly when the 500ms coalescing window closes.
	var mu sync.Mutex
	var pendingFlush func()
	timer := func(d time.Duration, cb func()) StoppableTimer {
		if d != 500*time.Millisecond {
			t.Fatalf("expected the default 500ms window, got %s", d)
		}
		mu.Lock()
		pendingFlush = cb
		mu.Unlock()
		return &noopTimer{}
	}

	// Two overlapping routes to the SAME destination alias, mirroring what a
	// misresolved binding set would look like; destinationsForEvent must
	// still collapse them to a single logical destination.
	bindings := []Binding{
		NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.AttentionRequired}, "https://example.invalid/a", ""),
		NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.AttentionRequired}, "https://example.invalid/a", ""),
	}
	lc := NewLifecycle(fakeD, NewDiagnosticStore(), func(workflow.NotificationPolicy) []Binding {
		return bindings
	}, WithLifecycleTimer(timer), WithLifecycleIDGen(func() string { return "notif-burst" }))

	lc.RunRegistered(engine.RunRegistration{RunID: "r-burst", Workflow: "wf", Epoch: 9})

	const burst = 100
	for i := 0; i < burst; i++ {
		lc.Publish(engine.ReviewRequest{
			RunID:   "r-burst",
			StepID:  fmt.Sprintf("step-%03d", i),
			RoundID: fmt.Sprintf("round-%03d", i),
		})
	}
	if fakeD.count() != 0 {
		t.Fatalf("window closed early: %d notifications enqueued before flush", fakeD.count())
	}

	// Resolve one wait before the window closes; it must not appear in the
	// coalesced summary.
	lc.Publish(engine.ReviewSubmitted{RunID: "r-burst", StepID: "step-050", RoundID: "round-050"})

	mu.Lock()
	flush := pendingFlush
	mu.Unlock()
	if flush == nil {
		t.Fatal("no coalescing timer was scheduled for the burst")
	}
	flush()

	if got := fakeD.count(); got != 1 {
		t.Fatalf("expected exactly one coalesced notification for %d rapid arrivals, got %d", burst, got)
	}
	sent := fakeD.sent[0]
	if got := len(sent.Attention); got != burst-1 {
		t.Fatalf("coalesced summary has %d descriptors, want %d (one resolved wait excluded)", got, burst-1)
	}
	for _, d := range sent.Attention {
		if d.StepID == "step-050" {
			t.Fatalf("resolved wait step-050 leaked into the coalesced summary")
		}
	}
	if len(sent.Destinations) != 1 {
		t.Fatalf("overlapping routes to the same destination produced %d destinations, want 1", len(sent.Destinations))
	}

	// A second, later arrival must open a fresh window rather than extending
	// the one that already flushed. Resolve every wait still standing from
	// the burst first so the second summary reflects only the new arrival —
	// each flush reports the full currently-unresolved set, so any wait left
	// open from the first window would otherwise legitimately reappear here.
	for i := 0; i < burst; i++ {
		if i == 50 {
			continue // already resolved above
		}
		lc.Publish(engine.ReviewSubmitted{
			RunID:   "r-burst",
			StepID:  fmt.Sprintf("step-%03d", i),
			RoundID: fmt.Sprintf("round-%03d", i),
		})
	}
	lc.Publish(engine.ReviewRequest{RunID: "r-burst", StepID: "step-late", RoundID: "round-late"})
	mu.Lock()
	flush2 := pendingFlush
	mu.Unlock()
	if flush2 == nil {
		t.Fatal("no new coalescing timer scheduled for the late arrival")
	}
	flush2()
	if got := fakeD.count(); got != 2 {
		t.Fatalf("expected a second coalesced notification for the late arrival, got %d total", got)
	}
	if got := len(fakeD.sent[1].Attention); got != 1 || fakeD.sent[1].Attention[0].StepID != "step-late" {
		t.Fatalf("second window carried unexpected attention: %+v", fakeD.sent[1].Attention)
	}

	// Downstream payload bounding (task 3.4's ten-descriptor cap) applies to
	// exactly the raw list this coalescing pass produced.
	payload := BuildOutboundPayload(sent)
	var decoded jsonPayload
	if err := json.Unmarshal(payload.JSON, &decoded); err != nil {
		t.Fatalf("unmarshal payload JSON: %v", err)
	}
	if decoded.AttentionCount != MaxDisplayedAttention {
		t.Fatalf("payload displayed %d attention items, want %d", decoded.AttentionCount, MaxDisplayedAttention)
	}
	if decoded.OmittedCount != burst-1-MaxDisplayedAttention {
		t.Fatalf("payload omitted count = %d, want %d", decoded.OmittedCount, burst-1-MaxDisplayedAttention)
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
