package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"jig/internal/engine"
	"jig/internal/step"
)

// monitorModel is the per-run view: a live step-status table for one run,
// updated as engine events arrive. The user can press esc to return to the
// runs list.
type monitorModel struct {
	runID    string
	workflow string
	steps    []monitorStep
	index    map[string]int // stepID → steps position
	done     bool
	failed   bool

	vp    viewport.Model
	ready bool

	width  int
	height int
}

type monitorStep struct {
	id     string
	status step.Status
	start  time.Time
	end    time.Time
}

func newMonitorModel(runID string) monitorModel {
	return monitorModel{
		runID: runID,
		index: make(map[string]int),
	}
}

// withSnapshot initialises the monitor from a RunSnapshot so the user sees
// current state immediately when navigating to an already-running run.
func (m monitorModel) withSnapshot(snap engine.RunSnapshot) monitorModel {
	m.workflow = snap.Workflow
	m.done = snap.Done
	m.failed = snap.Failed
	m.steps = make([]monitorStep, len(snap.Steps))
	m.index = make(map[string]int, len(snap.Steps))
	for i, st := range snap.Steps {
		m.steps[i] = monitorStep{id: st.ID, status: st.Status}
		m.index[st.ID] = i
	}
	return m
}

func (m monitorModel) Update(msg tea.Msg) (monitorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case engineEventMsg:
		m = m.handleEngineEvent(msg.event)
		if m.ready {
			m.vp.SetContent(m.body())
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "backspace", "h", "left":
			return m, func() tea.Msg { return showRunsMsg{} }
		}
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m monitorModel) handleEngineEvent(e engine.Event) monitorModel {
	switch ev := e.(type) {
	case engine.RunStarted:
		if ev.RunID != m.runID {
			return m
		}
		m.workflow = ev.Workflow
		m.steps = make([]monitorStep, len(ev.Steps))
		m.index = make(map[string]int, len(ev.Steps))
		for i, id := range ev.Steps {
			m.steps[i] = monitorStep{id: id, status: step.StatusPending}
			m.index[id] = i
		}

	case engine.StepStatus:
		if ev.RunID != m.runID {
			return m
		}
		i, ok := m.index[ev.StepID]
		if !ok {
			return m
		}
		m.steps[i].status = ev.To
		if ev.To == step.StatusRunning {
			m.steps[i].start = time.Now()
		}
		if ev.To == step.StatusSucceeded || ev.To == step.StatusFailed || ev.To == step.StatusSkipped {
			m.steps[i].end = time.Now()
		}

	case engine.RunFinished:
		if ev.RunID != m.runID {
			return m
		}
		m.done = true
		m.failed = ev.Failed
	}
	return m
}

func (m *monitorModel) resize() {
	footerH := lipgloss.Height(m.footerView())
	vpH := m.height - footerH
	if vpH < 1 {
		vpH = 1
	}
	if !m.ready {
		m.vp = viewport.New(m.width, vpH)
		m.ready = true
	} else {
		m.vp.Width = m.width
		m.vp.Height = vpH
	}
	m.vp.SetContent(m.body())
}

func (m monitorModel) body() string {
	var b strings.Builder

	wfName := m.workflow
	if wfName == "" {
		wfName = m.runID
	}

	b.WriteString("\n  " + titleStyle.Render(wfName) + "  " +
		pathStyle.Render(m.runID) + "\n\n")

	if len(m.steps) == 0 {
		b.WriteString("  " + questionStyle.Render("Waiting for run to start…") + "\n")
		return b.String()
	}

	idWidth := 2
	for _, s := range m.steps {
		if len(s.id) > idWidth {
			idWidth = len(s.id)
		}
	}

	for _, s := range m.steps {
		indicator, style := stepIndicator(s.status)
		dur := stepDuration(s)
		line := fmt.Sprintf("  %s  %s  %s  %s",
			indicator,
			style.Render(padRight(s.id, idWidth)),
			statusStyle(s.status).Render(fmt.Sprintf("%-16s", string(s.status))),
			questionStyle.Render(dur),
		)
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	if m.done {
		if m.failed {
			b.WriteString("  " + errorStyle.Render("✗ run failed") + "\n")
		} else {
			b.WriteString("  " + validStyle.Render("✓ run complete") + "\n")
		}
	}
	return b.String()
}

func stepIndicator(s step.Status) (string, lipgloss.Style) {
	switch s {
	case step.StatusPending:
		return "○", questionStyle
	case step.StatusRunning:
		return "●", runningStyle
	case step.StatusSucceeded:
		return "✓", validStyle
	case step.StatusFailed:
		return "✗", errorStyle
	case step.StatusSkipped:
		return "—", questionStyle
	case step.StatusValidating:
		return "⇢", questionStyle
	case step.StatusAwaitingReview:
		return "?", markerStyle
	default:
		return "·", questionStyle
	}
}

func statusStyle(s step.Status) lipgloss.Style {
	switch s {
	case step.StatusRunning:
		return runningStyle
	case step.StatusSucceeded:
		return validStyle
	case step.StatusFailed:
		return errorStyle
	default:
		return questionStyle
	}
}

func stepDuration(s monitorStep) string {
	if s.start.IsZero() {
		return "—"
	}
	end := s.end
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(s.start).Round(time.Millisecond)
	return d.String()
}

func (m monitorModel) footerView() string {
	var status string
	if m.done {
		if m.failed {
			status = errorStyle.Render("failed")
		} else {
			status = validStyle.Render("done")
		}
	} else {
		status = runningStyle.Render("running")
	}
	return footerStyle.Render("  " + status + "  ·  esc runs list  •  ctrl+c quit")
}

func (m monitorModel) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.footerView())
}
