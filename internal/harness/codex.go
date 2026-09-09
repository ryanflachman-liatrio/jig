package harness

import (
	"context"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
)

// CodexHarness drives Codex through the ACP adapter. It uses the operator's
// existing Codex CLI login and shares ACP event normalization with AcpHarness.
type CodexHarness struct{}

var codexACPConfig = semanticACPConfigPolicy{model: true, effort: true}

func NewCodexHarness() *CodexHarness { return &CodexHarness{} }

func (*CodexHarness) Name() string { return "codex" }

func (*CodexHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapPermissionCallback, CapSessionResume, CapStructuredOutput, CapPartialStreaming)
}

func (*CodexHarness) PreviewPrompt(spec SessionSpec) string {
	return appendSchemaPrompt(spec.Prompt, spec.Schema)
}

func (h *CodexHarness) Open(ctx context.Context, spec SessionSpec) (Session, error) {
	events := make(chan Event, 32)
	sess := &acpSession{events: events, hasSchema: spec.Schema != nil, schema: spec.Schema, partial: spec.Partial}

	var decide acp.Decider
	if spec.Permission != nil {
		decide = func(tc acpsdk.ToolCallUpdate) bool {
			return spec.Permission(toolCallName(tc), toolCallInput(tc)).Allow
		}
	}

	conn, err := acp.ConnectCodexWithDiagnostics(ctx, decide, func(ev acp.Event) {
		sess.onEvent(ev)
	}, spec.DiagnosticsDir)
	if err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	sess.conn = conn

	var sessionID string
	if spec.Resume != "" {
		if err := conn.LoadSession(ctx, spec.Cwd, spec.Resume); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("codex: %w", err)
		}
		sessionID = spec.Resume
	} else {
		sessionID, err = conn.NewSession(ctx, spec.Cwd)
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("codex: %w (log in with codex login first)", err)
	}
	if err := codexACPConfig.Apply(ctx, conn, sessionID, spec); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("codex: %w", err)
	}
	conn.ConfigurationCompleted()
	events <- Event{Type: EventSessionID, SessionID: sessionID}

	go sess.run(ctx, sessionID, spec.Prompt)
	return sess, nil
}

var _ Harness = (*CodexHarness)(nil)
