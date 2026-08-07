package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
)

// TestRuns verifies the runs screen renders inside a "Runs" panel and that
// scrolling keeps the selected row visible within the framed viewport when there
// are more runs than fit the inner height.
func TestRuns(t *testing.T) {
	m := newRunsModel()
	// A short terminal so the row list overflows the panel's inner height.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})

	// Seed more runs than the inner height can show at once. Rows sort
	// newest-first by ID, so run-29 lands at the top and run-00 at the bottom.
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("run-%02d", i)
		m, _ = m.Update(engineEventMsg{event: engine.RunStarted{
			RunID:    id,
			Workflow: "wf",
			Steps:    []string{"a", "b"},
		}})
	}

	// Top-edge title present.
	firstLine := strings.SplitN(m.View(), "\n", 2)[0]
	if !strings.Contains(firstLine, "Runs") {
		t.Errorf("top edge %q missing panel title \"Runs\"", firstLine)
	}

	// Drive the cursor to the bottom; the viewport must scroll to keep it visible.
	for i := 0; i < 29; i++ {
		m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if m.cursor != 29 {
		t.Fatalf("cursor = %d, want 29", m.cursor)
	}

	// The selected bottom row is the oldest run, run-00; it must be visible within
	// the scrolled viewport.
	top := m.vp.YOffset()
	bottom := top + m.vp.Height() - 1
	if m.cursor < top || m.cursor > bottom {
		t.Errorf("cursor row %d not within viewport [%d,%d]", m.cursor, top, bottom)
	}
	if !strings.Contains(m.vp.View(), "run-00") {
		t.Errorf("scrolled viewport does not show the selected run-00:\n%s", m.vp.View())
	}
}

// TestRunsNewestFirst verifies rows stay sorted newest-first as runs arrive out
// of order, that the cursor sticks to the top so a user at the head follows the
// newest run, and that once scrolled the selection follows its run rather than
// jumping when a newer run pushes in above it.
func TestRunsNewestFirst(t *testing.T) {
	m := newRunsModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	// Arrive out of chronological order.
	for _, id := range []string{"20260731-100000-a", "20260731-120000-c", "20260731-110000-b"} {
		m, _ = m.Update(engineEventMsg{event: engine.RunStarted{
			RunID: id, Workflow: "wf", Steps: []string{"s"},
		}})
	}
	wantOrder := []string{"20260731-120000-c", "20260731-110000-b", "20260731-100000-a"}
	for i, w := range wantOrder {
		if m.rows[i].id != w {
			t.Errorf("row %d: want %q, got %q", i, w, m.rows[i].id)
		}
	}
	// Cursor stuck to the top tracks the newest run.
	if got := m.rows[m.cursor].id; got != "20260731-120000-c" {
		t.Errorf("cursor should track newest at top, got %q", got)
	}

	// Scroll down to an older run, then a newer run arrives: the newest goes to the
	// top, but the selection stays on the run it was on.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // → row 1 (…-b)
	selected := m.rows[m.cursor].id
	m, _ = m.Update(engineEventMsg{event: engine.RunStarted{
		RunID: "20260731-130000-d", Workflow: "wf", Steps: []string{"s"},
	}})
	if m.rows[0].id != "20260731-130000-d" {
		t.Errorf("newest run should be at top, got %q", m.rows[0].id)
	}
	if got := m.rows[m.cursor].id; got != selected {
		t.Errorf("selection should follow run %q, got %q", selected, got)
	}
}

// TestRunsHydrate verifies that runs recovered from disk fold into rows exactly
// as live events do, and that a run already tracked from this session is not
// overwritten by its (possibly staler) journal group.
func TestRunsHydrate(t *testing.T) {
	m := newRunsModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	// A run started live this session — seeded from RunStarted only, so it is
	// still running.
	m, _ = m.Update(engineEventMsg{event: engine.RunStarted{
		RunID: "live-1", Workflow: "wf", Steps: []string{"a", "b"},
	}})

	past := [][]engine.Event{
		// Duplicate of the live run, and on disk it looks finished. Hydration must
		// skip it wholesale so the live row is not regressed to done/failed.
		{
			engine.RunStarted{RunID: "live-1", Workflow: "wf", Steps: []string{"a", "b"}},
			engine.RunFinished{RunID: "live-1", Failed: true},
		},
		// A genuinely past run: folds down to a finished, failed row.
		{
			engine.RunStarted{RunID: "past-1", Workflow: "wf", Steps: []string{"a", "b"}},
			engine.StepStatus{RunID: "past-1", StepID: "a", To: step.StatusSucceeded},
			engine.StepStatus{RunID: "past-1", StepID: "b", To: step.StatusFailed},
			engine.RunFinished{RunID: "past-1", Failed: true},
		},
	}
	m = m.hydrate(past)

	if len(m.rows) != 2 {
		t.Fatalf("rows: want 2 (live + past), got %d", len(m.rows))
	}

	// The live run must not have been marked done/failed by the duplicate group.
	live := m.rows[m.index["live-1"]]
	if live.done || live.failed {
		t.Errorf("live run regressed by duplicate hydration: done=%v failed=%v", live.done, live.failed)
	}

	// The past run folded to a finished, failed row with both steps counted done.
	pr := m.rows[m.index["past-1"]]
	if !pr.done || !pr.failed {
		t.Errorf("past run: want done && failed, got done=%v failed=%v", pr.done, pr.failed)
	}
	if got := runRowProgress(pr); got != "2/2 steps" {
		t.Errorf("past run progress: want %q, got %q", "2/2 steps", got)
	}
	if got := runRowStatus(pr); !strings.Contains(got, "failed") {
		t.Errorf("past run status render: want failed, got %q", got)
	}
}
