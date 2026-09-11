package notification

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sync"
	"time"

	"jig/internal/engine"
	"jig/internal/workflow"
)

// Lifecycle is the engine Observer that normalizes ctrl-class events into
// selected outbound notifications. It owns:
//
//   - per-run policy resolution based on the workflow snapshot;
//   - per-run/per-epoch wait tracking and coalescing;
//   - a bounded attention window that flushes on timer expiry or terminal
//     transitions.
//
// It does not perform network or desktop I/O itself; it hands prepared
// Notifications to the injected Dispatcher.
type Lifecycle struct {
	dispatch dispatchTarget
	diag     *DiagnosticStore
	bindings func(policy workflow.NotificationPolicy) []Binding
	idGen    IDGenerator
	clock    func() time.Time
	timer    func(d time.Duration, f func()) StoppableTimer
	window   time.Duration

	mu   sync.Mutex
	runs map[string]*runLifecycle
}

// dispatchTarget is the narrow interface Lifecycle needs from the Dispatcher.
type dispatchTarget interface {
	Enqueue(Notification)
}

// IDGenerator returns opaque logical notification IDs. Tests can substitute a
// deterministic one; production uses a 128-bit random id.
type IDGenerator func() string

// StoppableTimer is a thin wrapper around time.AfterFunc so tests can stub it.
type StoppableTimer interface {
	Stop() bool
}

// LifecycleOption customizes construction.
type LifecycleOption func(*Lifecycle)

// WithLifecycleClock installs a fake wall clock (tests only).
func WithLifecycleClock(f func() time.Time) LifecycleOption {
	return func(l *Lifecycle) { l.clock = f }
}

// WithLifecycleIDGen installs a deterministic ID generator (tests only).
func WithLifecycleIDGen(g IDGenerator) LifecycleOption {
	return func(l *Lifecycle) { l.idGen = g }
}

// WithLifecycleTimer installs a fake timer factory (tests only).
func WithLifecycleTimer(f func(d time.Duration, cb func()) StoppableTimer) LifecycleOption {
	return func(l *Lifecycle) { l.timer = f }
}

// WithLifecycleWindow overrides the coalescing window (tests only).
func WithLifecycleWindow(w time.Duration) LifecycleOption {
	return func(l *Lifecycle) { l.window = w }
}

