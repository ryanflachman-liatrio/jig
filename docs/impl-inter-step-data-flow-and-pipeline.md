# Implementation Guide: Inter-Step Data Flow Fix + Post-Execution Pipeline

## Purpose

This document is a self-contained implementation guide for a new agent. It covers:

1. **Bug 1** — `@step.field` inputs are never resolved before dispatch (all downstream steps receive empty inputs)
2. **Bug 2** — structured output `status:"blocked"` is silently treated as step success
3. **Refactor** — replace the monolithic `handle(stepDoneMsg)` post-execution block with a composable handler-chain (Chain of Responsibility), and centralize all pre-dispatch input resolution into a dedicated helper

---

## Repo overview (read first)

- Module path: `jig`; internal packages imported as `jig/internal/...`
- Entry point: `cmd/jig/main.go`
- Workflow execution: `internal/engine/` (scheduler + executor interface)
- Concrete executors: `internal/runner/` (AgentExecutor, CommandExecutor)
- Per-step structured data: `internal/step/step.go` (`Result.Structured json.RawMessage`)
- Workflow schema/validation: `internal/workflow/`

Run tests with `go test ./...`. Format with `gofmt -l -w .` and vet with `go vet ./...` before committing.

---

## Background: how agent output is captured today

`internal/runner/agent.go` lines 173–228:
- The agent runner streams SDK messages and watches for a `StructuredOutput` tool-use block in `AssistantMessage.Content`
- On `ResultMessage` (the final message): if `m.StructuredOutput != nil`, marshals it to `result.Structured`; otherwise falls back to `lastStructuredOutput` captured from the stream
- `result.Structured` is `json.RawMessage` — raw JSON bytes of the agent's schema output

`internal/step/step.go` lines 37–49: the `Result` struct:
```go
type Result struct {
    Status       Status          // engine-level lifecycle status
    OutputPath   string          // file written by command steps (step.output field)
    Structured   json.RawMessage // agent's schema-output JSON (never nil for successful agents with a schema)
    Verdict      string          // human verdict for review steps
    ...
}
```

`evalGuard` in `internal/engine/engine.go` lines 944–999 already decodes `Result.Structured` for `when` and `block_on` conditions and caches the decoded map in `s.structured[stepID]`. This is the decode pattern to reuse.

---

## Bug 1: `@step.field` inputs never resolved

### Root cause

`dispatch()` (`internal/engine/engine.go` ~line 685) builds `StepRequest.Inputs` from `s.preResolvedInputs[st.ID]`. That map is populated in exactly one place — `handle(userInputMsg)` line 824 — only for `from="user"` inputs. For every other input type (file paths, `@step.field` refs, bare `@step` refs), `preResolvedInputs[st.ID]` is never set, so the step always receives an empty `Inputs` slice.

The comment in `internal/engine/executor.go` line 17 states the unfulfilled contract:
```go
// @ref inputs are resolved to paths / inlined values before dispatch.
```

### `ResolvedInput` type (executor.go lines 43–46)

```go
type ResolvedInput struct {
    Ref   workflow.Input // original input declaration (Inline flag used by runner)
    Value string         // resolved path, or inlined content when Ref.Inline is true
}
```

`buildAgentPrompt` in `internal/runner/agent.go` lines 336–343 consumes `req.Inputs`:
- Non-inline: writes `inp.Value + "\n"` (agent sees the string, e.g. a file path it can Read)
- Inline: writes `inp.Value + "\n\n"` (content is injected directly into the prompt)

### `workflow.Input` struct (schema.go lines 219–227)

```go
type Input struct {
    Ref      string   // step ID, e.g. "intake"
    RefField []string // dotted field path, e.g. ["areas"] for "@intake.areas"
    Path     string   // literal file path
    Inline   bool     // inject content instead of passing path
    From     string   // "user" for interactive collection
    Label    string   // TUI prompt for from="user"
    As       string   // name hint for from="user"
}
```

`"@intake.areas"` parses to `Input{Ref:"intake", RefField:["areas"]}`.
`"@intake"` parses to `Input{Ref:"intake"}` (no RefField).
`"../../examples/request.md"` parses to `Input{Path:"../../examples/request.md"}`.

### Fix: add `resolveAllInputs` and `buildRequest` to `dispatch()`

Add two new methods to `scheduler` in `internal/engine/engine.go`.

