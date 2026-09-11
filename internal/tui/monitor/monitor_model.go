package monitor

import (
	"strings"
	"time"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"jig/internal/engine"
	"jig/internal/helpchat"
	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/prefs"
	questionpanel "jig/internal/tui/question"
	reviewworkspace "jig/internal/tui/review"
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
	inputKindRecovery                                    // RecoveryRequest (retry / resume / skip / abort)
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
	workspace    *reviewworkspace.Model
	recovery     *engine.RecoveryRequest
	integration  *engine.IntegrationConflictRequest
	finalMerge   *engine.FinalMergeRequest
	resetConfirm *resetConfirmEntry

	// draft is the in-progress textarea text (request/prompt, review compose, and
	// recovery guidance), preserved across navigation.
	draft string

	// composing is true while composing recovery guidance.
	// on a recovery entry.
	composing bool
}

type reviewOutcome struct {
	roundID      string
	verdict      string
	commentCount int
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
	chatItem       transcriptItemKey
	chatItemExpand map[transcriptItemKey]bool
	chatExpandAll  bool
	legacyExpand   map[blockKey]bool
	legacyGroups   map[blockKey]bool
	chatPageEnd    int64
	searchQuery    string
	filters        transcriptFilters
	targetStep     string
}

// visibleRow is one row in the Steps panel flat list. Steps always appear;
// child rows (one foreach family instance) appear beneath their family when it
// is expanded; file rows appear beneath their parent step or child only when
// that row is itself expanded.
type visibleRow struct {
	kind   string // "step", "child", or "file"
	stepID string
	file   *outputFile
}

func (r visibleRow) isStepRow() bool {
	return r.kind == "step"
}

func (r visibleRow) isChildRow() bool {
	return r.kind == "child"
}

func (r visibleRow) isFileRow() bool {
	return r.kind == "file"
}

