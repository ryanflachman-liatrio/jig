package helpchat

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/engine"
)

const helpModelID = "claude-haiku-4-5-20251001"

// connectCmd establishes the initial SDK connection for pre-flight error detection.
// It does NOT send a query — the user's first message fires queryCmd which
// reconnects with the full option set. Returns ConnectedMsg on success or
// ConnectErrMsg when the SDK is unreachable (API key missing, etc.).
func connectCmd(
	ctx context.Context,
	run *engine.Run,
	runDir string,
	snap engine.RunSnapshot,
	dispatch DispatchFunc,
	gateReq chan<- struct{},
	gateAns <-chan bool,
) tea.Cmd {
	return func() tea.Msg {
		mcpServer := BuildMcpServer(run, runDir, dispatch, gateReq, gateAns)
		client := claudecode.NewClient(
			claudecode.WithSdkMcpServer("jig-help", mcpServer),
			claudecode.WithMaxTurns(200),
			claudecode.WithModel(helpModelID),
			claudecode.WithIncludePartialMessages(true),
		)
		if err := client.Connect(ctx); err != nil {
			return ConnectErrMsg{err: fmt.Errorf("help agent connect: %w", err)}
		}
		msgChan := client.ReceiveMessages(ctx)
		_ = snap // snap is used by BuildSystemPrompt at query time
		return ConnectedMsg{client: client, msgChan: msgChan}
	}
}

// queryCmd creates a fresh SDK client for each turn (following the same pattern
// as runner/agent.go). When sessionID is set, it uses WithResume +
// WithContinueConversation for conversation continuity across turns. Returns
// ConnectedMsg{newClient, newMsgChan} so the model updates its stored connection.
func queryCmd(
	ctx context.Context,
	run *engine.Run,
	runDir string,
	dispatch DispatchFunc,
	gateReq chan<- struct{},
	gateAns <-chan bool,
	sessionID string,
	systemPrompt string,
	userMsg string,
) tea.Cmd {
	return func() tea.Msg {
		mcpServer := BuildMcpServer(run, runDir, dispatch, gateReq, gateAns)
		opts := []claudecode.Option{
			claudecode.WithSdkMcpServer("jig-help", mcpServer),
			claudecode.WithMaxTurns(200),
			claudecode.WithModel(helpModelID),
			claudecode.WithIncludePartialMessages(true),
		}
		if sessionID != "" {
			opts = append(opts,
				claudecode.WithResume(sessionID),
				claudecode.WithContinueConversation(true),
			)
		}

		client := claudecode.NewClient(opts...)
		if err := client.Connect(ctx); err != nil {
			return TurnErrorMsg{err: fmt.Errorf("connect: %w", err)}
		}
		msgChan := client.ReceiveMessages(ctx)

		prompt := userMsg
		if sessionID == "" {
			prompt = systemPrompt + "\n\n" + userMsg
		}
		if err := client.Query(ctx, prompt); err != nil {
			_ = client.Disconnect()
			return TurnErrorMsg{err: fmt.Errorf("query: %w", err)}
		}
		return ConnectedMsg{client: client, msgChan: msgChan}
	}
}

// waitForMessageCmd reads one message from msgChan and returns the appropriate
// helpchat message type. The model re-queues this cmd after each delta until
// TurnCompleteMsg or TurnErrorMsg signals end-of-turn.
func waitForMessageCmd(msgChan <-chan claudecode.Message) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-msgChan
		if !ok {
			return TurnCompleteMsg{}
		}
		switch m := msg.(type) {
		case *claudecode.StreamEvent:
			if delta, ok := streamTextDelta(m); ok {
				return DeltaMsg(delta)
			}
			// Non-text delta (thinking, input_json) — keep draining.
			return waitForMessageCmd(msgChan)()
		case *claudecode.ResultMessage:
			if m.IsError {
				return TurnErrorMsg{err: fmt.Errorf("%s", resultErrText(m))}
			}
			return TurnCompleteMsg{sessionID: m.SessionID}
		}
		// SystemMessage, AssistantMessage, UserMessage, RateLimitEventMessage —
		// skip and keep draining.
		return waitForMessageCmd(msgChan)()
	}
}

// WaitForDispatchCmd blocks on the dispatch channel and wraps the received
// message in DispatchedMsg so the monitor can re-queue it as a tea.Cmd.
// Fire-and-forget dispatch avoids blocking inside a tool handler against
// Bubble Tea's event loop.
func WaitForDispatchCmd(ch <-chan tea.Msg) tea.Cmd {
	return waitForDispatchCmd(ch)
}

func waitForDispatchCmd(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg := <-ch
		return DispatchedMsg{Inner: msg}
	}
}

// waitForGateReqCmd blocks on gateReq and returns FinalMergeGateMsg when the
// tool handler signals that the operator's TUI confirmation is needed.
func waitForGateReqCmd(gateReq <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-gateReq
		return FinalMergeGateMsg{}
	}
}

// streamTextDelta extracts a text delta from a content_block_delta StreamEvent.
func streamTextDelta(ev *claudecode.StreamEvent) (string, bool) {
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

func resultErrText(m *claudecode.ResultMessage) string {
	if m.Result != nil && *m.Result != "" {
		return *m.Result
	}
	if len(m.Errors) > 0 {
		return m.Errors[0]
	}
	return "unknown agent error"
}
