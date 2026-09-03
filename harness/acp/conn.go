package acp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// Conn is a live connection to a spawned
// `npx -y @zed-industries/claude-code-acp@latest` process, split out of Run's
// Initialize/NewSession/Prompt sequence so a caller can drive a session and
// receive session/update events as they arrive rather than waiting for a
// whole turn to finish and reading them back afterward.
type Conn struct {
	cmd           *exec.Cmd
	rpc           *acpsdk.ClientSideConnection
	client        *Client
	sessionConfig map[string][]acpsdk.SessionConfigOption
	diagnostics   *diagnosticLog

	// ProtocolVersion is the version the adapter reported during Initialize.
	ProtocolVersion int
	// SupportsLoadSession is negotiated during Initialize.
	SupportsLoadSession bool
}

// Connect spawns the adapter and performs the ACP Initialize handshake,
// failing fast (rather than hanging) if npx or the adapter package is
// unavailable. decide and onUpdate are wired into the connection's Client
// exactly as Run does; onUpdate additionally fires synchronously as each
// event is captured, for callers that stream rather than batch.
func Connect(ctx context.Context, decide Decider, onUpdate func(Event), elicit Elicitor) (*Conn, error) {
	npxPath, err := exec.LookPath("npx")
	if err != nil {
		return nil, fmt.Errorf("npx not found on PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, npxPath, "-y", "@agentclientprotocol/claude-agent-acp@0.70.0")
	// Capture stderr rather than forwarding it to os.Stderr: npm/npx prints
	// deprecation warnings and progress lines that would corrupt a TUI's
	// alt-screen display. The buffer is included in the error message if
	// Initialize fails, preserving diagnostics without polluting the terminal.
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	// Put npx and all its children (including the Node.js adapter) in their own
	// process group so Close() can kill the entire tree with one signal. Without
	// this, Kill() only sends SIGKILL to the npx PID; Node.js inherits the
	// stderr pipe write-end and keeps it open, which wedges the copy goroutine
	// started by cmd.Stderr (a non-*os.File writer) and blocks cmd.Wait() forever.
	configureProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude-code-acp: %w", err)
	}

	client := &Client{Decide: decide, OnUpdate: onUpdate, Elicit: elicit}
	rpc := acpsdk.NewClientSideConnection(client, stdin, stdout)

	initResp, err := rpc.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion:    acpsdk.ProtocolVersionNumber,
		ClientCapabilities: clientCapabilities(elicit),
	})
	if err != nil {
		_ = killProcess(cmd)
		_ = cmd.Wait()
		if msg := strings.TrimSpace(stderrBuf.String()); msg != "" {
			return nil, fmt.Errorf("initialize: %w\nadapter output: %s", err, msg)
		}
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return &Conn{cmd: cmd, rpc: rpc, client: client, ProtocolVersion: int(initResp.ProtocolVersion)}, nil
}

// ConnectCodex spawns the Codex ACP adapter and performs the ACP Initialize
// handshake. The adapter reads the operator's existing Codex CLI login; jig
// deliberately does not provide credentials or select an authentication method.
func ConnectCodex(ctx context.Context, decide Decider, onUpdate func(Event)) (*Conn, error) {
	return ConnectCodexWithDiagnostics(ctx, decide, onUpdate, "")
}

// ConnectCodexWithDiagnostics is ConnectCodex with an optional artifact
// directory. The artifacts live beside the step transcript so they survive an
// ungraceful parent-process exit without polluting the TUI's alt screen.
func ConnectCodexWithDiagnostics(ctx context.Context, decide Decider, onUpdate func(Event), diagnosticsDir string) (*Conn, error) {
	npxPath, err := exec.LookPath("npx")
	if err != nil {
		return nil, fmt.Errorf("npx not found on PATH: %w", err)
	}
	diagnostics, err := newDiagnosticLog(diagnosticsDir, nil)
	if err != nil {
		return nil, fmt.Errorf("open diagnostics: %w", err)
	}
	closeDiagnostics := true
	defer func() {
		if closeDiagnostics {
			_ = diagnostics.Close()
		}
	}()

	cmd := exec.CommandContext(ctx, npxPath, "-y", "@agentclientprotocol/codex-acp@1.6.2")
	diagnostics.Event("adapter_start", map[string]any{"adapter": "@agentclientprotocol/codex-acp@1.6.2"})
	cmd.Stderr = diagnostics.StderrWriter()
	configureProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		diagnostics.Event("adapter_start_failed", errorFields(err))
		return nil, fmt.Errorf("start codex-acp: %w", err)
	}
	diagnostics.setRootPID(cmd.Process.Pid)
	diagnostics.Event("adapter_started", nil)

	client := &Client{Decide: decide, OnUpdate: onUpdate}
	rpc := acpsdk.NewClientSideConnection(client, stdin, stdout)
	initResp, err := rpc.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	if err != nil {
		_ = killProcess(cmd)
		waitErr := cmd.Wait()
		fields := map[string]any{"rpc_error": errorKind(err), "adapter_exit_error": errorKind(waitErr)}
		addExitStatus(fields, cmd)
		diagnostics.Event("initialize_failed", fields)
		if msg := diagnostics.StderrTail(); msg != "" {
			return nil, fmt.Errorf("initialize: %w\nadapter output: %s", err, msg)
		}
		return nil, fmt.Errorf("initialize: %w", err)
	}
	diagnostics.Event("initialized", map[string]any{"protocol_version": initResp.ProtocolVersion})
	closeDiagnostics = false

	return &Conn{
		cmd:                 cmd,
		rpc:                 rpc,
		client:              client,
		diagnostics:         diagnostics,
		ProtocolVersion:     int(initResp.ProtocolVersion),
		SupportsLoadSession: initResp.AgentCapabilities.LoadSession,
	}, nil
}

