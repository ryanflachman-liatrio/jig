# 11-tasks-workflow-help-agent.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/helpchat/prompt.go` | New file — `BuildSystemPrompt` builds the system prompt injected at the first turn. |
| `internal/helpchat/tools.go` | New file — `DispatchFunc` type and `BuildMcpServer` registering all 10 MCP tools. |
| `internal/helpchat/msgs.go` | New file — all Bubble Tea message types for the helpchat component. |
| `internal/helpchat/cmds.go` | New file — `connectCmd`, `queryCmd`, `waitForMessageCmd`, `waitForDispatchCmd`. |
| `internal/helpchat/model.go` | New file — `Model` struct, `New`, `Init`, `Update`, `View`, `CapturesText`. |
| `internal/helpchat/helpchat_test.go` | New file — unit tests for prompt, tool schemas, dispatch, and final-merge gate. |
| `internal/tui/monitor/monitor_model.go` | Add help fields (`helpOpen`, `helpModel`, `helpReady`, `run`, gate channels) and `SetRun`. |
| `internal/tui/monitor/keys.go` | Add `ToggleHelp keybind.Binding` (ctrl+h). |
| `internal/tui/monitor/monitor_update.go` | Handle ctrl+h, route messages to helpModel when open, handle DispatchedMsg and FinalMergeGateMsg. |
| `internal/tui/monitor/monitor_view.go` | Add compositor overlay when `helpOpen`; add ToggleHelp to footer hint. |
| `internal/tui/monitor/monitor_events.go` | Reset help state on RunFinished / RunError events. |
| `internal/tui/monitor/monitor_test.go` | Add TestToggleHelp_OpenClose and TestHelpDispatch_DispatchedMsg. |
| `internal/tui/root_update.go` | Call `m.monitor.SetRun(run)` in `openMonitor` and `startRun`. |
| `internal/runner/agent.go` | Read-only reference — patterns for `claudecode.NewClient`, `WithSdkMcpServer`, `WithResume`, `WithContinueConversation`, `CreateSDKMcpServer`. |
| `internal/tui/shared/help.go` | Read-only reference — `RenderConfirmOverlay` compositor pattern to follow for the chat overlay. |
| `internal/tui/chart/render.go` | Read-only reference — `NewCompositor` / `NewLayer` / `NewCanvas().Compose().Render()` pattern. |
| `internal/tui/monitor/msgs.go` | Read-only reference — `RecoverResponseMsg`, `RequestResetMsg`, `StopStepMsg`, `ResumeStepMsg`, `ReviewVerdictMsg`, `ReviewMessageMsg` types to dispatch. |
| `internal/engine/engine.go` | Read-only reference — `Run`, `RunSnapshot`, `Run.Snapshot()` types used by tools. |

### Notes

- Run `go build ./cmd/jig` and `go test ./...` after each parent task before moving to the next.
- Follow the `claudecode.NewTool` / `claudecode.CreateSDKMcpServer` pattern from `internal/runner/agent.go:185-242`.
- All lipgloss styles belong in `internal/tui/styles.go` under the appropriate sub-struct; never add bare `lipgloss.NewStyle()` at call sites.
- Use `internal/tui/input.go`'s `newInputTextarea` helper for the chat textarea — do not inline textarea setup.
- Use `tea.KeyPressMsg` (not the old `tea.KeyMsg`) for all key matching in tests and Update handlers.
- `go test ./...` is the authoritative test command; `go vet ./...` before committing.

---

## Tasks

### [ ] 1.0 helpchat Package: Prompt and MCP Tools

