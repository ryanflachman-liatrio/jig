package monitor

import (
	"fmt"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	reviewworkspace "jig/internal/tui/review"
	"jig/internal/tui/shared"
)

type recoveryActionSpec struct {
	binding keybind.Binding
	body    string
	action  string
	compose bool
}

const recoveryActionRows = 4

func (m Model) recoveryActions(rec *engine.RecoveryRequest) []recoveryActionSpec {
	actions := []recoveryActionSpec{
		{binding: m.keys.RecoverRetry, body: "retry", action: engine.RecoverRetry},
	}
	if rec != nil && rec.CanResume {
		actions = append(actions, recoveryActionSpec{
			binding: m.keys.RecoverGuide,
			body:    "retry with guidance",
			action:  engine.RecoverResume,
			compose: true,
		})
	}
	return append(actions,
		recoveryActionSpec{
			binding: m.keys.RecoverSkip,
			body:    "skip — accept failure and continue",
			action:  engine.RecoverSkip,
		},
		recoveryActionSpec{binding: m.keys.RecoverAbort, body: "abort run", action: engine.RecoverAbort},
	)
}

// activeEntry returns a pointer to the entry at activeInputIdx, or (nil, false)
// when the queue is empty or the index is out of range.
func (m Model) activeEntry() (*pendingInputEntry, bool) {
	if len(m.inputQueue) == 0 || m.activeInputIdx < 0 || m.activeInputIdx >= len(m.inputQueue) {
		return nil, false
	}
	return &m.inputQueue[m.activeInputIdx], true
}

// removeEntryAt deletes the entry at index i, then clamps/advances activeInputIdx:
// the next entry stays at position i (entries shift left); if the removed entry
// was last, activeInputIdx clamps to the new last; if the queue empties, focus
// returns to Steps. Always rebuilds promptTextarea via loadActiveTextarea so the
// textarea tracks the new active entry without callers needing to know.
func (m *Model) removeEntryAt(i int) {
	if i < 0 || i >= len(m.inputQueue) {
		return
	}
	if m.reviewOpen && i == m.activeInputIdx {
		m.reviewOpen = false
	}
	// A decision can be submitted after inspecting its related transcript.
	// Restore the operator's prior selection before dropping the return target.
	if m.gateContext != nil {
		m.restoreGateContext()
	}
	m.inputQueue = append(m.inputQueue[:i], m.inputQueue[i+1:]...)
	if len(m.inputQueue) == 0 {
		m.activeInputIdx = 0
		m.focus = focusSteps
		m.loadActiveTextarea()
		return
	}
	if m.activeInputIdx >= len(m.inputQueue) {
		m.activeInputIdx = len(m.inputQueue) - 1
	}
	m.loadActiveTextarea()
}

// syncActiveTextarea saves the current textarea content into the active entry's
// draft field so it survives queue navigation or a gate blur.
func (m *Model) syncActiveTextarea() {
	if m.activeInputIdx < 0 || m.activeInputIdx >= len(m.inputQueue) {
		return
	}
	entry := &m.inputQueue[m.activeInputIdx]
	switch entry.kind {
	case inputKindRequest, inputKindPrompt:
		entry.draft = m.promptTextarea.Value()
	case inputKindRecovery:
		if entry.composing {
			entry.draft = m.promptTextarea.Value()
		}
	}
}

// loadActiveTextarea rebuilds m.promptTextarea from the active entry's draft
// with the correct placeholder and height for its kind. Called after entry
// navigation and after removeEntryAt advances the index.
func (m *Model) loadActiveTextarea() {
	if m.activeInputIdx < 0 || m.activeInputIdx >= len(m.inputQueue) {
		m.promptTextarea = textarea.Model{}
		return
	}
	entry := &m.inputQueue[m.activeInputIdx]
	switch entry.kind {
	case inputKindRequest:
		ta := shared.NewInputTextarea("Message to agent…", m.gateInnerWidth(), gateTextareaRows, shared.WithoutBorder())
		ta.SetValue(entry.draft)
		m.promptTextarea = ta
	case inputKindPrompt:
		label := entry.prompt.Label
		if label == "" {
			label = "Input…"
		}
		ta := shared.NewInputTextarea(label, m.gateInnerWidth(), gateTextareaRows, shared.WithoutBorder())
		ta.SetValue(entry.draft)
		m.promptTextarea = ta
	case inputKindRecovery:
		if entry.composing {
			ta := shared.NewInputTextarea("Guidance for the retry (optional)…", m.gateInnerWidth(), gateTextareaRows, shared.WithoutBorder())
			ta.SetValue(entry.draft)
			m.promptTextarea = ta
		} else {
			m.promptTextarea = textarea.Model{}
		}
	default: // inputKindQuestion
		m.promptTextarea = textarea.Model{}
	}
}

