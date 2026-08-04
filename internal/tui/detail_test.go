package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/workflow"
)

// TestDetail verifies the detail screen renders inside a panel whose top-edge
// title is the workflow name, falling back to the file path when the workflow is
// unnamed (e.g. unparseable).
func TestDetail(t *testing.T) {
	topEdge := func(m detailModel) string {
		return strings.SplitN(m.View(), "\n", 2)[0]
	}

	t.Run("named workflow titles with the name", func(t *testing.T) {
		m := newDetailModel("path/to/wf.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
		m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{Name: "My Flow"}, err: errors.New("boom")})
		if first := topEdge(m); !strings.Contains(first, "My Flow") {
			t.Errorf("top edge %q missing workflow name title", first)
		}
	})

	t.Run("unnamed workflow falls back to path", func(t *testing.T) {
		m := newDetailModel("path/to/unnamed.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
		m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{}, err: errors.New("boom")})
		if first := topEdge(m); !strings.Contains(first, "unnamed.toml") {
			t.Errorf("top edge %q should fall back to the file path", first)
		}
	})
}