**Purpose:** Establish the `internal/helpchat/` package with the system prompt builder and all 10 MCP-registered tools. This is the pure-logic foundation with no Bubble Tea dependency — all units are testable in isolation.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/helpchat/ -run TestBuildSystemPrompt` passes — output contains workflow name, run ID, and step IDs, demonstrating prompt injection is correct.
- Test: `go test ./internal/helpchat/ -run TestMcpServerToolSchemas` passes — all 10 tools (`workflow_snapshot`, `read_step_transcript`, `read_step_result`, `read_step_output`, `recover_step`, `reset_step`, `stop_step`, `resume_step`, `resolve_review`, `send_message_to_step`) are registered with correct names and required fields.
- Test: `go test ./internal/helpchat/ -run TestDispatchFunc_RecoverStep` passes — `recover_step` handler with a fake `DispatchFunc` emits the correct `monitor.RecoverResponseMsg`.

#### 1.0 Tasks

- [ ] 1.1 Create `internal/helpchat/` directory and `prompt.go`: define `const systemPromptBase` (multi-line raw string literal) covering role, tool descriptions, and all behavior rules (read before acting, explain before acting, confirm before destructive, prefer least-destructive, call `workflow_snapshot` after every action). Implement `BuildSystemPrompt(wfName string, snap engine.RunSnapshot) string` that formats the base with injected workflow name and step IDs+statuses.
- [ ] 1.2 Create `tools.go`: define `type DispatchFunc func(tea.Msg)` and the `BuildMcpServer(run *engine.Run, runDir string, dispatch DispatchFunc, gateReq chan<- struct{}, gateAns <-chan bool) *claudecode.McpSdkServerConfig` function signature. Call `claudecode.CreateSDKMcpServer("jig-help", "1.0.0", tool1, tool2, ...)` at the end — follow the pattern in `runner/agent.go:242`.
- [ ] 1.3 Implement the 4 read-only tools using `claudecode.NewTool` in `tools.go`: `workflow_snapshot` (calls `run.Snapshot()`, marshals to JSON); `read_step_transcript(step_id string, last_n int)` (reads the last N entries from `.jig/runs/<id>/steps/<id>/transcript.jsonl` via `transcript.Read` or direct file scan); `read_step_result(step_id string)` (reads `.jig/runs/<id>/steps/<id>/result.json`); `read_step_output(step_id string)` (reads `result.json` to get `OutputPath`, then reads that file).
- [ ] 1.4 Implement the 5 fire-and-forget action tools in `tools.go`: `recover_step(step_id, action, guidance string)` → `dispatch(monitor.RecoverResponseMsg{...})`; `reset_step(step_id string)` → `dispatch(monitor.RequestResetMsg{...})`; `stop_step(step_id string)` → `dispatch(monitor.StopStepMsg{...})`; `resume_step(step_id, message string)` → `dispatch(monitor.ResumeStepMsg{...})`; `send_message_to_step(step_id, text string)` → `dispatch(monitor.ReviewMessageMsg{...})`. Each handler returns a success `McpToolResult` confirming the action was enqueued; the agent is instructed to call `workflow_snapshot` to verify the transition.
- [ ] 1.5 Implement `resolve_review(step_id, verdict string)` in `tools.go` with a dual path: for non-final-merge steps, call `dispatch(monitor.ReviewVerdictMsg{...})` and return a success result; for the final-merge step (detect by checking the step's type via `run.Snapshot()` or a step-ID comparison), write to `gateReq`, block reading `gateAns`, and return `"awaiting operator confirmation"` or `"approved"` / `"discarded"` depending on the boolean read from `gateAns`.
- [ ] 1.6 Create `helpchat_test.go` with four unit tests: `TestBuildSystemPrompt` (asserts rendered string contains workflow name and step ID); `TestMcpServerToolSchemas` (builds the server with nil run/dispatch, asserts all 10 tool names are present); `TestDispatchFunc_RecoverStep` (calls the tool handler with a captured `DispatchFunc`, asserts `monitor.RecoverResponseMsg` is emitted); `TestFinalMergeGate_ChannelRendezvous` (calls `resolve_review` on the final-merge step in a goroutine, writes `true` to gateAns, asserts the handler returns the expected result string).

---

### [ ] 2.0 helpchat Bubble Tea Model: SDK Streaming Wiring

**Purpose:** Build the full `helpchat.Model` Bubble Tea component — message types, SDK streaming commands, and the viewport/textarea layout — so a standalone model renders and streams Claude responses correctly.

#### 2.0 Proof Artifact(s)

- Build: `go build ./...` succeeds after adding `internal/helpchat/` — demonstrates the package compiles and imports resolve.
- Test: `go test ./internal/helpchat/ -run TestModel` passes — `helpchat.Model` initializes without panic, `Init()` returns a non-nil `tea.Cmd`, and `CapturesText()` returns `true` when focus is on the textarea.
- Code review: `waitForMessageCmd` returns `DeltaMsg`, `TurnCompleteMsg`, and `TurnErrorMsg` for each SDK message variant — demonstrates streaming branch coverage.

#### 2.0 Tasks

- [ ] 2.1 Create `msgs.go`: define `ConnectedMsg{client claudecode.Client; msgChan <-chan claudecode.Message}`, `ConnectErrMsg{err error}`, `DeltaMsg string`, `TurnCompleteMsg{sessionID string}`, `TurnErrorMsg{err error}`, `DispatchedMsg{Inner tea.Msg}`, `FinalMergeGateMsg{}`.
- [ ] 2.2 Create `cmds.go` with `connectCmd(ctx context.Context, run *engine.Run, runDir string, snap engine.RunSnapshot, dispatch DispatchFunc, gateReq chan<- struct{}, gateAns <-chan bool) tea.Cmd`: creates `claudecode.NewClient` with `WithSdkMcpServer("jig-help", BuildMcpServer(...))`, `WithMaxTurns(200)`, `WithModel("claude-haiku-4-5-20251001")`, calls `client.Connect(ctx)`, calls `client.ReceiveMessages(ctx)`, and returns `ConnectedMsg` on success or `ConnectErrMsg` on failure. Does NOT send a query — only prepares the connection.
- [ ] 2.3 Add `queryCmd(ctx context.Context, client claudecode.Client, sessionID string, systemPrompt string, userMsg string) tea.Cmd` to `cmds.go`: when `sessionID == ""` (first turn), calls `client.Query(ctx, systemPrompt+"\n\n"+userMsg)` (system prompt prepended to first user message since the SDK uses user-turn prompting); when `sessionID != ""`, appends `WithResume(sessionID)` and `WithContinueConversation(true)` options and calls `client.Query(ctx, userMsg)`. Returns nil (streaming is driven by `waitForMessageCmd` draining the already-established `msgChan`).
- [ ] 2.4 Add `waitForMessageCmd(msgChan <-chan claudecode.Message) tea.Cmd` and `waitForDispatchCmd(ch <-chan tea.Msg) tea.Cmd` to `cmds.go`. `waitForMessageCmd` reads one message: `*claudecode.StreamEvent` with text delta → `DeltaMsg`; `*claudecode.ResultMessage` → `TurnCompleteMsg{sessionID: m.SessionID}`; error result → `TurnErrorMsg`; channel closed → `TurnCompleteMsg`. `waitForDispatchCmd` reads one message from ch and returns `DispatchedMsg{Inner: msg}`.
- [ ] 2.5 Create `model.go`: define `type helpTurn struct{user, assistant string; isError bool}` and `type helpFocus int` with `focusInput helpFocus = iota` and `focusHistory`. Define `Model` struct with all fields from the spec (`run`, `runDir`, `client`, `msgChan`, `sessionID`, `gateReq/Ans`, `dispatchCh`, `turns`, `pending`, `streaming`, `connected`, `ready`, `focus`, `width`, `height`, `vp viewport.Model`, `ta textarea.Model`, `renderer *glamour.TermRenderer`, `snap engine.RunSnapshot`). Implement `New(run *engine.Run, runDir string, snap engine.RunSnapshot) Model` constructing defaults, and `Init() tea.Cmd` returning `connectCmd(...)`.
- [ ] 2.6 Implement `Update(msg tea.Msg) (Model, tea.Cmd)` in `model.go`: `ConnectedMsg` → store client+msgChan, set `connected=true`, return `waitForMessageCmd` + `waitForDispatchCmd`; `ConnectErrMsg` → append error turn; `DeltaMsg` → append to `pending`, re-queue `waitForMessageCmd`; `TurnCompleteMsg` → commit pending as assistant turn, store sessionID, clear streaming; `TurnErrorMsg` → append error turn, clear streaming; `DispatchedMsg` → return as a `tea.Cmd` (wraps `msg.Inner` so the monitor model handles it); `FinalMergeGateMsg` → return as a `tea.Cmd`; `tea.KeyPressMsg` enter (when `focusInput` and not streaming) → fire `queryCmd`; tab → toggle focus between `focusInput` and `focusHistory`; `tea.WindowSizeMsg` → resize viewport and textarea.
- [ ] 2.7 Implement `View(width, height int, focused bool) string` in `model.go`: compute `histH = height * 80 / 100`, `inputH = height - histH`; render all `turns` through the glamour renderer into the viewport (`focusHistory` border = primary, else blurred); render `ta` into the textarea area; wrap the combined string in `shared.Theme.Help.Box` with title "Help Agent" and hint `ctrl+h or esc · close`. Implement `CapturesText() bool` returning `m.focus == focusInput`. Add `TestModelInit` to `helpchat_test.go`.

---

### [ ] 3.0 Monitor Integration: Open/Close and Dispatch Bridge

**Purpose:** Wire `helpchat.Model` into the monitor — new fields, `ctrl+h` key binding, open/close toggle, message routing, and the fire-and-forget dispatch bridge that re-queues agent-dispatched actions identically to keyboard gate actions.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor/ -run TestToggleHelp_OpenClose` passes — `ctrl+h` sets `helpOpen=true`, a second `ctrl+h` and `esc` both set it back to `false`.
- Test: `go test ./internal/tui/monitor/ -run TestHelpDispatch_DispatchedMsg` passes — injecting `helpchat.DispatchedMsg{Inner: monitor.RecoverResponseMsg{...}}` produces a `tea.Cmd` that returns the inner message when called.
- Build: `go build ./cmd/jig` succeeds after this task — demonstrates monitor compiles with the new fields and wiring.