type gatePresentation struct {
	title        string
	subjectLabel string
	subject      string
	action       string
	contextStep  string
	contextName  string
}

func presentationForGate(entry *pendingInputEntry) gatePresentation {
	if entry == nil {
		return gatePresentation{title: "Human actions"}
	}

	p := gatePresentation{
		subjectLabel: "Step",
		subject:      entry.stepID,
		contextStep:  entry.stepID,
		contextName:  "transcript",
	}
	switch entry.kind {
	case inputKindRequest:
		p.title = "Agent input required"
		p.action = "Enter a message to continue"
	case inputKindQuestion:
		p.title = "Answer required"
		p.action = "Choose or enter an answer"
	case inputKindPrompt:
		p.title = "User input required"
		p.action = "Provide input to continue"
	case inputKindReview:
		p.title = "Review required"
		if entry.workspace != nil {
			p.action = "Review documents and submit feedback"
			p.contextStep = ""
			p.contextName = ""
		} else {
			p.action = "Choose a decision"
			p.contextName = "diff"
		}
	case inputKindRecovery:
		p.title = "Recovery action"
		p.action = "Retry, guide, skip, or abort"
	case inputKindIntegrationConflict:
		p.title = "Conflict resolution"
		p.action = "Resolve the conflict or abort"
	case inputKindFinalMerge:
		p.title = "Merge approval"
		p.subjectLabel = "Run branch"
		p.contextStep = ""
		p.contextName = ""
		p.action = "Merge or discard the run branch"
		if entry.finalMerge != nil && entry.finalMerge.RunBranch != "" {
			p.subject = entry.finalMerge.RunBranch
		}
	case inputKindResetConfirm:
		p.title = "Reset confirmation"
		p.action = "Confirm or cancel the reset"
	case inputKindHelpFinalMerge:
		p.title = "Merge approval"
		p.subjectLabel = "Scope"
		p.subject = "Run-level action"
		p.contextStep = ""
		p.contextName = ""
		p.action = "Approve or discard the run branch"
	default:
		p.title = "Human action required"
		p.action = "Respond to continue"
	}
	if p.subject == "" {
		p.subject = "Unknown"
	}
	return p
}

func (m Model) gateHasInnerBack(entry *pendingInputEntry) bool {
	if entry == nil {
		return false
	}
	if entry.composing {
		return entry.kind == inputKindRecovery
	}
	return entry.kind == inputKindQuestion && entry.question.HasInnerBack()
}

func (m Model) gateEscapeBinding(entry *pendingInputEntry) keybind.Binding {
	if m.gateHasInnerBack(entry) {
		return m.keys.GateBack
	}
	return m.keys.GateBlur
}

func (m Model) updateGateEscape(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	if entry.kind == inputKindQuestion && entry.question.HasInnerBack() {
		return m.updateGateQuestion(msg, entry)
	}
	if m.gateHasInnerBack(entry) {
		m.syncActiveTextarea()
		m.inputQueue[m.activeInputIdx].composing = false
		m.loadActiveTextarea()
		m.refreshPanels()
		return m, nil
	}

	m.syncActiveTextarea()
	m.focus = focusSteps
	m.refreshPanels()
	return m, nil
}

