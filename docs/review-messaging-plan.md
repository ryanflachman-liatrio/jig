# Plan: Human-to-agent messaging at review gates (+ fix premature "succeeded")

## Context

Running the `review` workflow (`examples/review.toml`) surfaced two gaps in the
human-in-the-loop model:

1. **No way to reply to the agent.** The `review`-type gate (`sign_off`) only
   lets a human pick an enum verdict (`waive`/`block`). There is no way to send a
   free-text response back to the agent that produced the reviewed content and
   have it keep working. The one existing "feedback" path — `[step.loop]
   feedback = "@review"` — is also broken: it injects the literal *step id*
   string (`[Previous iteration feedback: review]`) instead of any real content
   (`engine.go:1069-1072` → `agent.go:256-259`).

2. **Step reports "succeeded" while still waiting.** `verdictMsg` flips the
   review step to `StatusSucceeded` the instant a verdict key is pressed
   (`engine.go:754-756`). Combined with (1), there is no room for an interaction
   that keeps the step legitimately "awaiting" — any human touch ends the step.

**Decisions (confirmed with the user):**
- Gate actions = **keep the existing enum verdicts, add a universal `message`
  action** that sends free text to the agent.
- On `message`, the agent **resumes its session** (continues its context) and
  re-runs; control returns to the gate. Verdicts still drive routing/finish
  exactly as today ("complete the workflow" = pick a terminal verdict).
- The message↔agent round-trip is **bounded** (`max_messages`, generous
  default) to preserve jig's static termination guarantee; the human normally
  ends early by choosing a verdict.
- Target of the message = **the reviewed agent step**, inferred from
  `review = "@stepid"` — works without a `[step.loop]` block ("auto-feed").

Session continuation is feasible: the SDK exposes `WithResume(sessionID)` /
`WithContinueConversation(true)` (`.../claude-agent-sdk-go@v0.6.22/options.go:244-254`)
and `ResultMessage.SessionID` (`internal/shared/message.go:197`) carries the id.

Scope note: the primitive (resume an agent with a human message, re-run, return
to the gate) is general. This plan wires it through the **review gate** as the
trigger. A standalone "reply to any agent from the monitor" is a follow-on and
is intentionally out of scope here.

---

## Phase A — Capture session id + resume plumbing (foundation)

- **`internal/step/step.go`**: add `SessionID string \`json:"session_id,omitempty"\``
  to `Result`.
- **`internal/runner/agent.go`** (`captureStream`): handle the
  `*claudecode.ResultMessage` case in the `for msg := range msgChan` switch and
  copy `m.SessionID` into the returned `Result`. Persistence-off path unchanged.
- **`internal/engine/executor.go`** (`StepRequest`): add
  `ResumeSessionID string` and `Message string`.
- **`internal/runner/agent.go`** (`Execute`): when `req.ResumeSessionID != ""`,
  append `claudecode.WithResume(req.ResumeSessionID)` and
  `WithContinueConversation(true)`; use `req.Message` as the query prompt instead
  of the freshly-built full prompt. Also append the human `Message` to the
  target step's transcript as a `RoleUser` entry (reuse `appendEntry`) so it
  shows in the chat drill-in.

## Phase B — Engine: bounded review→agent message round-trip

- **`internal/engine/engine.go`**:
  - New `Run.Message(stepID, text string)` (mirror `Resolve`/`ProvideUserInput`,
    `engine.go:158-165`) → sends `humanMessageMsg{reviewStepID, text}`.
  - New `humanMessageMsg` handler (sibling of `verdictMsg`, `engine.go:748-759`):
    guard on `StatusAwaitingReview`; resolve the target = strip `@` and any
    `.field` from `wfStep.Review`; look up the target's last
    `state.Result.SessionID`; increment `s.reviewMessages[reviewID]`; if over the
    cap emit a `RunError`/warning and keep the gate; otherwise stash
    `s.stepMessage[target] = text` + `s.resumeSessions[target] = <sessionID>` and
    reset the loop body (`loopBody(target, reviewID)`, `engine.go:1075`) — target
    and review — back to `StatusPending`. **Do not** transition the review to
    succeeded. When the target re-runs and completes, `nextReady` re-dispatches
    the review gate automatically.
  - `dispatch` (`engine.go:621-632`): set
    `ResumeSessionID: s.resumeSessions[st.ID]`, `Message: s.stepMessage[st.ID]`,
    and `delete` both after dispatch (same pattern as `preResolvedInputs`).
  - New scheduler maps: `resumeSessions map[string]string`,
    `stepMessage map[string]string`, `reviewMessages map[string]int`.