// Model is the per-run view: a live step-status table for one run,
// updated as engine events arrive. The user can press esc to return to the
// runs list.
type Model struct {
	RunID      string
	RunDir     string
	workflow   string
	keys       monitorKeys
	steps      []monitorStep
	index      map[string]int // stepID → steps position
	done       bool
	failed     bool
	historical bool
	// interrupted is set for historical journals whose last durable worker
	// status is running/validating (Spec 20 crash reopen).
	interrupted bool
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

	// chatEntries is the currently-loaded page for chatStep. Page cursors are
	// opaque transcript byte offsets, so paging stays bounded without retaining
	// an index proportional to the run.
	chatEntries []transcript.Entry
	chatPage    transcript.Page

	// Legacy group state is retained only until Task 4 migration cleanup lands.
	chatGroupHeaders   []chatItem
	chatBlocks         []chatItem
	chatRenderPlan     []renderItem
	chatBlockCursor    int
	chatExpand         map[blockKey]bool
	chatGroupExpand    map[blockKey]bool
	chatExpandAll      bool
	chatGroupForBlock  map[blockKey]blockKey
	chatItems          []transcriptItem
	chatVisibleItems   []transcriptItem
	chatItemCursor     int
	chatItemExpand     map[transcriptItemKey]bool
	chatItemExpandAll  bool
	chatItemRendered   map[transcriptRenderKey]string
	chatItemLineRanges map[transcriptLineKey]lineRange

	// renderer renders text blocks as markdown; chatRendered caches the output
	// keyed by block (glamour re-parses whole documents, so re-rendering on every
	// event is wasteful). The cache is invalidated when the transcript panel's
	// inner width changes (see lastTranscriptW / rebuildRenderer).
	renderer       *glamour.TermRenderer
	chatRendered   map[blockKey]string
	chatLineRanges map[chatLineKey]lineRange

	// Search is intentionally page-local: loading another bounded page rebuilds
	// hits from that page rather than indexing the complete transcript in memory.
	searchOpen      bool
	searchInput     textarea.Model
	searchQuery     string
	searchHits      []searchHit
	searchHitCursor int

	filterOpen   bool
	filterCursor int
	filters      transcriptFilters

	// fileRenderer removes document framing for output files. insetRenderer uses
	// the same flush layout at the narrower width left inside a tool block's bar.
	fileRenderer  *glamour.TermRenderer
	insetRenderer *glamour.TermRenderer

	// msgCount tracks the latest transcript entry seq observed per step (via
	// StepMessage liveness events), used as a message count in the list.
	msgCount map[string]int

	expanded  map[string]bool
	stepFiles map[string][]outputFile

	// familyChildren maps a foreach family's step id to the ordered instance
	// ids of its *current* generation's children (source order). A family that
	// has never expanded, or one that produced zero items, has no entry (or an
	// empty slice) here. A reset/route re-expansion overwrites the slice
	// wholesale — the prior generation's child monitorSteps remain in m.steps
	// as harmless historical residue (their StepStatus events already folded)
	// but are no longer reachable by expanding the family, matching the
	// scheduler's own fanOutFamilies bookkeeping (see engine.fanOutFamily).
	familyChildren map[string][]string

	selKind string // "file" when a file row is selected, "" otherwise
	selFile string // absolute path of the selected file

	// inputQueue holds every step currently blocked on a human, in arrival order.
	// activeInputIdx is the entry currently shown in the gate overlay. hasGate() is
	// len(inputQueue) > 0; an empty queue leaves only the compact, inert input bar.
	inputQueue     []pendingInputEntry
	activeInputIdx int
	// focusNextPromptStep records a submitted from="user" prompt until its
	// sequential replacement arrives. PromptRequest has no accompanying status
	// transition, so this distinguishes the next expected prompt from an
	// unsolicited gate arrival.
	focusNextPromptStep string
	// focusInputOnArrival is armed while entering a run before its first input
	// event reaches the monitor. It preserves entry-time intent without making
	// ordinary, later arrivals steal focus.
	focusInputOnArrival bool
	reviewOpen          bool
	gateContext         *gateContextSnapshot

	// reviews retains the last ReviewRequest seen per step so the Transcript panel
	// can show a document overview when a review step is selected — review steps
	// have no transcript. Kept after the queue entry is removed (Unit 5).
	reviews map[string]engine.ReviewRequest
	// reviewOutcomes preserves the terminal review summary after its queue entry
	// is removed, so selecting a completed review does not fall back to stale
	// pre-submission content.
	reviewOutcomes map[string]reviewOutcome

	// reviewDraftErrors is kept per queue entry so a failed draft write is
	// visible without disturbing another queued review.
	reviewDraftErrors map[string]string

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

	// simpleMode hides advanced transcript affordances from footer/help (2.3).
	// Default true; loaded from .jig/tui.json when jigRoot is set.
	simpleMode bool
	jigRoot    string // .jig/ root for prefs persistence; "" = in-memory only

	// diagnostics renders a sanitized text dump of the process-wide
	// notification diagnostic ring (spec 23-spec-run-notifications FR-17).
	// Nil when notifications are not wired (tests) — the overlay is inert.
	diagnostics DiagnosticsRenderer
	// showDiagnostics is true while the notification-diagnostics overlay is
	// composited over the Monitor. Only a deliberate operator command opens
	// it; an arriving diagnostic never does.
	showDiagnostics bool

	// telemetryMode is the resolved A18 telemetry exporter mode (off | prom |
	// otlp | both). Populated via WithTelemetryMode; empty and "off" both
	// hide the status-line badge so the indicator matches the "off by
	// default" observability posture.
	telemetryMode string

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
	// available through fixed-size pages.
	chatWindowMax = 300
	// A tool exchange can straddle an entry-count page boundary. Keep only a
	// small adjacent run of tool-only entries so the common batched use/result
	// pair stays together without making memory proportional to transcript size.
	chatBoundaryContextMax = 16

	// outputMaxLines is the number of streaming output lines shown per step.
	outputMaxLines = 10
)

// blockKey identifies one block within a step's transcript by entry seq (unique
// per step file) and block index. It keys the expand-state and render caches.
type blockKey struct {
	seq   int
	block int
}

