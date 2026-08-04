package tui

import (
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/workflow"
)

// detailModel is the read-only view of one workflow: its steps, their kinds,
// and the loop/gate structure — plus whether the file passes full validation.
// It runs no agents; it just makes the parsed graph legible.
type detailModel struct {
	path string
	keys detailKeys

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
	keys := defaultDetailKeys()
	// Run is unavailable until a valid workflow loads; disabling it both stops
	// matching "r" and drops "r run" from the footer.
	keys.Run.SetEnabled(false)
	return detailModel{path: path, keys: keys}
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
		m.keys.Run.SetEnabled(m.wf != nil)
		if m.ready {
			m.vp.SetContent(m.body())
			m.vp.GotoTop()
		}
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case keybind.Matches(msg, m.keys.Runs):
			return m, func() tea.Msg { return showRunsMsg{} }
		case keybind.Matches(msg, m.keys.Back):
			return m, func() tea.Msg { return backToSelectorMsg{} }
		case keybind.Matches(msg, m.keys.Run):
			// Run is disabled until m.wf is non-nil, so a match implies a workflow.
			wf := m.wf
			return m, func() tea.Msg { return startRunMsg{wf: wf} }
		}
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// resize (re)builds the viewport to fit the panel's inner area, leaving one row
// for the footer help line below the box.
func (m *detailModel) resize() {
	hFrame, vFrame := panelFrame()
	footerHeight := lipgloss.Height(m.footerView())
	vpWidth := m.width - hFrame
	vpHeight := m.height - vFrame - footerHeight
	if vpWidth < 1 {
		vpWidth = 1
	}
	if vpHeight < 1 {
		vpHeight = 1
	}
	if !m.ready {
		m.vp = viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight))
		m.ready = true
	} else {
		m.vp.SetWidth(vpWidth)
		m.vp.SetHeight(vpHeight)
	}
	m.vp.SetContent(m.body())
}

// titleText is the detail panel's title: the workflow name, falling back to the
// file path when the workflow could not be named (unparseable or unnamed).
func (m detailModel) titleText() string {
	if m.meta.Name != "" {
		return m.meta.Name
	}
	return m.path
}

func (m detailModel) footerView() string {
	// Run drops out automatically when disabled (no workflow loaded).
	return theme.Footer.Render("  " + hintString(m.keys.Run, m.keys.Runs, m.keys.Back, keyQuit))
}

// body renders the header and step list into the viewport's content.
func (m detailModel) body() string {
	if !m.loaded {
		return "\n  Loading…\n"
	}

	// The panel title carries the workflow name, so the body opens with the
	// version/description/path metadata rather than repeating the name.
	var b strings.Builder
	b.WriteString("\n")
	if m.meta.Version != "" {
		b.WriteString("  " + theme.Question.Render("v"+m.meta.Version) + "\n")
	}
	if m.meta.Description != "" {
		b.WriteString("  " + theme.Question.Render(m.meta.Description) + "\n")
	}
	b.WriteString("  " + theme.Path.Render(m.path) + "\n\n")

	if m.loadErr != nil {
		b.WriteString("  " + theme.Error.Render(IconError+" invalid workflow") + "\n\n")
		// The loader aggregates every problem; indent the multi-line message.
		for _, line := range strings.Split(strings.TrimRight(m.loadErr.Error(), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		return b.String()
	}

	b.WriteString("  " + theme.Valid.Render(IconSuccess+" valid") +
		theme.Question.Render(fmt.Sprintf("  ·  %d step(s)", len(m.wf.Steps))) + "\n\n")
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
		if style, ok := theme.Step.Types[typ]; ok {
			badge = style.Render(typ)
		}
		fmt.Fprintf(&b, "  %2d  %s  %s",
			i+1,
			theme.Step.ID.Render(padRight(s.ID, idWidth)),
			padRight(badge, len(typ), 8),
		)
		for _, mk := range stepMarkers(s) {
			b.WriteString("  " + theme.Marker.Render(mk))
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
	footer := m.footerView()
	body := panel(m.titleText(), m.vp.View(), m.width, m.height-lipgloss.Height(footer), true)
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}
