package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
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
	inputKindRequest  pendingInputKind = iota // block_on InputRequest
	inputKindQuestion                         // AskUserQuestion AgentQuestion
	inputKindPrompt                           // from="user" PromptRequest
	inputKindReview                           // ReviewRequest (verdict + message)
)

// pendingInputEntry is one element of the persistent input queue. Exactly one
// payload pointer is non-nil, matching kind. Per-entry state (draft text,
// question progress, compose flag, scroll position) is preserved across queue
// navigation so the user can return to a partially-answered entry.
type pendingInputEntry struct {
	kind      pendingInputKind
	stepID    string
	toolUseID string // non-empty only for inputKindQuestion

	// Exactly one payload pointer is non-nil, matching kind.
	request  *engine.InputRequest
	question *engine.AgentQuestion
	prompt   *engine.PromptRequest
	review   *engine.ReviewRequest

	// draft is the in-progress textarea text (request/prompt, and review compose),
	// preserved across navigation.
	draft string

	// composing is true while composing a message on a review entry.
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

	// Phase 4: rolling output buffer per step (last outputMaxLines lines).
	stepOutput map[string]*strings.Builder

	vp     viewport.Model // Steps panel scroll
	chatVP viewport.Model // Transcript panel scroll — independent scroll position
	ready  bool

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

// panelSplit computes the two panels' outer widths for the given total width per
// Resolved Decision 11: Steps = max(32, width/3), clamped so the Transcript keeps
// an inner width of at least ~40; the Transcript takes the remainder. narrow
// reports that the terminal is too narrow for both panels to meet their minimums,
// so the caller should fall back to a single full-width focused panel.
func panelSplit(width int) (stepsW, transcriptW int, narrow bool) {
	hFrame, _ := panelFrame()
	// Both panels need at least their border frame; the Transcript additionally
	// needs transcriptMinInnerWidth inner cells. Below this the split is impossible.
	minTotal := stepsMinWidth + hFrame + transcriptMinInnerWidth
	if width < minTotal {
		return width, width, true
	}
	stepsW = width / 3
	if stepsW < stepsMinWidth {
		stepsW = stepsMinWidth
	}
	// Clamp so the Transcript keeps its minimum inner width.
	if maxSteps := width - (transcriptMinInnerWidth + hFrame); stepsW > maxSteps {
		stepsW = maxSteps
	}
	return stepsW, width - stepsW, false
}

// outputMaxLines is the number of streaming output lines shown per step.
const outputMaxLines = 10

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
	err     string // failure reason when status == StatusFailed
	subtype string // SDK result subtype for agent policy-limit failures
}

func newMonitorModel(runID string) monitorModel {
	return monitorModel{
		runID:        runID,
		keys:         defaultMonitorKeys(),
		index:        make(map[string]int),
		stepOutput:   make(map[string]*strings.Builder),
		msgCount:     make(map[string]int),
		chatExpand:   make(map[blockKey]bool),
		chatRendered: make(map[blockKey]string),
		reviews:      make(map[string]engine.ReviewRequest),
	}
}

// withSnapshot initialises the monitor from a RunSnapshot so the user sees
// current state immediately when navigating to an already-running run.
func (m monitorModel) withSnapshot(snap engine.RunSnapshot) monitorModel {
	m.workflow = snap.Workflow
	m.done = snap.Done
	m.failed = snap.Failed
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
		if st.Status == step.StatusFailed && st.Result != nil {
			ms.err = st.Result.Err
			ms.subtype = st.Result.Subtype
		}
		m.steps[i] = ms
		m.index[st.ID] = i
	}
	return m
}

func (m monitorModel) Update(msg tea.Msg) (monitorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case engineEventMsg:
		var evCmd tea.Cmd
		wasAtBottom := m.chatVP.AtBottom()
		m, evCmd = m.handleEngineEvent(msg.event)
		// The Transcript panel always shows the cursor's step; point it at the
		// first step as soon as the run's steps are known (eager reload).
		if m.chatStep == "" && m.cursor < len(m.steps) {
			m.reloadTranscript()
		}
		// Both panels are always visible, so refresh both on every event.
		if m.ready {
			m.vp.SetContent(m.listBody())
			m.chatVP.SetContent(m.chatBody())
			if wasAtBottom {
				m.chatVP.GotoBottom()
			}
		}
		return m, evCmd

	case tea.KeyPressMsg:
		// Focus-switch keys move keyboard focus between the present regions even
		// while a gate is pending — gates are non-blocking (ADR 0002). Handled
		// first so navigation is never frozen. In the Transcript panel the block
		// cursor moved off tab to n/N (Resolved Decision 9), so tab is unambiguous.
		switch {
		case keybind.Matches(msg, m.keys.FocusNext):
			m.focus = m.cycleFocus(+1)
			m.refreshPanels()
			return m, nil
		case keybind.Matches(msg, m.keys.FocusPrev):
			m.focus = m.cycleFocus(-1)
			m.refreshPanels()
			return m, nil
		case keybind.Matches(msg, m.keys.PanelFocus):
			// left/right alias for moving between the two side-by-side panels.
			m.focus = m.aliasPanelFocus(msg.String())
			m.refreshPanels()
			return m, nil
		}

		// Dispatch keys to the focused region first (ADR 0002): a focused gate
		// consumes its keys; only Steps-focus reads j/k as select and only
		// Transcript-focus reads them as scroll.
		switch m.focus {
		case focusGate:
			return m.updateGate(msg)
		case focusSteps:
			return m.updateSteps(msg)
		case focusTranscript:
			return m.updateTranscript(msg)
		}
	}

	// Route non-key messages to the textarea (blink timer, focus events) for entry
	// kinds that use the textarea (request, prompt, or a composing review entry).
	if entry, ok := m.activeEntry(); ok &&
		(entry.kind == inputKindRequest || entry.kind == inputKindPrompt ||
			(entry.kind == inputKindReview && entry.composing)) {
		var taCmd tea.Cmd
		m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
		m.refreshPanels()
		return m, taCmd
	}

	// Route remaining messages (scroll keys, mouse wheel) to the focused panel.
	var cmd tea.Cmd
	if m.focus == focusTranscript {
		m.chatVP, cmd = m.chatVP.Update(msg)
	} else {
		m.vp, cmd = m.vp.Update(msg)
	}
	return m, cmd
}

// hasGate reports whether the input queue has any pending entries.
func (m monitorModel) hasGate() bool {
	return len(m.inputQueue) > 0
}

// activeEntry returns a pointer to the entry at activeInputIdx, or (nil, false)
// when the queue is empty or the index is out of range.
func (m monitorModel) activeEntry() (*pendingInputEntry, bool) {
	if len(m.inputQueue) == 0 || m.activeInputIdx < 0 || m.activeInputIdx >= len(m.inputQueue) {
		return nil, false
	}
	return &m.inputQueue[m.activeInputIdx], true
}

