# Plan: Workflow Help Agent Modal

## Context

Running a jig workflow today requires the operator to manually diagnose failures by reading step transcripts and then dispatch recovery actions from the gate strip. There is no way to ask "why did this fail?" or "what should I do?" and get a reasoned answer tied to the actual run state.

This plan adds a **workflow help agent** — a Claude-backed chat modal that opens in the monitor page via `ctrl+h`. The agent can read workflow state and transcripts on demand, explain what happened, and take control actions (retry, skip, reset, resolve reviews, etc.) on behalf of the operator.

---

## Settled Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| SDK | Agent SDK + MCP server (`severity1/claude-agent-sdk-go`) | No new dependency; reuses `runner/agent.go` patterns |
| Session lifecycle | Preserved across open/close; torn down on run end / monitor exit | Operator closes to read transcript, reopens to continue |
| Gate conflict | Coexist — duplicate engine action becomes a stale no-op | Engine handles idempotency gracefully |
| Action feedback | Fire-and-forget; agent calls `workflow_snapshot` to confirm | Blocking inside tool handler fights Bubble Tea's event loop |
| Tool count | 10 tools (see list below), full access | Operator present; system prompt enforces confirmation |
| Final merge | Structural channel-rendezvous TUI gate | Only truly irreversible action; structural block is warranted |
| Other destructive | System-prompt confirmation only (reset, abort) | Agent instructed to describe blast radius + ask before calling |
| Submit key | `enter` submit / `alt+enter` newline | Consistent with gate strip textareas |
| Streaming | Yes — text deltas via `ReceiveMessages` | Expected for a chat interface |
| Tool loop cap | None — `WithMaxTurns(200)` effectively uncaps | Operator trusts the agent to resolve when done |
| Modal size | 80% terminal width/height | Scales with terminal; compositor overlay |
| Internal split | 80% history viewport / 20% textarea | Maximize readable context |
| Tab in modal | Cycles between history viewport and textarea | Keeps operator in help context |
| API errors | Inline error; textarea stays open for retry | Non-destructive; operator can resend or rephrase |
| Cost | No cap | Operator tool, operator present |
| Hotkey | `ctrl+h` | Confirmed free across entire TUI keybind set |

---

## New Package: `internal/helpchat/`

### Tools (10 total)

| Tool | Action |
|---|---|
| `workflow_snapshot` | `run.Snapshot()` → JSON of all step statuses |
| `read_step_transcript(step_id, last_n)` | Last N entries from `.jig/runs/<id>/steps/<id>/transcript.jsonl` |
| `read_step_result(step_id)` | Contents of `.jig/runs/<id>/steps/<id>/result.json` |
| `read_step_output(step_id)` | Contents of the file at `result.OutputPath` (actual step artifact) |
| `recover_step(step_id, action, guidance)` | → `monitor.RecoverResponseMsg` |
| `reset_step(step_id)` | → `monitor.RequestResetMsg` |
| `stop_step(step_id)` | → `monitor.StopStepMsg` |
| `resume_step(step_id, message)` | → `monitor.ResumeStepMsg` |
| `resolve_review(step_id, verdict)` | → `monitor.ReviewVerdictMsg` |
| `send_message_to_step(step_id, text)` | → `monitor.ReviewMessageMsg` |

**Special case — `resolve_review` on final-merge gate**: The tool handler detects if `step_id` matches the final-merge step, then blocks on a rendezvous channel until the operator confirms via a TUI gate strip entry. Claude receives `"awaiting operator confirmation"` as the tool result and waits for the next turn.

All other action tools enqueue a typed monitor message via `DispatchFunc` (a callback to a buffered `chan tea.Msg`). The help model drains this channel and re-queues messages as `tea.Cmd` → the root handles them identically to keyboard-triggered gate actions.

### `tools.go`

```go
type DispatchFunc func(tea.Msg)

// BuildMcpServer registers all ten tools and returns a claudecode.McpSdkServerConfig.
// Registered via: claudecode.WithSdkMcpServer("jig-help", BuildMcpServer(...))
func BuildMcpServer(
    run     *engine.Run,
    runDir  string,
    dispatch DispatchFunc,
    gateReq chan<- struct{},   // written when final-merge gate is needed
    gateAns <-chan bool,       // read by tool handler to get operator answer
) *claudecode.McpSdkServerConfig
```

### `prompt.go`

```go
const systemPromptBase = `...`  // multiline raw string literal

func BuildSystemPrompt(wfName string, snap engine.RunSnapshot) string
```

