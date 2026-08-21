package monitor

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/tui/shared"
)

const securityMaxHeight = 5

// helpOverlay composites the help chat modal over the base layout using the
// same Compositor technique as RenderHelpOverlay. The modal takes 60% of the
// width and 80% of the height, centered.
func (m Model) helpOverlay(base string) string {
	boxW := m.width * 60 / 100
	boxH := m.height * 80 / 100
	if boxW < 40 {
		boxW = 40
	}
	if boxH < 10 {
		boxH = 10
	}
	box := m.helpModel.View(boxW, boxH, false)
	x := (m.width - lipgloss.Width(box)) / 2
	y := (m.height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(m.width, m.height).Compose(comp).Render()
}

func (m Model) securityViewHeight(available int) int {
	if len(m.secFindings) == 0 || available < 1 || m.width < 1 {
		return 0
	}
	return min(1+len(m.secFindings), securityMaxHeight, available)
}

// securityView renders a bounded summary rather than letting an arbitrarily
// large findings file displace the gate and footer.
func (m Model) securityView(height int) string {
	if len(m.secFindings) == 0 || height < 1 || m.width < 1 {
		return ""
	}

	header := fmt.Sprintf("Security findings (%d)", len(m.secFindings))
	lines := []string{shared.Theme.Security.Header.MaxWidth(m.width).Render(header)}

	visible := min(len(m.secFindings), height-1)
	overflow := len(m.secFindings) > visible
	if overflow && height > 1 {
		visible = max(height-2, 0)
	}
	for _, f := range m.secFindings[:visible] {
		sev := strings.ToUpper(string(f.Severity))
		label := "[" + sev + "] " + f.Monitor + ": "
		detail := f.Detail
		if detail == "" {
			detail = string(f.Action)
		}
		var row string
		switch f.Severity {
		case sentinel.SeverityCritical:
			row = shared.Theme.Security.CriticalRow.MaxWidth(m.width).Render(label + detail)
		case sentinel.SeverityHigh:
			row = shared.Theme.Security.HighRow.MaxWidth(m.width).Render(label + detail)
		case sentinel.SeverityMedium:
			row = shared.Theme.Security.MediumRow.MaxWidth(m.width).Render(label + detail)
		default:
			row = shared.Theme.Security.LowRow.MaxWidth(m.width).Render(label + detail)
		}
		lines = append(lines, row)
	}
	if overflow && height > 1 {
		remaining := len(m.secFindings) - visible
		more := fmt.Sprintf("… %d more finding", remaining)
		if remaining != 1 {
			more += "s"
		}
		lines = append(lines, shared.Theme.Security.LowRow.MaxWidth(m.width).Render(more))
	}
	return strings.Join(lines, "\n")
}

func fitBlock(s string, width, height int) string {
	if s == "" || width < 1 || height < 1 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	return strings.Join(lines, "\n")
}

func joinVertical(parts ...string) string {
	visible := parts[:0]
	for _, part := range parts {
		if part != "" {
			visible = append(visible, part)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, visible...)
}

// statusLabel computes the status text shown in the footer.
func (m Model) statusLabel() string {
	if m.done {
		if m.failed {
			return shared.Theme.Error.Render("failed")
		}
		return shared.Theme.Valid.Render("done")
	}
	entry, ok := m.activeEntry()
	if !ok {
		return shared.Theme.Running.Render("running")
	}
	n := len(m.inputQueue)
	queueSuffix := ""
	if n > 1 {
		queueSuffix = fmt.Sprintf(" (%d pending)", n)
	}
	switch entry.kind {
	case inputKindRequest:
		return shared.Theme.Marker.Render("awaiting agent input" + queueSuffix)
	case inputKindQuestion:
		return shared.Theme.Marker.Render("awaiting answer" + queueSuffix)
	case inputKindPrompt:
		return shared.Theme.Marker.Render("awaiting user input" + queueSuffix)
	case inputKindReview:
		if entry.composing {
			return shared.Theme.Marker.Render("composing message")
		}
		return shared.Theme.Marker.Render("awaiting review")
	case inputKindRecovery:
		if entry.composing {
			return shared.Theme.Marker.Render("composing guidance")
		}
		return shared.Theme.Error.Render("step failed — recovery" + queueSuffix)
	case inputKindIntegrationConflict:
		return shared.Theme.Error.Render("integration conflict" + queueSuffix)
	case inputKindFinalMerge:
		return shared.Theme.Marker.Render("awaiting final merge" + queueSuffix)
	}
	return shared.Theme.Running.Render("running")
}

// hintLabel computes the keyboard hint shown in the footer.
func (m Model) hintLabel() string {
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
			return shared.HintString(m.keys.Submit, m.keys.Newline, entryNav, m.keys.GateBlur)
		case inputKindQuestion:
			hint := entry.question.Hint()
			if len(m.inputQueue) > 1 {
				hint += " · tab entry"
			}
			return hint
		case inputKindReview:
			if entry.composing {
				return shared.HintString(m.keys.Submit, m.keys.Newline, m.keys.GateBlur)
			} else if entry.review.AllowMessage {
				return shared.HintString(m.keys.Verdict, m.keys.Message, entryNav, m.keys.GateBlur)
			}
			return shared.HintString(m.keys.Verdict, entryNav, m.keys.GateBlur)
		case inputKindPrompt:
			return shared.HintString(m.keys.Submit, m.keys.Newline, entryNav, m.keys.GateBlur)
		case inputKindRecovery:
			if entry.composing {
				return shared.HintString(m.keys.Submit, m.keys.Newline, m.keys.GateBlur)
			} else if entry.recovery.CanResume {
				return shared.HintString(m.keys.RecoverRetry, m.keys.RecoverGuide, m.keys.RecoverSkip, m.keys.RecoverAbort, entryNav, m.keys.GateBlur)
			}
			return shared.HintString(m.keys.RecoverRetry, m.keys.RecoverSkip, m.keys.RecoverAbort, entryNav, m.keys.GateBlur)
		case inputKindIntegrationConflict:
			return shared.HintString(m.keys.IntegrationResolve, m.keys.RecoverAbort, entryNav, m.keys.GateBlur)
		case inputKindFinalMerge, inputKindHelpFinalMerge:
			return shared.HintString(m.keys.FinalMergeApprove, m.keys.FinalMergeDiscard, entryNav, m.keys.GateBlur)
		case inputKindResetConfirm:
			return shared.HintString(m.keys.GateBlur) // y/n shown inline in the gate strip
		}
	case m.focus == focusTranscript:
		return shared.HintString(m.keys.FocusFull, m.keys.Scroll, m.keys.GotoTop, m.keys.GotoBottom, m.keys.BlockNav, m.keys.Toggle, m.keys.ExpandAll, m.keys.ToggleHelp)
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
		return shared.HintString(m.keys.FocusFull, m.keys.StepsNav, stopKey, resetKey, resumeKey, m.keys.ToggleHelp, m.keys.StepsLeave, shared.KeyHelp, shared.KeyQuit)
	}
	return ""
}

