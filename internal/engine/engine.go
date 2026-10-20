// Package engine drives workflow execution. One goroutine (the scheduler) owns
// all mutable state for a run; workers and the TUI communicate with it via its
// inbox channel. Every state transition is emitted as a typed Event, published
// to subscribers — the TUI is merely one.
package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/review"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// ── scheduler messages ────────────────────────────────────────────────────────

// ── reporter ─────────────────────────────────────────────────────────────────

// reporter routes live step signals through the scheduler's fan-out.
// It is created per-dispatch and passed to the executor; the executor
// may call it from its own goroutine, so fanOutLive must not touch scheduler state.
type reporter struct {
	subs     []sub
	ev       func(Event)
	stepID   string
	inbox    chan<- schedMsg
	security func(int)
}

func (r *reporter) Output(delta string)          { r.ev(StepOutput{Delta: delta}) }
func (r *reporter) ToolCall(tool, detail string) { r.ev(StepToolCall{Tool: tool, Detail: detail}) }
func (r *reporter) Message(seq, iteration int) {
	r.ev(StepMessage{Seq: seq, Iteration: iteration})
	if r.security != nil {
		r.security(seq)
	}
}

// Finding routes a SecurityFinding through the ctrl channel (must-not-drop).
func (r *reporter) Finding(sf SecurityFinding) { r.ev(sf) }

func (r *reporter) Question(ctx context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
	if err := req.Validate(); err != nil {
		return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
	}
	reply := make(chan interaction.QuestionResponse, 1)
	select {
	case r.inbox <- agentQuestionRequestMsg{stepID: r.stepID, request: req, reply: reply}:
	case <-ctx.Done():
		return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
	}
	select {
	case response := <-reply:
		return response
	case <-ctx.Done():
		select {
		case r.inbox <- agentQuestionAbandonMsg{stepID: r.stepID, requestID: req.ID}:
		default:
		}
		return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
	}
}

type pendingQuestion struct {
	request  interaction.QuestionRequest
	reply    chan<- interaction.QuestionResponse
	restored bool
}

// ── scheduler ────────────────────────────────────────────────────────────────

type scheduler struct {
	// graph is wf's static dependency DAG; see stepGraph.
	graph stepGraph
	// runtime holds per-step execution state; see stepRuntime.
	runtime  map[string]*stepRuntime
	wf       *workflow.Workflow
	runID    string
	states   map[string]*step.State
	inbox    chan schedMsg
	subs     []sub
	exec     Executor
	resolver IntegrationResolver
	// runCtx is the run's context, captured by runLoop so asynchronous work
	// started from a command (the agent conflict resolver) observes stop and
	// cancel. It is read only on the scheduler goroutine.
	runCtx         context.Context
	integrationSeq uint64
	cancel         context.CancelFunc // cancels the run context; used by abort policy
	writer         *manifest.Writer   // nil when persistence is disabled (root = "")
	runDir         datastore.Run      // .jig/runs/<runID>/; "" when persistence is disabled

	// pendingLoops coalesces route back-edges that target the same goto step
	// within one execution wave. A finishing step records its intent here
	// instead of rewinding immediately; the rewind fires once, with the union of
	// every contributing looper's feedback, when the whole rewind body has
	// settled (loopBarrierReady). This makes parallel siblings that all loop to
	// the same step deterministic — no reset-collides-with-in-flight-worker, no
	// last-write-wins feedback clobber. Keyed by goto-target step id.
	pendingLoops     map[string]*loopIntent
	aborted          bool // true when the run was explicitly aborted (loop cap, etc.)
	slots            capacity
	securityFindings int
	networkRequests  int
	secretResolver   SecretResolver
	seq              int
	fatalJournalErr  error // stops scheduling when state cannot be made durable
	securitySignals  chan<- sentinel.StepSignal

	// Phase 5: worktree lifecycle.
	jigRoot   string                  // .jig/ root; "" when persistence is disabled
	repoRoot  string                  // git repo root (parent of jigRoot)
	worktrees map[string]stepWorktree // active mutation worktree per step
	// executionViews holds read-only dispatch snapshots separately from mutation
	// worktrees so they can never be captured, validated, or integrated as edits.
	executionViews map[string]executionWorkspace

	// Run-integration branch (spec 06). At run start (repoRoot != "") the
	// scheduler creates a run branch at the working-branch HEAD and a run
	// worktree checked out on it; each mutating step's worktree branches off the
	// run branch's current HEAD and squash-merges back as one commit. stepCommits
	// maps stepID → the sha of that step's squash commit, reconstructable from the
	// run branch's `jig-step:` trailers. All three stay "" / empty on the
	// persistence-off / non-git path, where steps run in place with no integration.
	branch      runBranch         // per-run integration branch; zero when persistence-off
	stepCommits map[string]string // stepID → squash-merge commit sha on the run branch

	// Final-merge gate (spec 06 A3): a pre-RunFinished completion step. When the
	// run reaches terminal with a non-empty run branch, the scheduler emits one
	// FinalMergeRequest and parks (awaitingFinalMerge) instead of finishing;
	// Run.FinalMerge lands or discards, then the run settles. terminated is set by
	// the settling handler so run()'s loop returns after emitting RunFinished. No
	// RunResumed event exists — the run does not re-enter after it settles.
	awaitingFinalMerge bool
	terminated         bool

	// review-gate messaging: a human can send free-text to the reviewed agent
	// step, which resumes the agent's SDK session and re-runs it before a
	reviewSessions map[string]review.Session

	// block_on: tracks how many times a human has provided input to a blocked step.

	// recovery gate: tracks retry/resume rounds per step so the human-driven
	// recovery loop stays bounded (maxRecoverRounds).
	// restoredHold prevents a reopened scheduler from dispatching pending work
	// before the operator acknowledges one of its durable gates.
	restoredHold bool

	// seenEscalations deduplicates critical-finding recovery escalations by
	// Fingerprint. A fingerprint that already triggered enterRecovery must not
	// trigger it again for the same step.
	seenEscalations map[string]bool

	// Per-step cancellation (spec 07 B1). Each dispatched worker gets its own
	// child context derived from the run context, its CancelFunc stored here keyed
	// by step id, so Run.Stop can cancel one worker without touching the run
	// context or its siblings. This deliberately reverses the single-run-context
	// assumption the engine started with (one context.WithCancel shared by every
	// worker): run-level cancellation still exists via s.cancel, but stop is
	// surgical. Entries are removed when the worker's stepDoneMsg is handled.
	stepCancels map[string]context.CancelFunc

	// postExecChain is the Chain of Responsibility built once in newScheduler
	// below and walked by (stepDoneMsg).execute (commands.go) after every
	// successful step execution. See postExecDecision/postExecHandler in
	// handlers.go for the link contract.
	postExecChain []postExecHandler

	onDone func(RunSnapshot) // called once before the scheduler goroutine exits

	// getCauseHint, when non-nil, tells the scheduler which semantic completion
	// cause a caller assigned to Run.Cancel/CancelWithCause. It is read only from
	// the scheduler goroutine after ctx.Done to classify the terminal event; a
	// nil hint falls back to CauseCancelled. Kept out of newScheduler's parameter
	// list so existing test constructors stay compatible.
	getCauseHint func() CompletionCause

	// observers, when non-nil, receives non-blocking notification of every
	// ctrl-class engine Event after it has been journaled. The bridge is set
	// only by production Start/Resume paths; test constructors leave it nil.
	observers *observerRegistry

	// Runtime fan-out registries (see fanout.go). Runtime children are NEVER
	// appended to wf.Steps — the static DAG stays pure and visualizable — so
	// they live only here, keyed by their stable instance id.
	foreach fanOutRegistry
}

