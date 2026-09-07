package runs

import (
	"sort"
	"time"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/tui/monitor"
	"jig/internal/tui/shared"
)

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m = m.resize()
		return m, nil

	case monitor.EngineEventMsg:
		m = m.handleEngineEvent(msg.Event)
		m = m.syncViewport()
		return m, nil

	case tea.KeyPressMsg:
		rows := m.visibleRows()
		switch {
		case keybind.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m = m.syncViewport()
			}
		case keybind.Matches(msg, m.keys.Down):
			if m.cursor < len(rows)-1 {
				m.cursor++
				m = m.syncViewport()
			}
		case keybind.Matches(msg, m.keys.Open):
			if m.cursor < len(rows) {
				id := rows[m.cursor].id
				return m, func() tea.Msg { return ShowMonitorMsg{RunID: id} }
			}
		case keybind.Matches(msg, m.keys.NewRun):
			if m.wf != nil {
				wf := m.wf
				return m, func() tea.Msg { return StartRunMsg{Wf: wf} }
			}
		case keybind.Matches(msg, m.keys.Resume):
			if m.cursor < len(rows) && rows[m.cursor].paused {
				row := rows[m.cursor]
				m.notice = ""
				return m, func() tea.Msg { return ResumeRunMsg{RunID: row.id, Workflow: row.workflow} }
			}
		case keybind.Matches(msg, m.keys.Delete):
			if m.cursor < len(rows) {
				id := rows[m.cursor].id
				return m, func() tea.Msg { return RequestDeleteMsg{RunID: id} }
			}
		case keybind.Matches(msg, m.keys.Back):
			return m, func() tea.Msg { return BackMsg{} }
		}
	}
	return m, nil
}

// DeleteRun removes the row for runID from the list, rebuilds the index, and
// clamps the cursor so it stays in bounds.
func (m Model) DeleteRun(runID string) Model {
	filtered := m.rows[:0]
	for _, row := range m.rows {
		if row.id != runID {
			filtered = append(filtered, row)
		}
	}
	m.rows = filtered
	m.index = make(map[string]int, len(m.rows))
	for i, row := range m.rows {
		m.index[row.id] = i
	}
	visible := m.visibleRows()
	if m.cursor >= len(visible) && m.cursor > 0 {
		m.cursor = len(visible) - 1
	}
	return m.syncViewport()
}

// Hydrate folds runs recovered from disk into the list — one grouped,
// seq-ordered durable event slice per run, oldest run first.
// It reuses handleEngineEvent so a replayed run folds exactly as a live one did.
//
// A run already tracked in the index is skipped wholesale: a live row owns its
// state, which may be ahead of what the journal has flushed to disk.
func (m Model) Hydrate(runs [][]engine.Event) Model {
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
		if rs, ok := evs[0].(engine.RunStarted); ok {
			if i, exists := m.index[rs.RunID]; exists && !m.rows[i].done {
				m.rows[i].paused = true
				switch engine.ClassifyUnfinished(evs) {
				case engine.UnfinishedInterrupted:
					m.rows[i].interrupted = true
				default:
					m.rows[i].interrupted = false
				}
			}
		}
	}
	m = m.syncViewport()
	return m
}

// sortRows orders rows newest-first by run ID and rebuilds the id→position
// index. Run IDs are timestamp-prefixed (YYYYMMDD-HHMMSS-…), so a lexical
// descending sort is reverse-chronological. The cursor is repositioned to stay
// on whatever run it was pointing at so a re-sort never silently changes the
// selection.
func (m Model) sortRows() Model {
	var selectedID string
	visible := m.visibleRows()
	if m.cursor >= 0 && m.cursor < len(visible) {
		selectedID = visible[m.cursor].id
	}
	sort.Slice(m.rows, func(i, j int) bool {
		return m.rows[i].id > m.rows[j].id
	})
	m.index = make(map[string]int, len(m.rows))
	for i := range m.rows {
		m.index[m.rows[i].id] = i
	}
	if selectedID != "" {
		for pos, row := range m.visibleRows() {
			if row.id == selectedID {
				m.cursor = pos
				break
			}
		}
	}
	return m
}

func (m Model) handleEngineEvent(e engine.Event) Model {
	switch ev := e.(type) {
	case engine.RunStarted:
		return m.handleRunStarted(ev)
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
		m.rows[i].paused = false
		m.rows[i].interrupted = false
	}
	return m
}

// MarkLive flips a successfully resumed row back to the live scheduler state.
func (m Model) MarkLive(runID string) Model {
	if i, ok := m.index[runID]; ok {
		m.rows[i].paused = false
		m.rows[i].interrupted = false
	}
	m.notice = ""
	return m.syncViewport()
}

// SetNotice surfaces an asynchronous resume failure without replacing the list.
func (m Model) SetNotice(message string) Model {
	m.notice = message
	return m
}

func (m Model) handleRunStarted(ev engine.RunStarted) Model {
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
	m = m.sortRows()
	if atTop {
		m.cursor = 0
	}
	return m
}

// resize fits the run-row viewport to the panel's inner area, leaving a row
// for the footer below the box.
func (m Model) resize() Model {
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
	m = m.syncViewport()
	return m
}

// syncViewport re-renders the rows into the viewport and keeps the selection
// in view; called whenever the rows or the cursor change.
func (m Model) syncViewport() Model {
	if !m.ready {
		return m
	}
	m.vp.SetContent(m.rowsBody())
	m = m.ensureCursorVisible()
	return m
}

// ensureCursorVisible nudges the viewport so the selected run stays on screen
// as the cursor moves. Rows start at line 0 (the panel border carries the
// title), so the cursor index maps directly to a viewport row.
func (m Model) ensureCursorVisible() Model {
	if !m.ready {
		return m
	}
	if len(m.visibleRows()) == 0 {
		m.vp.GotoTop()
		return m
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
	return m
}