// transcriptItemKey is stable for the lifetime of a loaded transcript page.
// Tool exchanges are anchored to their use block, while every other item uses
// its own block position. That keeps selection and expansion attached to the
// conversation event a reader sees, even when a later result enriches it.
type transcriptItemKey struct {
	anchor blockKey
	kind   transcriptItemKind
}

type transcriptItemKind int

const (
	transcriptItemText transcriptItemKind = iota
	transcriptItemThinking
	transcriptItemToolExchange
	transcriptItemToolResult
	transcriptItemSystem
	transcriptItemUnsupported
)

// transcriptBlockRef points into the bounded loaded page; it intentionally
// contains no transcript reader or file path, so normalization cannot expand
// its scope beyond that page.
type transcriptBlockRef struct {
	key      blockKey
	entryIdx int
	blockIdx int
}

// toolCorrelationKey scopes reusable tool IDs to one execution attempt.
// ToolUseID alone is not sufficient because retries and operator re-runs append
// to the same durable transcript.
type toolCorrelationKey struct {
	generation int
	iteration  int
	attempt    int
	toolUseID  string
}

type toolDisplayState int

const (
	toolDisplaySuccess toolDisplayState = iota
	toolDisplayError
	toolDisplayRunning
	toolDisplayUnknownUse
	toolDisplayUnknownResult
)

// transcriptItem is the immutable, page-local conversation unit consumed by
// rendering, search, and navigation. A paired tool exchange has both refs;
// incomplete exchanges retain exactly the evidence present in the page.
type transcriptItem struct {
	key          transcriptItemKey
	kind         transcriptItemKind
	role         transcript.Role
	primary      transcriptBlockRef
	toolUse      *transcriptBlockRef
	toolResult   *transcriptBlockRef
	displayState toolDisplayState
	coord        toolCorrelationKey
}

// transcriptRenderKey separates markdown and detail cache surfaces so changing
// a detail width cannot reuse output formatted for the conversation body.
type transcriptRenderKey struct {
	itemKey transcriptItemKey
	surface transcriptRenderSurface
	width   int
}

type transcriptRenderSurface int

const (
	transcriptRenderMarkdown transcriptRenderSurface = iota
	transcriptRenderDetail
)

type transcriptLineKey struct {
	itemKey transcriptItemKey
}

type lineRange struct {
	start int
	end   int
}

type chatLineKey struct {
	blockKey
	isGroup bool
}

func (i chatItem) lineKey() chatLineKey { return chatLineKey{blockKey: i.key, isGroup: i.isGroup} }

type searchHit struct {
	key     blockKey
	preview string
}

type transcriptFilters struct {
	errors    bool
	tools     bool
	reasoning bool
	retries   bool
	assistant bool
	user      bool
	system    bool
	result    bool
}

type chatItem struct {
	isGroup bool
	key     blockKey
	group   *toolGroup
}
type toolGroup struct {
	blocks         []blockKey
	count, results int
}
type renderKind int

const (
	renderEntrySep renderKind = iota
	renderEntryHeader
	renderText
	renderGroupHeader
	renderGroupGap
	renderBlock
)

type renderItem struct {
	kind  renderKind
	sep   string
	key   blockKey
	blk   *transcript.Block
	role  transcript.Role
	group *toolGroup
	ts    string
}

func (f transcriptFilters) active() bool {
	return f.errors || f.tools || f.reasoning || f.retries ||
		f.assistant || f.user || f.system || f.result
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

	// parentID/fanOutIndex/fanOutTotal mirror step.State's foreach provenance
	// (A8): parentID is the family step id for a runtime child, "" for every
	// ordinary step (including a family step itself — a family is a barrier,
	// not a child). fanOutIndex/fanOutTotal are the child's source position
	// and the family's item count at the generation the child belongs to.
	parentID    string
	fanOutIndex int
	fanOutTotal int
}

