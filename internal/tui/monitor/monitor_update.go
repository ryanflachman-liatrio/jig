package monitor

import (
	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/step"
)

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

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
// and esc/q leave to the runs list.
func (m Model) updateSteps(msg tea.KeyPressMsg) (Model, tea.Cmd) {
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
		return m, func() tea.Msg { return ShowRunsMsg{} }

	// ── spec 08 C4: stop/reset/resume ─────────────────────────────────────────
	case keybind.Matches(msg, m.keys.StopStep):
		if !m.done && m.cursor < len(m.steps) {
			st := m.steps[m.cursor]
			if st.status == step.StatusRunning {
				runID, stepID := m.RunID, st.id
				return m, func() tea.Msg { return StopStepMsg{RunID: runID, StepID: stepID} }
			}
		}
		return m, nil

	case keybind.Matches(msg, m.keys.ResetStep):
		if !m.done && m.cursor < len(m.steps) {
			st := m.steps[m.cursor]
			// Only terminal/stopped steps can be reset, and only when the run is
			// quiescent (no worker in flight). We delegate the quiescence check to
			// handleReset in the engine; the TUI pre-filters obviously ineligible
			// cases to avoid a noisy no-op.
			switch st.status {
			case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped,
				step.StatusStopped, step.StatusAwaitingReview:
				runID, stepID := m.RunID, st.id
				return m, func() tea.Msg { return RequestResetMsg{RunID: runID, StepID: stepID} }
			}
		}
		return m, nil

	case keybind.Matches(msg, m.keys.ResumeStep):
		if !m.done && m.cursor < len(m.steps) {
			st := m.steps[m.cursor]
			if st.status == step.StatusStopped {
				runID, stepID := m.RunID, st.id
				return m, func() tea.Msg { return ResumeStepMsg{RunID: runID, StepID: stepID} }
			}
		}
		return m, nil
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
