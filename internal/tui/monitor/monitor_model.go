package monitor

import (
	"strings"
	"time"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/glamour/v2"

	"jig/internal/engine"
	"jig/internal/helpchat"
	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/transcript"
	questionpanel "jig/internal/tui/question"
	"jig/internal/tui/shared"
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
	inputKindHelpFinalMerge                              // final-merge gate triggered by help agent
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
	kind   pendingInputKind
	stepID string

	// Exactly one payload pointer is non-nil, matching kind.
	request      *engine.InputRequest
	question     questionpanel.Model
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
}

type gateContextSnapshot struct {
	cursor         int
	rowKind        string
	stepID         string
	filePath       string
	listOffset     int
	chatOffset     int
	chatAutoScroll bool
	chatSeenSeq    int
	chatBlock      chatItem
	chatExpand     map[blockKey]bool
	groupExpand    map[blockKey]bool
	chatExpandAll  bool
	targetStep     string
}

// visibleRow is one row in the Steps panel flat list. Steps always appear; file
// rows appear beneath their parent step only when it is expanded.
type visibleRow struct {
	kind   string // "step" or "file"
	stepID string
	file   *outputFile
}

func (r visibleRow) isStepRow() bool {
	return r.kind == "step"
}

func (r visibleRow) isFileRow() bool {
	return r.kind == "file"
}

