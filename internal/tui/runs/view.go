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
		name := m.workflowName
		if name == "" {
			name = "this workflow"
		}
		return "\n  " + shared.Theme.Title.Render("No runs yet") + "\n\n" +
			shared.Theme.Question.Render("  No runs yet for "+name+".") + "\n\n" +
			shared.Theme.Question.Render("  Press r to start a run.") + "\n\n" +
			shared.Theme.Footer.Render("  "+shared.HintString(m.keys.NewRun, m.keys.Back, shared.KeyQuit)) + "\n"
	}

	footer := m.footerView()
	content := m.rowsBody()
	if m.ready {
		content = m.vp.View()
	}
	title := "Runs"
	if m.workflowName != "" {
		title = "Runs · " + m.workflowName
	}
	body := shared.Panel(title, content, m.width, m.height-lipgloss.Height(footer), true)
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
	runIDWidth       = 8
	runWorkflowWidth = 16
	runStatusWidth   = 8
)

// shortRunID returns the trailing 8-character run suffix used in Home lists
// and status chrome (G1). Full IDs remain the datastore key.
func shortRunID(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 && len(id)-i-1 >= 8 {
		return id[i+1 : i+9]
	}
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}

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
			runIDWidth, shortRunID(row.id),
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
