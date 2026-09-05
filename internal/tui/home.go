package tui

import (
	"path/filepath"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/detail"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

// homePane identifies which Home dual-pane region owns keys.
type homePane int

const (
	homeWorkflows homePane = iota
	homeRuns
)

// homeNarrowBreak stacks Workflows above Runs when the terminal is narrower
// than this width. Monitor keeps its own single-panel narrow behavior.
const homeNarrowBreak = 100

// homeKeys are Home-owned chords (pane focus / detail overlay) that are not
// owned by the embedded selector or runs models.
type homeKeys struct {
	Pane   keybind.Binding
	Detail keybind.Binding
	Nav    keybind.Binding
}

func defaultHomeKeys() homeKeys {
	return homeKeys{
		Pane:   keybind.NewBinding(keybind.WithKeys("tab", "shift+tab"), keybind.WithHelp("tab", "pane")),
		Detail: keybind.NewBinding(keybind.WithKeys("d"), keybind.WithHelp("d", "detail")),
		Nav:    keybind.NewBinding(keybind.WithKeys("j", "k"), keybind.WithHelp("j/k", "move")),
	}
}

// homeWorkflowLoadedMsg carries a fully loaded workflow for the selected path
// so Home can seed the Runs pane's StartRun / filter context.
type homeWorkflowLoadedMsg struct {
	path string
	name string
	wf   *workflow.Workflow
	err  error
}

func loadHomeWorkflowCmd(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		meta, _, _ := workflow.LoadMeta(path)
		wf, err := workflow.Load(path)
		name := meta.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		return homeWorkflowLoadedMsg{path: path, name: name, wf: wf, err: err}
	}
}

func (m rootModel) homeHelpSections() []shared.HelpSection {
	keys := defaultHomeKeys()
	switch {
	case m.showDetailOverlay:
		return m.detail.HelpSections()
	case m.homeFocus == homeRuns:
		open := m.runs.Keys().Open
		open.SetHelp("enter", "open run")
		newRun := m.runs.Keys().NewRun
		resume := m.runs.Keys().Resume
		del := m.runs.Keys().Delete
		return []shared.HelpSection{
			{Title: "Runs", Bindings: []keybind.Binding{
				open, newRun, resume, del, keys.Nav, keys.Pane,
			}},
			{Title: "Global", Bindings: shared.GlobalHelpBindings(false)},
		}
	default:
		filter := m.selector.Keys().Filter
		open := m.selector.Keys().Open
		open.SetHelp("enter", "focus runs")
		newRun := m.runs.Keys().NewRun
		return []shared.HelpSection{
			{Title: "Workflows", Bindings: []keybind.Binding{
				keys.Nav, open, newRun, keys.Detail, filter, keys.Pane,
			}},
			{Title: "Global", Bindings: shared.GlobalHelpBindings(m.selector.CapturesText())},
		}
	}
}

func (m rootModel) homeCapturesText() bool {
	if m.showDetailOverlay {
		return m.detail.CapturesText()
	}
	return m.homeFocus == homeWorkflows && m.selector.CapturesText()
}

func (m rootModel) homeFooter() string {
	sections := m.homeHelpSections()
	bindings := sections[0].Bindings
	hint := shared.CompactHint(max(m.width-2, 0), shared.MoreHelpBinding(m.homeCapturesText()), bindings...)
	f := shared.Theme.Footer
	if m.width > 0 {
		f = f.MaxWidth(m.width)
	}
	return f.Render("  " + hint)
}

func (m rootModel) homeView() string {
	if m.showDetailOverlay {
		return m.detail.View()
	}
	footer := m.homeFooter()
	footerH := lipgloss.Height(footer)
	bodyH := m.height - footerH
	if bodyH < 1 {
		bodyH = 1
	}

	var body string
	if m.width < homeNarrowBreak {
		topH := bodyH / 2
		if topH < 3 {
			topH = 3
		}
		botH := bodyH - topH
		if botH < 3 {
			botH = 3
			if topH+botH > bodyH && bodyH > 6 {
				topH = bodyH - botH
			}
		}
		wfPane := m.homeWorkflowPane(m.width, topH)
		runsPane := m.homeRunsPane(m.width, botH)
		body = lipgloss.JoinVertical(lipgloss.Left, wfPane, runsPane)
	} else {
		leftW := m.width * 2 / 5
		if leftW < 24 {
			leftW = 24
		}
		rightW := m.width - leftW
		if rightW < 24 {
			rightW = m.width / 2
			leftW = m.width - rightW
		}
		wfPane := m.homeWorkflowPane(leftW, bodyH)
		runsPane := m.homeRunsPane(rightW, bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, wfPane, runsPane)
	}
	return body + "\n" + footer
}

func (m rootModel) homeWorkflowPane(width, height int) string {
	content := m.selector.PaneBody(width, height)
	return shared.Panel("Workflows", content, width, height, m.homeFocus == homeWorkflows)
}

func (m rootModel) homeRunsPane(width, height int) string {
	title := "Runs"
	if name := m.runs.WorkflowName(); name != "" {
		title = "Runs · " + name
	}
	content := m.runs.PaneBody(width, height)
	return shared.Panel(title, content, width, height, m.homeFocus == homeRuns)
}

