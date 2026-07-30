package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"jig/internal/workflow"
)

// detailModel is the read-only view of one workflow: its steps, their kinds,
// and the loop/gate structure — plus whether the file passes full validation.
// It runs no agents; it just makes the parsed graph legible.
type detailModel struct {
	path string

	meta    workflow.Meta
	wf      *workflow.Workflow
	loadErr error
	loaded  bool

	vp     viewport.Model
	ready  bool
	width  int
	height int
}

func newDetailModel(path string) detailModel {
	return detailModel{path: path}
}

// workflowLoadedMsg carries the result of fully loading the selected workflow.
// meta is filled from the tolerant peek so the header renders even when full
// validation (wf) fails.
type workflowLoadedMsg struct {
	meta workflow.Meta
	wf   *workflow.Workflow
	err  error
}

func loadWorkflowCmd(path string) tea.Cmd {
	return func() tea.Msg {
		meta, _, _ := workflow.LoadMeta(path)
		wf, err := workflow.Load(path)
		return workflowLoadedMsg{meta: meta, wf: wf, err: err}
	}
}

func (m detailModel) Init() tea.Cmd {
	return loadWorkflowCmd(m.path)
}

func (m detailModel) Update(msg tea.Msg) (detailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case workflowLoadedMsg:
		m.loaded = true
		m.meta = msg.meta
		m.wf = msg.wf
		m.loadErr = msg.err
		if m.ready {
			m.vp.SetContent(m.body())
			m.vp.GotoTop()
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "enter", "esc", "q", "backspace", "h", "left":
			return m, func() tea.Msg { return backToSelectorMsg{} }
		}
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// resize (re)builds the viewport to fit the terminal, leaving one row for the
// footer help line.
func (m *detailModel) resize() {
	footerHeight := lipgloss.Height(m.footerView())
	vpHeight := m.height - footerHeight
	if vpHeight < 1 {
		vpHeight = 1
	}
	if !m.ready {
		m.vp = viewport.New(m.width, vpHeight)
		m.ready = true
	} else {
		m.vp.Width = m.width
		m.vp.Height = vpHeight
	}
	m.vp.SetContent(m.body())
}

func (m detailModel) footerView() string {
	return footerStyle.Render("  esc/enter back • ctrl+c quit")
}

// body renders the header and step list into the viewport's content.
func (m detailModel) body() string {
	if !m.loaded {
		return "\n  Loading…\n"
	}

	var b strings.Builder
	name := m.meta.Name
	if name == "" {
		name = m.path
	}
	header := titleStyle.Render(name)
	if m.meta.Version != "" {
		header += "  " + questionStyle.Render("v"+m.meta.Version)
	}
	b.WriteString("\n  " + header + "\n")
	if m.meta.Description != "" {
		b.WriteString("  " + questionStyle.Render(m.meta.Description) + "\n")
	}
	b.WriteString("  " + pathStyle.Render(m.path) + "\n\n")

	if m.loadErr != nil {
		b.WriteString("  " + errorStyle.Render("✗ invalid workflow") + "\n\n")
		// The loader aggregates every problem; indent the multi-line message.
		for _, line := range strings.Split(strings.TrimRight(m.loadErr.Error(), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		return b.String()
	}

	b.WriteString("  " + validStyle.Render("✓ valid") +
		questionStyle.Render(fmt.Sprintf("  ·  %d step(s)", len(m.wf.Steps))) + "\n\n")
	b.WriteString(m.stepsView())
	return b.String()
}

// stepsView renders one line per step: an index, the step id, a type badge, and
// any loop/gate/guard annotations.
func (m detailModel) stepsView() string {
	steps := m.wf.Steps
	idWidth := len("id")
	for _, s := range steps {
		if len(s.ID) > idWidth {
			idWidth = len(s.ID)
		}
	}

	var b strings.Builder
	for i, s := range steps {
		typ := string(s.Type)
		badge := typ
		if style, ok := stepTypeStyles[typ]; ok {
			badge = style.Render(typ)
		}
		fmt.Fprintf(&b, "  %2d  %s  %s",
			i+1,
			stepIDStyle.Render(padRight(s.ID, idWidth)),
			padRight(badge, len(typ), 8),
		)
		for _, mk := range stepMarkers(s) {
			b.WriteString("  " + markerStyle.Render(mk))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// stepMarkers describes the extra structure hanging off a step: its bounded
// loop back-edge, its deterministic validation gate, and any run guard.
func stepMarkers(s workflow.Step) []string {
	var out []string
	if s.Loop != nil {
		m := fmt.Sprintf("↺ loop→%s", s.Loop.Goto)
		if s.Loop.MaxIterations > 0 {
			m += fmt.Sprintf(" (max %d)", s.Loop.MaxIterations)
		}
		out = append(out, m)
	}
	if s.Validate != nil {
		out = append(out, "⇢ gate")
	}
	if s.When != "" {
		out = append(out, "when "+s.When)
	}
	return out
}

// padRight pads s with spaces to visibleWidth. When the string carries ANSI
// styling, pass the unstyled length as the second arg so padding math ignores
// the escape codes; a third arg overrides the target width.
func padRight(s string, args ...int) string {
	width := 0
	visible := len(s)
	switch len(args) {
	case 1:
		width = args[0]
	case 2:
		visible = args[0]
		width = args[1]
	}
	if pad := width - visible; pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func (m detailModel) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.footerView())
}