// schedulerDeps are newScheduler's inputs. The zero value of every optional
// field means "not configured": no subscribers, no executor, persistence off.
type schedulerDeps struct {
	wf       *workflow.Workflow // required
	runID    string
	inbox    chan schedMsg // required
	subs     []sub
	exec     Executor
	cancel   context.CancelFunc
	writer   *manifest.Writer // nil when persistence is off
	runDir   datastore.Run    // disabled when persistence is off
	jigRoot  string           // "" when persistence is off
	repoRoot string           // "" on the persistence-off / non-git path
	onDone   func(RunSnapshot)
}

func newScheduler(d schedulerDeps) *scheduler {
	if d.wf == nil || d.inbox == nil {
		panic("engine: newScheduler requires a workflow and an inbox")
	}
	wf := d.wf
	states := make(map[string]*step.State, len(wf.Steps))
	for _, s := range wf.Steps {
		states[s.ID] = &step.State{ID: s.ID, Status: step.StatusPending}
	}
	return &scheduler{
		wf:              wf,
		runID:           d.runID,
		states:          states,
		inbox:           d.inbox,
		subs:            d.subs,
		exec:            d.exec,
		cancel:          d.cancel,
		writer:          d.writer,
		runDir:          d.runDir,
		pendingLoops:    make(map[string]*loopIntent),
		jigRoot:         d.jigRoot,
		repoRoot:        d.repoRoot,
		worktrees:       make(map[string]stepWorktree),
		executionViews:  make(map[string]executionWorkspace),
		stepCommits:     make(map[string]string),
		reviewSessions:  make(map[string]review.Session),
		seenEscalations: make(map[string]bool),
		stepCancels:     make(map[string]context.CancelFunc),
		runtime:         make(map[string]*stepRuntime),
		graph:           newStepGraph(wf),
		postExecChain: []postExecHandler{
			phCaptureWorktreeDiff,
			phValidateMutationPaths,
			phRunValidateGate,
			phCheckBlockOn,
			phSquashMergeIntegration,
		},
		onDone:  d.onDone,
		foreach: newFanOutRegistry(),
	}
}

func (s *scheduler) run(ctx context.Context) {
	ids := make([]string, len(s.wf.Steps))
	for i, st := range s.wf.Steps {
		ids[i] = st.ID
	}
	if err := s.emit(RunStarted{RunID: s.runID, Workflow: s.wf.Meta.Name, Steps: ids}); err != nil {
		s.onDone(s.snapshot())
		if s.writer != nil {
			_ = s.writer.Close()
		}
		return
	}

	// Create the per-run integration branch + run worktree at the working-branch
	// HEAD (spec 06). Persistence-off / non-git (repoRoot == "") is a no-op: no
	// run branch, no run worktree, steps run in place. A git error here is a hard
	// setup failure — fail the run before any step burns work.
	if err := s.setupRunBranch(); err != nil {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("setup run branch: %v", err)})
		s.emit(RunFinished{RunID: s.runID, Failed: true, Cause: CauseFailed})
		s.onDone(s.snapshot())
		if s.writer != nil {
			_ = s.writer.Close()
		}
		return
	}
	s.runLoop(ctx)
}

