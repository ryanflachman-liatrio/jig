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
	"sort"
	"strings"
	"sync"
	"time"

	"jig/internal/datastore"
	"jig/internal/manifest"
	"jig/internal/sentinel"
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
	mu       sync.Mutex
	runs     map[string]*Run
	exec     Executor
	root     string // .jig/ root (used in Phase 2+ for file I/O)
	subs     []sub  // manager-level fan-out; TUI subscribes once
	monitors []sentinel.MonitorDef
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

// SetMonitors registers Tier-2 monitor definitions that will be dispatched
// out-of-band for every run started by this manager. Call before Start.
func (m *Manager) SetMonitors(monitors []sentinel.MonitorDef) {
	m.monitors = monitors
}

// Root returns the .jig/ directory configured for this manager, or "" when
// persistence is disabled.
func (m *Manager) Root() string { return m.root }

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
	return datastore.ListRunIDs(m.root)
}

// RunSnapshot is a point-in-time summary of a run, safe to read from any goroutine.
type RunSnapshot struct {
	ID           string
	Workflow     string
	Steps        []step.State
	Done         bool
	Failed       bool
	TotalCostUSD float64 // sum of all steps' TotalCostUSD; 0.0 when none reported
	TotalTokens  int     // sum of all steps' token counts; 0 when none reported
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

	// Tier-2: start the supervisor out-of-band when monitors are configured and
	// persistence is on (runDir non-empty — transcripts exist to read).
	if len(m.monitors) > 0 && runDir != "" {
		secOn := wf.Defaults.Security.Enabled == nil || *wf.Defaults.Security.Enabled
		t2On := wf.Defaults.Security.Tier2Enabled == nil || *wf.Defaults.Security.Tier2Enabled
		if secOn && t2On {
			sigCh := make(chan sentinel.StepSignal, 128)

			// Bridge StepMessage liveness events from the live bus channel to the
			// supervisor's signal channel. Drops are safe: a missed signal only
			// delays the next flush; the supervisor re-reads from disk on the next
			// signal it does receive.
			liveCh, _ := m.Subscribe()
			go func() {
				defer close(sigCh)
				for {
					select {
					case <-ctx.Done():
						return
					case ev, ok := <-liveCh:
						if !ok {
							return
						}
						if sm, ok := ev.(StepMessage); ok {
							select {
							case sigCh <- sentinel.StepSignal{
								RunID:     sm.RunID,
								StepID:    sm.StepID,
								Seq:       sm.Seq,
								Iteration: sm.Iteration,
							}:
							default:
							}
						}
					}
				}
			}()

			// notify converts a sentinel.Finding to an engine SecurityFinding event
			// and fans it out to all bus subscribers (TUI) and the scheduler inbox
			// (critical-finding escalation). Called from the supervisor goroutine.
			notify := func(f sentinel.Finding) {
				sf := SecurityFinding{
					RunID:       f.RunID,
					StepID:      f.StepID,
					Tier:        string(f.Tier),
					Monitor:     f.Monitor,
					Severity:    string(f.Severity),
					Action:      string(f.Action),
					Fingerprint: f.Fingerprint,
				}
				fanOutCtrl(subs, sf)
				select {
				case inbox <- securityFindingMsg{sf: sf}:
				default:
				}
			}

			var sink *sentinel.Writer
			if fw, err := sentinel.NewWriter(datastore.FindingsPath(runDir)); err == nil {
				sink = fw
			}

			sup := sentinel.NewSupervisor(
				runID,
				sigCh,
				sink,
				m.monitors,
				wf.Defaults.Security.FleetBudgetUSD,
				func(stepID string) string {
					return datastore.TranscriptPath(runDir, stepID)
				},
				notify,
			)
			go sup.Run(ctx)
		}
	}

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

// Stop halts one running step's worker without ending the run (spec 07 B1). The
// step's partial work is preserved and it parks at step.StatusStopped; the run
// stays alive and becomes quiescent (no worker in flight). Stopping a step that
// is not currently running is a no-op. Resume the step with Run.Resume.
func (r *Run) Stop(stepID string) { r.inbox <- stopMsg{stepID: stepID} }

// Resume brings a step parked at step.StatusStopped back up (spec 07 B2). When
// the stopped step captured an SDK session id its agent session is continued
// with message as the new turn; otherwise the step restarts fresh (a documented
// degrade, since the SDK cannot always surface a session id at cancel time).
// Resuming a step that is not stopped is a no-op.
func (r *Run) Resume(stepID, message string) {
	r.inbox <- resumeMsg{stepID: stepID, message: message}
}

// ClosureOf returns the reset set for stepID — the step itself plus every
// step that transitively depends on it, in declaration order. This is the
// same set handleReset would invalidate, and the TUI uses it to compute the
// blast-radius count for the reset confirmation dialog. Returns nil when the
// run is already settled (the scheduler goroutine has exited).
func (r *Run) ClosureOf(stepID string) []string {
	select {
	case <-r.done:
		return nil
	default:
	}
	reply := make(chan []string, 1)
	select {
	case r.inbox <- closureReqMsg{stepID: stepID, reply: reply}:
		select {
		case cl := <-reply:
			return cl
		case <-r.done:
			return nil
		}
	case <-r.done:
		return nil
	}
}

// Reset rewinds the run branch to before stepID's transitive depends_on
// closure, replays independent survivor commits, returns the closure to pending,
// and bumps each reset step's Generation counter (spec 08 C2). Only valid on
// an unfinished, quiescent run (no worker in flight); settled runs and in-flight
// runs are silent no-ops. Persistence-off runs (no git) are also no-ops.
func (r *Run) Reset(stepID string) { r.inbox <- resetMsg{stepID: stepID} }

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
//   - RecoverSkip   — accept the failure and continue: the step stays failed but
//     is treated as a transparent on_failure="continue" node so its dependents
//     proceed. The run is NOT torn down. This is the release valve for a step
//     whose failure a human judges non-fatal (e.g. a missing optional tool).
//   - RecoverAbort  — fail the step and abort the run (the old default behaviour).
//
// text is optional guidance, used only by RecoverResume.
func (r *Run) Recover(stepID, action, text string) {
	r.inbox <- recoverMsg{stepID: stepID, action: action, text: text}
}

// ResolveIntegration delivers a human decision for a step parked in
// step.StatusAwaitingIntegration after a squash-merge conflict (spec 06 A2).
// abort=false finishes the integration from the operator-resolved run worktree;
// abort=true fails the step, routing it to the recovery gate.
func (r *Run) ResolveIntegration(stepID string, abort bool) {
	r.inbox <- resolveIntegrationMsg{stepID: stepID, abort: abort}
}

// FinalMerge delivers the operator's decision for the final-merge gate (spec 06
// A3): approve=true lands the run branch onto the base working branch; approve=
// false discards it, leaving the run branch in place. Either way the run settles
// afterward (no resume).
func (r *Run) FinalMerge(approve bool) {
	r.inbox <- finalMergeMsg{approve: approve}
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

type closureReqMsg struct {
	stepID string
	reply  chan<- []string
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
	RecoverSkip   = "skip"   // accept the failure and continue past it (like on_failure="continue")
	RecoverAbort  = "abort"  // fail the step and abort the run
)

// maxRecoverRounds bounds how many times a single step may be retried/resumed
// through the recovery gate, preserving jig's static termination guarantee even
// though the gate is human-driven. The human can always abort instead.
const maxRecoverRounds = 20

type recoverMsg struct {
	stepID string
	action string // RecoverRetry | RecoverResume | RecoverSkip | RecoverAbort
	text   string // optional guidance, used by RecoverResume
}

// resolveIntegrationMsg carries a human decision for a step parked on an
// integration conflict: resolve (finish the merge) or abort (fail the step).
type resolveIntegrationMsg struct {
	stepID string
	abort  bool
}

// finalMergeMsg carries the operator's decision for the final-merge gate (spec
// 06 A3): approve lands the run branch onto the base, discard leaves it.
type finalMergeMsg struct {
	approve bool
}

// stopMsg asks the scheduler to stop one running step's worker without ending
// the run (spec 07 B1). handleStop cancels that step's child context only.
type stopMsg struct {
	stepID string
}

// resumeMsg asks the scheduler to resume a stopped step (spec 07 B2). When the
// step has a captured session id the agent session is continued with message;
// otherwise the step restarts fresh (documented degrade, not an error).
type resumeMsg struct {
	stepID  string
	message string
}

// resetMsg asks the scheduler to reset the run to targetID (spec 08 C2).
// handleReset rewinds the run branch to before the target's dependency closure,
// replays survivors, and returns the closure to pending for re-dispatch.
type resetMsg struct {
	stepID string
}

func (stepDoneMsg) isSchedMsg()            {}
func (verdictMsg) isSchedMsg()             {}
func (userInputMsg) isSchedMsg()           {}
func (snapshotReqMsg) isSchedMsg()         {}
func (closureReqMsg) isSchedMsg()          {}
func (humanMessageMsg) isSchedMsg()        {}
func (agentInputMsg) isSchedMsg()          {}
func (agentQuestionNotifyMsg) isSchedMsg() {}
func (agentQuestionAnswerMsg) isSchedMsg() {}
func (recoverMsg) isSchedMsg()             {}
func (resolveIntegrationMsg) isSchedMsg()  {}
func (finalMergeMsg) isSchedMsg()          {}
func (stopMsg) isSchedMsg()                {}
func (resumeMsg) isSchedMsg()              {}
func (resetMsg) isSchedMsg()               {}

// securityFindingMsg delivers a SecurityFinding to the scheduler inbox so the
// scheduler can escalate critical findings to the recovery gate without
// blocking the ctrl fan-out path.
type securityFindingMsg struct{ sf SecurityFinding }

func (securityFindingMsg) isSchedMsg() {}

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

// Finding routes a SecurityFinding through the ctrl channel (must-not-drop).
func (r *reporter) Finding(sf SecurityFinding) { r.ev(sf) }

// Question delivers an AskUserQuestion from the running agent to the scheduler,
// transitions the step to StatusNeedsInput, and blocks until the human answers
// via Run.AnswerQuestion. Runs in the executor goroutine.
func (r *reporter) Question(ctx context.Context, toolUseID string, questions []AgentQuestionItem) string {
	// answerCh is created at dispatch (scheduler goroutine); this goroutine only
	// reads it, so there is no cross-goroutine write to the field. Select on ctx so
	// a Stop, a run cancel, or a security escalation (all of which cancel the step's
	// context) unblocks this goroutine instead of parking it on answerCh forever.
	r.ev(AgentQuestion{ToolUseID: toolUseID, Questions: questions})
	select {
	case a := <-r.answerCh:
		return a
	case <-ctx.Done():
		return ""
	}
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
	// rerunSource maps a loop's goto target → the firing source step id. When a
	// coalesced rewind has several contributing loopers it names the first in
	// declaration order. It lets buildStepContext name why a step is re-running.
	// In-memory only, never persisted — it mirrors stepFeedback.
	rerunSource map[string]string

	// pendingLoops coalesces loop back-edges that target the same goto step
	// within one execution wave. A finishing looper records its intent here
	// instead of rewinding immediately; the rewind fires once, with the union of
	// every contributing looper's feedback, when the whole rewind body has
	// settled (loopBarrierReady). This makes parallel siblings that all loop to
	// the same step deterministic — no reset-collides-with-in-flight-worker, no
	// last-write-wins feedback clobber. Keyed by goto-target step id.
	pendingLoops map[string]*loopIntent
	aborted      bool // true when the run was explicitly aborted (loop cap, etc.)
	inFlight     int
	seq          int

	// Phase 5: worktree lifecycle.
	jigRoot    string            // .jig/ root; "" when persistence is disabled
	repoRoot   string            // git repo root (parent of jigRoot)
	worktrees  map[string]string // stepID → active worktree absolute path
	wtBaseSHAs map[string]string // stepID → HEAD SHA captured at worktree creation
	diffs      map[string]string // stepID → latest diff text (updated each execution)

	// Run-integration branch (spec 06). At run start (repoRoot != "") the
	// scheduler creates a run branch at the working-branch HEAD and a run
	// worktree checked out on it; each mutating step's worktree branches off the
	// run branch's current HEAD and squash-merges back as one commit. stepCommits
	// maps stepID → the sha of that step's squash commit, reconstructable from the
	// run branch's `jig-step:` trailers. All three stay "" / empty on the
	// persistence-off / non-git path, where steps run in place with no integration.
	runBranch   string            // per-run integration branch name; "" when persistence-off
	runWorktree string            // run worktree absolute path; "" when persistence-off
	runBaseSHA  string            // working-branch HEAD the run branch was rooted at
	baseBranch  string            // user's working branch name (final-merge target)
	stepCommits map[string]string // stepID → squash-merge commit sha on the run branch

	// Final-merge gate (spec 06 A3): a pre-RunFinished completion step. When the
	// run reaches terminal with a non-empty run branch, the scheduler emits one
	// FinalMergeRequest and parks (awaitingFinalMerge) instead of finishing;
	// Run.FinalMerge lands or discards, then the run settles. terminated is set by
	// the settling handler so run()'s loop returns after emitting RunFinished. No
	// RunResumed event exists — the run does not re-enter after it settles.
	awaitingFinalMerge bool
	terminated         bool

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

	// seenEscalations deduplicates critical-finding recovery escalations by
	// Fingerprint. A fingerprint that already triggered enterRecovery must not
	// trigger it again for the same step.
	seenEscalations map[string]bool

	// skippedByOperator marks steps a human chose to skip at the recovery gate
	// (RecoverSkip). The step is transitioned to StatusFailed, but unlike an
	// abort-policy failure it must NOT block its dependents: it is treated as a
	// transparent, on_failure="continue" node by depsReady and anyPendingRunnable.
	// This is the interactive equivalent of a static on_failure = "continue".
	skippedByOperator map[string]bool

	// skippedByGuard marks steps whose when= condition evaluated false. These
	// steps are optional branches: a dependent should not be blocked just because
	// an optional upstream node did not apply. They are transparent in depsReady,
	// cascadeSkip, and anyPendingRunnable.
	skippedByGuard map[string]bool

	// reporters holds the active reporter for each in-flight step so
	// agentQuestionAnswerMsg can route the answer to the correct channel.
	reporters map[string]*reporter

	// Per-step cancellation (spec 07 B1). Each dispatched worker gets its own
	// child context derived from the run context, its CancelFunc stored here keyed
	// by step id, so Run.Stop can cancel one worker without touching the run
	// context or its siblings. This deliberately reverses the single-run-context
	// assumption the engine started with (one context.WithCancel shared by every
	// worker): run-level cancellation still exists via s.cancel, but stop is
	// surgical. Entries are removed when the worker's stepDoneMsg is handled.
	stepCancels map[string]context.CancelFunc
	// stopping marks steps whose worker was cancelled by Run.Stop (not by failure
	// or run-level abort). When that worker's stepDoneMsg arrives it is routed to
	// StatusStopped — partial work captured, no failure policy, run stays alive —
	// rather than through the normal failure path.
	stopping map[string]bool

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
		pendingLoops:        make(map[string]*loopIntent),
		jigRoot:             jigRoot,
		repoRoot:            repoRoot,
		worktrees:           make(map[string]string),
		wtBaseSHAs:          make(map[string]string),
		diffs:               make(map[string]string),
		stepCommits:         make(map[string]string),
		pendingUserInputs:   make(map[string][]workflow.Input),
		collectedUserInputs: make(map[string][]ResolvedInput),
		preResolvedInputs:   make(map[string][]ResolvedInput),
		resumeSessions:      make(map[string]string),
		stepMessage:         make(map[string]string),
		reviewMessages:      make(map[string]int),
		stepInputCount:      make(map[string]int),
		recoverCount:        make(map[string]int),
		seenEscalations:     make(map[string]bool),
		skippedByOperator:   make(map[string]bool),
		skippedByGuard:      make(map[string]bool),
		reporters:           make(map[string]*reporter),
		stepCancels:         make(map[string]context.CancelFunc),
		stopping:            make(map[string]bool),
		postExecChain: []postExecHandler{
			phCaptureWorktreeDiff,
			phRunValidateGate,
			phCheckBlockOn,
			phSquashMergeIntegration,
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

	// Create the per-run integration branch + run worktree at the working-branch
	// HEAD (spec 06). Persistence-off / non-git (repoRoot == "") is a no-op: no
	// run branch, no run worktree, steps run in place. A git error here is a hard
	// setup failure — fail the run before any step burns work.
	if err := s.setupRunBranch(); err != nil {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("setup run branch: %v", err)})
		s.emit(RunFinished{RunID: s.runID, Failed: true})
		return
	}

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
		// 0. Fire any coalesced loop back-edges whose rewind body has fully
		//    settled. Runs before dispatch so a fired rewind's freshly-pending body
		//    steps are picked up in this same iteration.
		s.fireReadyLoops()

		// 1. Dispatch every ready step, respecting max_parallel.
		for s.inFlight < maxPar {
			st, ok := s.nextReady()
			if !ok {
				break
			}
			s.dispatch(ctx, st)
		}

		// 2. Terminal check: nothing running, nothing pending and runnable. Before
		//    finishing, present the final-merge gate (spec 06 A3) when the run branch
		//    carries commits — this is a pre-RunFinished completion step, so the run
		//    parks (does not emit RunFinished) until the operator lands or discards.
		if s.inFlight == 0 && !s.anyPendingRunnable() {
			if !s.requestFinalMergeIfNeeded() {
				s.cleanupWorktrees()
				s.emit(RunFinished{RunID: s.runID, Failed: s.anyFailed()})
				return
			}
			// Parked on the final-merge gate: fall through and block on the inbox.
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
				s.skippedByGuard[st.ID] = true
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
		// A guard-skipped dep is transparent when the dependent only uses it for
		// sequencing (no @ref input). If the dependent references output data from
		// the guard-skipped step it must still wait (and will be cascade-skipped).
		if depState.Status == step.StatusSkipped && s.skippedByGuard[depID] && !stepRefsDep(st, depID) {
			continue
		}
		// A failed dep is "ready" when it declared on_failure = "continue", or
		// when a human skipped it at the recovery gate (RecoverSkip) — both mean
		// "its failure does not block dependents."
		if depState.Status == step.StatusFailed {
			if s.skippedByOperator[depID] {
				continue
			}
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
			// Guard-skipped steps are transparent optional branches — dependents
			// should not be considered blocked just because the guard didn't fire.
			if !s.skippedByGuard[id] {
				blocked[id] = true
			}
		}
		if st.Status == step.StatusFailed {
			// An operator-skipped step (RecoverSkip) is transparent like a
			// continue-failed one — it must not seed the blocked set.
			if s.skippedByOperator[id] {
				continue
			}
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
		// A step parked on an integration conflict keeps the run alive: the human
		// will resolve the conflict in the run worktree or abort (spec 06 A2).
		if st.Status == step.StatusAwaitingIntegration {
			return true
		}
		// A deliberately-stopped step keeps the run alive and quiescent (spec 07
		// B1): the operator will resume it or (via Feature C) reset. Reaching zero
		// in-flight workers must not be read as end-of-run while a stop is parked.
		if st.Status == step.StatusStopped {
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
	s.runBranch = branch
	s.runWorktree = wtPath
	s.runBaseSHA = head
	// Record the user's working branch as the final-merge target (spec 06 A3).
	// A detached HEAD yields "HEAD" here; the final merge still lands onto whatever
	// repoRoot has checked out, so the name is informational for the gate.
	if out, berr := gitCmd(s.repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); berr == nil {
		s.baseBranch = strings.TrimSpace(out)
	}
	return nil
}

// cleanupWorktrees removes all active step worktrees (and their ephemeral
// per-run branches) plus the run worktree. It is idempotent: it clears
// s.worktrees and s.runWorktree as it goes, so a second call is a no-op.
// Callers must invoke this BEFORE emitting RunFinished so that the git
// subprocesses finish before the caller's goroutine signals completion.
func (s *scheduler) cleanupWorktrees() {
	if s.repoRoot == "" {
		return
	}
	for stepID, path := range s.worktrees {
		_ = removeWorktree(s.repoRoot, path)
		_, _ = gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(stepID))
		delete(s.worktrees, stepID)
	}
	// Remove the run worktree but KEEP the run branch: it is the run's
	// integration history, left for inspection and for the final merge gate.
	if s.runWorktree != "" {
		_ = removeWorktree(s.repoRoot, s.runWorktree)
		s.runWorktree = ""
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
			branch := s.stepBranchName(st.ID)
			wtPath := filepath.Join(s.jigRoot, "worktrees", s.runID, st.ID)
			// Branch off the run branch's current HEAD at dispatch time (spec 06) so
			// the step sees the code upstream steps already integrated. Fall back to
			// repo HEAD when there is no run branch (non-git path, where this whole
			// block only runs if createWorktree can still find a HEAD to fail against).
			base := "HEAD"
			if s.runBranch != "" {
				base = s.runBranch
			}
			baseSHA, err := createWorktreeAt(s.repoRoot, wtPath, branch, base)
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
		subs: subs,
		// answerCh is created here on the scheduler goroutine — before the worker
		// starts — so the field is never written from the executor goroutine and
		// an answer delivered any time after dispatch has a live channel to land
		// on. Buffered(1) so AnswerQuestion never blocks the scheduler.
		answerCh: make(chan string, 1),
		inbox:    inbox,
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
			case SecurityFinding:
				// Must-not-drop: rides ctrl, not live.
				e.RunID, e.StepID = runID, stepID
				fanOutCtrl(subs, e)
				// Also notify the scheduler so critical findings can escalate
				// to the recovery gate. Non-blocking: the scheduler's inbox is
				// buffered (64) and rarely full.
				select {
				case inbox <- securityFindingMsg{sf: e}:
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

	// Give this worker its own child context so Run.Stop can cancel it
	// independently of the run context and its siblings (spec 07 B1). The child is
	// cancelled either by handleStop (a deliberate stop) or when the worker's
	// stepDoneMsg is handled (normal completion) — the latter releases the context
	// resources. Cancelling the run context still cascades to every child.
	stepCtx, stepCancel := context.WithCancel(ctx)
	s.stepCancels[st.ID] = stepCancel

	go func() {
		result, err := s.exec.Execute(stepCtx, req, rep)
		// Deliver the result, but never block forever: once the run context is
		// cancelled the scheduler has stopped draining the inbox, so an
		// unconditional send would strand this goroutine (the inbox is only
		// buffered to 64, far below max_parallel). Select on ctx (the *run*
		// context, not stepCtx) so a single-step Stop — which cancels only
		// stepCtx while the run keeps draining — still delivers its stepDoneMsg
		// and transitions the step to StatusStopped.
		select {
		case s.inbox <- stepDoneMsg{stepID: stepID, result: result, err: err}:
		case <-ctx.Done():
		}
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
	// Activate the Tier-1 guard for agent steps unless explicitly disabled.
	// Security is on by default (nil Enabled = on); resolved after applyDefaults.
	var guard *sentinel.Guard
	var findingsPath string
	if st.Type == workflow.StepAgent {
		secOn := st.Security.Enabled == nil || *st.Security.Enabled
		t1On := st.Security.Tier1Enabled == nil || *st.Security.Tier1Enabled
		if secOn && t1On {
			guard = sentinel.NewGuard(st.Security.OutboundAllowlist)
			if s.runDir != "" {
				findingsPath = datastore.FindingsPath(s.runDir)
			}
		}
	}

	return StepRequest{
		RunID:           runID,
		Step:            st,
		Inputs:          s.preResolvedInputs[st.ID],
		Feedback:        s.stepFeedback[st.ID],
		WorkflowContext: workflowContext,
		ArtifactDir:     artifactDir,
		Worktree:        worktreePath,
		RepoRoot:        s.repoRoot,
		TranscriptPath:  transcriptPath,
		Iteration:       state.Iteration,
		Attempt:         state.Attempt,
		Guard:           guard,
		FindingsPath:    findingsPath,
	}
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

	if sf.Severity != "critical" {
		return
	}
	state, ok := s.states[sf.StepID]
	if !ok {
		return // unknown step — finding already recorded above
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
		if cancel, ok := s.stepCancels[sf.StepID]; ok {
			cancel()
		}
		// StatusAwaitingRecovery: already parked; finding recorded above.
		// Terminal states (succeeded, failed, skipped, stopped): record only.
	}
}

// handleStop cancels one running step's worker without ending the run (spec 07
// B1). It cancels only that step's child context — never the run context — so
// the worker exits, its stepDoneMsg is routed to StatusStopped (handle marks it
// via s.stopping), and the run stays alive and quiescent. Stopping a step that
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
	s.stopping[m.stepID] = true
	cancel()
}

// handleResume brings a step parked at step.StatusStopped back up (spec 07 B2).
// With a captured session id it continues the agent conversation with a new
// message (reusing the resumeSessions/stepMessage machinery that dispatch and
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
		s.resumeSessions[m.stepID] = state.Result.SessionID
		s.stepMessage[m.stepID] = m.message
	}
	// No captured session id → leave resumeSessions unset: dispatch builds the
	// full prompt and the runner starts a fresh session (documented degrade).
	state.Attempt++
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
	// Guard: only on an unfinished, quiescent run with git persistence.
	if s.terminated || s.inFlight > 0 || s.runWorktree == "" {
		return
	}

	closure := s.closureOf(m.stepID)
	if len(closure) == 0 {
		return
	}

	rewindTo, survivors := s.rewindPlan(m.stepID)

	// Journal the audit event and StepStatus(→pending) transitions BEFORE any
	// destructive operation. A crash after this point leaves the journal in a
	// state consistent with the pending/empty-output files that follow.
	s.emit(StepsReset{
		RunID:    s.runID,
		Target:   m.stepID,
		Closure:  closure,
		RewindTo: rewindTo,
	})
	for _, id := range closure {
		state := s.states[id]
		if state == nil {
			continue
		}
		s.transition(id, state.Status, step.StatusPending)
	}

	// Rewind the run branch and replay independent survivors.
	if rewindTo != "" {
		if out, err := gitCmd(s.runWorktree, "reset", "--hard", rewindTo); err != nil {
			s.emit(RunError{RunID: s.runID,
				Err: fmt.Sprintf("reset: git reset --hard %s: %v — %s", rewindTo, err, strings.TrimSpace(out))})
			return
		}
		for _, sha := range survivors {
			out, err := gitCmd(s.runWorktree, "cherry-pick", sha)
			if err != nil {
				// Conflict: abort the cherry-pick and leave the run parked. The
				// conflicted paths are surfaced via IntegrationConflictRequest,
				// reusing the gate from Foundation A (spec 06 A2).
				_, _ = gitCmd(s.runWorktree, "cherry-pick", "--abort")
				paths := mergeConflictPaths(s.runWorktree)
				s.emit(IntegrationConflictRequest{RunID: s.runID, StepID: m.stepID, Paths: paths})
				s.emit(RunError{RunID: s.runID,
					Err: fmt.Sprintf("reset: cherry-pick %s: conflict — %s", sha, strings.TrimSpace(out))})
				return
			}
		}
	}

	// Clear per-step derived outputs for the closure (result.json / output.*).
	// transcript.jsonl is intentionally kept — the re-run appends a new generation.
	for _, id := range closure {
		_ = datastore.ClearStepOutputs(s.runDir, id)
	}

	// Reset in-memory state for each closure step and purge stale routing maps.
	for _, id := range closure {
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
		if path, ok := s.worktrees[id]; ok {
			_ = removeWorktree(s.repoRoot, path)
			_, _ = gitCmd(s.repoRoot, "branch", "-D", s.stepBranchName(id))
			delete(s.worktrees, id)
			delete(s.wtBaseSHAs, id)
		}
		delete(s.diffs, id)

		delete(s.stepCommits, id)
		delete(s.resumeSessions, id)
		delete(s.stepMessage, id)
		delete(s.stepFeedback, id)
		delete(s.rerunSource, id)
		delete(s.recoverCount, id)
		delete(s.reviewMessages, id)
		delete(s.stepInputCount, id)
		delete(s.pendingUserInputs, id)
		delete(s.collectedUserInputs, id)
		delete(s.preResolvedInputs, id)
		delete(s.stopping, id)
	}
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

	case RecoverSkip:
		// Accept the failure and continue past it. The step stays failed, but
		// skippedByOperator marks it transparent so depsReady/anyPendingRunnable
		// treat it like an on_failure="continue" node — dependents proceed and the
		// run keeps going. No worker is re-dispatched; the recovery loop ends here.
		s.skippedByOperator[m.stepID] = true
		s.transition(m.stepID, state.Status, step.StatusFailed)

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

	if m.abort {
		// Discard the conflicted/staged squash so the run worktree is clean, then
		// fail the step through the normal failure policy (→ recovery gate).
		_, _ = gitCmd(s.runWorktree, "reset", "--hard")
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
	if len(mergeConflictPaths(s.runWorktree)) > 0 {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("step %q: unresolved conflicts remain in the run worktree", m.stepID),
		})
		return // stay parked
	}
	if out, err := gitCmd(s.runWorktree, "add", "-A"); err != nil {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("stage resolved conflict for %q: %v — %s", m.stepID, err, strings.TrimSpace(out)),
		})
		return
	}
	msg := m.stepID + "\n\njig-step: " + m.stepID
	if out, err := gitCmd(s.runWorktree, "commit", "-m", msg); err != nil {
		s.emit(RunError{
			RunID: s.runID,
			Err:   fmt.Sprintf("commit resolved conflict for %q: %v — %s", m.stepID, err, strings.TrimSpace(out)),
		})
		return // stay parked
	}
	if sha, err := currentHEAD(s.runWorktree); err == nil {
		s.stepCommits[m.stepID] = sha
	}
	wfStep := s.stepByID(m.stepID)
	s.transition(m.stepID, step.StatusAwaitingIntegration, step.StatusSucceeded)
	if wfStep != nil && wfStep.Loop != nil {
		s.recordLoopIntent(m.stepID, wfStep)
	}
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
	if s.runBranch == "" || s.runWorktree == "" || s.anyFailed() {
		return false
	}
	head, err := currentHEAD(s.runWorktree)
	if err != nil || head == s.runBaseSHA {
		return false // run branch is empty — nothing to merge
	}
	s.awaitingFinalMerge = true
	s.emit(FinalMergeRequest{RunID: s.runID, RunBranch: s.runBranch, Base: s.baseBranch})
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
		conflict, err := finalMerge(s.repoRoot, s.baseBranch, s.runBranch)
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
				Err:   fmt.Sprintf("final merge conflicts with %s; run branch %s left for manual merge", s.baseBranch, s.runBranch),
			})
			return
		}
	}
	// Approved-and-merged, or discarded: the run settles here.
	s.awaitingFinalMerge = false
	s.terminated = true
	s.cleanupWorktrees()
	s.emit(RunFinished{RunID: s.runID, Failed: s.anyFailed()})
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
				// Guard-skipped deps are transparent when the dependent only uses
				// them for sequencing (no @ref input). If the dependent references
				// output data from the guard-skipped step it must still be skipped.
				if s.skippedByGuard[dep] && !stepRefsDep(st, dep) {
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

// stepRefsDep reports whether any of st's inputs has Ref == depID, meaning st
// requires output data from depID (as opposed to a pure sequencing dependency).
func stepRefsDep(st *workflow.Step, depID string) bool {
	for _, inp := range st.Inputs {
		if inp.Ref == depID {
			return true
		}
	}
	return false
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
	source   string
	kind     workflow.StepType
	feedback string
	iter     int
}

// recordLoopIntent evaluates a finished step's [step.loop] and, if it fires,
// records its contribution against the goto target instead of rewinding
// immediately. The actual rewind is deferred to fireReadyLoops once the whole
// body settles — this is what makes parallel siblings looping to one step
// deterministic. The cap is still checked here (at the moment the looper fires),
// so an over-cap loop aborts the run exactly as before.
func (s *scheduler) recordLoopIntent(stepID string, wfStep *workflow.Step) {
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

	// Resolve the feedback @ref to actual content (verdict for review steps,
	// output file text for agent/command steps) now, while the source step's
	// result is current.
	var content string
	if loop.Feedback != "" {
		feedbackID := strings.TrimPrefix(loop.Feedback, "@")
		if dot := strings.Index(feedbackID, "."); dot >= 0 {
			feedbackID = feedbackID[:dot]
		}
		if fs := s.states[feedbackID]; fs != nil && fs.Result != nil {
			if fs.Result.Verdict != "" {
				content = fs.Result.Verdict
			} else if fs.Result.OutputPath != "" {
				if data, err := os.ReadFile(fs.Result.OutputPath); err == nil {
					content = string(data)
				}
			}
		}
	}

	intent := s.pendingLoops[loop.Goto]
	if intent == nil {
		intent = &loopIntent{gotoID: loop.Goto}
		s.pendingLoops[loop.Goto] = intent
	}
	intent.contribs = append(intent.contribs, loopContribution{
		source:   stepID,
		kind:     wfStep.Type,
		feedback: content,
		iter:     state.Iteration,
	})
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
		if intent == nil || !s.loopBarrierReady(g) {
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
func (s *scheduler) loopBarrierReady(gotoID string) bool {
	for _, id := range s.bodyUnion(gotoID) {
		st := s.states[id]
		switch st.Status {
		case step.StatusRunning,
			step.StatusAwaitingReview,
			step.StatusNeedsInput,
			step.StatusAwaitingRecovery,
			step.StatusAwaitingIntegration:
			return false
		case step.StatusPending:
			if s.depsReady(s.stepByID(id)) {
				return false
			}
		}
	}
	return true
}

// bodyUnion is the reset set for a rewind to gotoID: the union of loopBody over
// every step statically declaring a loop back to gotoID, in workflow
// declaration order. For the common single-looper (join) case this is exactly
// loopBody(gotoID, theLooper); with parallel siblings it covers all their paths
// so one rewind redoes the whole iteration.
func (s *scheduler) bodyUnion(gotoID string) []string {
	seen := map[string]bool{}
	for i := range s.wf.Steps {
		L := &s.wf.Steps[i]
		if L.Loop != nil && L.Loop.Goto == gotoID {
			for _, id := range s.loopBody(gotoID, L.ID) {
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

// fireCoalescedLoop performs one rewind for an intent: it composes the union of
// its contributors' feedback, records the reason, emits a LoopFired per
// contributor, and resets the whole body to pending at the next iteration.
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
		s.stepFeedback[intent.gotoID] = feedbacks[0].feedback
	default:
		var b strings.Builder
		for i, c := range feedbacks {
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "From `%s`:\n%s", c.source, c.feedback)
		}
		s.stepFeedback[intent.gotoID] = b.String()
	}

	// Name the re-run reason from the first contributor (declaration order).
	s.rerunSource[intent.gotoID] = intent.contribs[0].source

	// Emit one LoopFired per contributor for journal/observability fidelity.
	for _, c := range intent.contribs {
		src := s.stepByID(c.source)
		max := 0
		if src != nil && src.Loop != nil {
			max = src.Loop.MaxIterations
		}
		s.emit(LoopFired{
			RunID:     s.runID,
			StepID:    c.source,
			Goto:      intent.gotoID,
			Iteration: newIter,
			Max:       max,
		})
	}

	// Reset every step in the body to pending with the new iteration count.
	for _, id := range s.bodyUnion(intent.gotoID) {
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

// closureOf returns the reset set for targetID: the target itself plus every
// step that transitively depends on it, in workflow declaration order.
// Independent parallel branches (steps with no transitive dependency on
// targetID) are excluded — they are survivors in a subsequent rewindPlan call.
func (s *scheduler) closureOf(targetID string) []string {
	// Forward reachability: start with the target, then add every step whose
	// depends_on chain passes through the accumulating set. Same algorithm as
	// loopBody's fwd set, but without the backward intersection.
	fwd := map[string]bool{targetID: true}
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
	// Return in declaration order so the caller has a stable, deterministic list.
	var out []string
	for i := range s.wf.Steps {
		if fwd[s.wf.Steps[i].ID] {
			out = append(out, s.wf.Steps[i].ID)
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
	if s.runWorktree == "" || len(s.stepCommits) == 0 {
		return "", nil
	}

	closure := s.closureOf(targetID)
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
	out, err := gitCmd(s.runWorktree, "log", "--format=%H", "--reverse",
		s.runBaseSHA+"..HEAD")
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
		rewindTo = s.runBaseSHA
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

func (s *scheduler) transition(stepID string, from, to step.Status) {
	state := s.states[stepID]
	state.Status = to
	ev := StepStatus{
		RunID:      s.runID,
		StepID:     stepID,
		From:       from,
		To:         to,
		Attempt:    state.Attempt,
		Iteration:  state.Iteration,
		Generation: state.Generation,
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
					if state.Result != nil {
						term.TotalCostUSD = state.Result.TotalCostUSD
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
	var totalCost float64
	var totalTokens int
	for i, wfStep := range s.wf.Steps {
		st := s.states[wfStep.ID]
		states[i] = *st
		switch st.Status {
		case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
		default:
			allDone = false
		}
		// Cumulative across every attempt (see step.State.SpentUSD), so a run that
		// retried or reset a step reports the full amount it actually paid.
		totalCost += st.SpentUSD
		totalTokens += st.SpentTokens
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