- **`internal/engine/event.go`** (`ReviewRequest`): add `AllowMessage bool`
  (true when the reviewed target is an agent step and the cap is not exhausted)
  so the TUI knows whether to offer the action. Set it in `dispatchReview`
  (`engine.go:975-990`).

## Phase C — Schema: bounded cap (`max_messages`), validated

Per CLAUDE.md, a new field needs parse + default + validate + tests.
- **`internal/workflow/schema.go`**: add `MaxMessages int \`toml:"max_messages"\``
  to the review step (near the `Review` field, `schema.go:171-172`).
- **`internal/workflow` validation**: only valid on `type = "review"`; must be
  `>= 0`; default to a package const (e.g. `defaultReviewMaxMessages = 10`) when
  omitted. Add valid + invalid table-driven cases.
- **`docs/workflow-schema.md`** review-step table (line 126-133): document
  `max_messages` and the `message` action / session-continuation semantics.

## Phase D — TUI: message action + compose box

- **`internal/tui/monitor.go`**:
  - Review overlay (`monitor.go:638-652`): render an extra `[m] message` line
    when `pendingReview.AllowMessage`.
  - Key handling (`monitor.go:229-252`): digit keys → verdict via
    `reviewVerdictMsg` (unchanged). `m` → enter a compose sub-mode that focuses
    the existing `promptTextarea` (reuse the `PromptRequest` setup at
    `monitor.go:443-459`). On submit (`enter`), emit new
    `reviewMessageMsg{runID, stepID, text}`; `esc` cancels back to the picker.
  - Footer (`footerView`, `monitor.go:1059-1073`): add a "composing message"
    state; keep "awaiting review" while the round-trip is in flight.
- **`internal/tui/root.go`**: handle `reviewMessageMsg` → `run.Message(...)`
  (mirror the `reviewVerdictMsg` → `run.Resolve` handler at `root.go:180-183`).

Status fix falls out naturally: the `message` action routes through
`humanMessageMsg` (never succeeds), the agent step shows `running`, then the
gate re-fires `awaiting_review` — so the step is no longer shown "succeeded"
while a human interaction is pending. A terminal verdict still succeeds on
submit (correct).

## Phase E — Fix the broken loop `@ref` feedback (adjacent bug)

- **`internal/engine/engine.go`** (`fireLoop`, `engine.go:1069-1072`): resolve
  `loop.Feedback`'s `@stepid` to the referenced step's real output — its
  `Result.Verdict` for a review, else its output text — and pass *that* as
  `StepRequest.Feedback`.
- **`internal/runner/agent.go:256-259`**: reword the injected block to
  `[Reviewer feedback (<verdict>): <content>]`.

## Files touched (summary)

- `internal/step/step.go` — `Result.SessionID`
- `internal/runner/agent.go` — capture session id; resume; inject message; feedback wording
- `internal/engine/executor.go` — `StepRequest.{ResumeSessionID,Message}`
- `internal/engine/engine.go` — `Run.Message`, `humanMessageMsg`, dispatch wiring, maps, `dispatchReview.AllowMessage`, `fireLoop` @ref fix
- `internal/engine/event.go` — `ReviewRequest.AllowMessage`
- `internal/workflow/*` — `max_messages` field + validation
- `internal/tui/monitor.go`, `internal/tui/root.go` — message action + compose box
- `docs/workflow-schema.md`, `examples/review.toml` (+ `examples/feature.toml` if it exercises review)

## Verification

1. `go build ./cmd/jig && go vet ./... && gofmt -l .`
2. `go run ./cmd/jig validate examples/review.toml` (and `examples/feature.toml`).
3. `go test ./...` — new/updated tests:
   - workflow: `max_messages` valid + invalid (wrong type / on non-review step).
   - engine: `humanMessageMsg` resets target+review to pending, keeps review
     `awaiting_review`, stamps resume session + message; cap exceeded aborts/warns;
     persistence-off path still works.
   - runner: `Execute` adds `WithResume`/`WithContinueConversation` and uses the
     message as prompt when `ResumeSessionID` set; `captureStream` records
     `Result.SessionID`; `fireLoop` @ref resolves to real content.
4. Manual TUI: run `examples/review.toml` with an input that makes
   `review.passed == false` so `sign_off` fires. Confirm: `[m] message` appears;
   sending a message re-runs the `review` agent (step shows `running`, not
   `succeeded`) and returns to the gate with the agent's new output; picking
   `waive`/`block` finishes; the message round-trip stops at `max_messages`.
