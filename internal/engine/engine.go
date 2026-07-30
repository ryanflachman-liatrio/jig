// Package engine drives workflow execution. One goroutine (the scheduler) owns
// all mutable state for a run; workers and the TUI communicate with it via its
// inbox channel. Every state transition is emitted as a typed Event, published
// to subscribers — the TUI is merely one.
package engine

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"jig/internal/step"
	"jig/internal/workflow"
)

// Manager is the registry of concurrent runs.
// mu guards the registry only — workflow state is owned by each scheduler.
type Manager struct {
	mu   sync.Mutex
	runs map[string]*Run
	exec Executor
	root string         // .jig/ root (used in Phase 2+ for file I/O)
	subs []chan<- Event // manager-level fan-out; TUI subscribes once
}

// NewManager returns a Manager backed by exec. root is the .jig/ directory;
// pass "" in tests or when file persistence is not yet wired (Phase 1).
func NewManager(exec Executor, root string) *Manager {
	return &Manager{
		runs: make(map[string]*Run),
		exec: exec,
		root: root,
	}
}

// RunSnapshot is a point-in-time summary of a run, safe to read from any goroutine.
type RunSnapshot struct {
	ID       string
	Workflow string
	Steps    []step.State
	Done     bool
	Failed   bool
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
	}

	m.mu.Lock()
	m.runs[runID] = run
	// Snapshot subscriber list at start — additions after Start don't join mid-run.
	subs := make([]chan<- Event, len(m.subs))
	copy(subs, m.subs)
	m.mu.Unlock()

	s := newScheduler(wf, runID, inbox, subs, m.exec)
	go s.run(ctx)
	return run, nil
}

// Subscribe returns a channel that receives every event from every run,
// tagged with RunID. The caller must drain it; slow readers drop events
// (the scheduler never blocks on fan-out).
func (m *Manager) Subscribe() <-chan Event {
	ch := make(chan Event, 256)
	m.mu.Lock()
	m.subs = append(m.subs, ch)
	m.mu.Unlock()
	return ch
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

// Run is the caller's handle to a live scheduler goroutine.
type Run struct {
	ID     string
	cancel context.CancelFunc
	inbox  chan schedMsg
}

// Cancel terminates the run. In-flight workers receive context cancellation.
func (r *Run) Cancel() { r.cancel() }

// Resolve delivers a human verdict for a review step (Phase 3+).
func (r *Run) Resolve(stepID, verdict string) {
	r.inbox <- verdictMsg{stepID: stepID, verdict: verdict}
}

// Snapshot requests a point-in-time view of the run's state. The reply goes
// through the scheduler's inbox so only one goroutine ever reads state.
func (r *Run) Snapshot() RunSnapshot {
	reply := make(chan RunSnapshot, 1)
	r.inbox <- snapshotReqMsg{reply: reply}
	return <-reply
}

func newRunID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + string(b)
}

// ── scheduler messages ────────────────────────────────────────────────────────

type schedMsg interface{ isSchedMsg() }

type stepDoneMsg struct {
	stepID string
	result *step.Result
	err    error
}

type verdictMsg struct {
	stepID  string
	verdict string
}

type snapshotReqMsg struct {
	reply chan<- RunSnapshot
}

func (stepDoneMsg) isSchedMsg()    {}
func (verdictMsg) isSchedMsg()     {}
func (snapshotReqMsg) isSchedMsg() {}

// ── reporter ─────────────────────────────────────────────────────────────────

// reporter routes live step signals through the scheduler's fan-out.
// It is created per-dispatch and passed to the executor; the executor
// may call it from its own goroutine, so fanOut must not touch scheduler state.
type reporter struct {
	subs []chan<- Event
	ev   func(Event) // pre-bound to emit tags (runID, stepID)
}

func (r *reporter) Output(delta string)          { r.ev(StepOutput{Delta: delta}) }
func (r *reporter) ToolCall(tool, detail string) { r.ev(StepToolCall{Tool: tool, Detail: detail}) }

// ── scheduler ────────────────────────────────────────────────────────────────

type scheduler struct {
	wf       *workflow.Workflow
	runID    string
	states   map[string]*step.State
	inbox    chan schedMsg
	subs     []chan<- Event
	exec     Executor
	inFlight int
	seq      int
}

func newScheduler(
	wf *workflow.Workflow,
	runID string,
	inbox chan schedMsg,
	subs []chan<- Event,
	exec Executor,
) *scheduler {
	states := make(map[string]*step.State, len(wf.Steps))
	for _, s := range wf.Steps {
		states[s.ID] = &step.State{ID: s.ID, Status: step.StatusPending}
	}
	return &scheduler{
		wf:     wf,
		runID:  runID,
		states: states,
		inbox:  inbox,
		subs:   subs,
		exec:   exec,
	}
}

func (s *scheduler) run(ctx context.Context) {
	ids := make([]string, len(s.wf.Steps))
	for i, st := range s.wf.Steps {
		ids[i] = st.ID
	}
	s.emit(RunStarted{RunID: s.runID, Workflow: s.wf.Meta.Name, Steps: ids})

	maxPar := s.wf.Defaults.MaxParallel
	if maxPar <= 0 {
		maxPar = 4
	}

	for {
		// 1. Dispatch every ready step, respecting max_parallel.
		for s.inFlight < maxPar {
			st, ok := s.nextReady()
			if !ok {
				break
			}
			s.dispatch(ctx, st)
		}

		// 2. Terminal check: nothing running, nothing pending and runnable.
		if s.inFlight == 0 && !s.anyPendingRunnable() {
			s.emit(RunFinished{RunID: s.runID, Failed: s.anyFailed()})
			return
		}

		// 3. Block for exactly one message, then loop to re-dispatch.
		select {
		case msg := <-s.inbox:
			s.handle(msg)
		case <-ctx.Done():
			s.emit(RunFinished{RunID: s.runID, Failed: true})
			return
		}
	}
}