func (s *scheduler) runLoop(ctx context.Context) {
	s.runCtx = ctx
	maxPar := s.wf.Defaults.MaxParallel
	if maxPar <= 0 {
		maxPar = 4
	}

	defer func() {
		// Worktree cleanup runs before RunFinished is emitted (inside the loop or
		// in handleFinalMerge) so that t.TempDir() cleanup in tests never races
		// against a running git subprocess. This call is a safety net for abnormal
		// exits (e.g. panic, early return from setupRunBranch) that bypass those
		// paths; it is idempotent and a no-op when called a second time.
		s.cleanupWorktrees()
		// Signal completion so Snapshot() callers unblock. finalSnap is written
		// before done is closed to satisfy the memory-model happens-before
		// requirement for lock-free reads in Run.Snapshot().
		s.onDone(s.snapshot())
		if s.writer != nil {
			_ = s.writer.Close()
		}
	}()

	for {
		if s.fatalJournalErr != nil {
			return
		}
		if !s.restoredHold {
			// 0. Fire any coalesced loop back-edges whose rewind body has fully
			//    settled. Runs before dispatch so a fired rewind's freshly-pending body
			//    steps are picked up in this same iteration.
			s.fireReadyLoops()
			if s.fatalJournalErr != nil {
				return
			}

			// 1. Dispatch every ready step, respecting max_parallel.
			for s.slots.inFlight < maxPar {
				st, ok := s.nextReady(ctx)
				if !ok {
					break
				}
				s.dispatch(ctx, st)
			}
			if s.fatalJournalErr != nil {
				return
			}

			// 2. Terminal check: nothing running, nothing pending and runnable. Before
			//    finishing, present the final-merge gate (spec 06 A3) when the run branch
			//    carries commits — this is a pre-RunFinished completion step, so the run
			//    parks (does not emit RunFinished) until the operator lands or discards.
			if s.slots.inFlight == 0 && !s.anyPendingRunnable() {
				if !s.requestFinalMergeIfNeeded() {
					s.cleanupWorktrees()
					s.emit(RunFinished{RunID: s.runID, Failed: s.anyFailed(), Cause: s.terminalCause()})
					return
				}
				// Parked on the final-merge gate: fall through and block on the inbox.
			}
		}

		// 3. Block for exactly one message, then loop to re-dispatch.
		select {
		case msg := <-s.inbox:
			s.handle(msg)
			// The final-merge handler settles the run in place (emits RunFinished
			// itself); it does not re-enter the dispatch loop (no RunResumed).
			if s.terminated {
				return
			}
		case <-ctx.Done():
			s.cleanupWorktrees()
			s.emit(RunFinished{RunID: s.runID, Failed: true, Cause: s.cancelCause(ctx)})
			return
		}
	}
}

// nextReady returns the first pending step whose every depends_on is satisfied
// and whose when guard (if any) is true. Guard false → step is immediately
// skipped with a cascade; review steps are handled inline and also not returned
// (they park on human input via dispatchReview). Both cases continue scanning
// for the next dispatchable step.
func (s *scheduler) nextReady(ctx context.Context) (*workflow.Step, bool) {
scan:
	for i := range s.wf.Steps {
		if s.fatalJournalErr != nil {
			return nil, false
		}
		st := &s.wf.Steps[i]
		state := s.states[st.ID]

		// A running foreach family: source-order children take priority over
		// any other static step this scan would otherwise reach, keeping
		// dispatch order stable relative to declaration order even though
		// children never appear in wf.Steps. If nothing in this family is
		// ready right now (all done, or its family-local cap is full), fall
		// through to the rest of the static graph rather than blocking on it.
		if isOpenFamilyBarrier(st, state.Status) {
			if child, ok := s.nextReadyChild(st.ID); ok {
				return child, true
			}
			continue
		}
		if state.Status != step.StatusPending {
			continue
		}
		for _, check := range readinessChecks {
			switch check(s, ctx, st) {
			case readinessSkip:
				continue scan
			case readinessDispatch:
				return st, true
			case readinessHalt:
				return nil, false
			}
		}
		return st, true
	}
	return nil, false
}

// stepByID returns a pointer into wf.Steps for the given ID, or nil.
// O(n) but n is small (workflow steps) and called infrequently.
func (s *scheduler) stepByID(id string) *workflow.Step {
	for i := range s.wf.Steps {
		if s.wf.Steps[i].ID == id {
			return &s.wf.Steps[i]
		}
	}
	// Runtime fan-out children live only in this registry, never in wf.Steps
	// (see fanout.go).
	if st, ok := s.foreach.children[id]; ok {
		return st
	}
	return nil
}

// handle processes one message from the scheduler's inbox by delegating to
// its Command implementation — see command in commands.go.
func (s *scheduler) handle(msg schedMsg) {
	msg.(command).execute(s)
}