#### `resolveAllInputs(st *workflow.Step)`

Appends `ResolvedInput` entries for every non-user input into `s.preResolvedInputs[st.ID]`. (User inputs are already there from `handle(userInputMsg)`.)

```go
func (s *scheduler) resolveAllInputs(st *workflow.Step) {
    for _, inp := range st.Inputs {
        if inp.From == "user" {
            continue // already collected via prompt flow
        }

        var value string

        switch {
        case inp.Ref != "" && len(inp.RefField) > 0:
            // @step.field — extract from structured output
            depState := s.states[inp.Ref]
            if depState != nil && depState.Result != nil {
                m, ok := s.structured[inp.Ref]
                if !ok && len(depState.Result.Structured) > 0 {
                    if err := json.Unmarshal(depState.Result.Structured, &m); err == nil {
                        s.structured[inp.Ref] = m
                    }
                }
                var cur any = m
                for _, seg := range inp.RefField {
                    obj, ok := cur.(map[string]any)
                    if !ok {
                        cur = nil
                        break
                    }
                    cur, ok = obj[seg]
                    if !ok {
                        cur = nil
                        break
                    }
                }
                switch v := cur.(type) {
                case string:
                    value = v
                case bool:
                    if v {
                        value = "true"
                    } else {
                        value = "false"
                    }
                case nil:
                    // missing field — validation already caught dangling refs; pass empty
                default:
                    if b, err := json.Marshal(v); err == nil {
                        value = string(b)
                    }
                }
            }

        case inp.Ref != "":
            // bare @step — use artifact output file path
            if depState := s.states[inp.Ref]; depState != nil && depState.Result != nil {
                value = depState.Result.OutputPath
            }

        case inp.Path != "":
            value = inp.Path
        }

        s.preResolvedInputs[st.ID] = append(s.preResolvedInputs[st.ID], ResolvedInput{
            Ref:   inp,
            Value: value,
        })
    }
}
```

#### `buildRequest(st *workflow.Step, runID, worktreePath, artifactDir, transcriptPath string) StepRequest`

Replaces the inline struct literal at lines 685–696:

```go
func (s *scheduler) buildRequest(
    st *workflow.Step,
    runID, worktreePath, artifactDir, transcriptPath string,
) StepRequest {
    state := s.states[st.ID]
    return StepRequest{
        RunID:          runID,
        Step:           st,
        Inputs:         s.preResolvedInputs[st.ID],
        Feedback:       s.stepFeedback[st.ID],
        ArtifactDir:    artifactDir,
        Worktree:       worktreePath,
        TranscriptPath: transcriptPath,
        Iteration:      state.Iteration,
        Attempt:        state.Attempt,
    }
}
```

#### Call site in `dispatch()`

Replace lines 685–696 (the `state := …` / `req := StepRequest{…}` block) with:

```go
s.resolveAllInputs(st)
req := s.buildRequest(st, runID, worktreePath, artifactDir, transcriptPath)
```

`delete(s.preResolvedInputs, st.ID)` at line 697 stays unchanged — it cleans up after both user and non-user inputs.

### Assumption: inline flag for @ref.field inputs

When a TOML input is `{ ref = "@intake.areas", inline = true }`, the extracted field value is injected inline (double-spaced in the prompt). When it's a bare string `"@intake.areas"`, `Inline` is false, so the value is written with a single newline. Both are handled correctly because `resolveAllInputs` stores the original `inp` (which carries `Inline`) in `ResolvedInput.Ref`.

---

## Refactor: post-execution handler chain

### Why

`handle(stepDoneMsg)` in `engine.go` lines 714–851 is where every post-execution concern accumulates: result recording, error synthesis, worktree diff capture, validate gate, block_on check, failure policy, loop firing. Adding `fail_when` (a natural future feature) means editing this monolith again. The chain makes each concern an isolated, named, testable function.

### New types (add near top of `engine.go`, after the `sub` type)

```go
// postExecDecision is the signal returned by a post-execution handler.
type postExecDecision uint8

const (
    decisionContinue   postExecDecision = iota // pass to next handler
    decisionFailed                              // step failed — apply failure policy
    decisionNeedsInput                          // step paused for human input (handler handled transition)
)

// postExecHandler is one stage of the post-execution pipeline.
// It inspects a completed step and returns a decision.
// The first non-decisionContinue result stops the chain.
// All state mutation (transitions, events) is performed by the handler itself.
type postExecHandler func(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision
```

