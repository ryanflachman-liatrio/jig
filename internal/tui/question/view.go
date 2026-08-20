package question

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"jig/internal/interaction"
	"jig/internal/tui/shared"
)

func (m Model) View() string {
	var lines []string
	switch m.phase {
	case phaseReview:
		lines = m.reviewLines()
	default:
		lines = m.fieldLines()
	}
	lines = append(lines, shared.Theme.Chat.Hint.Render(m.Hint()))
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i, line := range lines {
		lines[i] = clip(line, m.width)
	}
	return strings.Join(lines, "\n")
}

func (m Model) fieldLines() []string {
	field := m.currentField()
	lines := make([]string, 0, m.height)
	if len(m.request.Fields) > 1 {
		lines = append(lines, shared.Theme.Chat.Hint.Render(
			fmt.Sprintf("Question %d of %d", m.fieldIdx+1, len(m.request.Fields)),
		))
	}
	if field.Header != "" {
		lines = append(lines, shared.Theme.Question.Render("["+field.Header+"]"))
	}
	if field.Prompt != "" {
		lines = append(lines, field.Prompt)
	}
	if m.CapturesText() {
		lines = append(lines, "", m.textarea.View())
		return lines
	}

	count := len(field.Options)
	if field.AllowCustom {
		count++
	}
	visible := m.optionRows()
	end := m.scrollOffset + visible
	if end > count {
		end = count
	}
	if m.scrollOffset > 0 {
		lines = append(lines, shared.Theme.Chat.Hint.Render("  ▲ more"))
	}
	for i := m.scrollOffset; i < end; i++ {
		label := "Other…"
		description := ""
		selected := false
		if i < len(field.Options) {
			option := field.Options[i]
			label = option.Label
			description = option.Description
			selected = m.selected[option.Value]
		}
		marker := "  "
		if field.Kind == interaction.FieldMultiSelect && i < len(field.Options) {
			marker = "[ ]"
			if selected {
				marker = "[x]"
			}
		}
		line := fmt.Sprintf("%s %s", marker, label)
		if description != "" {
			line += " — " + description
		}
		if i == m.optionCursor {
			lines = append(lines, shared.Theme.SelectedLine.Render("▶ "+line))
		} else {
			lines = append(lines, "  "+line)
		}
	}
	if end < count {
		lines = append(lines, shared.Theme.Chat.Hint.Render("  ▼ more"))
	}
	return lines
}

func (m Model) reviewLines() []string {
	lines := []string{shared.Theme.Question.Render("Review answers")}
	for i, field := range m.request.Fields {
		line := fmt.Sprintf("%s: %s", field.Prompt, m.answerText(field))
		if i == m.reviewCursor {
			lines = append(lines, shared.Theme.SelectedLine.Render("▶ "+line))
		} else {
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

func clip(s string, width int) string {
	if width < 2 {
		return s
	}
	return ansi.Truncate(s, width, "…")
}
