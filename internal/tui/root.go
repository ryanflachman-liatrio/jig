// Package tui implements jig's Bubble Tea program. It opens on a workflow
// picker that scans .agents/jig for workflow files; selecting one shows a
// read-only view of its steps and validation status. A rootModel owns which
// screen is active and forwards messages to it, so screens stay small and
// self-contained as the app grows toward a full run monitor.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// screen identifies which sub-model is currently driving the UI.
type screen int

const (
	screenSelector screen = iota
	screenDetail
)

// showDetailMsg asks the root to open the detail screen for a workflow file.
type showDetailMsg struct{ path string }

// backToSelectorMsg asks the root to return to the picker.
type backToSelectorMsg struct{}

type rootModel struct {
	active   screen
	selector selectorModel
	detail   detailModel

	ctx  context.Context
	dark bool

	width  int
	height int
}

// New returns jig's root TUI model. ctx and darkBackground are threaded through
// for the screens that will eventually drive agents (the run monitor); the
// picker and detail screens don't need a Claude connection, so — unlike the old
// chat entry point — nothing connects on startup. darkBackground must be
// detected before the terminal enters raw mode (see cmd/jig/main.go).
func New(ctx context.Context, darkBackground bool) tea.Model {
	return rootModel{
		active:   screenSelector,
		selector: newSelectorModel(),
		ctx:      ctx,
		dark:     darkBackground,
	}
}

func (m rootModel) Init() tea.Cmd {
	return discoverWorkflowsCmd(workflowsDir)
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		var selCmd, detCmd tea.Cmd
		m.selector, selCmd = m.selector.Update(msg)
		if m.detail.ready {
			m.detail, detCmd = m.detail.Update(msg)
		}
		return m, tea.Batch(selCmd, detCmd)

	case showDetailMsg:
		m.detail = newDetailModel(msg.path)
		// Size the fresh screen to the current terminal before its first paint.
		var sizeCmd tea.Cmd
		m.detail, sizeCmd = m.detail.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenDetail
		return m, tea.Batch(sizeCmd, m.detail.Init())

	case backToSelectorMsg:
		m.active = screenSelector
		return m, nil
	}

	// Everything else goes to the active screen.
	var cmd tea.Cmd
	switch m.active {
	case screenSelector:
		m.selector, cmd = m.selector.Update(msg)
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	}
	return m, cmd
}

func (m rootModel) View() string {
	switch m.active {
	case screenDetail:
		return m.detail.View()
	default:
		return m.selector.View()
	}
}
