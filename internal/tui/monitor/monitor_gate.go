package monitor

import (
	"encoding/json"
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/tui/shared"
)

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
// draft field so it survives tab-navigation between entries or a gate blur.
func (m *Model) syncActiveTextarea() {
	if m.activeInputIdx < 0 || m.activeInputIdx >= len(m.inputQueue) {
		return
	}
	entry := &m.inputQueue[m.activeInputIdx]
	switch entry.kind {
	case inputKindRequest, inputKindPrompt:
		entry.draft = m.promptTextarea.Value()
	case inputKindReview, inputKindRecovery:
		if entry.composing {
			entry.draft = m.promptTextarea.Value()
		}
	}
}

// loadActiveTextarea rebuilds m.promptTextarea from the active entry's draft
// with the correct placeholder and height for its kind. Called after entry
// navigation (tab/shift+tab) and after removeEntryAt advances the index.
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
	case inputKindReview:
		if entry.composing {
			ta := shared.NewInputTextarea("Message to agent…", m.gateInnerWidth(), gateTextareaRows, shared.WithoutBorder())
			ta.SetValue(entry.draft)
			m.promptTextarea = ta
		} else {
			m.promptTextarea = textarea.Model{}
		}
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

// kindName returns the short display label for a gate entry kind used in the
// [N / M]  step-id  (kind) header.
func kindName(k pendingInputKind) string {
	switch k {
	case inputKindReview:
		return "review"
	case inputKindQuestion:
		return "question"
	case inputKindPrompt:
		return "prompt"
	case inputKindRecovery:
		return "recovery"
	case inputKindIntegrationConflict:
		return "integration"
	case inputKindFinalMerge:
		return "final merge"
	case inputKindResetConfirm:
		return "reset confirm"
	default:
		return "input"
	}
}

