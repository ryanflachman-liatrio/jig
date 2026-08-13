package chat

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

func (m chatModel) Init() tea.Cmd {
	return tea.Batch(connectClaudeCmd(m.ctx), textarea.Blink)
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
		switch {
		case keybind.Matches(msg, m.keys.Quit):
			return m, tea.Sequence(disconnectClaudeCmd(m.client), tea.Quit)

		case keybind.Matches(msg, m.keys.Send):
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

		case keybind.Matches(msg, m.keys.ToOutput):
			if m.focus == focusInput {
				m.textarea.Blur()
				m.focus = focusOutput
			}
			return m, nil

		case keybind.Matches(msg, m.keys.FocusInput):
			if m.focus == focusOutput {
				m.focus = focusInput
				return m, m.textarea.Focus()
			}

		case keybind.Matches(msg, m.keys.PrevTurn):
			if m.focus == focusOutput && m.activeTurn > 0 {
				m.turns[m.activeTurn].scrollOffset = m.viewport.YOffset()
				m.activeTurn--
				m.renderActiveTurn()
				return m, nil
			}

		case keybind.Matches(msg, m.keys.NextTurn):
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
