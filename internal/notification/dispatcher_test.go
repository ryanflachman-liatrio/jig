package notification

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/workflow"
)

type recordingSender struct {
	mu       sync.Mutex
	sent     []Notification
	response func(target Binding, payload OutboundPayload) SendResult
}

func (r *recordingSender) Send(ctx context.Context, target Binding, payload OutboundPayload) SendResult {
	r.mu.Lock()
	r.sent = append(r.sent, Notification{ID: string(payload.JSON), Destinations: []Binding{target}})
	r.mu.Unlock()
	if r.response == nil {
		return SendResult{Reason: "delivered"}
	}
	return r.response(target, payload)
}

func (r *recordingSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func newTestDispatcher(sender Sender) (*Dispatcher, *DiagnosticStore) {
	diag := NewDiagnosticStore()
	return NewDispatcher(sender, diag), diag
}

func TestDispatcherEnqueueTerminalDelivered(t *testing.T) {
	sender := &recordingSender{}
	d, _ := newTestDispatcher(sender)
	target := NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.RunFailed}, "https://example.invalid/", "")
	d.Enqueue(Notification{
		ID: "n1", Event: workflow.RunFailed, RunID: "r", Workflow: "wf",
		Destinations: []Binding{target},
	})
	waitForCount(t, sender, 1, time.Second)
	d.Shutdown(context.Background())
	if sender.count() != 1 {
		t.Fatalf("expected 1 send, got %d", sender.count())
	}
}

func TestDispatcherDeduplicatesTerminalPerEpoch(t *testing.T) {
	sender := &recordingSender{}
	d, _ := newTestDispatcher(sender)
	target := NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.RunFailed}, "https://example.invalid/", "")
	n := Notification{ID: "n1", Event: workflow.RunFailed, RunID: "r", Epoch: 1, Workflow: "wf", Destinations: []Binding{target}}
	d.Enqueue(n)
	d.Enqueue(n)
	waitForCount(t, sender, 1, time.Second)
	d.Shutdown(context.Background())
	if sender.count() != 1 {
		t.Fatalf("expected 1 dedup, got %d", sender.count())
	}
}

func TestDispatcherTerminalEvictsPending(t *testing.T) {
	var startCh sync.Once
	blockCh := make(chan struct{})
	release := make(chan struct{})
	sender := &recordingSender{response: func(target Binding, payload OutboundPayload) SendResult {
		startCh.Do(func() { close(blockCh) })
		<-release
		return SendResult{Reason: "delivered"}
	}}
	d, diag := newTestDispatcher(sender)

	target := NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.RunFailed, workflow.AttentionRequired}, "https://example.invalid/", "")

	// First, occupy the sender with a slow send.
	d.Enqueue(Notification{ID: "a1", Event: workflow.AttentionRequired, RunID: "r", Workflow: "wf",
		Attention: []AttentionDescriptor{{StepID: "s0", Kind: AttentionReview}}, Destinations: []Binding{target}})
	<-blockCh

	// Fill the pending queue with non-failure notifications.
	for i := 0; i < QueueCapacity; i++ {
		d.Enqueue(Notification{ID: "a" + string(rune('a'+i%26)), Event: workflow.AttentionRequired, RunID: "r", Workflow: "wf",
			Attention: []AttentionDescriptor{{StepID: "s", Kind: AttentionReview}},
			Destinations: []Binding{target}})
	}

	// Enqueue a run_failed which should evict an attention entry.
	d.Enqueue(Notification{ID: "fail1", Event: workflow.RunFailed, RunID: "r", Workflow: "wf", Destinations: []Binding{target}})

	close(release)
	d.Shutdown(context.Background())

	counts := diag.OverflowCounts()
	// At least one evicted diagnostic should be present.
	entries := diag.Snapshot()
	foundEvicted := false
	for _, e := range entries {
		if e.Outcome == "dropped" && e.Reason == "evicted" {
			foundEvicted = true
			break
		}
	}
	if !foundEvicted {
		t.Fatalf("expected evicted diagnostic, entries=%+v overflow=%+v", entries, counts)
	}
}

func TestDispatcherFiltersResolvedWaits(t *testing.T) {
	sender := &recordingSender{}
	d, _ := newTestDispatcher(sender)
	d.waitResolver = WaitResolverFunc(func(runID string, waits []WaitIdentity) []WaitIdentity {
		return nil // all waits resolved
	})
	target := NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.AttentionRequired}, "https://example.invalid/", "")
	d.Enqueue(Notification{ID: "n1", Event: workflow.AttentionRequired, RunID: "r", Workflow: "wf",
		Attention: []AttentionDescriptor{{StepID: "s1", Kind: AttentionReview}}, Destinations: []Binding{target}})
	time.Sleep(200 * time.Millisecond)
	d.Shutdown(context.Background())
	if sender.count() != 0 {
		t.Fatalf("resolver=nil should suppress send, got %d", sender.count())
	}
}

func TestDispatcherRetryUpToLimit(t *testing.T) {
	var attempts atomic.Int32
	sender := &recordingSender{response: func(target Binding, payload OutboundPayload) SendResult {
		attempts.Add(1)
		return SendResult{Reason: "http_5xx", Retry: true, RetryAfter: 10 * time.Millisecond}
	}}
	d, _ := newTestDispatcher(sender)
	target := NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.RunFailed}, "https://example.invalid/", "")
	d.Enqueue(Notification{ID: "n1", Event: workflow.RunFailed, RunID: "r", Workflow: "wf", Destinations: []Binding{target}})
	// Give the dispatcher time to complete retries.
	deadline := time.Now().Add(2 * time.Second)
	for attempts.Load() < MaxRetryAttempts && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	d.Shutdown(context.Background())
	if got := attempts.Load(); got != MaxRetryAttempts {
		t.Fatalf("expected %d attempts, got %d", MaxRetryAttempts, got)
	}
}

func TestDispatcherShutdownDrains(t *testing.T) {
	sender := &recordingSender{}
	d, _ := newTestDispatcher(sender)
	target := NewBinding("ops", Webhook, []workflow.NotificationEvent{workflow.RunFailed}, "https://example.invalid/", "")
	d.Enqueue(Notification{ID: "n1", Event: workflow.RunFailed, RunID: "r", Workflow: "wf", Destinations: []Binding{target}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.Shutdown(ctx)
	if sender.count() != 1 {
		t.Fatalf("shutdown did not drain: %d", sender.count())
	}
}

func waitForCount(t *testing.T, sender *recordingSender, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sender.count() >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited %s for %d sends, got %d", timeout, want, sender.count())
}