func (m rootModel) updateHome(msg tea.Msg) (rootModel, tea.Cmd) {
	switch msg := msg.(type) {
	case homeWorkflowLoadedMsg:
		if msg.path != "" && msg.path != m.homeSelectedPath {
			return m, nil
		}
		m.homeSelectedPath = msg.path
		m.runs = m.runs.WithWorkflowContext(msg.name, msg.wf)
		return m, nil

	case detail.BackMsg:
		m.showDetailOverlay = false
		return m, nil

	case detail.StartRunMsg:
		next, cmd := m.startRun(msg.Wf)
		return next.(rootModel), cmd

	case tea.KeyPressMsg:
		if m.showDetailOverlay {
			var cmd tea.Cmd
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
		keys := defaultHomeKeys()
		if keybind.Matches(msg, keys.Pane) {
			if m.homeFocus == homeWorkflows {
				m.homeFocus = homeRuns
			} else {
				m.homeFocus = homeWorkflows
			}
			return m, nil
		}
		if m.homeFocus == homeWorkflows {
			return m.updateHomeWorkflowsKey(msg)
		}
		return m.updateHomeRunsKey(msg)
	}

	// Non-key messages: keep both children warm (engine events already update runs
	// at the root). Detail receives load/resize while the overlay is open.
	var sc, rc, dc tea.Cmd
	m.selector, sc = m.selector.Update(msg)
	m.runs, rc = m.runs.Update(msg)
	if m.showDetailOverlay {
		m.detail, dc = m.detail.Update(msg)
	}
	cmd := m.maybeSyncHomeSelection()
	return m, tea.Batch(sc, rc, dc, cmd)
}

func (m rootModel) updateHomeWorkflowsKey(msg tea.KeyPressMsg) (rootModel, tea.Cmd) {
	keys := defaultHomeKeys()
	sk := m.selector.Keys()

	// Enter focuses Runs (G2); Detail is the d overlay — not a root screen.
	if keybind.Matches(msg, sk.Open) && !m.selector.CapturesText() {
		m.homeFocus = homeRuns
		return m, m.maybeSyncHomeSelection()
	}
	if keybind.Matches(msg, keys.Detail) && !m.selector.CapturesText() {
		if path, ok := m.selector.SelectedPath(); ok {
			return m.openDetailOverlay(path)
		}
		return m, nil
	}
	if keybind.Matches(msg, m.runs.Keys().NewRun) && !m.selector.CapturesText() {
		if wf := m.runs.Workflow(); wf != nil {
			next, cmd := m.startRun(wf)
			return next.(rootModel), cmd
		}
		return m, nil
	}

	prev, _ := m.selector.SelectedPath()
	var cmd tea.Cmd
	m.selector, cmd = m.selector.Update(msg)
	sync := m.maybeSyncHomeSelection()
	if path, ok := m.selector.SelectedPath(); ok && path != prev {
		return m, tea.Batch(cmd, sync)
	}
	return m, tea.Batch(cmd, sync)
}

func (m rootModel) updateHomeRunsKey(msg tea.KeyPressMsg) (rootModel, tea.Cmd) {
	// Esc / q on Runs returns focus to Workflows (one-level), not a leave-Home.
	if keybind.Matches(msg, m.runs.Keys().Back) {
		m.homeFocus = homeWorkflows
		return m, nil
	}
	var cmd tea.Cmd
	m.runs, cmd = m.runs.Update(msg)
	return m, cmd
}

func (m *rootModel) maybeSyncHomeSelection() tea.Cmd {
	path, ok := m.selector.SelectedPath()
	if !ok || path == m.homeSelectedPath {
		return nil
	}
	m.homeSelectedPath = path
	name, _ := m.selector.SelectedName()
	// Filter immediately by name; full wf arrives async for StartRun.
	m.runs = m.runs.WithWorkflowContext(name, nil)
	return loadHomeWorkflowCmd(path)
}

func (m rootModel) openDetailOverlay(path string) (rootModel, tea.Cmd) {
	m.detail = detail.New(path)
	var sizeCmd tea.Cmd
	m.detail, sizeCmd = m.detail.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.showDetailOverlay = true
	return m, tea.Batch(sizeCmd, m.detail.Init())
}

// sizeHomeChildren sizes embedded panes so list/viewport math matches the View.
func (m rootModel) sizeHomeChildren() rootModel {
	footerH := lipgloss.Height(m.homeFooter())
	bodyH := m.height - footerH
	if bodyH < 1 {
		bodyH = 1
	}
	if m.width < homeNarrowBreak {
		topH := bodyH / 2
		if topH < 3 {
			topH = 3
		}
		botH := bodyH - topH
		if botH < 3 {
			botH = 3
		}
		m.selector = m.selector.SetPaneSize(m.width, topH)
		m.runs = m.runs.SetPaneSize(m.width, botH)
		return m
	}
	leftW := m.width * 2 / 5
	if leftW < 24 {
		leftW = 24
	}
	rightW := m.width - leftW
	if rightW < 24 {
		rightW = m.width / 2
		leftW = m.width - rightW
	}
	m.selector = m.selector.SetPaneSize(leftW, bodyH)
	m.runs = m.runs.SetPaneSize(rightW, bodyH)
	return m
}
