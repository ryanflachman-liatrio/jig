package tui

import (
	"context"
	"fmt"
	"strings"

	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// focusTarget identifies which pane currently receives non-global key
// input: the chat input textarea, or the output viewport.
type focusTarget int

const (
	focusInput focusTarget = iota
	focusOutput
)

// chatModel is the streaming Claude chat client — the original spike that
// proved SDK streaming into Bubble Tea. It is no longer the app's entry
// screen (see rootModel), but is kept because the coming run monitor will
// reuse per-step streaming.
type chatModel struct {
	textarea textarea.Model
	spinner  spinner.Model
	viewport viewport.Model
	renderer *glamour.TermRenderer

	turns         []turn
	activeTurn    int
	streamingTurn int
	streaming     bool
	focus         focusTarget

	client  claudecode.Client
	msgChan <-chan claudecode.Message
	ctx     context.Context

	darkBackground bool

	connected bool
	fatal     bool
	fatalErr  error

	ready  bool
	width  int
	height int
}

// newChatModel returns the initial state of the streaming chat as a tea.Model.
// ctx governs the lifetime of the Claude connection and every SDK call made on
// it. darkBackground selects the glamour markdown style and must be detected by
// the caller before the terminal is put in raw mode (see cmd/jig/main.go).
func newChatModel(ctx context.Context, darkBackground bool) tea.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	ta := textarea.New()
	ta.Placeholder = "Ask Claude... (enter to send, alt+enter for newline, ctrl+c to quit)"
	// enter submits the prompt (handled in Update), so newlines are inserted
	// with alt/shift+enter instead of the textarea's default enter binding.
	ta.KeyMap.InsertNewline = keybind.NewBinding(
		keybind.WithKeys("alt+enter", "shift+enter"),
		keybind.WithHelp("alt+enter", "insert newline"),
	)
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	focusedStyle, blurredStyle := textarea.DefaultStyles()
	focusedStyle.Base = textareaStyle.BorderForeground(textareaFocusedBorder)
	blurredStyle.Base = textareaStyle.BorderForeground(textareaBlurredBorder)
	ta.FocusedStyle = focusedStyle
	ta.BlurredStyle = blurredStyle
	ta.Focus()

	return chatModel{
		textarea:       ta,
		spinner:        s,
		ctx:            ctx,
		darkBackground: darkBackground,
		focus:          focusInput,
	}
}

func (m chatModel) Init() tea.Cmd {
	return tea.Batch(connectClaudeCmd(m.ctx), textarea.Blink)
}

func (m chatModel) headerView() string {
	status := "Connecting to Claude..."
	switch {
	case m.fatal:
		status = "⚠ " + m.fatalErr.Error()
	case m.connected:
		status = "Connected"
	}
	return titleStyle.Render("jig - Claude chat") + "\n" + questionStyle.Render(status)
}

func (m chatModel) statusLineView() string {
	switch {
	case m.streaming:
		return statusLineStyle.Render(m.spinner.View() + " Claude is responding...")
	case !m.connected && !m.fatal:
		return statusLineStyle.Render("Connecting to Claude...")
	default:
		return ""
	}
}

func (m chatModel) footerView() string {
	if m.fatal {
		return footerStyle.Render("ctrl+c quit")
	}
	if m.ready && len(m.turns) > 0 {
		return footerStyle.Render(fmt.Sprintf("enter send • alt+enter newline • esc/i switch focus • ctrl+c quit (%.0f%%)", m.viewport.ScrollPercent()*100))
	}
	return footerStyle.Render("enter send • alt+enter newline • ctrl+c quit")
}

// turnIndicatorView reports which turn is currently displayed, and how to
// move between turns. It always renders exactly one line so the layout
// math in handleResize doesn't need to react to the turn count changing.
func (m chatModel) turnIndicatorView() string {
	if len(m.turns) == 0 {
		return turnIndicatorStyle.Render(" ")
	}
	return turnIndicatorStyle.Render(fmt.Sprintf("Turn %d of %d (←/→ to switch)", m.activeTurn+1, len(m.turns)))
}

