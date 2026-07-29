package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// --- styles ---

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#fafafa"})

	questionStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#4b4b4b", Dark: "#a0a0a0"})

	viewportStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
			Padding(0, 1)

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#D7263D", Dark: "#FF5D5D"})

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})

	footerStyle = lipgloss.NewStyle().Faint(true)
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

type claudeResponseMsg string
type claudeErrMsg struct{ err error }

func initialModel() model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	return model{
		question: "Explain what a Go goroutine is and show a simple example",
		spinner:  s,
		loading:  true,
	}
}

// askClaude returns a tea.Cmd: Bubble Tea runs this in its own goroutine and
// feeds whatever tea.Msg it returns back into Update. Never call the SDK
// directly from View - View must stay a fast, side-effect-free render.
func askClaude(question string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var answer strings.Builder

		err := claudecode.WithClient(ctx, func(client claudecode.Client) error {
			if err := client.Query(ctx, question); err != nil {
				return fmt.Errorf("query failed: %w", err)
			}

			msgChan := client.ReceiveMessages(ctx)
			for {
				select {
				case message := <-msgChan:
					if message == nil {
						return nil // stream ended
					}

					switch m := message.(type) {
					case *claudecode.AssistantMessage:
						for _, block := range m.Content {
							if textBlock, ok := block.(*claudecode.TextBlock); ok {
								answer.WriteString(textBlock.Text)
							}
						}
					case *claudecode.ResultMessage:
						if m.IsError {
							if m.Result != nil {
								return fmt.Errorf("error: %s", *m.Result)
							}
							return fmt.Errorf("error: unknown error")
						}
						return nil // success, response complete
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		})
		if err != nil {
			return claudeErrMsg{err}
		}

		return claudeResponseMsg(answer.String())
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(askClaude(m.question), m.spinner.Tick)
}

// renderContent re-renders m.response as markdown through the current
// renderer and pushes it into the viewport. glamour bakes its word-wrap
// width in at construction time, so this must be re-run whenever the
// renderer is rebuilt (e.g. on resize), not just when the response arrives.
func (m *model) renderContent() {
	if m.renderer == nil || m.response == "" {
		return
	}
	rendered, err := m.renderer.Render(m.response)
	if err != nil {
		m.viewport.SetContent(m.response)
		return
	}
	m.viewport.SetContent(rendered)
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
		m.width, m.height = msg.Width, msg.Height

		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.footerView())
		// +2 for the blank lines around the body, plus the viewport border's own frame size.
		verticalMargins := headerHeight + footerHeight + 2 + viewportStyle.GetVerticalFrameSize()

		viewportHeight := m.height - verticalMargins
		if viewportHeight < 1 {
			viewportHeight = 1
		}

		if !m.ready {
			m.viewport = viewport.New(m.width, viewportHeight)
			m.viewport.Style = viewportStyle
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = viewportHeight
		}

		wordWrap := m.width - viewportStyle.GetHorizontalFrameSize()
		if wordWrap < 1 {
			wordWrap = 1
		}
		m.renderer, _ = glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(wordWrap),
		)

		m.renderContent()
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

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
