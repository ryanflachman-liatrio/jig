// Package tui implements the Bubble Tea program that streams a response
// from the Claude Agent SDK and renders it as markdown.
package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	question string
	response string
	err      error

	spinner  spinner.Model
	viewport viewport.Model
	renderer *glamour.TermRenderer

	loading bool
	ready   bool
	width   int
	height  int
}

// New returns the initial state of the TUI as a tea.Model, ready to be
// passed to tea.NewProgram.
func New() tea.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	return model{
		question: "Explain what a Go goroutine is and show a simple example",
		spinner:  s,
		loading:  true,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(askClaude(m.question), m.spinner.Tick)
}

func (m model) headerView() string {
	return titleStyle.Render("Claude Agent SDK - Client Streaming Example") + "\n" +
		questionStyle.Render("Asking: "+m.question)
}

func (m model) footerView() string {
	if m.ready && !m.loading && m.err == nil && m.response != "" {
		return footerStyle.Render(fmt.Sprintf("↑/↓ scroll (%.0f%%) • q to quit", m.viewport.ScrollPercent()*100))
	}
	return footerStyle.Render("Press q to quit")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.handleResize(msg)
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case claudeResponseMsg:
		m.response = string(msg)
		m.loading = false
		m.renderContent()
		m.viewport.GotoTop()
		return m, nil

	case claudeErrMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if !m.ready {
		return "Initializing...\n"
	}

	var body string
	switch {
	case m.err != nil:
		body = errorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	case m.loading:
		body = fmt.Sprintf("%s Waiting for Claude...", m.spinner.View())
	default:
		body = m.viewport.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), "", body, "", m.footerView())
}