func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.handleResize(msg)
		return m, nil

	case spinner.TickMsg:
		if !m.streaming {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case claudeConnectedMsg:
		m.client = msg.client
		m.msgChan = msg.msgChan
		m.connected = true
		return m, waitForClaudeMessageCmd(m.msgChan)

	case claudeConnectErrMsg:
		m.fatal = true
		m.fatalErr = msg.err
		m.textarea.Blur()
		return m, nil

	case claudeDeltaMsg:
		m.turns[m.streamingTurn].answer += string(msg)
		if m.activeTurn == m.streamingTurn {
			m.renderActiveTurn()
		}
		return m, waitForClaudeMessageCmd(m.msgChan)

	case claudeTurnCompleteMsg:
		m.streaming = false
		if m.activeTurn == m.streamingTurn {
			m.renderActiveTurn()
		}
		return m, waitForClaudeMessageCmd(m.msgChan)

	case claudeTurnErrorMsg:
		t := &m.turns[m.streamingTurn]
		t.isError = true
		if t.answer != "" {
			t.answer += "\n\n[error: " + msg.err.Error() + "]"
		} else {
			t.answer = msg.err.Error()
		}
		m.streaming = false
		if m.activeTurn == m.streamingTurn {
			m.renderActiveTurn()
		}
		return m, waitForClaudeMessageCmd(m.msgChan)

	case claudeChannelClosedMsg:
		m.fatal = true
		m.fatalErr = errChannelClosed
		if m.streaming {
			t := &m.turns[m.streamingTurn]
			t.isError = true
			t.answer += "\n\n[error: " + errChannelClosed.Error() + "]"
			m.activeTurn = m.streamingTurn
		} else {
			m.turns = append(m.turns, turn{isError: true, answer: errChannelClosed.Error()})
			m.activeTurn = len(m.turns) - 1
		}
		m.streaming = false
		m.textarea.Blur()
		m.renderActiveTurn()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Sequence(disconnectClaudeCmd(m.client), tea.Quit)

		case "enter":
			// From the output pane, enter focuses the input (mirrors "i").
			if m.focus == focusOutput {
				m.focus = focusInput
				m.viewport.Style = viewportBlurredStyle
				return m, m.textarea.Focus()
			}
			if !m.connected || m.fatal || m.streaming {
				return m, nil
			}
			prompt := strings.TrimSpace(m.textarea.Value())
			if prompt == "" {
				return m, nil
			}
			m.turns = append(m.turns, turn{question: prompt})
			m.activeTurn = len(m.turns) - 1
			m.streamingTurn = m.activeTurn
			m.textarea.Reset()
			m.streaming = true
			m.renderActiveTurn()
			return m, tea.Batch(submitPromptCmd(m.ctx, m.client, prompt), m.spinner.Tick)

		case "esc":
			if m.focus == focusInput {
				m.textarea.Blur()
				m.focus = focusOutput
				m.viewport.Style = viewportFocusedStyle
			}
			return m, nil

		case "i":
			if m.focus == focusOutput {
				m.focus = focusInput
				m.viewport.Style = viewportBlurredStyle
				return m, m.textarea.Focus()
			}

		case "left":
			if m.focus == focusOutput && m.activeTurn > 0 {
				m.turns[m.activeTurn].scrollOffset = m.viewport.YOffset
				m.activeTurn--
				m.renderActiveTurn()
				return m, nil
			}

		case "right":
			if m.focus == focusOutput && m.activeTurn < len(m.turns)-1 {
				m.turns[m.activeTurn].scrollOffset = m.viewport.YOffset
				m.activeTurn++
				m.renderActiveTurn()
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	if m.focus == focusInput {
		m.textarea, cmd = m.textarea.Update(msg)
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m chatModel) View() string {
	if !m.ready {
		return "Initializing...\n"
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.headerView(),
		m.turnIndicatorView(),
		m.viewport.View(),
		m.statusLineView(),
		m.textarea.View(),
		m.footerView(),
	)
}
