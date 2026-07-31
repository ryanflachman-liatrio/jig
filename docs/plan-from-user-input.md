# Plan: `from = "user"` Input Source

Add a new input source kind that pauses workflow execution and collects free-form text from the user via a Bubble Tea textarea before dispatching the step.

## Example TOML

```toml
[[step]]
id = "feature"
type = "agent"
skill = "feature"

[[step.inputs]]
from = "user"
label = "Describe the feature to implement"
as = "feature_description.md"
```

## Core mechanism

The engine's existing `ReviewRequest` / `verdictMsg` pause-resume pattern is extended with a parallel `PromptRequest` / `userInputMsg` pair. User-collected text is delivered to the agent runner as an inline `ResolvedInput` (no temp files — the agent runner's existing `Inline` path writes text directly into the agent prompt).

---

## Step 1 — Schema: extend `Input` struct

**`internal/workflow/schema.go`**

Add `From`, `Label`, `As` to `Input`. In `UnmarshalTOML`, parse the three new keys and add a consistency check: if `From` is set, `Ref` and `Path` must both be empty.

```go
type Input struct {
    Ref    string
    Path   string
    Inline bool
    From   string // "user" for interactive collection
    Label  string // prompt shown in TUI
    As     string // name hint passed to agent prompt
}
```

---

## Step 2 — Validation

**`internal/workflow/validate.go`**

Extend `checkInputs` with rules for `from = "user"`:
- `label` is required
- `as` is required
- only valid on `type = "agent"` steps
- cannot be combined with `ref` / `path` / `inline`

---

## Step 3 — New engine event

**`internal/engine/event.go`**

Add `PromptRequest`, exactly parallel to `ReviewRequest`:

```go
// PromptRequest asks the human to supply free-form text for a from="user" input.
// Multiple user inputs on one step are requested sequentially.
type PromptRequest struct {
    RunID  string
    StepID string
    Label  string
    As     string
}

func (PromptRequest) isEvent() {}
```

---

## Step 4 — Engine scheduler

**`internal/engine/engine.go`** — the biggest change.

### New types

```go
type userInputMsg struct {
    stepID string
    as     string // which input this answer corresponds to
    text   string
}
func (userInputMsg) isSchedMsg() {}
```

### New scheduler state

Add three maps to the `scheduler` struct (initialize in `newScheduler`):

```go
pendingUserInputs   map[string][]workflow.Input    // stepID → remaining from="user" inputs
collectedUserInputs map[string][]ResolvedInput     // stepID → answers collected so far
preResolvedInputs   map[string][]ResolvedInput     // stepID → fully collected, ready to inject
```

### `nextReady` guard

Before the existing review-step intercept, add:

```go
if hasUserInputs(st) && len(s.preResolvedInputs[st.ID]) == 0 {
    s.dispatchUserPrompt(st)
    continue
}
```

The `preResolvedInputs` check prevents re-intercepting the step after all inputs have been collected and it has been reset to `StatusPending`.

### `dispatchUserPrompt`

```go
func (s *scheduler) dispatchUserPrompt(st *workflow.Step) {
    var userInputs []workflow.Input
    for _, inp := range st.Inputs {
        if inp.From == "user" {
            userInputs = append(userInputs, inp)
        }
    }
    s.pendingUserInputs[st.ID] = userInputs[1:]
    s.collectedUserInputs[st.ID] = nil
    s.transition(st.ID, s.states[st.ID].Status, step.StatusAwaitingReview)
    s.emit(PromptRequest{
        RunID:  s.runID,
        StepID: st.ID,
        Label:  userInputs[0].Label,
        As:     userInputs[0].As,
    })
}
```

### `handle(userInputMsg)`

1. Append inline `ResolvedInput{Ref: workflow.Input{Inline: true, As: m.as}, Value: m.text}` to `collectedUserInputs[stepID]`.
2. If `pendingUserInputs[stepID]` is non-empty, pop the next input and emit another `PromptRequest`. Return.
3. Otherwise, move collected inputs to `preResolvedInputs[stepID]`, clean up the other maps, and transition the step back to `StatusPending` so `nextReady` picks it up for normal dispatch.

### `dispatch`

When building `StepRequest`, merge `preResolvedInputs[st.ID]` into `Inputs` and delete the entry:

```go
req := StepRequest{
    RunID:       runID,
    Step:        st,
    Inputs:      s.preResolvedInputs[st.ID], // nil-safe
    Feedback:    s.stepFeedback[st.ID],
    ArtifactDir: artifactDir,
    Worktree:    worktreePath,
}
delete(s.preResolvedInputs, st.ID)
```

### `Run.ProvideUserInput`

Add alongside `Resolve`:

```go
func (r *Run) ProvideUserInput(stepID, as, text string) {
    r.inbox <- userInputMsg{stepID: stepID, as: as, text: text}
}
```

---

## Step 5 — TUI root

**`internal/tui/root.go`**

Add `userInputResponseMsg` and handle it in `Update` by calling `run.ProvideUserInput(...)`, mirroring the `reviewVerdictMsg` handler exactly:

```go
type userInputResponseMsg struct {
    runID  string
    stepID string
    as     string
    text   string
}
```

---

## Step 6 — TUI monitor

**`internal/tui/monitor.go`**

### New fields on `monitorModel`

```go
pendingPrompt  *engine.PromptRequest
promptTextarea textarea.Model
```

### `handleEngineEvent`

Change return signature from `monitorModel` to `(monitorModel, tea.Cmd)` and thread the cmd through the caller in `Update`. On `engine.PromptRequest`, set `m.pendingPrompt`, init a focused textarea (4 lines tall, label as placeholder, same style as `chat.go`), and return `textarea.Blink` as the cmd.

### Key handling in `Update`

When `m.pendingPrompt != nil`:
- Route keystrokes through `m.promptTextarea.Update(msg)`.
- On `ctrl+s`: capture `m.promptTextarea.Value()`, clear `m.pendingPrompt`, reset textarea, return a cmd emitting `userInputResponseMsg`.
- Guard the existing digit-key verdict check for `pendingReview` behind `m.pendingPrompt == nil`.

### `body()` rendering

```go
if m.pendingPrompt != nil {
    b.WriteString(markerStyle.Render("Input required — step: " + m.pendingPrompt.StepID) + "\n")
    b.WriteString(questionStyle.Render(m.pendingPrompt.Label) + "\n\n")
    b.WriteString(m.promptTextarea.View() + "\n")
}
```

### `footerView`

Show `"awaiting user input"` status and `ctrl+s submit` hint when `m.pendingPrompt != nil`.

### Cleanup

Clear `m.pendingPrompt` in the `StepStatus` case when the step reaches a terminal state (covers error paths).

### `resize()`

Call `m.promptTextarea.SetWidth(m.width - 4)` when `m.pendingPrompt != nil`.

---

## Step 7 — Agent runner: no changes

**`internal/runner/agent.go`**

The inline `ResolvedInput` is already handled by `buildAgentPrompt`'s existing `Inline` branch. No changes needed.

---

## Change order

1. `internal/workflow/schema.go` — parse new fields
2. `internal/workflow/validate.go` — validate new rules
3. `internal/engine/event.go` — add `PromptRequest`
4. `internal/engine/engine.go` — scheduler changes
5. `internal/tui/root.go` — wire `userInputResponseMsg`
6. `internal/tui/monitor.go` — textarea UI

---

## Tricky points

**Re-dispatch guard.** After all inputs are collected, the step resets to `StatusPending`. `nextReady` checks `preResolvedInputs[id]` to avoid re-intercepting — the step proceeds to normal dispatch on the next tick.

**`handleEngineEvent` signature.** Currently returns only `monitorModel`; must return `(monitorModel, tea.Cmd)` so the textarea blink timer works. Thread the cmd back through the `Update` caller.

**`StatusAwaitingReview` reuse.** Reusing it for prompt-parked steps is safe — `anyPendingRunnable` already treats it as "still alive," and review vs. agent steps are never the same step type.

**Multiple `from = "user"` inputs.** The sequential queue in `pendingUserInputs` handles this. The TUI receives multiple `PromptRequest` events for the same step, each replacing `pendingPrompt`. Rendering always uses the current `pendingPrompt` so no special multi-input handling is needed in the view.

**Inline delivery.** User text is passed as `ResolvedInput{Ref: workflow.Input{Inline: true, As: as}, Value: text}` — no temp files, no `ArtifactDir` needed for this feature.