### Add `postExecChain` field to `scheduler` struct (line ~342)

```go
postExecChain []postExecHandler
```

### Initialize in `newScheduler()` (line ~385)

```go
postExecChain: []postExecHandler{
    phCaptureWorktreeDiff,
    phRunValidateGate,
    phCheckBlockOn,
},
```

### Handler functions

Create a new file `internal/engine/handlers.go` to keep `engine.go` from growing. Each function is extracted from the existing `handle(stepDoneMsg)` body.

```go
package engine

import "jig/internal/step"
import "jig/internal/workflow"

// phCaptureWorktreeDiff snapshots the worktree diff after every execution
// so downstream review steps always see the most-recent state.
func phCaptureWorktreeDiff(s *scheduler, m stepDoneMsg, _ *workflow.Step) postExecDecision {
    if path, ok := s.worktrees[m.stepID]; ok {
        s.diffs[m.stepID] = captureDiff(path, s.wtBaseSHAs[m.stepID])
    }
    return decisionContinue
}

// phRunValidateGate runs [step.validate] synchronously when present.
// It transitions the step to StatusValidating, emits GateResult, and
// records the gate detail as the failure reason when the gate rejects.
func phRunValidateGate(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision {
    if wfStep == nil || wfStep.Validate == nil {
        return decisionContinue
    }
    from := s.states[m.stepID].Status
    s.transition(m.stepID, from, step.StatusValidating)
    passed, detail := s.runGate(wfStep, s.worktrees[m.stepID])
    s.emit(GateResult{RunID: s.runID, StepID: m.stepID, Passed: passed, Detail: detail})
    if !passed {
        res := s.states[m.stepID].Result
        if res == nil {
            res = &step.Result{}
            s.states[m.stepID].Result = res
        }
        res.Status = step.StatusFailed
        if res.Err == "" {
            res.Err = detail
        }
        return decisionFailed
    }
    return decisionContinue
}

// phCheckBlockOn parks the step at StatusNeedsInput when the block_on
// condition evaluates true against the step's own structured output.
func phCheckBlockOn(s *scheduler, m stepDoneMsg, wfStep *workflow.Step) postExecDecision {
    if wfStep == nil || wfStep.BlockOn == "" {
        return decisionContinue
    }
    if s.evalBlockOn(m.stepID, wfStep) {
        curFrom := s.states[m.stepID].Status
        s.transition(m.stepID, curFrom, step.StatusNeedsInput)
        s.emit(InputRequest{RunID: s.runID, StepID: m.stepID})
        return decisionNeedsInput
    }
    return decisionContinue
}
```

### Rewrite `handle(stepDoneMsg)` body

Replace lines 717–799 (the entire `case stepDoneMsg:` branch body) with:

```go
case stepDoneMsg:
    s.inFlight--

    // Record result and synthesize an error result when the executor
    // returned a Go error rather than a failed Result.
    if m.result != nil {
        s.states[m.stepID].Result = m.result
    }
    if m.err != nil {
        res := s.states[m.stepID].Result
        if res == nil {
            res = &step.Result{Status: step.StatusFailed}
            s.states[m.stepID].Result = res
        }
        if res.Err == "" {
            res.Err = m.err.Error()
        }
    }

    // Raw execution failure short-circuits the chain.
    execFailed := m.err != nil || (m.result != nil && m.result.Status == step.StatusFailed)

    wfStep := s.stepByID(m.stepID)

    if execFailed {
        s.applyFailurePolicy(m.stepID, wfStep)
        break
    }

    // Run the post-execution handler chain.
    decision := decisionContinue
    for _, h := range s.postExecChain {
        if decision = h(s, m, wfStep); decision != decisionContinue {
            break
        }
    }

    switch decision {
    case decisionFailed:
        s.applyFailurePolicy(m.stepID, wfStep)
    case decisionNeedsInput:
        // handler already transitioned the step; nothing more to do
    default: // decisionContinue — all handlers passed → step succeeded
        curFrom := s.states[m.stepID].Status
        s.transition(m.stepID, curFrom, step.StatusSucceeded)
        if wfStep != nil && wfStep.Loop != nil {
            s.fireLoop(m.stepID, wfStep)
        }
    }
```

