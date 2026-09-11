package notification

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"jig/internal/workflow"
)

const (
	// Bounded dispatcher defaults enumerated in the specification.
	QueueCapacity    = 256
	MaxActiveSends   = 4
	AttentionWindow  = 500 * time.Millisecond
	MessageLifetime  = 30 * time.Second
	SlackPacing      = 1 * time.Second
	MaxRetryAttempts = 3
	ShutdownDrainCap = 5 * time.Second
)

// Clock is a minimal seam so deterministic tests can control time without
// blocking the dispatcher on real sleeps.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer mirrors the subset of time.Timer the dispatcher needs.
type Timer interface {
	Chan() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// realClock adapts stdlib time.
type realClock struct{}

func (realClock) Now() time.Time                 { return time.Now() }
func (realClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (rt realTimer) Chan() <-chan time.Time { return rt.t.C }
func (rt realTimer) Stop() bool             { return rt.t.Stop() }
func (rt realTimer) Reset(d time.Duration) bool {
	rt.t.Stop()
	return rt.t.Reset(d)
}

// WaitResolver reports which wait identities remain unresolved for a run. The
// dispatcher consults it immediately before each initial send and each retry
// so a coalesced attention summary is never delivered with items whose waits
// have already been answered.
type WaitResolver interface {
	Filter(runID string, waits []WaitIdentity) []WaitIdentity
}

// WaitResolverFunc adapts a plain function to the WaitResolver interface.
type WaitResolverFunc func(runID string, waits []WaitIdentity) []WaitIdentity

func (f WaitResolverFunc) Filter(runID string, waits []WaitIdentity) []WaitIdentity {
	return f(runID, waits)
}

// Dispatcher is the single-owner queue that turns Notifications into bounded
// Send attempts. It is process-wide: one instance serves every run.
type Dispatcher struct {
	sender       Sender
	diagnostics  *DiagnosticStore
	clock        Clock
	waitResolver WaitResolver

	// active send slots keyed by destination alias.
	mu        sync.Mutex
	closed    bool
	acceptors bool
	pending   []*pendingEntry
	inFlight  int
	lastStart map[string]time.Time // per-destination pacing
	terminals map[terminalKey]bool // dedupe terminal events per (run, epoch, event)
	drained   chan struct{}
}

// pendingEntry carries one queued or in-flight notification plus its retry
// state.
type pendingEntry struct {
	notif      Notification
	waits      []WaitIdentity
	attempts   int
	enqueuedAt time.Time
	notBefore  time.Time
	sending    bool
	binding    Binding
}

// terminalKey deduplicates a terminal event per (run, epoch, event) so a
// re-run scheduler cannot emit two run_failed notifications for the same
// settlement.
type terminalKey struct {
	Run   string
	Epoch int
	Event workflow.NotificationEvent
}

// DispatcherOption customizes the dispatcher at construction.
type DispatcherOption func(*Dispatcher)

// WithClock installs a fake clock (tests only).
func WithClock(c Clock) DispatcherOption { return func(d *Dispatcher) { d.clock = c } }

// WithWaitResolver installs a resolver consulted before every send/retry.
func WithWaitResolver(r WaitResolver) DispatcherOption {
	return func(d *Dispatcher) { d.waitResolver = r }
}

// WithResolver installs a resolver after construction. Wire code uses this
// because the resolver (Lifecycle) is constructed with a reference to the
// Dispatcher, creating a circular dependency at construction time.
func (d *Dispatcher) WithResolver(r WaitResolver) {
	d.waitResolver = r
}

// NewDispatcher builds a dispatcher tied to the given sender and diagnostic
// store. Callers may Enqueue immediately; the dispatcher spins up worker
// goroutines lazily.
func NewDispatcher(sender Sender, diag *DiagnosticStore, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		sender:      sender,
		diagnostics: diag,
		clock:       realClock{},
		lastStart:   make(map[string]time.Time),
		terminals:   make(map[terminalKey]bool),
		acceptors:   true,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Enqueue submits one logical notification. The notification is expanded to
// its bound destinations; each destination becomes an independently-scheduled
// send. Enqueue never blocks on network I/O.
func (d *Dispatcher) Enqueue(n Notification) {
	if d == nil {
		return
	}
	if len(n.Destinations) == 0 {
		return
	}
	if n.Event == "" {
		return
	}
	if err := d.validateEvent(n.Event); err != nil {
		d.diagnostics.RecordOverflow("invalid_event")
		return
	}
	// Filter attention immediately so a coalesced summary that is already empty
	// never enqueues at all.
	waits := waitIdentitiesFor(n)
	if n.Event == workflow.AttentionRequired {
		filtered := d.filter(n.RunID, waits)
		if len(filtered) == 0 {
			return
		}
		n.Attention = descriptorsFromWaits(filtered)
		n.AttentionN = len(filtered)
		waits = filtered
	}
	if n.Timestamp.IsZero() {
		n.Timestamp = d.clock.Now().UTC()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.acceptors || d.closed {
		d.diagnostics.RecordOverflow("dispatcher_closed")
		return
	}

	if terminalEvent(n.Event) {
		key := terminalKey{Run: n.RunID, Epoch: n.Epoch, Event: n.Event}
		if d.terminals[key] {
			return
		}
		d.terminals[key] = true
	}

	for _, target := range n.Destinations {
		if err := d.enqueueOneLocked(&pendingEntry{
			notif:      n,
			binding:    target,
			waits:      append([]WaitIdentity(nil), waits...),
			enqueuedAt: d.clock.Now(),
			notBefore:  d.clock.Now(),
		}); err != nil {
			d.recordDrop(target.Alias, n, "queue_full", 0)
		}
	}

	go d.pump()
}

func (d *Dispatcher) validateEvent(ev workflow.NotificationEvent) error {
	switch ev {
	case workflow.AttentionRequired, workflow.RunFailed, workflow.RunSucceeded:
		return nil
	}
	return errors.New("unknown event")
}

// enqueueOneLocked appends a pending entry or, under pressure, evicts a
// non-failure to make room for an incoming failure. Callers must hold d.mu.
func (d *Dispatcher) enqueueOneLocked(entry *pendingEntry) error {
	if len(d.pending) < QueueCapacity {
		d.pending = append(d.pending, entry)
		return nil
	}
	if entry.notif.Event != workflow.RunFailed {
		return errors.New("queue full")
	}
	// Evict the oldest non-failure to prioritize terminal failure delivery.
	for i, existing := range d.pending {
		if existing.notif.Event != workflow.RunFailed {
			d.pending = append(d.pending[:i], d.pending[i+1:]...)
			d.pending = append(d.pending, entry)
			d.recordDrop(existing.binding.Alias, existing.notif, "evicted", existing.attempts)
			return nil
		}
	}
	return errors.New("queue full")
}

// pump is the dispatcher's central loop. It picks up ready pending entries,
// throttles per-destination pacing, and runs Send in a worker goroutine. The
// dispatcher retains only bounded state on the pending slice; workers hand
// their result back through a mutex-guarded update.
func (d *Dispatcher) pump() {
	for {
		d.mu.Lock()
		if d.closed || (!d.acceptors && len(d.pending) == 0 && d.inFlight == 0) {
			d.mu.Unlock()
			return
		}
		nextIndex := -1
		now := d.clock.Now()
		var nextWake time.Time
		for i, e := range d.pending {
			if e.sending {
				continue
			}
			if e.notBefore.After(now) {
				if nextWake.IsZero() || e.notBefore.Before(nextWake) {
					nextWake = e.notBefore
				}
				continue
			}
			if d.inFlight >= MaxActiveSends {
				break
			}
			if d.hasActiveForAliasLocked(e.binding.Alias) {
				continue
			}
			if e.binding.Type == Slack {
				if last, ok := d.lastStart[e.binding.Alias]; ok {
					earliest := last.Add(SlackPacing)
					if earliest.After(now) {
						if nextWake.IsZero() || earliest.Before(nextWake) {
							nextWake = earliest
						}
						continue
					}
				}
			}
			nextIndex = i
			break
		}
		if nextIndex < 0 {
			d.mu.Unlock()
			if !nextWake.IsZero() {
				delay := time.Until(nextWake)
				if delay < 0 {
					delay = 0
				}
				d.clock.NewTimer(delay)
				go func() {
					time.Sleep(delay)
					d.pump()
				}()
			}
			return
		}
		entry := d.pending[nextIndex]
		entry.sending = true
		entry.attempts++
		d.inFlight++
		if entry.binding.Type == Slack {
			d.lastStart[entry.binding.Alias] = now
		}
		d.mu.Unlock()

		go d.deliver(entry)
	}
}

func (d *Dispatcher) hasActiveForAliasLocked(alias string) bool {
	for _, e := range d.pending {
		if e.sending && e.binding.Alias == alias {
			return true
		}
	}
	return false
}

// deliver runs one send attempt, records its outcome, and either removes the
// entry from the queue on success/abandonment or schedules a bounded retry.
func (d *Dispatcher) deliver(entry *pendingEntry) {
	// Re-filter waits immediately before sending so a coalesced summary whose
	// waits resolved between enqueue and send does not go out.
	if entry.notif.Event == workflow.AttentionRequired {
		filtered := d.filter(entry.notif.RunID, entry.waits)
		if len(filtered) == 0 {
			d.finish(entry, SendResult{Reason: "waits_resolved"})
			return
		}
		entry.notif.Attention = descriptorsFromWaits(filtered)
		entry.notif.AttentionN = len(filtered)
		entry.waits = filtered
	}

	payload := BuildOutboundPayload(entry.notif)
	remaining := MessageLifetime - d.clock.Now().Sub(entry.enqueuedAt)
	if remaining <= 0 {
		d.finish(entry, SendResult{Reason: "lifetime_expired"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()

	result := d.sender.Send(ctx, entry.binding, payload)
	if result.Reason == "" {
		result.Reason = "unknown"
	}
	d.finish(entry, result)
}

// finish records the send outcome and either removes the entry or schedules
// its retry. Callers hold no locks.
func (d *Dispatcher) finish(entry *pendingEntry, result SendResult) {
	outcome := "delivered"
	if !result.Success() {
		outcome = "failed"
	}
	d.diagnostics.Record(Diagnostic{
		Time:    d.clock.Now().UTC(),
		Alias:   entry.binding.Alias,
		RunID:   entry.notif.RunID,
		Event:   string(entry.notif.Event),
		Outcome: outcome,
		Reason:  result.Reason,
		Attempt: entry.attempts,
		NotifID: entry.notif.ID,
	})

	shouldRetry := result.Retry && entry.attempts < MaxRetryAttempts && !d.closed
	next := d.clock.Now()
	if shouldRetry {
		delay := result.RetryAfter
		if delay == 0 {
			delay = defaultRetryDelay(entry.attempts)
		}
		remaining := MessageLifetime - next.Sub(entry.enqueuedAt)
		if delay > remaining {
			shouldRetry = false
		} else {
			next = next.Add(delay)
		}
	}

	d.mu.Lock()
	d.inFlight--
	if shouldRetry {
		entry.sending = false
		entry.notBefore = next
	} else {
		for i, existing := range d.pending {
			if existing == entry {
				d.pending = append(d.pending[:i], d.pending[i+1:]...)
				break
			}
		}
	}
	if !d.acceptors && len(d.pending) == 0 && d.inFlight == 0 && d.drained != nil {
		close(d.drained)
		d.drained = nil
	}
	d.mu.Unlock()

	go d.pump()
}

// StopAdmission stops accepting new notifications so callers can begin
// draining. Already-queued entries continue to run until Shutdown times out.
func (d *Dispatcher) StopAdmission() {
	d.mu.Lock()
	d.acceptors = false
	d.mu.Unlock()
}

// Shutdown drains up to ShutdownDrainCap and cancels any remaining work.
// Callers should call StopAdmission first (or Shutdown will call it).
func (d *Dispatcher) Shutdown(ctx context.Context) {
	d.mu.Lock()
	d.acceptors = false
	if len(d.pending) == 0 && d.inFlight == 0 {
		d.closed = true
		d.mu.Unlock()
		return
	}
	drained := make(chan struct{})
	d.drained = drained
	d.mu.Unlock()

	drainCtx, cancel := context.WithTimeout(ctx, ShutdownDrainCap)
	defer cancel()
	select {
	case <-drained:
	case <-drainCtx.Done():
	}

	d.mu.Lock()
	remaining := len(d.pending)
	if remaining > 0 {
		d.diagnostics.RecordOverflow("shutdown_cancelled")
	}
	d.pending = nil
	d.closed = true
	d.drained = nil
	d.mu.Unlock()
}

func (d *Dispatcher) filter(runID string, waits []WaitIdentity) []WaitIdentity {
	if d.waitResolver == nil {
		return waits
	}
	return d.waitResolver.Filter(runID, waits)
}

func (d *Dispatcher) recordDrop(alias string, n Notification, reason string, attempts int) {
	d.diagnostics.Record(Diagnostic{
		Time:    d.clock.Now().UTC(),
		Alias:   alias,
		RunID:   n.RunID,
		Event:   string(n.Event),
		Outcome: "dropped",
		Reason:  reason,
		Attempt: attempts,
		NotifID: n.ID,
	})
}

func defaultRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Second
	case 2:
		return 2 * time.Second
	default:
		return 4 * time.Second
	}
}

func waitIdentitiesFor(n Notification) []WaitIdentity {
	waits := make([]WaitIdentity, len(n.Attention))
	for i, a := range n.Attention {
		waits[i] = WaitIdentity{StepID: a.StepID, Kind: a.Kind}
	}
	return waits
}

func descriptorsFromWaits(waits []WaitIdentity) []AttentionDescriptor {
	out := make([]AttentionDescriptor, len(waits))
	for i, w := range waits {
		out[i] = AttentionDescriptor{StepID: w.StepID, Kind: w.Kind}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StepID == out[j].StepID {
			return out[i].Kind < out[j].Kind
		}
		return out[i].StepID < out[j].StepID
	})
	return out
}

func terminalEvent(ev workflow.NotificationEvent) bool {
	return ev == workflow.RunFailed || ev == workflow.RunSucceeded
}
