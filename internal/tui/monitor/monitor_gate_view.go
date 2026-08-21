package monitor

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// clipReason wraps s to width and truncates to at most maxLines, appending an
// ellipsis to the last kept line when content was dropped. It bounds the
// recovery gate's error message to a fixed number of rows so the gate strip
// never shifts the surrounding panels.
func clipReason(s string, width, maxLines int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if width < 1 {
		width = 1
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(s)
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= maxLines {
		return wrapped
	}
	lines = lines[:maxLines]
	r := []rune(strings.TrimRight(lines[maxLines-1], " "))
	if len(r) > 0 {
		r = r[:len(r)-1]
	}
	lines[maxLines-1] = string(r) + "…"
	return strings.Join(lines, "\n")
}

func (m Model) inputBarView() string {
	if !m.hasGate() {
		label := shared.Theme.Title.Render("Human actions")
		return "  " + label + shared.Theme.Chat.Hint.Render("  ·  no pending actions")
	}

	entry, _ := m.activeEntry()
	presentation := presentationForGate(entry)
	label := shared.Theme.Title.Render(presentation.title)
	subject := shared.Theme.Marker.Render(presentation.subjectLabel + ": " + presentation.subject)
	count := fmt.Sprintf("%d pending", len(m.inputQueue))
	action := "tab to open"
	if m.focus == focusGate {
		action = "esc to close"
	}
	return "  " + label + "  ·  " + subject + "  ·  " + shared.Theme.Marker.Render(count) +
		shared.Theme.Chat.Hint.Render("  ·  "+action)
}

// gateOverlay keeps the input controls out of vertical layout calculations, so
// focusing a gate cannot resize or move either transcript viewport.
func (m Model) gateOverlay() string {
	if !m.hasGate() {
		return ""
	}

	_, vFrame := shared.PanelFrame()
	fixedH := m.gateBodyHeight() + vFrame

	entry, _ := m.activeEntry()
	var b strings.Builder
	presentation := presentationForGate(entry)

	if entry != nil {
		n := len(m.inputQueue)
		header := fmt.Sprintf(
			"[%d / %d]  %s: %s",
			m.activeInputIdx+1,
			n,
			presentation.subjectLabel,
			presentation.subject,
		)
		b.WriteString(shared.Theme.Title.Render(header) + "\n")
		action := "Required: " + presentation.action
		if presentation.contextStep != "" {
			action += "  ·  [ctrl+o] view " + presentation.contextName
		}
		b.WriteString("  " + shared.Theme.Chat.Hint.Render(action) + "\n")
		switch entry.kind {
		case inputKindRequest:
			m.renderGateRequest(&b, entry)
		case inputKindQuestion:
			m.renderGateQuestion(&b, entry)
		case inputKindPrompt:
			m.renderGatePrompt(&b, entry)
		case inputKindReview:
			m.renderGateReview(&b, entry)
		case inputKindRecovery:
			m.renderGateRecovery(&b, entry)
		case inputKindIntegrationConflict:
			m.renderGateIntegration(&b, entry)
		case inputKindFinalMerge:
			m.renderGateFinalMerge(&b, entry)
		case inputKindHelpFinalMerge:
			m.renderGateHelpFinalMerge(&b)
		case inputKindResetConfirm:
			m.renderGateResetConfirm(&b, entry)
		}
	}

	return shared.Panel(presentation.title, b.String(), m.width, fixedH, true)
}

func (m Model) renderGateRequest(b *strings.Builder, entry *pendingInputEntry) {
	b.WriteString(m.promptTextarea.View())
}

func (m Model) renderGateQuestion(b *strings.Builder, entry *pendingInputEntry) {
	b.WriteString(entry.question.View())
}

func (m Model) renderGatePrompt(b *strings.Builder, entry *pendingInputEntry) {
	b.WriteString("  " + shared.Theme.Question.Render(entry.prompt.Label) + "\n\n")
	b.WriteString(m.promptTextarea.View())
}

func (m Model) renderGateReview(b *strings.Builder, entry *pendingInputEntry) {
	if entry.composing {
		b.WriteString(m.promptTextarea.View())
	} else {
		for i, ch := range entry.review.Choices {
			b.WriteString(fmt.Sprintf("    [%d] %s\n", i+1, ch))
		}
		if entry.review.AllowMessage {
			b.WriteString("    [m] message\n")
		}
		b.WriteString("\n    " + shared.Theme.Chat.Hint.Render("[ctrl+o] view diff") + "\n")
	}
}

func (m Model) renderGateRecovery(b *strings.Builder, entry *pendingInputEntry) {
	if entry.composing {
		b.WriteString(m.promptTextarea.View())
	} else {
		// Failure reason, bounded to two rows so the strip height is fixed.
		reason := clipReason(entry.recovery.Err, m.gateInnerWidth()-2, 2)
		reasonLines := strings.Split(reason, "\n")
		for i := 0; i < 2; i++ {
			if i < len(reasonLines) {
				b.WriteString("  " + shared.Theme.Error.Render(reasonLines[i]) + "\n")
			} else {
				b.WriteString("\n") // pad to keep height stable
			}
		}
		b.WriteString("    [r] retry\n")
		if entry.recovery.CanResume {
			b.WriteString("    [g] retry with guidance\n")
		} else {
			b.WriteString("\n") // keep height stable when resume is unavailable
		}
		b.WriteString("    [s] skip\n")
		b.WriteString("    [a] abort run\n")
	}
}

func (m Model) renderGateIntegration(b *strings.Builder, entry *pendingInputEntry) {
	// Name the conflicted paths (bounded to two rows for a fixed strip
	// height), then the resolve/abort affordances.
	paths := strings.Join(entry.integration.Paths, ", ")
	if paths == "" {
		paths = "(conflicted files unknown)"
	}
	pathLines := strings.Split(clipReason("conflict in "+paths, m.gateInnerWidth()-2, 2), "\n")
	for i := 0; i < 2; i++ {
		if i < len(pathLines) {
			b.WriteString("  " + shared.Theme.Error.Render(pathLines[i]) + "\n")
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString("    [r] resolve (finish merge from run worktree)\n")
	b.WriteString("    [a] abort run\n")
}

func (m Model) renderGateFinalMerge(b *strings.Builder, entry *pendingInputEntry) {
	fm := entry.finalMerge
	base := fm.Base
	if base == "" {
		base = "working branch"
	}
	line := clipReason("merge "+fm.RunBranch+" → "+base, m.gateInnerWidth()-2, 1)
	b.WriteString("  " + shared.Theme.Marker.Render(line) + "\n\n")
	b.WriteString("    [y] merge onto " + base + "\n")
	b.WriteString("    [d] discard (leave run branch)\n")
}

// renderGateHelpFinalMerge renders the confirmation gate entry for the
// help-agent-triggered final-merge approval (tool resolve_review, step_id="final_merge").
func (m Model) renderGateHelpFinalMerge(b *strings.Builder) {
	b.WriteString("  " + shared.Theme.Marker.Render("Help agent is requesting final merge approval") + "\n\n")
	b.WriteString("    [y] approve merge\n")
	b.WriteString("    [d] discard (leave run branch)\n")
}

func (m Model) renderGateResetConfirm(b *strings.Builder, entry *pendingInputEntry) {
	rc := entry.resetConfirm
	count := len(rc.closure) - 1 // downstream steps (excluding the target itself)
	ids := strings.Join(rc.closure, ", ")
	summary := fmt.Sprintf("Reset to %q will re-run %d step(s): %s", rc.stepID, len(rc.closure), ids)
	line := clipReason(summary, m.gateInnerWidth()-2, 2)
	lineRows := strings.Split(line, "\n")
	for i := 0; i < 2; i++ {
		if i < len(lineRows) {
			b.WriteString("  " + shared.Theme.Error.Render(lineRows[i]) + "\n")
		} else {
			b.WriteString("\n")
		}
	}
	_ = count // count is embedded in the ids string above
	b.WriteString("    [y] confirm reset\n")
	b.WriteString("    [n] cancel  (default)\n")
}