**Important**: the `execFailed` path now uses `break` (not a nested else) because this is a `switch` statement body. Verify the surrounding `switch m := msg.(type)` structure before editing.

---

## Bug 2: `status:"blocked"` silently treated as success

### Fix: add `block_on` to research steps in workflow files

The `block_on` field already supports self-referencing conditions — `intake` uses `block_on = "intake.status == 'needs_info'"` to check its own output. The same pattern applies to the research steps.

In **`examples/feature.toml`** and **`.agents/jig/feature.toml`**, add `block_on` to `research_backend` and `research_frontend`:

```toml
[[step]]
id            = "research_backend"
type          = "agent"
depends_on    = ["intake"]
when          = "intake.status == 'ready'"
skill         = "../../examples/skills/research"
inputs        = ["@intake.areas"]
allowed_tools = ["Read", "Grep", "Glob", "WebSearch"]
block_on      = "research_backend.status == 'blocked'"   # ← add this

[[step]]
id            = "research_frontend"
type          = "agent"
depends_on    = ["intake"]
when          = "intake.status == 'ready'"
skill         = "../../examples/skills/research"
inputs        = ["@intake.areas"]
allowed_tools = ["Read", "Grep", "Glob", "WebSearch"]
block_on      = "research_frontend.status == 'blocked'"  # ← add this
```

When a research agent returns `{"status":"blocked",...}`, the step parks at `StatusNeedsInput` for human intervention rather than silently passing `plan` empty research data.

**Note**: `block_on` causes `StatusNeedsInput` (waits for human input), not `StatusFailed`. This is the correct behavior — a human can supply clarifying context to unblock the research agent and let the run continue. If hard-failing is preferred in the future, a `fail_when` field following the same pattern could be added as a new `phCheckFailWhen` handler in the chain.

---

## Tests to add

File: `internal/engine/engine_test.go`

### Helper: `capturingExec`

A new test executor that records the `Inputs` slice it received per step, allowing assertions:

```go
type capturingExec struct {
    testExec
    mu     sync.Mutex
    inputs map[string][]ResolvedInput // stepID → Inputs received
}

func (e *capturingExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
    e.mu.Lock()
    if e.inputs == nil {
        e.inputs = make(map[string][]ResolvedInput)
    }
    e.inputs[req.Step.ID] = req.Inputs
    e.mu.Unlock()
    return e.testExec.Execute(ctx, req, rep)
}
```

### `TestScheduler_RefFieldInput`

Step A returns `{"areas":["backend","frontend"]}` as structured output. Step B declares `inputs = ["@a.areas"]`. Assert B's executor receives `Value = '["backend","frontend"]'`.

```go
func TestScheduler_RefFieldInput(t *testing.T) {
    const toml = `
[workflow]
name = "ref-field-test"
version = "0.1"

[[step]]
id   = "a"
type = "agent"
skill = "a"

  [step.schema]
  areas = { list = "text" }

[[step]]
id         = "b"
type       = "agent"
skill      = "b"
depends_on = ["a"]
inputs     = ["@a.areas"]
`
    wf, err := workflow.Decode(toml, "")
    if err != nil { t.Fatal(err) }

    inner := &structuredExec{
        testExec:  testExec{outcomes: map[string]testOutcome{"a": {delay: delay}, "b": {delay: delay}}},
        stepID:    "a",
        sessionID: "sess-1",
        responses: []string{`{"areas":["backend","frontend"]}`},
    }
    exec := &capturingExec{testExec: inner.testExec}
    // wire structuredExec on top of capturingExec so we can capture and inject
    // ... (see note below about wrapping)

    // Assert exec.inputs["b"][0].Value == `["backend","frontend"]`
}
```

**Note on wrapping**: `structuredExec` embeds `testExec` directly. For this test, you'll need a combined executor that both injects structured output for step A AND captures inputs for step B. The simplest approach is a new `inputCapturingStructuredExec` that embeds `structuredExec` and captures `req.Inputs`.

### `TestScheduler_PathInput`

Step B declares `inputs = ["testdata/request.md"]`. Assert executor receives `ResolvedInput{Value:"testdata/request.md"}`.

### `TestScheduler_BareRefInput`

