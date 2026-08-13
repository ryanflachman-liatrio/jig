package chat

import (
	"errors"

	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// claudeConnectedMsg is sent once, after Connect() and ReceiveMessages()
// both succeed.
type claudeConnectedMsg struct {
	client  claudecode.Client
	msgChan <-chan claudecode.Message
}

// claudeConnectErrMsg is sent if Connect() fails. Fatal: the app never
// retries automatically.
type claudeConnectErrMsg struct{ err error }

// claudeDeltaMsg is one incremental text delta for the turn currently
// streaming in.
type claudeDeltaMsg string

// claudeTurnCompleteMsg signals the current turn finished successfully.
type claudeTurnCompleteMsg struct{}

// claudeTurnErrorMsg signals the current turn finished with an error, or
// that submitting the prompt itself failed.
type claudeTurnErrorMsg struct{ err error }

// claudeChannelClosedMsg signals the message channel closed (subprocess
// died). Fatal: the read loop is not re-armed after this.
type claudeChannelClosedMsg struct{}

var errChannelClosed = errors.New("connection to Claude closed unexpectedly")