// updateGate handles keys when the gate holds focus. Dispatches by the active
// entry's kind; each submit path reads routing IDs from the entry, emits the
// unchanged routing message, and removes the entry (auto-advance via removeEntryAt).
func (m Model) updateGate(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	entry, ok := m.activeEntry()
	if !ok {
		return m, nil
	}

	// esc blurs the gate to Steps without navigating away (ADR 0005 §esc-blurs).
	// Handled before the per-kind switch so all kinds share one blur path.
	if keybind.Matches(msg, m.keys.GateBlur) {
		m.syncActiveTextarea()
		m.focus = focusSteps
		m.refreshPanels()
		return m, nil
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
	// q cancels the question and delivers a "cancelled" answer so the engine
	// can continue; esc is caught by GateBlur above (blurs without cancelling).
	if keybind.Matches(msg, m.keys.QuestionCancel) && msg.String() == "q" {
		q := entry.question
		m.removeEntryAt(idx)
		m.refreshPanels()
		return m, func() tea.Msg {
			return AgentQuestionResponseMsg{
				RunID:     q.RunID,
				StepID:    q.StepID,
				ToolUseID: q.ToolUseID,
				Answer:    "cancelled",
			}
		}
	}
	// j/↓ and k/↑ scroll the option list (Unit 6). These do not collide with
	// tab (entry cycle), left/right (panel exit), digits (option select), q
	// (cancel), or enter/space (QConfirm).
	switch msg.String() {
	case "j", "down":
		if idx >= 0 && idx < len(m.inputQueue) {
			qIdx := m.inputQueue[idx].questionIdx
			if qIdx < len(entry.question.Questions) {
				opts := entry.question.Questions[qIdx].Options
				if m.inputQueue[idx].scrollOffset < len(opts)-1 {
					m.inputQueue[idx].scrollOffset++
					m.refreshPanels()
				}
			}
		}
		return m, nil
	case "k", "up":
		if idx >= 0 && idx < len(m.inputQueue) {
			if m.inputQueue[idx].scrollOffset > 0 {
				m.inputQueue[idx].scrollOffset--
				m.refreshPanels()
			}
		}
		return m, nil
	}
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
}

func (m Model) updateGatePrompt(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	if keybind.Matches(msg, m.keys.Submit) {
		text := m.promptTextarea.Value()
		if text == "" {
			return m, nil
		}
		pr := entry.prompt
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
	if entry.composing {
		if keybind.Matches(msg, m.keys.ComposeCancel) {
			m.inputQueue[m.activeInputIdx].composing = false
			m.loadActiveTextarea()
			m.refreshPanels()
			return m, nil
		}
		if keybind.Matches(msg, m.keys.Submit) {
			text := m.promptTextarea.Value()
			if text == "" {
				return m, nil
			}
			rev := entry.review
			m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
			m.refreshPanels()
			return m, func() tea.Msg {
				return ReviewMessageMsg{RunID: rev.RunID, StepID: rev.StepID, Text: text}
			}
		}
		var taCmd tea.Cmd
		m.promptTextarea, taCmd = m.promptTextarea.Update(msg)
		m.refreshPanels()
		return m, taCmd
	}
	if entry.review.AllowMessage && keybind.Matches(msg, m.keys.Message) {
		m.inputQueue[m.activeInputIdx].composing = true
		m.loadActiveTextarea() // review-composing branch builds the message textarea
		m.refreshPanels()
		return m, textarea.Blink
	}
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
	if keybind.Matches(msg, m.keys.RecoverRetry) {
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return RecoverResponseMsg{RunID: rec.RunID, StepID: rec.StepID, Action: engine.RecoverRetry}
		}
	}
	if rec.CanResume && keybind.Matches(msg, m.keys.RecoverGuide) {
		m.inputQueue[m.activeInputIdx].composing = true
		m.loadActiveTextarea() // recovery-composing branch builds the guidance textarea
		m.refreshPanels()
		return m, textarea.Blink
	}
	if keybind.Matches(msg, m.keys.RecoverSkip) {
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return RecoverResponseMsg{RunID: rec.RunID, StepID: rec.StepID, Action: engine.RecoverSkip}
		}
	}
	if keybind.Matches(msg, m.keys.RecoverAbort) {
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return RecoverResponseMsg{RunID: rec.RunID, StepID: rec.StepID, Action: engine.RecoverAbort}
		}
	}
	return m, nil
}

func (m Model) updateGateIntegration(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	ic := entry.integration
	// Resolve: the operator merged the conflict in the run worktree; the engine
	// finishes the integration. Abort: fail the step (→ recovery gate).
	if keybind.Matches(msg, m.keys.IntegrationResolve) {
		m.removeEntryAt(m.activeInputIdx) // also calls loadActiveTextarea
		m.refreshPanels()
		return m, func() tea.Msg {
			return ResolveIntegrationResponseMsg{RunID: ic.RunID, StepID: ic.StepID, Abort: false}
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

func (m Model) updateGateResetConfirm(msg tea.KeyPressMsg, entry *pendingInputEntry) (Model, tea.Cmd) {
	rc := entry.resetConfirm
	// y confirms the reset; n / esc / GateBlur cancel (esc caught above).
	if msg.String() == "y" {
		m.removeEntryAt(m.activeInputIdx)
		m.refreshPanels()
		return m, func() tea.Msg {
			return ResetStepMsg{RunID: rc.runID, StepID: rc.stepID}
		}
	}
	if msg.String() == "n" {
		m.removeEntryAt(m.activeInputIdx)
		m.refreshPanels()
	}
	return m, nil
}

// advanceQuestion records the answer for the current question and advances the
// question index on the active entry. When all questions are answered it removes
// the entry and emits AgentQuestionResponseMsg with the formatted answer.
func (m Model) advanceQuestion(answer string) (Model, tea.Cmd) {
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
		return AgentQuestionResponseMsg{
			RunID:     q.RunID,
			StepID:    q.StepID,
			ToolUseID: q.ToolUseID,
			Answer:    formatted,
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
