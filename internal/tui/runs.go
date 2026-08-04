package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// runsModel is the run-list screen: one row per active/completed run, updated
// in real-time as engine events arrive. The user can press r to start another
// run of the same workflow, Enter to open the monitor for the selected run, or
// esc to return to the detail screen.
type runsModel struct {
	rows   []runRow
	index  map[string]int // runID → rows position
	cursor int
	wf     *workflow.Workflow // used for "r" to start another run

	vp    viewport.Model // scrolls the run rows within the panel frame
	ready bool

	width  int
	height int
}

type runRow struct {
	id       string
	workflow string
	statuses map[string]step.Status // stepID → current status
	total    int
	done     bool
	failed   bool
	started  time.Time
}

func newRunsModel() runsModel {
	return runsModel{index: make(map[string]int)}
}

// withWorkflow returns a copy of the model with the current workflow set, so
// the user can press r to start another run without going back to detail.
func (m runsModel) withWorkflow(wf *workflow.Workflow) runsModel {
	m.wf = wf
	return m
}

func (m runsModel) Update(msg tea.Msg) (runsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case engineEventMsg:
		m = m.handleEngineEvent(msg.event)
		m.syncViewport()
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.syncViewport()
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
				m.syncViewport()
			}
		case "enter":
			if m.cursor < len(m.rows) {
				id := m.rows[m.cursor].id
				return m, func() tea.Msg { return showMonitorMsg{runID: id} }
			}
		case "r":
			if m.wf != nil {
				return m, func() tea.Msg { return startRunMsg{wf: m.wf} }
			}
		case "esc", "q", "backspace", "h", "left":
			return m, func() tea.Msg { return backToSelectorMsg{} }
		}
	}
	return m, nil
}

func (m runsModel) handleEngineEvent(e engine.Event) runsModel {
	switch ev := e.(type) {
	case engine.RunStarted:
		if _, ok := m.index[ev.RunID]; ok {
			return m
		}
		row := runRow{
			id:       ev.RunID,
			workflow: ev.Workflow,
			statuses: make(map[string]step.Status, len(ev.Steps)),
			total:    len(ev.Steps),
			started:  time.Now(),
		}
		for _, id := range ev.Steps {
			row.statuses[id] = step.StatusPending
		}
		m.index[ev.RunID] = len(m.rows)
		m.rows = append(m.rows, row)

	case engine.StepStatus:
		i, ok := m.index[ev.RunID]
		if !ok {
			return m
		}
		m.rows[i].statuses[ev.StepID] = ev.To

	case engine.RunFinished:
		i, ok := m.index[ev.RunID]
		if !ok {
			return m
		}
		m.rows[i].done = true
		m.rows[i].failed = ev.Failed
	}
	return m
}

// resize fits the run-row viewport to the panel's inner area, leaving a row for
// the footer below the box.
func (m *runsModel) resize() {
	hFrame, vFrame := panelFrame()
	footerH := lipgloss.Height(m.footerView())
	w := m.width - hFrame
	h := m.height - vFrame - footerH
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if !m.ready {
		m.vp = viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
		m.ready = true
	} else {
		m.vp.SetWidth(w)
		m.vp.SetHeight(h)
	}
	m.syncViewport()
}

// syncViewport re-renders the rows into the viewport and keeps the selection in
// view; called whenever the rows or the cursor change.
func (m *runsModel) syncViewport() {
	if !m.ready {
		return
	}
	m.vp.SetContent(m.rowsBody())
	m.ensureCursorVisible()
}

// ensureCursorVisible nudges the viewport so the selected run stays on screen as
// the cursor moves. Rows start at line 0 (the panel border carries the title),
// so the cursor index maps directly to a viewport row.
func (m *runsModel) ensureCursorVisible() {
	if !m.ready {
		return
	}
	row := m.cursor
	const margin = 1
	top := m.vp.YOffset()
	bottom := top + m.vp.Height() - 1
	switch {
	case row-margin < top:
		m.vp.SetYOffset(row - margin)
	case row+margin > bottom:
		m.vp.SetYOffset(row + margin - m.vp.Height() + 1)
	}
}

func (m runsModel) footerView() string {
	return theme.Footer.Render("  r new run  •  enter monitor  •  esc back  •  ctrl+c quit")
}

// rowsBody renders one line per run with the selected row highlighted; the panel
// wraps it and the viewport scrolls it.
func (m runsModel) rowsBody() string {
	var b strings.Builder
	for i, row := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = theme.SelectedBar.Render(CursorBar) + " "
		}

		status := runRowStatus(row)
		progress := runRowProgress(row)

		line := fmt.Sprintf("%s%-22s  %-20s  %-8s  %s",
			cursor,
			truncate(row.id, 22),
			truncate(row.workflow, 20),
			status,
			progress,
		)
		if i == m.cursor {
			b.WriteString(theme.SelectedLine.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func (m runsModel) View() string {
	if len(m.rows) == 0 {
		return "\n  " + theme.Title.Render("No runs yet") + "\n\n" +
			theme.Question.Render("  Press r in a workflow detail to start a run.") + "\n\n" +
			theme.Footer.Render("  esc back  •  ctrl+c quit") + "\n"
	}

	footer := m.footerView()
	content := m.rowsBody()
	if m.ready {
		content = m.vp.View()
	}
	body := panel("Runs", content, m.width, m.height-lipgloss.Height(footer), true)
	return body + "\n" + footer
}

func runRowStatus(row runRow) string {
	if row.failed {
		return theme.Error.Render("failed")
	}
	if row.done {
		return theme.Valid.Render("done")
	}
	return theme.Running.Render("running")
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