// removeEntryAt deletes the entry at index i, then clamps/advances activeInputIdx:
// the next entry stays at position i (entries shift left); if the removed entry
// was last, activeInputIdx clamps to the new last; if the queue empties, focus
// returns to Steps.
func (m *monitorModel) removeEntryAt(i int) {
	if i < 0 || i >= len(m.inputQueue) {
		return
	}
	m.inputQueue = append(m.inputQueue[:i], m.inputQueue[i+1:]...)
	if len(m.inputQueue) == 0 {
		m.activeInputIdx = 0
		m.focus = focusSteps
		return
	}
	if m.activeInputIdx >= len(m.inputQueue) {
		m.activeInputIdx = len(m.inputQueue) - 1
	}
}

// refreshPanels re-renders both always-visible panels into their viewports. A
// no-op until the first resize makes the model ready.
func (m *monitorModel) refreshPanels() {
	if !m.ready {
		return
	}
	m.vp.SetContent(m.listBody())
	m.chatVP.SetContent(m.chatBody())
}

// cycleFocus advances focus by dir (+1 next, -1 previous) across the regions
// currently present: Steps and Transcript are always present; Gate only when a
// gate is pending. In the narrow single-panel fallback the two panels still
// cycle (toggling which one is shown), so both remain present.
func (m monitorModel) cycleFocus(dir int) focusRegion {
	regions := []focusRegion{focusSteps, focusTranscript}
	if m.hasGate() {
		regions = append(regions, focusGate)
	}
	cur := 0
	for i, r := range regions {
		if r == m.focus {
			cur = i
			break
		}
	}
	next := (cur + dir + len(regions)) % len(regions)
	return regions[next]
}

// aliasPanelFocus maps left/right to the two side-by-side panels: right focuses
// Transcript, left focuses Steps. From the Gate, left/right returns to a panel so
// the user can leave the gate the same way tab does.
func (m monitorModel) aliasPanelFocus(key string) focusRegion {
	if key == "right" {
		return focusTranscript
	}
	return focusSteps
}

// updateSteps handles keys when the Steps panel holds focus: j/k move the
// selection cursor (eagerly reloading the Transcript per Resolved Decision 10),
// and esc/q leave to the runs list.
func (m monitorModel) updateSteps(msg tea.KeyPressMsg) (monitorModel, tea.Cmd) {
	switch {
	case keybind.Matches(msg, m.keys.Down):
		if m.cursor < len(m.steps)-1 {
			m.cursor++
			m.ensureCursorVisible()
			m.reloadTranscript()
			m.refreshPanels()
		}
		return m, nil
	case keybind.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
			m.reloadTranscript()
			m.refreshPanels()
		}
		return m, nil
	case keybind.Matches(msg, m.keys.OpenTranscript):
		// enter/l cross into the Transcript panel (the step is already loaded).
		m.focus = focusTranscript
		m.refreshPanels()
		return m, nil
	case keybind.Matches(msg, m.keys.StepsLeave):
		return m, func() tea.Msg { return showRunsMsg{} }
	}
	// Other keys (scroll wheel, ctrl+d/u) scroll the Steps viewport.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// updateTranscript handles keys when the Transcript panel holds focus: n/N move
// the block cursor, enter/space toggle the cursored block, o toggles all, and
// h/esc return focus to the Steps panel. Remaining keys
// (j/k/ctrl+d/ctrl+u/pgup/pgdn) scroll the transcript viewport.
func (m monitorModel) updateTranscript(msg tea.KeyPressMsg) (monitorModel, tea.Cmd) {
	switch {
	case keybind.Matches(msg, m.keys.TransToSteps):
		m.focus = focusSteps
		m.refreshPanels()
		return m, nil
	case keybind.Matches(msg, m.keys.TransLeave):
		return m, func() tea.Msg { return showRunsMsg{} }
	case keybind.Matches(msg, m.keys.NextBlock):
		// Move the block cursor to the next collapsible block.
		if n := len(m.chatBlocks); n > 0 {
			m.chatBlockCursor = (m.chatBlockCursor + 1) % n
			m.refreshPanels()
		}
		return m, nil
	case keybind.Matches(msg, m.keys.PrevBlock):
		if n := len(m.chatBlocks); n > 0 {
			m.chatBlockCursor = (m.chatBlockCursor - 1 + n) % n
			m.refreshPanels()
		}
		return m, nil
	case keybind.Matches(msg, m.keys.Toggle):
		// Toggle the block under the cursor.
		if n := len(m.chatBlocks); n > 0 && m.chatBlockCursor < n {
			k := m.chatBlocks[m.chatBlockCursor]
			m.chatExpand[k] = !m.chatExpand[k]
			m.refreshPanels()
		}
		return m, nil
	case keybind.Matches(msg, m.keys.ExpandAll):
		// Expand/collapse everything in view at once.
		m.chatExpandAll = !m.chatExpandAll
		m.refreshPanels()
		return m, nil
	}
	// Other keys (j/k/ctrl+d/ctrl+u/pgup/pgdn) scroll the transcript viewport.
	var cmd tea.Cmd
	m.chatVP, cmd = m.chatVP.Update(msg)
	return m, cmd
}

