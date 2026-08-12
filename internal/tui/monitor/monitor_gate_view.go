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

// gateStrip renders the human-in-the-loop gate as a full-width titled panel
// beneath the two main panels, above the footer. It always renders — even when
// the queue is empty — so the Steps and Transcript panels never resize on input
// arrival or departure (Spec Unit 2). The panel height is always
// gateBodyHeight()+vFrame, and the border is blurred when focus != focusGate.
func (m Model) gateStrip() string {
	_, vFrame := shared.PanelFrame()
	fixedH := m.gateBodyHeight() + vFrame

	// Empty queue: render a placeholder panel with a blurred border.
	if !m.hasGate() {
		placeholder := "\n  " + shared.Theme.Chat.Hint.Render("No pending agent inputs")
		return shared.Panel("Agent input", placeholder, m.width, fixedH, false)
	}

	entry, _ := m.activeEntry()
	var b strings.Builder

	if entry != nil {
		// [N / M]  step-id  (kind) header above every active entry body (task 3.4).
		n := len(m.inputQueue)
		header := fmt.Sprintf("[%d / %d]  %s  (%s)", m.activeInputIdx+1, n, entry.stepID, kindName(entry.kind))
		b.WriteString(shared.Theme.Title.Render(header) + "\n")
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
		case inputKindResetConfirm:
			m.renderGateResetConfirm(&b, entry)
		}
	}

	return shared.Panel("Agent input", b.String(), m.width, fixedH, m.focus == focusGate)
}

func (m Model) renderGateRequest(b *strings.Builder, entry *pendingInputEntry) {
	b.WriteString(m.promptTextarea.View())
}

func (m Model) renderGateQuestion(b *strings.Builder, entry *pendingInputEntry) {
	if entry.questionIdx < len(entry.question.Questions) {
		q := entry.question.Questions[entry.questionIdx]

		// Compute rows consumed by fixed chrome so we know how many option
		// rows fit in the fixed body height. gateHeaderRows is already consumed
		// by the [N/M] header written above; count the remaining chrome here.
		overhead := gateHeaderRows // [N/M] header already written
		if q.Header != "" {
			overhead++
		}
		overhead += 2 // question text + blank line
		if q.MultiSelect {
			overhead++ // "enter to confirm" hint
		}
		if len(entry.question.Questions) > 1 {
			overhead++ // "question N of M" hint
		}

		budget := m.gateBodyHeight() - overhead
		if budget < 1 {
			budget = 1
		}
		scrollable := len(q.Options) > budget
		visibleCount := budget
		if scrollable {
			// Reserve one row each for ▲ / ▼ indicators (always shown when
			// the list overflows to prevent height jitter as offset changes).
			visibleCount = budget - 2
			if visibleCount < 1 {
				visibleCount = 1
			}
		}

		// Clamp scrollOffset so the last option is always reachable.
		maxOffset := len(q.Options) - visibleCount
		if maxOffset < 0 {
			maxOffset = 0
		}
		scrollOffset := entry.scrollOffset
		if scrollOffset > maxOffset {
			scrollOffset = maxOffset
		}

		if q.Header != "" {
			b.WriteString("  " + shared.Theme.Question.Render("["+q.Header+"]") + "\n")
		}
		b.WriteString("  " + q.Question + "\n\n")

		if scrollable {
			if scrollOffset > 0 {
				b.WriteString("    " + shared.Theme.Chat.Hint.Render("▲ more") + "\n")
			} else {
				b.WriteString("\n") // placeholder keeps height stable
			}
		}

		end := scrollOffset + visibleCount
		if end > len(q.Options) {
			end = len(q.Options)
		}
		for i := scrollOffset; i < end; i++ {
			opt := q.Options[i]
			// Label uses absolute [N] so blind-typing selects correctly even
			// when the visible window is scrolled (Unit 6 FR).
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

		if scrollable {
			if end < len(q.Options) {
				b.WriteString("    " + shared.Theme.Chat.Hint.Render("▼ more") + "\n")
			} else {
				b.WriteString("\n") // placeholder keeps height stable
			}
		}

		if q.MultiSelect {
			b.WriteString("\n    " + shared.Theme.Chat.Hint.Render("enter to confirm selection") + "\n")
		}
		if len(entry.question.Questions) > 1 {
			b.WriteString("    " + shared.Theme.Chat.Hint.Render(
				fmt.Sprintf("question %d of %d", entry.questionIdx+1, len(entry.question.Questions))) + "\n")
		}
	}
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
		b.WriteString("\n    " + shared.Theme.Chat.Hint.Render("diff shown in Transcript — select this step") + "\n")
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