// writeStepSecurityMarkdown refreshes steps/<stepID>/security.md from the
// run's findings.jsonl (the source of truth) so the step's on-disk artifacts
// carry a human-readable security report alongside input.md/output.md,
// visible in the same file list. Best-effort: persistence-off runs and
// findings read errors are skipped since findings.jsonl is authoritative; a
// failed write is reported as a RunError so a stale report is not silent.
func (s *scheduler) writeStepSecurityMarkdown(stepID string) {
	if !s.runDir.Enabled() || stepID == "" {
		return
	}
	findings, err := sentinel.ReadAll(s.runDir.Findings())
	if err != nil {
		return
	}
	var stepFindings []sentinel.Finding
	for _, f := range findings {
		if f.StepID == stepID {
			stepFindings = append(stepFindings, f)
		}
	}
	if err := s.runDir.Step(stepID).Ensure(); err != nil {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("write security report for %q: %v", stepID, err)})
		return
	}
	path := s.runDir.Step(stepID).Security()
	if err := os.WriteFile(path, []byte(sentinel.RenderMarkdown(stepID, stepFindings)), 0o644); err != nil {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("write security report %s: %v", path, err)})
	}
}

// handleSecurityFinding escalates critical security findings to the recovery
// gate. Non-critical findings are silently recorded (seenEscalations) so
// duplicate fingerprints remain no-ops even if they later become critical.
// A fingerprint that already triggered enterRecovery is skipped (rate-limit).
// If the step is already terminal or the run is done, no recovery action is taken.
func (s *scheduler) handleSecurityFinding(sf SecurityFinding) {
	// Always record the fingerprint to prevent duplicate escalations.
	if s.seenEscalations[sf.Fingerprint] {
		return
	}
	s.seenEscalations[sf.Fingerprint] = true
	s.securityFindings++
	s.writeStepSecurityMarkdown(sf.StepID)

	if sf.Severity != "critical" {
		return
	}
	state, ok := s.states[sf.StepID]
	if !ok {
		return // unknown step — finding already recorded above
	}
	if sf.Generation != state.Generation || sf.Iteration != state.Iteration || sf.Attempt != state.Attempt {
		return
	}
	switch state.Status {
	case step.StatusRunning, step.StatusNeedsInput:
		// Blockable: set a descriptive error and park for human recovery decision.
		if state.Result == nil {
			state.Result = &step.Result{Status: step.StatusFailed}
		}
		if state.Result.Err == "" {
			state.Result.Err = "security escalation: critical finding from " + sf.Monitor
		}
		s.enterRecovery(sf.StepID)
		// Reap the still-live worker: cancel its context so a Question-blocked (or
		// otherwise in-flight) worker unwinds and its stepDoneMsg decrements
		// inFlight. The step is already parked at StatusAwaitingRecovery, so the
		// completing worker is recorded-and-kept-parked (see handle(stepDoneMsg))
		// rather than double-counted when the human later retries.
		// The registry entry goes with it, so a replacement dispatch registers
		// its own and the reaped completion cannot cancel that.
		if cancel, ok := s.stepCancels[sf.StepID]; ok {
			cancel()
			delete(s.stepCancels, sf.StepID)
			s.rt(sf.StepID).reaped = true
		}
		// StatusAwaitingRecovery: already parked; finding recorded above.
		// Terminal states (succeeded, failed, skipped, stopped): record only.
	}
}

// handleStop cancels one running step's worker without ending the run (spec 07
// B1). It cancels only that step's child context — never the run context — so
// the worker exits, its stepDoneMsg is routed to StatusStopped (handle marks it
// via stepRuntime.stopping), and the run stays alive and quiescent. Stopping a step that
// has no live worker (pending, already terminal, parked without a worker, or an
// unknown id) is a documented no-op: the absence of a cancel-registry entry is
// exactly "no worker in flight".
func (s *scheduler) handleStop(m stopMsg) {
	cancel, ok := s.stepCancels[m.stepID]
	if !ok {
		return // no worker in flight for this step: nothing to stop
	}
	// Mark the step as being stopped before cancelling so the worker's inbound
	// stepDoneMsg (which may already be racing us) is routed to StatusStopped
	// rather than through the failure policy. The registry entry is cleared when
	// that stepDoneMsg is handled.
	s.rt(m.stepID).stopping = true
	cancel()
}

// handleResume brings a step parked at step.StatusStopped back up (spec 07 B2).
// With a captured session id it continues the agent conversation with a new
// message (reusing the stepRuntime resumeSession/message machinery that dispatch and
// the runner's WithResume/WithContinueConversation path already implement);
// without one it restarts the step fresh — a documented degrade, not an error.
// Either way the step returns to StatusPending so the dispatch loop re-runs it,
// reusing its existing worktree so partial edits accumulate. Resuming a step
// that is not stopped is a no-op.
func (s *scheduler) handleResume(m resumeMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusStopped {
		return // stale, duplicate, or not a stopped step
	}
	if state.Result != nil && state.Result.SessionID != "" {
		// Resume-as-continue: hand the session id + operator message to the next
		// dispatch. This continues the conversation; it does not recover the exact
		// interrupted turn (an SDK limitation).
		s.rt(m.stepID).resumeSession = state.Result.SessionID
		s.rt(m.stepID).message = m.message
	}
	// No captured session id → leave resumeSession unset: dispatch builds the
	// full prompt and the runner starts a fresh session (documented degrade).
	state.Attempt++
	s.restoredHold = false
	s.transition(m.stepID, step.StatusStopped, step.StatusPending)
}

