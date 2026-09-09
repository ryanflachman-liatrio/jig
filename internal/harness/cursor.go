package harness

import (
	"context"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
)

// CursorHarness drives Cursor over the Agent Client Protocol by spawning
// `cursor-agent acp`. The wire protocol is identical to AcpHarness (Zed); the
// only differences are the spawn command and an extra Authenticate RPC after
// Initialize (handled by acp.ConnectCursor). All session and event logic is
// shared with AcpHarness via the same acpSession type.
type CursorHarness struct{}

func NewCursorHarness() *CursorHarness { return &CursorHarness{} }

func (*CursorHarness) Name() string { return "cursor" }

func (*CursorHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapPermissionCallback, CapUserQuestion, CapSessionResume, CapStructuredOutput, CapPartialStreaming)
}

func (*CursorHarness) PreviewPrompt(spec SessionSpec) string {
	return appendSchemaPrompt(spec.Prompt, spec.Schema)
}

// Open spawns cursor-agent acp, authenticates, opens a session at spec.Cwd,
// and starts the prompt turn in the background. Rejects capability-gated
// SessionSpec fields this harness does not advertise.
func (h *CursorHarness) Open(ctx context.Context, spec SessionSpec) (Session, error) {
	events := make(chan Event, 32)
	sess := &acpSession{events: events, hasSchema: spec.Schema != nil, schema: spec.Schema, partial: spec.Partial}

	var decide acp.Decider
	if spec.Permission != nil {
		decide = func(tc acpsdk.ToolCallUpdate) bool {
			return spec.Permission(toolCallName(tc), toolCallInput(tc)).Allow
		}
	}

	conn, err := acp.ConnectCursor(ctx, decide, func(ev acp.Event) {
		sess.onEvent(ev)
	}, newCursorQuestionHandler(spec.Question), spec.DiagnosticsDir)
	if err != nil {
		return nil, fmt.Errorf("cursor: %w", err)
	}
	sess.conn = conn

	var sessionID string
	if spec.Resume != "" {
		if err := conn.LoadSession(ctx, spec.Cwd, spec.Resume); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("cursor: %w", err)
		}
		sessionID = spec.Resume
	} else {
		sessionID, err = conn.NewSession(ctx, spec.Cwd)
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("cursor: %w", err)
	}
	events <- Event{Type: EventSessionID, SessionID: sessionID}

	go sess.run(ctx, sessionID, spec.Prompt)
	return sess, nil
}

var _ Harness = (*CursorHarness)(nil)
