package helpchat

import (
	tea "charm.land/bubbletea/v2"
	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// ConnectedMsg is returned by connectCmd when the SDK client is ready.
type ConnectedMsg struct {
	client  claudecode.Client
	msgChan <-chan claudecode.Message
}

// ConnectErrMsg is returned by connectCmd when the SDK connection fails.
type ConnectErrMsg struct{ err error }

// DeltaMsg carries a streaming text delta from the assistant.
type DeltaMsg string

// TurnCompleteMsg is returned when the assistant's turn finishes.
type TurnCompleteMsg struct{ sessionID string }

// TurnErrorMsg is returned when the SDK reports an error during streaming.
type TurnErrorMsg struct{ err error }

// DispatchedMsg wraps an engine action ready to be re-queued through the
// monitor's root handler, identical to a keyboard-triggered gate action.
type DispatchedMsg struct{ Inner tea.Msg }

// FinalMergeGateMsg signals the monitor to show a yes/no confirmation prompt
// for the final-merge rendezvous gate. The tool handler is blocked on gateAns
// while the monitor waits for the operator's response.
type FinalMergeGateMsg struct{}
