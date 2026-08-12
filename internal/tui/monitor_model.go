package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/glamour/v2"

	"jig/internal/engine"
	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/transcript"
)

// focusRegion is which of the monitor's three regions currently holds keyboard
// input. Both the Steps panel and the Transcript panel are always visible; only
// the focused region's border is drawn primary (Charple). A pending gate is a
// third region: it auto-focuses on arrival but does not freeze navigation — the
// user can tab away to read the transcript a verdict is about and tab back. See
// docs/adr/0002-gates-are-nonblocking-focus-regions.md.
//
// The explicit focus keeps the Steps list-navigation keymap (j/k select) from
// colliding with the Transcript viewport's scroll keymap (j/k scroll).
type focusRegion int

const (
	focusSteps focusRegion = iota
	focusTranscript
	focusGate
)

// pendingInputKind discriminates the four human-in-the-loop request types that
// can live in the input queue simultaneously.
type pendingInputKind int

const (
	inputKindRequest             pendingInputKind = iota // block_on InputRequest
	inputKindQuestion                                    // AskUserQuestion AgentQuestion
	inputKindPrompt                                      // from="user" PromptRequest
	inputKindReview                                      // ReviewRequest (verdict + message)
	inputKindRecovery                                    // RecoveryRequest (retry / resume / abort)
	inputKindIntegrationConflict                         // IntegrationConflictRequest (resolve / abort)
	inputKindFinalMerge                                  // FinalMergeRequest (approve / discard)
	inputKindResetConfirm                                // reset confirmation (y/n, default n — spec 08 C4)
)

// resetConfirmEntry holds the data for a pending reset confirmation gate entry.
type resetConfirmEntry struct {
	runID   string
	stepID  string   // the reset target
	closure []string // all steps that will be reset (incl. target)
}

// pendingInputEntry is one element of the persistent input queue. Exactly one
// payload pointer is non-nil, matching kind. Per-entry state (draft text,
// question progress, compose flag, scroll position) is preserved across queue
// navigation so the user can return to a partially-answered entry.
type pendingInputEntry struct {
	kind      pendingInputKind
	stepID    string
	toolUseID string // non-empty only for inputKindQuestion

	// Exactly one payload pointer is non-nil, matching kind.
	request      *engine.InputRequest
	question     *engine.AgentQuestion
	prompt       *engine.PromptRequest
	review       *engine.ReviewRequest
	recovery     *engine.RecoveryRequest
	integration  *engine.IntegrationConflictRequest
	finalMerge   *engine.FinalMergeRequest
	resetConfirm *resetConfirmEntry

	// draft is the in-progress textarea text (request/prompt, review compose, and
	// recovery guidance), preserved across navigation.
	draft string

	// composing is true while composing a message on a review entry, or guidance
	// on a recovery entry.
	composing bool

	// AgentQuestion multi-step flow, preserved per entry (Decision 9).
	questionIdx      int
	questionSelected map[int]bool
	questionAnswers  []string

	// scrollOffset windows a long AgentQuestion option list within the fixed
	// strip height (Unit 6).
	scrollOffset int
}

