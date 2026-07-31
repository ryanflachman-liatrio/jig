// Package engine drives workflow execution. One goroutine (the scheduler) owns
// all mutable state for a run; workers and the TUI communicate with it via its
// inbox channel. Every state transition is emitted as a typed Event, published
// to subscribers — the TUI is merely one.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"jig/internal/datastore"
	"jig/internal/manifest"
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

	// Create the run directory and open a manifest.Writer when root is
	// configured. A missing or uncreatable root is non-fatal — runs still work,
	// they just don't persist. This avoids breaking existing tests that pass "".
	var w *manifest.Writer
	var runDir string
	if m.root != "" {
		if rd, err := datastore.RunDir(m.root, runID); err == nil {
			runDir = rd
			if mw, err := manifest.NewWriter(runDir); err == nil {
				w = mw
			}
		}
	}

	s := newScheduler(wf, runID, inbox, subs, m.exec, cancel, w, runDir)
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
	wf           *workflow.Workflow
	runID        string
	states       map[string]*step.State
	inbox        chan schedMsg
	subs         []chan<- Event
	exec         Executor
	cancel       context.CancelFunc        // cancels the run context; used by abort policy
	writer       *manifest.Writer          // nil when persistence is disabled (root = "")
	runDir       string                    // .jig/runs/<runID>/; "" when persistence is disabled
	structured   map[string]map[string]any // cached JSON decode of step Result.Structured
	stepFeedback map[string]string         // gotoStepID → feedback step ID for loop replay
	aborted      bool                      // true when the run was explicitly aborted (loop cap, etc.)
	inFlight     int
	seq          int
}

