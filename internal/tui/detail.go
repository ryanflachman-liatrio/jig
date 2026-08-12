package tui

import (
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/chart"
	"jig/internal/tui/monitor"
	"jig/internal/tui/shared"
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

	vp       viewport.Model
	ready    bool
	width    int
	height   int
	vpWidth  int  // viewport inner width; the chart lays out to fit it
	viewMode bool // false = flat step list (default), true = chart
}

func newDetailModel(path string) detailModel {
	keys := defaultDetailKeys()
	// Run and Toggle are unavailable until a valid workflow loads; disabling
	// them both stops matching their keys and drops them from the footer.
	keys.Run.SetEnabled(false)
	keys.Toggle.SetEnabled(false)
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
		// The chart is only meaningful for a valid graph; keep the toggle out of
		// the footer (and unmatched) until one loads.
		m.keys.Toggle.SetEnabled(m.wf != nil)
		if m.wf == nil && m.viewMode {
			// A prior valid workflow was charted, then a reload failed: fall back
			// to the list so body() never charts a nil workflow.
			m.viewMode = false
			m.applyViewMode()
		}
		if m.ready {
			m.vp.SetContent(m.body())
			m.vp.GotoTop()
		}
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case keybind.Matches(msg, m.keys.Runs):
			return m, func() tea.Msg { return monitor.ShowRunsMsg{} }
		case keybind.Matches(msg, m.keys.Toggle):
			m.viewMode = !m.viewMode
			m.applyViewMode()
			if m.ready {
				m.vp.SetContent(m.body())
				m.vp.SetXOffset(0)
				m.vp.GotoTop()
			}
			return m, nil
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
	hFrame, vFrame := shared.PanelFrame()
	footerHeight := lipgloss.Height(m.footerView())
	vpWidth := m.width - hFrame
	vpHeight := m.height - vFrame - footerHeight
	if vpWidth < 1 {
		vpWidth = 1
	}
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.vpWidth = vpWidth
	if !m.ready {
		m.vp = viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight))
		m.ready = true
		// Set horizontal-scroll bindings to match the current mode: the list view
		// keeps them unbound (content fits the panel width); the chart view binds
		// them as the wide-graph escape hatch. See applyViewMode.
		m.applyViewMode()
	} else {
		m.vp.SetWidth(vpWidth)
		m.vp.SetHeight(vpHeight)
	}
	m.vp.SetContent(m.body())
}

// applyViewMode reconciles the toggle-dependent state after viewMode flips (or on
// first viewport build): the Toggle's own help label, the Back binding, and the
// viewport's horizontal-scroll keys.
//
// The list view keeps horizontal scroll unbound — its content is laid out to the
// panel width. The chart view lays out to fit that width too, but a graph wider
// than one rank can still overflow, so it re-enables horizontal scroll (arrows +
// vim h/l) as the escape hatch. Because the default scroll keys ("left"/"h")
// collide with Back's vim aliases, Back sheds them in chart mode so left/h reach
// the viewport; esc/q/backspace still leave the screen.
func (m *detailModel) applyViewMode() {
	if m.viewMode {
		m.keys.Toggle.SetHelp("v", "list")
		m.keys.Back.SetKeys("esc", "q", "backspace")
		def := viewport.DefaultKeyMap()
		m.vp.KeyMap.Left = def.Left
		m.vp.KeyMap.Right = def.Right
	} else {
		m.keys.Toggle.SetHelp("v", "chart")
		m.keys.Back.SetKeys("esc", "q", "backspace", "h", "left")
		m.vp.KeyMap.Left.Unbind()
		m.vp.KeyMap.Right.Unbind()
	}
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
	// Run and Toggle drop out automatically when disabled (no workflow loaded).
	return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.Run, m.keys.Toggle, m.keys.Runs, m.keys.Back, shared.KeyHelp, shared.KeyQuit))
}

// helpSections satisfies helpProvider: the workflow detail actions plus the
// global chord. Run drops out when disabled, mirroring the footer.
func (m detailModel) helpSections() []shared.HelpSection {
	return []shared.HelpSection{
		{Title: "Workflow", Bindings: []keybind.Binding{m.keys.Run, m.keys.Toggle, m.keys.Runs, m.keys.Back}},
		{Title: "Global", Bindings: []keybind.Binding{shared.KeyHelp, shared.KeyQuit}},
	}
}

// capturesText: the detail screen never captures free text.
func (m detailModel) capturesText() bool { return false }

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
		b.WriteString("  " + shared.Theme.Question.Render("v"+m.meta.Version) + "\n")
	}
	if m.meta.Description != "" {
		b.WriteString("  " + shared.Theme.Question.Render(m.meta.Description) + "\n")
	}
	b.WriteString("  " + shared.Theme.Path.Render(m.path) + "\n\n")

	if m.loadErr != nil {
		b.WriteString("  " + shared.Theme.Error.Render(shared.IconError+" invalid workflow") + "\n\n")
		// The loader aggregates every problem; indent the multi-line message.
		for _, line := range strings.Split(strings.TrimRight(m.loadErr.Error(), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		return b.String()
	}

	b.WriteString("  " + shared.Theme.Valid.Render(shared.IconSuccess+" valid") +
		shared.Theme.Question.Render(fmt.Sprintf("  ·  %d step(s)", len(m.wf.Steps))) + "\n\n")
	if m.viewMode {
		b.WriteString(m.chartView())
	} else {
		b.WriteString(m.stepsView())
	}
	return b.String()
}

// chartView renders the depends_on DAG as a top-down flowchart laid out to fit
// the viewport's inner width. It falls back to the flat list if no valid
// workflow is loaded (the toggle is disabled in that case, so this is defensive).
func (m detailModel) chartView() string {
	if m.wf == nil {
		return m.stepsView()
	}
	return chart.RenderChart(m.wf, m.vpWidth)
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
		if style, ok := shared.Theme.Step.Types[typ]; ok {
			badge = style.Render(typ)
		}
		fmt.Fprintf(&b, "  %2d  %s  %s",
			i+1,
			shared.Theme.Step.ID.Render(padRight(s.ID, idWidth)),
			padRight(badge, len(typ), 8),
		)
		for _, mk := range stepMarkers(s) {
			b.WriteString("  " + shared.Theme.Marker.Render(mk))
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

var padRight = shared.PadRight

func (m detailModel) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}
	footer := m.footerView()
	body := shared.Panel(m.titleText(), m.vp.View(), m.width, m.height-lipgloss.Height(footer), true)
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}