// monitorModel is the per-run view: a live step-status table for one run,
// updated as engine events arrive. The user can press esc to return to the
// runs list.
type monitorModel struct {
	runID    string
	workflow string
	keys     monitorKeys
	steps    []monitorStep
	index    map[string]int // stepID → steps position
	done     bool
	failed   bool
	// runErr is an engine-level failure (worktree setup, max_iterations) that is
	// not attributable to a single step. Set by the engine.RunError event.
	runErr string
	// totalCost / totalTokens are the summed cost and token counts across all
	// steps, recomputed whenever a step reaches a terminal state (live) and on
	// snapshot/journal load. Zero means "nothing reported" — not "$0.00 / 0 spent".
	totalCost   float64
	totalTokens int

	// Two-panel navigation: focus selects the active region; cursor selects the
	// step in the Steps panel. chatStep is the step whose transcript the right
	// panel currently shows — kept in sync with the cursor via eager reload (the
	// transcript always shows the cursor's step, Resolved Decision 10/13).
	focus    focusRegion
	cursor   int    // selected step in the Steps panel
	chatStep string // step whose transcript the Transcript panel renders

	// Phase 5 chat rendering. runDir locates the per-step transcript.jsonl on
	// disk; the transcript file — not the lossy event bus — is what the
	// Transcript panel renders (as themed Charmtone markdown).
	runDir string

	// chatEntries is the currently-loaded (windowed) transcript for chatStep,
	// re-read on entry and on each StepMessage for that step. chatElided counts
	// entries dropped off the front of the window (see chatWindowMax).
	chatEntries []transcript.Entry
	chatElided  int

	// Collapse/expand: chatBlocks flattens the collapsible blocks (thinking,
	// tool_use, tool_result) of chatEntries in render order; chatBlockCursor
	// selects one for toggling; chatExpand records per-block overrides and
	// chatExpandAll a global "expand everything in view" toggle.
	chatBlocks      []blockKey
	chatBlockCursor int
	chatExpand      map[blockKey]bool
	chatExpandAll   bool

	// renderer renders text blocks as markdown; chatRendered caches the output
	// keyed by block (glamour re-parses whole documents, so re-rendering on every
	// event is wasteful). The cache is invalidated when the transcript panel's
	// inner width changes (see lastTranscriptW / rebuildRenderer).
	renderer     *glamour.TermRenderer
	chatRendered map[blockKey]string

	// msgCount tracks the latest transcript entry seq observed per step (via
	// StepMessage liveness events), used as a message count in the list.
	msgCount map[string]int

	// inputQueue holds every step currently blocked on a human, in arrival order.
	// activeInputIdx is the entry currently shown in the gate strip. hasGate() is
	// len(inputQueue) > 0; an empty queue still renders (placeholder) but is not
	// focusable via cycleFocus.
	inputQueue     []pendingInputEntry
	activeInputIdx int

	// reviews retains the last ReviewRequest seen per step so the Transcript panel
	// can show the diff when a review step is selected — review steps have no
	// transcript. Kept after the queue entry is removed (Unit 5).
	reviews map[string]engine.ReviewRequest

	// promptTextarea is the active textarea, rebuilt from the current entry's draft
	// via newInputTextarea on every entry switch (request/prompt/review-compose kinds).
	promptTextarea textarea.Model

	// secFindings is the list of security findings produced during this run,
	// populated by SecurityFinding ctrl events. Content is read from
	// findings.jsonl (file is truth) rather than from the event fields, so
	// the Detail field (redacted-secret preview) is always present.
	secFindings []sentinel.Finding

	// Phase 4: rolling output buffer per step (last outputMaxLines lines).
	stepOutput map[string]*strings.Builder

	vp     viewport.Model // Steps panel scroll
	chatVP viewport.Model // Transcript panel scroll — independent scroll position
	ready  bool

	// The monitor coalesces high-frequency engine events (streaming StepOutput
	// deltas, StepMessage liveness) into at most one repaint per frame instead of
	// re-rendering on every event. An event marks the affected panel(s) dirty; a
	// self-perpetuating frame tick flushes the dirty panels via SetContent. ticking
	// records that a frame is currently scheduled (so a burst of events doesn't
	// stack concurrent loops); the frame also drives the live-clock column while a
	// step runs. dirtyList/dirtyChat are the pending-repaint flags for the two
	// panels — the Steps list is cheap and always dirtied, while the Transcript
	// panel runs glamour and is dirtied only when an event touches the visible
	// step, so a parallel step's stream never repaints the panel you are viewing.
	ticking   bool
	dirtyList bool
	dirtyChat bool

	// chatAutoScroll tracks whether the Transcript panel should follow new
	// content to the bottom. True by default; cleared when the user scrolls up;
	// restored when the user scrolls back to bottom or navigates to a new step.
	chatAutoScroll bool
	// pendingGPrefix is set on a single 'g' keypress in the Transcript panel
	// to arm the gg→GotoTop chord; any other key clears it.
	pendingGPrefix bool

	width  int
	height int

	// stepsInnerW / transcriptInnerW are the two panels' inner content widths,
	// computed in resize() from the width split (Resolved Decision 11). narrow
	// is true when the terminal is too narrow for both panels to meet their
	// minimums, triggering the single-focused-panel fallback (Decision 14).
	stepsInnerW      int
	transcriptInnerW int
	narrow           bool

	// lastTranscriptW is the transcript panel inner width the glamour renderer
	// and per-block cache were last built for; rebuildRenderer invalidates the
	// cache when it changes.
	lastTranscriptW int
}

const (
	// stepsMinWidth / transcriptMinInnerWidth are the panel-split minimums from
	// Resolved Decision 11: the Steps panel is at least stepsMinWidth cells wide
	// and the Transcript panel keeps at least transcriptMinInnerWidth inner cells.
	stepsMinWidth           = 32
	transcriptMinInnerWidth = 40
)

