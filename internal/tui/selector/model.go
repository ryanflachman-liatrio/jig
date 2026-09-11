package selector

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// workflowsDir is the directory tree jig scans for workflow files on startup.
// Keeping workflows under a well-known path (rather than anywhere in the repo)
// makes the picker fast and predictable, and mirrors the .jig/ run-directory
// convention.
const workflowsDir = ".agents/jig"

// Charmtone tokens for the list delegate — the only place in the selector that
// directly references raw palette colors rather than theme styles.
const (
	hexCharple = "#6B50FF" // primary   (violet)
	hexDolly   = "#FF60FF" // secondary (magenta)
	hexSash    = "#ECEBF0" // fgBase
	hexSquid   = "#858392" // fgMuted
	hexOyster  = "#605F6B" // fgDim
)

// Model is the startup screen: a filterable list of the workflows found under
// workflowsDir.
type Model struct {
	list    list.Model
	keys    selectorKeys
	loading bool
	err     error
	width   int
	height  int

	// itemHeight/itemSpacing mirror the delegate's own row geometry (set once
	// from the delegate at construction) so ItemAt's row math can never drift
	// from what the list actually renders.
	itemHeight  int
	itemSpacing int
}

func New() Model {
	delegate := list.NewDefaultDelegate()
	// Repaint the stock delegate in Charmtone: the selected row gets a charple
	// left bar + brand-colored title, unselected rows the muted foreground ladder.
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color(hexCharple)).
		BorderForeground(lipgloss.Color(hexCharple))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color(hexDolly)).
		BorderForeground(lipgloss.Color(hexCharple))
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.Foreground(lipgloss.Color(hexSash))
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.Foreground(lipgloss.Color(hexSquid))
	delegate.Styles.DimmedTitle = delegate.Styles.DimmedTitle.Foreground(lipgloss.Color(hexOyster))
	delegate.Styles.DimmedDesc = delegate.Styles.DimmedDesc.Foreground(lipgloss.Color(hexOyster))

	l := list.New(nil, delegate, 0, 0)
	// The panel border supplies the "Workflows" title and the footer supplies
	// the hints, so strip the bubbles-list internal title/help/status chrome.
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetStatusBarItemName("workflow", "workflows")
	return Model{
		list:        l,
		keys:        defaultKeys(),
		loading:     true,
		itemHeight:  delegate.Height(),
		itemSpacing: delegate.Spacing(),
	}
}

func (m Model) Init() tea.Cmd {
	return DiscoverCmd(workflowsDir)
}

// Keys exposes display/match bindings for Home's composed help/footer.
func (m Model) Keys() selectorKeys { return m.keys }

// SelectedPath returns the filesystem path of the cursored workflow, if any.
func (m Model) SelectedPath() (string, bool) {
	item, ok := m.list.SelectedItem().(workflowItem)
	if !ok {
		return "", false
	}
	return item.path, true
}

// SelectedName returns the [workflow] name of the cursored item, if any.
func (m Model) SelectedName() (string, bool) {
	item, ok := m.list.SelectedItem().(workflowItem)
	if !ok {
		return "", false
	}
	return item.name, true
}

// SetPaneSize fits the list to a titled panel of the given outer size (no
// footer — Home renders a shared footer below both panes).
func (m Model) SetPaneSize(width, height int) Model {
	m.width, m.height = width, height
	hFrame, vFrame := shared.PanelFrame()
	w := width - hFrame
	h := height - vFrame
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	m.list.SetSize(w, h)
	return m
}

// PaneBody is the list content for an embedded Home pane (no panel chrome).
func (m Model) PaneBody(width, height int) string {
	m = m.SetPaneSize(width, height)
	switch {
	case m.loading:
		return "  Scanning " + workflowsDir + "…"
	case m.err != nil:
		return "  Failed to scan: " + m.err.Error()
	case len(m.list.Items()) == 0:
		return "  No workflows found.\n  Add a <name>.toml under " + workflowsDir + "/."
	default:
		return m.list.View()
	}
}
