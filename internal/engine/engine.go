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

// defaultReviewMaxMessages is the message-round-trip cap for review gates when
// max_messages is omitted from the workflow. Generous to support real workflows
// while preserving the static termination guarantee.
const defaultReviewMaxMessages = 10

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
	mu   sync.Mutex
	runs map[string]*Run
	exec Executor
	root string // .jig/ root (used in Phase 2+ for file I/O)
	subs []sub  // manager-level fan-out; TUI subscribes once
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

// RunDir returns the on-disk directory for runID without creating it, or "" when
// persistence is disabled (root == ""). Readers such as the TUI transcript view
// use it to locate a run's per-step transcript files; the path mirrors the
// layout datastore.RunDir builds at run start.
func (m *Manager) RunDir(runID string) string {
	if m.root == "" {
		return ""
	}
	return filepath.Join(m.root, "runs", runID)
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
		done:   make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[runID] = run
	// Snapshot subscriber list at start — additions after Start don't join mid-run.
	subs := make([]sub, len(m.subs))
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

	repoRoot := ""
	if m.root != "" {
		repoRoot = filepath.Dir(filepath.Clean(m.root))
	}
	// onDone is called by the scheduler goroutine before it exits.
	// Writing finalSnap before closing done satisfies the memory-model
	// happens-before requirement so Snapshot() reads are lock-free.
	onDone := func(snap RunSnapshot) {
		run.finalSnap = snap
		close(run.done)
	}
	s := newScheduler(wf, runID, inbox, subs, m.exec, cancel, w, runDir, m.root, repoRoot, onDone)
	go s.run(ctx)
	return run, nil
}

// Subscribe returns two channels for this subscriber.
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

// Run is the caller's handle to a live scheduler goroutine.
type Run struct {
	ID     string
	cancel context.CancelFunc
	inbox  chan schedMsg
	// done is closed by the scheduler goroutine before it exits.
	// finalSnap is written before done is closed; reads after observing
	// done closed see the written value (Go memory model, channel close).
	done      chan struct{}
	finalSnap RunSnapshot
}

// Cancel terminates the run. In-flight workers receive context cancellation.
func (r *Run) Cancel() { r.cancel() }

// Resolve delivers a human verdict for a review step (Phase 3+).
func (r *Run) Resolve(stepID, verdict string) {
	r.inbox <- verdictMsg{stepID: stepID, verdict: verdict}
}

// ProvideUserInput delivers text collected from the user for a from="user" input.
func (r *Run) ProvideUserInput(stepID, as, text string) {
	r.inbox <- userInputMsg{stepID: stepID, as: as, text: text}
}

// Message delivers a free-text human message to an awaiting_review gate, which
// routes it to the reviewed agent step for continued processing. The agent
// resumes its SDK session, re-runs, and the gate re-fires when done.
func (r *Run) Message(reviewStepID, text string) {
	r.inbox <- humanMessageMsg{stepID: reviewStepID, text: text}
}

// SendInput delivers a human response to an agent step that is blocked by
// block_on. The agent resumes its session with the response as the query.
func (r *Run) SendInput(stepID, text string) {
	r.inbox <- agentInputMsg{stepID: stepID, text: text}
}

// AnswerQuestion delivers the user's response to an in-flight AskUserQuestion call.
func (r *Run) AnswerQuestion(stepID, toolUseID, answer string) {
	r.inbox <- agentQuestionAnswerMsg{stepID: stepID, toolUseID: toolUseID, answer: answer}
}

// Recover delivers a human recovery decision for a step parked in
// step.StatusAwaitingRecovery after an unrecoverable failure. action is one of:
//
//   - RecoverRetry  — re-run the step fresh (a new agent session / full prompt).
//   - RecoverResume — re-run the failed agent session, feeding the captured
//     error plus the optional text as guidance so it doesn't repeat the mistake.
//   - RecoverAbort  — fail the step and abort the run (the old default behaviour).
//
// text is optional guidance, used only by RecoverResume.
func (r *Run) Recover(stepID, action, text string) {
	r.inbox <- recoverMsg{stepID: stepID, action: action, text: text}
}

