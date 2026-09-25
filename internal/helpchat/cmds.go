package helpchat

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"jig/internal/agentcfg"
	"jig/internal/engine"
	"jig/internal/harness"
)

const helpModelID = "claude-haiku-4-5-20251001"

// helpchatHarness is the narrow seam helpchat needs from harness.Harness
// (open one session, read its events), mirroring runner/monitor.go's
// monitorHarness pattern so this package depends on jig's own
// harness.SessionSpec/harness.Session types rather than any ACP wire type.
// *harness.AcpHarness satisfies this directly.
type helpchatHarness interface {
	Open(ctx context.Context, spec harness.SessionSpec) (harness.Session, error)
}

func newDefaultHarness() helpchatHarness { return harness.NewAcpHarness() }

// startServerCmd binds the local loopback tool server (BuildToolHandlers
// dispatched against live *engine.Run state) and starts serving it in the
// background for the lifetime of this help-chat session — the jig-facing
// counterpart to each turn's spawned jig mcp-serve subprocess. Returns
// ServerReadyMsg on success, or ConnectErrMsg if the local bind fails.
func startServerCmd(
	ctx context.Context,
	run *engine.Run,
	runDir string,
	dispatch DispatchFunc,
	gateReq chan<- struct{},
	gateAns <-chan bool,
) tea.Cmd {
	return func() tea.Msg {
		srv, err := newToolServer(BuildToolHandlers(run, runDir, dispatch, gateReq, gateAns))
		if err != nil {
			return ConnectErrMsg{err: fmt.Errorf("help agent: %w", err)}
		}
		go func() {
			// srv.Serve derives its own context from ctx, so it tears down
			// on its own once the run's context is cancelled — no separate
			// teardown call needed. A non-nil error here instead means the
			// connection to jig mcp-serve dropped mid-conversation (or
			// never came up) while ctx was still live — most likely a
			// crashed subprocess. Routed through dispatch/DispatchedMsg like
			// every other tool-triggered message so it reaches Update as a
			// normal TurnErrorMsg, matching how any other turn failure
			// surfaces.
			if err := srv.Serve(ctx); err != nil {
				dispatch(TurnErrorMsg{err: fmt.Errorf("help agent: local tool server: %w", err)})
			}
		}()
		return ServerReadyMsg{srv: srv}
	}
}

// queryCmd opens a fresh AcpHarness session for one turn (following the same
// per-turn-fresh-session pattern runner/agent.go and MonitorAdapter already
// use). When sessionID is set, SessionSpec.Resume carries conversation
// continuity across turns (AcpHarness's CapSessionResume). MCPServers names
// jig mcp-serve so the agent spawns it and forwards its jig-help tool calls
// back to toolSrv over the loopback connection toolSrv is already serving.
func queryCmd(
	ctx context.Context,
	newHarness func() helpchatHarness,
	toolSrv *toolServer,
	dispatch DispatchFunc,
	sessionID string,
	systemPrompt string,
	userMsg string,
) tea.Cmd {
	return func() tea.Msg {
		prompt := userMsg
		if sessionID == "" {
			prompt = systemPrompt + "\n\n" + userMsg
		}
		spec := harness.SessionSpec{
			Prompt:     prompt,
			Model:      helpModelID,
			Agent:      agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: helpModelID}, MaxTurns: 200},
			Partial:    true,
			Resume:     sessionID,
			Permission: buildPermissionFn(dispatch),
			MCPServers: []harness.McpServerStdio{{
				Name:    "jig-help",
				Command: "jig",
				Args:    []string{"mcp-serve"},
				Env: map[string]string{
					"JIG_MCP_PORT":  toolSrv.Port(),
					"JIG_MCP_TOKEN": toolSrv.Token(),
				},
			}},
		}
		h := newHarness()
		sess, err := h.Open(ctx, spec)
		if err != nil {
			return TurnErrorMsg{err: fmt.Errorf("connect: %w", err)}
		}
		return ConnectedMsg{session: sess}
	}
}

// waitForMessageCmd reads one event from msgChan and returns the appropriate
// helpchat message type. The model re-queues this cmd after each delta until
// TurnCompleteMsg or TurnErrorMsg signals end-of-turn.
func waitForMessageCmd(msgChan <-chan harness.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-msgChan
		if !ok {
			return TurnCompleteMsg{}
		}
		switch ev.Type {
		case harness.EventTextDelta:
			return DeltaMsg(ev.Text)
		case harness.EventResult:
			if ev.IsError {
				return TurnErrorMsg{err: fmt.Errorf("%s", resultErrText(ev))}
			}
			return TurnCompleteMsg{sessionID: ev.SessionID}
		}
		// EventText (the finalized duplicate of EventTextDelta's chunks —
		// this modal only needs the live-typing preview), EventThinking,
		// EventToolUse/EventToolResult, EventAssistantEnd/EventUserEnd, and
		// EventSessionID all keep draining without producing a UI message,
		// matching the pre-ACP path's handling of SystemMessage/
		// AssistantMessage/UserMessage/RateLimitEventMessage.
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

func resultErrText(ev harness.Event) string {
	if ev.ErrText != "" {
		return ev.ErrText
	}
	return "unknown agent error"
}

// buildPermissionFn returns a harness.PermissionFn that pre-approves jig-help
// tools (trusted Go handlers with no shell side-effects, namespaced by the
// "jig-help" MCP server name) and surfaces every other tool call to the
// operator via the chat modal gate.
func buildPermissionFn(dispatch DispatchFunc) harness.PermissionFn {
	return func(toolName string, input map[string]any) harness.Decision {
		if strings.HasPrefix(toolName, "mcp__jig-help__") {
			return harness.Decision{Allow: true}
		}
		ansC := make(chan bool, 1)
		dispatch(PermRequestMsg{ToolName: toolName, Input: input, AnsC: ansC})
		if allow := <-ansC; allow {
			return harness.Decision{Allow: true}
		}
		return harness.Decision{Allow: false, Reason: "denied by operator"}
	}
}
