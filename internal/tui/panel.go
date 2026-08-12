package tui

import (
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

func panel(title, body string, width, height int, focused bool) string {
	return shared.Panel(title, body, width, height, focused)
}

func panelTopEdge(title string, width int, border lipgloss.Style) string {
	return shared.PanelTopEdge(title, width, border)
}

func truncateTitle(s string, max int) string { return shared.TruncateTitle(s, max) }

func panelFrame() (int, int) { return shared.PanelFrame() }
