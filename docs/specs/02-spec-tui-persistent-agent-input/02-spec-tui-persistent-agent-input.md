# 02-spec-tui-persistent-agent-input.md

> **Rendering update:** Issue 9 supersedes this completed spec's permanently
> expanded, fixed-height empty strip. The queue, routing, focus, and draft
> guarantees remain; the current monitor uses a compact action bar and opens the
> full gate as a focused overlay, as recorded in
> [ADR 0002](../../adr/0002-gates-are-nonblocking-focus-regions.md).

## Introduction/Overview

The run monitor surfaces human-in-the-loop requests through a **gate strip** at the
bottom of the run detail view. Today that strip shows **one** pending request at a
time (`pendingReview` / `pendingInput` / `pendingQuestion` / `pendingPrompt`) and its
own doc-comment asserts *"at most one gate is pending at a time."* A second request
that arrives while one is displayed is **silently dropped** — so a parallel workflow
can strand a blocked step the user never sees. The strip also appears and disappears
conditionally, so the layout jumps when a request arrives.

This spec replaces that with a **persistent, multi-entry input queue**:

- The gate strip is **always visible** at a **fixed height**, showing a placeholder
  when nothing is pending.
- **Every** human-in-the-loop request — `InputRequest` (`block_on`), `AgentQuestion`
  (AskUserQuestion), `PromptRequest` (`from="user"`), **and `ReviewRequest`** — is
  enqueued as an **entry**; nothing is dropped.
- While the gate holds focus, the user cycles entries with `[` / `]` and answers
  them in any order; each response routes to the exact step (and tool-use ID) it
  came from. `tab` / `shift+tab` always cycle focus regions.
- A `ReviewRequest` entry carries only its verdict picker and message affordance; its
  **diff renders in the Transcript panel** (not in the strip) when its step is
  selected in the Steps list.