func newScheduler(
	wf *workflow.Workflow,
	runID string,
	inbox chan schedMsg,
	subs []chan<- Event,
	exec Executor,
	cancel context.CancelFunc,
	writer *manifest.Writer,
	runDir string,
) *scheduler {
	states := make(map[string]*step.State, len(wf.Steps))
	for _, s := range wf.Steps {
		states[s.ID] = &step.State{ID: s.ID, Status: step.StatusPending}
	}
	return &scheduler{
		wf:           wf,
		runID:        runID,
		states:       states,
		inbox:        inbox,
		subs:         subs,
		exec:         exec,
		cancel:       cancel,
		writer:       writer,
		runDir:       runDir,
		structured:   make(map[string]map[string]any),
		stepFeedback: make(map[string]string),
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

	defer func() {
		// Close the journal file after the final RunFinished event has been
		// appended. Deferring ensures the file is closed even when the run exits
		// via ctx.Done().
		if s.writer != nil {
			_ = s.writer.Close()
		}
	}()

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

// nextReady returns the first pending step whose every depends_on is satisfied
// and whose when guard (if any) is true. Guard false → step is immediately
// skipped with a cascade; review steps are handled inline and also not returned
// (they park on human input via dispatchReview). Both cases continue scanning
// for the next dispatchable step.
func (s *scheduler) nextReady() (*workflow.Step, bool) {
	for i := range s.wf.Steps {
		st := &s.wf.Steps[i]
		state := s.states[st.ID]
		if state.Status != step.StatusPending {
			continue
		}
		if !s.depsReady(st) {
			continue
		}

		// Evaluate the when guard. The condition was validated at load time so
		// ParseCondition will not return an error here.
		if st.When != "" {
			cond, _ := workflow.ParseCondition(st.When)
			if !s.evalGuard(cond) {
				s.transition(st.ID, state.Status, step.StatusSkipped)
				s.cascadeSkip(st.ID)
				continue
			}
		}

		// Review steps never go to a worker: park them on human input and keep
		// scanning for other runnable steps this iteration.
		if st.Type == workflow.StepReview {
			s.dispatchReview(st)
			continue
		}

		return st, true
	}
	return nil, false
}

// depsReady reports whether all of st's dependencies have reached a state
// that allows st to proceed.  A dependency is ready when:
//   - its status is succeeded, or
//   - its status is failed and its on_failure policy is "continue" (the dep
//     explicitly opts in to letting dependents run despite its own failure).
//
// All other statuses (pending, running, validating, awaiting_review, skipped)
// are not ready — the step must wait.
func (s *scheduler) depsReady(st *workflow.Step) bool {
	for _, depID := range st.DependsOn {
		depState := s.states[depID]
		if depState.Status == step.StatusSucceeded {
			continue
		}
		// A failed dep is only "ready" when it declared on_failure = "continue".
		if depState.Status == step.StatusFailed {
			depStep := s.stepByID(depID)
			if depStep != nil && depStep.OnFailure == workflow.FailContinue {
				continue
			}
		}
		return false
	}
	return true
}

// stepByID returns a pointer into wf.Steps for the given ID, or nil.
// O(n) but n is small (workflow steps) and called infrequently.
func (s *scheduler) stepByID(id string) *workflow.Step {
	for i := range s.wf.Steps {
		if s.wf.Steps[i].ID == id {
			return &s.wf.Steps[i]
		}
	}
	return nil
}

// anyPendingRunnable reports whether any pending step could eventually become
// ready. Uses transitive failure propagation so A→B→C where A fails (abort
// policy) doesn't deadlock the scheduler.
//
// A failed step with on_failure = "continue" does NOT block its dependents —
// it is treated as a transparent node for the purposes of reachability.
func (s *scheduler) anyPendingRunnable() bool {
	// Seed the permanently-blocked set with abort-failed and skipped steps.
	// A step that failed with "continue" is NOT in this set — its failure is
	// known but it doesn't block dependents.
	blocked := make(map[string]bool, len(s.states))
	for id, st := range s.states {
		if st.Status == step.StatusSkipped {
			blocked[id] = true
		}
		if st.Status == step.StatusFailed {
			wfStep := s.stepByID(id)
			if wfStep == nil || wfStep.OnFailure != workflow.FailContinue {
				blocked[id] = true
			}
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
				// Only propagate from hard-blocked nodes, not from continue-failed ones.
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
		// A step parked on human input is still "runnable" — it will unblock when
		// the user delivers a verdict. Don't let the run terminate early.
		if st.Status == step.StatusAwaitingReview {
			return true
		}
	}
	return false
}

func (s *scheduler) anyFailed() bool {
	if s.aborted {
		return true
	}
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
	var artifactDir string
	if s.runDir != "" {
		artifactDir = filepath.Join(s.runDir, "artifacts")
	}
	req := StepRequest{
		RunID:       runID,
		Step:        st,
		Feedback:    s.stepFeedback[st.ID],
		ArtifactDir: artifactDir,
	}

	go func() {
		result, err := s.exec.Execute(ctx, req, rep)
		s.inbox <- stepDoneMsg{stepID: stepID, result: result, err: err}
	}()
}

// handle processes one message from the scheduler's inbox.
func (s *scheduler) handle(msg schedMsg) {
	switch m := msg.(type) {
	case stepDoneMsg:
		s.inFlight--
		// Determine whether the raw execution succeeded or failed.
		execFailed := m.err != nil || (m.result != nil && m.result.Status == step.StatusFailed)
		if m.result != nil {
			s.states[m.stepID].Result = m.result
		}

		wfStep := s.stepByID(m.stepID)

		if !execFailed && wfStep != nil && wfStep.Validate != nil {
			// Step executed successfully but has a [step.validate] gate.
			// Run the gate synchronously in the scheduler goroutine (file checks)
			// or inline for command gates.  This keeps all state mutation in one
			// goroutine — the "single writer" invariant — at the cost of blocking
			// the scheduler loop briefly.  Gate commands are expected to be fast
			// (e.g. file existence checks, grep, quick test); slow gates belong
			// in a proper step of type "command".
			from := s.states[m.stepID].Status
			s.transition(m.stepID, from, step.StatusValidating)

			passed, detail := s.runGate(wfStep)
			s.emit(GateResult{
				RunID:  s.runID,
				StepID: m.stepID,
				Passed: passed,
				Detail: detail,
			})

			if !passed {
				execFailed = true
			}
		}

		if execFailed {
			s.applyFailurePolicy(m.stepID, wfStep)
		} else {
			// Use the current status as "from" — it may be StatusValidating if a
			// gate ran, or StatusRunning if there was no gate.
			curFrom := s.states[m.stepID].Status
			s.transition(m.stepID, curFrom, step.StatusSucceeded)
			if wfStep != nil && wfStep.Loop != nil {
				s.fireLoop(m.stepID, wfStep)
			}
		}

	case verdictMsg:
		// Deliver the human's verdict to an awaiting_review step.
		state := s.states[m.stepID]
		if state.Status != step.StatusAwaitingReview {
			return // stale or duplicate verdict
		}
		state.Result = &step.Result{Status: step.StatusSucceeded, Verdict: m.verdict}
		wfStep := s.stepByID(m.stepID)
		s.transition(m.stepID, step.StatusAwaitingReview, step.StatusSucceeded)
		if wfStep != nil && wfStep.Loop != nil {
			s.fireLoop(m.stepID, wfStep)
		}

	case snapshotReqMsg:
		m.reply <- s.snapshot()
	}
}

// applyFailurePolicy marks the step failed (or retries it) according to its
// on_failure policy.  Called from handle(stepDoneMsg) after exec or gate failure.
func (s *scheduler) applyFailurePolicy(stepID string, wfStep *workflow.Step) {
	policy := workflow.FailAbort // default
	if wfStep != nil && wfStep.OnFailure != "" {
		policy = wfStep.OnFailure
	}

	state := s.states[stepID]
	from := state.Status

	switch policy {
	case workflow.FailRetry:
		maxRetries := 1
		if wfStep != nil && wfStep.MaxRetries > 0 {
			maxRetries = wfStep.MaxRetries
		}
		if state.Attempt < maxRetries {
			state.Attempt++
			// Reset to pending so the main scheduler loop picks it up again and
			// calls dispatch(), which will increment inFlight.  handle() already
			// decremented inFlight at its top, so the count stays correct.
			s.transition(stepID, from, step.StatusPending)
			return
		}
		// Exhausted retries — treat as abort so the run fails.
		s.transition(stepID, from, step.StatusFailed)
		s.cancel()

	case workflow.FailContinue:
		// Mark failed but don't cancel; dependents with depsReady() will treat
		// this step as a "satisfied" node and proceed.
		s.transition(stepID, from, step.StatusFailed)

	default: // FailAbort or unrecognised
		s.transition(stepID, from, step.StatusFailed)
		s.cancel()
	}
}

// runGate evaluates the [step.validate] block for wfStep synchronously.
// Returns (true, detail) on pass, (false, detail) on failure.
// Only OutputExists and OutputContains are implemented in Phase 2; Command
// gates delegate to the shell; OutputSchema is deferred to Phase 4.
func (s *scheduler) runGate(wfStep *workflow.Step) (bool, string) {
	v := wfStep.Validate

	// Command gate: run via sh -c, check exit code 0.
	if v.Command != "" {
		cmd := exec.Command("sh", "-c", v.Command)
		out, err := cmd.CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		if err != nil {
			return false, fmt.Sprintf("gate command failed: %v — %s", err, outStr)
		}
		return true, "gate command passed"
	}

	// OutputExists gate: verify the step's output file was written.
	if v.OutputExists {
		outputPath := wfStep.Output
		if outputPath == "" {
			return false, "output_exists gate: step has no output field"
		}
		if _, err := os.Stat(outputPath); err != nil {
			return false, fmt.Sprintf("output_exists gate: %v", err)
		}
		// Fall through to also check OutputContains if set.
	}

	// OutputContains gate: check that the output file contains a substring.
	if v.OutputContains != "" {
		outputPath := wfStep.Output
		if outputPath == "" {
			return false, "output_contains gate: step has no output field"
		}
		data, err := os.ReadFile(outputPath)
		if err != nil {
			return false, fmt.Sprintf("output_contains gate: read file: %v", err)
		}
		if !strings.Contains(string(data), v.OutputContains) {
			return false, fmt.Sprintf("output_contains gate: %q not found in output", v.OutputContains)
		}
	}

	return true, "gate passed"
}

// evalGuard evaluates a when-guard condition against the current step results.
// The condition was syntactically and semantically validated at load time, so
// missing steps or bad field paths return false (treat as "not yet ready").
func (s *scheduler) evalGuard(cond *workflow.Condition) bool {
	depState := s.states[cond.Step]
	if depState == nil || depState.Result == nil {
		return false
	}

	var val string
	if len(cond.Field) == 0 {
		// Scalar verdict (output_type bool or enum).
		val = depState.Result.Verdict
	} else {
		// Field path into structured JSON output. Decode once and cache.
		m, ok := s.structured[cond.Step]
		if !ok && len(depState.Result.Structured) > 0 {
			if err := json.Unmarshal(depState.Result.Structured, &m); err == nil {
				s.structured[cond.Step] = m
			}
		}
		var cur any = m
		for _, seg := range cond.Field {
			obj, ok := cur.(map[string]any)
			if !ok {
				return false
			}
			cur, ok = obj[seg]
			if !ok {
				return false
			}
		}
		switch v := cur.(type) {
		case string:
			val = v
		case bool:
			if v {
				val = "true"
			} else {
				val = "false"
			}
		default:
			val = fmt.Sprintf("%v", cur)
		}
	}

	switch cond.Op {
	case workflow.CondTruthy:
		return val == "true"
	case workflow.CondEq:
		return val == cond.Value
	case workflow.CondNeq:
		return val != cond.Value
	}
	return false
}

// cascadeSkip skips every pending step that has a transitive dependency on
// skipID (and whose dep chain doesn't pass through a on_failure=continue step).
// A skipped dep means the dependent's @ref inputs can never resolve.
func (s *scheduler) cascadeSkip(skipID string) {
	toSkip := map[string]bool{skipID: true}
	for changed := true; changed; {
		changed = false
		for i := range s.wf.Steps {
			st := &s.wf.Steps[i]
			if s.states[st.ID].Status != step.StatusPending || toSkip[st.ID] {
				continue
			}
			for _, dep := range st.DependsOn {
				if !toSkip[dep] {
					continue
				}
				// A skipped dep cascades unless the dep opted into "continue".
				depStep := s.stepByID(dep)
				if depStep == nil || depStep.OnFailure != workflow.FailContinue {
					toSkip[st.ID] = true
					changed = true
					break
				}
			}
		}
	}
	for id := range toSkip {
		if id == skipID {
			continue // already transitioned by nextReady
		}
		from := s.states[id].Status
		s.transition(id, from, step.StatusSkipped)
	}
}

// dispatchReview handles a review step inline: it never goes to a worker.
// The step is parked at awaiting_review and a ReviewRequest is emitted so the
// TUI can render choices and collect a human verdict via Run.Resolve.
func (s *scheduler) dispatchReview(st *workflow.Step) {
	from := s.states[st.ID].Status
	s.transition(st.ID, from, step.StatusAwaitingReview)
	s.emit(ReviewRequest{
		RunID:   s.runID,
		StepID:  st.ID,
		Choices: reviewChoices(st),
	})
}

// reviewChoices returns the ordered set of verdicts the human can select.
func reviewChoices(st *workflow.Step) []string {
	switch st.OutputType.Kind {
	case workflow.OutputBool:
		return []string{"true", "false"}
	case workflow.OutputEnum:
		return append([]string(nil), st.OutputType.Enum...)
	default:
		return []string{"approve"}
	}
}

// fireLoop checks the step's [step.loop] condition and, if it fires, resets the
// loop body (from Goto to this step) to pending for another iteration.
// When the cap is exceeded while the condition is still true, the run is aborted.
func (s *scheduler) fireLoop(stepID string, wfStep *workflow.Step) {
	loop := wfStep.Loop
	state := s.states[stepID]

	// Evaluate the loop's when guard (validated at load, won't error).
	if loop.When != "" {
		cond, _ := workflow.ParseCondition(loop.When)
		if !s.evalGuard(cond) {
			return // condition false: loop does not fire
		}
	}

	if state.Iteration >= loop.MaxIterations {
		// Exceeded the cap — abort the run per spec.
		s.aborted = true
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q exceeded max_iterations %d", stepID, loop.MaxIterations),
		})
		s.cancel()
		return
	}

	newIter := state.Iteration + 1
	s.emit(LoopFired{
		RunID:     s.runID,
		StepID:    stepID,
		Goto:      loop.Goto,
		Iteration: newIter,
		Max:       loop.MaxIterations,
	})

	// Wire feedback: the goto step's next dispatch gets the feedback step ID so
	// the executor can include it as an extra input.
	if loop.Feedback != "" {
		feedbackID := strings.TrimPrefix(loop.Feedback, "@")
		s.stepFeedback[loop.Goto] = feedbackID
	}

	// Reset every step in the loop body to pending with the new iteration count.
	for _, id := range s.loopBody(loop.Goto, stepID) {
		bodyState := s.states[id]
		bodyState.Iteration = newIter
		s.transition(id, bodyState.Status, step.StatusPending)
	}
}

// loopBody returns the IDs of all steps on any path from gotoID to loopID
// (inclusive) in the DAG. These are the steps to reset when the loop fires.
func (s *scheduler) loopBody(gotoID, loopID string) []string {
	// Forward set: all steps reachable from gotoID by following dependents.
	fwd := map[string]bool{gotoID: true}
	for changed := true; changed; {
		changed = false
		for i := range s.wf.Steps {
			st := &s.wf.Steps[i]
			if fwd[st.ID] {
				continue
			}
			for _, dep := range st.DependsOn {
				if fwd[dep] {
					fwd[st.ID] = true
					changed = true
					break
				}
			}
		}
	}
	// Backward set: all steps from which loopID is reachable (ancestors).
	bwd := map[string]bool{loopID: true}
	for changed := true; changed; {
		changed = false
		for i := range s.wf.Steps {
			st := &s.wf.Steps[i]
			if !bwd[st.ID] {
				continue
			}
			for _, dep := range st.DependsOn {
				if !bwd[dep] {
					bwd[dep] = true
					changed = true
				}
			}
		}
	}
	// Intersection = the loop body.
	var body []string
	for id := range fwd {
		if bwd[id] {
			body = append(body, id)
		}
	}
	return body
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

// emit writes an event to the journal (if a writer is configured), then fans
// it out to subscribers.  The journal write is synchronous and happens before
// any subscriber receives the event, preserving the "journal before fan-out"
// invariant: in-memory state is always fold(journal).
// emit is called only from the scheduler goroutine.
func (s *scheduler) emit(e Event) {
	s.seq++
	if s.writer != nil {
		line, err := MarshalEnvelope(s.seq, e)
		if err == nil {
			var term *manifest.StepTerminal
			if ss, ok := e.(StepStatus); ok {
				switch ss.To {
				case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
					state := s.states[ss.StepID]
					term = &manifest.StepTerminal{
						StepID:  ss.StepID,
						Status:  string(ss.To),
						Attempt: state.Attempt,
					}
				}
			}
			s.writer.AppendLine(line, term)
		}
	}
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
