# 01-tasks-tui-bordered-screens.md

> Task list for [`01-spec-tui-bordered-screens.md`](./01-spec-tui-bordered-screens.md).
> **Status:** Sub-tasks generated. Planning audit gate
> ([`01-audit-tui-bordered-screens.md`](./01-audit-tui-bordered-screens.md)) is
> next. Do not begin implementation until the audit's REQUIRED gates pass.

## Parent-Task ↔ Spec-Unit Mapping

The spec was grilled into three **Demoable Units of Work**; each is an
independently demonstrable, appropriately-scoped vertical slice, so the parent
tasks map 1:1 to them. Unit 2 bundles the two-panel re-layout with non-blocking
gates deliberately — a two-panel monitor that ignored the existing gate flow
would break navigation, so they must ship together to stay demoable.

| Parent Task | Spec Unit | Demo |
| --- | --- | --- |
| 1.0 | Unit 1 — panel helper + single-box screens | Titled boxes on selector/detail/runs |
| 2.0 | Unit 2 — two-panel monitor + non-blocking gates | Side-by-side Steps/Transcript, focus borders, gate strip |
| 3.0 | Unit 3 — paneled streaming chat client | Conversation + Message panels with folded chrome |

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/panel.go` | **New.** The single pure-presentation `panel(title, body string, width, height int, focused bool) string` helper + a frame-size helper. Home of the manual top-edge compositing (ADR 0001). |
| `internal/tui/panel_test.go` | **New.** Table-driven tests for the panel helper (corners, title, truncation, stable Width/Height). |
| `internal/tui/styles.go` | Add a `Panel` sub-struct to `Styles` (focused/blurred border + title styles) and set it in `DefaultTheme()` from existing `primary`/Iron/`fgMuted` tokens. No new hex, no bare package vars. |
| `internal/tui/selector.go` | Strip bubbles-list chrome (`SetShowTitle(false)`, `SetShowHelp(false)`), wrap the list in a "Workflows" panel, size list to inner area, render own footer below. |
| `internal/tui/selector_test.go` | Extend: assert "Workflows" title on the top edge and that list-internal title/help chrome is absent. |
| `internal/tui/detail.go` | Wrap the viewport body in a panel titled with the workflow name (fallback: file path); size viewport to inner area; footer below the box. |
| `internal/tui/detail_test.go` | **New.** Assert detail title = workflow name, with path fallback when the name is empty. |
| `internal/tui/runs.go` | Add a `viewport.Model`, build rows into it sized to the panel inner area, wrap in a "Runs" panel, add scroll/`resize()` plumbing, footer below the box. |
| `internal/tui/runs_test.go` | **New.** Assert the "Runs" title appears and the selection stays within the framed viewport when scrolling. |
| `internal/tui/monitor.go` | Largest change: replace `monitorMode`/`mode` with a `focus` region field, two-panel `JoinHorizontal` layout via `panel()`, focus-routed key dispatch, eager transcript reload, non-blocking gate strip (ADR 0002), narrow-terminal fallback, transcript-panel-inner-width-driven `rebuildRenderer`. |
| `internal/tui/monitor_test.go` | **New/extend.** Two-panel titles + `tab` focus toggle, non-blocking gate, eager reload. Follows `root_test.go`/`selector_test.go` key-construction style. |
| `internal/tui/chat.go` | Restructure into "Conversation" + "Message" panels; fold `headerView`/`turnIndicatorView`/`statusLineView` into titles + a fatal-error line; input panel owns the frame. |
| `internal/tui/chat_test.go` | **New.** Both chat panel titles appear; focus toggle moves the highlighted border; streaming shows "Message · responding…". |
| `internal/tui/input.go` | `newInputTextarea` is reused by chat/gates; Unit 3 needs the textarea's own border suppressed when the input panel owns the frame (add an option or blank the border style at the chat call site). |
| `docs/adr/0001-manual-border-title-compositing.md` | Reference — cross-linked from the `panel.go` compositing comment. |
| `docs/adr/0002-gates-are-nonblocking-focus-regions.md` | Reference — cross-linked from the monitor gate-routing comment. |
| `docs/specs/01-spec-tui-bordered-screens/proof/` | **New dir.** Destination for the screenshot proof artifacts (`1.0-*`, `2.0-*`, `3.0-*`). |

### Notes

- Tests live alongside code as `internal/tui/*_test.go`; construct keys as
  `tea.KeyPressMsg{Code:…, Text:…}` and drive `Update`, asserting on the returned
  model / `View()` string (per `docs/TESTING.md` and existing `monitor_test.go`).
- Run the suite with `go test ./... -race` for this TUI/concurrency work; finish
  with `gofmt -l -w . && go vet ./...` clean (both are required gates).
- All styling flows from the `styles.go` theme tokens; no hardcoded hex or bare
  package-level style vars at call sites.
- The persistence-off path (`runDir == ""`) must keep working — most monitor
  tests exercise it; do not add a disk dependency.

## Tasks

### [x] 1.0 Reusable titled-panel helper and single-box screens (selector, detail, runs)

Establish the one shared, **pure-presentation** `panel(title, body string, width, height int, focused bool) string` helper in a new `internal/tui/panel.go`, composited per ADR 0001 (omit the top border; hand-build `corner + ─ + styled title + fill dashes + corner`, width-matched with `lipgloss.Width`, over-long titles truncated with `…`). Apply it to the three single-view screens — selector (title "Workflows", bubbles-list chrome stripped, own footer line), detail (title = workflow name, falling back to file path), and runs (title "Runs", gaining a `viewport.Model` sized to the panel's inner area plus scroll-key/`resize()` plumbing). Single-panel screens always render the focused/primary border. New border/title/focus styles live in `styles.go` and derive from existing tokens. Every panel and its inner content re-fits on `WindowSizeMsg`.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui -run TestPanel -v` passes — a table-driven test in `internal/tui/panel_test.go` asserts the rendered helper output contains the rounded corner runes (`╭ ╮ ╰ ╯`) and the title text on the top edge, that an over-long title is truncated with `…`, and that `lipgloss.Width`/`lipgloss.Height` of the result equal the requested outer `width`/`height` (maps FR: pure-presentation helper, manual compositing, truncation, stable dimensions).
- Test: `go test ./internal/tui -run TestSelector -v` passes — asserts the selector `View()` string contains a top-edge title "Workflows" and does **not** contain the bubbles-list internal title/help chrome (maps FR: selector chrome stripped, panel title supplied).
- Test: `go test ./internal/tui -run TestDetail -v` passes — asserts the detail `View()` carries the workflow name on the top edge and falls back to the file path when the name is empty (maps FR: detail titled with workflow name / path fallback).
- Test: `go test ./internal/tui -run TestRuns -v` passes — asserts the runs `View()` carries the "Runs" title on the top edge and that a scroll/cursor key keeps the selection within the framed viewport (maps FR: runs titled panel + scrolling viewport within the frame).
- Screenshot: `docs/specs/01-spec-tui-bordered-screens/proof/1.0-selector.txt` (or `.png`) captured via `go run ./cmd/jig` showing the selector as a rounded box titled "Workflows" with the workflow list inside and no list-internal title/help — demonstrates the titled frame on a chrome-stripped bubbles-list screen.
- Screenshot: `docs/specs/01-spec-tui-bordered-screens/proof/1.0-runs.txt` (or `.png`) showing the runs screen as a rounded box titled "Runs" with a scrolling run list inside and the plain footer hint line below the box (maps FR: runs viewport + footer-below-box).
- CLI: `go test ./internal/tui && go vet ./... && gofmt -l internal/tui` — tests pass, vet clean, gofmt reports no files (maps repo standards: no regression, formatting/vet gates).

#### 1.0 Tasks

- [x] 1.1 In `styles.go`, add a `Panel` sub-struct to `Styles` with `FocusedBorder`/`BlurredBorder` `lipgloss.Style` fields (rounded border, top border omitted) and a `Title` style; set them in `DefaultTheme()` deriving the focused color from `primary` (Charple) and the blurred color from Iron (`hexIron`) — reuse the same token pair behind `Viewport.Focused`/`.Blurred`. No new hex, no bare package var.
- [x] 1.2 Create `internal/tui/panel.go` with `panel(title, body string, width, height int, focused bool) string`: apply the bordered style with the top edge omitted, hand-build the top line (`corner + ─ + " " + styled title + " " + fill dashes + corner`) width-matched with `lipgloss.Width`, left-aligned one cell in from the corner. Truncate an over-long title with `…` so total width never exceeds `width`. Keep total `lipgloss.Width`/`Height` equal to the requested outer dimensions. Add a `panelFrame() (hFrame, vFrame int)` (or document use of `GetHorizontalFrameSize`/`GetVerticalFrameSize`) so callers size bodies without magic numbers. Comment the manual-compositing rationale and cross-reference ADR 0001.
- [x] 1.3 Create `internal/tui/panel_test.go`: table-driven cases asserting the rendered output contains the rounded corner runes (`╭╮╰╯`) and the title text on the top edge; an over-long title is truncated with `…`; and `lipgloss.Width`/`lipgloss.Height` of the result equal the requested `width`/`height` for both focused and blurred. (Proof: FR pure-presentation helper, manual compositing, truncation, stable dimensions.)
- [x] 1.4 In `selector.go`, disable list chrome (`SetShowTitle(false)`, `SetShowHelp(false)`; status bar already off), size the list to the panel's inner area (`width-hFrame`, `height-vFrame-footerHeight`), and in `View()` wrap `m.list.View()` in `panel("Workflows", …, focused=true)` with an externally-rendered footer line below (branch hint text on `m.list.FilterState()`). Re-fit on `WindowSizeMsg`.
- [x] 1.5 In `selector_test.go`, add a case asserting the rendered `View()` contains "Workflows" on the top border edge and does **not** contain the bubbles-list internal title/help chrome. (Proof: selector chrome stripped, panel title supplied.)
- [x] 1.6 In `detail.go`, wrap the viewport body in `panel(name, …, focused=true)` where `name = m.meta.Name` falling back to `m.path`; remove the now-redundant in-body name header line that the panel title carries (keep version/description/path/steps body); recompute `resize()` viewport size against the panel inner area; keep the footer as a plain line below the box.
- [x] 1.7 In `runs.go`, introduce a `viewport.Model` and a `resize()` path (sized to the panel inner area), render the run rows into it, wrap in `panel("Runs", …, focused=true)`, and add scroll plumbing so `j`/`k`/arrows move the cursor and keep it visible within the frame (mirror `ensureCursorVisible`); keep the footer plain line below the box; handle `WindowSizeMsg`.
- [x] 1.8 Run `gofmt -l -w . && go vet ./... && go test ./internal/tui`; capture the selector and runs screenshots into `docs/specs/01-spec-tui-bordered-screens/proof/` (`1.0-selector.*`, `1.0-runs.*`). (Proof: no-regression CLI + screenshots.)
- [x] 1.9 Create `internal/tui/detail_test.go`: assert the rendered `View()` carries the workflow name on the panel's top edge, and that when `m.meta.Name == ""` the title falls back to `m.path`. (Proof: detail title + path-fallback FR.)
- [x] 1.10 Create `internal/tui/runs_test.go`: assert the rendered `View()` carries the "Runs" title on the top edge, and that with more rows than fit the inner height a scroll/cursor key (`j`/down) keeps the selection visible within the framed viewport (selection stays on-screen; border intact). (Proof: runs panel + framed-viewport-scroll FR.)

### [x] 2.0 Two-panel run monitor with focus-aware borders and non-blocking gates

Re-lay the monitor into two `lipgloss.JoinHorizontal`'d titled panels — left "Steps" (step list) and right (title = selected step id, falling back to "Transcript") — shown simultaneously, replacing the `mode`/`modeList`/`modeChat` toggle with a `focus ∈ {Steps, Transcript, Gate}` field that colors only the focused region's border primary. Size panels per Resolved Decision 11 (left `max(32, width/3)`, right the remainder with inner ≥ ~40, frame math via lipgloss helpers), rebuild the glamour wrap width and invalidate the `blockKey` render cache off the **transcript panel's** inner width, and remove the internal title/header rows from `listBody()`/`chatBody()`. Route keys per focused region (`j`/`k` = select vs. scroll; `n`/`N` block cursor; `enter`/`space`/`o` toggles), cycle focus with `tab`/`shift+tab` (+ `left`/`right` alias), and reload the selected step's transcript **eagerly** on every selection change (resetting `chatBlockCursor`/`chatExpandAll`/`chatExpand`). Render each pending gate (review verdict, `block_on` input, AskUserQuestion, `from="user"` prompt) as a **non-blocking full-width strip** beneath the panels per ADR 0002 — auto-focused on arrival, but navigation stays live and existing gate-resolution semantics (verdict digits, multi-select + confirm, `m` compose, textarea submit, cancellation delivering a response so no reporter goroutine hangs) are retained. Provide the narrow-terminal (~<76 col) single-focused-panel fallback. Persistence-off (`runDir == ""`) path unaffected.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui -run TestMonitorTwoPanel -v` passes — constructs a `monitorModel` with a few steps, renders it, and asserts the output contains **both** panel titles ("Steps" and the selected step id) and that a `tea.KeyPressMsg{Code: tea.KeyTab}` toggles which region's border uses the primary style (maps FR: two simultaneous panels, focus-colored borders, focus-switch keymap).
- Test: `go test ./internal/tui -run TestMonitorGateNonBlocking -v` passes — asserts that with a gate pending, a focus-switch key still moves focus (navigation not frozen) and that gate keys resolve the gate delivering the correct response (maps FR + ADR 0002: non-blocking gate, retained resolution semantics).
- Test: `go test ./internal/tui -run TestMonitorEagerReload -v` passes — asserts moving the Steps cursor changes the rendered transcript panel body to the newly-selected step's transcript (maps FR: eager transcript reload + per-step state reset).
- Screenshot: `docs/specs/01-spec-tui-bordered-screens/proof/2.0-two-panel.txt` showing the "Steps" panel and transcript panel side by side with the focused panel's border visibly highlighted; `2.0-gate.txt` showing a review gate strip beneath the panels with focus on the transcript panel; `2.0-narrow.txt` showing the single-panel fallback with an intact border at narrow width (maps FR: side-by-side layout, gate strip, narrow fallback).
- CLI: `go test ./... -race && go vet ./... && gofmt -l .` — full suite passes under the race detector, vet clean, gofmt reports no files (maps repo standards: `-race` for TUI/concurrency work, no regression, formatting/vet gates).

#### 2.0 Tasks

- [x] 2.1 In `monitor.go`, replace the `monitorMode` type and `modeList`/`modeChat` constants with a `focusRegion` type (`focusSteps`, `focusTranscript`, `focusGate`) and rename the `mode` field to `focus` (default `focusSteps`). Remove `chatStep`-as-mode-gate usage; the transcript now always shows the cursor's step.
- [x] 2.2 Add panel-sizing helpers computing the split per Resolved Decision 11: left "Steps" width `= max(32, width/3)`, clamped so the right panel keeps inner width ≥ 40; right width is the remainder. Derive each panel's inner content size from its outer size minus `panelFrame()`; size `m.vp` (Steps) and `m.chatVP` (Transcript) accordingly in `resize()`.
- [x] 2.3 Rewrite `View()` to `lipgloss.JoinHorizontal` a left `panel("Steps", listBody, …, focused = focus==focusSteps)` and a right `panel(rightTitle, chatBody, …, focused = focus==focusTranscript)`, where `rightTitle` is the selected step's id or "Transcript" when nothing is selected. Join the gate strip (task 2.8) beneath, then the footer.
- [x] 2.4 Remove the internal title/header rows now carried by the panel border: drop the leading title/blank lines from `listBody()` (and update `listBodyHeaderLines` / `ensureCursorVisible` row math) and from `chatBody()`.
- [x] 2.5 In `rebuildRenderer()`, compute `wordWrap` and the cache-invalidation key from the **transcript panel's inner width** (not `m.width-4`); track the last transcript inner width and invalidate `chatRendered` when it changes.
- [x] 2.6 Rework key routing into per-focus-region dispatch (after gate handling, task 2.8): `tab`/`shift+tab` cycle focus across regions present (Steps → Transcript → Gate); `left`/`right` alias for moving between the two side-by-side panels. With `focusSteps`, `j`/`k` move the selection cursor; with `focusTranscript`, `j`/`k`/`ctrl+d`/`ctrl+u`/`pgup`/`pgdn` scroll the transcript viewport, `n`/`N` move the block cursor (moved off `tab`), and `enter`/`space` toggle the cursored block, `o` toggles all.
- [x] 2.7 Reload the selected step's transcript **eagerly** on every Steps selection change: on cursor move, set the transcript target to the new step, call `loadChat()`, and reset `chatBlockCursor`/`chatExpandAll`/`chatExpand` — not only on an explicit open.
- [x] 2.8 Build a `gateStrip()` rendering each pending gate (`pendingReview` verdict picker, `pendingInput` textarea, `pendingQuestion` option list, `pendingPrompt` textarea) as a full-width strip beneath the two panels, above the footer. Make gates **non-blocking** (ADR 0002): auto-set `focus = focusGate` on arrival, but let `tab`/`left`/`right` move focus away and back; dispatch keys to the focused region first (a focused gate/textarea consumes them; Steps reads `j`/`k` as select, Transcript as scroll). Retain existing resolution semantics (verdict digits, multi-select toggle+confirm, `m` compose, textarea submit on `enter`, and cancellation delivering the appropriate response so no reporter goroutine hangs).
- [x] 2.9 Add the narrow-terminal fallback: when `width` is below the threshold where both panels meet their minimums (~76 cols: `32 + 40 + border cells`), render only the **focused** panel full-width, with the focus key toggling which panel is shown; the gate strip still renders beneath.
- [x] 2.10 Update `footerView()` hints to reflect the focus-switch keys (`tab`/`shift+tab`, `left`/`right`) per focused region, and verify the `runDir == ""` persistence-off path renders transcript-unavailable states as today (no new disk dependency).
- [x] 2.11 Add `monitor_test.go` cases: `TestMonitorTwoPanel` (both panel titles present; `tea.KeyPressMsg{Code: tea.KeyTab}` toggles which region's border style is primary), `TestMonitorGateNonBlocking` (with a gate pending a focus-switch key still moves focus and gate keys resolve the gate with the correct response, **and** that `esc`/`q` cancellation emits the cancellation response command — e.g. `agentQuestionResponseMsg{answer:"cancelled"}` — so no reporter goroutine hangs), `TestMonitorEagerReload` (moving the Steps cursor changes the rendered transcript body to the newly-selected step). Also assert a second `WindowSizeMsg` re-fits panels with no overflow. (Proof: all three Unit-2 test artifacts + resize.)
- [x] 2.12 Run `go test ./... -race && go vet ./... && gofmt -l .` clean; capture `2.0-two-panel.*`, `2.0-gate.*`, `2.0-narrow.*` screenshots into `proof/`. (Proof: no-regression CLI under race + screenshots.)

### [ ] 3.0 Paneled streaming chat client

Restructure the standalone `chatModel` (reached via `go run ./cmd/jig`) into two titled panels using the same helper and focus-border convention, reusing its existing `focusInput`/`focusOutput` toggle: a "Conversation" output panel and a "Message" input panel (input panel owns the frame; the `newInputTextarea` border is removed to avoid a double box). Fold the loose chrome into titles — turn info into the Conversation title ("Conversation · Turn 2 of 3" when >1 turn), transient "connecting…" that drops once connected (never a persistent "Connected"), and "Message · responding…" on the input title while streaming (no spinner glyph in the border edge). Render a fatal connection/stream error as its own full-width line beneath the panels (the chat's Gate analogue). Remove `headerView`, `turnIndicatorView`, and `statusLineView`. Chat continues to return `tea.View` (standalone root model). All styling flows from existing theme tokens.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui -run TestChatPanels -v` passes — renders `chatModel` and asserts both panel titles ("Conversation", "Message") appear and that toggling focus (the `focusInput`/`focusOutput` key) moves which panel's border uses the primary style (maps FR: two titled panels, focus-border reuse).
- Test: `go test ./internal/tui -run TestChatStreamingTitle -v` passes — asserts that while a turn streams the input panel title reads "Message · responding…" and clears to "Message" when idle, and that turn info appears in the Conversation title when >1 turn exists (maps FR: streaming indicator in title, turn info in title).
- Screenshot: `docs/specs/01-spec-tui-bordered-screens/proof/3.0-chat.txt` showing a "Conversation" panel (turn info in title) above a "Message" panel with the focused panel's border highlighted; `3.0-chat-streaming.txt` showing "Message · responding…" in the input title mid-stream (maps FR: paneled chat, streaming title).
- CLI: `go test ./... && go vet ./...` — full suite passes and vet is clean (maps repo standards: no regression, vet gate).

#### 3.0 Tasks

- [ ] 3.1 In `chat.go`, rewrite `View()` to `lipgloss.JoinVertical` two `panel()` calls: a "Conversation" output panel wrapping `m.viewport.View()` and a "Message" input panel wrapping `m.textarea.View()`, coloring the focused panel's border primary via the existing `focusInput`/`focusOutput` toggle. Remove the `headerView`, `turnIndicatorView`, and `statusLineView` calls from the layout.
- [ ] 3.2 Fold turn info into the Conversation title: render "Conversation · Turn N of M" when `len(m.turns) > 1`, plain "Conversation" otherwise. Show "connecting…" in the title only until `m.connected`, then drop it (never a persistent "Connected").
- [ ] 3.3 Make the input panel own its frame: suppress the textarea's own border for the chat (add an option to `newInputTextarea` or blank the border style at the chat call site) to avoid a double box. Put the streaming indicator in the input panel title ("Message · responding…" while `m.streaming`, "Message" when idle); do not embed the animated spinner glyph in the border edge.
- [ ] 3.4 Render a fatal connection/stream error (`m.fatal`) as its own full-width line beneath the panels — the chat's analogue of a gate strip — never inside a truncatable title.
- [ ] 3.5 Recompute both panels' inner sizes on `WindowSizeMsg` (`handleResize`) from `panelFrame()`; keep `chatModel.View()` returning `tea.View` (standalone root model).
- [ ] 3.6 Delete the now-unused `headerView`, `turnIndicatorView`, and `statusLineView` methods.
- [ ] 3.7 Add `internal/tui/chat_test.go`: `TestChatPanels` (both "Conversation" and "Message" titles appear; toggling focus moves which panel's border style is primary) and `TestChatStreamingTitle` (input title reads "Message · responding…" while streaming and "Message" when idle; Conversation title carries turn info when >1 turn). (Proof: both Unit-3 test artifacts.)
- [ ] 3.8 Run `go test ./... && go vet ./...` clean; capture `3.0-chat.*` and `3.0-chat-streaming.*` screenshots into `proof/`. (Proof: no-regression CLI + screenshots.)
