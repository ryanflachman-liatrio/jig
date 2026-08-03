# Plan: Agent Pause Detection and In-Session Question Handling

## Background

Two related problems have been observed in jig's execution engine:

1. **Premature run termination** — A step that transitions to `StatusNeedsInput` (via the
   `block_on` handler) causes the scheduler to declare the run finished, because
   `anyPendingRunnable` only counts `StatusAwaitingReview` as a "still live" status.

2. **`AskUserQuestion` sessions hang then crash** — When Claude dynamically asks the user a
   question by calling the built-in `AskUserQuestion` tool, jig has no mechanism to
   intercept the tool call, pause the step, collect the answer, and send it back. The SDK
   subprocess blocks waiting for a tool result that never comes; eventually the context
   is cancelled and `captureStream` returns "agent connection closed unexpectedly."

These are distinct bugs that happen to share symptoms. The sections below cover each
in isolation, then sketch the integration point between them.

---

## Research Summary

**How Claude signals "I need input":** There is no dedicated `human_turn_pending` message
type. When Claude wants to ask the user something, it calls the `AskUserQuestion` built-in
tool. This appears as a `ToolUseBlock` (name `"AskUserQuestion"`) inside an
`AssistantMessage` in the SDK message stream. The CLI then blocks waiting for a
`ToolResultBlock` response. In headless mode with no responder, this blocks indefinitely
or until context cancellation.

**Structured question schema** (from the `AskUserQuestion` tool input):

```json
{
  "questions": [
    {
      "header": "Format",           // ≤ 12 chars, short chip label
      "question": "Full question?", // full text shown to user
      "options": [
        { "label": "...", "description": "..." }
      ],
      "multiSelect": false
    }
  ]
}
```

**How Zed gets structured questions:** Zed wraps the Claude Code SDK with an ACP
(Agent Client Protocol) adapter that translates `AskUserQuestion` tool calls into
JSON-RPC 2.0 method calls with the full structured payload. jig currently skips this
layer entirely.

**`ResultMessage.Subtype` values** (official CLI):
`"success"`, `"error_max_turns"`, `"error_max_budget_usd"`,
`"error_during_execution"`, `"error_max_structured_output_retries"`.
There is no `"human_turn_pending"` subtype.

**Sub-agent waiting:** When Claude spawns a sub-agent via the `Agent` tool,
`AssistantMessage` and `StreamEvent` messages carry `ParentToolUseID` identifying the
parent tool call. No special pause event is emitted; `msgChan` stays open and messages
from sub-agents flow through tagged with that field. No engine changes are needed for
this case — it works today — but the `ParentToolUseID` field is silently ignored.

---

## Open Questions

These need answers before or during implementation. If an assumption below turns out to
be wrong, revisit the relevant task.

1. **Does the `AskUserQuestion` ToolUseBlock appear in the SDK `AssistantMessage` stream
   before the session blocks?**
   Assumed yes (ToolUseBlocks always appear in the stream). Needs a live test to confirm
   the exact ordering: does the AssistantMessage arrive before or after the CLI starts
   blocking?

2. **Does the Go SDK expose a way to send a `UserMessage` with `ToolResultBlock`
   mid-session (not just a new `Query` turn)?**
   The `Transport.SendMessage` API exists; the question is whether sending a
   `UserMessage` with a `ToolResultBlock` is the correct wire format, or whether we
   must resume via `WithResume` + `Query` like the existing review-messaging flow.
   This determines whether Task 2c or Task 2d is the right sub-approach.

3. **When the CLI is blocked on `AskUserQuestion`, does `msgChan` stay open or does
   a `ResultMessage` arrive first?**
   If a `ResultMessage` arrives (perhaps with a new subtype), the existing `captureStream`
   would return before we can intercept. If `msgChan` stays open, we have time to inject
   a tool result.

4. **Should the `AskUserQuestion` answer flow use session-resume (like the existing
   `block_on` path) or inline tool-result injection?**
   Session-resume is already implemented in the runner/engine. Inline tool-result
   injection is cleaner but requires SDK support for mid-session `SendMessage`. If the
   SDK doesn't support it cleanly, we fall back to session-resume.