#### 3.0 Tasks

- [ ] 3.1 Add `ToggleHelp keybind.Binding` to the `monitorKeys` struct in `keys.go`. In `defaultMonitorKeys()`, bind it as `keybind.NewBinding(keybind.WithKeys("ctrl+h"), keybind.WithHelp("ctrl+h", "help agent"))`.
- [ ] 3.2 Add six fields to `Model` in `monitor_model.go`: `helpOpen bool`, `helpModel helpchat.Model`, `helpReady bool` (true once `Init()` has been called for this run), `run *engine.Run`, `helpGateReq <-chan struct{}`, `helpGateAns chan<- bool`. Add `func (m *Model) SetRun(run *engine.Run)` that sets `m.run = run`.
- [ ] 3.3 Add `toggleHelpChat() (Model, tea.Cmd)` to `monitor_update.go`: if `!m.helpOpen`, create `gateReq := make(chan struct{}, 1)` and `gateAns := make(chan bool, 1)`, build `m.helpModel = helpchat.New(m.run, m.RunDir, snap)` (snap from `m.run.Snapshot()` if run is non-nil, else use a zero snapshot), store channel pointers, call `m.helpModel.Init()` to get cmd, set `m.helpReady = true`, `m.helpOpen = true`. If already ready, just toggle `m.helpOpen`. On close, set `m.helpOpen = false`.
- [ ] 3.4 In `monitor_update.go`'s `Update`, handle `tea.KeyPressMsg` with `ctrl+h` in both the `updateSteps` and `updateTranscript` paths (and the gate-focused path when `helpOpen` is false): call `toggleHelpChat()` and return. When `m.run == nil` (journal-replayed run), open the modal with a static "Help agent unavailable for completed runs" turn pre-populated in `helpModel.turns` instead of connecting to the SDK.
- [ ] 3.5 When `m.helpOpen`, route all `tea.KeyPressMsg` messages (and `esc` to close) to `m.helpModel.Update(msg)`. Also forward all `helpchat.*Msg` type-switched messages to `m.helpModel.Update(msg)`. Collect and return all cmds from helpModel's Update alongside any monitor cmds. `esc` when `helpOpen` should close the modal (set `helpOpen=false`) before passing to helpModel.
- [ ] 3.6 In `monitor_update.go`'s `Update`, handle `helpchat.DispatchedMsg`: return `func() tea.Msg { return msg.Inner }` as a `tea.Cmd`. This re-queues the agent-dispatched action through the root model's existing handler, identical to a keyboard-triggered gate action. Also return `waitForDispatchCmd(m.helpModel.dispatchCh)` so the next dispatch is picked up.
- [ ] 3.7 Add `TestToggleHelp_OpenClose` and `TestHelpDispatch_DispatchedMsg` to `monitor_test.go`. `TestToggleHelp_OpenClose`: create a `monitor.Model` with a nil run, send `tea.KeyPressMsg{Code: tea.KeyCtrlH}`, assert `helpOpen==true`; send again, assert `helpOpen==false`; send once more to reopen, send `tea.KeyPressMsg{Code: tea.KeyEscape}`, assert `helpOpen==false`. `TestHelpDispatch_DispatchedMsg`: inject a `helpchat.DispatchedMsg{Inner: RecoverResponseMsg{RunID: "r", StepID: "s", Action: "retry"}}` into the monitor's Update; assert the returned cmd is non-nil and produces `RecoverResponseMsg` when called.