// nextReady returns the first pending step whose every depends_on is succeeded.
// Phase 1: no when-guards; failure policy is always abort.
func (s *scheduler) nextReady() (*workflow.Step, bool) {
	for i := range s.wf.Steps {
		st := &s.wf.Steps[i]
		if s.states[st.ID].Status != step.StatusPending {
			continue
		}
		if s.depsSucceeded(st) {
			return st, true
		}
	}
	return nil, false
}

func (s *scheduler) depsSucceeded(st *workflow.Step) bool {
	for _, dep := range st.DependsOn {
		if s.states[dep].Status != step.StatusSucceeded {
			return false
		}
	}
	return true
}

// anyPendingRunnable reports whether any pending step could eventually become
// ready. Uses transitive failure propagation so A→B→C where A fails doesn't
// deadlock the scheduler.
func (s *scheduler) anyPendingRunnable() bool {
	// Seed the permanently-blocked set with terminal-failure statuses.
	blocked := make(map[string]bool, len(s.states))
	for id, st := range s.states {
		if st.Status == step.StatusFailed || st.Status == step.StatusSkipped {
			blocked[id] = true
		}
	}
	// Propagate through the DAG (terminates because there are no cycles).
	for changed := true; changed; {
		changed = false
		for i := range s.wf.Steps {
			st := &s.wf.Steps[i]
			if blocked[st.ID] {
				continue
			}
			for _, dep := range st.DependsOn {
				if blocked[dep] {
					blocked[st.ID] = true
					changed = true
					break
				}
			}
		}
	}
	for id, st := range s.states {
		if st.Status == step.StatusPending && !blocked[id] {
			return true
		}
	}
	return false
}

func (s *scheduler) anyFailed() bool {
	for _, st := range s.states {
		if st.Status == step.StatusFailed {
			return true
		}
	}
	return false
}

// dispatch launches a worker goroutine for one step. The worker sends a
// stepDoneMsg to the inbox when it finishes; only the scheduler reads state.
func (s *scheduler) dispatch(ctx context.Context, st *workflow.Step) {
	from := s.states[st.ID].Status
	s.transition(st.ID, from, step.StatusRunning)
	s.inFlight++

	runID, stepID := s.runID, st.ID
	subs := s.subs
	rep := &reporter{
		subs: subs,
		ev: func(e Event) {
			// Tag the event with run/step IDs and fan-out without holding any lock.
			switch e := e.(type) {
			case StepOutput:
				e.RunID, e.StepID = runID, stepID
				fanOut(subs, e)
			case StepToolCall:
				e.RunID, e.StepID = runID, stepID
				fanOut(subs, e)
			}
		},
	}
	req := StepRequest{RunID: runID, Step: st}

	go func() {
		result, err := s.exec.Execute(ctx, req, rep)
		s.inbox <- stepDoneMsg{stepID: stepID, result: result, err: err}
	}()
}

// handle processes one message from the scheduler's inbox.
// Phase 1: no retry/validate/loop — just succeed or fail.
func (s *scheduler) handle(msg schedMsg) {
	switch m := msg.(type) {
	case stepDoneMsg:
		s.inFlight--
		from := s.states[m.stepID].Status
		var to step.Status
		if m.err != nil || (m.result != nil && m.result.Status == step.StatusFailed) {
			to = step.StatusFailed
		} else {
			to = step.StatusSucceeded
		}
		if m.result != nil {
			s.states[m.stepID].Result = m.result
		}
		s.transition(m.stepID, from, to)

	case verdictMsg:
		// Phase 3: review step verdict delivery.

	case snapshotReqMsg:
		m.reply <- s.snapshot()
	}
}

func (s *scheduler) transition(stepID string, from, to step.Status) {
	state := s.states[stepID]
	state.Status = to
	s.emit(StepStatus{
		RunID:     s.runID,
		StepID:    stepID,
		From:      from,
		To:        to,
		Attempt:   state.Attempt,
		Iteration: state.Iteration,
	})
}

// emit writes an event to the journal (Phase 2+), then fans it out to subscribers.
// emit is called only from the scheduler goroutine.
func (s *scheduler) emit(e Event) {
	s.seq++
	// Phase 2: journal.Append(s.seq, e) goes here, before fan-out.
	fanOut(s.subs, e)
}

func (s *scheduler) snapshot() RunSnapshot {
	states := make([]step.State, len(s.wf.Steps))
	allDone := true
	for i, wfStep := range s.wf.Steps {
		st := s.states[wfStep.ID]
		states[i] = *st
		switch st.Status {
		case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
		default:
			allDone = false
		}
	}
	return RunSnapshot{
		ID:       s.runID,
		Workflow: s.wf.Meta.Name,
		Steps:    states,
		Done:     allDone,
		Failed:   s.anyFailed(),
	}
}

// fanOut sends e to each subscriber channel, dropping events for slow consumers.
// Called from both scheduler and reporter (which runs in worker goroutines).
func fanOut(subs []chan<- Event, e Event) {
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}
