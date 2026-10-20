// Package engine drives workflow execution. One goroutine (the scheduler) owns
// all mutable state for a run; workers and the TUI communicate with it via its
// inbox channel. Every state transition is emitted as a typed Event, published
// to subscribers — the TUI is merely one.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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

// runBranch is the run's integration branch (spec 06). Every field is "" on
// the persistence-off / non-git path, and integration keys off that.
type runBranch struct {
	name     string // per-run integration branch name
	worktree string // run worktree absolute path
	baseSHA  string // working-branch HEAD the run branch was rooted at
	base     string // user's working branch name (final-merge target)
}

// setupRunBranch creates the per-run integration branch at the working-branch
// HEAD and a run worktree checked out on it, recorded on the scheduler. It runs
// once at run start on the scheduler goroutine, before the dispatch loop.
//
// Persistence-off / non-git (repoRoot == "") is a first-class no-op: runBranch
// and runWorktree stay "" and every downstream integration step keys off that,
// so steps run in place exactly as before spec 06.
func (s *scheduler) setupRunBranch() error {
	if s.repoRoot == "" {
		return nil
	}
	// repoRoot can be set for persistence without the parent actually being a git
	// work tree (e.g. a plain temp dir in tests, or jig run outside a repo). That
	// is the non-git case, which spec 06 designates a first-class no-op: no run
	// branch, steps run in place. Probe for a real HEAD and degrade rather than
	// failing the run. The only hard error left is a genuine worktree-add failure
	// on a valid repo.
	head, err := currentHEAD(s.repoRoot)
	if err != nil {
		return nil
	}
	branch := runBranchName(s.wf.Meta.Name, s.runID)
	// createWorktree does `git worktree add -B <branch> <path>`, which creates the
	// branch at the repo's current HEAD (the user's working-branch HEAD) and checks
	// it out in a fresh worktree — exactly the run-branch root spec 06 requires.
	wtPath := filepath.Join(s.jigRoot, "worktrees", s.runID, "_run")
	if _, err := createWorktree(s.repoRoot, wtPath, branch); err != nil {
		return err
	}
	s.branch.name = branch
	s.branch.worktree = wtPath
	s.branch.baseSHA = head
	// Record the user's working branch as the final-merge target (spec 06 A3).
	// A detached HEAD yields "HEAD" here; the final merge still lands onto whatever
	// repoRoot has checked out, so the name is informational for the gate.
	if out, berr := gitCmd(s.repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); berr == nil {
		s.branch.base = strings.TrimSpace(out)
	}
	return nil
}

// cleanupWorktrees removes all active step worktrees (and their ephemeral
// per-run branches) plus the run worktree. It is idempotent: it clears
// s.worktrees and s.branch.worktree as it goes, so a second call is a no-op.
// Callers must invoke this BEFORE emitting RunFinished so that the git
// subprocesses finish before the caller's goroutine signals completion.
func (s *scheduler) cleanupWorktrees() {
	s.releaseExecutionWorkspaces()
	if s.repoRoot == "" {
		return
	}
	// Best-effort: this also runs as the deferred safety net after RunFinished,
	// when no further event may be journaled. Leftovers are reclaimed by the
	// stale worktree retry in createWorktreeAt.
	for stepID, wt := range s.worktrees {
		_ = removeWorktree(s.repoRoot, wt.path)
		_, _ = gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(stepID))
		delete(s.worktrees, stepID)
	}
	// Remove the run worktree but KEEP the run branch: it is the run's
	// integration history, left for inspection and for the final merge gate.
	if s.branch.worktree != "" {
		_ = removeWorktree(s.repoRoot, s.branch.worktree)
		s.branch.worktree = ""
	}
}

// stepBranchName returns the per-run, per-step branch name
// jig/<workflow>/<runID>/<stepID>. Including the runID (without the "run-"
// prefix used by run branches) mirrors the worktree filesystem layout and
// prevents concurrent runs of the same workflow from colliding on step
// branches. The "run-" prefix is intentionally absent so that git's ref
// storage does not conflict: run branch jig/<wf>/run-<id> is a ref file
// while step branches jig/<wf>/<id>/<step> live under a sibling directory.
// Both worktree creation and squash-merge use this function as the single
// source of truth.
func (s *scheduler) stepBranchName(stepID string) string {
	return "jig/" + sanitizeBranchName(s.wf.Meta.Name) + "/" + sanitizeBranchName(s.runID) + "/" + stepID
}

// structuredField decodes a completed step's structured output once and walks
// a declared field path. Inputs and review targets share it so both read the
// same value from the same dispatch-time result cache.
func (s *scheduler) structuredField(stepID string, fields []string) (any, error) {
	state := s.states[stepID]
	if state == nil || state.Result == nil {
		return nil, fmt.Errorf("step %q has no result", stepID)
	}
	m := s.rt(stepID).structured
	if m == nil {
		if len(state.Result.Structured) == 0 {
			return nil, fmt.Errorf("step %q has no structured output", stepID)
		}
		if err := json.Unmarshal(state.Result.Structured, &m); err != nil {
			return nil, fmt.Errorf("decode structured output for step %q: %w", stepID, err)
		}
		s.rt(stepID).structured = m
	}
	var value any = m
	for _, field := range fields {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("step %q field %q is not an object", stepID, strings.Join(fields, "."))
		}
		var found bool
		value, found = object[field]
		if !found {
			return nil, fmt.Errorf("step %q has no field %q", stepID, strings.Join(fields, "."))
		}
	}
	return value, nil
}

