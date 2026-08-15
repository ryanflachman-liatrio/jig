# Plan: helpchat permission prompts and structured questions

## Problem

The helpchat SDK client (`internal/helpchat`) creates a `claudecode.NewClient` with no
`WithCanUseTool` callback and no `WithPermissionMode`. The SDK's comment says "If no
callback is set, all tool requests are denied (secure default)." In practice the behaviour
depends on the SDK version, but in either case jig has no way to surface the decision to
the user inside the TUI. When the `claude` CLI subprocess tries to call a jig-help MCP
tool, the permission prompt either gets denied silently or leaks to the terminal — both
invisible while the alt-screen TUI is active.

There is also no `ask_user` tool registered for the help agent. Right now the agent must
ask questions via conversational prose; a structured ask-with-options pattern is impossible.

## Goal

1. Every tool the help agent calls must go through an in-TUI confirmation or be
   pre-approved by a static allow-list — no prompts ever reach the raw terminal.
2. The help agent can call `ask_user` to present a structured question (free text or
   option list) to the operator inside the chat modal.
3. All interaction happens inside the chat modal, not the gate strip (the gate strip is
   for workflow-step events, not the help agent).

## Two interaction kinds

| Kind | Trigger | Blocks | Rendered as |
|------|---------|--------|-------------|
| **Permission request** | `WithCanUseTool` callback fires for any tool call | SDK goroutine blocked | "Tool X wants to run — allow?" + y/n |
| **Structured question** | Agent calls `ask_user` MCP tool | MCP handler goroutine blocked | Question text + option list or free-text field |

Both use the same rendezvous pattern: the blocking goroutine writes a request (with an
embedded reply channel) to `dispatchCh`; the TUI receives it, renders a gate UI, and sends
the answer back through the embedded channel.

## Architecture

### Channel rendezvous

```
SDK/MCP goroutine                           Bubble Tea event loop
─────────────────                           ──────────────────────
ansC := make(chan T, 1)
dispatch(XxxRequestMsg{..., AnsC: ansC})
select {                    →   dispatchCh ─→ DispatchedMsg ─→ model.pendingGate set
  case v := <-ansC: ...     ←   user presses key ─→ ansC <- answer; pendingGate = nil
  case <-ctx.Done(): ...
}
```

This is the same pattern already used for `FinalMergeGate` in `gateReq`/`gateAns`.
The difference is that we embed the answer channel in the request struct itself so
multiple pending requests could queue in theory (though in practice only one is
outstanding at a time because the agent is blocked until answered).

### `pendingGateEntry` (new type in `model.go`)

```go
type gateKind int
const (
    gateKindPerm     gateKind = iota // WithCanUseTool fired
    gateKindQuestion                 // ask_user tool called
)

type pendingGateEntry struct {
    kind     gateKind
    // permission fields
    toolName string
    input    map[string]any
    permAnsC chan<- bool
    // question fields
    question string
    options  []string  // nil → free-text answer
    qAnsC    chan<- string
    // UI state
    selected int    // cursor for option list
    textBuf  string // typed text for free-text answer
}
```

The `Model` gains one new field:

```go
pendingGate *pendingGateEntry // nil when no interaction pending
```

Because `Model` uses value receivers but shares `dispatchCh` (a reference type), storing
`pendingGate` as a pointer is safe; a write inside any method persists because maps and
channels are reference types, but a pointer field reassignment on a value copy does NOT
persist. `pendingGate` must therefore be updated only on the returned copy, which is
already how every other field works in `Update`.

## File-by-file changes

### 1. `helpchat/msgs.go` — new message types

Add:

```go
// PermRequestMsg is dispatched by the WithCanUseTool callback when the SDK
// subprocess wants to call a tool and needs the operator's allow/deny decision.
// AnsC receives true (allow) or false (deny).
type PermRequestMsg struct {
    ToolName string
    Input    map[string]any
    AnsC     chan<- bool
}

// QuestionRequestMsg is dispatched by the ask_user MCP tool handler when the
// agent wants to present a structured question to the operator.
// Options is nil for a free-text answer; non-nil for a numbered choice list.
// AnsC receives the operator's answer text (or the selected option text).
type QuestionRequestMsg struct {
    Question string
    Options  []string
    AnsC     chan<- string
}
```