Step A produces `OutputPath = "/tmp/out.txt"`. Step B declares `inputs = ["@a"]` (no field). Assert executor receives `ResolvedInput{Value:"/tmp/out.txt"}`.

To inject `OutputPath`, add a new test executor variant that sets `result.OutputPath` on a scripted step.

### `TestPostExecHandler_ValidateGate` (unit test for `phRunValidateGate`)

Build a minimal `scheduler` with a step that has a `[step.validate]` block, call `phRunValidateGate` directly, assert `decisionFailed` when gate fails and `decisionContinue` when it passes. This tests the handler in isolation without running a full workflow.

### `TestPostExecHandler_BlockOn` (unit test for `phCheckBlockOn`)

Inject `Result.Structured = []byte(`{"status":"blocked"}`)` into the scheduler state, set `wfStep.BlockOn = "mystep.status == 'blocked'"`, call `phCheckBlockOn`, assert `decisionNeedsInput`.

---

## Files to create/modify

| File | Action | What changes |
|---|---|---|
| `internal/engine/engine.go` | Modify | Add `postExecDecision` type, `postExecHandler` type; add `postExecChain []postExecHandler` to `scheduler` struct; initialize chain in `newScheduler()`; add `resolveAllInputs()` and `buildRequest()` methods; call them in `dispatch()`; replace `handle(stepDoneMsg)` body with chain-driven version |
| `internal/engine/handlers.go` | **Create new** | `phCaptureWorktreeDiff`, `phRunValidateGate`, `phCheckBlockOn` |
| `internal/engine/engine_test.go` | Modify | Add `capturingExec`, `TestScheduler_RefFieldInput`, `TestScheduler_PathInput`, `TestScheduler_BareRefInput`, `TestPostExecHandler_ValidateGate`, `TestPostExecHandler_BlockOn` |
| `examples/feature.toml` | Modify | Add `block_on` to `research_backend` and `research_frontend` steps |
| `.agents/jig/feature.toml` | Modify | Same as above |

---

## Verification commands

```bash
# Run engine tests
go test ./internal/engine/... -v

# Run full suite
go test ./...

# Validate updated workflow files
go run ./cmd/jig validate examples/feature.toml
go run ./cmd/jig validate .agents/jig/feature.toml

# Format and vet
gofmt -l -w ./internal/engine/
go vet ./...
```

---

## Assumptions and gotchas

1. **`evalGuard`'s structured cache is shared** — `resolveAllInputs` deliberately reuses `s.structured[inp.Ref]` (same cache as `evalGuard`) for consistency and to avoid double-unmarshal. This is safe because the scheduler is single-writer; the cache is invalidated by `delete(s.structured, st.ID)` in `dispatch()` before re-dispatch.

2. **User inputs + non-user inputs coexist** — When a step has both `from="user"` and `@ref` inputs, user inputs land in `preResolvedInputs` after the prompt flow completes (via `handle(userInputMsg)` line 824). `resolveAllInputs` is then called in `dispatch()` and APPENDS the non-user inputs to the same slice. The `from="user"` skip in `resolveAllInputs` prevents double-adding user inputs.

3. **`@ref.field` for lists/objects → inline JSON** — when a field's value is a list or object (e.g., `areas: ["backend","frontend"]`), `resolveAllInputs` JSON-encodes it and the agent receives the JSON string in its prompt. The agent is expected to parse it. This matches the expectation of the skill prompts in `examples/`.

4. **`block_on` → `StatusNeedsInput`, not `StatusFailed`** — this means the run pauses and waits for human input to resume. If the intent is to hard-fail (abort the run when research is blocked), use `on_failure = "abort"` on a downstream command step that checks the condition, or add a future `fail_when` field. The current fix uses `block_on` to match existing patterns.

5. **`execFailed` short-circuit** — the refactored `handle` uses `break` after `applyFailurePolicy` for the `execFailed` path. The surrounding `switch m := msg.(type)` uses implicit fallthrough-free case statements in Go, so `break` exits the `select` case correctly. Double-check this in context.

6. **`handlers.go` package** — the new file is in `package engine` (same package as `engine.go`), so all unexported methods and types on `scheduler` are accessible.

7. **Do not add `decisionSucceeded` as a handler return value** — the plan considered it but removed it. Handlers only declare failure or pausing; success is the default when no handler objects. This keeps the "happy path" implicit and matches Go's zero-value convention.