System prompt sections:
1. **Role**: operator-side assistant for a jig workflow run
2. **Runtime context** (injected at first turn): workflow name + step IDs with current statuses
3. **Tool descriptions** with when-to-use guidance
4. **Behavior rules**:
   - Read before acting (`workflow_snapshot` + `read_step_result` first)
   - Explain before acting (state findings and plan before calling action tools)
   - Confirm before destructive actions (reset/abort): describe blast radius, ask "Do you want to proceed?", wait for yes
   - For final merge: state what will happen, call `resolve_review` — a TUI gate will appear for the operator
   - Prefer least-destructive: retry before reset, skip before abort
   - Call `workflow_snapshot` after dispatching any action to confirm the transition

### `model.go`

```go
type Model struct {
    run    *engine.Run
    runDir string

    // SDK client (reused across turns via WithResume + WithContinueConversation)
    client    claudecode.Client
    msgChan   <-chan claudecode.Message
    sessionID string   // set after first ResultMessage; used for WithResume on next turn

    // final-merge gate rendezvous
    gateReq chan struct{}
    gateAns chan bool

    // dispatch: MCP tool handlers write here; waitForDispatchCmd drains
    dispatchCh chan tea.Msg

    // conversation
    turns   []helpTurn   // immutable once appended
    pending string       // current streaming delta

    streaming bool
    connected bool
    ready     bool

    // focus / layout
    focus  helpFocus   // focusHistory | focusInput
    width  int
    height int
    vp     viewport.Model
    ta     textarea.Model
    renderer *glamour.TermRenderer
    snap   engine.RunSnapshot   // injected at first turn
}

type helpTurn struct {
    user      string
    assistant string
    isError   bool
}

type helpFocus int
const (
    focusInput   helpFocus = iota
    focusHistory
)

func New(run *engine.Run, runDir string, snap engine.RunSnapshot) Model
func (m Model) Init() tea.Cmd   // fires connectCmd (establish SDK client, register MCP server)
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd)
func (m Model) View(width, height int, focused bool) string
func (m Model) CapturesText() bool   // true when focusInput
```

### `msgs.go`

```go
type ConnectedMsg    struct{ client claudecode.Client; msgChan <-chan claudecode.Message }
type ConnectErrMsg   struct{ err error }
type DeltaMsg        string
type TurnCompleteMsg struct{ sessionID string }
type TurnErrorMsg    struct{ err error }
type DispatchedMsg   struct{ Inner tea.Msg }   // engine action ready to re-queue
type FinalMergeGateMsg struct{}                // signal monitor to show yes/no gate
```

### `cmds.go`

```go
// connectCmd: creates SDK client with MCP server + WithMaxTurns(200) + WithModel(helpModel)
// Does NOT start a query — just prepares the client for use.
func connectCmd(ctx context.Context, run *engine.Run, runDir string, snap engine.RunSnapshot,
    dispatch DispatchFunc, gateReq chan<- struct{}, gateAns <-chan bool) tea.Cmd

// queryCmd: sends the user's message (first turn: system prompt + message via client.Query;
// subsequent turns: client.QueryWithSession + WithResume(sessionID) + WithContinueConversation(true))
func queryCmd(ctx context.Context, client claudecode.Client, sessionID string,
    systemPrompt string, userMsg string) tea.Cmd

// waitForMessageCmd: drains msgChan; returns DeltaMsg / TurnCompleteMsg / TurnErrorMsg
func waitForMessageCmd(msgChan <-chan claudecode.Message) tea.Cmd

// waitForDispatchCmd: drains dispatchCh; returns DispatchedMsg
func waitForDispatchCmd(ch <-chan tea.Msg) tea.Cmd
```

---

## Monitor Changes (`internal/tui/monitor/`)

### `monitor_model.go` — new fields

```go
helpOpen  bool
helpModel helpchat.Model
helpReady bool            // true once Init() called this session
run       *engine.Run     // set by root via SetRun()

// final-merge gate rendezvous channels (shared with helpModel's MCP server)
helpGateReq <-chan struct{}
helpGateAns chan<- bool
```

New method:
```go
func (m *Model) SetRun(run *engine.Run)
```

### `keys.go` — new binding

```go
ToggleHelp keybind.Binding   // ctrl+h · "help agent"
```

### `monitor_update.go`

1. `ctrl+h` in `updateSteps` and `updateTranscript` → `toggleHelpChat()`
2. When `helpOpen`: route `tea.KeyPressMsg` and non-key messages to `m.helpModel.Update(msg)`; collect returned cmds
3. Handle `helpchat.*Msg` types by delegating to `m.helpModel.Update(msg)`
4. Handle `helpchat.DispatchedMsg`:
   ```go
   case helpchat.DispatchedMsg:
       return m, func() tea.Msg { return msg.Inner }
   ```
