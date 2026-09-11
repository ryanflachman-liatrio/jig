package selector

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

func numberedItems(n int) []list.Item {
	items := make([]list.Item, n)
	for i := 0; i < n; i++ {
		items[i] = workflowItem{
			name: "wf" + strconv.Itoa(i),
			desc: "d" + strconv.Itoa(i),
			path: "/p" + strconv.Itoa(i),
		}
	}
	return items
}

func newSizedSelector(t *testing.T, items []list.Item, paneW, paneH int) Model {
	t.Helper()
	m := New()
	m, _ = m.Update(workflowsLoadedMsg{items: items})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	return m.SetPaneSize(paneW, paneH)
}

// TestItemAtUnfiltered covers the base case: three items fit on one page with
// no trailing fill (pane 40x14 gives exactly PerPage=3 for the default
// delegate's 2-row height + 1-row spacing). Rows 2-3/5-6/8-9 are the two
// content rows of each item; rows 4/7 are the spacing gaps between them.
func TestItemAtUnfiltered(t *testing.T) {
	m := newSizedSelector(t, numberedItems(3), 40, 14)

	cases := []struct {
		y        int
		wantPath string
		wantOK   bool
	}{
		{y: 0, wantOK: false}, // panel title edge
		{y: 1, wantOK: false}, // list's own blank title/filter row
		{y: 2, wantPath: "/p0", wantOK: true},
		{y: 3, wantPath: "/p0", wantOK: true},
		{y: 4, wantOK: false}, // spacing gap
		{y: 5, wantPath: "/p1", wantOK: true},
		{y: 6, wantPath: "/p1", wantOK: true},
		{y: 7, wantOK: false}, // spacing gap
		{y: 8, wantPath: "/p2", wantOK: true},
		{y: 9, wantPath: "/p2", wantOK: true},
		{y: 12, wantOK: false}, // pagination row
		{y: 13, wantOK: false}, // panel bottom border
	}
	for _, tc := range cases {
		path, ok := m.ItemAt(3, tc.y)
		if ok != tc.wantOK || (ok && path != tc.wantPath) {
			t.Errorf("ItemAt(3, %d) = (%q, %v), want (%q, %v)", tc.y, path, ok, tc.wantPath, tc.wantOK)
		}
	}
}

// TestItemAtClickIsIndependentOfKeyboardSelection asserts a click resolves by
// row position, not by the list's current keyboard cursor.
func TestItemAtClickIsIndependentOfKeyboardSelection(t *testing.T) {
	m := newSizedSelector(t, numberedItems(3), 40, 14)
	// Keyboard-select the first item...
	if got, ok := m.SelectedPath(); !ok || got != "/p0" {
		t.Fatalf("expected default selection /p0, got %q ok=%v", got, ok)
	}
	// ...but click the third row (item /p2, rendered at y=8/9).
	path, ok := m.ItemAt(3, 8)
	if !ok || path != "/p2" {
		t.Fatalf("ItemAt(3, 8) = (%q, %v), want (/p2, true)", path, ok)
	}
}

// TestItemAtPagination confirms a click on the second page resolves against
// the items actually drawn there, not the first-page/unfiltered items.
func TestItemAtPagination(t *testing.T) {
	m := newSizedSelector(t, numberedItems(10), 40, 14) // PerPage=3, TotalPages=4
	if got := m.list.Paginator.PerPage; got != 3 {
		t.Fatalf("expected PerPage=3 for this fixture, got %d (test assumptions stale)", got)
	}
	m.list.NextPage()

	cases := []struct {
		y        int
		wantPath string
	}{
		{y: 2, wantPath: "/p3"},
		{y: 5, wantPath: "/p4"},
		{y: 8, wantPath: "/p5"},
	}
	for _, tc := range cases {
		path, ok := m.ItemAt(3, tc.y)
		if !ok || path != tc.wantPath {
			t.Errorf("page 2 ItemAt(3, %d) = (%q, %v), want (%q, true)", tc.y, path, ok, tc.wantPath)
		}
	}
	// Row 10 has no third-page item and no wraparound to page 1's items.
	if _, ok := m.ItemAt(3, 11); ok {
		t.Error("expected no match past the last item on page 2")
	}
}