The engine event model is unchanged — this is entirely a TUI state-model and
rendering change. The design decisions and their rationale are recorded in
[ADR 0005](../../adr/0005-monitor-input-is-a-persistent-concurrent-queue.md) and the
[Design Decisions & Rationale](#design-decisions--rationale) log at the end of this
doc. Glossary terms (**Gate**, **Input queue**, **Input entry**) live in
[`CONTEXT.md`](../../../CONTEXT.md).

## Goals

- The gate strip is always visible at a fixed height, so users know at a glance where
  blocked steps appear and the layout never jumps when a request arrives or clears.
- Every `InputRequest`, `AgentQuestion`, `PromptRequest`, and `ReviewRequest` is
  enqueued; no request is ever silently dropped, even when several steps block at once.
- The user cycles all pending entries with `[` / `]` and responds in any order;
  `tab` / `shift+tab` consistently cycle focus regions, and a new arrival **never
  steals keyboard focus** from the panel the user is in.
- Each submitted response routes to the exact step (and tool-use ID) of its entry,
  leaving other queued entries undisturbed.
- After an entry is answered (or its step leaves `StatusNeedsInput`), the queue
  auto-advances to the next pending entry; when the queue empties, focus returns to
  the Steps panel.
- A review's raw diff remains the artifact under review, rendered in the Transcript
  panel; the queue entry holds only the verdict/message controls.

## User Stories

**As a user monitoring a parallel workflow**, I want the gate strip always visible so
I always know where to look and am never surprised by a blocked step I missed.

**As a user whose workflow has multiple parallel steps waiting for input**, I want all
blocked steps collected in a navigable queue so I can respond to each without losing
any requests.

**As a user**, I want to choose which blocked step to answer first (by moving backward
or forward through the queue) so I can prioritize without being forced into a fixed
order.

**As a user reading a transcript when a parallel step blocks**, I do not want the
cursor yanked into the input box; I want the queue indicator to update quietly so I
can finish reading and answer when ready.

**As a reviewer**, I want the raw diff shown in the Transcript panel (by selecting the
review step) rather than a paraphrase, so my approval is grounded in the actual change.

**As a user**, I want each answer I submit to reach the correct step so parallel agents
receive the right responses.

## Demoable Units of Work

The units are dependency-ordered; each is independently demoable and testable. Units
1–4 deliver the core queue; Units 5–6 complete review handling and long-question
scrolling.

### Unit 1: Unified input-queue state model & ingestion

**Purpose:** Replace the four single-pointer gate fields with one ordered queue that
ingests all four request kinds and prunes entries whose step is no longer blocked. Pure
state — no rendering or key handling yet.

**Functional Requirements:**
- The system shall maintain `inputQueue []pendingInputEntry` and `activeInputIdx int`
  on `monitorModel`, replacing `pendingInput`, `pendingQuestion`, `pendingPrompt`,
  `pendingReview`, and the loose `questionIdx` / `questionSelected` / `questionAnswers`
  fields (which move into the entry).
- The system shall define `pendingInputEntry` with a `kind` discriminator
  (`inputKindRequest` / `inputKindQuestion` / `inputKindPrompt` / `inputKindReview`),
  the routing IDs (`stepID`, and `toolUseID` for questions), the typed payload pointer
  for that kind, the per-entry `draft` string, and the per-entry question-flow state
  (`questionIdx`, `questionSelected`, `questionAnswers`) — see
  [State model](#state-model).
- The system shall append a new entry on `engine.InputRequest`, `engine.AgentQuestion`,
  `engine.PromptRequest`, and `engine.ReviewRequest`, regardless of how many entries are
  already queued, and shall **not** change `m.focus` on arrival (no focus-stealing).
- On `engine.StepStatus` transitioning a step out of `step.StatusNeedsInput`, the
  system shall remove every queue entry for that step and clamp/advance `activeInputIdx`
  to a valid index (see Unit 3 for the advance rule).
- `hasGate()` shall be redefined as `len(m.inputQueue) > 0`; the `reviews` map
  (retained review requests per step) is still populated on `ReviewRequest` for the
  Transcript diff view (Unit 5).
- All queue mutation shall be panic-safe on the empty queue and on `activeInputIdx`
  out of range.

**Proof Artifacts:**
- Unit test: three `InputRequest` events for distinct steps produce `len(inputQueue)==3`
  in arrival order, with `activeInputIdx==0` and `m.focus` unchanged from its prior value.
- Unit test: a `ReviewRequest` and an `AgentQuestion` for two steps coexist as two
  entries of the correct `kind`.
- Unit test: a `StepStatus` moving a queued step to `StatusRunning`/`StatusFailed`
  removes exactly that step's entry and leaves `activeInputIdx` valid.

### Unit 2: Persistent, fixed-height panel & empty state

**Purpose:** The gate strip renders at all times at a reserved fixed height, with a
placeholder when the queue is empty, so the Steps and Transcript panels size once at
startup and never jump.

**Functional Requirements:**
- `gateStrip()` shall always return a rendered panel (remove the early `return ""`);
  when `len(inputQueue) == 0` it shall render a placeholder header (`No pending agent
  inputs`) and a blurred, inert textarea.
- The strip shall occupy a **fixed** body height (see [Layout height](#layout-height))
  regardless of content; `resize()` shall reserve this constant instead of measuring
  `lipgloss.Height(m.gateStrip())` at `monitor.go:827`.
- The empty strip shall be **visible but not focusable**: `cycleFocus` shall keep its
  `len(inputQueue) > 0` guard so `tab` skips the gate when the queue is empty.
- The reserved height shall be derived, not magic — the textarea case (label + 4-row
  textarea + the `[N/M]` header) plus `theme.Viewport.Blurred.GetVerticalFrameSize()`.

**Proof Artifacts:**
- Screenshot: run monitor with no blocked steps shows the strip with the placeholder
  and a grayed-out textarea.
- Screenshot pair: before/after the first `InputRequest` arrives, the Steps and
  Transcript panels are byte-for-byte the same height (no layout shift).
- Unit test: with an empty queue, `cycleFocus(+1)` from Transcript returns `focusSteps`
  (the gate is skipped).

### Unit 3: Queue navigation, focus & draft preservation

**Purpose:** With the gate focused, `[` / `]` cycle entries; `tab` / `shift+tab`
always cycle focus regions; `left` / `right` remain panel-focus aliases; per-entry
draft text survives navigation; the `[N/M]` indicator tracks position.

**Functional Requirements:**
- When `m.focus == focusGate` and `len(inputQueue) > 1`, `GateEntryNav` shall use
  `[` to retreat and `]` to advance `activeInputIdx` modulo `len(inputQueue)`.
  With zero or one entry, these keys shall continue to the focused region so a
  single textarea can accept literal brackets.
- `FocusNext` (`tab`) / `FocusPrev` (`shift+tab`) shall always cycle regions via
  `cycleFocus`, including while the Gate is focused.
- `PanelFocus` (`left` / `right`) shall continue to exit the gate to the side panels
  (unchanged; already handled at `monitor.go:298`).
- `esc` shall **blur** the gate — set `m.focus = focusSteps`, keep the entry queued —
  and shall **not** leave the Monitor. Leaving the Monitor stays on `StepsLeave` /
  `TransLeave` from the panels. (Removes the `InputLeave` / `ReviewLeave` → `showRunsMsg`
  behavior from the gate; see [Keybinding changes](#keybinding-changes).)
- On any navigation away from the active entry (`[`, `]`, `tab`, `shift+tab`,
  `left`, `right`, `esc`), the system shall save the current textarea value into
  `inputQueue[activeInputIdx].draft`; on landing on an entry it shall rebuild the
  textarea from that entry's `draft` via `newInputTextarea`.
- The strip shall render a `[N / M]  step-id  (kind)` header line above the entry body,
  styled via a theme token (`theme.Title` or a new semantic token), never a bare
  `lipgloss.NewStyle()`.
- Removing the active entry (submit, decline, or step-status change) shall set
  `activeInputIdx` to the next entry, or clamp to the last entry when the removed one
  was last; emptying the queue shall set `m.focus = focusSteps`.

**Proof Artifacts:**
- Sequential screenshots: two entries; `]` moves `[1/2] → [2/2]`; `[` moves
  `[2/2] → [1/2]` and wraps `[1/2] → [2/2]`.
- Screenshot: type a partial answer in entry 2, `[` to entry 1, then `]` back to
  entry 2 — the partial answer is still present.
- Unit test: `esc` while gate-focused yields `m.focus == focusSteps`, the queue length
  unchanged, and no `showRunsMsg` command emitted.

### Unit 4: Per-kind entry rendering & response routing

**Purpose:** Each entry kind renders and submits correctly from within the queue, and
each response routes to the right step, removing that entry and auto-advancing.

**Functional Requirements:**
- `gateStrip()` shall render per `kind`: `inputKindRequest` / `inputKindPrompt` → label
  + textarea; `inputKindQuestion` → header + question + numbered options (with the
  multi-select `[x]` marks and `question N of M` hint), reading/writing the entry's own
  `questionIdx` / `questionSelected` / `questionAnswers`; `inputKindReview` → numbered
  verdict choices + `[m]` message affordance (no diff — see Unit 5).
- `updateGate` shall dispatch keys by the **active entry's** `kind` (not by the removed
  single-pointer fields): textarea input + `Submit` for request/prompt; digit
  select / `QConfirm` / multi-step advance for questions; `Verdict` digits + `Message`
  compose for reviews.
- On submit, the system shall emit the existing routing message built from the entry's
  stored IDs — `agentInputMsg` (request), `agentQuestionResponseMsg` (question, with
  `toolUseID`), `userInputResponseMsg` (prompt), `reviewVerdictMsg` / `reviewMessageMsg`
  (review) — then remove that entry and auto-advance (Unit 3).
- `q` on an `inputKindQuestion` entry shall deliver `agentQuestionResponseMsg{answer:
  "cancelled"}`, remove that entry, and auto-advance — and shall **not** leave the
  Monitor. Other kinds owe no response and have no per-entry decline.
- The compose-message sub-flow for a review entry (formerly `composingMessage`) shall be
  represented per-entry (e.g. a bool on the entry or reusing `draft`), so composing on
  one review does not affect another entry.

**Proof Artifacts:**
- Log/transcript inspection: two `InputRequest` entries from different steps; answer
  entry 1 then entry 2; each step's transcript shows its own answer (routing correct).
- Unit test: submitting the active entry emits the correct `*Msg` with the entry's
  `stepID`/`toolUseID` and shrinks the queue by one.
- Unit test: `q` on a question entry emits `agentQuestionResponseMsg` with
  `answer=="cancelled"` and no `showRunsMsg`.
- Screenshot: answer both entries in a `[1/2]` queue; queue drops to `[1/1]` then shows
  the empty placeholder, focus back on Steps.

### Unit 5: Review diff in the Transcript panel

**Purpose:** A review's diff renders in the Transcript panel when its step is selected,
independent of queue navigation, so the reviewer sees the raw change.

**Functional Requirements:**
- When the selected Steps-list step is a review step, the Transcript panel shall render
  that step's diff via the existing `writeDiff` on the verbatim (non-glamour) path,
  sourced from the retained `reviews` map. Review steps have no `transcript.jsonl`; this
  is a synthetic Transcript view.
- Queue navigation and Steps-list selection shall remain **independent**: moving to a
  review entry with `[` / `]` shall not move the Steps cursor, and selecting a review
  step shall not change `activeInputIdx`.
- The `inputKindReview` entry body shall include a one-line hint that the diff is shown
  in the Transcript panel when the review's step is selected.

**Proof Artifacts:**
- Screenshot: a review step selected in the Steps list renders its unified diff
  (green/red/hunk styling) in the Transcript panel; the gate entry shows verdict choices
  + `[m]` + the diff-location hint, and no diff.
- Unit test: selecting a review step loads its diff into the transcript view; the queue's
  `activeInputIdx` is unchanged.

### Unit 6: AgentQuestion overflow scrolling

**Purpose:** A long AgentQuestion option list scrolls within the fixed strip height
rather than growing the panel.

**Functional Requirements:**
- When an `inputKindQuestion` entry's rendered options exceed the reserved body rows,
  the strip shall show a scrollable window over the options, driven by an offset stored
  per entry (e.g. `scrollOffset`).
- `↑` / `↓` (and/or `j` / `k`) shall scroll the option window while the gate holds
  focus and the active entry is a question; these keys do not collide with `[` / `]`
  (entries), `tab` / `shift+tab` (focus), `left`/`right` (panel aliases), digit
  selection, `enter`, or `q`.
- Option lists shall **never** be truncated in a way that hides a blind-selectable
  numbered option; scrolling keeps every option reachable.

**Proof Artifacts:**
- Screenshot: an AgentQuestion with more options than fit shows a scrolled window; the
  panel height is identical to the empty-state height (no growth).
- Unit test: scrolling changes the visible option range without changing the strip's
  reserved height.

## Non-Goals (Out of Scope)

1. **Reordering the queue by drag or keyboard sort**: users navigate and respond; they
   do not manually reorder the queue.
2. **Persisting unanswered queue entries across TUI restarts**: if the TUI exits with
   blocked steps, existing engine behavior applies; this spec covers only the in-session
   queue.
3. **Audio or desktop notifications for new queue entries**: the panel is visual-only.
4. **Filtering or searching the queue**: with bounded parallel steps the queue is short
   enough for linear bracket-key navigation.
5. **Changing the engine event model or the request types**: the engine-side protocol is
   unchanged; only the TUI state model and rendering change. In particular, attaching a
   free-text comment *to a verdict* (routed to the loop's `goto` target) would require an
   engine change and is out of scope; the existing verdict + `max_messages` mechanisms
   are reused as-is.
6. **A diff-explainer agent**: feeding a review diff to a producer agent that emits a
   structured, section-by-section explanation is deferred to its own future spec
   (`03-spec-review-diff-explainer`). It is a workflow-authoring feature, and the raw
   diff must remain the artifact under review — an agent's paraphrase must never become
   the thing a human approves.

## Design Considerations

The gate strip becomes a fixed-height third region that is always drawn:

- Reserve a fixed height (see [Layout height](#layout-height)) so the Steps and
  Transcript panels resize once at startup, not on every input event. When the queue is
  empty, show the placeholder in that same space — do not collapse the panel.
- The queue-position indicator (`[N / M]  step-id  (kind)`) is a single styled header
  line above the entry body (`theme.Title` or a new semantic token). Never a bare
  `lipgloss.NewStyle()`.
- Preserve the `theme.Viewport.Focused` / `.Blurred` border conventions; the border is
  primary only when `m.focus == focusGate`.
- Per-entry draft text is stored as a `string` on the entry; the displayed
  `textarea.Model` is rebuilt from it via `newInputTextarea` when switching entries.
- The review diff is **not** in the strip. It renders in the Transcript panel via
  `writeDiff` (`theme.Diff.Add` / `.Remove` / `.Hunk`) when the review step is selected;
  Charm has no first-party diff component, so `writeDiff` is the intended renderer.

## Repository Standards

- New styles follow the existing pattern: add a field to the appropriate `Styles`
  sub-struct in `styles.go`, set it in `DefaultTheme()` from existing color tokens,
  reference as `theme.X` — never a bare `lipgloss.NewStyle()`.
- New `monitorModel` and `pendingInputEntry` fields carry the same concise doc-comment
  style as the existing `pendingInput` / `pendingQuestion` fields.
- No magic-number heights/widths: derive from
  `theme.Viewport.Blurred.GetVerticalFrameSize()` and `m.gateInnerWidth()`. Introduce a
  named constant/helper for the fixed body height (there is no `gateHeight()` today —
  height is currently measured live at `resize()`); do not scatter literals.
- All queue mutation/validation handles the empty queue and out-of-range
  `activeInputIdx` without panicking.
- Tests follow the existing table-driven pattern in the `tui` package (extend
  `monitor_test.go` or add `input_queue_test.go`).

## Technical Considerations

### State model

Replace the four single-pointer gate fields and the loose question-flow fields on
`monitorModel` with:

```go
// inputQueue holds every step currently blocked on a human, in arrival order.
// activeInputIdx is the entry currently shown in the gate strip. hasGate() is
// len(inputQueue) > 0; an empty queue still renders (placeholder) but is not focusable.
inputQueue     []pendingInputEntry
activeInputIdx int
```

```go
type pendingInputKind int

const (
    inputKindRequest  pendingInputKind = iota // block_on InputRequest
    inputKindQuestion                         // AskUserQuestion AgentQuestion
    inputKindPrompt                           // from="user" PromptRequest
    inputKindReview                           // ReviewRequest (verdict + message)
)

type pendingInputEntry struct {
    kind      pendingInputKind
    stepID    string
    toolUseID string // non-empty only for inputKindQuestion

    // Exactly one payload pointer is non-nil, matching kind.
    request *engine.InputRequest
    question *engine.AgentQuestion
    prompt  *engine.PromptRequest
    review  *engine.ReviewRequest

    // draft is the in-progress textarea text (request/prompt, and review compose),
    // preserved across navigation.
    draft string

    // composing is true while composing a message on a review entry.
    composing bool

    // AgentQuestion multi-step flow, preserved per entry (Decision 9).
    questionIdx      int
    questionSelected map[int]bool
    questionAnswers  []string

    // scrollOffset windows a long AgentQuestion option list within the fixed
    // strip height (Unit 6).
    scrollOffset int
}
```

The `reviews map[string]engine.ReviewRequest` field stays: it is the source for the
Transcript diff view (Unit 5) and outlives the queue entry.

### Event handling changes (`handleEngineEvent`)

- `engine.InputRequest` / `engine.AgentQuestion` / `engine.PromptRequest` /
  `engine.ReviewRequest` → build the matching `pendingInputEntry` and append. **Do not
  set `m.focus`** (Decision 6). For `ReviewRequest`, also populate `m.reviews[stepID]`
  as today.
- `engine.StepStatus` where `ev.To != step.StatusNeedsInput` → remove all entries whose
  `stepID == ev.StepID`; if the removed set included or preceded `activeInputIdx`, clamp
  it into range; if the queue is now empty and `m.focus == focusGate`, set
  `m.focus = focusSteps`.

### Key handling changes (`updateGate` — not `handleGateKey`)

`updateGate` (`monitor.go:470`) currently branches on the single-pointer fields; it must
branch on `inputQueue[activeInputIdx].kind`. Additionally:

- Handle `GateEntryNav` before focused-region dispatch: when `m.focus == focusGate`
  and more than one entry exists, `[` / `]` move `activeInputIdx` modulo the queue
  length and rebuild the textarea from the new entry's `draft`.
- Keep `FocusNext` / `FocusPrev` in the top-level focus path so `tab` /
  `shift+tab` move focus consistently from every region.
- Save the current textarea value to the active entry's `draft` on every blur/navigate.
- Submit paths read routing IDs from the active entry, emit the existing `*Msg`, remove
  the entry, and auto-advance.

### Keybinding changes

The intent is to keep the same keys but change what leaving means from within the gate:

- **`esc` (in gate)**: blur to Steps; no `showRunsMsg`. Remove the gate-context
  `InputLeave` / `ReviewLeave` / `QuestionCancel`→runs-list behavior.
- **`q` (question entry)**: decline → deliver `"cancelled"`, remove + advance; stays in
  Monitor.
- **`[` / `]` (in gate, multiple entries)**: previous / next entry, wrapping.
- **`tab` / `shift+tab` (in gate)**: cycle focus regions.
- **`left` / `right` (in gate)**: exit to side panels (unchanged).
- **`StepsLeave` / `TransLeave`**: unchanged — the ways to leave the Monitor.
- `Verdict` (1–9), `Message` (`m`), `QConfirm` (enter/space), digit option select:
  unchanged, now dispatched by the active entry's kind.

Update the footer hint strings to reflect gate-context keys (entries `[/]`, focus
`tab/⇧tab` and `←/→`, `esc` blur, per-kind actions).

### Layout height

Introduce a named fixed body-height constant/helper (there is no `gateHeight()` today;
`resize()` at `monitor.go:823–827` currently measures the rendered strip). `resize()`
must reserve this constant unconditionally — never zero, even on an empty queue — so the
panels size once. AgentQuestion overflow scrolls within it (Unit 6) rather than growing
the strip.

### No engine-side changes required

`SendInput`, `AnswerQuestion`, and `ProvideUserInput` on `Run` already accept a
`stepID`; the queue entries carry the IDs. Verdict/message routing (`reviewVerdictMsg` /
`reviewMessageMsg`) is unchanged.

## Security Considerations

No specific security considerations. All input is local, in-process, and sent to the
same engine event bus as today. No credentials or external calls are involved.

## Success Metrics

1. **No dropped inputs**: a test workflow with 3 parallel `block_on` steps emitting
   `InputRequest` simultaneously shows all 3 as `[1/3]`, `[2/3]`, `[3/3]`.
2. **Correct routing**: each submitted response reaches its originating step, confirmed
   via per-step transcript inspection.
3. **No focus theft**: an arrival while the user is in the Transcript panel bumps the
   `[N/M]` indicator without changing `m.focus`.
4. **Layout stability**: the monitor does not shift layout when the first input arrives
   or the last is answered; the strip occupies a fixed region at all times.
5. **Review diff placement**: a review's diff appears in the Transcript panel (on step
   selection), and the review entry in the strip shows verdict + message controls only.
6. **No regressions**: `go test ./internal/tui/...` passes; existing single-step input,
   prompt, question, and review flows work unchanged (relocated into the queue).

## Design Decisions & Rationale

Resolved in a grilling session on 2026-08-05. The body above reflects these; this log
preserves the rationale and the alternatives rejected. Recorded architecturally in
[ADR 0005](../../adr/0005-monitor-input-is-a-persistent-concurrent-queue.md).

1. **Reviews are a fourth queue entry kind.** Rejected keeping reviews out of the queue
   (a review would still eclipse a concurrent input, only narrowing the "hidden blocked
   step" hole). Chosen: `inputKindReview` in the unified queue so nothing is eclipsed.

2. **A review entry's diff renders in the Transcript panel, not the gate.** The entry
   carries only verdict + `[m]`. Queue navigation and Steps-list selection are
   independent — the reviewer navigates steps freely and sees the raw diff via
   `writeDiff` on the verbatim path. Rejected auto-following the Steps cursor to the
   active review entry (couples two navigations); accepted the tradeoff that the diff is
   reached by selecting the review step, mitigated by an in-entry hint. Charm has no
   first-party diff component, so `writeDiff` + `theme.Diff.*` is the renderer.

3. **The review entry keeps today's engine semantics** (numbered verdicts + `[m]`
   `max_messages`). A comment bundled *with* a verdict was rejected for this spec because
   it needs an engine change (Non-Goal 5).

4. **`[` / `]` cycle entries in-gate; `tab` / `shift+tab` always move focus.**
   Issue 11 superseded the original gate-only meaning of `tab`, which made focus
   behavior depend on the active region. Brackets navigate only when multiple
   entries exist; with a single entry they remain literal textarea input.
   `left` / `right` remain panel-focus aliases.

5. **`esc` blurs to Steps; it does not leave the Monitor.** From an ever-present panel,
   making the reflexive "escape the text field" key yank the user out of the whole run
   view is a trap. Leaving stays on `StepsLeave` / `TransLeave`. `q` on a question still
   delivers `"cancelled"` (so the agent isn't left awaiting a `tool_result`) but no
   longer leaves. Other kinds owe no response, so they have no per-entry decline.

6. **Arrivals never auto-steal focus** (amends ADR 0002's auto-focus). Rejected the
   spec's original "auto-focus when `len==1`" and today's always-steal. With an
   always-visible panel + `[N/M]` indicator, the indicator is the attention mechanism;
   stealing focus only harms the parallel case this spec exists to serve.

7. **Fixed reserved height; AgentQuestion overflow scrolls.** Rejected dynamic-capped
   height (violates layout stability) and truncating option lists (a hidden,
   blind-typeable option is a footgun). AgentQuestion has no textarea, so `↑`/`↓` scroll
   its options within the fixed region. Vertical cost of the always-reserved strip is
   accepted.

8. **Empty panel is visible but not focusable.** Landing on an inert, uneditable panel
   is a dead end, so `cycleFocus` keeps its `len(inputQueue) > 0` guard. Answering the
   last entry auto-returns focus to Steps.

9. **AgentQuestion multi-question state is preserved per entry.** `questionIdx` /
   `questionSelected` / `questionAnswers` move onto `pendingInputEntry`, so a
   partially-answered question flow survives navigating away and back — consistent with
   per-entry draft preservation.