func clientCapabilities(elicit Elicitor) acpsdk.ClientCapabilities {
	caps := acpsdk.ClientCapabilities{}
	if elicit != nil {
		caps.Elicitation = &acpsdk.ElicitationCapabilities{
			Form: &acpsdk.ElicitationFormCapabilities{},
		}
	}
	return caps
}

// NewSession creates a new ACP session rooted at cwd and returns its id.
func (c *Conn) NewSession(ctx context.Context, cwd string) (string, error) {
	c.diagnostic("new_session_started", map[string]any{"cwd": cwd})
	resp, err := c.rpc.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: cwd, McpServers: []acpsdk.McpServer{}})
	if err != nil {
		c.diagnostic("new_session_failed", errorFields(err))
		return "", fmt.Errorf("new session: %w", err)
	}
	sessionID := string(resp.SessionId)
	c.setSessionConfig(sessionID, resp.ConfigOptions)
	c.diagnostic("new_session_finished", map[string]any{"session_id": sessionID, "config_options": len(resp.ConfigOptions)})
	return sessionID, nil
}

// LoadSession restores a previously-created ACP session into this connection.
func (c *Conn) LoadSession(ctx context.Context, cwd, sessionID string) error {
	if !c.SupportsLoadSession {
		return fmt.Errorf("adapter did not advertise session/load")
	}
	resp, err := c.rpc.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
		SessionId:  acpsdk.SessionId(sessionID),
	})
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	c.setSessionConfig(sessionID, resp.ConfigOptions)
	return nil
}

// SetSelectConfig applies an advertised select configuration option by its
// adapter-provided ID.
func (c *Conn) SetSelectConfig(ctx context.Context, sessionID, configID, value string) error {
	if value == "" {
		return nil
	}
	options, ok := c.selectOptions(sessionID, configID)
	if !ok {
		return fmt.Errorf("adapter did not advertise session config option %q", configID)
	}
	if !containsConfigValue(options, value) {
		return fmt.Errorf("%s %q is unavailable; adapter advertises %s", configID, value, strings.Join(options, ", "))
	}

	resp, err := c.rpc.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			ConfigId:  acpsdk.SessionConfigId(configID),
			SessionId: acpsdk.SessionId(sessionID),
			Value:     acpsdk.SessionConfigValueId(value),
		},
	})
	if err != nil {
		c.diagnostic("set_config_failed", errorFields(err))
		return fmt.Errorf("set %s %q: %w", configID, value, err)
	}
	c.setSessionConfig(sessionID, resp.ConfigOptions)
	c.diagnostic("set_config_finished", map[string]any{"config_id": configID, "value": value})
	return nil
}

// ConfigurationCompleted records the boundary after the harness has applied
// its session configuration policy.
func (c *Conn) ConfigurationCompleted() {
	c.diagnostic("configuration_completed", nil)
}

// SetSelectConfigByCategory applies a select option found by ACP's semantic
// category. Categories are optional in ACP, so adapters that omit one fail
// closed rather than being guessed from an adapter-specific option ID.
func (c *Conn) SetSelectConfigByCategory(
	ctx context.Context,
	sessionID string,
	category acpsdk.SessionConfigOptionCategory,
	value string,
) error {
	if value == "" {
		return nil
	}
	configID, ok := c.selectOptionIDByCategory(sessionID, category)
	if !ok {
		return fmt.Errorf("adapter did not advertise a %q session config option", category)
	}
	return c.SetSelectConfig(ctx, sessionID, configID, value)
}

func (c *Conn) setSessionConfig(sessionID string, options []acpsdk.SessionConfigOption) {
	if c.sessionConfig == nil {
		c.sessionConfig = make(map[string][]acpsdk.SessionConfigOption)
	}
	c.sessionConfig[sessionID] = options
}

