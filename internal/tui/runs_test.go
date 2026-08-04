package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
)

// TestRuns verifies the runs screen renders inside a "Runs" panel and that
// scrolling keeps the selected row visible within the framed viewport when there
// are more runs than fit the inner height.
func TestRuns(t *testing.T) {
	m := newRunsModel()
	// A short terminal so the row list overflows the panel's inner height.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})

	// Seed more runs than the inner height can show at once.
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

	// The selected (last) run must be visible within the scrolled viewport.
	top := m.vp.YOffset()
	bottom := top + m.vp.Height() - 1
	if m.cursor < top || m.cursor > bottom {
		t.Errorf("cursor row %d not within viewport [%d,%d]", m.cursor, top, bottom)
	}
	if !strings.Contains(m.vp.View(), "run-29") {
		t.Errorf("scrolled viewport does not show the selected run-29:\n%s", m.vp.View())
	}
}