// NewLifecycle constructs a lifecycle normalizer. Bindings resolves the
// resolved workflow policy against the current operator configuration; the
// dispatcher owns retry and shutdown lifecycle for the produced messages.
func NewLifecycle(
	target dispatchTarget,
	diag *DiagnosticStore,
	bindings func(policy workflow.NotificationPolicy) []Binding,
	opts ...LifecycleOption,
) *Lifecycle {
	l := &Lifecycle{
		dispatch: target,
		diag:     diag,
		bindings: bindings,
		clock:    func() time.Time { return time.Now().UTC() },
		window:   AttentionWindow,
		runs:     make(map[string]*runLifecycle),
	}
	l.idGen = defaultIDGen
	l.timer = func(d time.Duration, cb func()) StoppableTimer { return time.AfterFunc(d, cb) }
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// runLifecycle carries per-run/epoch state used to coalesce attention and
// deduplicate terminal transitions. All fields are guarded by Lifecycle.mu.
type runLifecycle struct {
	runID    string
	workflow string
	epoch    int
	policy   workflow.NotificationPolicy
	bindings []Binding
	// waits tracks currently-unresolved parked human waits keyed by wait
	// identity. A wait's presence in the map means the notification
	// consumer still considers it live.
	waits map[waitKey]WaitIdentity
	// pending is set while a coalescing window is open.
	pendingTimer StoppableTimer
	terminal     bool
}

type waitKey struct {
	StepID string
	Kind   AttentionKind
	Nonce  string
}

// RunRegistered implements engine.Observer. It installs per-run state and, on
// reopen, seeds the wait map so a single filtered restored summary is
// dispatched immediately.
func (l *Lifecycle) RunRegistered(reg engine.RunRegistration) {
	if reg.RunID == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if existing, ok := l.runs[reg.RunID]; ok {
		// A distinct epoch replaces prior state entirely — see spec FR-14 on
		// distinguishing reopen epochs.
		existing.terminal = true
		if existing.pendingTimer != nil {
			existing.pendingTimer.Stop()
		}
		delete(l.runs, reg.RunID)
	}

	policy := reg.Policy
	if policy.Events == nil && policy.Routes == nil {
		// Fall back to the package-scoped lookup (test seam / older
		// registration paths) before treating the run as notification-off.
		if fallback, ok := workflowPolicy(reg); ok {
			policy = fallback
		}
	}
	// A workflow with no events or no destinations is a no-op observer.
	if len(policy.Events) == 0 || len(policy.Routes) == 0 {
		return
	}
	bindings := l.bindings(policy)
	if len(bindings) == 0 {
		return
	}

	state := &runLifecycle{
		runID:    reg.RunID,
		workflow: reg.Workflow,
		epoch:    reg.Epoch,
		policy:   policy,
		bindings: bindings,
		waits:    make(map[waitKey]WaitIdentity),
	}
	for _, w := range reg.UnresolvedWaits {
		id := WaitIdentity{StepID: w.StepID, Kind: waitKindToAttention(w.Kind), Nonce: w.Nonce}
		state.waits[waitKey{StepID: id.StepID, Kind: id.Kind, Nonce: id.Nonce}] = id
	}
	l.runs[reg.RunID] = state

	if reg.Reopen && len(state.waits) > 0 {
		// One filtered restored summary per (run, epoch, destination). No
		// coalescing window: the reopen path publishes immediately.
		l.emitAttentionLocked(state, true)
	}
}

// ProducersStopped implements engine.Observer.
func (l *Lifecycle) ProducersStopped(runID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.runs[runID]
	if !ok {
		return
	}
	if state.pendingTimer != nil {
		state.pendingTimer.Stop()
		state.pendingTimer = nil
	}
	delete(l.runs, runID)
}

// Publish implements engine.Observer.
func (l *Lifecycle) Publish(ev engine.Event) {
	switch e := ev.(type) {
	case engine.ReviewRequest:
		l.addWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionReview, Nonce: e.RoundID})
	case engine.ReviewSubmitted:
		l.resolveWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionReview, Nonce: e.RoundID})
	case engine.InputRequest:
		l.addWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionInput})
	case engine.PromptRequest:
		l.addWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionPrompt, Nonce: e.As})
	case engine.AgentQuestion:
		l.addWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionQuestion, Nonce: e.Request.ID})
	case engine.AgentQuestionResolved:
		l.resolveWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionQuestion, Nonce: e.RequestID})
	case engine.RecoveryRequest:
		l.addWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionRecovery})
	case engine.IntegrationConflictRequest:
		l.addWait(e.RunID, WaitIdentity{StepID: e.StepID, Kind: AttentionIntegrationConflict})
	case engine.FinalMergeRequest:
		l.addWait(e.RunID, WaitIdentity{Kind: AttentionFinalMerge})
	case engine.StepStatus:
		// Any transition out of an awaiting_* status resolves its wait.
		l.resolveWaitsForStatus(e)
	case engine.RunFinished:
		l.handleRunFinished(e)
	}
}

func (l *Lifecycle) addWait(runID string, id WaitIdentity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.runs[runID]
	if !ok || state.terminal {
		return
	}
	key := waitKey{StepID: id.StepID, Kind: id.Kind, Nonce: id.Nonce}
	if _, exists := state.waits[key]; exists {
		return
	}
	state.waits[key] = id
	// Attention notifications are enabled only when the workflow policy
	// selected the event.
	if !slices.Contains(state.policy.Events, workflow.AttentionRequired) {
		return
	}
	if state.pendingTimer == nil {
		state.pendingTimer = l.timer(l.window, func() { l.flushAttention(runID) })
	}
}

