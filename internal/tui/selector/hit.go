package selector

import "jig/internal/tui/shared"

// ItemAt returns the path of the workflow item whose rendered row contains
// the pane-local point (x, y) — (0, 0) is the outer top-left corner of the
// panel Home renders this selector into (see shared.Panel), the same
// coordinate space SetPaneSize/PaneBody size and render into.
//
// The row math mirrors bubbles' own list.Model layout exactly: the list
// always reserves one row for its title/filter line and one for its
// pagination line (both present even when blank — lipgloss.Height("") is 1,
// not 0), then lays out VisibleItems() over the remaining rows at
// itemHeight+itemSpacing per slot, sliced to the current page via the list's
// own Paginator. This is why hit testing can't be a flat width×height
// division: filtering, pagination, and scrolling all change which item (if
// any) is actually drawn at a given row.
func (m Model) ItemAt(x, y int) (string, bool) {
	if m.loading || m.err != nil {
		return "", false
	}

	ox, oy := shared.PanelContentOrigin()
	hFrame, vFrame := shared.PanelFrame()
	contentW := m.width - hFrame
	// The list itself reserves its own title/filter row (1) and pagination
	// row (1) on top of the panel's border/title frame.
	contentH := m.height - vFrame - 2

	cx, cy := x-ox, y-(oy+1)
	if cx < 0 || cy < 0 || cx >= contentW || cy >= contentH {
		return "", false
	}

	items := m.list.VisibleItems()
	if len(items) == 0 {
		return "", false
	}
	slot := m.itemHeight + m.itemSpacing
	if slot <= 0 {
		return "", false
	}
	onPage := cy / slot
	if cy%slot >= m.itemHeight {
		return "", false // the spacing gap between rows
	}

	pager := m.list.Paginator
	start, end := pager.GetSliceBounds(len(items))
	page := items[start:end]
	if onPage >= len(page) {
		return "", false // trailing blank fill below the last item on this page
	}
	item, ok := page[onPage].(workflowItem)
	if !ok {
		return "", false
	}
	return item.path, true
}
