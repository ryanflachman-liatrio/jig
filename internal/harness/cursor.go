package harness

import (
	"context"
	"fmt"

	"jig/harness/acp"
	"jig/internal/agentcfg"
)

// CursorHarness drives Cursor over the Agent Client Protocol by spawning
// `cursor-agent acp`. The wire protocol is identical to AcpHarness (Zed); the
// only differences are the spawn command and an extra Authenticate RPC after
// Initialize (handled by acp.ConnectCursor). All session and event logic is
// shared with AcpHarness via the same acpSession type.
type CursorHarness struct{}

// cursorACPConfig applies the model and mode through ACP's semantic
// selectors. Cursor advertises its model selector with exact model names.
// CursorAgent has no effort key; Cursor exposes reasoning parameters under its
// own category, not thought_level.
var cursorACPConfig = cursorACPConfigPolicy{}

func NewCursorHarness() *CursorHarness { return &CursorHarness{} }

func (*CursorHarness) Name() string { return "cursor" }

func (*CursorHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapPermissionCallback, CapUserQuestion, CapSessionResume, CapStructuredOutput, CapPartialStreaming)
}

func (*CursorHarness) PreviewPrompt(spec SessionSpec) string {
	return appendSchemaPrompt(spec.Prompt, spec.Schema)
}

// Open spawns cursor-agent acp, authenticates, opens a session at spec.Cwd,
// and starts the prompt turn in the background. Capability gating happens
// before Open, in the runner (runner.AgentExecutor.Execute).
func (h *CursorHarness) Open(ctx context.Context, spec SessionSpec) (Session, error) {
	sessionOpts, err := sessionOptions(cursorACPConfig, spec)
	if err != nil {
		return nil, fmt.Errorf("cursor: %w", err)
	}
	events := make(chan Event, 32)
	sess := &acpSession{events: events, hasSchema: spec.Schema != nil, schema: spec.Schema, partial: spec.Partial}

	decide := permissionDecider(agentcfg.BackendCursor, &sess.calls, spec.Permission)

	// Without a question function no handler is installed, and the client
	// answers Cursor questions as skipped.
	var question acp.CursorQuestionHandler
	if spec.Question != nil {
		question = newCursorQuestionHandler(spec.Question)
	}
	conn, err := acp.ConnectCursor(ctx, decide, func(ev acp.Event) {
		sess.onEvent(ev)
	}, question, spec.DiagnosticsDir)
	if err != nil {
		return nil, fmt.Errorf("cursor: %w", err)
	}
	sess.conn = conn

	var sessionID string
	if spec.Resume != "" {
		if err := conn.LoadSession(ctx, spec.Cwd, spec.Resume, sessionOpts...); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("cursor: %w", err)
		}
		sessionID = spec.Resume
	} else {
		sessionID, err = conn.NewSession(ctx, spec.Cwd, sessionOpts...)
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("cursor: %w", err)
	}
	if err := cursorACPConfig.Apply(ctx, conn, sessionID, spec); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("cursor: %w", err)
	}
	conn.ConfigurationCompleted()
	events <- Event{Type: EventSessionID, SessionID: sessionID}

	go sess.run(ctx, sessionID, spec.Prompt)
	return sess, nil
}

var _ Harness = (*CursorHarness)(nil)