// updateGate handles keys when the gate holds focus. Dispatches by the active
// entry's kind; each submit path reads routing IDs from the entry, emits the
// unchanged routing message, and removes the entry (auto-advance via removeEntryAt).
func (m Model) updateGate(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	entry, ok := m.activeEntry()
	if !ok {
		return m, nil
	}
	if m.historical {
		if keybind.Matches(msg, m.keys.GateBlur) {
			return m.updateGateEscape(msg, entry)
		}
		return m, nil
	}

	if entry.kind == inputKindReview && entry.workspace != nil {
		if !m.reviewOpen {
			if keybind.Matches(msg, m.keys.ReviewOpen) {
				m.reviewOpen = true
				w, h := m.reviewWorkspaceInnerSize()
				workspace, _ := entry.workspace.Update(tea.WindowSizeMsg{Width: w, Height: h})
				m.inputQueue[m.activeInputIdx].workspace = &workspace
				m.refreshPanels()
				return m, nil
			}
			if keybind.Matches(msg, m.keys.GateBlur) {
				return m.updateGateEscape(msg, entry)
			}
			return m, nil
		}

		// An open review workspace owns editor escape and all local keys. A
		// browse-mode escape closes only the workspace, returning to the Gate.
		if keybind.Matches(msg, m.keys.GateBlur) && entry.workspace.Mode() == reviewworkspace.ModeBrowse {
			m.reviewOpen = false
			m.refreshPanels()
			return m, nil
		}
		// y/Y build a clipboard request from the workspace's captured state
		// and hand it to the root without ever calling workspace.Update — so
		// no draft persistence side-effect can escape a copy key. Copy is
		// only honoured in browse/select-range modes; when the composer or
		// summary editor captures text, the key is a literal input.
		if entry.workspace.Mode() != reviewworkspace.ModeSummary &&
			!entry.workspace.CapturesText() {
			key := msg.String()
			if entry.workspace.MatchesCopyItem(key) {
				req, ok := entry.workspace.CopyItemRequest()
				if !ok {
					target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewLine, Label: entry.stepID}
					return m, func() tea.Msg {
						return shared.ClipboardRequest{
							Target: target,
							Loader: func() shared.ClipboardPayload {
								return shared.ClipboardPayload{Err: shared.ErrClipboardUnavailable}
							},
						}
					}
				}
				return m, func() tea.Msg { return req }
			}
			if entry.workspace.MatchesCopyAll(key) {
				req, ok := entry.workspace.CopyAllRequest()
				if !ok {
					target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewDocument, Label: entry.stepID}
					return m, func() tea.Msg {
						return shared.ClipboardRequest{
							Target: target,
							Loader: func() shared.ClipboardPayload {
								return shared.ClipboardPayload{Err: shared.ErrClipboardUnavailable}
							},
						}
					}
				}
				return m, func() tea.Msg { return req }
			}
		}
		workspace, cmd := entry.workspace.Update(msg)
		m.inputQueue[m.activeInputIdx].workspace = &workspace
		m.refreshPanels()
		persist := func() tea.Msg { return reviewworkspace.DraftChangedMsg{Draft: workspace.Draft()} }
		return m, tea.Batch(cmd, persist)
	}

	if keybind.Matches(msg, m.keys.GateBlur) {
		return m.updateGateEscape(msg, entry)
	}

	switch entry.kind {
	case inputKindRequest:
		return m.updateGateRequest(msg, entry)
	case inputKindQuestion:
		return m.updateGateQuestion(msg, entry)
	case inputKindPrompt:
		return m.updateGatePrompt(msg, entry)
	case inputKindReview:
		return m.updateGateReview(msg, entry)
	case inputKindRecovery:
		return m.updateGateRecovery(msg, entry)
	case inputKindIntegrationConflict:
		return m.updateGateIntegration(msg, entry)
	case inputKindFinalMerge:
		return m.updateGateFinalMerge(msg, entry)
	case inputKindHelpFinalMerge:
		return m.updateGateHelpFinalMerge(msg)
	case inputKindResetConfirm:
		return m.updateGateResetConfirm(msg, entry)
	}

	return m, nil
}

func (m Model) updateGateRequest(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	if keybind.Matches(msg, m.keys.Submit) {
		text := m.promptTextarea.Value()
		if text == "" {
			return m, nil
		}
		inp := entry.request
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return AgentInputMsg{RunID: inp.RunID, StepID: inp.StepID, Text: text}
		}
	}
	var taCmd tea.Cmd
	m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
	m.refreshPanels()
	return m, taCmd
}

func (m Model) updateGateQuestion(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	idx := m.activeInputIdx
	if idx < 0 || idx >= len(m.inputQueue) {
		return m, nil
	}
	panel, cmd := entry.question.Update(msg)
	m.inputQueue[idx].question = panel
	response, done := panel.Response()
	if !done {
		m.refreshPanels()
		return m, cmd
	}
	stepID := entry.stepID
	m.removeEntryAt(idx)
	m.refreshPanels()
	return m, func() tea.Msg {
		return AgentQuestionResponseMsg{RunID: m.RunID, StepID: stepID, Response: response}
	}
}

func (m Model) updateGatePrompt(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	if keybind.Matches(msg, m.keys.Submit) {
		text := m.promptTextarea.Value()
		if text == "" {
			return m, nil
		}
		pr := entry.prompt
		m.focusNextPromptStep = pr.StepID
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return UserInputResponseMsg{
				RunID:  pr.RunID,
				StepID: pr.StepID,
				As:     pr.As,
				Text:   text,
			}
		}
	}
	var taCmd tea.Cmd
	m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
	m.refreshPanels()
	return m, taCmd
}

