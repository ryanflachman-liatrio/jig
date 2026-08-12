package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// renderActiveTurn rebuilds the viewport's content from m.turns[m.activeTurn].
//
// While that turn is the one actively streaming, "stick to bottom"
// scrolling is preserved: if the viewport was already scrolled to the
// bottom before this call, it stays pinned to the bottom after new content
// is appended. Otherwise, the turn's remembered scrollOffset is restored,
// so navigating away and back leaves the reader where they left off.
//
// A completed turn's answer is rendered as markdown once and cached in
// t.rendered. The actively-streaming reply is always appended as raw text,
// never through glamour - glamour re-parses whole documents, so
// re-rendering a growing markdown string on every token is wasteful and
// flickers as unclosed code fences and other partial syntax resolve.
func (m *chatModel) renderActiveTurn() {
	if len(m.turns) == 0 {
		m.viewport.SetContent("Ask your first question to get started.")
		return
	}

	t := &m.turns[m.activeTurn]
	isActiveStream := m.streaming && m.activeTurn == m.streamingTurn
	wasAtBottom := !m.ready || m.viewport.AtBottom()

	var b strings.Builder
	if t.question != "" {
		b.WriteString(shared.Theme.UserPrompt.Render("You") + "\n" + t.question)
		b.WriteString("\n\n")
	}

	switch {
	case isActiveStream:
		b.WriteString(t.answer)
	case t.isError:
		b.WriteString(shared.Theme.Error.Render("Error: " + t.answer))
	default:
		if t.rendered == "" && m.renderer != nil {
			if out, err := m.renderer.Render(t.answer); err == nil {
				t.rendered = out
			} else {
				t.rendered = t.answer
			}
		}
		if t.rendered != "" {
			b.WriteString(t.rendered)
		} else {
			b.WriteString(t.answer)
		}
	}

	m.viewport.SetContent(b.String())
	if isActiveStream && wasAtBottom {
		m.viewport.GotoBottom()
	} else if !isActiveStream {
		m.viewport.SetYOffset(t.scrollOffset)
	}
}

// handleResize rebuilds the viewport, textarea, and glamour renderer to fit
// the new terminal dimensions, then re-renders the active turn into them. Both
// the Conversation and Message panels now own their frames, so inner sizes are
// derived from panelFrame() (no magic numbers) and the panel borders — not the
// viewport/textarea's own styles — carry the focus color.
func (m *chatModel) handleResize(msg tea.WindowSizeMsg) {
	m.width, m.height = msg.Width, msg.Height

	hFrame, vFrame := shared.PanelFrame()

	footerHeight := lipgloss.Height(m.footerView())
	fatalHeight := lipgloss.Height(m.fatalLine())
	// The Message panel is the textarea's height plus the panel frame; the
	// Conversation panel takes the remaining rows (also minus its own frame).
	messagePanelH := m.textarea.Height() + vFrame
	viewportHeight := m.height - footerHeight - fatalHeight - messagePanelH - vFrame
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	innerW := m.width - hFrame
	if innerW < 1 {
		innerW = 1
	}

	if !m.ready {
		m.viewport = viewport.New(viewport.WithWidth(innerW), viewport.WithHeight(viewportHeight))
		// No own border: the Conversation panel draws the frame and its color
		// reflects focus. A bordered viewport here would double-box.
		m.viewport.Style = lipgloss.NewStyle()
		// Content is word-wrapped to the panel width; horizontal scrolling only
		// ever cuts left characters off rendered lines. Disable it.
		m.viewport.KeyMap.Left.Unbind()
		m.viewport.KeyMap.Right.Unbind()
		m.ready = true
	} else {
		m.viewport.SetWidth(innerW)
		m.viewport.SetHeight(viewportHeight)
	}

	m.textarea.SetWidth(innerW)

	wordWrap := innerW
	if wordWrap < 1 {
		wordWrap = 1
	}
	// A static themed style, not glamour.WithAutoStyle(): AutoStyle performs a
	// live OSC 11 terminal query on every call, which races with Bubble Tea's
	// stdin reader and leaks the terminal's response into the focused textarea
	// as garbled keystrokes. The Charmtone theme is dark-only, so
	// m.darkBackground no longer selects the style.
	m.renderer, _ = glamour.NewTermRenderer(
		glamour.WithStyles(shared.Theme.Markdown),
		glamour.WithWordWrap(wordWrap),
	)

	// glamour bakes its word-wrap width in at construction time, so every
	// cached render must be invalidated here, not just the next one.
	for i := range m.turns {
		m.turns[i].rendered = ""
	}
	m.renderActiveTurn()
}