// updateGate handles keys when the gate holds focus. Dispatches by the active
// entry's kind; each submit path reads routing IDs from the entry, emits the
// unchanged routing message, and removes the entry (auto-advance via removeEntryAt).
func (m monitorModel) updateGate(msg tea.KeyPressMsg) (monitorModel, tea.Cmd) {
	entry, ok := m.activeEntry()
	if !ok {
		return m, nil
	}

	switch entry.kind {
	case inputKindRequest:
		if keybind.Matches(msg, m.keys.InputLeave) {
			return m, func() tea.Msg { return showRunsMsg{} }
		}
		if keybind.Matches(msg, m.keys.Submit) {
			text := m.promptTextarea.Value()
			if text == "" {
				return m, nil
			}
			inp := entry.request
			m.removeEntryAt(m.activeInputIdx)
			m.promptTextarea = textarea.Model{}
			m.refreshPanels()
			return m, func() tea.Msg {
				return agentInputMsg{runID: inp.RunID, stepID: inp.StepID, text: text}
			}
		}
		var taCmd tea.Cmd
		m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
		m.refreshPanels()
		return m, taCmd

	case inputKindQuestion:
		if keybind.Matches(msg, m.keys.QuestionCancel) {
			// Deliver a cancellation answer so the blocked reporter goroutine
			// unblocks and Claude receives a tool_result instead of hanging.
			q := entry.question
			m.removeEntryAt(m.activeInputIdx)
			return m, tea.Batch(
				func() tea.Msg {
					return agentQuestionResponseMsg{
						runID:     q.RunID,
						stepID:    q.StepID,
						toolUseID: q.ToolUseID,
						answer:    "cancelled",
					}
				},
				func() tea.Msg { return showRunsMsg{} },
			)
		}
		idx := m.activeInputIdx
		if m.inputQueue[idx].questionIdx < len(entry.question.Questions) {
			q := entry.question.Questions[m.inputQueue[idx].questionIdx]
			if q.MultiSelect {
				for i := range q.Options {
					if msg.String() == fmt.Sprintf("%d", i+1) {
						m.inputQueue[idx].questionSelected[i] = !m.inputQueue[idx].questionSelected[i]
						m.refreshPanels()
						return m, nil
					}
				}
				if keybind.Matches(msg, m.keys.QConfirm) {
					var selected []string
					for i, opt := range q.Options {
						if m.inputQueue[idx].questionSelected[i] {
							selected = append(selected, opt.Label)
						}
					}
					if len(selected) == 0 {
						return m, nil
					}
					return m.advanceQuestion(strings.Join(selected, ", "))
				}
			} else {
				for i, opt := range q.Options {
					if msg.String() == fmt.Sprintf("%d", i+1) {
						return m.advanceQuestion(opt.Label)
					}
				}
			}
		}
		return m, nil

	case inputKindPrompt:
		if keybind.Matches(msg, m.keys.Submit) {
			text := m.promptTextarea.Value()
			if text == "" {
				return m, nil
			}
			pr := entry.prompt
			m.removeEntryAt(m.activeInputIdx)
			m.promptTextarea = textarea.Model{}
			m.refreshPanels()
			return m, func() tea.Msg {
				return userInputResponseMsg{
					runID:  pr.RunID,
					stepID: pr.StepID,
					as:     pr.As,
					text:   text,
				}
			}
		}
		var taCmd tea.Cmd
		m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
		m.refreshPanels()
		return m, taCmd

	case inputKindReview:
		if entry.composing {
			if keybind.Matches(msg, m.keys.ComposeCancel) {
				m.inputQueue[m.activeInputIdx].composing = false
				m.promptTextarea = textarea.Model{}
				m.refreshPanels()
				return m, nil
			}
			if keybind.Matches(msg, m.keys.Submit) {
				text := m.promptTextarea.Value()
				if text == "" {
					return m, nil
				}
				rev := entry.review
				m.removeEntryAt(m.activeInputIdx)
				m.promptTextarea = textarea.Model{}
				m.refreshPanels()
				return m, func() tea.Msg {
					return reviewMessageMsg{runID: rev.RunID, stepID: rev.StepID, text: text}
				}
			}
			var taCmd tea.Cmd
			m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
			m.refreshPanels()
			return m, taCmd
		}
		if entry.review.AllowMessage && keybind.Matches(msg, m.keys.Message) {
			m.inputQueue[m.activeInputIdx].composing = true
			m.promptTextarea = newInputTextarea("Message to agent…", m.gateInnerWidth(), 4)
			m.refreshPanels()
			return m, textarea.Blink
		}
		for i, ch := range entry.review.Choices {
			if msg.String() == fmt.Sprintf("%d", i+1) {
				rev := entry.review
				m.removeEntryAt(m.activeInputIdx)
				m.refreshPanels()
				return m, func() tea.Msg {
					return reviewVerdictMsg{runID: rev.RunID, stepID: rev.StepID, verdict: ch}
				}
			}
		}
		if keybind.Matches(msg, m.keys.ReviewLeave) {
			return m, func() tea.Msg { return showRunsMsg{} }
		}
		return m, nil
	}

	return m, nil
}

// reloadTranscript re-points the Transcript panel at the cursor's step and reads
// its transcript eagerly (Resolved Decision 10), resetting per-step view state so
// block-cursor/expand toggles never carry over between steps (seq keys are only
// meaningful within one step's transcript).
func (m *monitorModel) reloadTranscript() {
	if m.cursor >= len(m.steps) {
		return
	}
	id := m.steps[m.cursor].id
	if id == m.chatStep {
		return
	}
	m.chatStep = id
	m.chatBlockCursor = 0
	m.chatExpandAll = false
	m.chatExpand = make(map[blockKey]bool)
	// blockKey is (seq, block) and seq restarts per step-file, so cached renders
	// from the previous step would collide with the new step's same-seq blocks.
	// Reset the render cache along with the other per-step view state.
	m.chatRendered = make(map[blockKey]string)
	m.loadChat()
	if m.ready {
		m.chatVP.GotoTop()
	}
}