// handle processes one message from the scheduler's inbox by delegating to
// its Command implementation — see command in commands.go.
func (s *scheduler) handle(msg schedMsg) {
	msg.(command).execute(s)
}

// applyFailurePolicy marks the step failed (or retries it) according to its
// on_failure policy.  Called from handle(stepDoneMsg) after exec or gate failure.
func (s *scheduler) applyFailurePolicy(stepID string, wfStep *workflow.Step) {
	policy := workflow.FailAbort // default
	if wfStep != nil && wfStep.OnFailure != "" {
		policy = wfStep.OnFailure
	}

	from := s.states[stepID].Status

	strat, ok := failurePolicyStrategies[policy]
	if !ok {
		strat = abortFailureStrategy{} // unrecognised policy — same fallback as before
	}
	strat.apply(s, stepID, wfStep, from)
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
	if s.rt(stepID).sessionResumeInFlight {
		// Spec 20 D19: a failed Recover(resume) must not keep advertising resume
		// of a likely-dead backend session.
		s.rt(stepID).failedSessionResume = true
		s.rt(stepID).sessionResumeInFlight = false
	}
	if !s.transition(stepID, state.Status, step.StatusAwaitingRecovery) {
		return
	}
	s.emit(RecoveryRequest{
		RunID:     s.runID,
		StepID:    stepID,
		Err:       state.Result.Err,
		CanResume: s.stepCanResume(stepID, state.Result.SessionID),
	})
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

// handleReset rewinds the run to before targetID's dependency closure (spec 08
// C2). It is only valid on an unfinished, quiescent run; settled runs and runs
// with a live worker are silent no-ops. The reset is a clean single-writer
// mutation: no lock is needed because the scheduler goroutine owns all state.
//
// Ordering is crash-consistent: the StepsReset audit event and the
// StepStatus(→pending) transitions are journaled *before* any destructive git
// or file operation. A crash after journaling leaves the journal (showing
// pending, no expected output) consistent with the deleted files.
func (s *scheduler) handleReset(m resetMsg) {
	reply := func(result ResetResult, err error) {
		if m.reply != nil {
			m.reply <- resetReply{result: result, err: err}
		}
	}
	// Guard: only on an unfinished, quiescent run with git persistence.
	if s.terminated {
		reply(ResetResult{}, &ResetError{Code: "settled", Target: m.stepID, Err: fmt.Errorf("run is already settled")})
		return
	}
	if s.slots.inFlight > 0 {
		reply(ResetResult{}, &ResetError{Code: "in_flight", Target: m.stepID, Err: fmt.Errorf("run has %d worker(s) in flight", s.slots.inFlight)})
		return
	}
	if s.branch.worktree == "" {
		reply(ResetResult{}, &ResetError{Code: "persistence_required", Target: m.stepID, Err: fmt.Errorf("run has no git worktree")})
		return
	}
	// A runtime fan-out child is not an independently resettable unit — it is
	// replaced wholesale on the family's next expansion. Point the operator at
	// the family instead of silently no-oping or resetting just one instance
	// out from under its siblings (docs/plans/a8-dynamic-foreach-fan-out.md,
	// "Reset, routes, worktrees, and budgets").
	if childState := s.states[m.stepID]; childState != nil && childState.ParentID != "" {
		reply(ResetResult{}, &ResetError{Code: "fanout_child", Target: m.stepID,
			Err: fmt.Errorf("%q is a fan-out child of %q; reset %q instead", m.stepID, childState.ParentID, childState.ParentID)})
		return
	}

	closure := s.closureOf(m.stepID)
	if len(closure) == 0 {
		reply(ResetResult{}, &ResetError{Code: "unknown_target", Target: m.stepID, Err: fmt.Errorf("step does not exist")})
		return
	}
	// closureOf/ResetClosure stay pure static-DAG operations (used for
	// operator previews); the actual scheduler-level reset must additionally
	// invalidate every family's current children — journal transitions,
	// artifact cleanup, worktree removal, and commit rewind all operate on
	// this expanded set.
	expanded := s.expandFanOutClosure(closure)

	rewindTo, survivors := s.rewindPlan(m.stepID)
	result := ResetResult{Target: m.stepID, Closure: append([]string(nil), expanded...), RewindTo: rewindTo}
	// A restored scheduler must not dispatch pending work between journal
	// invalidation and successful Git/artifact cleanup.
	s.restoredHold = true

	// Journal the audit event and StepStatus(→pending) transitions BEFORE any
	// destructive operation. A crash after this point leaves the journal in a
	// state consistent with the pending/empty-output files that follow.
	if err := s.emit(StepsReset{
		RunID:    s.runID,
		Target:   m.stepID,
		Closure:  expanded,
		RewindTo: rewindTo,
	}); err != nil {
		reply(ResetResult{}, &ResetError{Code: "journal", Target: m.stepID, Err: err})
		return
	}
	for _, id := range expanded {
		state := s.states[id]
		if state == nil {
			continue
		}
		if !s.transition(id, state.Status, step.StatusPending) {
			reply(ResetResult{}, &ResetError{Code: "journal", Target: m.stepID, Err: fmt.Errorf("persist pending transition for step %q", id)})
			return
		}
	}

	// Rewind the run branch and replay independent survivors.
	if rewindTo != "" {
		if out, err := gitCmd(s.branch.worktree, "reset", "--hard", rewindTo); err != nil {
			s.emit(RunError{RunID: s.runID,
				Err: fmt.Sprintf("reset: git reset --hard %s: %v — %s", rewindTo, err, strings.TrimSpace(out))})
			reply(ResetResult{}, &ResetError{Code: "git_reset", Target: m.stepID, Err: fmt.Errorf("git reset --hard %s: %w: %s", rewindTo, err, strings.TrimSpace(out))})
			return
		}
		for _, sha := range survivors {
			out, err := gitCmd(s.branch.worktree, "cherry-pick", sha)
			if err != nil {
				// Conflict: abort the cherry-pick and leave the run parked. The
				// conflicted paths are surfaced via IntegrationConflictRequest,
				// reusing the gate from Foundation A (spec 06 A2).
				_, _ = gitCmd(s.branch.worktree, "cherry-pick", "--abort")
				paths := mergeConflictPaths(s.branch.worktree)
				s.emit(IntegrationConflictRequest{RunID: s.runID, StepID: m.stepID, Paths: paths})
				s.emit(RunError{RunID: s.runID,
					Err: fmt.Sprintf("reset: cherry-pick %s: conflict — %s", sha, strings.TrimSpace(out))})
				reply(ResetResult{}, &ResetError{Code: "survivor_conflict", Target: m.stepID, Err: fmt.Errorf("cherry-pick %s: %w: %s", sha, err, strings.TrimSpace(out))})
				return
			}
		}
	}

	// Clear per-step derived outputs for the closure (result.json / output.*),
	// including every current fan-out child's own outputs. transcript.jsonl is
	// intentionally kept — the re-run appends a new generation. A family's own
	// fanout/generation-*.json manifests are untouched: ClearStepOutputs only
	// removes the fixed per-step artifact paths, never that subdirectory, so
	// older-generation manifests remain as immutable history.
	for _, id := range expanded {
		if err := s.runDir.Step(id).ClearOutputs(); err != nil {
			reply(ResetResult{}, &ResetError{Code: "artifact_cleanup", Target: m.stepID, Err: err})
			return
		}
	}

	// Reset in-memory state for each closure step (and every current fan-out
	// child) and purge stale routing maps.
	for _, id := range expanded {
		state := s.states[id]
		if state == nil {
			continue
		}
		state.Generation++
		state.Attempt = 0
		state.Iteration = 0
		state.Result = nil
		// status was already set to pending by the transition loop above

		// Remove the step worktree so re-dispatch creates a fresh one rooted at
		// the (now-rewound) run branch HEAD. Leaving a stale worktree would cause
		// squash-merge to diverge from the reset state.
		if wt, ok := s.worktrees[id]; ok {
			if err := removeWorktree(s.repoRoot, wt.path); err != nil {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("reset %q: remove worktree %s: %v", id, wt.path, err)})
			}
			if out, err := gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(id)); err != nil {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("reset %q: delete branch %s: %v — %s", id, s.stepBranchName(id), err, strings.TrimSpace(out))})
			}
			delete(s.worktrees, id)
		}

		delete(s.stepCommits, id)
		s.runtime[id] = s.rt(id).afterReset()
		delete(s.reviewSessions, id)
	}
	// Remove the current expansion from runtime scheduling for every family in
	// the (static) closure — a fresh FanOutExpanded/manifest is written the
	// next time the family becomes dependency-ready and dispatches (see
	// dispatchForEach). The now-orphaned child ids remain in s.states/
	// fanOut.children/fanOut.items as harmless historical residue (never
	// dispatched again, since they no longer appear in any family's Order) —
	// deleting the family entry itself is what stops RunSnapshot from folding
	// the just-reset children back in before the next expansion exists.
	for _, id := range closure {
		delete(s.foreach.families, id)
	}
	s.restoredHold = false
	reply(result, nil)
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
		s.restoredHold = false
		s.aborted = true
		s.transition(m.stepID, state.Status, step.StatusFailed)
		s.cancel()

	case RecoverSkip:
		// Accept the failure and continue past it. The step stays failed, but
		// skippedByOperator marks it transparent so depsReady/anyPendingRunnable
		// treat it like an on_failure="continue" node — dependents proceed and the
		// run keeps going. No worker is re-dispatched; the recovery loop ends here.
		s.rt(m.stepID).skippedByOperator = true
		s.restoredHold = false
		s.transitionRecovery(m.stepID, state.Status, step.StatusFailed, RecoverSkip)

	case RecoverRetry, RecoverResume:
		if s.rt(m.stepID).recoverRounds >= maxRecoverRounds {
			s.emit(RunError{
				RunID: s.runID,
				Err:   fmt.Sprintf("step %q: exceeded maximum recovery rounds (%d)", m.stepID, maxRecoverRounds),
			})
			return // stay parked; the human can still abort
		}
		if m.action == RecoverResume {
			// Resume requires a captured session. Without one, the TUI should not
			// have offered resume; ignore rather than silently doing a fresh retry.
			if state.Result == nil || state.Result.SessionID == "" || !s.stepCanResume(m.stepID, state.Result.SessionID) {
				return
			}
			s.rt(m.stepID).resumeSession = state.Result.SessionID
			s.rt(m.stepID).message = composeRecoveryMessage(state.Result.Err, m.text)
		} else {
			// Spec 20 D20: fresh retry must not resume a stale backend session.
			s.rt(m.stepID).resumeSession = ""
			s.rt(m.stepID).message = ""
			s.rt(m.stepID).failedSessionResume = false
			if state.Result != nil {
				state.Result.SessionID = ""
			}
			if err := s.runDir.Step(m.stepID).ClearSession(); err != nil {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: clear session before retry: %v", m.stepID, err)})
				return
			}
			if err := s.discardMutationWorkspace(m.stepID); err != nil {
				s.emit(RunError{RunID: s.runID, Err: err.Error()})
				return
			}
		}
		s.rt(m.stepID).recoverRounds++
		state.Attempt++
		s.restoredHold = false
		s.transition(m.stepID, state.Status, step.StatusPending)
	}
}

