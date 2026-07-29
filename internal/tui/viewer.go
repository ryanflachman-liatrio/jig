package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// renderContent re-renders m.response as markdown through the current
// renderer and pushes it into the viewport. glamour bakes its word-wrap
// width in at construction time, so this must be re-run whenever the
// renderer is rebuilt (e.g. on resize), not just when the response arrives.
func (m *model) renderContent() {
	if m.renderer == nil || m.response == "" {
		return
	}
	rendered, err := m.renderer.Render(m.response)
	if err != nil {
		m.viewport.SetContent(m.response)
		return
	}
	m.viewport.SetContent(rendered)
}

// handleResize rebuilds the viewport and glamour renderer to fit the new
// terminal dimensions, then re-renders any existing content into them.
func (m *model) handleResize(msg tea.WindowSizeMsg) {
	m.width, m.height = msg.Width, msg.Height

	headerHeight := lipgloss.Height(m.headerView())
	footerHeight := lipgloss.Height(m.footerView())
	// +2 for the blank lines around the body, plus the viewport border's own frame size.
	verticalMargins := headerHeight + footerHeight + 2 + viewportStyle.GetVerticalFrameSize()

	viewportHeight := m.height - verticalMargins
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	if !m.ready {
		m.viewport = viewport.New(m.width, viewportHeight)
		m.viewport.Style = viewportStyle
		m.ready = true
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = viewportHeight
	}

	wordWrap := m.width - viewportStyle.GetHorizontalFrameSize()
	if wordWrap < 1 {
		wordWrap = 1
	}
	m.renderer, _ = glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(wordWrap),
	)

	m.renderContent()
}
