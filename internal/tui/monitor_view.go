package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/sentinel"
	"jig/internal/step"
)

// securityView renders the Security region: a compact list of findings by
// severity, visible only when at least one finding exists. Every row is rendered
// verbatim (not through glamour) so redacted previews like [aws-key:…MPLE] are
// displayed literally rather than being re-interpreted as markdown.
func (m monitorModel) securityView() string {
	if len(m.secFindings) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(theme.Security.Header.Render("Security findings"))
	sb.WriteString("\n")
	for _, f := range m.secFindings {
		sev := strings.ToUpper(string(f.Severity))
		label := "[" + sev + "] " + f.Monitor + ": "
		detail := f.Detail
		if detail == "" {
			detail = string(f.Action)
		}
		var row string
		switch f.Severity {
		case sentinel.SeverityCritical:
			row = theme.Security.CriticalRow.Render(label + detail)
		case sentinel.SeverityHigh:
			row = theme.Security.HighRow.Render(label + detail)
		case sentinel.SeverityMedium:
			row = theme.Security.MediumRow.Render(label + detail)
		default:
			row = theme.Security.LowRow.Render(label + detail)
		}
		sb.WriteString(row)
		sb.WriteString("\n")
	}
	return sb.String()
}

// statusLabel computes the status text shown in the footer.
func (m monitorModel) statusLabel() string {
	if m.done {
		if m.failed {
			return theme.Error.Render("failed")
		}
		return theme.Valid.Render("done")
	}
	entry, ok := m.activeEntry()
	if !ok {
		return theme.Running.Render("running")
	}
	n := len(m.inputQueue)
	queueSuffix := ""
	if n > 1 {
		queueSuffix = fmt.Sprintf(" (%d pending)", n)
	}
	switch entry.kind {
	case inputKindRequest:
		return theme.Marker.Render("awaiting agent input" + queueSuffix)
	case inputKindQuestion:
		return theme.Marker.Render("awaiting answer" + queueSuffix)
	case inputKindPrompt:
		return theme.Marker.Render("awaiting user input" + queueSuffix)
	case inputKindReview:
		if entry.composing {
			return theme.Marker.Render("composing message")
		}
		return theme.Marker.Render("awaiting review")
	case inputKindRecovery:
		if entry.composing {
			return theme.Marker.Render("composing guidance")
		}
		return theme.Error.Render("step failed — recovery" + queueSuffix)
	case inputKindIntegrationConflict:
		return theme.Error.Render("integration conflict" + queueSuffix)
	case inputKindFinalMerge:
		return theme.Marker.Render("awaiting final merge" + queueSuffix)
	}
	return theme.Running.Render("running")
}

// hintLabel computes the keyboard hint shown in the footer.
func (m monitorModel) hintLabel() string {
	switch {
	case m.focus == focusGate && m.hasGate():
		entry := m.inputQueue[m.activeInputIdx]
		// Show the entry-cycle hint only when there is more than one entry.
		entryNav := m.keys.GateEntryNav
		if len(m.inputQueue) < 2 {
			entryNav.SetEnabled(false)
		}
		switch entry.kind {
		case inputKindRequest:
			return hintString(m.keys.Submit, m.keys.Newline, entryNav, m.keys.GateBlur)
		case inputKindQuestion:
			if entry.questionIdx < len(entry.question.Questions) && entry.question.Questions[entry.questionIdx].MultiSelect {
				return hintString(m.keys.ToggleOpt, m.keys.QConfirm, m.keys.QuestionScroll, entryNav, m.keys.GateBlur)
			}
			return hintString(m.keys.Answer, m.keys.QuestionScroll, entryNav, m.keys.GateBlur)
		case inputKindReview:
			if entry.composing {
				return hintString(m.keys.Submit, m.keys.Newline, m.keys.GateBlur)
			} else if entry.review.AllowMessage {
				return hintString(m.keys.Verdict, m.keys.Message, entryNav, m.keys.GateBlur)
			}
			return hintString(m.keys.Verdict, entryNav, m.keys.GateBlur)
		case inputKindPrompt:
			return hintString(m.keys.Submit, m.keys.Newline, entryNav, m.keys.GateBlur)
		case inputKindRecovery:
			if entry.composing {
				return hintString(m.keys.Submit, m.keys.Newline, m.keys.GateBlur)
			} else if entry.recovery.CanResume {
				return hintString(m.keys.RecoverRetry, m.keys.RecoverGuide, m.keys.RecoverSkip, m.keys.RecoverAbort, entryNav, m.keys.GateBlur)
			}
			return hintString(m.keys.RecoverRetry, m.keys.RecoverSkip, m.keys.RecoverAbort, entryNav, m.keys.GateBlur)
		case inputKindIntegrationConflict:
			return hintString(m.keys.IntegrationResolve, m.keys.RecoverAbort, entryNav, m.keys.GateBlur)
		case inputKindFinalMerge:
			return hintString(m.keys.FinalMergeApprove, m.keys.FinalMergeDiscard, entryNav, m.keys.GateBlur)
		case inputKindResetConfirm:
			return hintString(m.keys.GateBlur) // y/n shown inline in the gate strip
		}
	case m.focus == focusTranscript:
		return hintString(m.keys.FocusFull, m.keys.Scroll, m.keys.GotoTop, m.keys.GotoBottom, m.keys.BlockNav, m.keys.Toggle, m.keys.ExpandAll)
	default: // focusSteps
		// Gate eligibility: advertise stop/reset/resume only for eligible steps.
		stopKey := m.keys.StopStep
		resetKey := m.keys.ResetStep
		resumeKey := m.keys.ResumeStep
		if m.done || m.cursor >= len(m.steps) {
			stopKey.SetEnabled(false)
			resetKey.SetEnabled(false)
			resumeKey.SetEnabled(false)
		} else {
			st := m.steps[m.cursor]
			stopKey.SetEnabled(st.status == step.StatusRunning)
			resumeKey.SetEnabled(st.status == step.StatusStopped)
			switch st.status {
			case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped,
				step.StatusStopped, step.StatusAwaitingReview:
				resetKey.SetEnabled(true)
			default:
				resetKey.SetEnabled(false)
			}
		}
		return hintString(m.keys.FocusFull, m.keys.StepsNav, stopKey, resetKey, resumeKey, m.keys.StepsLeave, keyHelp, keyQuit)
	}
	return ""
}

func (m monitorModel) footerView() string {
	status := m.statusLabel()
	hint := m.hintLabel()
	// When a gate is pending but the user has focused a panel, remind them a gate
	// is waiting (it is non-blocking — tab returns to it).
	if m.hasGate() && m.focus != focusGate {
		hint = "tab to gate  •  " + hint
	}
	// The per-run cost/token total lives at the bottom of the Steps panel
	// (listBody's Total row), not the footer.
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

	// The gate is always rendered at a fixed height (Unit 2); mirror resize().
	_, vFrame := panelFrame()
	gateH := m.gateBodyHeight() + vFrame
	panelH := m.height - lipgloss.Height(footer) - gateH
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

	sec := m.securityView()
	return lipgloss.JoinVertical(lipgloss.Left, panels, sec, gate, footer)
}
