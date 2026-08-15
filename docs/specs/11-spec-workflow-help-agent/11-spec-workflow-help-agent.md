# 11-spec-workflow-help-agent.md

## Introduction/Overview

This feature adds a **workflow help agent** — a Claude-backed chat modal that opens in the run monitor via `ctrl+h`. The agent can read workflow state and step transcripts on demand, explain what happened during a run, and dispatch recovery actions (retry, skip, reset, resolve reviews, etc.) on behalf of the operator. The primary goal is to eliminate the current manual diagnosis loop where operators must read raw transcripts and hand-compose recovery actions without contextual guidance.

## Goals

- Provide a persistent, streaming chat modal that overlays the run monitor without replacing it.
- Give the agent read access to all live run state (step statuses, transcripts, results, output artifacts) via 10 MCP-registered tools.
- Allow the agent to dispatch every gate-strip action (recover, reset, stop, resume, resolve review, send message) as first-class tool calls.
- Preserve conversation history across open/close cycles for the lifetime of a single run.
- Protect the one truly irreversible action (final-merge gate approval) behind a TUI channel-rendezvous confirmation.

## User Stories

- **As a workflow operator**, I want to ask "why did step X fail?" and receive a reasoned answer drawn from the actual transcript and result files so that I don't have to manually read raw JSONL to diagnose a failure.
- **As a workflow operator**, I want to say "retry the failing step" and have the agent confirm its plan, then dispatch the retry action, so that I can recover from failures without remembering the exact gate-strip key sequence.
- **As a workflow operator**, I want the chat modal to overlay the monitor (not replace it) so that I can observe the step list changing in real time while the agent acts.
- **As a workflow operator**, I want the conversation to persist when I close and reopen the modal within the same run so that I can close it to read a transcript and then resume the conversation.
- **As a workflow operator**, I want the agent to require my explicit confirmation before taking irreversible actions (reset, abort, final-merge) so that I remain in control of destructive operations.

## Demoable Units of Work

### Unit 1: Help modal opens and displays a streaming response

**Purpose:** Proves the Bubble Tea modal and SDK streaming wiring are functional end to end — an operator can open the modal and receive a live response from Claude.

**Functional Requirements:**
- The system shall open a centered chat overlay (80% terminal width × 80% terminal height) when the operator presses `ctrl+h` in the monitor's Steps or Transcript views.
- The system shall close the modal when the operator presses `ctrl+h` or `esc` while the modal is open.
- The system shall render the modal using a lipgloss compositor layer above the existing monitor view so the step list remains visible behind the modal.
- The system shall display chat history in an 80%-height viewport and an input textarea in the remaining 20%.
- The system shall stream Claude's response as text deltas appear, with `enter` submitting the message and `alt+enter` inserting a newline.
- The system shall display an inline error inside the modal (textarea stays open) if the SDK call fails, allowing the operator to retry or rephrase.
- The system shall advertise the `ctrl+h` hotkey in the monitor's hint footer.

**Proof Artifacts:**
- TUI screenshot: modal opens centered over the monitor when `ctrl+h` is pressed, demonstrating compositor overlay.
- TUI recording: typing a message and pressing `enter` produces streaming text in the history viewport before the turn completes, demonstrating real-time streaming.

### Unit 2: Agent reads live run state via MCP tools

**Purpose:** Proves the 10-tool MCP server is wired and callable — the agent can fetch step statuses, transcripts, results, and output artifacts from the running engine and on-disk files.

**Functional Requirements:**
- The system shall register the following 10 tools via `claudecode.WithSdkMcpServer`: `workflow_snapshot`, `read_step_transcript`, `read_step_result`, `read_step_output`, `recover_step`, `reset_step`, `stop_step`, `resume_step`, `resolve_review`, `send_message_to_step`.
- The `workflow_snapshot` tool shall return a JSON snapshot of all step statuses via `run.Snapshot()`.
- The `read_step_transcript` tool shall return the last N entries from `.jig/runs/<id>/steps/<id>/transcript.jsonl`.
- The `read_step_result` tool shall return the contents of `.jig/runs/<id>/steps/<id>/result.json`.
- The `read_step_output` tool shall return the contents of the file at `result.OutputPath`.
- The system shall inject a system prompt at the first turn that includes the workflow name, run ID, and all step IDs with their current statuses.
- The system shall use `WithMaxTurns(200)` to effectively uncap the tool-call loop.

**Proof Artifacts:**
- Unit test `TestMcpServerToolSchemas`: asserts all 10 tools are registered with correct names and required fields.
- Unit test `TestBuildSystemPrompt`: asserts the rendered system prompt contains the workflow name, run ID, and step IDs.
- TUI recording: asking "why did step X fail?" causes the agent to call `workflow_snapshot` then `read_step_result` and reference the actual error from the result file in its answer, demonstrating live data access.

### Unit 3: Agent dispatches recovery actions to the monitor

**Purpose:** Proves the fire-and-forget dispatch bridge — when the agent calls an action tool, the matching typed monitor message is enqueued and the engine processes it identically to a keyboard-triggered gate action.

