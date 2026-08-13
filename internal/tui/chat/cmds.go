package chat

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// connectClaudeCmd connects a new Claude client with partial-message
// streaming enabled and captures its message channel.
func connectClaudeCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		client := claudecode.NewClient(claudecode.WithIncludePartialMessages(true))
		if err := client.Connect(ctx); err != nil {
			return claudeConnectErrMsg{err: fmt.Errorf("connect: %w", err)}
		}
		msgChan := client.ReceiveMessages(ctx)
		return claudeConnectedMsg{client: client, msgChan: msgChan}
	}
}

// waitForClaudeMessageCmd drains msgChan inside its own goroutine, only
// returning to Bubble Tea when there's something the UI needs to react to
// (a text delta, or a turn boundary). Every Update handler for the msg this
// returns must re-issue waitForClaudeMessageCmd to keep listening for the
// rest of the current turn, and for any future turn.
func waitForClaudeMessageCmd(msgChan <-chan claudecode.Message) tea.Cmd {
	return func() tea.Msg {
		for raw := range msgChan {
			switch m := raw.(type) {
			case *claudecode.StreamEvent:
				if text, ok := extractTextDelta(m); ok {
					return claudeDeltaMsg(text)
				}
				// content_block_start/stop, message_start/stop, thinking
				// deltas, input_json deltas: drain and keep looping.
			case *claudecode.ResultMessage:
				if m.IsError {
					if m.Result != nil {
						return claudeTurnErrorMsg{err: errors.New(*m.Result)}
					}
					return claudeTurnErrorMsg{err: errors.New("unknown error")}
				}
				return claudeTurnCompleteMsg{}
			}
			// *claudecode.AssistantMessage, *claudecode.UserMessage,
			// *claudecode.SystemMessage, *claudecode.RateLimitEventMessage,
			// etc: drain and keep looping - we already have the text via deltas.
		}
		return claudeChannelClosedMsg{}
	}
}

// extractTextDelta pulls the text out of a content_block_delta StreamEvent,
// ignoring other delta kinds (thinking_delta, input_json_delta, ...).
func extractTextDelta(ev *claudecode.StreamEvent) (string, bool) {
	if ev.Event["type"] != claudecode.StreamEventTypeContentBlockDelta {
		return "", false
	}
	delta, ok := ev.Event["delta"].(map[string]any)
	if !ok || delta["type"] != "text_delta" {
		return "", false
	}
	text, ok := delta["text"].(string)
	return text, ok
}

// submitPromptCmd sends the user's prompt on the persistent client. It does
// not touch the turn state - the read loop reports the results.
func submitPromptCmd(ctx context.Context, client claudecode.Client, prompt string) tea.Cmd {
	return func() tea.Msg {
		if err := client.Query(ctx, prompt); err != nil {
			return claudeTurnErrorMsg{err: fmt.Errorf("send: %w", err)}
		}
		return nil
	}
}

// disconnectClaudeCmd is used only in the quit sequence.
func disconnectClaudeCmd(client claudecode.Client) tea.Cmd {
	return func() tea.Msg {
		if client != nil {
			_ = client.Disconnect()
		}
		return nil
	}
}