func (m Model) updateGateReview(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	for i, ch := range entry.review.Choices {
		if msg.String() == fmt.Sprintf("%d", i+1) {
			rev := entry.review
			m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
			m.refreshPanels()
			return m, func() tea.Msg {
				return ReviewVerdictMsg{RunID: rev.RunID, StepID: rev.StepID, Verdict: ch}
			}
		}
	}
	return m, nil
}

func (m Model) updateGateRecovery(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	rec := entry.recovery
	if entry.composing {
		// Composing guidance: enter resumes the failed session with the error
		// and this text folded in. Empty is allowed (resume with just the error).
		if keybind.Matches(msg, m.keys.Submit) {
			text := m.promptTextarea.Value()
			m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
			m.refreshPanels()
			return m, func() tea.Msg {
				return RecoverResponseMsg{RunID: rec.RunID, StepID: rec.StepID, Action: engine.RecoverResume, Text: text}
			}
		}
		var taCmd tea.Cmd
		m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
		m.refreshPanels()
		return m, taCmd
	}
	for _, action := range m.recoveryActions(rec) {
		if !keybind.Matches(msg, action.binding) {
			continue
		}
		if action.compose {
			m.inputQueue[m.activeInputIdx].composing = true
			m.loadActiveTextarea()
			m.refreshPanels()
			return m, textarea.Blink
		}
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return RecoverResponseMsg{RunID: rec.RunID, StepID: rec.StepID, Action: action.action}
		}
	}
	return m, nil
}

func (m Model) updateGateIntegration(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	ic := entry.integration
	// Resolve: the operator merged the conflict in the run worktree; the engine
	// finishes the integration. Abort: fail the step (→ recovery gate).
	if keybind.Matches(msg, m.keys.IntegrationResolve) {
		return m, func() tea.Msg {
			return ResolveIntegrationResponseMsg{RunID: ic.RunID, StepID: ic.StepID, Abort: false}
		}
	}
	if ic.CanAgentResolve && keybind.Matches(msg, m.keys.IntegrationAgent) {
		return m, func() tea.Msg {
			return ResolveIntegrationWithAgentMsg{RunID: ic.RunID, StepID: ic.StepID}
		}
	}
	if keybind.Matches(msg, m.keys.RecoverAbort) {
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return ResolveIntegrationResponseMsg{RunID: ic.RunID, StepID: ic.StepID, Abort: true}
		}
	}
	return m, nil
}

func (m Model) updateGateFinalMerge(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	fm := entry.finalMerge
	// Approve lands the run branch onto the base; discard leaves it in place.
	if keybind.Matches(msg, m.keys.FinalMergeApprove) {
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return FinalMergeResponseMsg{RunID: fm.RunID, Approve: true}
		}
	}
	if keybind.Matches(msg, m.keys.FinalMergeDiscard) {
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return FinalMergeResponseMsg{RunID: fm.RunID, Approve: false}
		}
	}
	return m, nil
}

// updateGateHelpFinalMerge handles y/d on the help-agent final-merge gate.
// The verdict is written to helpGateAns (buffered size 1) so the blocked tool
// handler can unblock; no monitor message is emitted because the response goes
// directly to the tool rather than through the engine's ApproveMerge path.
func (m Model) updateGateHelpFinalMerge(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	send := func(verdict bool) (Model, tea.Cmd) {
		m.removeEntryAt(m.activeInputIdx)
		m.refreshPanels()
		ch := m.helpGateAns
		return m, func() tea.Msg {
			ch <- verdict
			return nil
		}
	}
	if keybind.Matches(msg, m.keys.FinalMergeApprove) {
		return send(true)
	}
	if keybind.Matches(msg, m.keys.FinalMergeDiscard) {
		return send(false)
	}
	return m, nil
}

func (m Model) updateGateResetConfirm(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	rc := entry.resetConfirm
	if keybind.Matches(msg, m.keys.ResetConfirm) {
		m.removeEntryAt(m.activeInputIdx)
		m.refreshPanels()
		return m, func() tea.Msg {
			return ResetStepMsg{RunID: rc.runID, StepID: rc.stepID}
		}
	}
	if keybind.Matches(msg, m.keys.ResetCancel) {
		m.removeEntryAt(m.activeInputIdx)
		m.refreshPanels()
	}
	return m, nil
}
