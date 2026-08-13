package chat

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// conversationTitle is the output panel's title. It folds the old header/turn
// chrome into the border edge: "connecting…" until the client connects (then it
// drops — never a persistent "Connected"), and "· Turn N of M" once more than
// one turn exists so the reader knows which of several answers is shown.
func (m chatModel) conversationTitle() string {
	title := "Conversation"
	if !m.connected && !m.fatal {
		return title + " · connecting…"
	}
	if len(m.turns) > 1 {
		return fmt.Sprintf("%s · Turn %d of %d", title, m.activeTurn+1, len(m.turns))
	}
	return title
}

// messageTitle is the input panel's title. The streaming state lives here as
// text ("· responding…") rather than a spinner glyph in the border edge, which
// would jitter the top line every tick.
func (m chatModel) messageTitle() string {
	if m.streaming {
		return "Message · responding…"
	}
	return "Message"
}

// fatalLine renders a fatal connection/stream error as its own full-width line
// beneath the panels — the chat's analogue of the monitor's gate strip — so the
// message is never truncated inside a panel title. Returns "" when no error.
func (m chatModel) fatalLine() string {
	if !m.fatal || m.fatalErr == nil {
		return ""
	}
	line := shared.Theme.Error.Render("⚠ " + m.fatalErr.Error())
	if m.width > 0 {
		line = shared.Theme.Error.MaxWidth(m.width).Render("⚠ " + m.fatalErr.Error())
	}
	return line
}

func (m chatModel) footerView() string {
	if m.fatal {
		return shared.Theme.Footer.Render("  " + shared.HintString(shared.KeyQuit))
	}
	if m.ready && len(m.turns) > 0 {
		hint := shared.HintString(m.keys.Send, m.keys.Newline, m.keys.SwitchFocus, shared.KeyQuit)
		return shared.Theme.Footer.Render(fmt.Sprintf("  %s (%.0f%%)", hint, m.viewport.ScrollPercent()*100))
	}
	return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.Send, m.keys.Newline, shared.KeyQuit))
}

// View stacks two titled panels: a "Conversation" output panel wrapping the
// viewport and a "Message" input panel wrapping the textarea. The focused
// panel's border is drawn primary (via the focusInput/focusOutput toggle). A
// fatal error renders as its own full-width line beneath the panels (never
// inside a truncatable title). chatModel is a standalone root model, so View
// returns a tea.View (unlike the sub-models).
func (m chatModel) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing...\n")
	}

	_, vFrame := shared.PanelFrame()
	width := m.width
	if width < 1 {
		width = 1
	}

	// Panel heights are the viewport/textarea content heights plus the frame the
	// border+title consume; handleResize sized the inner components to match.
	convH := m.viewport.Height() + vFrame
	msgH := m.textarea.Height() + vFrame

	conversation := shared.Panel(m.conversationTitle(), m.viewport.View(), width, convH, m.focus == focusOutput)
	message := shared.Panel(m.messageTitle(), m.textarea.View(), width, msgH, m.focus == focusInput)

	parts := []string{conversation, message}
	if line := m.fatalLine(); line != "" {
		parts = append(parts, line)
	}
	parts = append(parts, m.footerView())

	return tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
}