func (m monitorModel) handleEngineEvent(e engine.Event) (monitorModel, tea.Cmd) {
	switch ev := e.(type) {
	case engine.RunStarted:
		if ev.RunID != m.runID {
			return m, nil
		}
		m.workflow = ev.Workflow
		m.steps = make([]monitorStep, len(ev.Steps))
		m.index = make(map[string]int, len(ev.Steps))
		for i, id := range ev.Steps {
			m.steps[i] = monitorStep{id: id, status: step.StatusPending}
			m.index[id] = i
		}

	case engine.StepStatus:
		if ev.RunID != m.runID {
			return m, nil
		}
		i, ok := m.index[ev.StepID]
		if !ok {
			return m, nil
		}
		m.steps[i].status = ev.To
		if ev.To == step.StatusFailed {
			m.steps[i].err = ev.Err
			m.steps[i].subtype = ev.Subtype
		}
		if ev.To == step.StatusRunning {
			m.steps[i].start = time.Now()
		}
		if ev.To == step.StatusSucceeded || ev.To == step.StatusFailed || ev.To == step.StatusSkipped {
			m.steps[i].end = time.Now()
		}
		// Remove every queue entry for a step that is no longer blocked. Prune on any
		// transition away from StatusNeedsInput, and on all terminal transitions.
		if ev.To != step.StatusNeedsInput {
			for i := len(m.inputQueue) - 1; i >= 0; i-- {
				if m.inputQueue[i].stepID == ev.StepID {
					m.removeEntryAt(i)
				}
			}
			// If the pruned step had an active textarea entry, clear the textarea.
			if len(m.inputQueue) == 0 {
				m.promptTextarea = textarea.Model{}
			}
		}

	case engine.ReviewRequest:
		if ev.RunID != m.runID {
			return m, nil
		}
		// Retain the request so the Transcript panel can show the diff when the step
		// is selected (Unit 5), even after the queue entry is answered.
		if m.reviews == nil {
			m.reviews = make(map[string]engine.ReviewRequest)
		}
		m.reviews[ev.StepID] = ev
		// Append a queue entry. Decision 6: no focus steal on arrival.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:   inputKindReview,
			stepID: ev.StepID,
			review: &evCopy,
		})

	case engine.InputRequest:
		if ev.RunID != m.runID {
			return m, nil
		}
		// Decision 6: no focus steal on arrival.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:    inputKindRequest,
			stepID:  ev.StepID,
			request: &evCopy,
		})

	case engine.AgentQuestion:
		if ev.RunID != m.runID {
			return m, nil
		}
		// Update the step badge immediately — the scheduler inbox notification
		// may be dropped under load, so drive the display from this reliable event.
		if idx, ok := m.index[ev.StepID]; ok {
			m.steps[idx].status = step.StatusNeedsInput
		}
		// Decision 6: no focus steal on arrival.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:             inputKindQuestion,
			stepID:           ev.StepID,
			toolUseID:        ev.ToolUseID,
			question:         &evCopy,
			questionSelected: make(map[int]bool),
		})

	case engine.StepMessage:
		if ev.RunID != m.runID {
			return m, nil
		}
		if m.msgCount == nil {
			m.msgCount = make(map[string]int)
		}
		if ev.Seq > m.msgCount[ev.StepID] {
			m.msgCount[ev.StepID] = ev.Seq
		}
		// A StepMessage means a message was just finalized to the transcript, so
		// the live-typing tail for that step is now on disk: reset it (the next
		// deltas belong to the next, not-yet-finalized bubble). If that step's
		// chat is open, re-read the transcript so the finalized entry appears.
		if buf, ok := m.stepOutput[ev.StepID]; ok {
			buf.Reset()
		}
		// The Transcript panel always shows the cursor's step, so re-read whenever
		// the finalized entry belongs to it.
		if ev.StepID == m.chatStep {
			m.loadChat()
		}

	case engine.PromptRequest:
		if ev.RunID != m.runID {
			return m, nil
		}
		// Decision 6: no focus steal on arrival.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:   inputKindPrompt,
			stepID: ev.StepID,
			prompt: &evCopy,
		})

	case engine.StepOutput:
		if ev.RunID != m.runID {
			return m, nil
		}
		if m.stepOutput == nil {
			m.stepOutput = make(map[string]*strings.Builder)
		}
		buf, ok := m.stepOutput[ev.StepID]
		if !ok {
			buf = &strings.Builder{}
			m.stepOutput[ev.StepID] = buf
		}
		buf.WriteString(ev.Delta)

	case engine.RunError:
		if ev.RunID != m.runID {
			return m, nil
		}
		// Engine-level failures (worktree setup, max_iterations) are not tied to a
		// single step's transition, so they would otherwise vanish. Retain the
		// most recent one for the summary.
		m.runErr = ev.Err

	case engine.RunFinished:
		if ev.RunID != m.runID {
			return m, nil
		}
		m.done = true
		m.failed = ev.Failed
	}
	return m, nil
}

// resize fits both panels to the width split (Resolved Decision 11) minus the
// footer and gate-strip rows. In the narrow fallback (Decision 14) each panel is
// sized full-width, since only the focused one renders.
func (m *monitorModel) resize() {
	hFrame, vFrame := panelFrame()

	footerH := lipgloss.Height(m.footerView())
	gateH := lipgloss.Height(m.gateStrip())
	panelH := m.height - footerH - gateH
	if panelH < 1 {
		panelH = 1
	}
	innerH := panelH - vFrame
	if innerH < 1 {
		innerH = 1
	}

	stepsOuter, transcriptOuter, narrow := panelSplit(m.width)
	m.narrow = narrow
	if narrow {
		// Only the focused panel renders, full-width; size both to the full width
		// so whichever is shown fits.
		stepsOuter, transcriptOuter = m.width, m.width
	}
	m.stepsInnerW = stepsOuter - hFrame
	if m.stepsInnerW < 1 {
		m.stepsInnerW = 1
	}
	m.transcriptInnerW = transcriptOuter - hFrame
	if m.transcriptInnerW < 1 {
		m.transcriptInnerW = 1
	}

	if !m.ready {
		m.vp = viewport.New(viewport.WithWidth(m.stepsInnerW), viewport.WithHeight(innerH))
		m.chatVP = viewport.New(viewport.WithWidth(m.transcriptInnerW), viewport.WithHeight(innerH))
		// Content is word-wrapped to each panel's inner width; horizontal
		// scrolling only ever cuts left characters off rendered lines.
		m.vp.KeyMap.Left.Unbind()
		m.vp.KeyMap.Right.Unbind()
		m.chatVP.KeyMap.Left.Unbind()
		m.chatVP.KeyMap.Right.Unbind()
		m.ready = true
	} else {
		m.vp.SetWidth(m.stepsInnerW)
		m.vp.SetHeight(innerH)
		m.chatVP.SetWidth(m.transcriptInnerW)
		m.chatVP.SetHeight(innerH)
	}
	if entry, ok := m.activeEntry(); ok &&
		(entry.kind == inputKindRequest || entry.kind == inputKindPrompt ||
			(entry.kind == inputKindReview && entry.composing)) {
		m.promptTextarea.SetWidth(m.gateInnerWidth())
	}
	m.rebuildRenderer()
	m.vp.SetContent(m.listBody())
	m.chatVP.SetContent(m.chatBody())
}

// gateInnerWidth is the content width available to a gate strip's textarea: the
// full terminal width minus the textarea's own border frame.
func (m monitorModel) gateInnerWidth() int {
	w := m.width - theme.Textarea.Base.GetHorizontalFrameSize()
	if w < 1 {
		w = 1
	}
	return w
}

// rebuildRenderer (re)constructs the markdown renderer for the *Transcript
// panel's* inner width — the transcript occupies only part of the terminal, so
// wrapping to the full width would overflow the panel. It invalidates the
// per-block render cache when that inner width changes: glamour bakes its
// word-wrap width in at construction, so a stale cache would wrap to the old
// width (see turn.go / viewer.go for the same house rule). A static style (not
// AutoStyle) avoids the OSC-11 stdin race documented in main.go.
func (m *monitorModel) rebuildRenderer() {
	wordWrap := m.transcriptInnerW
	if wordWrap < 1 {
		wordWrap = 1
	}
	m.renderer, _ = glamour.NewTermRenderer(
		glamour.WithStyles(theme.Markdown),
		glamour.WithWordWrap(wordWrap),
	)
	if m.lastTranscriptW != m.transcriptInnerW {
		m.chatRendered = make(map[blockKey]string)
		m.lastTranscriptW = m.transcriptInnerW
	}
}

// ensureCursorVisible nudges the viewport so the selected step stays on screen
// as the cursor moves, keeping a small margin from the top and bottom edges. The
// step rows now start at line 0 (the panel border carries the "Steps" title), so
// the cursor index maps directly to a viewport row.
func (m *monitorModel) ensureCursorVisible() {
	if !m.ready {
		return
	}
	row := listBodyHeaderLines + m.cursor
	const margin = 2
	top := m.vp.YOffset()
	bottom := top + m.vp.Height() - 1
	switch {
	case row-margin < top:
		m.vp.SetYOffset(row - margin)
	case row+margin > bottom:
		m.vp.SetYOffset(row + margin - m.vp.Height() + 1)
	}
}