// isChild reports whether s is a foreach family's runtime child rather than an
// ordinary declared step.
func (s monitorStep) isChild() bool {
	return s.parentID != ""
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
		RunID:              runID,
		keys:               defaultMonitorKeys(),
		index:              make(map[string]int),
		stepOutput:         make(map[string]*strings.Builder),
		msgCount:           make(map[string]int),
		chatExpand:         make(map[blockKey]bool),
		chatGroupExpand:    make(map[blockKey]bool),
		chatGroupForBlock:  make(map[blockKey]blockKey),
		chatRendered:       make(map[blockKey]string),
		chatLineRanges:     make(map[chatLineKey]lineRange),
		chatItemExpand:     make(map[transcriptItemKey]bool),
		chatItemRendered:   make(map[transcriptRenderKey]string),
		chatItemLineRanges: make(map[transcriptLineKey]lineRange),
		reviews:            make(map[string]engine.ReviewRequest),
		reviewOutcomes:     make(map[string]reviewOutcome),
		reviewDraftErrors:  make(map[string]string),
		chatAutoScroll:     true,
		expanded:           make(map[string]bool),
		stepFiles:          make(map[string][]outputFile),
		familyChildren:     make(map[string][]string),
		simpleMode:         true, // C5 default; WithPrefs overrides from disk
	}
}

// WithPrefs loads simple-mode preference from jigRoot (.jig/). Empty root keeps
// the in-memory default (simple ON).
func (m Model) WithPrefs(jigRoot string) Model {
	m.jigRoot = jigRoot
	m.simpleMode = prefs.Load(jigRoot).SimpleMode
	return m
}

// WithTelemetryMode sets the exporter mode (off | prom | otlp | both) so the
// status line can render an "otel:<mode>" badge. Empty and "off" both hide
// the badge (A18).
func (m Model) WithTelemetryMode(mode string) Model {
	m.telemetryMode = mode
	return m
}

// SimpleMode reports whether advanced transcript chrome is hidden.
func (m Model) SimpleMode() bool { return m.simpleMode }

// ToggleSimpleMode flips simple/advanced and persists to .jig/tui.json.
func (m Model) ToggleSimpleMode() Model {
	m.simpleMode = !m.simpleMode
	_ = prefs.Save(m.jigRoot, prefs.Prefs{SimpleMode: m.simpleMode})
	return m
}