---

### [ ] 4.0 Compositor Overlay Rendering and Root Wiring

**Purpose:** Render the chat modal as a centered compositor layer above the live monitor view (80% width × 80% height), add the `ctrl+h` hint to the footer, and wire `SetRun()` in `root_update.go` so live runs have an engine handle while journal-replayed runs show a static unavailable message.

#### 4.0 Proof Artifact(s)

- TUI screenshot: `ctrl+h` during a live run shows the chat modal centered over the monitor with the step list visible behind it — demonstrates compositor overlay.
- TUI screenshot: pressing `ctrl+h` on a journal-replayed (completed) run shows "Help agent unavailable for completed runs" instead of a chat input — demonstrates the nil-run guard.
- Code review: `hintLabel()` in `monitor_view.go` includes the `ToggleHelp` binding in the footer hint — demonstrates key discoverability.

#### 4.0 Tasks

- [ ] 4.1 In `hintLabel()` in `monitor_view.go`, add `m.keys.ToggleHelp` to the `default` (focusSteps) branch's `shared.HintString(...)` call, after the existing step lifecycle keys. Also add it to the `focusTranscript` branch so it is always visible.
- [ ] 4.2 In the `View` method of `monitor_view.go` (or at the end of the `View` function), add a compositor block after the existing base string is computed: `if m.helpOpen { overlayW := m.width * 80 / 100; overlayH := m.height * 80 / 100; chatStr := m.helpModel.View(overlayW, overlayH, true); x := (m.width - overlayW) / 2; y := (m.height - overlayH) / 2; comp := lipgloss.NewCompositor(lipgloss.NewLayer(base), lipgloss.NewLayer(chatStr).X(x).Y(y).Z(1)); return lipgloss.NewCanvas(m.width, m.height).Compose(comp).Render() }`. Follow the identical pattern in `shared/help.go:46-50`.
- [ ] 4.3 In `openMonitor` in `root_update.go`, after building or reusing the monitor model, add: `if run, ok := m.handles[runID]; ok { m.monitor.SetRun(run) }`. For journal-replayed runs the handle is absent and `SetRun` is not called (run stays nil, triggering the "unavailable" message path in `toggleHelpChat`).
- [ ] 4.4 In `startRun` in `root_update.go`, after creating `m.monitor = monitor.New(run.ID)` and setting `RunDir`, add `m.monitor.SetRun(run)`.