const (
	// gateTextareaRows is the content-row count passed to newInputTextarea for
	// every gate entry that uses a textarea (inputKindRequest, inputKindPrompt,
	// and review compose). Changing it here propagates to gateBodyHeight().
	gateTextareaRows = 4

	// gateHeaderRows is the number of rows reserved for the [N / M] step-id (kind)
	// header that task 3.4 renders above each non-empty gate entry. Reserved in the
	// fixed height calculation now so gateBodyHeight() is stable before that task
	// lands.
	gateHeaderRows = 1

	// maxReviewChoices is the bounded maximum number of verdict-choice lines a
	// review entry can render without overflowing the fixed gate height. A value
	// of 4 covers the common approve/reject/defer/escalate pattern; the review
	// panel height is validated against this bound in the unit5-review-diff.txt
	// proof capture (task 5.4).
	maxReviewChoices = 4
)

const (
	// chatCollapseWidth is the render-time collapse: large blocks (thinking,
	// tool input, tool result) show at most this many characters on one line
	// until expanded. Distinct from the writer's byte cap (Truncated).
	chatCollapseWidth = 80

	// chatExpandMax bounds an expanded block so a 256 KiB write-capped result
	// never lays out in full; beyond it the middle is elided head+tail.
	chatExpandMax = 4096

	// chatWindowMax bounds how many trailing entries modeChat renders, so a long
	// run with thousands of messages stays responsive. Earlier entries are
	// summarised by a leading "… N earlier messages" marker.
	chatWindowMax = 300

	// outputMaxLines is the number of streaming output lines shown per step.
	outputMaxLines = 10
)

// blockKey identifies one block within a step's transcript by entry seq (unique
// per step file) and block index. It keys the expand-state and render caches.
type blockKey struct {
	seq   int
	block int
}

type monitorStep struct {
	id      string
	status  step.Status
	start   time.Time
	end     time.Time
	err     string   // failure reason when status == StatusFailed
	subtype string   // SDK result subtype for agent policy-limit failures
	cost    *float64 // TotalCostUSD from step.Result; nil when not yet known
	tokens  int      // total tokens processed; 0 when not yet known
}

func newMonitorModel(runID string) monitorModel {
	return monitorModel{
		runID:          runID,
		keys:           defaultMonitorKeys(),
		index:          make(map[string]int),
		stepOutput:     make(map[string]*strings.Builder),
		msgCount:       make(map[string]int),
		chatExpand:     make(map[blockKey]bool),
		chatRendered:   make(map[blockKey]string),
		reviews:        make(map[string]engine.ReviewRequest),
		chatAutoScroll: true,
	}
}

// withSnapshot initialises the monitor from a RunSnapshot so the user sees
// current state immediately when navigating to an already-running run.
func (m monitorModel) withSnapshot(snap engine.RunSnapshot) monitorModel {
	m.workflow = snap.Workflow
	m.done = snap.Done
	m.failed = snap.Failed
	m.totalCost = snap.TotalCostUSD
	m.totalTokens = snap.TotalTokens
	m.steps = make([]monitorStep, len(snap.Steps))
	m.index = make(map[string]int, len(snap.Steps))
	if m.stepOutput == nil {
		m.stepOutput = make(map[string]*strings.Builder)
	}
	if m.msgCount == nil {
		m.msgCount = make(map[string]int)
	}
	if m.chatExpand == nil {
		m.chatExpand = make(map[blockKey]bool)
	}
	if m.chatRendered == nil {
		m.chatRendered = make(map[blockKey]string)
	}
	if m.reviews == nil {
		m.reviews = make(map[string]engine.ReviewRequest)
	}
	for i, st := range snap.Steps {
		ms := monitorStep{id: st.ID, status: st.Status}
		if st.Result != nil && st.Status == step.StatusFailed {
			ms.err = st.Result.Err
			ms.subtype = st.Result.Subtype
		}
		// Cumulative spend across all attempts (step.State.SpentUSD), not the
		// latest Result — a reset/retry still cost what it cost.
		if st.SpentUSD > 0 {
			cost := st.SpentUSD
			ms.cost = &cost
		}
		ms.tokens = st.SpentTokens
		m.steps[i] = ms
		m.index[st.ID] = i
	}
	return m
}

// withJournal rebuilds the monitor from a run's replayed journal — the recovery
// path for a run from an earlier session, where no in-memory Run handle exists to
// Snapshot(). It folds the same events a live run emits (reconstructing the step
// list, statuses, and done/failed), then points the Transcript panel at the first
// step so content shows on open without waiting for an event that will never
// arrive. runDir must be set before calling so the transcript load can find the
// step files.
//
// Any gate entries a finished run's journal contains (a review or recovery
// prompt) are cleared by the resolving step transition that follows them, so a
// cleanly finished run folds down to an empty queue. A run that died while parked
// keeps its historical prompt, but the root guards every gate action on a live
// handle, so a recovered prompt is inert.
func (m monitorModel) withJournal(evs []engine.Event) monitorModel {
	for _, e := range evs {
		m, _ = m.handleEngineEvent(e)
	}
	m.reloadTranscript()
	return m
}