// body returns the Steps-panel body. Retained for tests that assert list content
// directly; View() renders both panels. transcriptText returns the Transcript
// panel body.
func (m monitorModel) body() string {
	return m.listBody()
}

// listBodyHeaderLines is the number of lines listBody renders above the step
// table. The workflow/run header moved to the panel border, so the table now
// starts at line 0; this stays 0 to keep the ensureCursorVisible row math honest.
const listBodyHeaderLines = 0

func (m monitorModel) listBody() string {
	var b strings.Builder

	if len(m.steps) == 0 {
		b.WriteString("  " + theme.Question.Render("Waiting for run to start…") + "\n")
		return b.String()
	}

	idWidth := 2
	for _, s := range m.steps {
		if len(s.id) > idWidth {
			idWidth = len(s.id)
		}
	}

	for i, s := range m.steps {
		cursor := "  "
		if i == m.cursor {
			cursor = theme.SelectedBar.Render(CursorBar) + " "
		}
		indicator, style := stepIndicator(s.status)
		dur := stepDuration(s)
		msgs := ""
		if n := m.msgCount[s.id]; n > 0 {
			msgs = theme.Question.Render(fmt.Sprintf("%d msg", n))
		}
		line := fmt.Sprintf("%s%s  %s  %s  %s  %s",
			cursor,
			indicator,
			style.Render(padRight(s.id, idWidth)),
			statusStyle(s.status).Render(fmt.Sprintf("%-16s", string(s.status))),
			theme.Question.Render(padRight(dur, 10)),
			msgs,
		)
		if label := subtypeBadgeLabel(s.subtype); label != "" {
			line += "  " + theme.Badge.Error.Render(label)
		}
		if i == m.cursor {
			b.WriteString(theme.SelectedLine.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	if m.done {
		if m.failed {
			b.WriteString("  " + theme.Error.Render(IconError+" run failed") + "\n")
			m.writeFailureReasons(&b)
		} else {
			b.WriteString("  " + theme.Valid.Render(IconSuccess+" run complete") + "\n")
		}
	}

	// Human-in-the-loop gates (review/input/question/prompt) render in the
	// gate strip beneath the panels (gateStrip), not inline here — the strip is a
	// non-blocking focus region (ADR 0002).

	// Streaming output: show last outputMaxLines lines for any running agent step.
	for _, s := range m.steps {
		if s.status != step.StatusRunning {
			continue
		}
		buf, ok := m.stepOutput[s.id]
		if !ok || buf.Len() == 0 {
			continue
		}
		lines := strings.Split(buf.String(), "\n")
		// Keep only the last outputMaxLines non-empty lines.
		var recent []string
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				recent = append(recent, l)
			}
		}
		if len(recent) > outputMaxLines {
			recent = recent[len(recent)-outputMaxLines:]
		}
		b.WriteString("\n  " + theme.Question.Render("▸ "+s.id) + "\n")
		for _, l := range recent {
			b.WriteString("    " + l + "\n")
		}
	}

	return b.String()
}

// writeFailureReasons appends the human-readable "why" behind a failed run: the
// engine-level error (if any), then each failed step's reason. Without this the
// summary only says "✗ run failed" — the reason lives in the events but was
// never rendered. Reasons wrap to the view width so a long shell error or gate
// detail stays readable.
func (m monitorModel) writeFailureReasons(b *strings.Builder) {
	wrapW := m.width - 6
	if wrapW < 20 {
		wrapW = 20
	}
	wrap := lipgloss.NewStyle().Width(wrapW)

	if m.runErr != "" {
		b.WriteString("    " + theme.Error.Render(wrap.Render("engine: "+m.runErr)) + "\n")
	}
	for _, s := range m.steps {
		if s.status != step.StatusFailed || s.err == "" {
			continue
		}
		b.WriteString("    " + theme.Error.Render(wrap.Render(s.id+": "+s.err)) + "\n")
	}
}

// gateStrip renders the currently-active human-in-the-loop gate entry as a
// full-width titled panel beneath the two panels, above the footer. It is a
// non-blocking focus region (ADR 0002): its border is primary only when the Gate
// holds focus. Returns "" when no gate is pending (empty queue). The full
// per-kind rendering and fixed-height layout land in tasks 2.0–4.0; this version
// preserves the prior single-entry rendering semantics via activeEntry().
func (m monitorModel) gateStrip() string {
	if !m.hasGate() {
		return ""
	}
	_, vFrame := panelFrame()

	entry, _ := m.activeEntry()
	var title string
	var b strings.Builder

	if entry != nil {
		switch entry.kind {
		case inputKindRequest:
			title = "Agent input — " + entry.stepID
			b.WriteString(m.promptTextarea.View())

		case inputKindQuestion:
			if entry.questionIdx < len(entry.question.Questions) {
				q := entry.question.Questions[entry.questionIdx]
				title = "Agent question — " + entry.stepID
				if q.Header != "" {
					b.WriteString("  " + theme.Question.Render("["+q.Header+"]") + "\n")
				}
				b.WriteString("  " + q.Question + "\n\n")
				for i, opt := range q.Options {
					if q.MultiSelect {
						mark := "[ ]"
						if entry.questionSelected[i] {
							mark = "[x]"
						}
						b.WriteString(fmt.Sprintf("    %s [%d] %s", mark, i+1, opt.Label))
					} else {
						b.WriteString(fmt.Sprintf("    [%d] %s", i+1, opt.Label))
					}
					if opt.Description != "" {
						b.WriteString("  —  " + opt.Description)
					}
					b.WriteString("\n")
				}
				if q.MultiSelect {
					b.WriteString("\n    " + theme.Chat.Hint.Render("enter to confirm selection") + "\n")
				}
				if len(entry.question.Questions) > 1 {
					b.WriteString("    " + theme.Chat.Hint.Render(
						fmt.Sprintf("question %d of %d", entry.questionIdx+1, len(entry.question.Questions))) + "\n")
				}
			}

		case inputKindPrompt:
			title = "Input — " + entry.stepID
			b.WriteString("  " + theme.Question.Render(entry.prompt.Label) + "\n\n")
			b.WriteString(m.promptTextarea.View())

		case inputKindReview:
			title = "Review — " + entry.stepID
			if entry.review.Diff != "" {
				writeDiff(&b, entry.review.Diff)
				b.WriteString("\n")
			}
			if entry.composing {
				b.WriteString(m.promptTextarea.View())
			} else {
				for i, ch := range entry.review.Choices {
					b.WriteString(fmt.Sprintf("    [%d] %s\n", i+1, ch))
				}
				if entry.review.AllowMessage {
					b.WriteString("    [m] message\n")
				}
			}
		}
	}

	// Fit the body height to its natural content, bounded so a huge diff never
	// pushes the panels off screen. The panel height is body height + frame.
	body := b.String()
	bodyH := lipgloss.Height(body)
	const maxGateBodyH = 14
	if bodyH > maxGateBodyH {
		bodyH = maxGateBodyH
	}
	if bodyH < 1 {
		bodyH = 1
	}
	return panel(title, body, m.width, bodyH+vFrame, m.focus == focusGate)
}

// advanceQuestion records the answer for the current question and advances the
// question index on the active entry. When all questions are answered it removes
// the entry and emits agentQuestionResponseMsg with the formatted answer.
func (m monitorModel) advanceQuestion(answer string) (monitorModel, tea.Cmd) {
	idx := m.activeInputIdx
	if idx < 0 || idx >= len(m.inputQueue) || m.inputQueue[idx].kind != inputKindQuestion {
		return m, nil
	}
	m.inputQueue[idx].questionAnswers = append(m.inputQueue[idx].questionAnswers, answer)
	m.inputQueue[idx].questionIdx++
	m.inputQueue[idx].questionSelected = make(map[int]bool)

	if m.inputQueue[idx].questionIdx < len(m.inputQueue[idx].question.Questions) {
		m.refreshPanels()
		return m, nil
	}

	q := m.inputQueue[idx].question
	answers := m.inputQueue[idx].questionAnswers
	m.removeEntryAt(idx)
	m.refreshPanels()
	formatted := formatQuestionAnswers(q.Questions, answers)
	return m, func() tea.Msg {
		return agentQuestionResponseMsg{
			runID:     q.RunID,
			stepID:    q.StepID,
			toolUseID: q.ToolUseID,
			answer:    formatted,
		}
	}
}

// formatQuestionAnswers encodes the human's selections as a JSON payload that
// captureStream sends back to Claude as the AskUserQuestion tool result.
func formatQuestionAnswers(questions []engine.AgentQuestionItem, answers []string) string {
	m := make(map[string]string, len(questions))
	for i, q := range questions {
		if i < len(answers) {
			m[q.Question] = answers[i]
		}
	}
	b, err := json.Marshal(map[string]any{"answers": m})
	if err != nil {
		return strings.Join(answers, ", ")
	}
	return string(b)
}

// loadChat re-reads chatStep's transcript into the model. The transcript file is
// the source of truth (not the lossy event bus); we read only the trailing
// chatWindowMax entries so a long run stays bounded, and flatten the collapsible
// blocks so the block cursor and expand toggles have a stable index. Safe to
// call while the writer appends: the reader opens, reads to EOF, and closes.
func (m *monitorModel) loadChat() {
	m.chatEntries = nil
	m.chatBlocks = nil
	m.chatElided = 0
	if m.runDir == "" || m.chatStep == "" {
		return
	}
	r, err := transcript.Open(datastore.TranscriptPath(m.runDir, m.chatStep))
	if err != nil {
		return
	}
	count, err := r.Count()
	if err != nil {
		return
	}
	offset := 0
	if count > chatWindowMax {
		offset = count - chatWindowMax
		m.chatElided = offset
	}
	entries, err := r.Window(offset, chatWindowMax)
	if err != nil {
		return
	}
	m.chatEntries = entries
	for _, e := range entries {
		for bi, blk := range e.Blocks {
			if collapsible(blk.Type) {
				m.chatBlocks = append(m.chatBlocks, blockKey{seq: e.Seq, block: bi})
			}
		}
	}
	if m.chatBlockCursor >= len(m.chatBlocks) {
		m.chatBlockCursor = 0
	}
}

// collapsible reports whether a block type is collapsed to chatCollapseWidth by
// default (and thus a target for the block cursor). Text renders in full as
// markdown; unknown types render as an inert placeholder.
func collapsible(t transcript.BlockType) bool {
	switch t {
	case transcript.BlockThinking, transcript.BlockToolUse, transcript.BlockToolResult:
		return true
	default:
		return false
	}
}

// chatBody renders one step's agent chat chain from its transcript: assistant
// text (markdown), reasoning, tool calls with inputs, and tool results, with
// large blocks collapsed to chatCollapseWidth and iteration/retry separators.
func (m monitorModel) chatBody() string {
	var b strings.Builder

	// The step id now titles the Transcript panel border, so no in-body title
	// row (task 2.4). A compact status line gives at-a-glance state.
	i, ok := m.index[m.chatStep]
	if !ok {
		if m.chatStep == "" {
			b.WriteString("  " + theme.Question.Render("select a step") + "\n")
		} else {
			b.WriteString("  " + theme.Question.Render("no such step") + "\n")
		}
		return b.String()
	}
	s := m.steps[i]
	indicator, _ := stepIndicator(s.status)
	b.WriteString("  " + indicator + "  " +
		statusStyle(s.status).Render(string(s.status)) + "\n\n")

	running := s.status == step.StatusRunning
	hasTail := false
	if buf, ok := m.stepOutput[m.chatStep]; ok && buf.Len() > 0 {
		hasTail = true
	}

	if len(m.chatEntries) == 0 {
		// Review steps have no transcript; drilling in shows the diff and choices
		// instead of a dead end (Phase 6).
		if rev, ok := m.reviews[m.chatStep]; ok {
			if rev.Diff != "" {
				writeDiff(&b, rev.Diff)
				b.WriteString("\n")
			}
			for i, ch := range rev.Choices {
				b.WriteString(fmt.Sprintf("    [%d] %s\n", i+1, ch))
			}
			return b.String()
		}
		if m.runDir == "" {
			b.WriteString("  " + theme.Question.Render("transcript unavailable (persistence off)") + "\n")
		} else if !running && !hasTail {
			b.WriteString("  " + theme.Question.Render("no output yet") + "\n")
		}
	}

	if m.chatElided > 0 {
		b.WriteString("  " + theme.Chat.Hint.Render(
			fmt.Sprintf("… %d earlier message(s) elided", m.chatElided)) + "\n\n")
	}

	lastIter, lastAttempt := -1, -1
	for _, e := range m.chatEntries {
		if lastIter != -1 && e.Iteration > lastIter {
			b.WriteString("\n  " + theme.Marker.Render(
				fmt.Sprintf("── iteration %d ──", e.Iteration+1)) + "\n\n")
		}
		if lastAttempt != -1 && e.Attempt > lastAttempt {
			b.WriteString("\n  " + theme.Marker.Render(
				fmt.Sprintf("── retry %d ──", e.Attempt)) + "\n\n")
		}
		lastIter, lastAttempt = e.Iteration, e.Attempt

		b.WriteString("  " + theme.Chat.Hint.Render(fmt.Sprintf("#%d %s", e.Seq, e.Role)) + "\n")
		for bi, blk := range e.Blocks {
			key := blockKey{seq: e.Seq, block: bi}
			m.writeBlock(&b, key, blk, e.Role)
		}
		b.WriteString("\n")
	}

	// Live tail: the current, not-yet-finalized bubble. Reset on each
	// StepMessage, so it shows only deltas past the last finalized entry.
	if running && hasTail {
		b.WriteString("  " + theme.Question.Render("typing…") + "\n")
		tail := m.stepOutput[m.chatStep].String()
		lines := strings.Split(tail, "\n")
		if len(lines) > outputMaxLines {
			lines = lines[len(lines)-outputMaxLines:]
		}
		for _, l := range lines {
			b.WriteString("  " + l + "\n")
		}
	}

	return b.String()
}

// writeBlock renders one transcript block. Assistant text is markdown (cached);
// system text (command output) is shown verbatim so terminal output is not
// reflowed as prose; thinking, tool_use, and tool_result collapse to
// chatCollapseWidth until expanded.
func (m monitorModel) writeBlock(b *strings.Builder, key blockKey, blk transcript.Block, role transcript.Role) {
	switch blk.Type {
	case transcript.BlockText:
		if role == transcript.RoleSystem {
			writeVerbatim(b, blk.Text)
			return
		}
		b.WriteString(m.renderMarkdown(key, blk.Text))
	case transcript.BlockThinking:
		m.writeCollapsible(b, key, theme.Chat.Thinking, theme.Chat.BarThinking, IconThinking+" reasoning", blk.Text, "", false, blk.Truncated)
	case transcript.BlockToolUse:
		m.writeCollapsible(b, key, theme.Chat.ToolCall, theme.Chat.BarToolCall, IconToolCall+" "+blk.Name, string(blk.Input), fenceJSON(string(blk.Input)), false, false)
	case transcript.BlockToolResult:
		m.writeCollapsible(b, key, theme.Chat.ToolResult, theme.Chat.BarToolResult, IconToolResult+" result", blk.Content, fenceJSON(blk.Content), blk.IsError, blk.Truncated)
	default:
		b.WriteString("  " + theme.Question.Render("[unsupported block: "+string(blk.Type)+"]") + "\n")
	}
}

// renderMarkdown renders a text block as markdown, caching the result per block.
// The cache map is shared across the value copies of monitorModel, so writing to
// it here persists even though the receiver is by value; the map is invalidated
// wholesale on a width change (rebuildRenderer).
func (m monitorModel) renderMarkdown(key blockKey, text string) string {
	if cached, ok := m.chatRendered[key]; ok {
		return cached
	}
	out := text
	if m.renderer != nil {
		if r, err := m.renderer.Render(text); err == nil {
			out = r
		}
	}
	if m.chatRendered != nil {
		m.chatRendered[key] = out
	}
	return out
}

// fenceJSON pretty-prints s as a ```json fenced markdown block so it can be
// rendered with syntax highlighting via renderMarkdown. Returns "" when s is
// not valid JSON so callers can fall back to plain text.
func fenceJSON(s string) string {
	s = expandView(s)
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return ""
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return "```json\n" + string(pretty) + "\n```"
}

// writeCollapsible renders one collapsible block: a role-colored left bar ("▌")
// and a labelled header with a ▸/▾ affordance, then either a one-line preview
// clipped to chatCollapseWidth or the bounded full content (also bar-accented).
// The block under the chat cursor is highlighted so the expand target is
// obvious; error results take theme.Error and the danger bar.
func (m monitorModel) writeCollapsible(b *strings.Builder, key blockKey, labelStyle, barStyle lipgloss.Style, label, content, formattedContent string, isError, truncated bool) {
	expanded := m.chatExpandAll || m.chatExpand[key]
	cursored := len(m.chatBlocks) > 0 && m.chatBlockCursor < len(m.chatBlocks) &&
		m.chatBlocks[m.chatBlockCursor] == key

	marker := CollapsedMarker
	if expanded {
		marker = ExpandedMarker
	}
	head := labelStyle
	bar := barStyle
	if isError {
		head = theme.Error
		bar = theme.Chat.BarError
	}
	barGlyph := bar.Render(BarThick)
	if cursored {
		b.WriteString("  " + barGlyph + " " + theme.Chat.BlockCursor.Render(marker+" "+label))
	} else {
		b.WriteString("  " + barGlyph + " " + marker + " " + head.Render(label))
	}

	if !expanded {
		shown, clipped := collapseLine(content)
		if shown != "" {
			b.WriteString("  " + theme.Question.Render(shown))
		}
		if clipped || truncated {
			b.WriteString(theme.Chat.Hint.Render(
				fmt.Sprintf(" [%d chars]", utf8.RuneCountInString(content))))
		}
		b.WriteString("\n")
		return
	}

	b.WriteString("\n")
	if formattedContent != "" {
		b.WriteString(withBar(bar, m.renderMarkdown(key, formattedContent)))
	} else {
		var body strings.Builder
		for _, l := range strings.Split(expandView(content), "\n") {
			body.WriteString(theme.Question.Render(l) + "\n")
		}
		b.WriteString(withBar(bar, body.String()))
	}
	if truncated {
		b.WriteString("    " + theme.Chat.Hint.Render("… (truncated at write)") + "\n")
	}
}

// withBar prefixes every line of content with a role-colored thick bar ("▌"),
// crush's signature block affordance. content may already carry ANSI styling
// (e.g. glamour output); the bar is emitted before each line's styling begins so
// nested SGR resets never clear it.
func withBar(style lipgloss.Style, content string) string {
	bar := style.Render(BarThick)
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		b.WriteString("  " + bar + " " + l + "\n")
	}
	return b.String()
}