func (c *Conn) selectOptions(sessionID, configID string) ([]string, bool) {
	for _, option := range c.sessionConfig[sessionID] {
		if option.Select == nil || string(option.Select.Id) != configID {
			continue
		}
		return configOptionValues(option.Select.Options), true
	}
	return nil, false
}

func (c *Conn) selectOptionIDByCategory(sessionID string, category acpsdk.SessionConfigOptionCategory) (string, bool) {
	for _, option := range c.sessionConfig[sessionID] {
		if option.Select == nil || option.Select.Category == nil || *option.Select.Category != category {
			continue
		}
		return string(option.Select.Id), true
	}
	return "", false
}

func configOptionValues(options acpsdk.SessionConfigSelectOptions) []string {
	var values []string
	if options.Ungrouped != nil {
		for _, option := range *options.Ungrouped {
			values = append(values, string(option.Value))
		}
	}
	if options.Grouped != nil {
		for _, group := range *options.Grouped {
			for _, option := range group.Options {
				values = append(values, string(option.Value))
			}
		}
	}
	return values
}

func containsConfigValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Prompt sends one user turn and blocks until the agent finishes responding,
// returning the stop reason. session/update notifications are delivered to
// the onUpdate callback passed to Connect as they arrive on the connection's
// own read loop, concurrently with this call being in flight — Prompt
// blocking does not delay event delivery.
func (c *Conn) Prompt(ctx context.Context, sessionID, text string) (acpsdk.StopReason, error) {
	start := time.Now()
	c.diagnostic("prompt_started", map[string]any{"session_id": sessionID, "prompt_bytes": len(text)})
	resp, err := c.rpc.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(sessionID),
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(text)},
	})
	if err != nil {
		c.diagnostic("prompt_failed", errorFields(err))
		return "", fmt.Errorf("prompt: %w", err)
	}
	c.diagnostic("prompt_finished", map[string]any{
		"session_id":  sessionID,
		"stop_reason": resp.StopReason,
		"elapsed_ms":  time.Since(start).Milliseconds(),
	})
	return resp.StopReason, nil
}

// PermissionRequests returns every session/request_permission request seen
// on this connection so far.
func (c *Conn) PermissionRequests() []acpsdk.RequestPermissionRequest {
	return c.client.PermissionRequests()
}

// Close terminates the adapter subprocess and all its children.
func (c *Conn) Close() error {
	c.diagnostic("adapter_close_requested", nil)
	_ = killProcess(c.cmd)
	err := c.cmd.Wait()
	fields := errorFields(err)
	if fields == nil {
		fields = make(map[string]any)
	}
	addExitStatus(fields, c.cmd)
	c.diagnostic("adapter_exited", fields)
	if c.diagnostics != nil {
		_ = c.diagnostics.Close()
	}
	return err
}

func (c *Conn) diagnostic(event string, fields map[string]any) {
	if c.diagnostics != nil {
		c.diagnostics.Event(event, fields)
	}
}

func errorFields(err error) map[string]any {
	if err == nil {
		return nil
	}
	return map[string]any{"error_type": errorKind(err), "error": err.Error()}
}

func errorKind(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}

func addExitStatus(fields map[string]any, cmd *exec.Cmd) {
	if cmd != nil && cmd.ProcessState != nil {
		fields["exit_code"] = cmd.ProcessState.ExitCode()
	}
}

// ConnectCursor spawns `cursor-agent acp` and performs the ACP Initialize +
// Authenticate handshake. Cursor requires an explicit authenticate call (with
// methodId "cursor_login") before any session can be created; this is the only
// protocol-level difference from Connect (which spawns the Zed npx adapter and
// requires no auth step). If CURSOR_API_KEY is set in the environment Cursor
// treats itself as already authenticated, but calling Authenticate is still
// safe (it is a no-op when already authenticated).
func ConnectCursor(ctx context.Context, decide Decider, onUpdate func(Event)) (*Conn, error) {
	agentPath, err := exec.LookPath("cursor-agent")
	if err != nil {
		return nil, fmt.Errorf("cursor-agent not found on PATH (run: cursor-agent --version to verify install): %w", err)
	}

	cmd := exec.CommandContext(ctx, agentPath, "acp")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cursor-agent acp: %w", err)
	}

	client := &Client{Decide: decide, OnUpdate: onUpdate}
	rpc := acpsdk.NewClientSideConnection(client, stdin, stdout)

	initResp, err := rpc.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	if _, err := rpc.Authenticate(ctx, acpsdk.AuthenticateRequest{MethodId: "cursor_login"}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("cursor authenticate: %w (run: cursor-agent login)", err)
	}

	return &Conn{cmd: cmd, rpc: rpc, client: client, ProtocolVersion: int(initResp.ProtocolVersion)}, nil
}
