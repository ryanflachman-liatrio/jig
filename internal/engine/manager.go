package engine

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// sub holds a subscriber's two event channels: live for high-volume droppable
// signals (StepOutput, StepToolCall, StepMessage) and ctrl for critical
// low-volume events that must not be lost (RunFinished, ReviewRequest, etc.).
// Separating them prevents a burst of live events from displacing ctrl events.
type sub struct {
	live chan Event // large buffer; drop-on-full is acceptable
	ctrl chan Event // sized for worst-case ctrl-event volume; rarely full
}

// Manager is the registry of concurrent runs.
// mu guards the registry only — workflow state is owned by each scheduler.
type Manager struct {
	mu        sync.Mutex
	runs      map[string]*Run
	exec      Executor
	resolver  IntegrationResolver
	store     datastore.Store // .jig/ root; disabled when persistence is off
	subs      []sub           // manager-level fan-out; TUI subscribes once
	monitors  []sentinel.MonitorDef
	secrets   SecretResolver
	observers observerRegistry
	epochMu   sync.Mutex
	epoch     int
}

// nextEpoch returns a monotonic epoch number stamped onto each Run
// registration. The value is only ever consumed by observers.
func (m *Manager) nextEpoch() int {
	m.epochMu.Lock()
	defer m.epochMu.Unlock()
	m.epoch++
	return m.epoch
}

// SecretResolver obtains a named secret at dispatch time. Workflow TOML only
// names references; implementations own where their values come from.
type SecretResolver func(name string) (string, error)

// NewManager returns a Manager backed by exec. root is the .jig/ directory;
// pass "" in tests or when file persistence is not yet wired (Phase 1).
//
// A non-empty root is resolved to an absolute path immediately: every
// worktree and execution-view path the scheduler builds is derived from it
// (see execution.go), and those paths are handed to the ACP harness as a
// session's cwd, which the protocol requires to be absolute. Callers may
// pass a relative root (e.g. the ".jig" default) without knowing this.
func NewManager(exec Executor, root string) *Manager {
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	return &Manager{
		runs:  make(map[string]*Run),
		exec:  exec,
		store: datastore.Open(root),
	}
}

// SetMonitors registers Tier-2 monitor definitions that will be dispatched
// out-of-band for every run started by this manager. Call before Start.
func (m *Manager) SetMonitors(monitors []sentinel.MonitorDef) {
	m.monitors = monitors
}

// SetIntegrationResolver installs the agent used by an operator-requested
// conflict proposal. Without one, integration conflicts remain manual-only.
func (m *Manager) SetIntegrationResolver(resolver IntegrationResolver) {
	m.resolver = resolver
}

// SetSecretResolver configures external secret resolution for subsequent runs.
// A step that names secrets fails before dispatch if no resolver is configured
// or a referenced value cannot be resolved.
func (m *Manager) SetSecretResolver(resolver SecretResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets = resolver
}

// Root returns the .jig/ directory configured for this manager, or "" when
// persistence is disabled.
func (m *Manager) Root() string { return m.store.Root() }

// Store returns the persistence store backing this manager. It is disabled
// when persistence is off.
func (m *Manager) Store() datastore.Store { return m.store }

// RunDir addresses the on-disk run for runID without creating or checking
// it. The result is disabled when persistence is off or runID is not a valid
// run id. Readers such as the TUI transcript view use it to locate a run's
// per-step transcript files.
func (m *Manager) RunDir(runID string) datastore.Run {
	run, err := m.store.Locate(runID)
	if err != nil {
		return datastore.Run{}
	}
	return run
}

// PersistedRuns lists the IDs of runs persisted on disk under .jig/runs/, oldest
// first. These are the runs a fresh session inherits — including ones from
// earlier sessions that have no in-memory Run handle — so the TUI can seed its
// run list at startup rather than only showing runs started this session. Pair
// each ID with RunDir and ReplayJournal to reconstruct that run's state.
//
// Returns nil when persistence is off (root == ""). The manager holds no state
// for these runs beyond the on-disk layout, so this reads the directory each
// call.
func (m *Manager) PersistedRuns() ([]string, error) {
	return m.store.List()
}