### 2. `helpchat/cmds.go` — register `WithCanUseTool`

`connectCmd` and `queryCmd` both build a `[]claudecode.Option` slice. Add
`WithCanUseTool` to each:

```go
claudecode.WithCanUseTool(func(
    ctx context.Context,
    toolName string,
    input map[string]any,
    _ claudecode.ToolPermissionContext,
) (claudecode.PermissionResult, error) {
    // jig-help SDK MCP tools are in-process and can be pre-approved without
    // prompting. All other tool calls (shell, external MCP) need operator review.
    if strings.HasPrefix(toolName, "mcp__jig-help__") {
        return claudecode.NewPermissionResultAllow(), nil
    }
    ansC := make(chan bool, 1)
    dispatch(PermRequestMsg{ToolName: toolName, Input: input, AnsC: ansC})
    select {
    case allow := <-ansC:
        if allow {
            return claudecode.NewPermissionResultAllow(), nil
        }
        return claudecode.NewPermissionResultDeny("denied by operator"), nil
    case <-ctx.Done():
        return claudecode.NewPermissionResultDeny("context cancelled"), nil
    }
}),
```

**Why pre-approve `mcp__jig-help__*` tools?**
These tools are registered via `WithSdkMcpServer`, which means they execute as Go
function calls inside the same process — there is no shell, no file system side-effect
beyond reading run artifacts that jig already owns. Prompting for them would be noise.
Any tool the agent discovers outside that prefix (which would mean a misconfigured
external MCP server or a shell tool) deserves a real prompt.

Note: `dispatch` is the closure captured from the caller. Both `connectCmd` and
`queryCmd` already receive `dispatch DispatchFunc` as a parameter, so the callback has
it in scope.

### 3. `helpchat/tools.go` — add `ask_user` tool

Register a new in-process MCP tool that lets the agent ask a structured question:

```go
func buildAskUser(dispatch DispatchFunc) *claudecode.McpTool {
    schema := map[string]any{
        "type": "object",
        "properties": map[string]any{
            "question": map[string]any{
                "type":        "string",
                "description": "The question to present to the operator.",
            },
            "options": map[string]any{
                "type":        "array",
                "items":       map[string]any{"type": "string"},
                "description": "Optional list of choices. Omit for a free-text answer.",
            },
        },
        "required": []any{"question"},
    }
    return claudecode.NewTool(
        "ask_user",
        "Present a question to the operator and wait for their answer. "+
            "Provide options[] for a multiple-choice prompt; omit for free-text.",
        schema,
        func(ctx context.Context, args map[string]any) (*claudecode.McpToolResult, error) {
            question, _ := args["question"].(string)
            if question == "" {
                return errResult("question is required"), nil
            }
            var options []string
            if raw, ok := args["options"].([]any); ok {
                for _, v := range raw {
                    if s, ok := v.(string); ok {
                        options = append(options, s)
                    }
                }
            }
            ansC := make(chan string, 1)
            dispatch(QuestionRequestMsg{Question: question, Options: options, AnsC: ansC})
            select {
            case answer := <-ansC:
                return okResult(answer), nil
            case <-ctx.Done():
                return errResult("operator did not respond (context cancelled)"), nil
            }
        },
    )
}
```

Add `buildAskUser(dispatch)` to the `claudecode.CreateSDKMcpServer(...)` call in
`BuildMcpServer`. The function already receives `dispatch DispatchFunc`.

### 4. `helpchat/model.go` — gate state + Update + View

#### 4a. New types (top of file, after existing consts)

```go
type gateKind int

const (
    gateKindPerm     gateKind = iota
    gateKindQuestion
)

type pendingGateEntry struct {
    kind     gateKind
    toolName string
    input    map[string]any
    permAnsC chan<- bool
    question string
    options  []string
    qAnsC    chan<- string
    selected int
    textBuf  string
}
```

#### 4b. `Model` struct — add one field

```go
pendingGate *pendingGateEntry
```

#### 4c. `Update` — handle new message types

In the `switch msg := msg.(type)` block add two cases **before** the
`tea.KeyPressMsg` case:

```go
case PermRequestMsg:
    m.pendingGate = &pendingGateEntry{
        kind:     gateKindPerm,
        toolName: msg.ToolName,
        input:    msg.Input,
        permAnsC: msg.AnsC,
    }
    m.updateViewport()
    if m.dispatchCh != nil {
        cmds = append(cmds, waitForDispatchCmd(m.dispatchCh))
    }

case QuestionRequestMsg:
    m.pendingGate = &pendingGateEntry{
        kind:     gateKindQuestion,
        question: msg.Question,
        options:  msg.Options,
        qAnsC:    msg.AnsC,
    }
    m.updateViewport()
    if m.dispatchCh != nil {
        cmds = append(cmds, waitForDispatchCmd(m.dispatchCh))
    }
```

#### 4d. `Update` — route keys to gate when active

The `tea.KeyPressMsg` case currently unconditionally falls through to textarea/viewport
handling. Prepend a guard:

```go
case tea.KeyPressMsg:
    if m.pendingGate != nil {
        m, cmd := m.updateGate(msg)
        return m, cmd
    }
    // ... existing focus/enter/default handling unchanged ...
```

`updateGate` is a new method (see §4e).

#### 4e. New method `updateGate`

```go
func (m Model) updateGate(msg tea.KeyPressMsg) (Model, tea.Cmd) {
    g := m.pendingGate
    switch g.kind {

    case gateKindPerm:
        switch {
        case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("y", "Y"))):
            g.permAnsC <- true
            m.pendingGate = nil
        case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("n", "N", "esc"))):
            g.permAnsC <- false
            m.pendingGate = nil
        }

    case gateKindQuestion:
        if len(g.options) > 0 {
            // Option list navigation
            switch {
            case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("up", "k"))):
                if g.selected > 0 {
                    g.selected--
                }
                m.pendingGate = g
            case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("down", "j"))):
                if g.selected < len(g.options)-1 {
                    g.selected++
                }
                m.pendingGate = g
            case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("enter"))):
                g.qAnsC <- g.options[g.selected]
                m.pendingGate = nil
            case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("esc"))):
                g.qAnsC <- ""
                m.pendingGate = nil
            }
        } else {
            // Free-text: accumulate into textBuf
            switch {
            case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("enter"))):
                if g.textBuf != "" {
                    g.qAnsC <- g.textBuf
                    m.pendingGate = nil
                }
            case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("backspace"))):
                if len(g.textBuf) > 0 {
                    g.textBuf = g.textBuf[:len(g.textBuf)-1]
                }
                m.pendingGate = g
            case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("esc"))):
                g.qAnsC <- ""
                m.pendingGate = nil
            default:
                if msg.Text != "" {
                    g.textBuf += msg.Text
                }
                m.pendingGate = g
            }
        }
    }
    m.updateViewport()
    return m, nil
}
```

Note: when `m.pendingGate` is nil after a send, the pointer itself changed on the
returned `m` copy — this is correct value-receiver behaviour.

#### 4f. `CapturesText`

```go
func (m Model) CapturesText() bool {
    // Capture all text while a gate is active (keys route to updateGate).
    if m.pendingGate != nil {
        return true
    }
    return m.focus == focusInput
}
```

#### 4g. `View` — render gate strip

The `View` method currently joins `[histStr, taStr, hint]`. When a gate is pending,
replace `taStr` with the gate rendering:

```go
func (m Model) View(width, height int, focused bool) string {
    if m.vpW == 0 {
        return ""
    }
    histStr := m.vp.View()
    var bottomStr string
    if m.pendingGate != nil {
        bottomStr = m.renderGate(width - 4)
    } else {
        bottomStr = m.ta.View()
    }
    hint := m.gateHint()
    inner := strings.Join([]string{histStr, bottomStr, hint}, "\n")
    return shared.Theme.Help.Box.Width(width - 2).Render("Help Agent\n\n" + inner)
}

func (m Model) gateHint() string {
    if g := m.pendingGate; g != nil {
        switch g.kind {
        case gateKindPerm:
            return shared.Theme.Chat.Hint.Render("[y] allow  ·  [n] deny")
        case gateKindQuestion:
            if len(g.options) > 0 {
                return shared.Theme.Chat.Hint.Render("↑↓ navigate  ·  enter confirm  ·  esc cancel")
            }
            return shared.Theme.Chat.Hint.Render("enter send  ·  esc cancel")
        }
    }
    return shared.Theme.Chat.Hint.Render(`ctrl+\ or esc · close  ·  tab · switch focus`)
}
```