func (s *scheduler) discardMutationWorkspace(stepID string) error {
	path := s.worktrees[stepID].path
	if path == "" {
		return nil
	}
	expected := filepath.Clean(filepath.Join(s.jigRoot, "worktrees", s.runID, stepID))
	if s.jigRoot == "" || filepath.Clean(path) != expected {
		return fmt.Errorf("step %q: refuse to discard unexpected worktree path %q", stepID, path)
	}
	leafInfo, leafErr := os.Lstat(path)
	if leafErr == nil && leafInfo.Mode()&os.ModeSymlink != 0 {
		// Never pass a leaf symlink to git worktree remove: git may resolve it to
		// a different registered tree. Remove only the canonical-path residue.
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("step %q: discard worktree symlink before retry: %w", stepID, err)
		}
		_, _ = gitCmd(s.repoRoot, "worktree", "prune")
	} else if leafErr != nil && !os.IsNotExist(leafErr) {
		return fmt.Errorf("step %q: inspect worktree before retry: %w", stepID, leafErr)
	} else if err := removeWorktree(s.repoRoot, path); err != nil {
		// A crash can leave a directory at the canonical path without valid git
		// worktree registration. Fresh retry explicitly discards that residue.
		info, statErr := os.Stat(path)
		switch {
		case statErr == nil && info.IsDir():
			if removeErr := os.RemoveAll(path); removeErr != nil {
				return fmt.Errorf("step %q: discard invalid worktree before retry: %w", stepID, removeErr)
			}
		case statErr != nil && !os.IsNotExist(statErr):
			return fmt.Errorf("step %q: inspect invalid worktree before retry: %w", stepID, statErr)
		}
		_, _ = gitCmd(s.repoRoot, "worktree", "prune")
	}
	delete(s.worktrees, stepID)
	s.rt(stepID).diff = ""
	// createWorktreeAt uses -B and therefore resets any surviving branch ref to
	// the current run tip. Branch deletion is only cleanup, not correctness.
	_, _ = gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(stepID))
	return nil
}