// collapseLine flattens s to a single line and clips it to chatCollapseWidth
// runes, reporting whether it was clipped. Interior whitespace is collapsed so a
// multi-line block previews as one tidy line.
func collapseLine(s string) (string, bool) {
	flat := strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(flat) <= chatCollapseWidth {
		return flat, false
	}
	return string([]rune(flat)[:chatCollapseWidth]), true
}

// expandView bounds a block's expanded content: below chatExpandMax it returns
// the content unchanged; above it, a head+tail with the middle elided so a
// write-capped 256 KiB result never lays out in full.
func expandView(s string) string {
	if len(s) <= chatExpandMax {
		return s
	}
	half := chatExpandMax / 2
	head := clampRunes(s[:half])
	tail := clampRunesTail(s[len(s)-half:])
	elided := len(s) - len(head) - len(tail)
	return head + fmt.Sprintf("\n… %d KB elided …\n", elided/1024) + tail
}

// clampRunes / clampRunesTail back a byte slice off to a rune boundary so
// expandView never splits a multibyte rune at the elision seam.
func clampRunes(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func clampRunesTail(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}

// writeDiff renders a unified diff with +/-/@@ lines styled and indented two
// spaces, truncated to maxDiffLines. Shared by the review overlay in the list
// view and the review-step drill-in in the chat view (Phase 6).
func writeDiff(b *strings.Builder, diff string) {
	b.WriteString("  " + theme.Question.Render("── diff ─────────────────────────────") + "\n")
	lines := strings.Split(diff, "\n")
	const maxDiffLines = 200
	truncated := len(lines) > maxDiffLines
	if truncated {
		lines = lines[:maxDiffLines]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			b.WriteString("  " + theme.Diff.Add.Render(line) + "\n")
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			b.WriteString("  " + theme.Diff.Remove.Render(line) + "\n")
		case strings.HasPrefix(line, "@@"):
			b.WriteString("  " + theme.Diff.Hunk.Render(line) + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}
	if truncated {
		b.WriteString("  " + theme.Question.Render("… diff truncated") + "\n")
	}
}

// writeVerbatim renders text as-is, one indented line per line, without markdown
// reflow. Used for command-step output (role system), where the content is
// terminal output that glamour would mangle (Phase 6).
func writeVerbatim(b *strings.Builder, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
}

func stepIndicator(s step.Status) (string, lipgloss.Style) {
	switch s {
	case step.StatusPending:
		return "○", theme.Question
	case step.StatusRunning:
		return "●", theme.Running
	case step.StatusSucceeded:
		return "✓", theme.Valid
	case step.StatusFailed:
		return "✗", theme.Error
	case step.StatusSkipped:
		return "—", theme.Question
	case step.StatusValidating:
		return "⇢", theme.Question
	case step.StatusAwaitingReview:
		return "?", theme.Marker
	case step.StatusNeedsInput:
		return "⊙", theme.Marker
	default:
		return "·", theme.Question
	}
}

func statusStyle(s step.Status) lipgloss.Style {
	switch s {
	case step.StatusRunning:
		return theme.Running
	case step.StatusSucceeded:
		return theme.Valid
	case step.StatusFailed:
		return theme.Error
	default:
		return theme.Question
	}
}

// subtypeBadgeLabel returns a compact label for policy-limit failure subtypes so
// operators can distinguish "hit turn limit" from an API error at a glance.
// Returns "" for subtypes that don't warrant a special annotation.
func subtypeBadgeLabel(subtype string) string {
	switch subtype {
	case "error_max_turns":
		return "max turns"
	case "error_max_budget_usd":
		return "budget"
	default:
		return ""
	}
}

func stepDuration(s monitorStep) string {
	if s.start.IsZero() {
		return "—"
	}
	end := s.end
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(s.start).Round(time.Millisecond)
	return d.String()
}

func (m monitorModel) footerView() string {
	var status string
	if m.done {
		if m.failed {
			status = theme.Error.Render("failed")
		} else {
			status = theme.Valid.Render("done")
		}
	} else if entry, ok := m.activeEntry(); ok {
		switch entry.kind {
		case inputKindRequest:
			status = theme.Marker.Render("awaiting agent input")
		case inputKindQuestion:
			status = theme.Marker.Render("awaiting answer")
		case inputKindPrompt:
			status = theme.Marker.Render("awaiting user input")
		case inputKindReview:
			if entry.composing {
				status = theme.Marker.Render("composing message")
			} else {
				status = theme.Marker.Render("awaiting review")
			}
		}
	} else {
		status = theme.Running.Render("running")
	}
	var hint string
	switch {
	case m.focus == focusGate && m.hasGate():
		entry := m.inputQueue[m.activeInputIdx]
		switch entry.kind {
		case inputKindRequest:
			hint = hintString(m.keys.Submit, m.keys.Newline, m.keys.FocusHint, m.keys.InputLeave)
		case inputKindQuestion:
			if entry.questionIdx < len(entry.question.Questions) && entry.question.Questions[entry.questionIdx].MultiSelect {
				hint = hintString(m.keys.ToggleOpt, m.keys.QConfirm, m.keys.FocusHint, m.keys.QuestionCancel)
			} else {
				hint = hintString(m.keys.Answer, m.keys.FocusHint, m.keys.QuestionCancel)
			}
		case inputKindReview:
			if entry.composing {
				hint = hintString(m.keys.Submit, m.keys.Newline, m.keys.ComposeCancel)
			} else if entry.review.AllowMessage {
				hint = hintString(m.keys.Verdict, m.keys.Message, m.keys.FocusHint, m.keys.ReviewLeave)
			} else {
				hint = hintString(m.keys.Verdict, m.keys.FocusHint, m.keys.ReviewLeave)
			}
		case inputKindPrompt:
			hint = hintString(m.keys.Submit, m.keys.Newline, m.keys.FocusHint, m.keys.PromptLeave)
		}
	case m.focus == focusTranscript:
		hint = hintString(m.keys.FocusFull, m.keys.Scroll, m.keys.BlockNav, m.keys.Toggle, m.keys.ExpandAll)
	default: // focusSteps
		hint = hintString(m.keys.FocusFull, m.keys.StepsNav, m.keys.StepsLeave, keyQuit)
	}
	// When a gate is pending but the user has focused a panel, remind them a gate
	// is waiting (it is non-blocking — tab returns to it).
	if m.hasGate() && m.focus != focusGate {
		hint = "tab to gate  •  " + hint
	}
	// Clip to the terminal width so a long hint line never overflows the panels
	// and skews JoinVertical's per-line width (which would break the box borders).
	f := theme.Footer
	if m.width > 0 {
		f = f.MaxWidth(m.width)
	}
	return f.Render("  " + status + "  ·  " + hint)
}

// View lays the monitor out as two side-by-side titled panels (Steps + the
// selected step's transcript) with the gate strip and footer beneath. Below the
// narrow threshold only the focused panel renders full-width (Resolved
// Decision 14). Only the focused region's border is drawn primary.
func (m monitorModel) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}

	footer := m.footerView()
	gate := m.gateStrip()

	panelH := m.height - lipgloss.Height(footer) - lipgloss.Height(gate)
	if panelH < 1 {
		panelH = 1
	}

	rightTitle := m.chatStep
	if rightTitle == "" {
		rightTitle = "Transcript"
	}

	var panels string
	if m.narrow {
		// Single-panel fallback: render only the focused panel full-width.
		if m.focus == focusTranscript {
			panels = panel(rightTitle, m.chatVP.View(), m.width, panelH, true)
		} else {
			// Steps or Gate focus shows the Steps panel (the gate has its own strip).
			panels = panel("Steps", m.vp.View(), m.width, panelH, m.focus == focusSteps)
		}
	} else {
		stepsW, transcriptW, _ := panelSplit(m.width)
		left := panel("Steps", m.vp.View(), stepsW, panelH, m.focus == focusSteps)
		right := panel(rightTitle, m.chatVP.View(), transcriptW, panelH, m.focus == focusTranscript)
		panels = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}

	parts := []string{panels}
	if gate != "" {
		parts = append(parts, gate)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