### 5. `helpchat/gate_view.go` (new file) — `renderGate` helper

Extract gate rendering into its own file to keep `model.go` focused:

```go
package helpchat

import (
    "encoding/json"
    "fmt"
    "strings"

    "jig/internal/tui/shared"
)

// renderGate returns the bottom-of-modal string for an active pendingGateEntry.
func (m Model) renderGate(width int) string {
    g := m.pendingGate
    switch g.kind {
    case gateKindPerm:
        return m.renderPermGate(g, width)
    case gateKindQuestion:
        return m.renderQuestionGate(g, width)
    }
    return ""
}

func (m Model) renderPermGate(g *pendingGateEntry, width int) string {
    var b strings.Builder
    b.WriteString(shared.Theme.Question.Render("Tool permission request") + "\n")
    b.WriteString(shared.Theme.Running.Render(g.toolName) + "\n")
    if len(g.input) > 0 {
        if raw, err := json.MarshalIndent(g.input, "  ", "  "); err == nil {
            b.WriteString(shared.Theme.Chat.Hint.Render(string(raw)) + "\n")
        }
    } else {
        b.WriteString(shared.Theme.Chat.Hint.Render("(no parameters)") + "\n")
    }
    return b.String()
}

func (m Model) renderQuestionGate(g *pendingGateEntry, width int) string {
    var b strings.Builder
    b.WriteString(shared.Theme.Question.Render("Agent question") + "\n")
    b.WriteString(g.question + "\n\n")
    if len(g.options) > 0 {
        for i, opt := range g.options {
            cursor := "  "
            style := shared.Theme.Chat.Hint
            if i == g.selected {
                cursor = "▶ "
                style = shared.Theme.SelectedLine
            }
            b.WriteString(style.Render(fmt.Sprintf("%s%s", cursor, opt)) + "\n")
        }
    } else {
        b.WriteString(shared.Theme.Marker.Render("> ") + g.textBuf + "█\n")
    }
    return b.String()
}
```

## Edge cases and invariants

**Only one gate at a time.** The agent is blocked in its goroutine while a gate is
active (either the SDK callback is waiting on `permAnsC` or the MCP tool handler is
waiting on `qAnsC`). A second gate cannot arrive until the first is answered. The
`pendingGate` pointer is therefore always nil or a single live entry.

**Context cancellation.** Both the `WithCanUseTool` callback and the `ask_user` tool
handler select on `ctx.Done()` as well as the answer channel. If the help modal is
closed (context cancelled), the goroutine unblocks and returns a denial/error result
without leaking.

**Pre-approving jig-help tools.** The `WithCanUseTool` callback pre-approves any tool
with the `mcp__jig-help__` prefix. These are all in-process Go functions with no
side-effects beyond reading run artifacts jig owns. Prompting for them would be noise
that obscures genuine external-tool permission requests.

**Esc to cancel a gate.** Pressing `esc` while a gate is active sends `false`/`""` to
the answer channel (denial/empty), clears the gate, and returns the user to normal chat
input. The agent receives a denial result and can choose to explain the refusal.

**`updateViewport` is called after every gate state change** so the chat history (behind
the gate) stays current. The gate renders in the bottom region only; the history viewport
is not affected by gate presence.

**`dispatchCh` is re-armed after each `PermRequestMsg`/`QuestionRequestMsg`** so the
model keeps draining the channel while blocked — any concurrent `DispatchedMsg` (fire-
and-forget actions from other tools) still gets processed.

## Testing

- Unit test `updateGate` with `gateKindPerm`: send `y`, `n`, `esc` — verify `permAnsC`
  receives the correct bool and `pendingGate` is nil after each.
- Unit test `updateGate` with `gateKindQuestion` + options: navigate with `up`/`down`,
  confirm with `enter`, cancel with `esc`.
- Unit test `updateGate` with `gateKindQuestion` + no options (free-text): type chars,
  backspace, enter to submit.
- Verify `CapturesText()` returns true while gate active (prevents monitor from routing
  keys to other components).
- Integration: send a user message that triggers a tool, confirm the gate entry is
  created, submit a response, confirm the gate is cleared and the next streaming delta
  arrives.