// Snapshot returns a point-in-time view of the run's state. For a live run
// the request routes through the scheduler's inbox (single-writer invariant).
// For a completed run it returns the cached final snapshot immediately,
// avoiding a deadlock on the no-longer-running scheduler goroutine.
func (r *Run) Snapshot() RunSnapshot {
	// Fast path: scheduler already exited and finalSnap is ready.
	select {
	case <-r.done:
		return r.finalSnap
	default:
	}
	// Slow path: ask the live scheduler. Also select on done in case the
	// scheduler exits between the fast-path check and the inbox send.
	reply := make(chan RunSnapshot, 1)
	select {
	case r.inbox <- snapshotReqMsg{reply: reply}:
		select {
		case snap := <-reply:
			return snap
		case <-r.done:
			return r.finalSnap
		}
	case <-r.done:
		return r.finalSnap
	}
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

type userInputMsg struct {
	stepID string
	as     string
	text   string
}

type snapshotReqMsg struct {
	reply chan<- RunSnapshot
}

type humanMessageMsg struct {
	stepID string // review step ID receiving the message
	text   string
}

type agentInputMsg struct {
	stepID string // agent step blocked by block_on
	text   string
}

type agentQuestionNotifyMsg struct {
	stepID string
}

type agentQuestionAnswerMsg struct {
	stepID    string
	toolUseID string
	answer    string
}

// Recovery actions delivered via Run.Recover for a step parked in
// step.StatusAwaitingRecovery.
const (
	RecoverRetry  = "retry"  // re-run fresh (new session, full prompt)
	RecoverResume = "resume" // resume the failed agent session with the error + guidance
	RecoverAbort  = "abort"  // fail the step and abort the run
)

// maxRecoverRounds bounds how many times a single step may be retried/resumed
// through the recovery gate, preserving jig's static termination guarantee even
// though the gate is human-driven. The human can always abort instead.
const maxRecoverRounds = 20

type recoverMsg struct {
	stepID string
	action string // RecoverRetry | RecoverResume | RecoverAbort
	text   string // optional guidance, used by RecoverResume
}

func (stepDoneMsg) isSchedMsg()            {}
func (verdictMsg) isSchedMsg()             {}
func (userInputMsg) isSchedMsg()           {}
func (snapshotReqMsg) isSchedMsg()         {}
func (humanMessageMsg) isSchedMsg()        {}
func (agentInputMsg) isSchedMsg()          {}
func (agentQuestionNotifyMsg) isSchedMsg() {}
func (agentQuestionAnswerMsg) isSchedMsg() {}
func (recoverMsg) isSchedMsg()             {}

// ── reporter ─────────────────────────────────────────────────────────────────

// reporter routes live step signals through the scheduler's fan-out.
// It is created per-dispatch and passed to the executor; the executor
// may call it from its own goroutine, so fanOutLive must not touch scheduler state.
type reporter struct {
	subs     []sub
	ev       func(Event)     // pre-bound to emit tags (runID, stepID)
	inbox    chan<- schedMsg // scheduler inbox; used to deliver agentQuestionNotifyMsg
	answerCh chan string     // nil until Question is called; receives the human's answer
}

func (r *reporter) Output(delta string)          { r.ev(StepOutput{Delta: delta}) }
func (r *reporter) ToolCall(tool, detail string) { r.ev(StepToolCall{Tool: tool, Detail: detail}) }
func (r *reporter) Message(seq, iteration int) {
	r.ev(StepMessage{Seq: seq, Iteration: iteration})
}

// Question delivers an AskUserQuestion from the running agent to the scheduler,
// transitions the step to StatusNeedsInput, and blocks until the human answers
// via Run.AnswerQuestion. Runs in the executor goroutine.
func (r *reporter) Question(toolUseID string, questions []AgentQuestionItem) string {
	r.answerCh = make(chan string, 1)
	r.ev(AgentQuestion{ToolUseID: toolUseID, Questions: questions})
	return <-r.answerCh
}

// ── scheduler ────────────────────────────────────────────────────────────────

type scheduler struct {
	wf           *workflow.Workflow
	runID        string
	states       map[string]*step.State
	inbox        chan schedMsg
	subs         []sub
	exec         Executor
	cancel       context.CancelFunc        // cancels the run context; used by abort policy
	writer       *manifest.Writer          // nil when persistence is disabled (root = "")
	runDir       string                    // .jig/runs/<runID>/; "" when persistence is disabled
	structured   map[string]map[string]any // cached JSON decode of step Result.Structured
	stepFeedback map[string]string         // gotoStepID → feedback step ID for loop replay
	// rerunSource maps a loop's goto target → the firing source step id
	// (last-fired wins per re-run; only one loop fires per re-run event). It lets
	// buildStepContext name why a step is re-running. In-memory only, never
	// persisted — it mirrors stepFeedback.
	rerunSource map[string]string
	aborted     bool // true when the run was explicitly aborted (loop cap, etc.)
	inFlight    int
	seq         int

	// Phase 5: worktree lifecycle.
	jigRoot    string            // .jig/ root; "" when persistence is disabled
	repoRoot   string            // git repo root (parent of jigRoot)
	worktrees  map[string]string // stepID → active worktree absolute path
	wtBaseSHAs map[string]string // stepID → HEAD SHA captured at worktree creation
	diffs      map[string]string // stepID → latest diff text (updated each execution)

	// from="user" input collection: inputs are gathered one at a time before
	// the step is dispatched. preResolvedInputs guards against re-intercepting
	// after all inputs are collected and the step resets to StatusPending.
	pendingUserInputs   map[string][]workflow.Input // stepID → remaining prompts
	collectedUserInputs map[string][]ResolvedInput  // stepID → answers so far
	preResolvedInputs   map[string][]ResolvedInput  // stepID → fully collected, ready to inject

	// review-gate messaging: a human can send free-text to the reviewed agent
	// step, which resumes the agent's SDK session and re-runs it before a
	// terminal verdict is chosen. The cap (max_messages) bounds the round-trips.
	resumeSessions map[string]string // stepID → SDK session ID for next dispatch
	stepMessage    map[string]string // stepID → human message for resumed query
	reviewMessages map[string]int    // reviewStepID → messages sent so far

	// block_on: tracks how many times a human has provided input to a blocked step.
	stepInputCount map[string]int // stepID → input rounds delivered so far

	// recovery gate: tracks retry/resume rounds per step so the human-driven
	// recovery loop stays bounded (maxRecoverRounds).
	recoverCount map[string]int // stepID → recovery rounds taken so far

	// reporters holds the active reporter for each in-flight step so
	// agentQuestionAnswerMsg can route the answer to the correct channel.
	reporters map[string]*reporter

	postExecChain []postExecHandler // post-execution handler chain

	onDone func(RunSnapshot) // called once before the scheduler goroutine exits
}

func newScheduler(
	wf *workflow.Workflow,
	runID string,
	inbox chan schedMsg,
	subs []sub,
	exec Executor,
	cancel context.CancelFunc,
	writer *manifest.Writer,
	runDir string,
	jigRoot string,
	repoRoot string,
	onDone func(RunSnapshot),
) *scheduler {
	states := make(map[string]*step.State, len(wf.Steps))
	for _, s := range wf.Steps {
		states[s.ID] = &step.State{ID: s.ID, Status: step.StatusPending}
	}
	return &scheduler{
		wf:                  wf,
		runID:               runID,
		states:              states,
		inbox:               inbox,
		subs:                subs,
		exec:                exec,
		cancel:              cancel,
		writer:              writer,
		runDir:              runDir,
		structured:          make(map[string]map[string]any),
		stepFeedback:        make(map[string]string),
		rerunSource:         make(map[string]string),
		jigRoot:             jigRoot,
		repoRoot:            repoRoot,
		worktrees:           make(map[string]string),
		wtBaseSHAs:          make(map[string]string),
		diffs:               make(map[string]string),
		pendingUserInputs:   make(map[string][]workflow.Input),
		collectedUserInputs: make(map[string][]ResolvedInput),
		preResolvedInputs:   make(map[string][]ResolvedInput),
		resumeSessions:      make(map[string]string),
		stepMessage:         make(map[string]string),
		reviewMessages:      make(map[string]int),
		stepInputCount:      make(map[string]int),
		recoverCount:        make(map[string]int),
		reporters:           make(map[string]*reporter),
		postExecChain: []postExecHandler{
			phCaptureWorktreeDiff,
			phRunValidateGate,
			phCheckBlockOn,
		},
		onDone: onDone,
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
		// Signal completion first so Snapshot() callers unblock immediately.
		// finalSnap is written before done is closed, satisfying the memory-model
		// happens-before requirement for lock-free reads in Run.Snapshot().
		s.onDone(s.snapshot())
		// Close the journal file after the final RunFinished event has been
		// appended. Deferring ensures the file is closed even when the run exits
		// via ctx.Done().
		if s.writer != nil {
			_ = s.writer.Close()
		}
		// Remove any worktrees left active at run end. The branches are kept so
		// downstream merge steps (e.g. git merge jig/feature/implement) can still
		// reference them after worktree removal.
		if s.repoRoot != "" {
			for _, path := range s.worktrees {
				_ = removeWorktree(s.repoRoot, path)
			}
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

		// If the step has from="user" inputs that haven't been collected yet,
		// park it on a prompt and keep scanning for other runnable steps.
		// The preResolvedInputs check prevents re-intercepting after all inputs
		// are collected and the step resets back to StatusPending.
		if hasUserInputs(st) && len(s.preResolvedInputs[st.ID]) == 0 {
			s.dispatchUserPrompt(st)
			continue
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

// hasUserInputs reports whether st declares any from="user" inputs.
func hasUserInputs(st *workflow.Step) bool {
	for _, inp := range st.Inputs {
		if inp.From == "user" {
			return true
		}
	}
	return false
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
		// the user delivers a verdict or input. Don't let the run terminate early.
		if st.Status == step.StatusAwaitingReview {
			return true
		}
		if st.Status == step.StatusNeedsInput {
			return true
		}
		// A step parked for a recovery decision keeps the run alive: the human
		// will retry, resume, or abort.
		if st.Status == step.StatusAwaitingRecovery {
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
	// Create a git worktree for mutating steps (first dispatch only; retries and
	// loop re-dispatches reuse the existing worktree so edits accumulate on the
	// same branch).
	var worktreePath string
	if st.Isolation == workflow.IsolationWorktree && s.repoRoot != "" {
		if existing, ok := s.worktrees[st.ID]; ok {
			worktreePath = existing
		} else {
			branch := "jig/" + sanitizeBranchName(s.wf.Meta.Name) + "/" + st.ID
			wtPath := filepath.Join(s.jigRoot, "worktrees", s.runID, st.ID)
			baseSHA, err := createWorktree(s.repoRoot, wtPath, branch)
			if err != nil {
				// Setup failure (e.g. a git error creating the branch). Park for a
				// human recovery decision rather than tearing down the run — a retry
				// re-runs createWorktree. There is no agent session to resume here.
				s.states[st.ID].Result = &step.Result{
					Status: step.StatusFailed,
					Err:    fmt.Sprintf("create worktree for step %q: %v", st.ID, err),
				}
				s.enterRecovery(st.ID)
				return
			}
			s.worktrees[st.ID] = wtPath
			s.wtBaseSHAs[st.ID] = baseSHA
			worktreePath = wtPath
		}
	}

	// Ensure the per-step directory exists before the executor writes its
	// transcript there. The manifest writer also calls StepDir, but only on the
	// step's terminal event — too late for the runner's mid-execution transcript
	// writes, which would otherwise fail with "no such file or directory".
	if s.runDir != "" {
		if _, err := datastore.StepDir(s.runDir, st.ID); err != nil {
			s.states[st.ID].Result = &step.Result{
				Status: step.StatusFailed,
				Err:    fmt.Sprintf("create step dir for %q: %v", st.ID, err),
			}
			s.enterRecovery(st.ID)
			return
		}
	}

	from := s.states[st.ID].Status
	s.transition(st.ID, from, step.StatusRunning)
	s.inFlight++

	runID, stepID := s.runID, st.ID
	subs := s.subs
	inbox := s.inbox
	rep := &reporter{
		subs:  subs,
		inbox: inbox,
		ev: func(e Event) {
			// Tag the event with run/step IDs and fan-out without holding any lock.
			switch e := e.(type) {
			case StepOutput:
				e.RunID, e.StepID = runID, stepID
				fanOutLive(subs, e)
			case StepToolCall:
				e.RunID, e.StepID = runID, stepID
				fanOutLive(subs, e)
			case StepMessage:
				e.RunID, e.StepID = runID, stepID
				fanOutLive(subs, e)
			case AgentQuestion:
				e.RunID, e.StepID = runID, stepID
				fanOutCtrl(subs, e)
				// Notify the scheduler to transition the step to StatusNeedsInput.
				// Non-blocking: inbox is buffered (64) and rarely full.
				select {
				case inbox <- agentQuestionNotifyMsg{stepID: stepID}:
				default:
				}
			}
		},
	}
	s.reporters[st.ID] = rep
	var artifactDir, transcriptPath string
	if s.runDir != "" {
		artifactDir = filepath.Join(s.runDir, "artifacts")
		transcriptPath = datastore.TranscriptPath(s.runDir, st.ID)
	}
	s.resolveAllInputs(st)
	req := s.buildRequest(st, runID, worktreePath, artifactDir, transcriptPath)
	delete(s.preResolvedInputs, st.ID)
	if sess := s.resumeSessions[st.ID]; sess != "" {
		req.ResumeSessionID = sess
		req.Message = s.stepMessage[st.ID]
		delete(s.resumeSessions, st.ID)
		delete(s.stepMessage, st.ID)
	}
	// Clear the structured-output cache so block_on and when-guards always
	// see fresh output after a re-run rather than a stale cached decode.
	delete(s.structured, st.ID)

	go func() {
		result, err := s.exec.Execute(ctx, req, rep)
		s.inbox <- stepDoneMsg{stepID: stepID, result: result, err: err}
	}()
}

// resolveAllInputs appends ResolvedInput entries for every non-user input
// into s.preResolvedInputs[st.ID]. User inputs are already present from the
// prompt-collection flow (handle(userInputMsg)); this fills the remaining refs.
func (s *scheduler) resolveAllInputs(st *workflow.Step) {
	for _, inp := range st.Inputs {
		if inp.From == "user" {
			continue // already collected via prompt flow
		}

		var value string

		switch {
		case inp.Ref != "" && len(inp.RefField) > 0:
			// @step.field — extract from the dependency's structured output.
			// Reuse the same decode cache as evalGuard for consistency.
			depState := s.states[inp.Ref]
			if depState != nil && depState.Result != nil {
				m, ok := s.structured[inp.Ref]
				if !ok && len(depState.Result.Structured) > 0 {
					if err := json.Unmarshal(depState.Result.Structured, &m); err == nil {
						s.structured[inp.Ref] = m
					}
				}
				var cur any = m
				for _, seg := range inp.RefField {
					obj, ok := cur.(map[string]any)
					if !ok {
						cur = nil
						break
					}
					cur, ok = obj[seg]
					if !ok {
						cur = nil
						break
					}
				}
				switch v := cur.(type) {
				case string:
					value = v
				case bool:
					if v {
						value = "true"
					} else {
						value = "false"
					}
				case nil:
					// missing field — validation already caught dangling refs; pass empty
				default:
					if b, err := json.Marshal(v); err == nil {
						value = string(b)
					}
				}
			}

		case inp.Ref != "":
			// bare @step — use the dependency's artifact output file path.
			if depState := s.states[inp.Ref]; depState != nil && depState.Result != nil {
				value = depState.Result.OutputPath
			}

		case inp.Path != "":
			value = inp.Path
		}

		s.preResolvedInputs[st.ID] = append(s.preResolvedInputs[st.ID], ResolvedInput{
			Ref:   inp,
			Value: value,
		})
	}
}

// buildRequest constructs the StepRequest for a dispatch. It must be called
// after resolveAllInputs so that preResolvedInputs contains all inputs
// (both user and non-user).
func (s *scheduler) buildRequest(
	st *workflow.Step,
	runID, worktreePath, artifactDir, transcriptPath string,
) StepRequest {
	state := s.states[st.ID]
	// Agent steps get the engine-assembled "Workflow context" preamble; command
	// and review steps carry none, leaving their prompt untouched. An agent step
	// with inject_context off (explicitly, or inherited from [defaults]) also
	// opts out, dispatching with a byte-identical no-context prompt.
	var workflowContext string
	if st.Type == workflow.StepAgent && st.InjectContextEnabled() {
		workflowContext = s.buildStepContext(st).Render()
	}
	return StepRequest{
		RunID:           runID,
		Step:            st,
		Inputs:          s.preResolvedInputs[st.ID],
		Feedback:        s.stepFeedback[st.ID],
		WorkflowContext: workflowContext,
		ArtifactDir:     artifactDir,
		Worktree:        worktreePath,
		TranscriptPath:  transcriptPath,
		Iteration:       state.Iteration,
		Attempt:         state.Attempt,
	}
}

// handle processes one message from the scheduler's inbox.
func (s *scheduler) handle(msg schedMsg) {
	switch m := msg.(type) {
	case stepDoneMsg:
		s.inFlight--
		delete(s.reporters, m.stepID)

		// Record result and synthesize an error result when the executor
		// returned a Go error rather than a failed Result.
		if m.result != nil {
			s.states[m.stepID].Result = m.result
		}
		if m.err != nil {
			res := s.states[m.stepID].Result
			if res == nil {
				res = &step.Result{Status: step.StatusFailed}
				s.states[m.stepID].Result = res
			}
			if res.Err == "" {
				res.Err = m.err.Error()
			}
		}

		// Raw execution failure short-circuits the chain.
		execFailed := m.err != nil || (m.result != nil && m.result.Status == step.StatusFailed)

		wfStep := s.stepByID(m.stepID)

		if execFailed {
			s.applyFailurePolicy(m.stepID, wfStep)
			break
		}

		// Run the post-execution handler chain.
		decision := decisionContinue
		for _, h := range s.postExecChain {
			if decision = h(s, m, wfStep); decision != decisionContinue {
				break
			}
		}

		switch decision {
		case decisionFailed:
			s.applyFailurePolicy(m.stepID, wfStep)
		case decisionNeedsInput:
			// handler already transitioned the step; nothing more to do
		default: // decisionContinue — all handlers passed → step succeeded
			curFrom := s.states[m.stepID].Status
			s.transition(m.stepID, curFrom, step.StatusSucceeded)
			if wfStep != nil && wfStep.Loop != nil {
				s.fireLoop(m.stepID, wfStep)
			}
		}

	case userInputMsg:
		// Append the collected text as an inline ResolvedInput.
		s.collectedUserInputs[m.stepID] = append(
			s.collectedUserInputs[m.stepID],
			ResolvedInput{
				Ref:   workflow.Input{Inline: true, As: m.as},
				Value: m.text,
			},
		)
		// If more prompts are queued, fire the next one.
		if len(s.pendingUserInputs[m.stepID]) > 0 {
			next := s.pendingUserInputs[m.stepID][0]
			s.pendingUserInputs[m.stepID] = s.pendingUserInputs[m.stepID][1:]
			s.emit(PromptRequest{
				RunID:  s.runID,
				StepID: m.stepID,
				Label:  next.Label,
				As:     next.As,
			})
			return
		}
		// All inputs collected — promote to preResolved and reset to pending
		// so nextReady picks it up for normal dispatch.
		s.preResolvedInputs[m.stepID] = s.collectedUserInputs[m.stepID]
		delete(s.collectedUserInputs, m.stepID)
		delete(s.pendingUserInputs, m.stepID)
		s.transition(m.stepID, step.StatusAwaitingReview, step.StatusPending)

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

	case humanMessageMsg:
		s.handleHumanMessage(m)

	case agentInputMsg:
		s.handleAgentInput(m)

	case recoverMsg:
		s.handleRecover(m)

	case agentQuestionNotifyMsg:
		// The agent step called AskUserQuestion; transition to StatusNeedsInput so
		// the TUI can surface the question. The goroutine is still alive, blocked on
		// rep.answerCh; inFlight remains > 0.
		state := s.states[m.stepID]
		if state != nil && state.Status == step.StatusRunning {
			s.transition(m.stepID, step.StatusRunning, step.StatusNeedsInput)
		}

	case agentQuestionAnswerMsg:
		// Deliver the human's answer to the blocked agent goroutine.
		rep := s.reporters[m.stepID]
		if rep == nil || rep.answerCh == nil {
			return
		}
		state := s.states[m.stepID]
		if state != nil && state.Status == step.StatusNeedsInput {
			s.transition(m.stepID, step.StatusNeedsInput, step.StatusRunning)
		}
		select {
		case rep.answerCh <- m.answer:
		default:
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
		// Exhausted automatic retries — hand off to the human recovery gate rather
		// than tearing down the run.
		s.enterRecovery(stepID)

	case workflow.FailContinue:
		// Mark failed but don't cancel; dependents with depsReady() will treat
		// this step as a "satisfied" node and proceed.
		s.transition(stepID, from, step.StatusFailed)

	default: // FailAbort or unrecognised
		// Pause for a human recovery decision (retry / resume / abort) instead of
		// the old hair-trigger s.cancel(): the run and any in-flight sibling steps
		// stay alive. Choosing RecoverAbort at the gate performs the real teardown.
		s.enterRecovery(stepID)
	}
}

// enterRecovery parks a failed step in step.StatusAwaitingRecovery and emits a
// RecoveryRequest so a human can retry (fresh), resume the failed agent session
// with the error fed back in, or abort. It replaces the previous s.cancel()
// teardown on unrecoverable failure: the run and any in-flight sibling steps
// keep running while the human decides. state.Result must already carry the
// failure (Err, and SessionID when an agent session ran).
func (s *scheduler) enterRecovery(stepID string) {
	state := s.states[stepID]
	if state.Result == nil {
		state.Result = &step.Result{Status: step.StatusFailed}
	}
	s.transition(stepID, state.Status, step.StatusAwaitingRecovery)
	s.emit(RecoveryRequest{
		RunID:     s.runID,
		StepID:    stepID,
		Err:       state.Result.Err,
		CanResume: state.Result.SessionID != "",
	})
}

// handleRecover applies a human recovery decision to a step parked in
// step.StatusAwaitingRecovery.
func (s *scheduler) handleRecover(m recoverMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusAwaitingRecovery {
		return // stale or duplicate
	}

	switch m.action {
	case RecoverAbort:
		s.aborted = true
		s.transition(m.stepID, state.Status, step.StatusFailed)
		s.cancel()

	case RecoverRetry, RecoverResume:
		if s.recoverCount[m.stepID] >= maxRecoverRounds {
			s.emit(RunError{
				RunID: s.runID,
				Err:   fmt.Sprintf("step %q: exceeded maximum recovery rounds (%d)", m.stepID, maxRecoverRounds),
			})
			return // stay parked; the human can still abort
		}
		if m.action == RecoverResume {
			// Resume requires a captured session. Without one, the TUI should not
			// have offered resume; ignore rather than silently doing a fresh retry.
			if state.Result == nil || state.Result.SessionID == "" {
				return
			}
			s.resumeSessions[m.stepID] = state.Result.SessionID
			s.stepMessage[m.stepID] = composeRecoveryMessage(state.Result.Err, m.text)
		}
		s.recoverCount[m.stepID]++
		state.Attempt++
		// Back to pending: the main loop re-dispatches. For an agent step that ran
		// in a worktree, dispatch reuses the existing worktree; a setup-failure
		// retry has no stored worktree, so dispatch re-runs createWorktree.
		s.transition(m.stepID, state.Status, step.StatusPending)
	}
}

// composeRecoveryMessage builds the resume prompt for RecoverResume: the failed
// step's captured error plus any operator guidance, framed so the agent revisits
// its approach instead of repeating the mistake.
func composeRecoveryMessage(errText, guidance string) string {
	var b strings.Builder
	b.WriteString("Your previous attempt failed with this error:\n\n")
	if strings.TrimSpace(errText) != "" {
		b.WriteString(errText)
	} else {
		b.WriteString("(no error detail was captured)")
	}
	if strings.TrimSpace(guidance) != "" {
		b.WriteString("\n\nAdditional guidance from the operator:\n")
		b.WriteString(guidance)
	}
	b.WriteString("\n\nReview what went wrong and take a different approach to complete the task.")
	return b.String()
}

// runGate evaluates the [step.validate] block for wfStep synchronously.
// Returns (true, detail) on pass, (false, detail) on failure.
// worktreePath, when non-empty, sets the working directory for command gates
// so they run inside the step's isolated worktree (Phase 5).
func (s *scheduler) runGate(wfStep *workflow.Step, worktreePath string) (bool, string) {
	v := wfStep.Validate

	// Command gate: run via sh -c, check exit code 0.
	if v.Command != "" {
		cmd := exec.Command("sh", "-c", v.Command)
		if worktreePath != "" {
			cmd.Dir = worktreePath
		}
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

// dispatchUserPrompt parks an agent step that needs from="user" inputs on
// StatusAwaitingReview and emits the first PromptRequest. Subsequent prompts
// are emitted by handle(userInputMsg) as each answer arrives.
func (s *scheduler) dispatchUserPrompt(st *workflow.Step) {
	var userInputs []workflow.Input
	for _, inp := range st.Inputs {
		if inp.From == "user" {
			userInputs = append(userInputs, inp)
		}
	}
	s.pendingUserInputs[st.ID] = userInputs[1:]
	s.collectedUserInputs[st.ID] = nil
	s.transition(st.ID, s.states[st.ID].Status, step.StatusAwaitingReview)
	s.emit(PromptRequest{
		RunID:  s.runID,
		StepID: st.ID,
		Label:  userInputs[0].Label,
		As:     userInputs[0].As,
	})
}

// handleHumanMessage processes a free-text message addressed to a review gate.
// It finds the reviewed agent step, checks the message cap, then resets the
// target + review steps to pending so the agent re-runs and the gate re-fires.
func (s *scheduler) handleHumanMessage(m humanMessageMsg) {
	state := s.states[m.stepID]
	if state.Status != step.StatusAwaitingReview {
		return // stale
	}
	wfStep := s.stepByID(m.stepID)
	if wfStep == nil {
		return
	}

	// Resolve "@stepid" or "@stepid.field" → bare step ID.
	targetID := strings.TrimPrefix(wfStep.Review, "@")
	if dot := strings.Index(targetID, "."); dot >= 0 {
		targetID = targetID[:dot]
	}
	targetState := s.states[targetID]
	if targetState == nil || targetState.Result == nil || targetState.Result.SessionID == "" {
		return // no resumable session
	}

	// Cap enforcement.
	maxMsg := defaultReviewMaxMessages
	if wfStep.MaxMessages > 0 {
		maxMsg = wfStep.MaxMessages
	}
	s.reviewMessages[m.stepID]++
	if s.reviewMessages[m.stepID] > maxMsg {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q: max_messages %d reached", m.stepID, maxMsg),
		})
		// Roll back the increment so the gate stays and the count stays at cap.
		s.reviewMessages[m.stepID]--
		return
	}

	// Stash resume info for the target's next dispatch.
	s.resumeSessions[targetID] = targetState.Result.SessionID
	s.stepMessage[targetID] = m.text

	// Reset the loop body (target → review) to pending so both re-run.
	for _, id := range s.loopBody(targetID, m.stepID) {
		st := s.states[id]
		s.transition(id, st.Status, step.StatusPending)
	}
}

// evalBlockOn evaluates the step's block_on condition against its own output.
func (s *scheduler) evalBlockOn(stepID string, wfStep *workflow.Step) bool {
	cond, err := workflow.ParseCondition(wfStep.BlockOn)
	if err != nil {
		return false
	}
	return s.evalGuard(cond)
}

// handleAgentInput processes human input for an agent step blocked by block_on.
// It stashes the session resume info and resets the step to pending for re-dispatch.
func (s *scheduler) handleAgentInput(m agentInputMsg) {
	state := s.states[m.stepID]
	if state.Status != step.StatusNeedsInput {
		return
	}
	if state.Result == nil || state.Result.SessionID == "" {
		return
	}
	const maxInputRounds = 20
	s.stepInputCount[m.stepID]++
	if s.stepInputCount[m.stepID] > maxInputRounds {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q: exceeded maximum input rounds (%d)", m.stepID, maxInputRounds),
		})
		s.stepInputCount[m.stepID]--
		return
	}
	s.resumeSessions[m.stepID] = state.Result.SessionID
	s.stepMessage[m.stepID] = m.text
	s.transition(m.stepID, step.StatusNeedsInput, step.StatusPending)
}

// dispatchReview handles a review step inline: it never goes to a worker.
// The step is parked at awaiting_review and a ReviewRequest is emitted so the
// TUI can render choices and collect a human verdict via Run.Resolve.
// When review = "diff", the Diff field is populated by walking the dependency
// graph for any captured worktree diffs.
func (s *scheduler) dispatchReview(st *workflow.Step) {
	from := s.states[st.ID].Status
	s.transition(st.ID, from, step.StatusAwaitingReview)

	var diff string
	if st.Review == "diff" {
		diff = s.collectDepDiffs(st.ID)
	}

	allowMsg := false
	if strings.HasPrefix(st.Review, "@") {
		targetID := strings.TrimPrefix(st.Review, "@")
		if dot := strings.Index(targetID, "."); dot >= 0 {
			targetID = targetID[:dot]
		}
		if tgt := s.stepByID(targetID); tgt != nil && tgt.Type == workflow.StepAgent {
			maxMsg := defaultReviewMaxMessages
			if st.MaxMessages > 0 {
				maxMsg = st.MaxMessages
			}
			allowMsg = s.reviewMessages[st.ID] < maxMsg
		}
	}

	s.emit(ReviewRequest{
		RunID:        s.runID,
		StepID:       st.ID,
		Choices:      reviewChoices(st),
		Diff:         diff,
		AllowMessage: allowMsg,
	})
}

// collectDepDiffs walks the dependency graph of stepID and concatenates any
// captured diffs from mutating steps that ran in worktrees.
func (s *scheduler) collectDepDiffs(stepID string) string {
	visited := make(map[string]bool)
	var parts []string
	var walk func(id string)
	walk = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		if d, ok := s.diffs[id]; ok && d != "" {
			parts = append(parts, d)
		}
		if wfStep := s.stepByID(id); wfStep != nil {
			for _, dep := range wfStep.DependsOn {
				walk(dep)
			}
		}
	}
	if wfStep := s.stepByID(stepID); wfStep != nil {
		for _, dep := range wfStep.DependsOn {
			walk(dep)
		}
	}
	return strings.Join(parts, "\n")
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

	// Wire feedback: resolve the @ref to actual content (verdict for review steps,
	// output file text for agent/command steps) so the agent receives real input.
	if loop.Feedback != "" {
		feedbackID := strings.TrimPrefix(loop.Feedback, "@")
		if dot := strings.Index(feedbackID, "."); dot >= 0 {
			feedbackID = feedbackID[:dot]
		}
		var content string
		if fs := s.states[feedbackID]; fs != nil && fs.Result != nil {
			if fs.Result.Verdict != "" {
				content = fs.Result.Verdict
			} else if fs.Result.OutputPath != "" {
				if data, err := os.ReadFile(fs.Result.OutputPath); err == nil {
					content = string(data)
				}
			}
		}
		s.stepFeedback[loop.Goto] = content
	}

	// Record which step's loop fired this re-run so buildStepContext can name the
	// reason. Kept outside the feedback block so it is recorded even when the loop
	// wires no feedback ref. Last-write-wins is correct: only one loop fires per
	// re-run event, so the last write to rerunSource[goto] names the loop that
	// caused this dispatch (the multiple-loops-target-one-step case).
	s.rerunSource[loop.Goto] = stepID

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
	ev := StepStatus{
		RunID:     s.runID,
		StepID:    stepID,
		From:      from,
		To:        to,
		Attempt:   state.Attempt,
		Iteration: state.Iteration,
	}
	// Carry the failure reason and subtype so the TUI can surface them without
	// re-reading result.json. handle() guarantees state.Result.Err is populated
	// (executor error or gate detail) before a step transitions to Failed.
	if to == step.StatusFailed && state.Result != nil {
		ev.Err = state.Result.Err
		ev.Subtype = state.Result.Subtype
	}
	s.emit(ev)
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
	switch e.(type) {
	case StepOutput, StepToolCall, StepMessage:
		fanOutLive(s.subs, e)
	default:
		fanOutCtrl(s.subs, e)
	}
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

// fanOutLive sends e to each subscriber's live channel, dropping for slow consumers.
// Live events (StepOutput, StepToolCall, StepMessage) are liveness signals only;
// the durable content lives in the per-step transcript on disk.
func fanOutLive(subs []sub, e Event) {
	for _, s := range subs {
		select {
		case s.live <- e:
		default:
		}
	}
}

// fanOutCtrl sends e to each subscriber's ctrl channel, dropping for slow consumers.
// Ctrl events are critical (RunFinished, ReviewRequest, etc.) but the ctrl channel
// is sized for worst-case workflow volume, so drops should not occur in practice.
func fanOutCtrl(subs []sub, e Event) {
	for _, s := range subs {
		select {
		case s.ctrl <- e:
		default:
		}
	}
}