func (l *Lifecycle) resolveWait(runID string, id WaitIdentity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.runs[runID]
	if !ok {
		return
	}
	key := waitKey{StepID: id.StepID, Kind: id.Kind, Nonce: id.Nonce}
	delete(state.waits, key)
}

// resolveWaitsForStatus removes waits whose backing step returned to a
// non-parked status.
func (l *Lifecycle) resolveWaitsForStatus(ev engine.StepStatus) {
	// We treat a transition into a terminal/failed status as resolving every
	// wait for that step; the notification vocabulary does not distinguish
	// terminal outcomes below the run level.
	if ev.To != "awaiting_review" && ev.To != "awaiting_input" &&
		ev.To != "awaiting_recovery" && ev.To != "awaiting_integration" {
		l.mu.Lock()
		defer l.mu.Unlock()
		state, ok := l.runs[ev.RunID]
		if !ok {
			return
		}
		for key := range state.waits {
			if key.StepID == ev.StepID {
				delete(state.waits, key)
			}
		}
	}
}

func (l *Lifecycle) handleRunFinished(ev engine.RunFinished) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.runs[ev.RunID]
	if !ok {
		return
	}
	state.terminal = true
	if state.pendingTimer != nil {
		state.pendingTimer.Stop()
		state.pendingTimer = nil
	}
	if ev.Cause == engine.CauseCancelled {
		return
	}
	switch ev.Cause {
	case engine.CauseSucceeded:
		if slices.Contains(state.policy.Events, workflow.RunSucceeded) {
			l.emitTerminalLocked(state, workflow.RunSucceeded)
		}
	case engine.CauseFailed, engine.CauseTimeout, engine.CausePolicyRejection:
		if slices.Contains(state.policy.Events, workflow.RunFailed) {
			l.emitTerminalLocked(state, workflow.RunFailed)
		}
	default:
		// Older records without a semantic cause default to Failed-if-Failed
		// so historical journals still classify correctly.
		if ev.Failed && slices.Contains(state.policy.Events, workflow.RunFailed) {
			l.emitTerminalLocked(state, workflow.RunFailed)
		} else if !ev.Failed && slices.Contains(state.policy.Events, workflow.RunSucceeded) {
			l.emitTerminalLocked(state, workflow.RunSucceeded)
		}
	}
}

func (l *Lifecycle) flushAttention(runID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.runs[runID]
	if !ok {
		return
	}
	state.pendingTimer = nil
	if state.terminal {
		return
	}
	l.emitAttentionLocked(state, false)
}

// emitAttentionLocked builds one Notification per destination that selected
// AttentionRequired and enqueues them.
func (l *Lifecycle) emitAttentionLocked(state *runLifecycle, isRestored bool) {
	if len(state.waits) == 0 {
		return
	}
	descriptors := make([]AttentionDescriptor, 0, len(state.waits))
	for _, w := range state.waits {
		descriptors = append(descriptors, AttentionDescriptor{StepID: w.StepID, Kind: w.Kind})
	}

	destinations := destinationsForEvent(state.bindings, state.policy, workflow.AttentionRequired)
	if len(destinations) == 0 {
		return
	}

	n := Notification{
		ID:           l.idGen(),
		Event:        workflow.AttentionRequired,
		Timestamp:    l.clock(),
		Workflow:     state.workflow,
		RunID:        state.runID,
		Epoch:        state.epoch,
		Attention:    descriptors,
		AttentionN:   len(descriptors),
		Destinations: destinations,
		IsRestored:   isRestored,
	}
	l.dispatch.Enqueue(n)
}

func (l *Lifecycle) emitTerminalLocked(state *runLifecycle, event workflow.NotificationEvent) {
	destinations := destinationsForEvent(state.bindings, state.policy, event)
	if len(destinations) == 0 {
		return
	}
	n := Notification{
		ID:           l.idGen(),
		Event:        event,
		Timestamp:    l.clock(),
		Workflow:     state.workflow,
		RunID:        state.runID,
		Epoch:        state.epoch,
		Destinations: destinations,
	}
	l.dispatch.Enqueue(n)
}

