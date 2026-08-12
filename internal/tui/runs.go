package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/tui/monitor"
	"jig/internal/tui/shared"
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
	keys   runsKeys

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
	return runsModel{index: make(map[string]int), keys: defaultRunsKeys()}
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

	case monitor.EngineEventMsg:
		m = m.handleEngineEvent(msg.Event)
		m.syncViewport()
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case keybind.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.syncViewport()
			}
		case keybind.Matches(msg, m.keys.Down):
			if m.cursor < len(m.rows)-1 {
				m.cursor++
				m.syncViewport()
			}
		case keybind.Matches(msg, m.keys.Open):
			if m.cursor < len(m.rows) {
				id := m.rows[m.cursor].id
				return m, func() tea.Msg { return showMonitorMsg{runID: id} }
			}
		case keybind.Matches(msg, m.keys.NewRun):
			if m.wf != nil {
				return m, func() tea.Msg { return startRunMsg{wf: m.wf} }
			}
		case keybind.Matches(msg, m.keys.Back):
			return m, func() tea.Msg { return backToSelectorMsg{} }
		}
	}
	return m, nil
}

// hydrate folds runs recovered from disk into the list — one grouped, seq-ordered
// event slice per run, oldest run first (see engine.ReplayJournal). It reuses
// handleEngineEvent so a replayed run folds into a row exactly as a live one did,
// keeping a single definition of "events → row".
//
// A run already tracked in the index is skipped wholesale: a run started this
// session owns its row, and its live state may be ahead of what the journal has
// flushed to disk. Hydration only fills in runs the session has not seen.
func (m runsModel) hydrate(runs [][]engine.Event) runsModel {
	for _, evs := range runs {
		if len(evs) == 0 {
			continue
		}
		// The first journal line is always RunStarted; use it to dedupe against
		// live rows before folding the rest.
		if rs, ok := evs[0].(engine.RunStarted); ok {
			if _, exists := m.index[rs.RunID]; exists {
				continue
			}
		}
		for _, e := range evs {
			m = m.handleEngineEvent(e)
		}
	}
	m.syncViewport()
	return m
}

// sortRows orders rows newest-first by run ID and rebuilds the id→position
// index. Run IDs are timestamp-prefixed (YYYYMMDD-HHMMSS-…), so a lexical
// descending sort is reverse-chronological — more reliable than the started
// field, which is only the moment the row was folded, not the run's real start.
// The cursor is repositioned to stay on whatever run it was pointing at, so a
// re-sort never silently changes the selection out from under the user.
func (m *runsModel) sortRows() {
	var selectedID string
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		selectedID = m.rows[m.cursor].id
	}
	sort.Slice(m.rows, func(i, j int) bool {
		return m.rows[i].id > m.rows[j].id
	})
	m.index = make(map[string]int, len(m.rows))
	for i := range m.rows {
		m.index[m.rows[i].id] = i
	}
	if pos, ok := m.index[selectedID]; ok {
		m.cursor = pos
	}
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
		// A run starting now is the newest, so it belongs at the top. Stick the
		// cursor to the top when it was already there, so a user watching the head
		// of the list follows the newest run; otherwise sortRows keeps the cursor
		// on whatever run it was on.
		atTop := m.cursor == 0
		m.rows = append(m.rows, row)
		m.sortRows()
		if atTop {
			m.cursor = 0
		}

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
	hFrame, vFrame := shared.PanelFrame()
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
	return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.NewRun, m.keys.Open, m.keys.Back, shared.KeyHelp, shared.KeyQuit))
}

// helpSections satisfies helpProvider: the run-list navigation and actions plus
// the global chord.
func (m runsModel) helpSections() []shared.HelpSection {
	return []shared.HelpSection{
		{Title: "Runs", Bindings: []keybind.Binding{m.keys.Up, m.keys.Down, m.keys.Open, m.keys.NewRun, m.keys.Back}},
		{Title: "Global", Bindings: []keybind.Binding{shared.KeyHelp, shared.KeyQuit}},
	}
}

// capturesText: the runs list never captures free text.
func (m runsModel) capturesText() bool { return false }

// rowsBody renders one line per run with the selected row highlighted; the panel
// wraps it and the viewport scrolls it.
func (m runsModel) rowsBody() string {
	var b strings.Builder
	for i, row := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
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
			b.WriteString(shared.Theme.SelectedLine.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func (m runsModel) View() string {
	if len(m.rows) == 0 {
		return "\n  " + shared.Theme.Title.Render("No runs yet") + "\n\n" +
			shared.Theme.Question.Render("  Press r in a workflow detail to start a run.") + "\n\n" +
			shared.Theme.Footer.Render("  "+shared.HintString(m.keys.Back, shared.KeyQuit)) + "\n"
	}

	footer := m.footerView()
	content := m.rowsBody()
	if m.ready {
		content = m.vp.View()
	}
	body := shared.Panel("Runs", content, m.width, m.height-lipgloss.Height(footer), true)
	return body + "\n" + footer
}

func runRowStatus(row runRow) string {
	if row.failed {
		return shared.Theme.Error.Render("failed")
	}
	if row.done {
		return shared.Theme.Valid.Render("done")
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
