package helpchat

import (
	tea "charm.land/bubbletea/v2"

	"jig/internal/harness"
	"jig/internal/interaction"
)

// ServerReadyMsg is returned once the local loopback tool server (the
// jig-facing counterpart to a spawned jig mcp-serve subprocess) is bound and
// serving, ready for the first queryCmd to reference its port/token. Its
// Serve goroutine's context derives from the Model's own ctx (the run's
// lifetime), so it tears down on its own when that ctx is cancelled — no
// separate teardown hook needed.
type ServerReadyMsg struct {
	srv *toolServer
}

// ConnectedMsg is returned by queryCmd when a fresh AcpHarness session is
// open and its prompt turn has started.
type ConnectedMsg struct {
	session harness.Session
}

// ConnectErrMsg is returned when the local tool server or the harness
// connection fails to start.
type ConnectErrMsg struct{ err error }

// DeltaMsg carries a streaming text delta from the assistant.
type DeltaMsg string

// TurnCompleteMsg is returned when the assistant's turn finishes.
type TurnCompleteMsg struct{ sessionID string }

// TurnErrorMsg is returned when the harness reports an error during
// streaming, or when the local tool server's connection to the spawned jig
// mcp-serve subprocess is lost mid-conversation (e.g. the subprocess
// crashed) — surfaced through the same error-turn UI either way.
type TurnErrorMsg struct{ err error }

// DispatchedMsg wraps an engine action ready to be re-queued through the
// monitor's root handler, identical to a keyboard-triggered gate action.
type DispatchedMsg struct{ Inner tea.Msg }

// FinalMergeGateMsg signals the monitor to show a yes/no confirmation prompt
// for the final-merge rendezvous gate. The tool handler is blocked on gateAns
// while the monitor waits for the operator's response.
type FinalMergeGateMsg struct{}

// PermRequestMsg is dispatched by the harness.PermissionFn callback when the
// agent wants to call a non-jig-help tool and needs the operator's
// allow/deny decision. AnsC receives true (allow) or false (deny).
type PermRequestMsg struct {
	ToolName string
	Input    map[string]any
	AnsC     chan<- bool
}

// QuestionRequestMsg is dispatched by the ask_user tool handler when the
// agent wants to present a structured question to the operator.
// Options is nil for a free-text answer; non-nil for a numbered choice list.
// AnsC receives the operator's answer (selected option text or typed text).
type QuestionRequestMsg struct {
	Request interaction.QuestionRequest
	AnsC    chan<- interaction.QuestionResponse
}