// terminalCause classifies the outcome of a normally-settled run for the
// terminating RunFinished event. Runs that reached this call site never
// executed the ctx.Done branch so a hint set from a Cancel is treated as an
// unrelated late signal.
func (s *scheduler) terminalCause() CompletionCause {
	if s.anyFailed() {
		return CauseFailed
	}
	return CauseSucceeded
}

// cancelCause classifies a run that exited on ctx.Done. A caller may have set
// an explicit semantic cause via Run.CancelWithCause; otherwise ctx.Err
// distinguishes a deadline expiry from an unclassified interruption.
func (s *scheduler) cancelCause(ctx context.Context) CompletionCause {
	if s.getCauseHint != nil {
		if c := s.getCauseHint(); c != "" {
			return c
		}
	}
	if ctx != nil && ctx.Err() == context.DeadlineExceeded {
		return CauseTimeout
	}
	return CauseCancelled
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
	s.rt(st.ID).pendingInputs = userInputs[1:]
	s.rt(st.ID).collectedInputs = nil
	s.transition(st.ID, s.states[st.ID].Status, step.StatusAwaitingReview)
	s.emit(PromptRequest{
		RunID:  s.runID,
		StepID: st.ID,
		Label:  userInputs[0].Label,
		As:     userInputs[0].As,
	})
}