// Model is the per-run view: a live step-status table for one run,
// updated as engine events arrive. The user can press esc to return to the
// runs list.
type Model struct {
	RunID    string
	RunDir   string
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
	// visible row in the Steps panel (a step or one of its expanded files).
	// chatStep is the step whose transcript the right panel currently shows —
	// kept in sync with the cursor via eager reload (the transcript always shows
	// the cursor's step, Resolved Decision 10/13).
	focus    focusRegion
	cursor   int    // selected visible row in the Steps panel
	chatStep string // step whose transcript the Transcript panel renders

	// Phase 5 chat rendering. RunDir locates the per-step transcript.jsonl on
	// disk; the transcript file — not the lossy event bus — is what the
	// Transcript panel renders (as themed Charmtone markdown).

	// chatEntries is the currently-loaded (windowed) transcript for chatStep,
	// re-read on entry and on each StepMessage for that step. chatElided counts
	// entries dropped off the front of the window (see chatWindowMax).
	chatEntries []transcript.Entry
	chatElided  int

	// Collapse/expand navigation. chatGroupHeaders is the canonical list of
	// collapsible navigation items (one entry per toolGroup or standalone thinking
	// block), stable until the next loadChat call. chatBlocks is the active
	// navigation list derived from chatGroupHeaders: when a group is expanded its
	// individual block items are inserted immediately after the group header.
	// chatRenderPlan is the pre-computed sequence of render items consumed by
	// chatBody; rebuilt by rebuildActiveState whenever expansion state changes.
	// chatBlockCursor selects the active item in chatBlocks.
	// chatExpand / chatGroupExpand record per-block and per-group expansion
	// overrides; chatExpandAll is a global read-only override.
	chatGroupHeaders []chatItem
	chatBlocks       []chatItem
	chatRenderPlan   []renderItem
	chatBlockCursor  int
	chatExpand       map[blockKey]bool
	chatGroupExpand  map[blockKey]bool
	chatExpandAll    bool

	// renderer renders text blocks as markdown; chatRendered caches the output
	// keyed by block (glamour re-parses whole documents, so re-rendering on every
	// event is wasteful). The cache is invalidated when the transcript panel's
	// inner width changes (see lastTranscriptW / rebuildRenderer).
	renderer     *glamour.TermRenderer
	chatRendered map[blockKey]string

	// fileRenderer removes document framing for output files. insetRenderer uses
	// the same flush layout at the narrower width left inside a tool block's bar.
	fileRenderer  *glamour.TermRenderer
	insetRenderer *glamour.TermRenderer

	// msgCount tracks the latest transcript entry seq observed per step (via
	// StepMessage liveness events), used as a message count in the list.
	msgCount map[string]int

	expanded  map[string]bool
	stepFiles map[string][]outputFile

	selKind string // "file" when a file row is selected, "" otherwise
	selFile string // absolute path of the selected file

	// inputQueue holds every step currently blocked on a human, in arrival order.
	// activeInputIdx is the entry currently shown in the gate overlay. hasGate() is
	// len(inputQueue) > 0; an empty queue leaves only the compact, inert input bar.
	inputQueue     []pendingInputEntry
	activeInputIdx int
	gateContext    *gateContextSnapshot

	// reviews retains the last ReviewRequest seen per step so the Transcript panel
	// can show the diff when a review step is selected — review steps have no
	// transcript. Kept after the queue entry is removed (Unit 5).
	reviews map[string]engine.ReviewRequest

	// promptTextarea is the active textarea, rebuilt from the current entry's draft
	// via shared.NewInputTextarea on every entry switch (request/prompt/review-compose kinds).
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
	// chatSeenSeq is the newest entry acknowledged for chatStep. Comparing it
	// with msgCount keeps the paused count correct even when lossy liveness
	// events skip directly across several transcript entries.
	chatSeenSeq int
	// pendingGPrefix is set on a single 'g' keypress in the Transcript panel
	// to arm the gg→GotoTop chord; any other key clears it.
	pendingGPrefix bool

	width  int
	height int

	// Help agent modal (ctrl+h). helpOpen/helpReady are the open/connected flags;
	// helpModel is preserved across open/close cycles for the run's lifetime.
	// helpGateReq/helpGateAns are the rendezvous channels for the final-merge gate.
	helpOpen    bool
	helpReady   bool
	helpModel   helpchat.Model
	run         *engine.Run
	helpGateReq chan struct{} // bidirectional: tools write, waitForGateReqCmd reads
	helpGateAns chan bool

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
	// gateTextareaRows is the content-row count passed to shared.NewInputTextarea for
	// every gate entry that uses a textarea (inputKindRequest, inputKindPrompt,
	// and review compose). Changing it here propagates to the overlay height.
	gateTextareaRows = 4

	// The contextual subject and required-action rows stay visible above every
	// gate body so clipped overlays still explain what the operator must decide.
	gateHeaderRows = 2

	// maxReviewChoices is the bounded maximum number of verdict-choice lines a
	// review entry can render without overflowing the overlay height. A value
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

// chatItem is one entry in the canonical navigation list (chatGroupHeaders) and
// the active navigation list (chatBlocks).
// isGroup=false: a standalone collapsible block (thinking, or a block outside
// any group). isGroup=true: a tool call group header; group points to its
// toolGroup payload. key is always the blockKey of the item's first block (used
// as the groupKey for chatGroupExpand).
type chatItem struct {
	isGroup bool
	key     blockKey
	group   *toolGroup
}

// toolGroup is the payload for a group-header chatItem.
type toolGroup struct {
	blocks []blockKey // all tool_use / tool_result blockKeys in the group, in order
	count  int        // number of tool_use blocks — the N in "N tool calls"
}

// renderKind discriminates the six variants of renderItem.
type renderKind int

const (
	renderEntrySep    renderKind = iota // iteration/retry/re-run separator line
	renderEntryHeader                   // "#N role" header line
	renderText                          // prose text block (assistant markdown or system verbatim)
	renderGroupHeader                   // tool call group collapsed/expanded header
	renderGroupGap                      // blank line between blocks in an expanded tool group
	renderBlock                         // individual collapsible block (thinking or inner tool block)
)

// renderItem is one element of the pre-computed render plan (chatRenderPlan).
// kind determines which fields are populated.
type renderItem struct {
	kind renderKind

	// renderEntrySep: sep is the separator string (e.g., "── iteration 2 ──").
	sep string

	// renderEntryHeader: key.seq is the entry seq; role is the entry role.
	// ts is the entry timestamp formatted as "HH:MM:SS"; "" if unavailable.
	// renderText, renderBlock: key identifies the block; blk is a pointer into chatEntries.
	// renderGroupHeader: key is the groupKey (first block); group points to the toolGroup.
	key   blockKey
	blk   *transcript.Block
	role  transcript.Role
	group *toolGroup
	ts    string // "HH:MM:SS" for renderEntryHeader; "" if timestamp absent or unparseable
}

type monitorStep struct {
	id        string
	status    step.Status
	start     time.Time
	end       time.Time
	err       string   // failure reason when status == StatusFailed
	subtype   string   // SDK result subtype for agent policy-limit failures
	cost      *float64 // TotalCostUSD from step.Result; nil when not yet known
	tokens    int      // total tokens processed; 0 when not yet known
	iteration int      // current loop iteration (from StepStatus.Iteration)
	attempt   int      // current retry attempt (from StepStatus.Attempt)
}

type lifecycleActions struct {
	stepID    string
	canStop   bool
	canReset  bool
	canResume bool
}

// New creates a fresh monitor model for the given runID.
func New(runID string) Model {
	return Model{
		RunID:           runID,
		keys:            defaultMonitorKeys(),
		index:           make(map[string]int),
		stepOutput:      make(map[string]*strings.Builder),
		msgCount:        make(map[string]int),
		chatExpand:      make(map[blockKey]bool),
		chatGroupExpand: make(map[blockKey]bool),
		chatRendered:    make(map[blockKey]string),
		reviews:         make(map[string]engine.ReviewRequest),
		chatAutoScroll:  true,
		expanded:        make(map[string]bool),
		stepFiles:       make(map[string][]outputFile),
	}
}

// SetRun wires the live engine handle so the help agent can read run state and
// dispatch recovery actions. Call this after New/WithSnapshot for live runs;
// leave unset for journal-replayed runs (ctrl+h shows a static unavailable message).
func (m *Model) SetRun(run *engine.Run) {
	m.run = run
}

// WithSnapshot initialises the monitor from a RunSnapshot so the user sees
// current state immediately when navigating to an already-running run.
func (m Model) WithSnapshot(snap engine.RunSnapshot) Model {
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
	if m.chatGroupExpand == nil {
		m.chatGroupExpand = make(map[blockKey]bool)
	}
	if m.chatRendered == nil {
		m.chatRendered = make(map[blockKey]string)
	}
	if m.reviews == nil {
		m.reviews = make(map[string]engine.ReviewRequest)
	}
	if m.expanded == nil {
		m.expanded = make(map[string]bool)
	}
	if m.stepFiles == nil {
		m.stepFiles = make(map[string][]outputFile)
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
	// Re-discover output files per step and clamp cursor to visible row count.
	for _, st := range snap.Steps {
		if m.RunDir != "" {
			m.stepFiles[st.ID] = stepOutputFiles(m.RunDir, st.ID, "")
		}
	}
	if nRows := len(m.visibleRows()); m.cursor >= nRows && nRows > 0 {
		m.cursor = nRows - 1
	}
	return m
}

// WithJournal rebuilds the monitor from a run's replayed journal — the recovery
// path for a run from an earlier session, where no in-memory Run handle exists to
// Snapshot(). It folds the same events a live run emits (reconstructing the step
// list, statuses, and done/failed), then points the Transcript panel at the first
// step so content shows on open without waiting for an event that will never
// arrive. RunDir must be set before calling so the transcript load can find the
// step files.
//
// Any gate entries a finished run's journal contains (a review or recovery
// prompt) are cleared by the resolving step transition that follows them, so a
// cleanly finished run folds down to an empty queue. A run that died while parked
// keeps its historical prompt, but the root guards every gate action on a live
// handle, so a recovered prompt is inert.
func (m Model) WithJournal(evs []engine.Event) Model {
	for _, e := range evs {
		m, _ = m.handleEngineEvent(e)
	}
	m.reloadTranscript()
	return m
}

// HelpSections returns the sections to show for the monitor's current focus and
// gate state for the help overlay.
func (m Model) HelpSections() []shared.HelpSection {
	var sections []shared.HelpSection

	switch {
	case m.focus == focusGate && m.hasGate():
		sections = append(sections, m.gateHelpSection())
	case m.focus == focusTranscript:
		bindings := []keybind.Binding{
			m.keys.Scroll, m.keys.GotoTop, m.keys.Follow,
			m.keys.BlockNav, m.keys.Toggle, m.keys.ExpandAll,
			m.keys.TransToSteps, m.keys.TransLeave,
		}
		if m.gateContext != nil {
			contextKey := m.keys.GateContext
			contextKey.SetHelp("ctrl+o", "return")
			bindings = append([]keybind.Binding{contextKey}, bindings...)
		}
		sections = append(sections, shared.HelpSection{
			Title:    "Transcript",
			Bindings: bindings,
		})
	default: // focusSteps
		actions := m.selectedLifecycleActions()
		stopKey := m.keys.StopStep
		resetKey := m.keys.ResetStep
		resumeKey := m.keys.ResumeStep
		stopKey.SetEnabled(actions.canStop)
		resetKey.SetEnabled(actions.canReset)
		resumeKey.SetEnabled(actions.canResume)
		bindings := []keybind.Binding{
			m.keys.StepsNav, m.keys.OpenTranscript, m.keys.ToggleTree,
			stopKey, resetKey, resumeKey, m.keys.StepsLeave,
		}
		if m.gateContext != nil {
			contextKey := m.keys.GateContext
			contextKey.SetHelp("ctrl+o", "return")
			bindings = append([]keybind.Binding{contextKey}, bindings...)
		}
		sections = append(sections, shared.HelpSection{
			Title:    "Steps",
			Bindings: bindings,
		})
	}

	// Focus + Global sections are shown on every screen.
	sections = append(sections, shared.HelpSection{
		Title:    "Focus",
		Bindings: []keybind.Binding{m.keys.FocusNext, m.keys.FocusPrev, m.keys.PanelFocus},
	})
	sections = append(sections, shared.HelpSection{
		Title:    "Global",
		Bindings: []keybind.Binding{shared.KeyHelp, shared.KeyQuit},
	})
	return sections
}

// gateHelpSection builds the section for the currently-active gate entry.
func (m Model) gateHelpSection() shared.HelpSection {
	entry, ok := m.activeEntry()
	if !ok {
		return shared.HelpSection{Title: "Gate", Bindings: []keybind.Binding{m.keys.GateBlur}}
	}
	entryNav := m.keys.GateEntryNav
	entryNav.SetEnabled(len(m.inputQueue) > 1)
	contextKey := m.keys.GateContext
	contextKey.SetEnabled(presentationForGate(entry).contextStep != "")

	sec := shared.HelpSection{Title: "Gate"}
	switch entry.kind {
	case inputKindRequest:
		sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, entryNav, m.keys.GateBlur}
	case inputKindQuestion:
		sec.Bindings = []keybind.Binding{m.keys.QuestionScroll, m.keys.QConfirm, contextKey, entryNav, m.keys.GateBlur}
	case inputKindReview:
		switch {
		case entry.composing:
			sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, m.keys.GateBlur}
		case entry.review != nil && entry.review.AllowMessage:
			sec.Bindings = []keybind.Binding{m.keys.Verdict, m.keys.Message, contextKey, entryNav, m.keys.GateBlur}
		default:
			sec.Bindings = []keybind.Binding{m.keys.Verdict, contextKey, entryNav, m.keys.GateBlur}
		}
	case inputKindPrompt:
		sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, entryNav, m.keys.GateBlur}
	case inputKindRecovery:
		switch {
		case entry.composing:
			sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, m.keys.GateBlur}
		case entry.recovery != nil && entry.recovery.CanResume:
			sec.Bindings = []keybind.Binding{m.keys.RecoverRetry, m.keys.RecoverGuide, m.keys.RecoverSkip, m.keys.RecoverAbort, contextKey, entryNav, m.keys.GateBlur}
		default:
			sec.Bindings = []keybind.Binding{m.keys.RecoverRetry, m.keys.RecoverSkip, m.keys.RecoverAbort, contextKey, entryNav, m.keys.GateBlur}
		}
	case inputKindIntegrationConflict:
		sec.Bindings = []keybind.Binding{m.keys.IntegrationResolve, m.keys.RecoverAbort, contextKey, entryNav, m.keys.GateBlur}
	case inputKindFinalMerge, inputKindHelpFinalMerge:
		sec.Bindings = []keybind.Binding{m.keys.FinalMergeApprove, m.keys.FinalMergeDiscard, contextKey, entryNav, m.keys.GateBlur}
	case inputKindResetConfirm:
		sec.Bindings = []keybind.Binding{contextKey, m.keys.GateBlur}
	}
	return sec
}