5. Handle `helpchat.FinalMergeGateMsg` → inject a `inputKindFinalMerge`-style gate entry; wire its answer back to `helpGateAns`
6. `toggleHelpChat()` helper: on open, create `helpModel` if first time (passing gateReq/gateAns channels), fire `Init()`, set focus to `helpOpen` focus; on close, restore transcript focus

### `monitor_view.go`

After computing `base` (the full panels + footer string), apply compositor when `helpOpen`:

```go
if m.helpOpen {
    overlayW := m.width * 80 / 100
    overlayH := m.height * 80 / 100
    chatStr  := m.helpModel.View(overlayW, overlayH, true)
    x := (m.width  - overlayW) / 2
    y := (m.height - overlayH) / 2
    comp := lipgloss.NewCompositor(
        lipgloss.NewLayer(base),
        lipgloss.NewLayer(chatStr).X(x).Y(y).Z(1),
    )
    return lipgloss.NewCanvas(m.width, m.height).Compose(comp).Render()
}
```

The `helpModel.View` wraps its content in `shared.Theme.Help.Box` (same style as the `?` overlay box) with title "Help Agent" and hint `ctrl+h or esc · close`.

### `hintLabel()` — `focusSteps` default branch

Append `m.keys.ToggleHelp` to the hint string so `ctrl+h` is advertised.

### Session teardown

On `engine.RunFinished` or `engine.RunError` events already received in `monitor_events.go`, if `helpReady`, reset `helpModel`, `helpReady`, and `helpOpen` to zero values.

---

## Root Wiring (`internal/tui/root_update.go`)

In `openMonitor` and `startRun`, after constructing the monitor:

```go
if run, ok := m.handles[runID]; ok {
    m.monitor.SetRun(run)
}
```

For journal-replayed runs (no live handle), `run` is nil; `ctrl+h` shows "Help agent unavailable for completed runs" as a static message in the modal instead of connecting.

---

## Implementation Sequence

1. `internal/helpchat/prompt.go` + test — pure data, no external deps
2. `internal/helpchat/tools.go` + test — tool schemas + handlers with fake `DispatchFunc`
3. `internal/helpchat/msgs.go` + `cmds.go` — SDK streaming wiring (patterns from `runner/agent.go`)
4. `internal/helpchat/model.go` — full Bubble Tea model
5. `monitor_model.go` — new fields + `SetRun` + `toggleHelpChat`
6. `monitor_update.go` — key handling + message routing + `FinalMergeGateMsg` handling
7. `monitor_view.go` — Compositor overlay rendering
8. `root_update.go` — `SetRun` calls in `openMonitor` / `startRun`

---

## Verification

**Unit tests** (`internal/helpchat/helpchat_test.go`):
- `TestBuildSystemPrompt` — rendered string contains workflow name, run ID, step IDs
- `TestMcpServerToolSchemas` — all ten tools have correct names and required fields
- `TestDispatchFunc_RecoverStep` — `recover_step` handler with fake `DispatchFunc` emits correct `monitor.RecoverResponseMsg`
- `TestFinalMergeGate_ChannelRendezvous` — `resolve_review` on final-merge step writes to `gateReq`, blocks until `gateAns` is written, returns expected result string

**Monitor tests** (`internal/tui/monitor/monitor_test.go`):
- `TestToggleHelp_OpenClose` — `ctrl+h` opens (`helpOpen=true`), second `ctrl+h` closes, `esc` also closes
- `TestHelpDispatch_DispatchedMsg` — inject `helpchat.DispatchedMsg{Inner: monitor.RecoverResponseMsg{...}}` → returned cmd produces the inner msg when called

**Manual smoke test**:
1. `go run ./cmd/jig` → open a workflow run → wait for a step to fail
2. Press `ctrl+h` → verify the chat modal appears centered over the monitor
3. Type "why did step X fail?" + `enter` → verify streaming response referencing actual error from `read_step_result`
4. Ask "retry it" → verify agent states its plan, asks confirmation, dispatches `recover_step` after yes
5. Verify step transitions to `running` in the Steps panel visible behind the modal
6. Press `ctrl+h` or `esc` → modal closes, monitor fully restored
7. Reopen with `ctrl+h` → conversation history preserved

**Build check**: `go build ./cmd/jig` and `go test ./...` must pass clean.