// handleAgentInput processes human input for an agent step blocked by block_on.
// It stashes the session resume info and resets the step to pending for re-dispatch.
func (s *scheduler) handleAgentInput(m agentInputMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusNeedsInput {
		return
	}
	if state.Result == nil || state.Result.SessionID == "" {
		return
	}
	const maxInputRounds = 20
	s.rt(m.stepID).inputRounds++
	if s.rt(m.stepID).inputRounds > maxInputRounds {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q: exceeded maximum input rounds (%d)", m.stepID, maxInputRounds),
		})
		s.rt(m.stepID).inputRounds--
		return
	}
	s.rt(m.stepID).resumeSession = state.Result.SessionID
	s.rt(m.stepID).message = m.text
	s.restoredHold = false
	s.transition(m.stepID, step.StatusNeedsInput, step.StatusPending)
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
		if d := s.rt(id).diff; d != "" {
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

// loopIntent accumulates the contributions of every looper that fired toward a
// single goto target within one execution wave, so the coalesced rewind carries
// the union of their feedback rather than only the fastest sibling's.
type loopIntent struct {
	gotoID   string
	contribs []loopContribution
}

// loopContribution is one looper's fired back-edge: the source step, its type
// (so buildStepContext can name the reason), the resolved feedback content (""
// when the loop wires none), and the iteration at which it fired.
type loopContribution struct {
	source     string
	kind       workflow.StepType
	feedback   string
	iter       int
	max        int
	routeIndex int
	when       string
	// automaticCheckRoute stops a completed failing check from dispatching a
	// downstream approval while its remediation back-edge is waiting to fire.
	automaticCheckRoute bool
}

// recordRoutes chooses the first matching route (or explicit fallback) and
// records an engine-owned route event before any rewind state changes.
func (s *scheduler) recordRoutes(stepID string, wfStep *workflow.Step, submittedFeedback string) {
	if wfStep == nil {
		return
	}
	for i, route := range wfStep.Routes {
		if !route.Fallback {
			if !s.guardHolds(route.When) {
				continue
			}
		}
		s.recordRouteIntent(stepID, wfStep, route, i+1, submittedFeedback)
		return
	}
}

func (s *scheduler) recordRouteIntent(stepID string, wfStep *workflow.Step, route workflow.Route, routeIndex int, submittedFeedback string) {
	state := s.states[stepID]
	s.emit(RouteSelected{RunID: s.runID, StepID: stepID, RouteIndex: routeIndex, When: route.When, Goto: route.Goto, Iteration: state.Iteration + 1, Max: route.MaxIterations})

	if state.Iteration >= route.MaxIterations {
		s.emit(RouteCapExceeded{RunID: s.runID, StepID: stepID, RouteIndex: routeIndex, Goto: route.Goto, Iteration: state.Iteration + 1, Max: route.MaxIterations})
		s.aborted = true
		s.emit(RunError{
			RunID: s.runID, Err: fmt.Sprintf("step %q route %d exceeded max_iterations %d", stepID, routeIndex, route.MaxIterations),
		})
		s.cancel()
		return
	}

	// Resolve the feedback now, while the source step's result is current. A
	// document review supplies its rendered submission directly so comments are
	// preserved even when persistence is off and there is no output file to read.
	var content string
	if route.Feedback != "" {
		feedbackID := strings.TrimPrefix(route.Feedback, "@")
		if dot := strings.Index(feedbackID, "."); dot >= 0 {
			feedbackID = feedbackID[:dot]
		}
		if feedbackID == stepID && submittedFeedback != "" {
			content = submittedFeedback
		} else if fs := s.states[feedbackID]; fs != nil && fs.Result != nil {
			if fs.Result.Verdict != "" {
				content = fs.Result.Verdict
			} else if fs.Result.OutputPath != "" {
				if data, err := os.ReadFile(fs.Result.OutputPath); err == nil {
					content = string(data)
				}
			}
		}
	}

	intent := s.pendingLoops[route.Goto]
	if intent == nil {
		intent = &loopIntent{gotoID: route.Goto}
		s.pendingLoops[route.Goto] = intent
	}
	intent.contribs = append(intent.contribs, loopContribution{
		source:              stepID,
		kind:                wfStep.Type,
		feedback:            content,
		iter:                state.Iteration,
		max:                 route.MaxIterations,
		routeIndex:          routeIndex,
		when:                route.When,
		automaticCheckRoute: isFailedCheck(wfStep, state.Result),
	})
}

// isFailedCheck reports whether a check step's verdict is a failure, which
// makes its route an automatic remediation back-edge.
func isFailedCheck(wfStep *workflow.Step, result *step.Result) bool {
	return wfStep.Type == workflow.StepCheck && result != nil && (result.Verdict == "fail" || result.Verdict == "error")
}

// deferredByAutomaticCheckRoute prevents an evidence failure from racing into
// a downstream review while the scheduler waits for parallel checks in the
// same remediation wave to settle. The back-edge then resets its own body;
// unrelated branches continue normally.
func (s *scheduler) deferredByAutomaticCheckRoute(candidate *workflow.Step) bool {
	for _, intent := range s.pendingLoops {
		for _, contribution := range intent.contribs {
			if !contribution.automaticCheckRoute || contribution.source == candidate.ID {
				continue
			}
			for _, id := range s.closureOf(contribution.source) {
				if id == candidate.ID {
					return true
				}
			}
		}
	}
	return false
}

// fireReadyLoops rewinds every pending loop whose body has fully settled. It is
// called at the top of the run loop, so a fired rewind's freshly-pending body
// steps are dispatched in the same iteration. Targets are processed in workflow
// declaration order for determinism.
func (s *scheduler) fireReadyLoops() {
	if len(s.pendingLoops) == 0 {
		return
	}
	for i := range s.wf.Steps {
		g := s.wf.Steps[i].ID
		intent := s.pendingLoops[g]
		if intent == nil || !s.loopBarrierReady(intent) {
			continue
		}
		s.fireCoalescedLoop(intent)
		delete(s.pendingLoops, g)
	}
}

// loopBarrierReady reports whether a coalesced rewind to gotoID can fire: no
// step in the rewind body is still able to contribute to (or be corrupted by)
// the rewind. A body step blocks the barrier while it is running, parked on a
// human gate, or pending-and-dispatchable (still going to run this wave). A
// pending body step whose deps can never be satisfied is treated as settled, so
// a permanently-blocked branch does not stall the rewind.
func (s *scheduler) loopBarrierReady(intent *loopIntent) bool {
	for _, id := range s.staticRouteBody(intent.gotoID) {
		st := s.states[id]
		switch st.Status {
		case step.StatusRunning,
			step.StatusAwaitingReview,
			step.StatusNeedsInput,
			step.StatusAwaitingRecovery,
			step.StatusAwaitingIntegration:
			return false
		case step.StatusPending:
			candidate := s.stepByID(id)
			if s.depsReady(candidate) && !s.deferredByAutomaticCheckRoute(candidate) {
				return false
			}
		}
	}
	return true
}

// staticRouteBody includes every route that shares a target only while the
// coalescing barrier waits for parallel contributors. The actual reset uses
// bodyUnion below, which deliberately contains only routes that fired.
func (s *scheduler) staticRouteBody(gotoID string) []string {
	seen := map[string]bool{}
	for i := range s.wf.Steps {
		looper := &s.wf.Steps[i]
		for _, route := range looper.Routes {
			if route.Goto != gotoID {
				continue
			}
			for _, id := range s.loopBody(gotoID, looper.ID) {
				seen[id] = true
			}
		}
	}
	var out []string
	for i := range s.wf.Steps {
		if seen[s.wf.Steps[i].ID] {
			out = append(out, s.wf.Steps[i].ID)
		}
	}
	return out
}

// bodyUnion is the reset set for the routes that fired in one execution wave.
// Routes that merely share a target must not join the body until they fire: a
// pending downstream review otherwise blocks automatic check remediation.
func (s *scheduler) bodyUnion(intent *loopIntent) []string {
	seen := map[string]bool{}
	for _, contribution := range intent.contribs {
		for _, id := range s.loopBody(intent.gotoID, contribution.source) {
			seen[id] = true
		}
	}
	var out []string
	for i := range s.wf.Steps {
		if seen[s.wf.Steps[i].ID] {
			out = append(out, s.wf.Steps[i].ID)
		}
	}
	return out
}

// fireCoalescedLoop performs one route-driven rewind and resets the affected
// body at the next iteration.
func (s *scheduler) fireCoalescedLoop(intent *loopIntent) {
	// Deterministic order: sort contributors by workflow declaration index.
	declIndex := func(id string) int {
		for i := range s.wf.Steps {
			if s.wf.Steps[i].ID == id {
				return i
			}
		}
		return len(s.wf.Steps)
	}
	sort.SliceStable(intent.contribs, func(i, j int) bool {
		return declIndex(intent.contribs[i].source) < declIndex(intent.contribs[j].source)
	})

	// New iteration is one past the highest iteration among contributors (they
	// share a wave, so this is just wave+1).
	newIter := 0
	for _, c := range intent.contribs {
		if c.iter+1 > newIter {
			newIter = c.iter + 1
		}
	}
	for _, id := range s.bodyUnion(intent) {
		if state := s.states[id]; state != nil && state.Iteration+1 > newIter {
			newIter = state.Iteration + 1
		}
	}

	// Compose feedback. A single contributor writes its content verbatim (so the
	// single-looper case is byte-identical to before). Multiple contributors are
	// concatenated, each labeled by source, so the rewound step sees every
	// looper's findings — not just the fastest one's.
	var feedbacks []loopContribution
	for _, c := range intent.contribs {
		if c.feedback != "" {
			feedbacks = append(feedbacks, c)
		}
	}
	switch len(feedbacks) {
	case 0:
		// no feedback wired by any contributor — leave stepFeedback untouched
	case 1:
		s.rt(intent.gotoID).feedback = feedbacks[0].feedback
	default:
		var b strings.Builder
		for i, c := range feedbacks {
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "From `%s`:\n%s", c.source, c.feedback)
		}
		s.rt(intent.gotoID).feedback = b.String()
	}

	// Name the re-run reason from the first contributor (declaration order).
	s.rt(intent.gotoID).rerunSource = intent.contribs[0].source
	s.rt(intent.gotoID).rerunMax = intent.contribs[0].max

	// Reset every step in the body to pending with the new iteration count.
	for _, id := range s.bodyUnion(intent) {
		bodyState := s.states[id]
		bodyState.Iteration = newIter
		s.transition(id, bodyState.Status, step.StatusPending)
	}
}