// CapturesText reports whether a gate textarea is capturing free text; delegates
// to textareaActive. Satisfies the helpProvider interface needed by root.
func (m Model) CapturesText() bool { return m.textareaActive() }

// visibleRows builds the flat row list for the Steps panel. Steps always
// appear; file rows appear beneath their parent when it is expanded and has
// accessible output files.
func (m Model) visibleRows() []visibleRow {
	var rows []visibleRow
	for _, s := range m.steps {
		rows = append(rows, visibleRow{kind: "step", stepID: s.id})
		if !m.expanded[s.id] {
			continue
		}
		files := m.stepFiles[s.id]
		for i, f := range files {
			if f.err != nil {
				continue
			}
			rows = append(rows, visibleRow{
				kind:   "file",
				stepID: s.id,
				file:   &files[i],
			})
		}
	}
	return rows
}

// cursorStepID returns the step ID of the row under the cursor.
// Returns "" when the cursor is out of bounds.
func (m Model) cursorStepID() string {
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return ""
	}
	return rows[m.cursor].stepID
}

// cursorIsFileRow reports whether the cursor is currently on a file row.
func (m Model) cursorIsFileRow() bool {
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return false
	}
	return rows[m.cursor].isFileRow()
}

// selectedLifecycleActions keeps dispatch and both help surfaces on the same
// visible-row interpretation; a file row names its parent step, but must never
// inherit that step's actions.
func (m Model) selectedLifecycleActions() lifecycleActions {
	if m.done || m.cursorIsFileRow() {
		return lifecycleActions{}
	}
	stepID := m.cursorStepID()
	i, ok := m.index[stepID]
	if stepID == "" || !ok || i < 0 || i >= len(m.steps) {
		return lifecycleActions{}
	}

	status := m.steps[i].status
	actions := lifecycleActions{
		stepID:    stepID,
		canStop:   status == step.StatusRunning,
		canResume: status == step.StatusStopped,
	}
	switch status {
	case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped,
		step.StatusStopped, step.StatusAwaitingReview:
		actions.canReset = true
	}
	return actions
}
