package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

// workflowsDir is the directory tree jig scans for workflow files on startup.
// Keeping workflows under a well-known path (rather than anywhere in the repo)
// makes the picker fast and predictable, and mirrors the .jig/ run-directory
// convention.
const workflowsDir = ".agents/jig"

// workflowItem is one row in the selector: a discovered workflow file, shown by
// its [workflow] name and description. It satisfies list.DefaultItem so the
// stock two-line delegate renders it.
type workflowItem struct {
	name string
	desc string
	path string
}

func (i workflowItem) Title() string       { return i.name }
func (i workflowItem) Description() string { return i.desc }
func (i workflowItem) FilterValue() string { return i.name + " " + i.desc }

// workflowsLoadedMsg is delivered once discovery finishes. err is set only for
// an unexpected walk failure; a missing directory yields zero items and no
// error, which the view renders as an empty state.
type workflowsLoadedMsg struct {
	items []list.Item
	err   error
}

// selectorModel is the startup screen: a filterable list of the workflows found
// under workflowsDir.
type selectorModel struct {
	list    list.Model
	keys    selectorKeys
	loading bool
	err     error
	width   int
	height  int
}

// Charmtone tokens for the list delegate — the only place in the tui package
// that directly references raw palette colors rather than theme styles.
const (
	hexCharple = "#6B50FF" // primary   (violet)
	hexDolly   = "#FF60FF" // secondary (magenta)
	hexSash    = "#ECEBF0" // fgBase
	hexSquid   = "#858392" // fgMuted
	hexOyster  = "#605F6B" // fgDim
)

func newSelectorModel() selectorModel {
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
	// The panel border now supplies the "Workflows" title and the footer supplies
	// the hints, so strip the bubbles-list internal title/help/status chrome.
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetStatusBarItemName("workflow", "workflows")
	return selectorModel{list: l, keys: defaultSelectorKeys(), loading: true}
}

// discoverWorkflowsCmd walks workflowsDir for *.toml files that carry a
// [workflow] table with a name, and returns them sorted by name. It uses the
// tolerant workflow.LoadMeta peek so a structurally-invalid workflow still
// shows up (its problems surface later, in the detail view).
func discoverWorkflowsCmd(root string) tea.Cmd {
	return func() tea.Msg {
		var items []list.Item
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// A missing root is expected (no workflows yet); report empty.
				if os.IsNotExist(err) {
					return filepath.SkipAll
				}
				return err
			}
			if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".toml") {
				return nil
			}
			meta, ok, err := workflow.LoadMeta(path)
			if err != nil || !ok {
				// Unreadable or not-a-workflow files are simply skipped.
				return nil
			}
			items = append(items, workflowItem{
				name: meta.Name,
				desc: meta.Description,
				path: path,
			})
			return nil
		})
		if err != nil {
			return workflowsLoadedMsg{err: err}
		}
		sort.Slice(items, func(a, b int) bool {
			return items[a].(workflowItem).name < items[b].(workflowItem).name
		})
		return workflowsLoadedMsg{items: items}
	}
}

func (m selectorModel) Update(msg tea.Msg) (selectorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
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
				return m, func() tea.Msg { return showDetailMsg{path: item.path} }
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
func (m *selectorModel) resize() {
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
}

// footerView is the single plain hint line below the panel; it branches on the
// filter state, mirroring lazygit's global keybind bar.
// helpSections satisfies helpProvider: the selector's list navigation, filter,
// and open action, plus the global chord.
func (m selectorModel) helpSections() []shared.HelpSection {
	return []shared.HelpSection{
		{Title: "Workflows", Bindings: []keybind.Binding{m.keys.Nav, m.keys.Filter, m.keys.Open, m.keys.Apply, m.keys.Clear}},
		{Title: "Global", Bindings: []keybind.Binding{shared.KeyHelp, shared.KeyQuit}},
	}
}

// capturesText reports whether the list filter is capturing text, in which case
// "?" is a literal character rather than the help chord.
func (m selectorModel) capturesText() bool {
	return m.list.FilterState() == list.Filtering
}

func (m selectorModel) footerView() string {
	if m.list.FilterState() == list.Filtering {
		return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.Apply, m.keys.Clear, shared.KeyQuit))
	}
	return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.Nav, m.keys.Filter, m.keys.Open, shared.KeyHelp, shared.KeyQuit))
}

func (m selectorModel) View() string {
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
