# 02-tasks-tui-persistent-agent-input.md

Task list for the **persistent, multi-entry input queue** in the run monitor.
Derived from
[`02-spec-tui-persistent-agent-input.md`](02-spec-tui-persistent-agent-input.md);
the six parent tasks map 1:1 to the spec's dependency-ordered Demoable Units of
Work.

Scope reminder: this is a **TUI state-model and rendering change only** — no
engine or request-type changes (spec Non-Goal 5). All work lands in
`internal/tui` (primarily `monitor.go`, plus `styles.go`, `keys.go`, and tests).

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor.go` | The entire feature. Holds `monitorModel` fields (lines ~94–117), `hasGate()` (337), `cycleFocus()` (356), `updateGate()` (470), `advanceQuestion()` (1145), `handleEngineEvent()` (659), `resize()` (823), `gateStrip()` (1057), `chatBody()` review path (1270–1288), `footerView()` (1599), and `View()` (1663). All of these change. |
| `internal/tui/monitor_test.go` | Table-driven TUI tests. Add the queue-ingest, prune, fixed-height, navigation, draft-preservation, routing, review-diff, and scroll tests here (or split queue tests into `input_queue_test.go`). Reuse `newMonitorWithSteps`, `enterChatStep`, and the `key()` helper. |
| `internal/tui/input_queue_test.go` (new, optional) | Optional home for the Unit 1–3 pure-state/navigation tests if `monitor_test.go` grows unwieldy; follow the same table-driven pattern. |
| `internal/tui/styles.go` | Add the `[N / M]  step-id  (kind)` header style token to the appropriate `Styles` sub-struct and set it in `DefaultTheme()` from existing color tokens (or reuse `theme.Title`). Never a bare `lipgloss.NewStyle()`. |
| `internal/tui/keys.go` | Rework gate keybindings: `esc` blurs (drop `InputLeave`/`ReviewLeave`/`QuestionCancel`→runs-list); keep `tab`/`shift+tab` for entry cycling; add `↑`/`↓` (`j`/`k`) option scroll for question entries; update footer help text. |
| `internal/tui/keys_test.go` | Update any keymap assertions affected by the gate-key rework. |
| `internal/tui/input.go` | `newInputTextarea(placeholder, width, rows)` — the shared textarea builder used to rebuild a per-entry textarea from its `draft`. Referenced, not modified. |
| `CONTEXT.md` | Normative glossary (Gate, Input queue, Input entry, Focus). Naming reference only — already updated for this feature. |
| `docs/adr/0005-monitor-input-is-a-persistent-concurrent-queue.md` | Architectural rationale (reviews-as-entries, esc-blurs, no focus-steal, fixed height). Reference only. |

### Notes

- **TUI tests drive the model directly** (per `docs/TESTING.md`): feed `tea.Msg`
  values (`engineEventMsg{event: …}`, `tea.KeyPressMsg{…}`) through `Update` and
  assert on the returned model and its `View()` string. Do not spin up a real
  terminal. Use the existing `key()` / `newMonitorWithSteps` helpers.
- Run TUI/concurrency work under the race detector: `go test ./internal/tui -race`.
- Before finishing any parent task: `gofmt -l -w . && go vet ./... && go test ./internal/tui -race`.
- Screenshot proof artifacts are captured `View()` output written to
  `docs/specs/02-spec-tui-persistent-agent-input/artifacts/*.txt` (plain text —
  no secrets; the monitor renders only local step IDs and diffs).
- `monitorModel` uses **value receivers but shares maps** — a map write in a
  value-receiver method persists. Keep `questionSelected` a `map[int]bool` on the
  entry, but remember the entry lives in a slice: mutate via
  `m.inputQueue[i].field = …`, not on a loop-copy of the entry.

## Tasks

### [x] 1.0 Unified input-queue state model & ingestion

Replace the four single-pointer gate fields (`pendingInput`, `pendingQuestion`,
`pendingPrompt`, `pendingReview`) and the loose question-flow fields
(`questionIdx`, `questionSelected`, `questionAnswers`, `composingMessage`) with a
single ordered `inputQueue []pendingInputEntry` + `activeInputIdx int`. Rewire
`handleEngineEvent` so all four request kinds **append** an entry (never stealing
`m.focus`) and a `StepStatus` leaving `StatusNeedsInput` prunes that step's
entries. Pure state — rendering and key handling still read the old shape at the
end of this task only through the accessor helpers below. (Spec Unit 1.)

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui -run TestInputQueueIngest` passes — three
  `engine.InputRequest` events for distinct steps fed through `Update` yield
  `len(inputQueue)==3` in arrival order, `activeInputIdx==0`, and `m.focus` equal
  to its pre-arrival value (no focus steal). Maps to Unit 1 FR "append …
  regardless of how many entries are already queued … shall not change `m.focus`".
- Test: `go test ./internal/tui -run TestInputQueueMixedKinds` passes — one
  `ReviewRequest` + one `AgentQuestion` for two steps coexist as two entries with
  the correct `kind` (`inputKindReview`, `inputKindQuestion`).
- Test: `go test ./internal/tui -run TestInputQueuePruneOnStatus` passes — a
  `StepStatus` moving a queued step to `StatusRunning` removes exactly that step's
  entry and leaves `activeInputIdx` within `[0, len(inputQueue))`; pruning on an
  empty queue does not panic.
- CLI: `go build ./cmd/jig` succeeds and `go vet ./internal/tui` is clean after
  the old single-pointer fields are removed (no dangling references).

#### 1.0 Tasks

- [x] 1.1 In `monitor.go`, define `pendingInputKind` (`int`) with the four
  `iota` constants `inputKindRequest`, `inputKindQuestion`, `inputKindPrompt`,
  `inputKindReview`, and the `pendingInputEntry` struct exactly as in the spec's
  [State model](02-spec-tui-persistent-agent-input.md#state-model): `kind`,
  `stepID`, `toolUseID`, the four payload pointers (`request`, `question`,
  `prompt`, `review`), `draft`, `composing`, `questionIdx`, `questionSelected`,
  `questionAnswers`, `scrollOffset`. Carry the same concise doc-comment style as
  the existing fields (Repository Standards).
- [x] 1.2 On `monitorModel`, replace the fields at `monitor.go:94–117`
  (`pendingReview`, `composingMessage`, `pendingInput`, `pendingQuestion`,
  `questionIdx`, `questionSelected`, `questionAnswers`, `pendingPrompt`) with
  `inputQueue []pendingInputEntry` and `activeInputIdx int`. Keep the `reviews
  map[string]engine.ReviewRequest` field and `promptTextarea textarea.Model`.
- [x] 1.3 Add small panic-safe helpers on `monitorModel`: `activeEntry() (*pendingInputEntry, bool)`
  returning the entry at `activeInputIdx` (false when the queue is empty or the
  index is out of range) and `removeEntryAt(i int)` that deletes index `i`, then
  clamps/advances `activeInputIdx` per the Unit 3 rule (next entry, or last when
  the removed one was last; `focusSteps` when emptied). All queue reads go
  through these.
- [x] 1.4 Redefine `hasGate()` (`monitor.go:337`) as `return len(m.inputQueue) > 0`.
- [x] 1.5 In `handleEngineEvent`, rewrite the `engine.InputRequest` (730),
  `engine.AgentQuestion` (739), `engine.PromptRequest` (777), and
  `engine.ReviewRequest` (714) cases to build the matching `pendingInputEntry`
  and **append** it to `m.inputQueue`. **Delete the `m.focus = focusGate` line
  from every one of them** (Decision 6 — no focus steal). For `AgentQuestion`,
  keep the immediate `m.steps[idx].status = StatusNeedsInput` badge update and
  initialize the entry's `questionSelected = make(map[int]bool)`. For
  `ReviewRequest`, keep populating `m.reviews[ev.StepID]`.
- [x] 1.6 Rewrite the `engine.StepStatus` clearing logic (`monitor.go:692–712`):
  when `ev.To != step.StatusNeedsInput` (and the terminal-state cases), remove
  **every** queue entry whose `stepID == ev.StepID` via `removeEntryAt`, so a
  resumed/failed/succeeded step drops its entry and `activeInputIdx` stays valid;
  if the queue empties and `m.focus == focusGate`, set `m.focus = focusSteps`.
- [x] 1.7 Write `TestInputQueueIngest`, `TestInputQueueMixedKinds`, and
  `TestInputQueuePruneOnStatus` in `monitor_test.go` (or `input_queue_test.go`)
  following the `newMonitorWithSteps` + `engineEventMsg` pattern; assert on
  `len(m.inputQueue)`, entry `kind`/`stepID`, `activeInputIdx`, and `m.focus`.
- [x] 1.8 `gofmt -l -w . && go vet ./internal/tui && go build ./cmd/jig` clean;
  temporarily stub `gateStrip`/`updateGate`/`footerView` reads against the new
  shape only as far as needed to compile (full rendering lands in 2.0–4.0).

### [x] 2.0 Persistent, fixed-height panel & empty-state placeholder

Make `gateStrip()` always render (remove the early `return ""`); show a
`No pending agent inputs` placeholder + blurred, inert textarea when the queue is
empty. Introduce a named fixed body-height helper and have `resize()`/`View()`
reserve it unconditionally so the Steps/Transcript panels size once. (Spec
Unit 2; Design "Layout height".)

#### 2.0 Proof Artifact(s)

- Screenshot: `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit2-empty-strip.txt`
  (captured `View()`) shows the gate with the `No pending agent inputs`
  placeholder and a grayed/blurred textarea on a run with no blocked steps.
- Test: `go test ./internal/tui -run TestGateFixedHeight` passes — the Steps and
  Transcript panel body heights (from `View()`) are byte-identical before and
  after the first `InputRequest` arrives, proving no layout shift.
- Test: `go test ./internal/tui -run TestCycleFocusSkipsEmptyGate` passes — with
  an empty queue, `cycleFocus(+1)` from `focusTranscript` returns `focusSteps`.

#### 2.0 Tasks

- [x] 2.1 Add a `gateBodyHeight()` helper (or `const gateBodyH`) on `monitorModel`
  that returns the fixed reserved **body** height as the **max of the bounded
  per-kind natural body heights** so no bounded kind clips (F2): the textarea
  case (label row + 4-row textarea + `[N/M]` header) vs. the review case
  (`[N/M]` header + label + verdict choices + `[m]` + diff-location hint). Derive
  each from the same constants (`newInputTextarea(..., 4)` rows, header rows); do
  not hardcode a bare literal (Repository Standards: "no magic-number heights").
  Only the unbounded `AgentQuestion` option list scrolls within this height
  (Unit 6) — document that in the doc-comment alongside the derivation. Verify
  the review verdict list fits this height in the `unit5-review-diff.txt`
  capture (5.4).
- [x] 2.2 Rewrite `gateStrip()` (`monitor.go:1057`): remove `if !m.hasGate() {
  return "" }`. When `len(m.inputQueue) == 0`, render title `Agent input` (or
  similar), a `No pending agent inputs` placeholder line, and a blurred textarea
  (or an empty body sized to `gateBodyHeight()`); the panel border must be blurred
  (`m.focus == focusGate` is false when empty). Otherwise fall through to the
  per-entry rendering (completed in 3.0/4.0). Fit the panel to
  `gateBodyHeight()+vFrame` — a **fixed** height, replacing the current
  `maxGateBodyH`/`lipgloss.Height(body)` measurement.
- [x] 2.3 In `resize()` (`monitor.go:823–827`), replace `gateH :=
  lipgloss.Height(m.gateStrip())` with `gateH := m.gateBodyHeight() + vFrame`
  (the fixed reserved height), so panels never resize on input arrival. Update
  the doc-comment noting the strip is always reserved.
- [x] 2.4 In `View()` (`monitor.go:1669–1700`), the gate is now always non-empty;
  simplify the `if gate != "" { parts = append… }` to always append `gate`, and
  recompute `panelH` from the fixed reserved height (mirror `resize()`).
- [x] 2.5 Confirm `cycleFocus()` (`monitor.go:356–360`) still guards
  `focusGate` behind `if m.hasGate()` (now `len > 0`) so an empty gate is skipped
  by `tab` — no change expected, just verify.
- [x] 2.6 Write `TestGateFixedHeight` (capture `View()`, split into panel lines,
  compare Steps/Transcript heights before/after an `InputRequest`) and
  `TestCycleFocusSkipsEmptyGate`. Capture the empty-strip `View()` to
  `artifacts/unit2-empty-strip.txt` (create the `artifacts/` dir).

### [ ] 3.0 Queue navigation, focus & per-entry draft preservation

With the gate focused, `tab`/`shift+tab` cycle `activeInputIdx` (mod len), not
regions; per-entry `draft` survives navigation; a themed `[N / M]  step-id
(kind)` header renders above the entry body; `esc` blurs to Steps (no
`showRunsMsg`); `left`/`right` still exit to the panels. (Spec Unit 3; Design
"Keybinding changes".)

#### 3.0 Proof Artifact(s)

- Screenshot: `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit3-nav.txt`
  captures three `View()` frames for a two-entry queue — initial `[1 / 2]`, after
  `tab` → `[2 / 2]`, after `shift+tab` from `[1 / 2]` → wraps to `[2 / 2]`.
- Test: `go test ./internal/tui -run TestGateDraftPreservation` passes — typing a
  partial answer into entry 2, `tab` to entry 1, `tab` back restores the text.
- Test: `go test ./internal/tui -run TestGateEscBlurs` passes — `esc` while
  `m.focus == focusGate` yields `m.focus == focusSteps`, `len(inputQueue)`
  unchanged, and no `showRunsMsg` emitted.

#### 3.0 Tasks

- [ ] 3.1 In `Update`'s `tea.KeyPressMsg` block (`monitor.go:289–303`), intercept
  `FocusNext`/`FocusPrev` **before** the `cycleFocus` calls **only when**
  `m.focus == focusGate`: advance/retreat `m.activeInputIdx = (idx ± 1 + n) % n`
  (no-op when `n == 1`), save the outgoing entry's textarea value to its `draft`,
  rebuild `m.promptTextarea` from the new entry's `draft` via `newInputTextarea`,
  `refreshPanels()`, return. When `m.focus != focusGate`, fall through to the
  existing `cycleFocus` handling unchanged.
- [ ] 3.2 Add a `syncActiveTextarea()` helper: saves
  `m.promptTextarea.Value()` into `m.inputQueue[activeInputIdx].draft` (guarded
  for request/prompt/review-compose kinds), used on every navigate/blur. Add its
  inverse `loadActiveTextarea()` that rebuilds `m.promptTextarea` from the active
  entry's `draft` with the correct placeholder/rows for its kind.
- [ ] 3.3 In `PanelFocus` (`left`/`right`, `monitor.go:298`) handling and the
  `esc`-in-gate path, call `syncActiveTextarea()` before leaving so drafts
  persist. `esc` in the gate sets `m.focus = focusSteps` and returns `nil` (no
  `showRunsMsg`) — see 3.5.
- [ ] 3.4 Add a `[N / M]  step-id  (kind)` header line at the top of the non-empty
  `gateStrip()` body, styled with a theme token (reuse `theme.Title` or add
  `theme.GateHeader` in `styles.go` from existing tokens — never a bare
  `lipgloss.NewStyle()`). `N` is `activeInputIdx+1`, `M` is `len(inputQueue)`,
  `kind` is a short label per `pendingInputKind` (`review`/`input`/`question`/`prompt`).
- [ ] 3.5 In `updateGate` (`monitor.go:470`), replace the per-field `InputLeave`/
  `ReviewLeave`/`QuestionCancel`→`showRunsMsg` branches with a single `esc`
  (blur) handler: `syncActiveTextarea()`, `m.focus = focusSteps`,
  `refreshPanels()`, `return m, nil`. `left`/`right` continue to be handled at the
  top level (298) and exit to panels.
- [ ] 3.6 Ensure `removeEntryAt` (1.3) is the single place that advances/clamps
  `activeInputIdx` and rebuilds the active textarea after a removal; emptying the
  queue sets `m.focus = focusSteps`.
- [ ] 3.7 Write `TestGateDraftPreservation` and `TestGateEscBlurs`. Capture the
  two-entry `tab`/`shift+tab` `View()` frames to `artifacts/unit3-nav.txt`.
- [ ] 3.8 (F1) Add a `left`/`right`-exit variant to `TestGateDraftPreservation`
  (or a sibling case): type a partial answer, exit the gate with `right`/`left`,
  re-enter via `tab`, and assert the draft is restored — proving arrow-exit calls
  `syncActiveTextarea()` (task 3.3), not only `tab`.

### [ ] 4.0 Per-kind entry rendering & response routing

`gateStrip()` renders by the active entry's `kind`; `updateGate` dispatches keys
by kind; submits emit the existing routing `*Msg` from the entry's stored IDs,
remove the entry, and auto-advance. (Spec Unit 4.)

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/tui -run TestGateSubmitRouting` passes — two
  `InputRequest` entries from distinct steps; submitting the active entry emits an
  `agentInputMsg` with that entry's `stepID` and shrinks the queue by one;
  submitting the second routes to the second `stepID`.
- Test: `go test ./internal/tui -run TestQuestionCancel` passes — `q` on an
  `inputKindQuestion` entry emits `agentQuestionResponseMsg` with
  `answer=="cancelled"`, removes the entry, advances, and emits no `showRunsMsg`.
- Screenshot: `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit4-drain.txt`
  captures `View()` frames answering a `[1 / 2]` queue down to `[1 / 1]` then the
  empty placeholder with `m.focus == focusSteps`.

#### 4.0 Tasks

- [ ] 4.1 Rewrite the body of `gateStrip()` (`monitor.go:1070–1126`) to `switch
  entry.kind` on the **active** entry: `inputKindRequest`/`inputKindPrompt` →
  label + `m.promptTextarea.View()`; `inputKindQuestion` → header + question +
  numbered options (multi-select `[x]` marks + `question N of M` hint) reading the
  entry's own `questionIdx`/`questionSelected`; `inputKindReview` → numbered
  verdict choices + `[m]` affordance + the diff-location hint (**no diff** — Unit
  5), or the compose textarea when `entry.composing`.
- [ ] 4.2 Rewrite `updateGate` (`monitor.go:470`) to dispatch by
  `entry.kind` via `activeEntry()`. Fold the four former single-pointer branches
  into a `switch entry.kind`, preserving each existing behavior: textarea
  input + `Submit` (request/prompt), digit toggle/select + `QConfirm` + multi-step
  advance (question), `Verdict` digits + `Message` compose (review).
- [ ] 4.3 Update the submit paths to read `runID`/`stepID`/`toolUseID` from the
  entry and emit the unchanged routing messages — `agentInputMsg`,
  `agentQuestionResponseMsg` (with `toolUseID`), `userInputResponseMsg`,
  `reviewVerdictMsg`/`reviewMessageMsg` — then call `removeEntryAt(activeInputIdx)`
  and `loadActiveTextarea()` (auto-advance). Delete `m.focus = focusSteps` from
  individual submits — `removeEntryAt` owns focus-on-empty.
- [ ] 4.4 Rework `advanceQuestion` (`monitor.go:1145`) to operate on the active
  entry's per-entry `questionIdx`/`questionSelected`/`questionAnswers` (mutate via
  `m.inputQueue[activeInputIdx].…`), and on the final answer emit
  `agentQuestionResponseMsg` then `removeEntryAt` + auto-advance (not
  `focusSteps` directly).
- [ ] 4.5 Change the `q`/`QuestionCancel` path so it delivers
  `agentQuestionResponseMsg{answer:"cancelled"}`, `removeEntryAt`, auto-advances,
  and does **not** emit `showRunsMsg` (stays in Monitor). Other kinds have no
  per-entry decline.
- [ ] 4.6 Move the review compose sub-flow onto `entry.composing` (bool on the
  entry): `[m]` sets `entry.composing = true` and builds the compose textarea;
  `ComposeCancel` clears it; submit emits `reviewMessageMsg` and removes the
  entry. Composing on one review must not affect another entry.
- [ ] 4.7 Rewrite `footerView()` gate hints (`monitor.go:1620–1644`) to switch on
  the active entry's kind, and the status line (1607–1618) to read from the queue
  (e.g. "awaiting N input(s)"). Add the gate-context entry-cycle hint
  (`tab/⇧tab entries`, `←/→ panel`, `esc blur`, per-kind actions).
- [ ] 4.8 Write `TestGateSubmitRouting` and `TestQuestionCancel`; capture the
  drain sequence `View()` to `artifacts/unit4-drain.txt`. Update/replace the
  existing `TestMonitorReviewAutoFocusesGate` (auto-focus is removed — Decision 6)
  and any other tests asserting the old single-pointer fields or focus-steal.
- [ ] 4.9 (F1) Write `TestReviewComposeIsolation`: enqueue two `ReviewRequest`
  entries, press `[m]` on the active one and type a message, `tab` to the other
  review entry, and assert the second entry's `composing == false` and its
  `draft` is empty — proving the compose sub-flow is per-entry (task 4.6), not a
  shared model field.

### [ ] 5.0 Review diff rendered in the Transcript panel

When the Steps-list-selected step is a review step, render its diff in the
Transcript panel via `writeDiff` on the verbatim path from the `reviews` map;
keep queue navigation and Steps-list selection independent; add an in-entry hint.
(Spec Unit 5; Decision 2.)

#### 5.0 Proof Artifact(s)

- Screenshot: `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit5-review-diff.txt`
  captures `View()` with a review step selected — the Transcript panel shows the
  unified diff (`theme.Diff.*` styling), and the gate entry shows verdict choices
  + `[m]` + the diff-location hint and no diff.
- Test: `go test ./internal/tui -run TestReviewDiffInTranscript` passes —
  selecting a review step loads its diff (from `reviews`) into the transcript
  view, and `activeInputIdx` is unchanged before vs. after selection.

#### 5.0 Tasks

- [ ] 5.1 Confirm/extend the existing review-diff branch in `chatBody`
  (`monitor.go:1270–1288`): when the selected step has an entry in
  `m.reviews[m.chatStep]` and no transcript entries, `writeDiff(&b, rev.Diff)` on
  the verbatim path. Keep the verdict-choice list out of the Transcript (choices
  live in the gate entry now) — render only the diff (+ a short heading).
- [ ] 5.2 Verify `reloadTranscript`/`updateSteps` selection does **not** touch
  `m.activeInputIdx`, and that `tab` cycling entries (3.1) does **not** call
  `reloadTranscript` or move `m.cursor`. Add a guard/comment asserting the two
  navigations are independent (Decision 2).
- [ ] 5.3 In the `inputKindReview` gate body (4.1), add a one-line hint:
  `diff shown in Transcript — select this step`. Style it with `theme.Chat.Hint`.
- [ ] 5.4 Write `TestReviewDiffInTranscript`: enqueue a `ReviewRequest` with a
  diff, select the review step in Steps, assert `chatBody()`/`View()` contains the
  diff markers and that `m.activeInputIdx` is unchanged. Capture
  `artifacts/unit5-review-diff.txt`. Adapt the existing
  `TestMonitorChatReviewFallback` if its expectations change.

### [ ] 6.0 AgentQuestion option-list overflow scrolling

A long `inputKindQuestion` option list scrolls within the fixed strip height via
a per-entry `scrollOffset`, driven by `↑`/`↓` (`j`/`k`) while the gate is focused
and the active entry is a question, without colliding with existing gate keys.
(Spec Unit 6; Decision 7.)

#### 6.0 Proof Artifact(s)

- Screenshot: `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit6-scroll.txt`
  captures `View()` of an AgentQuestion with more options than fit, showing a
  scrolled window; the panel height equals the empty-state height (no growth).
- Test: `go test ./internal/tui -run TestQuestionScroll` passes — `↓`/`↑` change
  the visible option range (via `scrollOffset`) while the total rendered strip
  height is unchanged, and every option index remains selectable across the scroll.

#### 6.0 Tasks

- [ ] 6.1 In the `inputKindQuestion` branch of `gateStrip()`, compute how many
  option rows fit in `gateBodyHeight()` (minus the header/question/hint rows) and
  render only `options[scrollOffset : scrollOffset+visible]`, with a `▲`/`▼` more
  indicator when clipped. Keep the option's original `[N]` number label so
  blind-typing a number still selects the right option even when scrolled.
- [ ] 6.2 Add a `ScrollUp`/`ScrollDown` (or reuse `Up`/`Down`) key handling in
  `updateGate`'s question branch that adjusts `m.inputQueue[activeInputIdx].scrollOffset`
  within `[0, maxOffset]`; ensure these keys do not collide with `tab` (entries),
  `left`/`right` (exit), digit selection, `enter`/`space` (`QConfirm`), or `q`.
  Add the binding(s) to `keys.go` and the footer hint for question entries.
- [ ] 6.3 Guarantee no option is truncated away: `maxOffset` is bounded so the
  last option is always reachable; digit selection maps to the absolute option
  index regardless of `scrollOffset` (Unit 6 FR "never … hides a
  blind-selectable numbered option").
- [ ] 6.4 Write `TestQuestionScroll`: enqueue an `AgentQuestion` with more options
  than fit, assert `scrollOffset` changes the visible range on `↓`/`↑`, the strip
  height (`lipgloss.Height(m.gateStrip())`) is constant, and a digit still selects
  the correct absolute option after scrolling. Capture `artifacts/unit6-scroll.txt`.
