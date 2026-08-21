package monitor

import (
	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/helpchat"
)

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		if m.helpReady {
			var hCmd tea.Cmd
			m.helpModel, hCmd = m.helpModel.Update(helpchat.SizeMsg{W: m.helpBoxW(), H: m.helpBoxH()})
			return m, hCmd
		}
		return m, nil

	case helpchat.ConnectedMsg, helpchat.ConnectErrMsg,
		helpchat.DeltaMsg, helpchat.TurnCompleteMsg, helpchat.TurnErrorMsg:
		if m.helpReady {
			var hCmd tea.Cmd
			m.helpModel, hCmd = m.helpModel.Update(msg)
			return m, hCmd
		}
		return m, nil

	case helpchat.DispatchedMsg:
		return m, m.dispatchHelpAction(msg)

	case helpchat.FinalMergeGateMsg:
		// The tool handler is blocked waiting for gateAns. Show a gate entry so the
		// operator can confirm. Re-arm the gate listener for subsequent gate calls.
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind: inputKindHelpFinalMerge,
		})
		m.refreshPanels()
		return m, waitForGateReqCmd(m.helpGateReq)

	case EngineEventMsg:
		var evCmd tea.Cmd
		m, evCmd = m.handleEngineEvent(msg.Event)
		// The Transcript panel always shows the cursor's step; point it at the
		// first step as soon as the run's steps are known (eager reload).
		if m.chatStep == "" && m.cursor < len(m.steps) {
			m.reloadTranscript()
		}
		// Mark the affected panel(s) dirty. The Steps list is cheap and reflects
		// every step, so it is always dirtied; the Transcript panel runs glamour, so
		// it is dirtied only when the event touches the visible step — a parallel
		// step's stream never repaints the panel you are viewing.
		m.dirtyList = true
		if m.eventAffectsChat(msg.Event) {
			m.dirtyChat = true
		}
		// Leading edge: when the frame loop is idle, this event opens a fresh burst
		// window, so paint it now — a lone event (or the first of a burst) then has
		// zero added latency. While the loop is already running (mid-stream), the
		// event is left dirty for the frame to coalesce, collapsing a burst of
		// per-chunk deltas into one repaint per frame instead of one render each.
		if !m.ticking {
			m.flushDirty()
		}
		return m, tea.Batch(evCmd, m.EnsureFrame())

	case TickMsg:
		// One animation frame: advance the live clock (a running step's elapsed
		// column changes even with no new events) and flush whatever is dirty.
		if m.anyRunning() {
			m.dirtyList = true
		}
		m.flushDirty()
		// Re-arm while there is ongoing work: a running clock, or a pending repaint
		// we could actually flush (a dirty flag only counts once ready — before the
		// first resize there is no viewport, and resize repaints synchronously — so
		// pending dirt while un-ready must not spin the loop). Otherwise fall silent.
		if m.anyRunning() || (m.ready && (m.dirtyList || m.dirtyChat)) {
			return m, monitorTickCmd()
		}
		m.ticking = false
		return m, nil

	case ShowResetConfirmMsg:
		if msg.RunID != m.RunID {
			return m, nil
		}
		// Mid-graph reset: show a confirmation gate entry naming the blast radius.
		// No focus steal on arrival (Decision 6); the user can tab to act on it.
		rc := &resetConfirmEntry{runID: msg.RunID, stepID: msg.StepID, closure: msg.Closure}
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:         inputKindResetConfirm,
			stepID:       msg.StepID,
			resetConfirm: rc,
		})
		m.refreshPanels()
		return m, nil

	case tea.KeyPressMsg:
		// ctrl+\ toggles the help agent modal from any focus region.
		if keybind.Matches(msg, m.keys.ToggleHelp) {
			return m.toggleHelpChat()
		}
		// When the help modal is open and captures text, route all key input to it.
		if m.helpOpen {
			if keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("esc"))) {
				m.helpOpen = false
				return m, nil
			}
			var hCmd tea.Cmd
			m.helpModel, hCmd = m.helpModel.Update(msg)
			return m, hCmd
		}

		// Context inspection is deliberately separate from queue navigation:
		// tab changes the pending decision, while ctrl+o is the only path that
		// temporarily re-points the Steps and Transcript panels.
		if keybind.Matches(msg, m.keys.GateContext) &&
			(m.focus == focusGate || m.gateContext != nil) {
			m.toggleGateContext()
			return m, nil
		}

		// Focus-switch keys move keyboard focus between the present regions even
		// while a gate is pending — gates are non-blocking (ADR 0002). Handled
		// first so navigation is never frozen. In the Transcript panel the block
		// cursor moved off tab to n/N (Resolved Decision 9), so tab is unambiguous.
		switch {
		case keybind.Matches(msg, m.keys.FocusNext):
			// When the gate has focus, tab cycles queue entries instead of regions
			// (ADR 0005 §entry-navigation). With a single entry the index is stable.
			// Entry cycling intentionally does NOT call reloadTranscript or move
			// cursor — queue navigation and Steps/Transcript navigation are
			// independent (Decision 2).
			if m.focus == focusGate {
				if n := len(m.inputQueue); n > 1 {
					m.syncActiveTextarea()
					m.activeInputIdx = (m.activeInputIdx + 1) % n
					m.loadActiveTextarea()
					m.refreshPanels()
				}
				return m, nil
			}
			m.focus = m.cycleFocus(+1)
			m.refreshPanels()
			return m, nil
		case keybind.Matches(msg, m.keys.FocusPrev):
			if m.focus == focusGate {
				if n := len(m.inputQueue); n > 1 {
					m.syncActiveTextarea()
					m.activeInputIdx = (m.activeInputIdx - 1 + n) % n
					m.loadActiveTextarea()
					m.refreshPanels()
				}
				return m, nil
			}
			m.focus = m.cycleFocus(-1)
			m.refreshPanels()
			return m, nil
		case keybind.Matches(msg, m.keys.PanelFocus):
			// left/right exits the gate first — save the draft before leaving.
			if m.focus == focusGate {
				m.syncActiveTextarea()
			}
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
			((entry.kind == inputKindReview || entry.kind == inputKindRecovery) && entry.composing)) {
		var taCmd tea.Cmd
		m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
		m.refreshPanels()
		return m, taCmd
	}

	// Route remaining messages (scroll keys, mouse wheel) to the focused panel.
	var cmd tea.Cmd
	if m.focus == focusTranscript {
		m.chatVP, cmd = m.chatVP.Update(msg)
		m.chatAutoScroll = m.chatVP.AtBottom()
	} else {
		m.vp, cmd = m.vp.Update(msg)
	}
	return m, cmd
}

