package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	loading bool
	err     error
	width   int
	height  int
}

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
	l.Title = "Workflows"
	l.Styles.Title = l.Styles.Title.
		Background(lipgloss.Color(hexCharple)).
		Foreground(lipgloss.Color(hexButter)).
		Bold(true)
	l.SetShowStatusBar(false)
	l.SetStatusBarItemName("workflow", "workflows")
	return selectorModel{list: l, loading: true}
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
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil

	case workflowsLoadedMsg:
		m.loading = false
		m.err = msg.err
		return m, m.list.SetItems(msg.items)

	case tea.KeyPressMsg:
		// While the filter input is open, let the list consume Enter (it applies
		// the filter) rather than treating it as a selection.
		if msg.String() == "enter" && m.list.FilterState() != list.Filtering {
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

func (m selectorModel) View() string {
	switch {
	case m.loading:
		return "\n  Scanning " + workflowsDir + "…\n"
	case m.err != nil:
		return "\n  " + theme.Error.Render("Failed to scan "+workflowsDir+": "+m.err.Error()) + "\n"
	case len(m.list.Items()) == 0:
		return "\n  " + theme.Title.Render("No workflows found") + "\n\n" +
			theme.Question.Render("  Add a <name>.toml with a [workflow] table under "+workflowsDir+"/.") +
			"\n\n" + theme.Footer.Render("  ctrl+c quit") + "\n"
	}
	return m.list.View()
}