func (m Model) footerView() string {
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
	f := shared.Theme.Footer
	if m.width > 0 {
		f = f.MaxWidth(m.width)
	}
	return f.Render("  " + status + "  ·  " + hint)
}

// View lays the monitor out as two side-by-side titled panels (Steps + the
// selected step's transcript) with the gate strip and footer beneath. Below the
// narrow threshold only the focused panel renders full-width (Resolved
// Decision 14). Only the focused region's border is drawn primary.
func (m Model) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}
	if m.width < 1 || m.height < 1 {
		return ""
	}

	layout := m.verticalLayout()
	footer := fitBlock(m.footerView(), m.width, layout.footerH)
	gate := fitBlock(m.gateStrip(), m.width, layout.gateH)

	rightTitle := m.chatStep
	if rightTitle == "" {
		rightTitle = "Transcript"
	}

	var panels string
	if layout.panelH == 0 {
		panels = ""
	} else if m.narrow {
		// Single-panel fallback: render only the focused panel full-width.
		if m.focus == focusTranscript {
			panels = shared.Panel(rightTitle, m.chatVP.View(), m.width, layout.panelH, true)
		} else {
			// Steps or Gate focus shows the Steps panel (the gate has its own strip).
			panels = shared.Panel("Steps", m.vp.View(), m.width, layout.panelH, m.focus == focusSteps)
		}
	} else {
		stepsW, transcriptW, _ := panelSplit(m.width)
		left := shared.Panel("Steps", m.vp.View(), stepsW, layout.panelH, m.focus == focusSteps)
		right := shared.Panel(rightTitle, m.chatVP.View(), transcriptW, layout.panelH, m.focus == focusTranscript)
		panels = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	panels = fitBlock(panels, m.width, layout.panelH)

	sec := fitBlock(m.securityView(layout.securityH), m.width, layout.securityH)
	base := fitBlock(joinVertical(panels, sec, gate, footer), m.width, m.height)
	if m.helpOpen {
		return m.helpOverlay(base)
	}
	return base
}