// hasGate reports whether the input queue has any pending entries.
func (m Model) hasGate() bool {
	return len(m.inputQueue) > 0
}

// textareaActive reports whether the focused gate is currently capturing free
// text in the shared textarea, in which case printable keys (including "?") are
// literal input rather than commands. Mirrors the message-routing condition in
// Update so the two never disagree.
func (m Model) textareaActive() bool {
	if m.focus != focusGate {
		return false
	}
	entry, ok := m.activeEntry()
	if !ok {
		return false
	}
	switch entry.kind {
	case inputKindRequest, inputKindPrompt:
		return true
	case inputKindReview, inputKindRecovery:
		return entry.composing
	case inputKindQuestion:
		return entry.question.CapturesText()
	}
	return false
}

// cycleFocus advances focus by dir (+1 next, -1 previous) across the regions
// currently present: Steps and Transcript are always present; Gate only when a
// gate is pending. In the narrow single-panel fallback the two panels still
// cycle (toggling which one is shown), so both remain present.
func (m Model) cycleFocus(dir int) focusRegion {
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
func (m Model) aliasPanelFocus(key string) focusRegion {
	if key == "right" {
		return focusTranscript
	}
	return focusSteps
}

// updateSteps handles keys when the Steps panel holds focus: j/k move the
// selection cursor (eagerly reloading the Transcript per Resolved Decision 10),
// space toggles the file tree expand/collapse, and esc/q leave to the runs list.
func (m Model) updateSteps(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	rows := m.visibleRows()
	switch {
	case keybind.Matches(msg, m.keys.Down):
		if m.cursor < len(rows)-1 {
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
		m.focus = focusTranscript
		m.refreshPanels()
		return m, nil
	case keybind.Matches(msg, m.keys.ToggleTree):
		// space: toggle expand/collapse on the cursored step.
		// No-op if cursor is on a file row (files cannot be expanded).
		if !m.cursorIsFileRow() {
			stepID := m.cursorStepID()
			if stepID != "" {
				m.expanded[stepID] = !m.expanded[stepID]
				// Discover and cache files on first expand.
				if m.expanded[stepID] && len(m.stepFiles[stepID]) == 0 {
					m.stepFiles[stepID] = stepOutputFiles(m.RunDir, stepID, "")
				}
				m.reloadTranscript()
				m.refreshPanels()
			}
		}
		return m, nil
	case keybind.Matches(msg, m.keys.StepsLeave):
		return m, func() tea.Msg { return ShowRunsMsg{} }

	// ── spec 08 C4: stop/reset/resume ─────────────────────────────────────────
	case keybind.Matches(msg, m.keys.StopStep):
		actions := m.selectedLifecycleActions()
		if actions.canStop {
			runID, stepID := m.RunID, actions.stepID
			return m, func() tea.Msg { return StopStepMsg{RunID: runID, StepID: stepID} }
		}
		return m, nil

	case keybind.Matches(msg, m.keys.ResetStep):
		actions := m.selectedLifecycleActions()
		if actions.canReset {
			runID, stepID := m.RunID, actions.stepID
			return m, func() tea.Msg { return RequestResetMsg{RunID: runID, StepID: stepID} }
		}
		return m, nil

	case keybind.Matches(msg, m.keys.ResumeStep):
		actions := m.selectedLifecycleActions()
		if actions.canResume {
			runID, stepID := m.RunID, actions.stepID
			return m, func() tea.Msg { return ResumeStepMsg{RunID: runID, StepID: stepID} }
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// updateTranscript handles keys when the Transcript panel holds focus: j/k move
// the block cursor between collapsible items (vim-style message navigation),
// J/K scroll the transcript viewport one line at a time, enter/space toggle the
// cursored block, o toggles all, and h/esc return focus to the Steps panel.
// Arrow keys and ctrl+d/u/pgup/pgdn fall through to the viewport as before.
func (m Model) updateTranscript(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	// G jumps to the bottom and re-enables auto-scroll (always processed first
	// so shift+G never starts a gg chord).
	if keybind.Matches(msg, m.keys.GotoBottom) {
		m.pendingGPrefix = false
		m.chatVP.GotoBottom()
		m.chatAutoScroll = true
		return m, nil
	}
	// gg chord: first g arms the prefix; second g fires GotoTop.
	if keybind.Matches(msg, m.keys.GotoTop) {
		if m.pendingGPrefix {
			m.pendingGPrefix = false
			m.chatVP.GotoTop()
			m.chatAutoScroll = false
			return m, nil
		}
		m.pendingGPrefix = true
		return m, nil
	}
	// Any other key cancels a pending g prefix before further processing.
	m.pendingGPrefix = false

	switch {
	case keybind.Matches(msg, m.keys.TransToSteps):
		m.focus = focusSteps
		m.refreshPanels()
		return m, nil
	case keybind.Matches(msg, m.keys.TransLeave):
		return m, func() tea.Msg { return ShowRunsMsg{} }
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
		if n := len(m.chatBlocks); n > 0 && m.chatBlockCursor < n {
			item := m.chatBlocks[m.chatBlockCursor]
			saved := item
			if item.isGroup {
				m.chatGroupExpand[item.key] = !m.chatGroupExpand[item.key]
			} else {
				m.chatExpand[item.key] = !m.chatExpand[item.key]
			}
			m.rebuildActiveState(saved)
			m.refreshPanels()
		}
		return m, nil
	case keybind.Matches(msg, m.keys.ExpandAll):
		saved := chatItem{}
		if len(m.chatBlocks) > 0 {
			saved = m.chatBlocks[m.chatBlockCursor]
		}
		m.chatExpandAll = !m.chatExpandAll
		m.rebuildActiveState(saved)
		m.refreshPanels()
		return m, nil
	case keybind.Matches(msg, m.keys.ScrollDown):
		m.chatVP.ScrollDown(1)
		m.chatAutoScroll = m.chatVP.AtBottom()
		m.refreshPanels()
		return m, nil
	case keybind.Matches(msg, m.keys.ScrollUp):
		m.chatVP.ScrollUp(1)
		m.chatAutoScroll = m.chatVP.AtBottom()
		m.refreshPanels()
		return m, nil
	}
	// Arrow keys and ctrl+d/u/pgup/pgdn fall through to the viewport.
	var cmd tea.Cmd
	m.chatVP, cmd = m.chatVP.Update(msg)
	m.chatAutoScroll = m.chatVP.AtBottom()
	return m, cmd
}

// refreshPanels re-renders both always-visible panels into their viewports. A
// no-op until the first resize makes the model ready.
func (m *Model) refreshPanels() {
	if !m.ready {
		return
	}
	m.vp.SetContent(m.listBody())
	m.chatVP.SetContent(m.chatBody())
}

// helpBoxW/H return the outer modal dimensions, mirroring helpOverlay in
// monitor_view.go. Kept in sync by hand — update both if the ratio changes.
func (m Model) helpBoxW() int {
	w := m.width * 60 / 100
	if w < 40 {
		w = 40
	}
	return w
}

func (m Model) helpBoxH() int {
	h := m.height * 80 / 100
	if h < 10 {
		h = 10
	}
	return h
}

// toggleHelpChat opens or closes the help agent modal.
// On first open it initialises helpModel and fires connectCmd; subsequent
// open/close cycles just flip helpOpen, preserving conversation history.
func (m Model) toggleHelpChat() (Model, tea.Cmd) {
	if m.helpOpen {
		m.helpOpen = false
		return m, nil
	}
	m.helpOpen = true
	if m.helpReady {
		// Already initialised — send size so the modal is sized for current window.
		var hCmd tea.Cmd
		m.helpModel, hCmd = m.helpModel.Update(helpchat.SizeMsg{W: m.helpBoxW(), H: m.helpBoxH()})
		return m, hCmd
	}

	// First open: create rendezvous channels, build helpModel, fire Init.
	gateReq := make(chan struct{}, 1)
	gateAns := make(chan bool, 1)
	m.helpGateReq = gateReq
	m.helpGateAns = gateAns

	sizeCmd := func() tea.Msg { return helpchat.SizeMsg{W: m.helpBoxW(), H: m.helpBoxH()} }

	if m.run == nil {
		// Journal-replayed run — pre-populate unavailable message, skip SDK connect.
		m.helpModel = helpchat.NewUnavailable()
		m.helpReady = true
		return m, sizeCmd
	}

	snap := m.run.Snapshot()
	m.helpModel = helpchat.New(m.run, m.RunDir, snap)
	m.helpModel.SetChannels(gateReq, gateAns)
	initCmd := m.helpModel.Init()
	m.helpReady = true

	// Arm the dispatch drain and gate listener.
	var cmds []tea.Cmd
	if initCmd != nil {
		cmds = append(cmds, initCmd)
	}
	cmds = append(cmds, sizeCmd)
	cmds = append(cmds, helpchat.WaitForDispatchCmd(m.helpModel.DispatchCh()))
	cmds = append(cmds, waitForGateReqCmd(gateReq))
	return m, tea.Batch(cmds...)
}

// dispatchHelpAction converts a helpchat.DispatchedMsg to the appropriate
// monitor message and re-queues it so the root model handles it identically
// to a keyboard-triggered gate action.
func (m Model) dispatchHelpAction(msg helpchat.DispatchedMsg) tea.Cmd {
	// Re-arm the dispatch drain first.
	drainCmd := helpchat.WaitForDispatchCmd(m.helpModel.DispatchCh())
	var innerCmd tea.Cmd
	switch a := msg.Inner.(type) {
	case helpchat.RecoverAction:
		innerCmd = func() tea.Msg {
			return RecoverResponseMsg{RunID: m.RunID, StepID: a.StepID, Action: a.Action, Text: a.Text}
		}
	case helpchat.ResetAction:
		innerCmd = func() tea.Msg { return RequestResetMsg{RunID: m.RunID, StepID: a.StepID} }
	case helpchat.StopAction:
		innerCmd = func() tea.Msg { return StopStepMsg{RunID: m.RunID, StepID: a.StepID} }
	case helpchat.ResumeAction:
		innerCmd = func() tea.Msg { return ResumeStepMsg{RunID: m.RunID, StepID: a.StepID, Message: a.Message} }
	case helpchat.ReviewVerdict:
		innerCmd = func() tea.Msg {
			return ReviewVerdictMsg{RunID: m.RunID, StepID: a.StepID, Verdict: a.Verdict}
		}
	case helpchat.ReviewMessage:
		innerCmd = func() tea.Msg { return ReviewMessageMsg{RunID: m.RunID, StepID: a.StepID, Text: a.Text} }
	}
	if innerCmd != nil {
		return tea.Batch(innerCmd, drainCmd)
	}
	return drainCmd
}

// waitForGateReqCmd blocks on the gate request channel (read by the monitor,
// not the helpchat model) and returns FinalMergeGateMsg when the tool handler
// signals that the operator's TUI confirmation is needed.
func waitForGateReqCmd(gateReq <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-gateReq
		return helpchat.FinalMergeGateMsg{}
	}
}
