package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	claudecode "github.com/severity1/claude-agent-sdk-go"
)

type claudeResponseMsg string
type claudeErrMsg struct{ err error }

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
