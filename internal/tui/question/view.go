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
	switch {
	case m.phase == phaseReview:
		lines = m.reviewLines()
	case m.stacked:
		lines = m.stackedLines()
	default:
		lines = m.fieldLines()
	}
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
		lines = append(lines, shared.Theme.Chat.Hint.Render("  "+shared.ArrowUpGlyph+" more"))
	}
	for i := m.scrollOffset; i < end; i++ {
		label := "Other" + shared.EllipsisGlyph
		description := ""
		selected := false
		if i < len(field.Options) {
			option := field.Options[i]
			label = option.Label
			description = option.Description
			selected = m.selected[option.Value]
		}
		marker := "   "
		switch {
		case field.Kind == interaction.FieldMultiSelect && i < len(field.Options):
			marker = "[ ]"
			if selected {
				marker = "[x]"
			}
		case field.Kind == interaction.FieldSingleSelect && i < len(field.Options):
			marker = "( )"
			if i == m.optionCursor {
				marker = "(*)"
			}
		}
		line := fmt.Sprintf("%s %s", marker, label)
		if description != "" {
			line += " — " + description
		}
		if i == m.optionCursor {
			lines = append(lines, shared.Theme.SelectedLine.Render(shared.SelectionMarker+" "+line))
		} else {
			lines = append(lines, "  "+line)
		}
	}
	if end < count {
		lines = append(lines, shared.Theme.Chat.Hint.Render("  "+shared.ArrowDownGlyph+" more"))
	}
	return lines
}

// stackedLines renders every field of a stacked-eligible request together as
// one compact form (Option B), instead of paging through fields one at a
// time. Only the focused field's cursor row is highlighted; tab/shift+tab
// move focus between fields (updateStacked).
func (m Model) stackedLines() []string {
	header := fmt.Sprintf("Answer all %d", len(m.request.Fields))
	if stackedHasCustom(m.request) {
		header += "  ·  o to type your own answer"
	}
	lines := []string{shared.Theme.Chat.Hint.Render(header)}
	for i, field := range m.request.Fields {
		focused := i == m.focusFieldIdx
		promptPrefix := "  "
		promptStyle := shared.Theme.Question
		if focused {
			promptPrefix = shared.SelectionMarker + " "
			promptStyle = shared.Theme.SelectedLine
		}
		lines = append(lines, "", promptPrefix+promptStyle.Render(field.Prompt))

		if custom, ok := m.answers[field.ID]; ok && custom.Custom != "" {
			line := "(*) " + custom.Custom + " (typed)"
			if focused {
				lines = append(lines, "  "+shared.Theme.SelectedLine.Render(line))
			} else {
				lines = append(lines, "  "+line)
			}
			continue
		}

		cursor := m.stackedCursor[field.ID]
		for oi, option := range field.Options {
			marker := "( )"
			if field.Kind == interaction.FieldMultiSelect {
				marker = "[ ]"
				if m.stackedSelected[field.ID][option.Value] {
					marker = "[x]"
				}
			} else if oi == cursor {
				marker = "(*)"
			}
			line := fmt.Sprintf("%s %s", marker, option.Label)
			if focused && oi == cursor {
				lines = append(lines, "  "+shared.Theme.SelectedLine.Render(line))
			} else {
				lines = append(lines, "  "+line)
			}
		}
	}
	return lines
}

func (m Model) reviewLines() []string {
	lines := []string{shared.Theme.Question.Render("Review answers")}
	for i, field := range m.request.Fields {
		line := fmt.Sprintf("%s: %s", field.Prompt, m.answerText(field))
		if i == m.reviewCursor {
			lines = append(lines, shared.Theme.SelectedLine.Render(shared.SelectionMarker+" "+line))
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
	return ansi.Truncate(s, width, shared.EllipsisGlyph)
}
