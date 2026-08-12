package selector

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"jig/internal/workflow"
)

// workflowItem is one row in the selector: a discovered workflow file, shown
// by its [workflow] name and description. It satisfies list.DefaultItem so the
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

// DiscoverCmd walks root for *.toml files that carry a [workflow] table with a
// name, and returns them sorted by name. It uses the tolerant workflow.LoadMeta
// peek so a structurally-invalid workflow still shows up (its problems surface
// later, in the detail view).
func DiscoverCmd(root string) tea.Cmd {
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