5. **Should dynamic `AskUserQuestion` handling replace or complement `block_on`?**
   `block_on` is a TOML-declared condition evaluated on the step's _structured output
   after execution completes_. `AskUserQuestion` fires _during_ execution. They're
   complementary: `block_on` is for predictable pause points; `AskUserQuestion` is for
   Claude-initiated dynamic questions. Both should work.

6. **Should all subtypes that are not `"success"` be treated as errors?**
   Currently `captureStream` checks `m.IsError` on the `ResultMessage`. `error_max_turns`
   and `error_max_budget_usd` are policy limits that may warrant dedicated error messages
   in the TUI rather than a generic "agent failed." Clarify desired behaviour before
   implementing.

---

## Assumptions

- The Go SDK (`severity1/claude-agent-sdk-go`) receives `AskUserQuestion` as a
  `ToolUseBlock` in an `AssistantMessage` before the session blocks. We can detect it
  in `captureStream`.
- The SDK's `Transport.SendMessage` can inject a `UserMessage` with a `ToolResultBlock`
  mid-session. If not, we fall back to the existing session-resume mechanism.
- `AskUserQuestion` is always the _last_ block in an `AssistantMessage` (i.e., Claude
  won't ask a question and use another tool in the same turn).
- The structured `input.questions` field is always present and well-formed when
  `toolName == "AskUserQuestion"`.
- Only one `AskUserQuestion` is outstanding at a time (the SDK serialises turns).
- The `block_on` mechanism remains unchanged; Task 1 is a pure bug fix.

---

## Task 1 — Fix premature run termination for `StatusNeedsInput`

**Root cause:** `engine.go:anyPendingRunnable` only returns `true` for
`StatusAwaitingReview`, not for `StatusNeedsInput`. After `phCheckBlockOn` transitions a
step to `StatusNeedsInput` and `inFlight` drops to zero, the scheduler sees nothing
runnable and calls `RunFinished`, terminating the run before the user has a chance to
answer.

### Subtask 1.1 — Add `StatusNeedsInput` to `anyPendingRunnable`

**File:** `internal/engine/engine.go`

**Change:** In the `anyPendingRunnable` loop, add a parallel guard alongside
`StatusAwaitingReview`:

```go
if st.Status == step.StatusNeedsInput {
    return true
}
```

**Test:** `internal/engine/engine_test.go` — add a test case where a step's `block_on`
fires, the run has no other pending steps, and the scheduler correctly stays alive until
`Run.SendInput` is called.

### Subtask 1.2 — Verify existing `StatusNeedsInput` tests still pass

Run `go test ./internal/engine/...` and confirm no regressions.

---

## Task 2 — Intercept `AskUserQuestion` in `captureStream`

This is the larger architectural change. The goal is: when Claude calls
`AskUserQuestion`, pause the step, surface the structured question to the TUI, collect
the user's answer, and send it back to the SDK so Claude can continue.

### Subtask 2.1 — Add `AgentQuestion` engine event

**File:** `internal/engine/event.go`

Add a new event type carrying the structured question data:

```go
// AgentQuestion is emitted when an in-flight agent step calls AskUserQuestion.
// It carries the full structured payload (parsed from ToolUseBlock.Input) so the
// TUI can render clickable options. The step transitions to StatusNeedsInput and
// waits for Run.AnswerQuestion to deliver the user's choice.
type AgentQuestion struct {
    RunID     string
    StepID    string
    ToolUseID string // correlates the answer to the tool call
    Questions []AgentQuestionItem
}

type AgentQuestionItem struct {
    Header      string
    Question    string
    Options     []AgentQuestionOption
    MultiSelect bool
}

type AgentQuestionOption struct {
    Label       string
    Description string
}

func (AgentQuestion) isEvent() {}
```

### Subtask 2.2 — Add `Question` method to `Reporter`

**File:** `internal/engine/executor.go`

Extend the `Reporter` interface with a blocking call that lets `captureStream` pause
execution until an answer arrives:

```go
// Question delivers a dynamic AskUserQuestion from the agent to the scheduler
// and blocks until the human provides an answer. toolUseID correlates the
// answer back to the tool call so captureStream can send the correct tool result.
// Returns the user's answer text. Blocks until Run.AnswerQuestion is called.
Question(toolUseID string, questions []AgentQuestionItem) string
```

> **Note on the blocking design:** `captureStream` runs inside a goroutine launched by
> `dispatch`. Blocking inside `Reporter.Question` is safe as long as the scheduler
> doesn't also block waiting for the goroutine to finish — it doesn't; it continues
> processing inbox messages. The goroutine unblocks when the answer arrives via
> `Run.AnswerQuestion`.

### Subtask 2.3 — Thread the answer-back channel through the reporter

**File:** `internal/engine/engine.go` (the `reporter` struct and `dispatch`)

The `reporter` struct needs a channel on which the answer arrives:

```go
type reporter struct {
    subs       []sub
    ev         func(Event)
    answerCh   chan string // one-shot; nil until a Question call opens it
    answerMu   sync.Mutex
}
```

`reporter.Question(...)`:
1. Builds `AgentQuestion` event, emits it via `r.ev`
2. Creates a one-shot `answerCh = make(chan string, 1)`
3. Blocks on `<-answerCh`
4. Returns the received string

A new scheduler message delivers the answer:

```go
type agentQuestionAnswerMsg struct {
    stepID    string
    toolUseID string
    answer    string
}
```

`scheduler.handle(agentQuestionAnswerMsg)` routes the answer to the step's active reporter.

> **Question (open):** How does the scheduler hold a reference to the active reporter
> for a running step? Currently `reporter` is created per-dispatch and passed only to
> the executor. One option: store `*reporter` in `scheduler` keyed by `stepID` for the
> duration of the step's execution, and delete it on `stepDoneMsg`. This keeps the
> contract "one active reporter per in-flight step."

### Subtask 2.4 — Detect `AskUserQuestion` in `captureStream`

**File:** `internal/runner/agent.go`

In the `case *claudecode.AssistantMessage:` branch, after building `blocks`, scan for
`AskUserQuestion`:

```go
for _, cb := range m.Content {
    b, ok := cb.(*claudecode.ToolUseBlock)
    if !ok || b.Name != "AskUserQuestion" {
        continue
    }
    questions, err := parseAskUserQuestions(b.Input)
    if err != nil {
        // malformed input — skip; Claude will handle the missing result
        continue
    }
    answer := rep.Question(b.ToolUseID, questions)
    // send the answer as a tool result (see 2.5)
    sendToolResult(ctx, send, b.ToolUseID, answer)
}
```

Add `parseAskUserQuestions(input map[string]any) ([]engine.AgentQuestionItem, error)` as
a pure helper function.

### Subtask 2.5 — Send tool result back to the SDK

**Investigation required (see Open Question 2):**

**Option A — `Transport.SendMessage` (preferred):** Pass a `send func(msg)` closure into
`captureStream` (wrapping `client`'s `SendMessage`). Construct a `UserMessage` with a
`ToolResultBlock{ToolUseID: b.ToolUseID, Content: answer}` and send it. Claude continues
in the same session without a new prompt.

**Option B — Session resume fallback:** If Option A is not supported by the SDK, emit
an `AgentQuestion` with a special flag, let `captureStream` return a partial result
(new `ResultMessage` subtype or a sentinel `step.Result`), and have the scheduler resume
the session via the existing `WithResume` + `Query` path with the answer as the new
message.

Option A avoids the overhead of a new session handshake and keeps the full conversation
intact. Option B reuses existing plumbing. Verify Option A in a spike before committing.

### Subtask 2.6 — Transition step to `StatusNeedsInput` during the question

While `reporter.Question` is blocking, the step's goroutine is paused but `inFlight` is
still counting it. The scheduler must not terminate (fixed by Task 1, which already
handles `StatusNeedsInput`). No additional state transition is strictly required, but it
is helpful for the TUI to show the step as "waiting for answer."

**File:** `internal/engine/engine.go`

When the `AgentQuestion` event is emitted, transition the step to `StatusNeedsInput` in
the scheduler's event handler. When the answer arrives (`agentQuestionAnswerMsg`), route
it to the reporter's `answerCh` and transition back to `StatusRunning`.

> **Timing concern:** The step is currently `StatusRunning` and `inFlight > 0`. Setting
> it to `StatusNeedsInput` while still in-flight is safe because `anyPendingRunnable`
> returns true for `StatusNeedsInput` (Task 1). When the goroutine eventually finishes
> and sends `stepDoneMsg`, `inFlight` is decremented and normal post-exec handling runs.

### Subtask 2.7 — Expose `Run.AnswerQuestion`

**File:** `internal/engine/engine.go`

```go
// AnswerQuestion delivers the user's response to an in-flight AskUserQuestion.
func (r *Run) AnswerQuestion(stepID, toolUseID, answer string) {
    r.inbox <- agentQuestionAnswerMsg{stepID: stepID, toolUseID: toolUseID, answer: answer}
}
```

---

## Task 3 — TUI: render structured questions

### Subtask 3.1 — Handle `AgentQuestion` event in monitor

**File:** `internal/tui/monitor.go`

When `AgentQuestion` arrives on the `ctrl` channel:
1. Store the structured question data in the monitor's step state (keyed by `StepID`)
2. Switch the compose-box mode to "question" (similar to how `InputRequest` shows a
   compose box, but renders options as a selectable list instead of a free-text field)

### Subtask 3.2 — Render question options as a list

**File:** `internal/tui/monitor.go`

When the step at focus is in question mode, render:
- The `header` chip and `question` text
- Numbered/lettered options with label + description
- Allow the user to select by pressing the option key or using arrow keys + enter
- `multiSelect` mode: allow toggling multiple options before confirming

### Subtask 3.3 — Wire selection to `Run.AnswerQuestion`

**File:** `internal/tui/root.go`

Add a new internal message type:

```go
type agentQuestionResponseMsg struct {
    runID     string
    stepID    string
    toolUseID string
    answer    string
}
```

In `root.Update`, handle `agentQuestionResponseMsg` by calling
`m.handles[msg.runID].AnswerQuestion(msg.stepID, msg.toolUseID, msg.answer)`.

### Subtask 3.4 — Add tests for question rendering

**File:** `internal/tui/monitor_test.go`

Test that:
- Receiving an `AgentQuestion` event shows the question panel
- Selecting an option emits `agentQuestionResponseMsg`
- The step status badge updates to reflect "waiting for answer"

---

## Task 4 — Improve `ResultMessage` subtype surfacing (low priority)

Currently `captureStream` treats any non-error `ResultMessage` as success. The engine
stores `m.Subtype` in `result.Subtype` but the TUI doesn't surface it.

### Subtask 4.1 — Surface `error_max_turns` and `error_max_budget_usd` distinctly

**File:** `internal/runner/agent.go`

When `m.IsError` is true and `m.Subtype` is `"error_max_turns"` or
`"error_max_budget_usd"`, set a descriptive `result.Err` message rather than relying on
the generic `resultErrorText`.

### Subtask 4.2 — Surface subtype in TUI step badge (optional)

Display a small subtype annotation next to the ✗ badge on failed steps so the operator
can distinguish "hit turn limit" from "API error" at a glance.

---

## Implementation Order

```
Task 1.1  (anyPendingRunnable fix — 5 min, isolated, fixes known crash)
Task 1.2  (verify tests)
Task 2.1  (AgentQuestion event type — additive, no breakage)
Task 2.2  (Reporter.Question interface method — breaks fake/reporter impls, fix them)
Task 2.3  (answer-back channel in reporter + scheduler msg)
Task 2.5  (spike: can Transport.SendMessage inject tool results? determines 2.4 approach)
Task 2.4  (captureStream AskUserQuestion detection)
Task 2.6  (StatusNeedsInput transition during question)
Task 2.7  (Run.AnswerQuestion)
Task 3.1–3.3 (TUI question rendering)
Task 3.4  (TUI tests)
Task 4.x  (subtype surfacing — after everything else works)
```

---

## Files Touched

| File | Tasks |
|---|---|
| `internal/engine/engine.go` | 1.1, 2.3, 2.6, 2.7 |
| `internal/engine/event.go` | 2.1 |
| `internal/engine/executor.go` | 2.2 |
| `internal/engine/handlers.go` | (none, block_on unchanged) |
| `internal/runner/agent.go` | 2.4, 2.5, 4.1 |
| `internal/tui/monitor.go` | 3.1, 3.2, 3.4 |
| `internal/tui/root.go` | 3.3 |
| `internal/engine/engine_test.go` | 1.2, 2.3 tests |
| `internal/runner/agent_test.go` | 2.4 tests |