**Functional Requirements:**
- The action tools (`recover_step`, `reset_step`, `stop_step`, `resume_step`, `send_message_to_step`) shall enqueue a typed monitor message (`monitor.RecoverResponseMsg`, `monitor.RequestResetMsg`, `monitor.StopStepMsg`, `monitor.ResumeStepMsg`, `monitor.ReviewMessageMsg`) via a `DispatchFunc` callback without blocking the tool handler.
- The monitor shall drain the dispatch channel and re-queue messages as `tea.Cmd` so the root model handles them identically to keyboard-triggered gate actions.
- The `resolve_review` tool shall enqueue `monitor.ReviewVerdictMsg` for non-final-merge steps via the same dispatch path.
- The system shall instruct the agent in the system prompt to call `workflow_snapshot` after dispatching any action to confirm the step status transition.
- The system shall instruct the agent in the system prompt to explain its plan and ask for operator confirmation before calling `reset_step` or any abort action, describing the blast radius before proceeding.

**Proof Artifacts:**
- Unit test `TestDispatchFunc_RecoverStep`: `recover_step` tool handler with a fake `DispatchFunc` emits the correct `monitor.RecoverResponseMsg`.
- Monitor unit test `TestHelpDispatch_DispatchedMsg`: injecting `helpchat.DispatchedMsg{Inner: monitor.RecoverResponseMsg{...}}` into the model produces a cmd that returns the inner message when called.
- TUI smoke test: asking "retry the failing step" → agent states its plan → operator confirms → step transitions to `running` in the Steps panel visible behind the modal.

### Unit 4: Final-merge gate and session teardown

**Purpose:** Proves the rendezvous gate for the one irreversible action and the lifecycle contract — the agent blocks on operator confirmation for final-merge, and the session resets cleanly when the run ends.

**Functional Requirements:**
- The `resolve_review` tool shall detect when `step_id` matches the final-merge step and block on a rendezvous channel (`gateReq`/`gateAns`) rather than using the fire-and-forget dispatch path.
- The tool handler shall return `"awaiting operator confirmation"` to Claude while blocking, causing the agent to wait for the next turn.
- The monitor shall present a TUI gate strip entry for final-merge confirmation when `helpchat.FinalMergeGateMsg` is received, and wire the operator's yes/no response to `helpGateAns`.
- The system shall preserve the `helpchat.Model` (including full conversation history) across open/close cycles for the lifetime of a single run.
- The system shall reset `helpModel`, `helpReady`, and `helpOpen` to zero values when `engine.RunFinished` or `engine.RunError` is received in `monitor_events.go`.
- For journal-replayed runs (no live engine handle), `ctrl+h` shall display a static "Help agent unavailable for completed runs" message instead of connecting.

**Proof Artifacts:**
- Unit test `TestFinalMergeGate_ChannelRendezvous`: `resolve_review` on the final-merge step writes to `gateReq`, blocks until `gateAns` is written, and returns the expected result string.
- Monitor unit test `TestToggleHelp_OpenClose`: `ctrl+h` sets `helpOpen=true`; a second `ctrl+h` sets it back to `false`; `esc` also closes it.
- TUI smoke test: closing and reopening the modal during an active run preserves the full conversation history.

## Non-Goals (Out of Scope)

1. **Completed-run analysis**: The help agent connects only to live runs with an engine handle. Journal-replayed runs show a static unavailable message — no read-only agent for historical runs.
2. **Cost limits or rate caps**: No per-session token budget or turn cap (beyond `WithMaxTurns(200)` as an effective uncap). This is an operator tool; the operator is present.
3. **Parallel agent sessions**: One help agent per run. No support for opening multiple simultaneous help sessions.
4. **Theme switching or modal size customization**: The 80%/80% overlay size and the 80%/20% history/textarea split are fixed constants; no runtime adjustment.
5. **Agent-initiated proactive messages**: The agent responds only when the operator sends a message; it does not monitor the run and push unsolicited alerts.
6. **Persisting conversation across runs**: The session is tied to a single run. When the run ends, the session is torn down and no history is preserved.

## Design Considerations

The modal renders as a compositor overlay using `lipgloss.NewCompositor` layered above the existing monitor view. The step list and transcript panels remain visible (and continuing to update) behind the modal. The overlay uses the same `shared.Theme.Help.Box` border style as the existing `?` overlay box, with title "Help Agent" and hint `ctrl+h or esc · close`.

Inside the modal:
- Top 80%: scrollable `viewport.Model` rendering glamour-formatted assistant turns and verbatim user turns.
- Bottom 20%: `textarea.Model` for input, styled via the existing `newInputTextarea` helper.
- `tab` cycles focus between the viewport and the textarea, consistent with the rest of the monitor.
- Streaming text deltas appear in the viewport in real time before the turn completes.
- API errors render inline in the viewport; the textarea remains open.

## Repository Standards

