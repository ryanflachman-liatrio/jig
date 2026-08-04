package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
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
	s.Style = theme.Spinner

	// enter submits the prompt (handled in Update); width is deferred to the
	// first resize. withoutBorder: the Message panel owns the frame, so the
	// textarea must not draw its own box (avoids a double border).
	ta := newInputTextarea("Ask Claude... (enter to send, alt+enter for newline, ctrl+c to quit)", 0, 3, withoutBorder())

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

// conversationTitle is the output panel's title. It folds the old header/turn
// chrome into the border edge: "connecting…" until the client connects (then it
// drops — never a persistent "Connected"), and "· Turn N of M" once more than
// one turn exists so the reader knows which of several answers is shown.
func (m chatModel) conversationTitle() string {
	title := "Conversation"
	if !m.connected && !m.fatal {
		return title + " · connecting…"
	}
	if len(m.turns) > 1 {
		return fmt.Sprintf("%s · Turn %d of %d", title, m.activeTurn+1, len(m.turns))
	}
	return title
}

// messageTitle is the input panel's title. The streaming state lives here as
// text ("· responding…") rather than a spinner glyph in the border edge, which
// would jitter the top line every tick.
func (m chatModel) messageTitle() string {
	if m.streaming {
		return "Message · responding…"
	}
	return "Message"
}

// fatalLine renders a fatal connection/stream error as its own full-width line
// beneath the panels — the chat's analogue of the monitor's gate strip — so the
// message is never truncated inside a panel title. Returns "" when no error.
func (m chatModel) fatalLine() string {
	if !m.fatal || m.fatalErr == nil {
		return ""
	}
	line := theme.Error.Render("⚠ " + m.fatalErr.Error())
	if m.width > 0 {
		line = theme.Error.MaxWidth(m.width).Render("⚠ " + m.fatalErr.Error())
	}
	return line
}

func (m chatModel) footerView() string {
	if m.fatal {
		return theme.Footer.Render("ctrl+c quit")
	}
	if m.ready && len(m.turns) > 0 {
		return theme.Footer.Render(fmt.Sprintf("enter send • alt+enter newline • esc/i switch focus • ctrl+c quit (%.0f%%)", m.viewport.ScrollPercent()*100))
	}
	return theme.Footer.Render("enter send • alt+enter newline • ctrl+c quit")
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

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Sequence(disconnectClaudeCmd(m.client), tea.Quit)

		case "enter":
			// From the output pane, enter focuses the input (mirrors "i").
			// The panel border (not the viewport's own style) reflects focus now.
			if m.focus == focusOutput {
				m.focus = focusInput
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
			}
			return m, nil

		case "i":
			if m.focus == focusOutput {
				m.focus = focusInput
				return m, m.textarea.Focus()
			}

		case "left":
			if m.focus == focusOutput && m.activeTurn > 0 {
				m.turns[m.activeTurn].scrollOffset = m.viewport.YOffset()
				m.activeTurn--
				m.renderActiveTurn()
				return m, nil
			}

		case "right":
			if m.focus == focusOutput && m.activeTurn < len(m.turns)-1 {
				m.turns[m.activeTurn].scrollOffset = m.viewport.YOffset()
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

// View stacks two titled panels: a "Conversation" output panel wrapping the
// viewport and a "Message" input panel wrapping the textarea. The focused
// panel's border is drawn primary (via the focusInput/focusOutput toggle). A
// fatal error renders as its own full-width line beneath the panels (never
// inside a truncatable title). chatModel is a standalone root model, so View
// returns a tea.View (unlike the sub-models).
func (m chatModel) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing...\n")
	}

	_, vFrame := panelFrame()
	width := m.width
	if width < 1 {
		width = 1
	}

	// Panel heights are the viewport/textarea content heights plus the frame the
	// border+title consume; handleResize sized the inner components to match.
	convH := m.viewport.Height() + vFrame
	msgH := m.textarea.Height() + vFrame

	conversation := panel(m.conversationTitle(), m.viewport.View(), width, convH, m.focus == focusOutput)
	message := panel(m.messageTitle(), m.textarea.View(), width, msgH, m.focus == focusInput)

	parts := []string{conversation, message}
	if line := m.fatalLine(); line != "" {
		parts = append(parts, line)
	}
	parts = append(parts, m.footerView())

	return tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
}
