// Package engine drives workflow execution. One goroutine (the scheduler) owns
// all mutable state for a run; workers and the TUI communicate with it via its
// inbox channel. Every state transition is emitted as a typed Event, published
// to subscribers — the TUI is merely one.
package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/review"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// ── scheduler messages ────────────────────────────────────────────────────────

// ── reporter ─────────────────────────────────────────────────────────────────

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
