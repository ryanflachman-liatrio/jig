package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	claudecode "github.com/severity1/claude-agent-sdk-go"
)

type model struct {
	choices  []string
	cursor   int
	selected map[int]string

	question string
	response string
	err      error
}

type claudeResponseMsg string
type claudeErrMsg struct{ err error }

func initialModel() model {
	return model{
		choices:  []string{"Buy Carrots", "Buy Celery", "Buy kohlrabi"},
		selected: make(map[int]string),
		question: "Explain what a Go goroutine is and show a simple example",
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
	return askClaude(m.question)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case claudeResponseMsg:
		m.response = string(msg)
		return m, nil

	case claudeErrMsg:
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}

		case "enter", " ":
			if _, ok := m.selected[m.cursor]; ok {
				delete(m.selected, m.cursor)
			} else {
				m.selected[m.cursor] = m.choices[m.cursor]
			}
		}
	}

	return m, nil
}

func (m model) View() string {
	s := "Claude Agent SDK - Client Streaming Example\n"
	s += "Asking: " + m.question + "\n"

	switch {
	case m.err != nil:
		s += fmt.Sprintf("\nError: %v\n", m.err)
	case m.response != "":
		s += "\n" + m.response + "\n"
	default:
		s += "\nWaiting for Claude...\n"
	}

	s += "\nPress q to quit.\n"

	return s
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
