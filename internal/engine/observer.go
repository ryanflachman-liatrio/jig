package engine

import (
	"context"
	"sync"

	"jig/internal/workflow"
)

// Observer is the non-blocking lifecycle contract for consumers that live at
// the composition boundary (notifications today; other operator-facing
// side-effect services later). It intentionally does not depend on
// notification packages — the engine only publishes normalized lifecycle
// state.
//
// Publication is best-effort from the engine's perspective. Implementations
// MUST NOT block: a slow observer cannot delay scheduler progress or worker
// completion. When an observer's queue is full it should signal
// ObserverBackpressure so the engine can account for the loss without
// pausing.
type Observer interface {
	// RunRegistered is called exactly once per Run instance (both fresh
	// starts and active reopens) before any live event is published for
	// that run. reg carries the immutable metadata a consumer needs to
	// classify subsequent events and, on reopen, the seeded unresolved
	// wait descriptors so the consumer can emit a filtered restored
	// summary without replaying history.
	RunRegistered(reg RunRegistration)

	// Publish delivers one lifecycle event. Implementations must return
	// quickly: forward to an unbounded (or bounded-with-drop) channel and
	// return immediately. Returning does not mean the event was processed.
	Publish(ev Event)

	// ProducersStopped signals that no further live events will be
	// published for the identified run. Consumers can drain per-run state
	// once no in-flight work references it.
	ProducersStopped(runID string)
}

// RunRegistration is the immutable, per-execution-epoch record the engine
// hands to an observer at run start (or active reopen).
//
// Snapshot returns a point-in-time RunSnapshot without traversing the
// scheduler inbox synchronously; consumers use it to re-check parked waits
// before a coalesced notification is delivered. Snapshot may be nil when a
// consumer does not need it (tests).
//
// UnresolvedWaits is populated only when Reopen is true; for a fresh Start
// it is nil. The engine walks the rehydrated park state and hands the
// observer one descriptor per still-pending human wait.
type RunRegistration struct {
	RunID    string
	Workflow string
	// Epoch is a monotonic identifier that changes for each fresh start or
	// active reopen of the same run id. Consumers key their state by
	// (RunID, Epoch) so a subsequent reopen never merges with the prior
	// epoch's notifications.
	Epoch int
	// Reopen distinguishes an active resume from a fresh start.
	Reopen bool
	// Snapshot, when non-nil, returns a live point-in-time view of the
	// run's step states. The scheduler owns the underlying state so this
	// closure routes through the scheduler goroutine.
	Snapshot func() RunSnapshot
	// Policy is the run's frozen resolved notification policy. It is
	// captured at registration time (from the workflow snapshot on reopen,
	// from the live workflow on fresh start) so an observer never has to
	// re-read profile files or peek at operator configuration to
	// classify the run.
	Policy workflow.NotificationPolicy
	// UnresolvedWaits describes each human wait that was already parked
	// when the run was reopened. For a fresh Start it is nil.
	UnresolvedWaits []UnresolvedWait
}

// UnresolvedWait is a compact descriptor of one parked human wait carried on
// reopen. Kinds mirror the seven attention categories the notification spec
// enumerates; consumers translate them to the notification vocabulary.
type UnresolvedWait struct {
	StepID string
	Kind   WaitKind
}

// WaitKind classifies unresolved human waits at reopen. The values match the
// notification specification's attention categories so consumers do not need
// to translate between engine and product vocabulary.
type WaitKind string

const (
	WaitReview              WaitKind = "review"
	WaitInput               WaitKind = "input"
	WaitPrompt              WaitKind = "prompt"
	WaitQuestion            WaitKind = "question"
	WaitRecovery            WaitKind = "recovery"
	WaitIntegrationConflict WaitKind = "integration_conflict"
	WaitFinalMerge          WaitKind = "final_merge"
)

// observerRegistry is Manager's non-blocking fan-out to registered observers.
// It is intentionally simpler than the Subscribe() sub channels: an observer
// owns its own queue and drop policy, so the engine's fan-out is a straight
// slice iteration with a small per-observer mutex.
type observerRegistry struct {
	mu        sync.RWMutex
	observers []Observer
}

func (r *observerRegistry) add(o Observer) {
	if o == nil {
		return
	}
	r.mu.Lock()
	r.observers = append(r.observers, o)
	r.mu.Unlock()
}

func (r *observerRegistry) list() []Observer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.observers) == 0 {
		return nil
	}
	out := make([]Observer, len(r.observers))
	copy(out, r.observers)
	return out
}

// dispatchRegistered fans a RunRegistration out to all observers. Any panic
// in one observer must not stop the others, matching the drop-on-full
// contract for live subscribers.
func (r *observerRegistry) dispatchRegistered(reg RunRegistration) {
	for _, o := range r.list() {
		safeRegister(o, reg)
	}
}

func (r *observerRegistry) dispatchPublish(ev Event) {
	for _, o := range r.list() {
		safePublish(o, ev)
	}
}

func (r *observerRegistry) dispatchStopped(runID string) {
	for _, o := range r.list() {
		safeStopped(o, runID)
	}
}

func safeRegister(o Observer, reg RunRegistration) {
	defer recoverObserver()
	o.RunRegistered(reg)
}

func safePublish(o Observer, ev Event) {
	defer recoverObserver()
	o.Publish(ev)
}

func safeStopped(o Observer, runID string) {
	defer recoverObserver()
	o.ProducersStopped(runID)
}

func recoverObserver() {
	_ = recover()
}

// RegisterObserver installs o as a lifecycle observer. Registration is
// idempotent for a given observer only in the sense that repeated adds
// register the observer multiple times — callers own uniqueness. Observers
// installed after Start register but do not receive registrations for
// already-running runs; production wiring installs observers before Start.
func (m *Manager) RegisterObserver(o Observer) {
	m.observers.add(o)
}

// contextWithoutCancel returns a background context; retained so future
// observer implementations can cancel their own long-running work without
// tying it to the run context.
func contextWithoutCancel() context.Context { return context.Background() }