// Start validates wf and spawns a scheduler goroutine for it, returning the
// Run handle. The goroutine owns all of the run's mutable state.
func (m *Manager) Start(wf *workflow.Workflow) (*Run, error) {
	if wf == nil {
		return nil, fmt.Errorf("engine: nil workflow")
	}
	runID := newRunID()
	ctx, cancel := context.WithCancel(context.Background())

	inbox := make(chan schedMsg, 64)
	run := &Run{
		ID:     runID,
		cancel: cancel,
		inbox:  inbox,
		done:   make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[runID] = run
	// Snapshot subscriber list at start — additions after Start don't join mid-run.
	subs := make([]sub, len(m.subs))
	copy(subs, m.subs)
	secrets := m.secrets
	m.mu.Unlock()

	// Create the run directory and open a manifest.Writer when root is
	// configured. A missing or uncreatable root is non-fatal — runs still work,
	// they just don't persist. This avoids breaking existing tests that pass "".
	var w *manifest.Writer
	var runDir datastore.Run
	if m.store.Enabled() {
		if rd, err := m.store.Create(runID); err == nil {
			runDir = rd
			// Once the run directory exists, the snapshot and journal are
			// required: without them the run can neither be reopened nor
			// audited, so a failure aborts Start instead of running unrecorded.
			fail := func(err error) (*Run, error) {
				if run.runLock != nil {
					_ = run.runLock.Close()
				}
				m.mu.Lock()
				delete(m.runs, runID)
				m.mu.Unlock()
				cancel()
				return nil, err
			}
			lock, lockErr := AcquireRunLease(runDir)
			if lockErr != nil {
				return fail(fmt.Errorf("engine: lock run: %w", lockErr))
			}
			run.runLock = lock
			if err := persistWorkflowSnapshot(runDir, wf); err != nil {
				return fail(fmt.Errorf("engine: persist workflow snapshot: %w", err))
			}
			mw, err := manifest.NewWriter(runDir)
			if err != nil {
				return fail(fmt.Errorf("engine: open journal: %w", err))
			}
			w = mw
		}
	}

	repoRoot := ""
	if m.store.Enabled() {
		repoRoot = filepath.Dir(filepath.Clean(m.store.Root()))
	}
	security := startRunSecurity(ctx, wf, runID, runDir, m.monitors, subs, inbox, false)

	// onDone is called by the scheduler goroutine before it exits.
	// Writing finalSnap before closing done satisfies the memory-model
	// happens-before requirement so Snapshot() reads are lock-free.
	onDone := func(snap RunSnapshot) {
		run.finalSnap = snap
		security.stop()
		_ = run.runLock.Close()
		run.runLock = nil
		close(run.done)
	}
	s := newScheduler(schedulerDeps{wf: wf, runID: runID, inbox: inbox, subs: subs, exec: m.exec, cancel: cancel, writer: w, runDir: runDir, jigRoot: m.store.Root(), repoRoot: repoRoot, onDone: onDone})
	if security != nil {
		s.securitySignals = security.signals
	}
	s.resolver = m.resolver
	s.secretResolver = secrets
	s.getCauseHint = run.causeSnapshot
	s.observers = &m.observers

	// Register the run before any live event is emitted. Observers that
	// depend on immutable per-run metadata (workflow name, resolved policy)
	// install their per-run state here, so they never see a Publish for an
	// unregistered run.
	m.observers.dispatchRegistered(RunRegistration{
		RunID:    runID,
		Workflow: wf.Meta.Name,
		Epoch:    m.nextEpoch(),
		Reopen:   false,
		Snapshot: run.Snapshot,
		Policy:   wf.NotificationPolicy(),
	})

	go func() {
		defer m.observers.dispatchStopped(runID)
		s.run(ctx)
	}()

	return run, nil
}

// Subscribe implements the Observer pattern: Manager is the subject, each
// sub registered here is an observer, and Event is the notification. A
// subscriber never polls scheduler state — it is pushed events as they
// happen via fanOutLive/fanOutCtrl below.
//
// It returns two channels for this subscriber.
//
// live carries high-volume liveness signals (StepOutput, StepToolCall,
// StepMessage). These are drop-on-full: a missed event only means the UI is
// one sequence stale, corrected on the next read from disk.
//
// ctrl carries all other events (RunStarted, RunFinished, StepStatus,
// ReviewRequest, etc.). These are critical and sized so they cannot fill under
// normal workflow volumes.
//
// Both channels must be drained by the caller; neither is ever closed.
func (m *Manager) Subscribe() (live, ctrl <-chan Event) {
	s := sub{
		live: make(chan Event, 1024),
		ctrl: make(chan Event, 256),
	}
	m.mu.Lock()
	m.subs = append(m.subs, s)
	m.mu.Unlock()
	return s.live, s.ctrl
}

// Runs returns the live Run handles. Callers that need step states should
// call Run.Snapshot(), which routes through the scheduler's inbox.
func (m *Manager) Runs() []*Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Run, 0, len(m.runs))
	for _, r := range m.runs {
		out = append(out, r)
	}
	return out
}

func newRunID() string {
	// The suffix keeps run directories distinct within one second. rand.Text
	// draws from crypto/rand; its base32 alphabet is lowered to keep ids
	// lowercase alphanumeric.
	b := strings.ToLower(rand.Text())[:8]
	return time.Now().UTC().Format("20060102-150405") + "-" + b
}
