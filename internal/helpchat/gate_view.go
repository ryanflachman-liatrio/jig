package helpchat

import (
	"encoding/json"
	"fmt"
	"strings"

	"jig/internal/tui/shared"
)

// renderGate returns the bottom-of-modal string for the active pendingGateEntry.
// Called from View only when pendingGate != nil.
func (m Model) renderGate(width int) string {
	g := m.pendingGate
	switch g.kind {
	case gateKindPerm:
		return m.renderPermGate(g, width)
	case gateKindQuestion:
		return m.renderQuestionGate(g, width)
	}
	return ""
}

func (m Model) renderPermGate(g *pendingGateEntry, width int) string {
	var b strings.Builder
	b.WriteString(shared.Theme.Question.Render("Tool permission request") + "\n")
	b.WriteString(shared.Theme.Running.Render(g.toolName) + "\n")
	if len(g.input) > 0 {
		raw, err := json.MarshalIndent(g.input, "  ", "  ")
		if err == nil {
			b.WriteString(shared.Theme.Chat.Hint.Render(string(raw)) + "\n")
		}
	} else {
		b.WriteString(shared.Theme.Chat.Hint.Render("(no parameters)") + "\n")
	}
	return b.String()
}

func (m Model) renderQuestionGate(g *pendingGateEntry, width int) string {
	var b strings.Builder
	b.WriteString(shared.Theme.Question.Render("Agent question") + "\n")
	b.WriteString(g.question + "\n\n")
	if len(g.options) > 0 {
		for i, opt := range g.options {
			if i == g.selected {
				b.WriteString(shared.Theme.SelectedLine.Render("▶ "+opt) + "\n")
			} else {
				b.WriteString(shared.Theme.Chat.Hint.Render("  "+opt) + "\n")
			}
		}
	} else {
		b.WriteString(shared.Theme.Marker.Render("> ") + g.textBuf + "█\n")
	}
	return b.String()
}

// unused width kept for future layout math (e.g. wrapping long option text).
var _ = fmt.Sprintf
