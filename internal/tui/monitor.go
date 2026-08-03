package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
)

// monitorMode is the master–detail state of the run monitor. modeList is the
// step list (j/k move a selection cursor); modeChat drills into one step's
// agent transcript (j/k scroll the viewport). The explicit mode keeps the
// list-navigation keymap from colliding with the viewport's scroll keymap.
type monitorMode int

const (
	modeList monitorMode = iota
	modeChat
)

// monitorModel is the per-run view: a live step-status table for one run,
// updated as engine events arrive. The user can press esc to return to the
// runs list.
type monitorModel struct {
	runID    string
	workflow string
	steps    []monitorStep
	index    map[string]int // stepID → steps position
	done     bool
	failed   bool
	// runErr is an engine-level failure (worktree setup, max_iterations) that is
	// not attributable to a single step. Set by the engine.RunError event.
	runErr string

	// Phase 4 navigation: modeList selects a step; modeChat drills into one.
	mode     monitorMode
	cursor   int    // selected step in modeList
	chatStep string // step whose chat is open in modeChat

	// Phase 5 chat rendering. runDir locates the per-step transcript.jsonl on
	// disk; the transcript file — not the lossy event bus — is what modeChat
	// renders. dark selects the glamour style.
	runDir string
	dark   bool

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
	// event is wasteful). renderWidth is the width the cache was built for; a
	// resize to a new width rebuilds the renderer and invalidates the cache.
	renderer     *glamour.TermRenderer
	chatRendered map[blockKey]string
	renderWidth  int

	// msgCount tracks the latest transcript entry seq observed per step (via
	// StepMessage liveness events), used as a message count in the list.
	msgCount map[string]int

	// Phase 3: review steps park here until a verdict is delivered.
	pendingReview    *engine.ReviewRequest
	composingMessage bool // true while the user is composing a message to the agent

	// block_on: set when an agent step needs human input before it can proceed.
	pendingInput *engine.InputRequest

	// AskUserQuestion: set when an in-flight agent step calls AskUserQuestion
	// mid-execution. questionIdx tracks which question we're currently presenting;
	// questionSelected tracks toggled options for multiSelect questions;
	// questionAnswers accumulates formatted answers for already-answered questions.
	pendingQuestion  *engine.AgentQuestion
	questionIdx      int
	questionSelected map[int]bool
	questionAnswers  []string

	// reviews retains the last ReviewRequest seen per step so drilling into a
	// review step (modeChat) can show its diff/choices — review steps have no
	// transcript. Kept after the verdict clears pendingReview (Phase 6).
	reviews map[string]engine.ReviewRequest

	// from="user" input collection: the active prompt and its textarea.
	pendingPrompt  *engine.PromptRequest
	promptTextarea textarea.Model

	// Phase 4: rolling output buffer per step (last outputMaxLines lines).
	stepOutput map[string]*strings.Builder

	vp    viewport.Model
	ready bool

	width  int
	height int
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
		m, evCmd = m.handleEngineEvent(msg.event)
		if m.ready {
			m.vp.SetContent(m.body())
		}
		return m, evCmd

	case tea.KeyMsg:
		// block_on input: enter submits, esc leaves to runs list, other keys go to textarea.
		if m.pendingInput != nil {
			if msg.String() == "esc" {
				return m, func() tea.Msg { return showRunsMsg{} }
			}
			if msg.String() == "enter" {
				text := m.promptTextarea.Value()
				if text == "" {
					return m, nil
				}
				inp := m.pendingInput
				m.pendingInput = nil
				m.promptTextarea = textarea.Model{}
				if m.ready {
					m.vp.SetContent(m.body())
				}
				return m, func() tea.Msg {
					return agentInputMsg{runID: inp.RunID, stepID: inp.StepID, text: text}
				}
			}
			var taCmd tea.Cmd
			m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
			if m.ready {
				m.vp.SetContent(m.body())
			}
			return m, taCmd
		}
		// AskUserQuestion: digit keys select / toggle options; enter confirms multiSelect.
		if m.pendingQuestion != nil {
			if s := msg.String(); s == "esc" || s == "q" {
				return m, func() tea.Msg { return showRunsMsg{} }
			}
			if m.questionIdx < len(m.pendingQuestion.Questions) {
				q := m.pendingQuestion.Questions[m.questionIdx]
				if q.MultiSelect {
					for i := range q.Options {
						if msg.String() == fmt.Sprintf("%d", i+1) {
							m.questionSelected[i] = !m.questionSelected[i]
							if m.ready {
								m.vp.SetContent(m.body())
							}
							return m, nil
						}
					}
					if s := msg.String(); s == "enter" || s == " " {
						var selected []string
						for i, opt := range q.Options {
							if m.questionSelected[i] {
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
		}
		// When awaiting user text input or composing a message, route keys to the textarea.
		if m.pendingPrompt != nil || m.composingMessage {
			if msg.String() == "esc" && m.composingMessage {
				// Cancel compose — return to the verdict picker.
				m.composingMessage = false
				m.promptTextarea = textarea.Model{}
				if m.ready {
					m.vp.SetContent(m.body())
				}
				return m, nil
			}
			if msg.String() == "enter" {
				text := m.promptTextarea.Value()
				if m.composingMessage {
					if text == "" {
						return m, nil
					}
					rev := m.pendingReview
					m.composingMessage = false
					m.pendingReview = nil
					m.promptTextarea = textarea.Model{}
					if m.ready {
						m.vp.SetContent(m.body())
					}
					return m, func() tea.Msg {
						return reviewMessageMsg{
							runID:  rev.RunID,
							stepID: rev.StepID,
							text:   text,
						}
					}
				}
				pr := m.pendingPrompt
				m.pendingPrompt = nil
				m.promptTextarea = textarea.Model{}
				if m.ready {
					m.vp.SetContent(m.body())
				}
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
			if m.ready {
				m.vp.SetContent(m.body())
			}
			return m, taCmd
		}
		// Review verdict: digit keys 1–9 select a choice when a review is pending.
		// A pending review freezes navigation — only a verdict, message compose, or
		// esc (leave to the runs list) is accepted.
		if m.pendingReview != nil {
			if msg.String() == "m" && m.pendingReview.AllowMessage {
				m.composingMessage = true
				ta := textarea.New()
				ta.Placeholder = "Message to agent…"
				ta.KeyMap.InsertNewline = keybind.NewBinding(
					keybind.WithKeys("alt+enter", "shift+enter"),
					keybind.WithHelp("alt+enter", "insert newline"),
				)
				ta.ShowLineNumbers = false
				ta.SetHeight(4)
				ta.SetWidth(m.width - 4)
				focusedStyle, blurredStyle := textarea.DefaultStyles()
				focusedStyle.Base = textareaStyle.BorderForeground(textareaFocusedBorder)
				blurredStyle.Base = textareaStyle.BorderForeground(textareaBlurredBorder)
				ta.FocusedStyle = focusedStyle
				ta.BlurredStyle = blurredStyle
				ta.Focus()
				m.promptTextarea = ta
				if m.ready {
					m.vp.SetContent(m.body())
				}
				return m, textarea.Blink
			}
			choices := m.pendingReview.Choices
			for i, ch := range choices {
				key := fmt.Sprintf("%d", i+1)
				if msg.String() == key {
					rev := m.pendingReview
					m.pendingReview = nil
					return m, func() tea.Msg {
						return reviewVerdictMsg{
							runID:   rev.RunID,
							stepID:  rev.StepID,
							verdict: ch,
						}
					}
				}
			}
			if s := msg.String(); s == "esc" || s == "q" {
				return m, func() tea.Msg { return showRunsMsg{} }
			}
			return m, nil
		}
		// Mode-specific navigation. In modeList, j/k move the selection cursor
		// and must be intercepted before the viewport's scroll keymap sees them;
		// in modeChat, j/k fall through to scroll the transcript.
		switch m.mode {
		case modeList:
			switch msg.String() {
			case "j", "down":
				if m.cursor < len(m.steps)-1 {
					m.cursor++
					m.ensureCursorVisible()
					if m.ready {
						m.vp.SetContent(m.body())
					}
				}
				return m, nil
			case "k", "up":
				if m.cursor > 0 {
					m.cursor--
					m.ensureCursorVisible()
					if m.ready {
						m.vp.SetContent(m.body())
					}
				}
				return m, nil
			case "enter":
				if m.cursor < len(m.steps) {
					m.mode = modeChat
					m.chatStep = m.steps[m.cursor].id
					// Fresh chat: reset per-step collapse/cursor state (seq keys
					// only make sense within one step's transcript) and load.
					m.chatBlockCursor = 0
					m.chatExpandAll = false
					m.chatExpand = make(map[blockKey]bool)
					m.loadChat()
					if m.ready {
						m.vp.SetContent(m.body())
						m.vp.GotoTop()
					}
				}
				return m, nil
			case "esc", "q", "backspace", "h", "left":
				return m, func() tea.Msg { return showRunsMsg{} }
			}
		case modeChat:
			switch msg.String() {
			case "esc", "h", "left":
				m.mode = modeList
				m.chatStep = ""
				if m.ready {
					m.vp.SetContent(m.body())
					m.vp.GotoTop()
				}
				return m, nil
			case "tab":
				// Move the block cursor to the next collapsible block.
				if n := len(m.chatBlocks); n > 0 {
					m.chatBlockCursor = (m.chatBlockCursor + 1) % n
					if m.ready {
						m.vp.SetContent(m.body())
					}
				}
				return m, nil
			case "shift+tab":
				if n := len(m.chatBlocks); n > 0 {
					m.chatBlockCursor = (m.chatBlockCursor - 1 + n) % n
					if m.ready {
						m.vp.SetContent(m.body())
					}
				}
				return m, nil
			case "enter", " ":
				// Toggle the block under the cursor.
				if n := len(m.chatBlocks); n > 0 && m.chatBlockCursor < n {
					k := m.chatBlocks[m.chatBlockCursor]
					m.chatExpand[k] = !m.chatExpand[k]
					if m.ready {
						m.vp.SetContent(m.body())
					}
				}
				return m, nil
			case "o":
				// Expand/collapse everything in view at once.
				m.chatExpandAll = !m.chatExpandAll
				if m.ready {
					m.vp.SetContent(m.body())
				}
				return m, nil
			}
			// Other keys (j/k/ctrl+d/ctrl+u/pgup/pgdn) fall through to the
			// viewport to scroll the transcript.
		}
	}

	// Route non-key messages to the textarea (blink timer, focus events) when active.
	if m.pendingPrompt != nil || m.composingMessage || m.pendingInput != nil {
		var taCmd tea.Cmd
		m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
		if m.ready {
			m.vp.SetContent(m.body())
		}
		return m, taCmd
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
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
		// Clear stale review or prompt when the step reaches a terminal state.
		if ev.To == step.StatusSucceeded || ev.To == step.StatusFailed || ev.To == step.StatusSkipped {
			if m.pendingReview != nil && m.pendingReview.StepID == ev.StepID {
				m.pendingReview = nil
			}
			if m.pendingPrompt != nil && m.pendingPrompt.StepID == ev.StepID {
				m.pendingPrompt = nil
				m.promptTextarea = textarea.Model{}
			}
		}
		// Clear pending input when the step is no longer blocked (e.g. resumed or failed).
		if m.pendingInput != nil && m.pendingInput.StepID == ev.StepID && ev.To != step.StatusNeedsInput {
			m.pendingInput = nil
			m.promptTextarea = textarea.Model{}
		}
		// Clear pending question when the step resumes or reaches a terminal state.
		if m.pendingQuestion != nil && m.pendingQuestion.StepID == ev.StepID && ev.To != step.StatusNeedsInput {
			m.pendingQuestion = nil
			m.questionSelected = nil
			m.questionAnswers = nil
		}

	case engine.ReviewRequest:
		if ev.RunID != m.runID {
			return m, nil
		}
		m.pendingReview = &ev
		if m.reviews == nil {
			m.reviews = make(map[string]engine.ReviewRequest)
		}
		// Retain the request so the step's chat drill-in can show the diff/choices
		// after the verdict clears pendingReview.
		m.reviews[ev.StepID] = ev
		// A human-input gate must never be ambiguous: surface it in the list
		// view where the overlay renders and keys are unambiguous.
		m.mode = modeList

	case engine.InputRequest:
		if ev.RunID != m.runID {
			return m, nil
		}
		m.pendingInput = &ev
		m.mode = modeList
		ta := textarea.New()
		ta.Placeholder = "Your response to the agent…"
		ta.KeyMap.InsertNewline = keybind.NewBinding(
			keybind.WithKeys("alt+enter", "shift+enter"),
			keybind.WithHelp("alt+enter", "insert newline"),
		)
		ta.ShowLineNumbers = false
		ta.SetHeight(4)
		ta.SetWidth(m.width - 4)
		focusedStyle, blurredStyle := textarea.DefaultStyles()
		focusedStyle.Base = textareaStyle.BorderForeground(textareaFocusedBorder)
		blurredStyle.Base = textareaStyle.BorderForeground(textareaBlurredBorder)
		ta.FocusedStyle = focusedStyle
		ta.BlurredStyle = blurredStyle
		ta.Focus()
		m.promptTextarea = ta
		return m, textarea.Blink

	case engine.AgentQuestion:
		if ev.RunID != m.runID {
			return m, nil
		}
		m.pendingQuestion = &ev
		m.questionIdx = 0
		m.questionSelected = make(map[int]bool)
		m.questionAnswers = nil
		m.mode = modeList

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
		if m.mode == modeChat && ev.StepID == m.chatStep {
			m.loadChat()
		}

	case engine.PromptRequest:
		if ev.RunID != m.runID {
			return m, nil
		}
		m.pendingPrompt = &ev
		m.mode = modeList
		ta := textarea.New()
		ta.Placeholder = ev.Label
		// enter submits the response; newlines are inserted with alt/shift+enter.
		ta.KeyMap.InsertNewline = keybind.NewBinding(
			keybind.WithKeys("alt+enter", "shift+enter"),
			keybind.WithHelp("alt+enter", "insert newline"),
		)
		ta.ShowLineNumbers = false
		ta.SetHeight(4)
		ta.SetWidth(m.width - 4)
		focusedStyle, blurredStyle := textarea.DefaultStyles()
		focusedStyle.Base = textareaStyle.BorderForeground(textareaFocusedBorder)
		blurredStyle.Base = textareaStyle.BorderForeground(textareaBlurredBorder)
		ta.FocusedStyle = focusedStyle
		ta.BlurredStyle = blurredStyle
		ta.Focus()
		m.promptTextarea = ta
		return m, textarea.Blink

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

func (m *monitorModel) resize() {
	footerH := lipgloss.Height(m.footerView())
	vpH := m.height - footerH
	if vpH < 1 {
		vpH = 1
	}
	if !m.ready {
		m.vp = viewport.New(m.width, vpH)
		m.ready = true
	} else {
		m.vp.Width = m.width
		m.vp.Height = vpH
	}
	if m.pendingPrompt != nil || m.composingMessage || m.pendingInput != nil {
		m.promptTextarea.SetWidth(m.width - 4)
	}
	m.rebuildRenderer()
	m.vp.SetContent(m.body())
}

// rebuildRenderer (re)constructs the markdown renderer for the current width and
// invalidates the per-block render cache when the width actually changed —
// glamour bakes its word-wrap width in at construction, so a stale cache would
// wrap to the old width (see turn.go / viewer.go for the same house rule). A
// static style (not AutoStyle) avoids the OSC-11 stdin race documented in
// main.go; the background was detected once before Bubble Tea started.
func (m *monitorModel) rebuildRenderer() {
	wordWrap := m.width - 4
	if wordWrap < 1 {
		wordWrap = 1
	}
	styleName := "light"
	if m.dark {
		styleName = "dark"
	}
	m.renderer, _ = glamour.NewTermRenderer(
		glamour.WithStandardStyle(styleName),
		glamour.WithWordWrap(wordWrap),
	)
	if m.renderWidth != m.width {
		m.chatRendered = make(map[blockKey]string)
		m.renderWidth = m.width
	}
}

// ensureCursorVisible nudges the viewport so the selected step stays on screen
// as the cursor moves, keeping a small margin from the top and bottom edges.
func (m *monitorModel) ensureCursorVisible() {
	if !m.ready {
		return
	}
	row := listBodyHeaderLines + m.cursor
	const margin = 2
	top := m.vp.YOffset
	bottom := top + m.vp.Height - 1
	switch {
	case row-margin < top:
		m.vp.SetYOffset(row - margin)
	case row+margin > bottom:
		m.vp.SetYOffset(row + margin - m.vp.Height + 1)
	}
}

// body dispatches on the current mode: the step list or one step's chat chain.
func (m monitorModel) body() string {
	if m.mode == modeChat {
		return m.chatBody()
	}
	return m.listBody()
}

// listBodyHeaderLines is the number of lines listBody renders above the step
// table (a leading blank, the title row, a trailing blank). ensureCursorVisible
// uses it to map a cursor index to a viewport row.
const listBodyHeaderLines = 3

func (m monitorModel) listBody() string {
	var b strings.Builder

	wfName := m.workflow
	if wfName == "" {
		wfName = m.runID
	}

	b.WriteString("\n  " + titleStyle.Render(wfName) + "  " +
		pathStyle.Render(m.runID) + "\n\n")

	if len(m.steps) == 0 {
		b.WriteString("  " + questionStyle.Render("Waiting for run to start…") + "\n")
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
			cursor = "> "
		}
		indicator, style := stepIndicator(s.status)
		dur := stepDuration(s)
		msgs := ""
		if n := m.msgCount[s.id]; n > 0 {
			msgs = questionStyle.Render(fmt.Sprintf("%d msg", n))
		}
		line := fmt.Sprintf("%s%s  %s  %s  %s  %s",
			cursor,
			indicator,
			style.Render(padRight(s.id, idWidth)),
			statusStyle(s.status).Render(fmt.Sprintf("%-16s", string(s.status))),
			questionStyle.Render(padRight(dur, 10)),
			msgs,
		)
		if label := subtypeBadgeLabel(s.subtype); label != "" {
			line += "  " + errorStyle.Render(label)
		}
		if i == m.cursor {
			b.WriteString(lipgloss.NewStyle().Bold(true).Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	if m.done {
		if m.failed {
			b.WriteString("  " + errorStyle.Render("✗ run failed") + "\n")
			m.writeFailureReasons(&b)
		} else {
			b.WriteString("  " + validStyle.Render("✓ run complete") + "\n")
		}
	}

	// User input: show textarea when a step needs free-form text.
	if m.pendingPrompt != nil {
		b.WriteString("\n")
		b.WriteString("  " + markerStyle.Render("Input required — step: "+m.pendingPrompt.StepID) + "\n")
		b.WriteString("  " + questionStyle.Render(m.pendingPrompt.Label) + "\n\n")
		b.WriteString(m.promptTextarea.View() + "\n")
	}

	// Review picker: show when a step is awaiting human input.
	if m.pendingReview != nil {
		b.WriteString("\n")
		b.WriteString("  " + markerStyle.Render("Review required — step: "+m.pendingReview.StepID) + "\n\n")

		if m.pendingReview.Diff != "" {
			writeDiff(&b, m.pendingReview.Diff)
			b.WriteString("\n")
		}

		if m.composingMessage {
			b.WriteString(m.promptTextarea.View() + "\n")
		} else {
			for i, ch := range m.pendingReview.Choices {
				b.WriteString(fmt.Sprintf("    [%d] %s\n", i+1, ch))
			}
			if m.pendingReview.AllowMessage {
				b.WriteString("    [m] message\n")
			}
		}
		b.WriteString("\n")
	}

	// block_on input: show textarea when an agent step is blocked awaiting human input.
	if m.pendingInput != nil {
		b.WriteString("\n")
		b.WriteString("  " + markerStyle.Render("Agent input required — step: "+m.pendingInput.StepID) + "\n\n")
		b.WriteString(m.promptTextarea.View() + "\n")
	}

	// AskUserQuestion: show selectable options when an in-flight agent asks a question.
	if m.pendingQuestion != nil && m.questionIdx < len(m.pendingQuestion.Questions) {
		q := m.pendingQuestion.Questions[m.questionIdx]
		b.WriteString("\n")
		b.WriteString("  " + markerStyle.Render("Agent question — step: "+m.pendingQuestion.StepID) + "\n\n")
		if q.Header != "" {
			b.WriteString("  " + questionStyle.Render("["+q.Header+"]") + "\n")
		}
		b.WriteString("  " + q.Question + "\n\n")
		for i, opt := range q.Options {
			if q.MultiSelect {
				mark := "[ ]"
				if m.questionSelected[i] {
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
			b.WriteString("\n    " + chatHintStyle.Render("enter to confirm selection") + "\n")
		}
		if len(m.pendingQuestion.Questions) > 1 {
			b.WriteString("    " + chatHintStyle.Render(
				fmt.Sprintf("question %d of %d", m.questionIdx+1, len(m.pendingQuestion.Questions))) + "\n")
		}
	}

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
		b.WriteString("\n  " + questionStyle.Render("▸ "+s.id) + "\n")
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
		b.WriteString("    " + errorStyle.Render(wrap.Render("engine: "+m.runErr)) + "\n")
	}
	for _, s := range m.steps {
		if s.status != step.StatusFailed || s.err == "" {
			continue
		}
		b.WriteString("    " + errorStyle.Render(wrap.Render(s.id+": "+s.err)) + "\n")
	}
}

// advanceQuestion records the answer for the current question and advances the
// question index. When all questions are answered it clears pendingQuestion and
// returns a command emitting agentQuestionResponseMsg with the formatted answer.
func (m monitorModel) advanceQuestion(answer string) (monitorModel, tea.Cmd) {
	m.questionAnswers = append(m.questionAnswers, answer)
	m.questionIdx++
	m.questionSelected = make(map[int]bool)

	if m.questionIdx < len(m.pendingQuestion.Questions) {
		if m.ready {
			m.vp.SetContent(m.body())
		}
		return m, nil
	}

	q := m.pendingQuestion
	answers := m.questionAnswers
	m.pendingQuestion = nil
	m.questionAnswers = nil
	if m.ready {
		m.vp.SetContent(m.body())
	}
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

	b.WriteString("\n  " + titleStyle.Render("chat") + "  " +
		pathStyle.Render(m.chatStep) + "\n\n")

	i, ok := m.index[m.chatStep]
	if !ok {
		b.WriteString("  " + questionStyle.Render("no such step") + "\n")
		return b.String()
	}
	s := m.steps[i]
	indicator, style := stepIndicator(s.status)
	b.WriteString("  " + indicator + "  " + style.Render(s.id) + "  " +
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
			b.WriteString("  " + questionStyle.Render("transcript unavailable (persistence off)") + "\n")
		} else if !running && !hasTail {
			b.WriteString("  " + questionStyle.Render("no output yet") + "\n")
		}
	}

	if m.chatElided > 0 {
		b.WriteString("  " + chatHintStyle.Render(
			fmt.Sprintf("… %d earlier message(s) elided", m.chatElided)) + "\n\n")
	}

	lastIter, lastAttempt := -1, -1
	for _, e := range m.chatEntries {
		if lastIter != -1 && e.Iteration > lastIter {
			b.WriteString("\n  " + markerStyle.Render(
				fmt.Sprintf("── iteration %d ──", e.Iteration+1)) + "\n\n")
		}
		if lastAttempt != -1 && e.Attempt > lastAttempt {
			b.WriteString("\n  " + markerStyle.Render(
				fmt.Sprintf("── retry %d ──", e.Attempt)) + "\n\n")
		}
		lastIter, lastAttempt = e.Iteration, e.Attempt

		b.WriteString("  " + chatHintStyle.Render(fmt.Sprintf("#%d %s", e.Seq, e.Role)) + "\n")
		for bi, blk := range e.Blocks {
			key := blockKey{seq: e.Seq, block: bi}
			m.writeBlock(&b, key, blk, e.Role)
		}
		b.WriteString("\n")
	}

	// Live tail: the current, not-yet-finalized bubble. Reset on each
	// StepMessage, so it shows only deltas past the last finalized entry.
	if running && hasTail {
		b.WriteString("  " + questionStyle.Render("typing…") + "\n")
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
		m.writeCollapsible(b, key, thinkingStyle, "🧠 reasoning", blk.Text, "", false, blk.Truncated)
	case transcript.BlockToolUse:
		m.writeCollapsible(b, key, toolCallStyle, "⚙ "+blk.Name, string(blk.Input), fenceJSON(string(blk.Input)), false, false)
	case transcript.BlockToolResult:
		m.writeCollapsible(b, key, toolResultStyle, "↳ result", blk.Content, fenceJSON(blk.Content), blk.IsError, blk.Truncated)
	default:
		b.WriteString("  " + questionStyle.Render("[unsupported block: "+string(blk.Type)+"]") + "\n")
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

// writeCollapsible renders one collapsible block: a labelled header line with a
// ▸/▾ affordance, then either a one-line preview clipped to chatCollapseWidth or
// the bounded full content. The block under the chat cursor is highlighted so
// the expand target is obvious; error results take errorStyle.
func (m monitorModel) writeCollapsible(b *strings.Builder, key blockKey, labelStyle lipgloss.Style, label, content, formattedContent string, isError, truncated bool) {
	expanded := m.chatExpandAll || m.chatExpand[key]
	cursored := len(m.chatBlocks) > 0 && m.chatBlockCursor < len(m.chatBlocks) &&
		m.chatBlocks[m.chatBlockCursor] == key

	marker := "▸"
	if expanded {
		marker = "▾"
	}
	head := labelStyle
	if isError {
		head = errorStyle
	}
	if cursored {
		b.WriteString("  " + blockCursorStyle.Render(marker+" "+label))
	} else {
		b.WriteString("  " + marker + " " + head.Render(label))
	}

	if !expanded {
		shown, clipped := collapseLine(content)
		if shown != "" {
			b.WriteString("  " + questionStyle.Render(shown))
		}
		if clipped || truncated {
			b.WriteString(chatHintStyle.Render(
				fmt.Sprintf(" [%d chars]", utf8.RuneCountInString(content))))
		}
		b.WriteString("\n")
		return
	}

	b.WriteString("\n")
	if formattedContent != "" {
		b.WriteString(m.renderMarkdown(key, formattedContent))
	} else {
		for _, l := range strings.Split(expandView(content), "\n") {
			b.WriteString("    " + questionStyle.Render(l) + "\n")
		}
	}
	if truncated {
		b.WriteString("    " + chatHintStyle.Render("… (truncated at write)") + "\n")
	}
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
	b.WriteString("  " + questionStyle.Render("── diff ─────────────────────────────") + "\n")
	lines := strings.Split(diff, "\n")
	const maxDiffLines = 200
	truncated := len(lines) > maxDiffLines
	if truncated {
		lines = lines[:maxDiffLines]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			b.WriteString("  " + diffAddStyle.Render(line) + "\n")
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			b.WriteString("  " + diffRemoveStyle.Render(line) + "\n")
		case strings.HasPrefix(line, "@@"):
			b.WriteString("  " + diffHunkStyle.Render(line) + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}
	if truncated {
		b.WriteString("  " + questionStyle.Render("… diff truncated") + "\n")
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
		return "○", questionStyle
	case step.StatusRunning:
		return "●", runningStyle
	case step.StatusSucceeded:
		return "✓", validStyle
	case step.StatusFailed:
		return "✗", errorStyle
	case step.StatusSkipped:
		return "—", questionStyle
	case step.StatusValidating:
		return "⇢", questionStyle
	case step.StatusAwaitingReview:
		return "?", markerStyle
	case step.StatusNeedsInput:
		return "⊙", markerStyle
	default:
		return "·", questionStyle
	}
}

func statusStyle(s step.Status) lipgloss.Style {
	switch s {
	case step.StatusRunning:
		return runningStyle
	case step.StatusSucceeded:
		return validStyle
	case step.StatusFailed:
		return errorStyle
	default:
		return questionStyle
	}
}

// subtypeBadgeLabel returns a compact label for policy-limit failure subtypes so
// operators can distinguish "hit turn limit" from an API error at a glance.
// Returns "" for subtypes that don't warrant a special annotation.
func subtypeBadgeLabel(subtype string) string {
	switch subtype {
	case "error_max_turns":
		return "[max turns]"
	case "error_max_budget_usd":
		return "[budget]"
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
			status = errorStyle.Render("failed")
		} else {
			status = validStyle.Render("done")
		}
	} else if m.pendingInput != nil {
		status = markerStyle.Render("awaiting agent input")
	} else if m.pendingQuestion != nil {
		status = markerStyle.Render("awaiting answer")
	} else if m.pendingPrompt != nil {
		status = markerStyle.Render("awaiting user input")
	} else if m.composingMessage {
		status = markerStyle.Render("composing message")
	} else if m.pendingReview != nil {
		status = markerStyle.Render("awaiting review")
	} else {
		status = runningStyle.Render("running")
	}
	var hint string
	switch {
	case m.pendingInput != nil:
		hint = "enter submit  •  alt+enter newline  •  esc runs list  •  ctrl+c quit"
	case m.pendingQuestion != nil:
		if m.questionIdx < len(m.pendingQuestion.Questions) && m.pendingQuestion.Questions[m.questionIdx].MultiSelect {
			hint = "1-9 toggle  •  enter confirm  •  esc runs list  •  ctrl+c quit"
		} else {
			hint = "1-9 select answer  •  esc runs list  •  ctrl+c quit"
		}
	case m.pendingPrompt != nil:
		hint = "enter submit  •  alt+enter newline  •  esc runs list  •  ctrl+c quit"
	case m.composingMessage:
		hint = "enter submit  •  alt+enter newline  •  esc cancel  •  ctrl+c quit"
	case m.pendingReview != nil:
		if m.pendingReview.AllowMessage {
			hint = "1-9 select verdict  •  m message  •  esc runs list  •  ctrl+c quit"
		} else {
			hint = "1-9 select verdict  •  esc runs list  •  ctrl+c quit"
		}
	case m.mode == modeChat:
		hint = "esc back  •  j/k scroll  •  tab block  •  enter expand  •  o all"
	default:
		hint = "j/k select  •  enter open  •  esc runs list  •  ctrl+c quit"
	}
	return footerStyle.Render("  " + status + "  ·  " + hint)
}

func (m monitorModel) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.footerView())
}