- The new package lives at `internal/helpchat/` following the `internal/` package-per-concern convention.
- All lipgloss styles are added to the appropriate sub-struct in `internal/tui/styles.go` and set in `DefaultTheme()` using existing color tokens — no bare `lipgloss.NewStyle()` calls at the call site.
- The input textarea uses the shared `newInputTextarea` helper (`internal/tui/input.go`), not inline setup.
- `enter` submits, `alt+enter` inserts a newline — consistent with all other gate strip textareas.
- Comments explain the non-obvious "why" only (e.g., why fire-and-forget avoids Bubble Tea event loop conflicts; why the final-merge gate blocks on a channel).
- All new packages have unit tests following the table-driven pattern in `internal/workflow/workflow_test.go`.
- `go build ./cmd/jig` and `go test ./...` must pass clean after every step.

## Technical Considerations

**SDK and client reuse:** The agent uses `severity1/claude-agent-sdk-go` (already a dependency via `runner/agent.go` patterns). Session continuity is achieved via `WithResume(sessionID)` + `WithContinueConversation(true)` on subsequent turns. The `sessionID` is captured from the first `ResultMessage` and stored in `helpchat.Model`.

**MCP server registration:** Tools are registered via `claudecode.WithSdkMcpServer("jig-help", BuildMcpServer(...))`. `BuildMcpServer` takes `*engine.Run`, `runDir string`, `DispatchFunc`, `gateReq chan<- struct{}`, and `gateAns <-chan bool`.

**Fire-and-forget dispatch:** Action tool handlers write to a buffered `chan tea.Msg` (`dispatchCh`). A `waitForDispatchCmd` tea.Cmd drains this channel and returns `helpchat.DispatchedMsg`. The monitor handles `DispatchedMsg` by re-queuing the inner message as a `tea.Cmd`, so the root model processes it identically to a keyboard gate action. This avoids blocking inside a tool handler, which would fight Bubble Tea's event loop.

**Final-merge rendezvous:** `resolve_review` detects the final-merge step by matching `step_id` against the known final-merge step ID. It sends on `gateReq`, blocks on `gateAns`, and returns a result string indicating the operator's decision. The monitor sends `helpchat.FinalMergeGateMsg` when it sees the request on `gateReq`, shows a gate strip entry, and wires the response to `helpGateAns`.

**Streaming:** The `waitForMessageCmd` tea.Cmd drains `msgChan <-chan claudecode.Message` and returns `helpchat.DeltaMsg` (text delta), `helpchat.TurnCompleteMsg` (with session ID), or `helpchat.TurnErrorMsg`.

**Model field model:** `helpchat.Model` holds the SDK client, message channel, session ID, dispatch channel, rendezvous channels, conversation turns (immutable once appended), streaming delta, viewport/textarea, glamour renderer, and focus state. It follows the same value-receiver-with-shared-maps pattern as the monitor (render cache is a map, so mutations in value receivers persist).

**Gate conflict idempotency:** If both the operator and the agent dispatch the same action concurrently (e.g., both press retry), the engine handles idempotency — a duplicate action on an already-recovering step is a no-op.

**Model selection:** The help agent uses the model specified in `helpModel` constant (default: `claude-haiku-4-5-20251001` for cost efficiency, or configurable). Given this is an operator tool present during live runs, a capable model is preferred over minimum cost.

## Security Considerations

- The help agent has access to full step transcripts, result files, and output artifacts — these may contain secrets, credentials, or sensitive intermediate outputs. The agent operates only locally (no data leaves the machine beyond the Claude API call, which is subject to the operator's existing API key usage).
- The system prompt instructs the agent to confirm before calling destructive tools (`reset_step`, abort-equivalent actions) — this is a behavioral guard, not a cryptographic one. The operator remains responsible for confirming agent actions.
- The final-merge gate adds a structural TUI block for the one action that cannot be undone by a subsequent step — this is the only hard gate.
- No API keys or run artifacts are committed to the repository. The `runDir` path (`.jig/runs/<id>/`) is outside the working tree by design.

## Success Metrics

1. **Diagnostic accuracy**: The agent correctly identifies the failing step and root cause (from transcript/result) for 90%+ of common failure modes (non-zero exit, schema validation failure, timeout) when asked in a smoke test.
2. **Action fidelity**: Every action tool dispatch (`recover_step`, `reset_step`, `stop_step`, `resume_step`, `resolve_review`, `send_message_to_step`) produces the correct typed monitor message as verified by unit tests.
3. **Build and test cleanliness**: `go build ./cmd/jig` and `go test ./...` pass with zero failures after the full implementation.
4. **Session persistence**: Closing and reopening the modal during a live run preserves the full conversation history with no regressions in the Steps panel rendering behind the modal.

## Open Questions

1. **Model selection**: The plan specifies `WithModel(helpModel)` but does not pin the constant. A reasonable default is `claude-haiku-4-5-20251001` (low cost, fast) unless the operator's workflow requires deeper reasoning — `claude-sonnet-4-6` would be stronger for complex multi-step diagnosis. This can be made configurable via a `[defaults]` or environment variable without changing the spec.
2. **Final-merge step detection**: The plan says the tool handler "detects if `step_id` matches the final-merge step" — the exact mechanism (step type check vs. step ID comparison vs. a flag on `engine.Run`) should be confirmed when reading the engine package during implementation, as `engine.Run` may already expose this.
