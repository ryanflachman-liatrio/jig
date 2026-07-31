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

	// Phase 3: review steps park here until a verdict is delivered.
	pendingReview *engine.ReviewRequest

	// Phase 4: rolling output buffer per step (last outputMaxLines lines).
	stepOutput map[string]*strings.Builder

	vp    viewport.Model
	ready bool

	width  int
	height int
}

// outputMaxLines is the number of streaming output lines shown per step.
const outputMaxLines = 10

type monitorStep struct {
	id     string
	status step.Status
	start  time.Time
	end    time.Time
}

func newMonitorModel(runID string) monitorModel {
	return monitorModel{
		runID:      runID,
		index:      make(map[string]int),
		stepOutput: make(map[string]*strings.Builder),
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
	if m.stepOutput == nil {
		m.stepOutput = make(map[string]*strings.Builder)
	}
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
		// Review verdict: digit keys 1–9 select a choice when a review is pending.
		if m.pendingReview != nil {
			choices := m.pendingReview.Choices
			for i, ch := range choices {
				key := fmt.Sprintf("%d", i+1)
				if msg.String() == key {
					rev := m.pendingReview
					m.pendingReview = nil
					return m, func() tea.Msg {
						return reviewVerdictMsg{
							runID:   rev.RunID,
							stepID:  rev.StepID,
							verdict: ch,
						}
					}
				}
			}
		}
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
		// Clear stale review when the step resolves.
		if m.pendingReview != nil && m.pendingReview.StepID == ev.StepID {
			if ev.To == step.StatusSucceeded || ev.To == step.StatusFailed || ev.To == step.StatusSkipped {
				m.pendingReview = nil
			}
		}

	case engine.ReviewRequest:
		if ev.RunID != m.runID {
			return m
		}
		m.pendingReview = &ev

	case engine.StepOutput:
		if ev.RunID != m.runID {
			return m
		}
		if m.stepOutput == nil {
			m.stepOutput = make(map[string]*strings.Builder)
		}
		buf, ok := m.stepOutput[ev.StepID]
		if !ok {
			buf = &strings.Builder{}
			m.stepOutput[ev.StepID] = buf
		}
		buf.WriteString(ev.Delta)

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

	// Review picker: show when a step is awaiting human input.
	if m.pendingReview != nil {
		b.WriteString("\n")
		b.WriteString("  " + markerStyle.Render("Review required — step: "+m.pendingReview.StepID) + "\n\n")
		for i, ch := range m.pendingReview.Choices {
			b.WriteString(fmt.Sprintf("    [%d] %s\n", i+1, ch))
		}
		b.WriteString("\n")
	}

	// Streaming output: show last outputMaxLines lines for any running agent step.
	for _, s := range m.steps {
		if s.status != step.StatusRunning {
			continue
		}
		buf, ok := m.stepOutput[s.id]
		if !ok || buf.Len() == 0 {
			continue
		}
		lines := strings.Split(buf.String(), "\n")
		// Keep only the last outputMaxLines non-empty lines.
		var recent []string
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				recent = append(recent, l)
			}
		}
		if len(recent) > outputMaxLines {
			recent = recent[len(recent)-outputMaxLines:]
		}
		b.WriteString("\n  " + questionStyle.Render("▸ "+s.id) + "\n")
		for _, l := range recent {
			b.WriteString("    " + l + "\n")
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
	} else if m.pendingReview != nil {
		status = markerStyle.Render("awaiting review")
	} else {
		status = runningStyle.Render("running")
	}
	hint := "esc runs list  •  ctrl+c quit"
	if m.pendingReview != nil {
		hint = "1-9 select verdict  •  " + hint
	}
	return footerStyle.Render("  " + status + "  ·  " + hint)
}

func (m monitorModel) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.footerView())
}