// handleResolveIntegration applies a human decision to a step parked in
// step.StatusAwaitingIntegration after a squash-merge conflict (spec 06 A2).
//
//   - resolve (abort=false): the operator merged the conflict in the run
//     worktree; the engine finishes the integration by committing the resolved
//     tree with the jig-step trailer, records stepCommits, and succeeds the step.
//   - abort (abort=true): discard the half-applied squash and fail the step,
//     routing it to the recovery gate via applyFailurePolicy.
func (s *scheduler) handleResolveIntegration(m resolveIntegrationMsg) {
	state := s.states[m.stepID]
	if state == nil || state.Status != step.StatusAwaitingIntegration {
		return // stale or duplicate
	}
	if s.rt(m.stepID).resolvingSeq != 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent conflict resolution in progress; wait for it before resolving or aborting", m.stepID)})
		return
	}
	s.restoredHold = false

	if m.abort {
		// Discard the conflicted/staged squash so the run worktree is clean, then
		// fail the step through the normal failure policy (→ recovery gate).
		if out, err := gitCmd(s.branch.worktree, "reset", "--hard"); err != nil {
			// The run worktree still holds the conflicted squash; failing the step
			// now would let the next integration build on it. Stay parked.
			s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("abort integration for %q: reset run worktree: %v — %s", m.stepID, err, strings.TrimSpace(out))})
			return
		}
		res := s.states[m.stepID].Result
		if res == nil {
			res = &step.Result{}
			s.states[m.stepID].Result = res
		}
		res.Status = step.StatusFailed
		if res.Err == "" {
			res.Err = fmt.Sprintf("integration conflict for step %q aborted by operator", m.stepID)
		}
		s.applyFailurePolicy(m.stepID, s.stepByID(m.stepID))
		return
	}

	// Resolve: refuse while unmerged paths remain — the operator must `git add`
	// their resolution first (standard git conflict resolution).
	if len(mergeConflictPaths(s.branch.worktree)) > 0 {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q: unresolved conflicts remain in the run worktree", m.stepID),
		})
		return // stay parked
	}
	if out, err := gitCmd(s.branch.worktree, "add", "-A"); err != nil {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("stage resolved conflict for %q: %v — %s", m.stepID, err, strings.TrimSpace(out)),
		})
		return
	}
	msg := m.stepID + "\n\njig-step: " + m.stepID
	if out, err := gitCmd(s.branch.worktree, "commit", "-m", msg); err != nil {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("commit resolved conflict for %q: %v — %s", m.stepID, err, strings.TrimSpace(out)),
		})
		return // stay parked
	}
	if sha, err := currentHEAD(s.branch.worktree); err == nil {
		s.stepCommits[m.stepID] = sha
	}
	wfStep := s.stepByID(m.stepID)
	s.transition(m.stepID, step.StatusAwaitingIntegration, step.StatusSucceeded)
	if wfStep != nil {
		s.recordRoutes(m.stepID, wfStep, "")
	}
}

