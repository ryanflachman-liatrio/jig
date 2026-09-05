package runs

import (
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"jig/internal/step"
	"jig/internal/tui/shared"
)

func (m Model) View() string {
	if len(m.visibleRows()) == 0 {
		cta := "r  start a run"
		body := "No runs yet."
		if m.filtered && m.workflowName != "" {
			body = "No runs for " + m.workflowName + " yet."
		}
		empty := shared.RenderEmptyState(shared.EmptyState{
			Title: body,
			Body:  "Press r to start a new run for this workflow.",
			CTA:   cta,
		})
		return empty + "\n" + shared.Theme.Footer.Render("  "+shared.HintString(m.keys.NewRun, m.keys.Back, shared.KeyHelp, shared.KeyQuit)) + "\n"
	}

	footer := m.footerView()
	content := m.rowsBody()
	if m.ready {
		content = m.vp.View()
	}
	body := shared.Panel("Runs", content, m.width, m.height-lipgloss.Height(footer), true)
	return body + "\n" + footer
}

// HelpSections satisfies the root's helpProvider bridge: the run-list
// navigation and actions plus the global chord.
func (m Model) HelpSections() []shared.HelpSection {
	return []shared.HelpSection{
		{Title: "Runs", Bindings: []keybind.Binding{m.keys.Up, m.keys.Down, m.keys.Open, m.keys.NewRun, m.keys.Resume, m.keys.Delete, m.keys.Back}},
		{Title: "Global", Bindings: shared.GlobalHelpBindings(m.CapturesText())},
	}
}

func (m Model) CapturesText() bool { return false }

func (m Model) footerView() string {
	footer := shared.Theme.Footer.Render("  " + shared.HintString(m.keys.NewRun, m.keys.Resume, m.keys.Open, m.keys.Delete, m.keys.Back, shared.KeyHelp, shared.KeyQuit))
	if m.notice != "" {
		return shared.Theme.Error.Render("  "+m.notice) + "\n" + footer
	}
	return footer
}

const (
	runIDWidth       = 22
	runWorkflowWidth = 20
	runStatusWidth   = 8
)

// rowsBody renders one line per run with the selected row highlighted; the
// panel wraps it and the viewport scrolls it.
func (m Model) rowsBody() string {
	var b strings.Builder
	for i, row := range m.visibleRows() {
		cursor := "  "
		if i == m.cursor {
			cursor = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
		}

		status := runRowStatus(row)
		progress := runRowProgress(row)

		line := fmt.Sprintf("%s%-*s  %-*s  %-*s  %s",
			cursor,
			runIDWidth, truncate(row.id, runIDWidth),
			runWorkflowWidth, truncate(row.workflow, runWorkflowWidth),
			runStatusWidth, status,
			progress,
		)
		if i == m.cursor {
			b.WriteString(shared.Theme.SelectedLine.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func runRowStatus(row runRow) string {
	if row.failed {
		return shared.Theme.Error.Render("failed")
	}
	if row.done {
		return shared.Theme.Valid.Render("done")
	}
	if row.paused {
		return shared.Theme.Question.Render("paused")
	}
	return shared.Theme.Running.Render("running")
}

func runRowProgress(row runRow) string {
	if row.total == 0 {
		return ""
	}
	done := 0
	for _, s := range row.statuses {
		switch s {
		case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
			done++
		}
	}
	return fmt.Sprintf("%d/%d steps", done, row.total)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
