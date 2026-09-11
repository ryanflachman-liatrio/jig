package selector

import "jig/internal/tui/shared"

// itemAtPoint resolves the pane-local point (x, y) to the visible item drawn
// there, along with its global index in the list's current filtered item set
// (the index list.Model.Select expects) — (0, 0) is the outer top-left corner
// of the panel Home renders this selector into (see shared.Panel), the same
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
func (m Model) itemAtPoint(x, y int) (int, workflowItem, bool) {
	if m.loading || m.err != nil {
		return 0, workflowItem{}, false
	}

	ox, oy := shared.PanelContentOrigin()
	hFrame, vFrame := shared.PanelFrame()
	contentW := m.width - hFrame
	// The list itself reserves its own title/filter row (1) and pagination
	// row (1) on top of the panel's border/title frame.
	contentH := m.height - vFrame - 2

	cx, cy := x-ox, y-(oy+1)
	if cx < 0 || cy < 0 || cx >= contentW || cy >= contentH {
		return 0, workflowItem{}, false
	}

	items := m.list.VisibleItems()
	if len(items) == 0 {
		return 0, workflowItem{}, false
	}
	slot := m.itemHeight + m.itemSpacing
	if slot <= 0 {
		return 0, workflowItem{}, false
	}
	onPage := cy / slot
	if cy%slot >= m.itemHeight {
		return 0, workflowItem{}, false // the spacing gap between rows
	}

	pager := m.list.Paginator
	start, end := pager.GetSliceBounds(len(items))
	page := items[start:end]
	if onPage >= len(page) {
		return 0, workflowItem{}, false // trailing blank fill below the last item on this page
	}
	item, ok := page[onPage].(workflowItem)
	if !ok {
		return 0, workflowItem{}, false
	}
	return start + onPage, item, true
}

// ItemAt returns the path of the workflow item whose rendered row contains
// the pane-local point (x, y). See itemAtPoint for the coordinate space and
// row-resolution rules.
func (m Model) ItemAt(x, y int) (string, bool) {
	_, item, ok := m.itemAtPoint(x, y)
	if !ok {
		return "", false
	}
	return item.path, true
}

// SelectItemAt moves the keyboard cursor to the workflow item whose rendered
// row contains the pane-local point (x, y), the same way pressing j/k would,
// without opening its Detail overlay — only the 'd' key does that. See
// itemAtPoint for the coordinate space and row-resolution rules.
func (m Model) SelectItemAt(x, y int) (Model, bool) {
	idx, _, ok := m.itemAtPoint(x, y)
	if !ok {
		return m, false
	}
	m.list.Select(idx)
	return m, true
}