// SetRun wires the live engine handle so the help agent can read run state and
// dispatch recovery actions. Call this after New/WithSnapshot for live runs;
// leave unset for journal-replayed runs (ctrl+h shows a static unavailable message).
func (m *Model) SetRun(run *engine.Run) {
	m.run = run
	m.historical = false
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
	if m.chatGroupForBlock == nil {
		m.chatGroupForBlock = make(map[blockKey]blockKey)
	}
	if m.chatLineRanges == nil {
		m.chatLineRanges = make(map[chatLineKey]lineRange)
	}
	if m.chatItemExpand == nil {
		m.chatItemExpand = make(map[transcriptItemKey]bool)
	}
	if m.chatItemRendered == nil {
		m.chatItemRendered = make(map[transcriptRenderKey]string)
	}
	if m.chatItemLineRanges == nil {
		m.chatItemLineRanges = make(map[transcriptLineKey]lineRange)
	}
	if m.reviews == nil {
		m.reviews = make(map[string]engine.ReviewRequest)
	}
	if m.reviewOutcomes == nil {
		m.reviewOutcomes = make(map[string]reviewOutcome)
	}
	if m.reviewDraftErrors == nil {
		m.reviewDraftErrors = make(map[string]string)
	}
	if m.expanded == nil {
		m.expanded = make(map[string]bool)
	}
	if m.stepFiles == nil {
		m.stepFiles = make(map[string][]outputFile)
	}
	m.familyChildren = make(map[string][]string)
	for i, st := range snap.Steps {
		ms := monitorStep{
			id:          st.ID,
			status:      st.Status,
			parentID:    st.ParentID,
			fanOutIndex: st.FanOutIndex,
			fanOutTotal: st.FanOutTotal,
		}
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
		// snap.Steps folds runtime fan-out children in family/source order (see
		// scheduler.snapshot), so appending here as encountered reconstructs
		// each family's current-generation child order without a second pass.
		if st.ParentID != "" {
			m.familyChildren[st.ParentID] = append(m.familyChildren[st.ParentID], st.ID)
		}
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
// keeps its historical prompt for inspection, but marks every gate action
// read-only because no scheduler exists to receive a response.
func (m Model) WithJournal(evs []engine.Event) Model {
	m.historical = true
	m.interrupted = engine.ClassifyUnfinished(evs) == engine.UnfinishedInterrupted
	for _, e := range evs {
		m, _ = m.handleEngineEvent(e)
	}
	m.reloadTranscript()
	return m
}

// FocusPendingInput moves focus to the input gate when the run has work waiting
// for the operator. It is called when entering a run; ordinary gate arrivals do
// not use it so they remain non-disruptive while the monitor is already open.
func (m Model) FocusPendingInput() Model {
	if !m.hasGate() {
		m.focusInputOnArrival = true
		return m
	}
	m.focusInputOnArrival = false
	m.focus = focusGate
	m.loadActiveTextarea()
	m.refreshPanels()
	return m
}

// leaveMonitor returns to Home, or asks root to confirm when a review compose
// buffer still has unsaved text (A6).
func (m Model) leaveMonitor() (Model, tea.Cmd) {
	if m.hasDirtyCompose() {
		return m, func() tea.Msg { return RequestLeaveConfirmMsg{} }
	}
	return m, func() tea.Msg { return ShowHomeMsg{} }
}

// hasDirtyCompose reports an open review workspace with unsaved compose text.
func (m Model) hasDirtyCompose() bool {
	entry, ok := m.activeEntry()
	if !ok || entry.workspace == nil || !m.reviewOpen {
		return false
	}
	return entry.workspace.HasDirtyCompose()
}

// DiscardDirtyCompose clears an unsaved review compose buffer and closes the
// workspace so leave-to-Home can proceed after the operator confirms.
func (m Model) DiscardDirtyCompose() Model {
	entry, ok := m.activeEntry()
	if !ok || entry.workspace == nil {
		m.reviewOpen = false
		return m
	}
	ws := entry.workspace.DiscardCompose()
	m.inputQueue[m.activeInputIdx].workspace = &ws
	m.reviewOpen = false
	m.refreshPanels()
	return m
}

// HelpSections returns the sections to show for the monitor's current focus and
// gate state for the help overlay. In simple mode, advanced transcript
// affordances are omitted (still available via the command palette).
func (m Model) HelpSections() []shared.HelpSection {
	return m.helpSections(m.simpleMode)
}

// PaletteSections is the full action catalog for ctrl+k — never filtered by
// simple mode so advanced commands stay discoverable (D10 / 2.3).
func (m Model) PaletteSections() []shared.HelpSection {
	return m.helpSections(false)
}

func (m Model) helpSections(simple bool) []shared.HelpSection {
	var sections []shared.HelpSection

	switch {
	case m.focus == focusGate && m.hasGate():
		sections = append(sections, m.gateHelpSection())
	case m.focus == focusTranscript:
		var bindings []keybind.Binding
		if m.selKind == "file" {
			copyAll := m.keys.CopyAll
			copyAll.SetHelp("Y", "copy file")
			bindings = []keybind.Binding{
				m.keys.Scroll, m.keys.GotoTop, m.keys.ScrollFast, copyAll, m.keys.TransToSteps, m.keys.TransLeave,
			}
		} else {
			blockNav := m.keys.BlockNav
			if m.searchQuery != "" {
				blockNav.SetHelp("n/N", "match")
			}
			pageOlder := m.keys.PageOlder
			pageOlder.SetEnabled(m.chatPage.HasEarlier)
			pageNewer := m.keys.PageNewer
			pageNewer.SetEnabled(m.chatPage.HasLater)
			clearView := m.keys.ClearView
			clearView.SetEnabled(m.searchQuery != "" || m.filters.active())
			copyItem := m.keys.CopyItem
			copyItem.SetHelp("y", contextualItemCopyLabel(m))
			copyItem.SetEnabled(len(m.chatVisibleItems) > 0)
			copyAll := m.keys.CopyAll
			copyAll.SetHelp("Y", "copy transcript")
			if simple {
				// Keep scroll / follow / expand / leave; hide paging, filters,
				// search, expand-all, and block-nav from footer/help.
				bindings = []keybind.Binding{
					m.keys.Scroll, m.keys.Follow, m.keys.Toggle, m.keys.ScrollFast,
					m.keys.GotoTop, copyItem, copyAll,
					m.keys.TransToSteps, m.keys.TransLeave,
				}
			} else {
				bindings = []keybind.Binding{
					m.keys.Scroll, m.keys.Follow, blockNav, m.keys.Toggle, m.keys.ScrollFast,
					m.keys.GotoTop, pageOlder, pageNewer,
					m.keys.Search, m.keys.Filters, clearView, m.keys.ExpandAll,
					copyItem, copyAll,
					m.keys.TransToSteps, m.keys.TransLeave,
				}
			}
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
		treeKey := m.keys.ToggleTree
		treeKey.SetEnabled(!m.cursorIsFileRow())
		copyAll := m.keys.CopyAll
		if m.cursorIsFileRow() {
			copyAll.SetHelp("Y", "copy file")
		} else {
			copyAll.SetHelp("Y", "copy transcript")
		}
		bindings := []keybind.Binding{
			m.keys.OpenTranscript, stopKey, resetKey, resumeKey,
			m.keys.StepsNav, treeKey, copyAll, m.keys.StepsLeave,
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

	modeTitle := "Mode · simple"
	modeDesc := "enable advanced"
	if !m.simpleMode {
		modeTitle = "Mode · advanced"
		modeDesc = "enable simple"
	}
	toggleSimple := m.keys.ToggleSimple
	toggleSimple.SetHelp("ctrl+shift+a", modeDesc)
	sections = append(sections, shared.HelpSection{
		Title:    modeTitle,
		Bindings: []keybind.Binding{toggleSimple},
	})

	notifDiag := m.keys.NotificationDiagnostics
	notifDiag.SetEnabled(!m.CapturesText())
	sections = append(sections, shared.HelpSection{
		Title:    "Notifications",
		Bindings: []keybind.Binding{notifDiag},
	})

	// Focus + Global sections are shown on every screen.
	sections = append(sections, shared.HelpSection{
		Title:    "Focus",
		Bindings: []keybind.Binding{m.keys.FocusNext, m.keys.FocusPrev, m.keys.PanelFocus},
	})
	sections = append(sections, shared.HelpSection{
		Title:    "Global",
		Bindings: shared.GlobalHelpBindings(m.CapturesText(), m.keys.ToggleHelp),
	})
	return sections
}

// contextualItemCopyLabel names what y will copy given the current selection.
// The label mirrors what shared.FormatClipboardNotice will emit.
func contextualItemCopyLabel(m Model) string {
	if n := len(m.chatVisibleItems); n == 0 || m.chatItemCursor < 0 || m.chatItemCursor >= n {
		return "copy"
	}
	item := m.chatVisibleItems[m.chatItemCursor]
	switch item.kind {
	case transcriptItemToolExchange, transcriptItemToolResult:
		return "copy exchange"
	case transcriptItemThinking:
		return "copy thinking"
	default:
		return "copy message"
	}
}

func (m Model) compactHelpBindings() []keybind.Binding {
	sections := m.HelpSections()
	if len(sections) == 0 {
		return nil
	}
	return sections[0].Bindings
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
	escapeKey := m.gateEscapeBinding(entry)

	sec := shared.HelpSection{Title: "Gate"}
	if m.historical {
		sec.Bindings = []keybind.Binding{entryNav, escapeKey}
		return sec
	}
	switch entry.kind {
	case inputKindRequest:
		sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, entryNav, escapeKey}
	case inputKindQuestion:
		sec.Bindings = entry.question.HelpBindings()
		sec.Bindings = append(sec.Bindings, contextKey, entryNav)
		if !entry.question.HasInnerBack() {
			sec.Bindings = append(sec.Bindings, escapeKey)
		}
	case inputKindReview:
		if entry.workspace != nil && m.reviewOpen {
			for _, item := range entry.workspace.Help() {
				sec.Bindings = append(sec.Bindings, keybind.NewBinding(
					keybind.WithKeys(item.Key),
					keybind.WithHelp(item.Key, item.Description),
				))
			}
			break
		}
		if entry.workspace != nil {
			sec.Bindings = []keybind.Binding{m.keys.ReviewOpen, entryNav, escapeKey}
			break
		}
		switch {
		case entry.composing:
			sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, escapeKey}
		default:
			sec.Bindings = []keybind.Binding{m.keys.Verdict, contextKey, entryNav, escapeKey}
		}
	case inputKindPrompt:
		sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, entryNav, escapeKey}
	case inputKindRecovery:
		if entry.composing {
			sec.Bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, contextKey, escapeKey}
		} else {
			for _, action := range m.recoveryActions(entry.recovery) {
				sec.Bindings = append(sec.Bindings, action.binding)
			}
			sec.Bindings = append(sec.Bindings, contextKey, entryNav, escapeKey)
		}
	case inputKindIntegrationConflict:
		sec.Bindings = []keybind.Binding{m.keys.IntegrationResolve, m.keys.RecoverAbort, contextKey, entryNav, escapeKey}
		if entry.integration.CanAgentResolve {
			sec.Bindings = append(sec.Bindings, m.keys.IntegrationAgent)
		}
	case inputKindFinalMerge, inputKindHelpFinalMerge:
		sec.Bindings = []keybind.Binding{m.keys.FinalMergeApprove, m.keys.FinalMergeDiscard, contextKey, entryNav, escapeKey}
	case inputKindResetConfirm:
		sec.Bindings = []keybind.Binding{m.keys.ResetConfirm, m.keys.ResetCancel, contextKey, escapeKey}
	}
	return sec
}

// CapturesText reports whether free text belongs to an active editor rather than
// the root keymap.
func (m Model) CapturesText() bool { return m.searchOpen || m.textareaActive() }

// visibleRows builds the flat row list for the Steps panel. Only top-level
// steps (ordinary steps and foreach families) appear at the outer level — a
// family's runtime children are folded in only while the family is expanded,
// in source order; a child's own output files fold in only while the child
// itself is expanded. An ordinary step's files fold in the same way a family's
// children do: only while that step's row is expanded.
func (m Model) visibleRows() []visibleRow {
	var rows []visibleRow
	for _, s := range m.steps {
		if s.isChild() {
			continue // folded in beneath its family below, not at top level.
		}
		rows = append(rows, visibleRow{kind: "step", stepID: s.id})
		children := m.familyChildren[s.id]
		if len(children) > 0 {
			if !m.expanded[s.id] {
				continue
			}
			for _, childID := range children {
				rows = append(rows, visibleRow{kind: "child", stepID: childID})
				rows = append(rows, m.fileRowsFor(childID)...)
			}
			continue
		}
		rows = append(rows, m.fileRowsFor(s.id)...)
	}
	return rows
}

// fileRowsFor returns stepID's expanded, accessible output-file rows, or nil
// when stepID is collapsed or has no visible files.
func (m Model) fileRowsFor(stepID string) []visibleRow {
	if !m.expanded[stepID] {
		return nil
	}
	files := m.stepFiles[stepID]
	var rows []visibleRow
	for i, f := range files {
		if f.err != nil {
			continue
		}
		rows = append(rows, visibleRow{kind: "file", stepID: stepID, file: &files[i]})
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
	// Replayed journals have no scheduler handle. Do not advertise controls the
	// root would have to discard without feedback.
	if m.done || m.run == nil || m.cursorIsFileRow() {
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
		// Reset is a family-level operation only (A8): resetting one runtime
		// child is rejected by the scheduler (engine.ResetError{Code:
		// "fanout_child"}), so the Steps panel never advertises it as an
		// action on a child row — the operator resets the family instead.
		actions.canReset = !m.steps[i].isChild()
	}
	return actions
}