// Filter implements WaitResolver over the same wait state, so the
// dispatcher can re-check before every send/retry.
func (l *Lifecycle) Filter(runID string, waits []WaitIdentity) []WaitIdentity {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.runs[runID]
	if !ok || len(state.waits) == 0 {
		return nil
	}
	filtered := waits[:0]
	for _, w := range waits {
		key := waitKey{StepID: w.StepID, Kind: w.Kind, Nonce: w.Nonce}
		if _, exists := state.waits[key]; exists {
			filtered = append(filtered, w)
			continue
		}
		// Fall back to any wait for the same step+kind: a coalesced summary
		// generalizes over nonces because retries reuse the earlier snapshot.
		match := false
		for existing := range state.waits {
			if existing.StepID == w.StepID && existing.Kind == w.Kind {
				match = true
				break
			}
		}
		if match {
			filtered = append(filtered, w)
		}
	}
	return filtered
}

// destinationsForEvent returns bindings that have selected the given event.
// The workflow.NotificationPolicy already encodes which destination alias
// selected which events; the operator binding just supplies the URL/type.
func destinationsForEvent(bindings []Binding, policy workflow.NotificationPolicy, event workflow.NotificationEvent) []Binding {
	if len(bindings) == 0 || len(policy.Routes) == 0 {
		return nil
	}
	// Build alias → allowed events (from policy routes).
	allowed := make(map[string]map[workflow.NotificationEvent]bool, len(policy.Routes))
	for _, r := range policy.Routes {
		if _, ok := allowed[r.Destination]; !ok {
			allowed[r.Destination] = make(map[workflow.NotificationEvent]bool)
		}
		for _, e := range r.Events {
			allowed[r.Destination][e] = true
		}
	}
	var out []Binding
	for _, b := range bindings {
		if allowed[b.Alias][event] {
			out = append(out, b)
		}
	}
	return out
}

// workflowPolicy extracts a resolved NotificationPolicy for a registered run.
// The engine.RunRegistration deliberately does not carry a notification field
// (the engine has no notification dependency); wire code installs a package-
// scoped lookup that maps run ID to its frozen resolved policy.
func workflowPolicy(reg engine.RunRegistration) (workflow.NotificationPolicy, bool) {
	if policyGetterForRun == nil {
		return workflow.NotificationPolicy{}, false
	}
	getter := policyGetterForRun(reg)
	if getter == nil {
		return workflow.NotificationPolicy{}, false
	}
	return getter(reg.RunID), true
}

// policyGetterForRun is a package-scoped hook the wire package populates so
// the Lifecycle can pull the resolved workflow policy without extending
// engine.RunRegistration with a notification-specific field.
var policyGetterForRun func(reg engine.RunRegistration) func(runID string) workflow.NotificationPolicy

// SetPolicyLookup installs a package-scoped getter so the observer can pull
// the resolved workflow policy for a run. Wire code calls this once at
// process startup. Passing nil clears the hook.
func SetPolicyLookup(f func(runID string) workflow.NotificationPolicy) {
	if f == nil {
		policyGetterForRun = nil
		return
	}
	policyGetterForRun = func(reg engine.RunRegistration) func(runID string) workflow.NotificationPolicy {
		return f
	}
}

func waitKindToAttention(k engine.WaitKind) AttentionKind {
	switch k {
	case engine.WaitReview:
		return AttentionReview
	case engine.WaitInput:
		return AttentionInput
	case engine.WaitPrompt:
		return AttentionPrompt
	case engine.WaitQuestion:
		return AttentionQuestion
	case engine.WaitRecovery:
		return AttentionRecovery
	case engine.WaitIntegrationConflict:
		return AttentionIntegrationConflict
	case engine.WaitFinalMerge:
		return AttentionFinalMerge
	default:
		return AttentionReview
	}
}

func defaultIDGen() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "id-" + hex.EncodeToString(b[:])
	}
	return "n_" + hex.EncodeToString(b[:])
}