// handleResolveIntegrationWithAgent starts the agent conflict resolver for a
// step parked on an integration conflict. The resolver runs an agent, so it
// executes off the scheduler goroutine under the run context; its outcome
// returns as an integrationResolvedMsg. The step stays AwaitingIntegration
// throughout, so a crash mid-resolution reopens on the same gate.
func (s *scheduler) handleResolveIntegrationWithAgent(m resolveIntegrationWithAgentMsg) {
	state := s.states[m.stepID]
	st := s.stepByID(m.stepID)
	if state == nil || state.Status != step.StatusAwaitingIntegration || s.resolver == nil || !hasAgentSession(st) {
		return
	}
	if s.rt(m.stepID).resolvingSeq != 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent conflict resolution already in progress", m.stepID)})
		return
	}
	s.restoredHold = false
	paths := mergeConflictPaths(s.branch.worktree)
	if len(paths) == 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: no unresolved integration conflict remains", m.stepID)})
		return
	}
	s.integrationSeq++
	seq := s.integrationSeq
	s.rt(m.stepID).resolvingSeq = seq
	ctx, resolver, inbox := s.runCtx, s.resolver, s.inbox
	req := IntegrationResolutionRequest{RunID: s.runID, Step: st, Worktree: s.branch.worktree, Conflicts: paths}
	go func() {
		result, err := resolver.ResolveIntegration(ctx, req, conflictReporter{})
		select {
		case inbox <- integrationResolvedMsg{stepID: req.Step.ID, seq: seq, paths: paths, result: result, err: err}:
		case <-ctx.Done():
		}
	}()
}

// handleIntegrationResolved applies the agent conflict resolver's outcome. A
// result for a superseded request (reset or abort while the agent ran) is
// dropped.
func (s *scheduler) handleIntegrationResolved(m integrationResolvedMsg) {
	if s.rt(m.stepID).resolvingSeq != m.seq {
		return
	}
	s.rt(m.stepID).resolvingSeq = 0
	state := s.states[m.stepID]
	st := s.stepByID(m.stepID)
	if state == nil || state.Status != step.StatusAwaitingIntegration || st == nil {
		return
	}
	if m.err != nil || m.result == nil || m.result.Status == step.StatusFailed {
		detail := "agent conflict resolver failed"
		if m.err != nil {
			detail += ": " + m.err.Error()
		} else if m.result != nil && m.result.Err != "" {
			detail += ": " + m.result.Err
		}
		s.emit(RunError{RunID: s.runID, Err: detail})
		return
	}
	if unresolved := mergeConflictPaths(s.branch.worktree); len(unresolved) > 0 {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent left unresolved conflicts: %s", m.stepID, strings.Join(unresolved, ", "))})
		return
	}
	if len(st.MutationPaths) > 0 {
		changed := strings.Fields(strings.TrimSpace(gitOutput(s.branch.worktree, "diff", "--name-only", "HEAD")))
		for _, path := range changed {
			if !mutationPathAllowed(path, st.MutationPaths) {
				s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("step %q: agent changed %q outside mutation_paths", m.stepID, path)})
				return
			}
		}
	}
	s.emit(s.integrationConflictRequest(m.stepID, m.paths, "agent proposal staged; review the diff, then finalize"))
}

func gitOutput(dir string, args ...string) string {
	out, _ := gitCmd(dir, args...)
	return out
}

type conflictReporter struct{}

func (conflictReporter) Output(string)           {}
func (conflictReporter) ToolCall(string, string) {}
func (conflictReporter) Message(int, int)        {}
func (conflictReporter) Finding(SecurityFinding) {}
func (conflictReporter) Question(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
	return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionCancel}
}