---

### [ ] 5.0 Final-Merge Gate, Session Persistence, and Lifecycle Teardown

**Purpose:** Implement the monitor side of the rendezvous channel for `resolve_review` on the final-merge step, verify conversation history survives open/close cycles without a reset, and ensure the session resets cleanly when the run ends.

#### 5.0 Proof Artifact(s)

- Test: `go test ./internal/helpchat/ -run TestFinalMergeGate_ChannelRendezvous` passes (written in task 1.6) — the tool handler blocks and unblocks correctly.
- Test: `go test ./...` passes with zero failures — demonstrates no regressions across the full test suite.
- TUI smoke test (manual): closing and reopening the chat modal during a live run preserves the full conversation history — demonstrates session persistence across open/close cycles.
- TUI smoke test (manual): after a run completes, reopening the monitor for a new run does not carry over the old `helpModel` state — demonstrates lifecycle teardown.

#### 5.0 Tasks

- [ ] 5.1 Add a `waitForGateReqCmd(gateReq <-chan struct{}) tea.Cmd` function to `cmds.go` in `internal/helpchat/`: blocks on `gateReq` and returns `FinalMergeGateMsg{}`. The monitor fires this cmd once per session, after the help model connects, so it is ready to receive a rendezvous signal from the tool handler.
- [ ] 5.2 In `monitor_update.go`, handle `helpchat.FinalMergeGateMsg`: if the monitor's existing `inputKindFinalMerge` gate entry logic can be reused, enqueue a synthetic `inputKindFinalMerge` entry with the final-merge request pulled from `m.run.Snapshot()`. Otherwise, add a dedicated `inputKindHelpFinalMerge` gate entry that renders the same y/d prompt. When the operator answers, write the result to `m.helpGateAns` (instead of the normal engine dispatch) and remove the gate entry.
- [ ] 5.3 In `monitor_events.go`, in the handlers for `engine.RunFinished` and `engine.RunError` events, add: `m.helpModel = helpchat.Model{}; m.helpReady = false; m.helpOpen = false` to reset the help session when the run ends.
- [ ] 5.4 Confirm session persistence in `toggleHelpChat()`: the helper only initializes `helpModel` when `!m.helpReady`. When `m.helpReady == true` and `!m.helpOpen`, it sets `helpOpen = true` and returns nil (no `Init()` call, no new channels) — verifying that turns, sessionID, and the established connection are preserved across open/close cycles.
- [ ] 5.5 Run `go build ./cmd/jig` and `go test ./...` to confirm zero build errors and zero test failures. Fix any compilation issues in the helpchat ↔ monitor package boundary (import cycles, unexported fields) before marking complete.
