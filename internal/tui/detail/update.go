package detail

import (
	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/monitor"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

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

func (m Model) Init() tea.Cmd {
	return loadWorkflowCmd(m.path)
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m = m.resize()
		return m, nil

	case workflowLoadedMsg:
		return m.applyLoaded(msg), nil

	case tea.KeyPressMsg:
		switch {
		case keybind.Matches(msg, m.keys.Runs):
			return m, func() tea.Msg { return monitor.ShowRunsMsg{} }
		case keybind.Matches(msg, m.keys.Toggle):
			return m.handleToggle()
		case keybind.Matches(msg, m.keys.Back):
			return m, func() tea.Msg { return BackMsg{} }
		case keybind.Matches(msg, m.keys.Run):
			// Run is disabled until m.wf is non-nil, so a match implies a workflow.
			wf := m.wf
			return m, func() tea.Msg { return StartRunMsg{Wf: wf} }
		}
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) applyLoaded(msg workflowLoadedMsg) Model {
	m.Loaded = true
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
		m = m.applyViewMode()
	}
	if m.Ready {
		m.vp.SetContent(m.body())
		m.vp.GotoTop()
	}
	return m
}

func (m Model) handleToggle() (Model, tea.Cmd) {
	m.viewMode = !m.viewMode
	m = m.applyViewMode()
	if m.Ready {
		m.vp.SetContent(m.body())
		m.vp.SetXOffset(0)
		m.vp.GotoTop()
	}
	return m, nil
}

// resize (re)builds the viewport to fit the panel's inner area, leaving one row
// for the footer help line below the box.
func (m Model) resize() Model {
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
	if !m.Ready {
		m.vp = viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight))
		m.Ready = true
		// Set horizontal-scroll bindings to match the current mode: the list view
		// keeps them unbound (content fits the panel width); the chart view binds
		// them as the wide-graph escape hatch. See applyViewMode.
		m = m.applyViewMode()
	} else {
		m.vp.SetWidth(vpWidth)
		m.vp.SetHeight(vpHeight)
	}
	m.vp.SetContent(m.body())
	return m
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
func (m Model) applyViewMode() Model {
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
	return m
}