// requestFinalMergeIfNeeded presents the final-merge gate the first time the run
// reaches terminal with a non-empty run branch (spec 06 A3), returning true to
// keep the run parked-but-alive. It returns false — finish normally — on the
// persistence-off / non-git path, when the run failed (nothing clean to land), or
// when the run branch gained no commits beyond its base. Idempotent: once the
// gate is emitted it keeps returning true until Run.FinalMerge settles the run.
func (s *scheduler) requestFinalMergeIfNeeded() bool {
	if s.awaitingFinalMerge {
		return true // gate already presented; still waiting on the operator
	}
	if s.branch.name == "" || s.branch.worktree == "" || s.anyFailed() {
		return false
	}
	head, err := currentHEAD(s.branch.worktree)
	if err != nil || head == s.branch.baseSHA {
		return false // run branch is empty — nothing to merge
	}
	s.awaitingFinalMerge = true
	s.emit(FinalMergeRequest{RunID: s.runID, RunBranch: s.branch.name, Base: s.branch.base})
	return true
}

// handleFinalMerge applies the operator's final-merge decision (spec 06 A3).
// Approve merges the run branch onto the base working branch; discard leaves the
// run branch in place. Either outcome settles the run: it emits RunFinished and
// sets terminated so run()'s loop returns. This is a pre-RunFinished completion
// step — there is deliberately no RunResumed and no post-finish re-entry.
func (s *scheduler) handleFinalMerge(m finalMergeMsg) {
	if !s.awaitingFinalMerge {
		return // stale or duplicate
	}
	if m.approve {
		conflict, err := finalMerge(s.repoRoot, s.branch.base, s.branch.name)
		if err != nil {
			s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("final merge: %v", err)})
			return // stay parked; the operator can retry or discard
		}
		if conflict {
			// The run branch conflicts with concurrent work on the base. finalMerge
			// aborted the half-applied merge, so the working tree is clean; surface it
			// and stay parked so the operator can discard (or fix base and re-approve).
			s.emit(RunError{
				RunID: s.runID,
				Err:   fmt.Sprintf("final merge conflicts with %s; run branch %s left for manual merge", s.branch.base, s.branch.name),
			})
			return
		}
	}
	// Approved-and-merged, or discarded: the run settles here.
	s.awaitingFinalMerge = false
	s.terminated = true
	s.cleanupWorktrees()
	s.emit(RunFinished{RunID: s.runID, Failed: s.anyFailed(), Cause: s.terminalCause()})
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