// TestItemAtFiltered confirms a click resolves against the filtered subset at
// its rendered position, not the unfiltered item at that index.
func TestItemAtFiltered(t *testing.T) {
	m := newSizedSelector(t, numberedItems(10), 40, 14)
	m.list.SetFilterText("wf7")
	m.list.SetFilterState(list.FilterApplied)
	if got := len(m.list.VisibleItems()); got != 1 {
		t.Fatalf("expected filter to narrow to 1 item, got %d", got)
	}

	path, ok := m.ItemAt(3, 2)
	if !ok || path != "/p7" {
		t.Fatalf("ItemAt(3, 2) = (%q, %v), want (/p7, true)", path, ok)
	}
	if _, ok := m.ItemAt(3, 5); ok {
		t.Error("expected no second row for a single filtered item")
	}
}

// TestItemAtResize confirms the mapping is recomputed (not cached) after a
// pane resize, so a stale row offset from a previous size can't misfire.
func TestItemAtResize(t *testing.T) {
	m := newSizedSelector(t, numberedItems(3), 40, 14)
	if path, ok := m.ItemAt(3, 8); !ok || path != "/p2" {
		t.Fatalf("before resize: ItemAt(3, 8) = (%q, %v), want (/p2, true)", path, ok)
	}

	narrow := m.SetPaneSize(24, 9) // PerPage=floor((9-2-2)/3)=1
	if got := narrow.list.Paginator.PerPage; got != 1 {
		t.Fatalf("expected PerPage=1 at the smaller size, got %d (test assumptions stale)", got)
	}
	// /p2 no longer fits on the first page at this size.
	if _, ok := narrow.ItemAt(3, 8); ok {
		t.Error("expected the old row 8 to no longer resolve after shrinking the pane")
	}
	if path, ok := narrow.ItemAt(3, 2); !ok || path != "/p0" {
		t.Fatalf("after resize: ItemAt(3, 2) = (%q, %v), want (/p0, true)", path, ok)
	}
}

// TestItemAtUnicodeWidth confirms wide names/descriptions don't shift which
// row an item occupies (row hit testing is vertical-only; horizontal glyph
// width must never perturb it).
func TestItemAtUnicodeWidth(t *testing.T) {
	items := []list.Item{
		workflowItem{name: "日本語ワークフロー", desc: "説明文がここに入ります", path: "/wide"},
		workflowItem{name: "beta", desc: "d1", path: "/p1"},
	}
	m := newSizedSelector(t, items, 40, 14)

	if path, ok := m.ItemAt(3, 2); !ok || path != "/wide" {
		t.Fatalf("ItemAt(3, 2) = (%q, %v), want (/wide, true)", path, ok)
	}
	if path, ok := m.ItemAt(3, 5); !ok || path != "/p1" {
		t.Fatalf("ItemAt(3, 5) = (%q, %v), want (/p1, true)", path, ok)
	}
}

// TestItemAtNoOpCases is the negative-case table: everything that must not
// resolve to a workflow.
func TestItemAtNoOpCases(t *testing.T) {
	t.Run("negative and far out-of-bounds coordinates", func(t *testing.T) {
		m := newSizedSelector(t, numberedItems(3), 40, 14)
		for _, pt := range [][2]int{{-1, 2}, {3, -1}, {-5, -5}, {1000, 2}, {3, 1000}} {
			if path, ok := m.ItemAt(pt[0], pt[1]); ok {
				t.Errorf("ItemAt(%d, %d) = (%q, true), want no match", pt[0], pt[1], path)
			}
		}
	})

	t.Run("loading state", func(t *testing.T) {
		m := New() // loading=true, never delivered workflowsLoadedMsg
		m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
		m = m.SetPaneSize(40, 14)
		if _, ok := m.ItemAt(3, 2); ok {
			t.Error("expected no match while loading")
		}
	})

	t.Run("error state", func(t *testing.T) {
		m := New()
		m, _ = m.Update(workflowsLoadedMsg{err: errors.New("boom")})
		m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
		m = m.SetPaneSize(40, 14)
		if _, ok := m.ItemAt(3, 2); ok {
			t.Error("expected no match in the error state")
		}
	})

	t.Run("empty selector", func(t *testing.T) {
		m := newSizedSelector(t, nil, 40, 14)
		if _, ok := m.ItemAt(3, 2); ok {
			t.Error("expected no match with zero items")
		}
	})
}