// loopBody returns the IDs of all steps on any path from gotoID to loopID
// (inclusive) in the DAG, in declaration order. These are the steps to reset
// when the loop fires.
func (s *scheduler) loopBody(gotoID, loopID string) []string {
	fwd := s.graph.descendants(nil, gotoID)
	bwd := s.graph.ancestors(loopID)
	body := make(map[string]bool)
	for id := range fwd {
		if bwd[id] {
			body[id] = true
		}
	}
	return s.graph.inOrder(body)
}

func (s *scheduler) transition(stepID string, from, to step.Status) bool {
	return s.transitionRecovery(stepID, from, to, "")
}

func (s *scheduler) transitionRecovery(stepID string, from, to step.Status, recoveryAction string) bool {
	state := s.states[stepID]
	previous, previousTiming := state.Status, state.Timing
	if err := s.emit(s.transitionEvent(stepID, from, to, recoveryAction)); err != nil {
		state.Status, state.Timing = previous, previousTiming
		return false
	}
	// Centralized fan-out barrier settlement: every path that can complete a
	// child (normal success, on_failure="continue", operator recovery-skip, a
	// resumed/recovered child reaching a real terminal status, validation, and
	// integration completion) flows through this one transition function, so
	// this single hook — rather than editing each of those call sites — is
	// what checks whether a settling family's barrier can now close. It is a
	// no-op for every ordinary step and for the family's own transition.
	// terminalStatus's three statuses (succeeded/failed/skipped) are exactly
	// the set that can satisfy a fan-out family's barrier for one child.
	// Parked-but-alive statuses (stopped, awaiting_recovery,
	// awaiting_integration, needs_input) are deliberately excluded: the
	// family keeps waiting until that child later reaches a real terminal
	// status.
	if to.IsTerminal() {
		s.trySettleFamilyIfChild(stepID)
	}
	return true
}

func (s *scheduler) transitionEvent(stepID string, from, to step.Status, recoveryAction string) StepStatus {
	state := s.states[stepID]
	state.Status = to
	at := time.Now().UTC()
	state.Timing.Apply(to, at)
	timing := state.Timing
	ev := StepStatus{
		At:             at,
		Timing:         &timing,
		RunID:          s.runID,
		StepID:         stepID,
		From:           from,
		To:             to,
		Attempt:        state.Attempt,
		Iteration:      state.Iteration,
		Generation:     state.Generation,
		RecoveryAction: recoveryAction,
	}
	// Carry the failure reason and subtype so the TUI can surface them without
	// re-reading result.json. handle() guarantees state.Result.Err is populated
	// (executor error or gate detail) before a step transitions to Failed.
	if to == step.StatusFailed && state.Result != nil {
		ev.Err = state.Result.Err
		ev.Subtype = state.Result.Subtype
	}
	// Attach the step's cumulative cost/tokens so the monitor can display per-step
	// figures and a running total live (and on journal replay) without re-reading
	// result.json. Carried on every transition — not just the terminal one — so a
	// step re-running after a reset/retry keeps showing what earlier attempts
	// already cost instead of blanking to zero. Zero means "nothing spent yet".
	if state.SpentUSD > 0 {
		cost := state.SpentUSD
		ev.Cost = &cost
	}
	ev.Tokens = state.SpentTokens
	return ev
}

// emit writes an event to the journal (if a writer is configured), then fans
// it out to subscribers.  The journal write is synchronous and happens before
// any subscriber receives the event, preserving the "journal before fan-out"
// invariant: in-memory state is always fold(journal).
// emit is called only from the scheduler goroutine.
func (s *scheduler) emit(e Event) error {
	nextSeq := s.seq + 1
	if s.writer != nil {
		line, err := MarshalEnvelope(nextSeq, e)
		if err != nil {
			s.failJournal(err)
			return err
		}
		if err := s.writer.AppendLine(line, s.terminalManifest(e)); err != nil {
			s.failJournal(err)
			return err
		}
	}
	s.seq = nextSeq
	s.fanOut(e)
	return nil
}