// composeRecoveryMessage builds the resume prompt for RecoverResume: the failed
// step's captured error plus any operator guidance, framed so the agent revisits
// its approach instead of repeating the mistake. Crash reopen uses a distinct
// preamble so the agent knows the process died mid-flight (Spec 20).
func composeRecoveryMessage(errText, guidance string) string {
	var b strings.Builder
	if strings.TrimSpace(errText) == processInterruptedErr {
		b.WriteString("The jig process exited while this step was running (")
		b.WriteString(processInterruptedErr)
		b.WriteString("). Partial transcript may exist on disk. Continue from the prior agent session; do not redo completed work unless necessary.\n\n")
	} else {
		b.WriteString("Your previous attempt failed with this error:\n\n")
		if strings.TrimSpace(errText) != "" {
			b.WriteString(errText)
		} else {
			b.WriteString("(no error detail was captured)")
		}
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(guidance) != "" {
		b.WriteString("Additional guidance from the operator:\n")
		b.WriteString(guidance)
		b.WriteString("\n\n")
	}
	b.WriteString("Review what went wrong and take a different approach to complete the task.")
	return b.String()
}

// runGate evaluates the [step.validate] block for wfStep synchronously.
// Returns (true, detail) on pass, (false, detail) on failure.
// executionDir, when non-empty, is the dispatch snapshot used by gate commands
// and relative output checks. It is a mutation worktree for writers and a
// read-only execution view for readers.
func (s *scheduler) runGate(wfStep *workflow.Step, executionDir string) (bool, string) {
	v := wfStep.Validate

	// Command gate: run via sh -c, check exit code 0.
	if v.Command != "" {
		cmd := exec.Command("sh", "-c", v.Command)
		if executionDir != "" {
			cmd.Dir = executionDir
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
		outputPath, err := gateOutputPath(executionDir, wfStep.Output)
		if err != nil {
			return false, fmt.Sprintf("output_exists gate: resolve output: %v", err)
		}
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
		outputPath, err := gateOutputPath(executionDir, wfStep.Output)
		if err != nil {
			return false, fmt.Sprintf("output_contains gate: resolve output: %v", err)
		}
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

func gateOutputPath(executionDir, output string) (string, error) {
	if output == "" {
		return "", nil
	}
	return workflow.ExecutionPath(executionDir, output)
}

// evalGuard evaluates a when-guard condition against the current step results.
// The condition was syntactically and semantically validated at load time, so
// missing steps or bad field paths return false (treat as "not yet ready").
func (s *scheduler) evalGuard(cond *workflow.Condition) bool {
	if cond == nil {
		return false
	}
	return s.evalGuardExpr(cond.Root)
}

func (s *scheduler) evalGuardExpr(expr *workflow.ConditionExpr) bool {
	if expr == nil {
		return false
	}
	switch expr.Op {
	case workflow.CondAnd:
		return s.evalGuardExpr(expr.Left) && s.evalGuardExpr(expr.Right)
	case workflow.CondOr:
		return s.evalGuardExpr(expr.Left) || s.evalGuardExpr(expr.Right)
	}
	value, fieldType, scalarKind, ok := s.resolveGuardValue(expr.Ref)
	if !ok {
		return false
	}
	if expr.Op == workflow.CondTruthy {
		actual, ok := value.(bool)
		return ok && actual
	}
	return compareGuardValue(value, fieldType, scalarKind, expr.Op, expr.Literal)
}

func (s *scheduler) resolveGuardValue(ref workflow.ConditionRef) (any, workflow.FieldType, workflow.OutputKind, bool) {
	depState := s.states[ref.Step]
	if depState == nil || depState.Result == nil {
		return nil, "", "", false
	}
	producer := s.stepByID(ref.Step)
	if len(ref.Field) == 0 {
		if producer == nil {
			return nil, "", "", false
		}
		switch producer.OutputType.Kind {
		case workflow.OutputBool:
			value, err := strconv.ParseBool(depState.Result.Verdict)
			return value, "", workflow.OutputBool, err == nil
		case workflow.OutputEnum:
			return depState.Result.Verdict, "", workflow.OutputEnum, true
		default:
			return nil, "", producer.OutputType.Kind, false
		}
	}

	m := s.rt(ref.Step).structured
	if m == nil && len(depState.Result.Structured) > 0 {
		if err := json.Unmarshal(depState.Result.Structured, &m); err == nil {
			s.rt(ref.Step).structured = m
		}
	}
	var cur any = m
	for _, seg := range ref.Field {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, "", "", false
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, "", "", false
		}
	}
	if producer == nil {
		return nil, "", "", false
	}
	field, ok := runtimeSchemaField(producer.ReferenceSchema(), ref.Field)
	if !ok {
		return nil, "", "", false
	}
	return cur, field.Type, "", true
}

// stepStringField reads a top-level string field from stepID's own structured
// output, populating the stepRuntime.structured cache the same way resolveGuardValue
// does. Used to surface the agent-authored `block_reason` base-schema field
// in a block_on gate without requiring a workflow-authored field reference.
func (s *scheduler) stepStringField(stepID, field string) string {
	depState := s.states[stepID]
	if depState == nil || depState.Result == nil {
		return ""
	}
	m := s.rt(stepID).structured
	if m == nil && len(depState.Result.Structured) > 0 {
		if err := json.Unmarshal(depState.Result.Structured, &m); err == nil {
			s.rt(stepID).structured = m
		}
	}
	value, _ := m[field].(string)
	return value
}

func runtimeSchemaField(schema *workflow.Schema, path []string) (*workflow.Field, bool) {
	if schema == nil {
		return nil, false
	}
	fields := schema.Fields
	var current *workflow.Field
	for _, segment := range path {
		current = nil
		for _, field := range fields {
			if field.Name == segment {
				current = field
				break
			}
		}
		if current == nil {
			return nil, false
		}
		fields = current.Fields
	}
	return current, current != nil
}

func compareGuardValue(value any, fieldType workflow.FieldType, scalarKind workflow.OutputKind, op workflow.CondOp, literal workflow.ConditionLiteral) bool {
	if scalarKind == workflow.OutputBool || fieldType == workflow.FieldBool {
		actual, ok := value.(bool)
		want, err := strconv.ParseBool(literal.Value)
		return ok && err == nil && compareOrderedBool(actual, want, op)
	}
	if scalarKind == workflow.OutputEnum || fieldType == workflow.FieldEnum || fieldType == workflow.FieldText {
		actual, ok := value.(string)
		return ok && compareOrderedString(actual, literal.Value, op)
	}
	if fieldType == workflow.FieldNumber {
		actual, ok := value.(float64)
		want, err := strconv.ParseFloat(literal.Value, 64)
		return ok && err == nil && compareOrderedNumber(actual, want, op)
	}
	if fieldType == workflow.FieldAny {
		switch actual := value.(type) {
		case string:
			if !literal.Quoted && (literal.Value == "true" || literal.Value == "false") {
				return false
			}
			if !literal.Quoted {
				if _, err := strconv.ParseFloat(literal.Value, 64); err == nil {
					return false
				}
			}
			return compareOrderedString(actual, literal.Value, op)
		case bool:
			want, err := strconv.ParseBool(literal.Value)
			return !literal.Quoted && err == nil && compareOrderedBool(actual, want, op)
		case float64:
			want, err := strconv.ParseFloat(literal.Value, 64)
			return !literal.Quoted && err == nil && compareOrderedNumber(actual, want, op)
		}
	}
	return false
}

func compareOrderedBool(actual, want bool, op workflow.CondOp) bool {
	switch op {
	case workflow.CondEq:
		return actual == want
	case workflow.CondNeq:
		return actual != want
	default:
		return false
	}
}

func compareOrderedString(actual, want string, op workflow.CondOp) bool {
	switch op {
	case workflow.CondEq:
		return actual == want
	case workflow.CondNeq:
		return actual != want
	default:
		return false
	}
}

func compareOrderedNumber(actual, want float64, op workflow.CondOp) bool {
	switch op {
	case workflow.CondEq:
		return actual == want
	case workflow.CondNeq:
		return actual != want
	case workflow.CondLT:
		return actual < want
	case workflow.CondLTE:
		return actual <= want
	case workflow.CondGT:
		return actual > want
	case workflow.CondGTE:
		return actual >= want
	default:
		return false
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

// closureOf returns the reset set for targetID: the target itself plus every
// step that transitively depends on it, in workflow declaration order.
// Independent parallel branches (steps with no transitive dependency on
// targetID) are excluded — they are survivors in a subsequent rewindPlan call.
func (s *scheduler) closureOf(targetID string) []string {
	if s.stepByID(targetID) == nil {
		return nil
	}
	return s.graph.inOrder(s.graph.descendants(nil, targetID))
}

// ResetClosure returns target plus every transitive dependent in workflow
// declaration order. It performs no scheduler, filesystem, lock, or Git work.
func ResetClosure(wf *workflow.Workflow, targetID string) []string {
	if wf == nil || targetID == "" {
		return nil
	}
	found := false
	for i := range wf.Steps {
		if wf.Steps[i].ID == targetID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	g := newStepGraph(wf)
	return g.inOrder(g.descendants(nil, targetID))
}

// expandFanOutClosure returns closure with every foreach family member
// replaced by itself plus its current runtime children (in source order),
// immediately after itself. closureOf/ResetClosure stay pure static-DAG
// operations for operator previews (docs/plans/a8-dynamic-foreach-fan-out.md,
// "Reset, routes, worktrees, and budgets"); this expansion happens only here,
// at the point scheduler-level reset performs destructive work — journal
// transitions, artifact cleanup, worktree removal, and commit rewind must all
// account for the family's actual current children, not just its declared id.
func (s *scheduler) expandFanOutClosure(closure []string) []string {
	out := make([]string, 0, len(closure))
	for _, id := range closure {
		out = append(out, id)
		if fam := s.foreach.families[id]; fam != nil {
			out = append(out, fam.Order...)
		}
	}
	return out
}

// rewindPlan computes the git operations needed to reset the run branch for a
// reset of targetID. It returns:
//
//   - rewindTo: the run-branch commit the caller should "git reset --hard" to
//     (the commit just before the earliest closure commit). Empty string when
//     there is nothing to rewind (no git repo, or no closure step has a commit).
//   - survivors: the ordered list of run-branch commit SHAs that are NOT in the
//     closure but sit after the rewind point; the caller cherry-picks these back
//     after the reset to preserve independent parallel branches.
func (s *scheduler) rewindPlan(targetID string) (rewindTo string, survivors []string) {
	if s.branch.worktree == "" || len(s.stepCommits) == 0 {
		return "", nil
	}

	// Every child integration commit belongs to its family for rewind purposes
	// (docs/plans/a8-dynamic-foreach-fan-out.md, "rewindPlan must treat every
	// child integration commit as belonging to its family"), so reset removes
	// all affected child commits together with the family's own and correctly
	// replays independent survivors in original run-branch order.
	closure := s.expandFanOutClosure(s.closureOf(targetID))
	closureSet := make(map[string]bool, len(closure))
	for _, id := range closure {
		closureSet[id] = true
	}

	// Collect the SHA of every closure step that has a commit on the run branch.
	closureSHAs := make(map[string]bool)
	for id, sha := range s.stepCommits {
		if closureSet[id] {
			closureSHAs[sha] = true
		}
	}
	if len(closureSHAs) == 0 {
		return "", nil // all closure steps are read-only (no commits); nothing to rewind
	}

	// Walk the run-branch commits oldest-first. Using runBaseSHA..HEAD keeps
	// us in the run branch's own history without touching the user's branch.
	out, err := gitCmd(s.branch.worktree, "log", "--format=%H", "--reverse",
		s.branch.baseSHA+"..HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", nil
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			commits = append(commits, sha)
		}
	}

	// Find the earliest commit that belongs to the closure.
	firstIdx := -1
	for i, sha := range commits {
		if closureSHAs[sha] {
			firstIdx = i
			break
		}
	}
	if firstIdx < 0 {
		return "", nil
	}

	// Rewind to the commit just before the earliest closure commit.
	// If the earliest closure commit is the very first run-branch commit,
	// rewind all the way back to the base SHA (the run-branch root).
	if firstIdx == 0 {
		rewindTo = s.branch.baseSHA
	} else {
		rewindTo = commits[firstIdx-1]
	}

	// Survivors: run-branch commits after the rewind point that are not in the
	// closure. The caller cherry-picks them (in order) to restore independent work.
	for _, sha := range commits[firstIdx+1:] {
		if !closureSHAs[sha] {
			survivors = append(survivors, sha)
		}
	}
	return rewindTo, survivors
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
