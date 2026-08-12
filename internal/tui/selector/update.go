package selector

import (
	"charm.land/bubbles/v2/list"
	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m = m.resize()
		return m, nil

	case workflowsLoadedMsg:
		m.loading = false
		m.err = msg.err
		return m, m.list.SetItems(msg.items)

	case tea.KeyPressMsg:
		// While the filter input is open, let the list consume Enter (it applies
		// the filter) rather than treating it as a selection.
		if keybind.Matches(msg, m.keys.Open) && m.list.FilterState() != list.Filtering {
			if item, ok := m.list.SelectedItem().(workflowItem); ok {
				return m, func() tea.Msg { return ShowDetailMsg{Path: item.path} }
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// resize fits the list to the panel's inner area, leaving room for the footer
// line rendered below the box.
func (m Model) resize() Model {
	hFrame, vFrame := shared.PanelFrame()
	footerH := lipgloss.Height(m.footerView())
	w := m.width - hFrame
	h := m.height - vFrame - footerH
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	m.list.SetSize(w, h)
	return m
}
