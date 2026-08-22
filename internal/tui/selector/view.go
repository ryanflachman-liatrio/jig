package selector

import (
	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

func (m Model) View() string {
	switch {
	case m.loading:
		return "\n  Scanning " + workflowsDir + "…\n"
	case m.err != nil:
		return "\n  " + shared.Theme.Error.Render("Failed to scan "+workflowsDir+": "+m.err.Error()) + "\n"
	case len(m.list.Items()) == 0:
		return "\n  " + shared.Theme.Title.Render("No workflows found") + "\n\n" +
			shared.Theme.Question.Render("  Add a <name>.toml with a [workflow] table under "+workflowsDir+"/.") +
			"\n\n" + shared.Theme.Footer.Render("  "+shared.HintString(shared.KeyQuit)) + "\n"
	}
	footer := m.footerView()
	body := shared.Panel("Workflows", m.list.View(), m.width, m.height-lipgloss.Height(footer), true)
	return body + "\n" + footer
}

// HelpSections satisfies the root's helpProvider bridge: the selector's list
// navigation, filter, and open action, plus the global chord.
func (m Model) HelpSections() []shared.HelpSection {
	return []shared.HelpSection{
		{Title: "Workflows", Bindings: []keybind.Binding{m.keys.Nav, m.keys.Filter, m.keys.Open, m.keys.Apply, m.keys.Clear}},
		{Title: "Global", Bindings: shared.GlobalHelpBindings(m.CapturesText())},
	}
}

// capturesText reports whether the list filter is capturing text, in which case
// "?" is a literal character rather than the help chord.
func (m Model) CapturesText() bool {
	return m.list.FilterState() == list.Filtering
}

// footerView renders the single hint line below the panel; it branches on the
// filter state, mirroring lazygit's global keybind bar.
func (m Model) footerView() string {
	if m.list.FilterState() == list.Filtering {
		return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.Apply, m.keys.Clear, shared.KeyQuit))
	}
	return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.Nav, m.keys.Filter, m.keys.Open, shared.KeyHelp, shared.KeyQuit))
}