// TestItemAtBoundaryRows confirms only a row's own interior matches: the row
// immediately above and immediately below an item must not.
func TestItemAtBoundaryRows(t *testing.T) {
	m := newSizedSelector(t, numberedItems(3), 40, 14)
	// Item /p1 occupies rows 5-6 (see TestItemAtUnfiltered).
	if _, ok := m.ItemAt(3, 4); ok {
		t.Error("row immediately above /p1 should not match")
	}
	if path, ok := m.ItemAt(3, 5); !ok || path != "/p1" {
		t.Fatalf("row 5 (top of /p1) = (%q, %v), want (/p1, true)", path, ok)
	}
	if path, ok := m.ItemAt(3, 6); !ok || path != "/p1" {
		t.Fatalf("row 6 (bottom of /p1) = (%q, %v), want (/p1, true)", path, ok)
	}
	if _, ok := m.ItemAt(3, 7); ok {
		t.Error("row immediately below /p1 should not match")
	}
}

// TestItemAtFixedSizeFixture is a render fixture at two terminal widths: it
// asserts the rendered rows and the hit-tested rows agree at each width, so a
// future rendering change that silently desyncs geometry is caught here.
func TestItemAtFixedSizeFixture(t *testing.T) {
	for _, w := range []int{40, 70} {
		m := newSizedSelector(t, numberedItems(2), w, 14)
		lines := strings.Split(m.View(), "\n")
		if len(lines) < 7 {
			t.Fatalf("width %d: expected at least 7 rendered lines, got %d", w, len(lines))
		}
		// Row 2 always renders the first item's title (stripped of ANSI, it
		// still contains the item name); ItemAt must agree it's item 0.
		if !strings.Contains(lines[2], "wf0") {
			t.Fatalf("width %d: row 2 = %q, expected to contain \"wf0\"", w, lines[2])
		}
		if path, ok := m.ItemAt(3, 2); !ok || path != "/p0" {
			t.Fatalf("width %d: ItemAt(3, 2) = (%q, %v), want (/p0, true)", w, path, ok)
		}
	}
}

func TestMoveSelectionAtUsesThreeItemClampedIncrement(t *testing.T) {
	m := newSizedSelector(t, numberedItems(10), 40, 14)
	var changed bool
	m, changed = m.MoveSelectionAt(3, 11, 1) // blank fill is wheel-eligible
	if !changed || m.list.Index() != 3 {
		t.Fatalf("wheel down index = %d changed=%v, want 3 true", m.list.Index(), changed)
	}
	m, changed = m.MoveSelectionAt(3, 2, -1)
	if !changed || m.list.Index() != 0 {
		t.Fatalf("wheel up index = %d changed=%v, want 0 true", m.list.Index(), changed)
	}
	m, changed = m.MoveSelectionAt(3, 2, -1)
	if changed || m.list.Index() != 0 {
		t.Fatalf("clamped wheel index = %d changed=%v, want 0 false", m.list.Index(), changed)
	}
	if _, changed = m.MoveSelectionAt(3, 1, 1); changed {
		t.Fatal("list title/filter row must not be wheel-eligible")
	}
}

// FuzzItemAt bounds arbitrary coordinates to a generous but finite range so
// this stays a geometry test, not an unbounded resource consumer, and asserts
// the seam never panics and never resolves outside the current page.
func FuzzItemAt(f *testing.F) {
	f.Add(0, 0)
	f.Add(3, 2)
	f.Add(-1, -1)
	f.Add(1000, 1000)
	f.Fuzz(func(t *testing.T, x, y int) {
		if x < -1000 || x > 1000 || y < -1000 || y > 1000 {
			return // keep inputs bounded; this is geometry, not a fuzz target for huge allocations
		}
		m := newSizedSelector(t, numberedItems(5), 40, 14)
		path, ok := m.ItemAt(x, y)
		if !ok {
			return
		}
		found := false
		for _, it := range m.list.VisibleItems() {
			if it.(workflowItem).path == path {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ItemAt(%d, %d) returned %q, which is not in the current visible window", x, y, path)
		}
	})
}