func (s *scheduler) failJournal(err error) {
	if s.fatalJournalErr == nil {
		s.fatalJournalErr = err
	}
	if s.cancel != nil {
		s.cancel()
	}
}

// emitBatch persists a recovery transaction before exposing any member of the
// batch. It is used during Resume before runLoop starts, so returning an error
// lets the caller unwind scheduler registration and the run lock cleanly.
func (s *scheduler) emitBatch(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	lines := make([][]byte, 0, len(events))
	nextSeq := s.seq
	for _, event := range events {
		nextSeq++
		line, err := MarshalEnvelope(nextSeq, event)
		if err != nil {
			return err
		}
		lines = append(lines, line)
	}
	if s.writer != nil {
		if err := s.writer.AppendBatch(lines); err != nil {
			return err
		}
	}
	s.seq = nextSeq
	for _, event := range events {
		s.fanOut(event)
	}
	return nil
}

func (s *scheduler) terminalManifest(e Event) *manifest.StepTerminal {
	ss, ok := e.(StepStatus)
	if !ok || !ss.To.IsTerminal() {
		return nil
	}
	state := s.states[ss.StepID]
	term := &manifest.StepTerminal{StepID: ss.StepID, Status: string(ss.To), Attempt: state.Attempt}
	if state.Result != nil {
		term.TotalCostUSD = state.Result.TotalCostUSD
		term.Result = state.Result
	}
	if wfStep := s.stepByID(ss.StepID); wfStep != nil {
		term.Backend = wfStep.Backend
		term.Model = wfStep.Model
		if a := wfStep.ResolvedAgent(); a != nil {
			if c, ok := a.(agentcfg.ClaudeAgent); ok {
				term.ToolPolicy = append(append([]string{}, c.Tools...), c.DisallowedTools...)
			}
			term.AgentPosture = agentcfg.Posture(a)
		}
	}
	term.IntegrationCommit = s.stepCommits[ss.StepID]
	if diff := s.rt(ss.StepID).diff; diff != "" {
		term.DiffSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(diff)))
	}
	return term
}

func (s *scheduler) fanOut(e Event) {
	switch e.(type) {
	case StepOutput, StepToolCall, StepMessage:
		fanOutLive(s.subs, e)
	default:
		fanOutCtrl(s.subs, e)
		if s.observers != nil {
			s.observers.dispatchPublish(e)
		}
	}
}

// snapshot is the Memento pattern's originator method: only scheduler (which
// owns all of a run's mutable state) knows how to capture that state into a
// RunSnapshot memento. Callers (Manager, replay.go) never reach into
// scheduler fields directly — they call snapshot()/the exported Snapshot
// wrappers and treat the result as opaque.
func (s *scheduler) snapshot() RunSnapshot {
	states := make([]step.State, len(s.wf.Steps))
	allDone := true
	var totalCost float64
	var totalTokens int
	for i, wfStep := range s.wf.Steps {
		st := s.states[wfStep.ID]
		states[i] = *st
		if !st.Status.IsTerminal() {
			allDone = false
		}
		// Cumulative across every attempt (see step.State.SpentUSD), so a run that
		// retried or reset a step reports the full amount it actually paid.
		totalCost += st.SpentUSD
		totalTokens += st.SpentTokens
	}
	// Runtime fan-out children never appear in wf.Steps (the static DAG stays
	// pure), so they are folded in here, family by family in expansion order
	// and source order within each family, to keep RunSnapshot deterministic.
	// The family step itself is a zero-cost barrier (its own SpentUSD/Tokens
	// stay at zero, counted once above) — only its children add to the totals,
	// so nothing is double-counted.
	for _, familyID := range s.foreach.order {
		fam := s.foreach.families[familyID]
		if fam == nil {
			continue
		}
		for _, childID := range fam.Order {
			st, ok := s.states[childID]
			if !ok {
				continue
			}
			states = append(states, *st)
			if !st.Status.IsTerminal() {
				allDone = false
			}
			totalCost += st.SpentUSD
			totalTokens += st.SpentTokens
		}
	}
	return RunSnapshot{
		ID:           s.runID,
		Workflow:     s.wf.Meta.Name,
		Steps:        states,
		Done:         allDone,
		Failed:       s.anyFailed(),
		TotalCostUSD: totalCost,
		TotalTokens:  totalTokens,
	}
}

// fanOutLive is the Observer pattern's notify step for the live channel (see
// Subscribe): it pushes e to every registered observer's live channel,
// dropping for slow consumers. Live events (StepOutput, StepToolCall,
// StepMessage) are liveness signals only; the durable content lives in the
// per-step transcript on disk.
func fanOutLive(subs []sub, e Event) {
	for _, s := range subs {
		select {
		case s.live <- e:
		default:
		}
	}
}

// fanOutCtrl is the Observer pattern's notify step for the ctrl channel (see
// Subscribe): it pushes e to every registered observer's ctrl channel,
// dropping for slow consumers. Ctrl events are critical (RunFinished,
// ReviewRequest, etc.) but the ctrl channel is sized for worst-case workflow
// volume, so drops should not occur in practice.
func fanOutCtrl(subs []sub, e Event) {
	for _, s := range subs {
		select {
		case s.ctrl <- e:
		default:
		}
	}
}
