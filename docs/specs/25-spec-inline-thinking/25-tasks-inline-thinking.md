# 25-tasks-inline-thinking.md

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Backend-agnostic Monitor; use `internal/tui/shared` for theme/panel primitives; format only changed Go files with `gofmt -w`; required verification is `go build`, `go test ./...`, `go vet ./...` | none |
| `docs/TUI.md` | yes | Elm-style model/update/command boundary; rebuild width-dependent Glamour renderers on resize and invalidate caches; measure terminal cells with ANSI-aware helpers; keep render caches local with explicit invalidation inputs | none |
| `docs/CONVENTIONS.md` | yes | Place constants/helpers near owning behavior; avoid comments that repeat the next statement; bound inputs, avoid rerendering unchanged documents per message; pre-v1 — remove obsolete paths instead of adding parallel ones | none |
| `docs/TESTING.md` | yes | Table-driven subtests for repeated shapes; model/update/render assertions for TUI changes; avoid sleeps as proof of timing — pulse frame selection must be a pure function of a timestamp, not a live clock read in tests | none |
| `README.md` | yes | High-level status/usage only; no additional coding standards beyond `AGENTS.md` | none |
| `CONTEXT.md` | yes | Domain vocabulary for transcript/step terms (`step`, `run`, `transcript`) used consistently in task descriptions | none |
| `CONTRIBUTING.md` | not found | n/a | n/a |
| `.github/pull_request_template.md` | not found | n/a | n/a |

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_layout.go` | `rebuildRenderer` builds `m.renderer`/`m.fileRenderer`/`m.insetRenderer`; add a `m.thinkingRenderer` variant here with italic/muted `ansi.StyleConfig` sourced from `Theme.Chat.Thinking`. |
| `internal/tui/monitor/monitor_layout_test.go` | New file (none exists today for layout) covering `rebuildRenderer` producing the italic/muted thinking renderer and its rebuild-on-resize/invalidation behavior. |
| `internal/tui/monitor/monitor_model.go` | Declare the new `thinkingRenderer *glamour.TermRenderer` field alongside `renderer`/`fileRenderer`/`insetRenderer`. |
| `internal/tui/monitor/monitor_transcript_items.go` | Extend the oversized condition (currently `kind == transcriptItemText && role == RoleUser`) to also cover `transcriptItemThinking` regardless of role (FR-10.3); add the trailing-running-thinking-item derivation used by the pulse (mirrors `standaloneToolItem`'s running check). |
| `internal/tui/monitor/monitor_transcript_items_test.go` | Table cases for the extended oversized condition (thinking, any role, over/under threshold) and the trailing-running-thinking-item derivation. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Narrow `itemHasDetail` so a thinking item only carries a collapse marker when oversized; rewrite the `transcriptItemThinking` case in `writeTranscriptItem` to route through `m.thinkingRenderer` and to render the pulse label when the item is the running step's trailing thinking item. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Cases for: under-threshold thinking renders fully expanded with no marker; italic/muted styling assertion; oversized thinking collapses/expands via the shared collapse helpers; running trailing item renders the pulse label; settled item renders the plain `◇ reasoning` label. |
| `internal/tui/monitor/monitor_transcript_pulse.go` | New file: pure pulse-frame-selection function (`int(t.UnixMilli()/100) % len(frames)`) plus the default and ASCII-fallback single-cell glyph frame sets, added to `internal/tui/shared`'s icon vocabulary per the spec's Repository Standards. |
| `internal/tui/shared/icons.go` | Add the default and ASCII-fallback pulse glyph frame sets as named constants/vars alongside `IconThinking`, following the existing "pending Slice 14 preset table" qualified-phrasing precedent used for other glyph pairs. |
| `internal/tui/monitor/monitor_transcript_pulse_test.go` | Table-driven tests: equal `lipgloss.Width` across all frames in both glyph sets; deterministic frame index per fixed `time.Time` input with no live clock/sleep; non-empty ASCII-fallback frame for every index. |
| `internal/tui/monitor/monitor_update.go` | `TickMsg` case: mark `dirtyChat = true` (in addition to the existing `dirtyList` on `anyRunning()`) when the visible step has an active running trailing thinking item, so the pulse frame actually advances between engine events. |
| `internal/tui/monitor/monitor_test.go` | Model-level test driving two `TickMsg`s with distinct timestamps against a running step whose trailing item is a thinking block, asserting the rendered glyph changes and the `reasoning` label persists; and a control case proving a non-running/settled item does not dirty chat or animate. |
| `internal/tui/monitor/monitor_search_test.go` | Add/confirm a case that a search query matching thinking prose still locates and expands the item under always-visible rendering (regression check). |
| `internal/tui/monitor/clipboard_test.go` | Confirm existing copy-selected-item coverage for thinking items is unaffected by the renderer/marker changes (regression check, extend if a gap is found). |

### Notes

- Unit tests live alongside the code they test, per existing convention in this package (e.g. `monitor_transcript_items.go` / `monitor_transcript_items_test.go`).
- Use `go test ./internal/tui/monitor/... -run <Test>` for focused runs; `go test ./...` and `go vet ./...` for full verification per `docs/TESTING.md`.
- Format only changed Go files with `gofmt -w`; do not reformat unrelated files.
- Do not introduce a second ticker, package-level animation counter, or config field for ASCII-fallback selection beyond a plain glyph-set constant — the ASCII set is a static frame table, not a runtime-switched mechanism (no such mechanism exists yet anywhere in this codebase; see task-generation assumption).

## Tasks

### [x] 1.0 Render thinking blocks as inline italic/muted prose, gated by the existing oversized-collapse mechanism

#### 1.0 Proof Artifact(s)

- Test: `TestWriteTranscriptItem_Thinking...` (or equivalent table case) in `internal/tui/monitor/monitor_transcript_items_view_test.go` renders a thinking block under `chatTextCollapseBytes` and asserts the output contains the fully rendered markdown body with no collapse marker, demonstrating FR-10.1/FR-10.2.
- Test: a render assertion on the same fixture confirms the output carries the italic ANSI escape and the muted foreground color sourced from `Theme.Chat.Thinking`, demonstrating FR-10.1.
- Test: a thinking block over `chatTextCollapseBytes` renders the shared `buildCollapseSummary` row when collapsed and full markdown when `chatItemExpand` is set, and a round-trip through `setChatPage` (simulating a reload of the same step) preserves the expansion state, demonstrating FR-10.3.

#### 1.0 Tasks

- [x] 1.1 Add `thinkingRenderer *glamour.TermRenderer` to `Model` in `monitor_model.go`, declared next to `renderer`/`fileRenderer`/`insetRenderer`.
- [x] 1.2 In `rebuildRenderer` (`monitor_layout.go`), build `m.thinkingRenderer` from a style copy that sets the document/primitive italic flag and muted foreground to match `Theme.Chat.Thinking`, following the same `chatStyle`-copy pattern used for `fileStyle`; build it once per renderer rebuild, not per render.
  - Implemented as `shared.Theme.ThinkingMarkdown` (built once in `DefaultTheme` via `thinkingMarkdown()`) rather than a monitor-local color literal, per the spec's "styling belongs in `shared.Styles`" standard.
- [x] 1.3 Invalidate/rebuild any cache entries keyed on renderer identity the same way `m.chatRendered`/`m.chatItemRendered` are cleared on `lastTranscriptW` changes, so a resize does not leave stale thinking output.
  - Added a dedicated `chatThinkingRendered` cache (separate from `chatRendered` to avoid blockKey collisions across differently-styled renderers), reset alongside `chatRendered` on width change and step reload.
- [x] 1.4 Extend the oversized condition in `monitor_transcript_items.go` (`buildTranscriptItems`) from `kind == transcriptItemText && role == transcript.RoleUser` to also mark `transcriptItemThinking` oversized when `len(block.Text) > chatTextCollapseBytes`, regardless of role.
  - Extracted as `itemOversized(kind, role, text)`.
- [x] 1.5 Narrow `itemHasDetail` in `monitor_transcript_items_view.go` so `transcriptItemThinking` only reports `true` when `item.oversized`, matching the existing `transcriptItemText` rule, instead of the current unconditional `true` for non-text kinds.
- [x] 1.6 Rewrite the `transcriptItemThinking` case in `writeTranscriptItem`: when not oversized, render `block.Text` through `m.thinkingRenderer` inline at the assistant-text offset (mirroring the non-user `transcriptItemText` branch) with no marker/label; when oversized, reuse `buildCollapseSummary`/`collapseSummaryLabel` for the collapsed row and the full `m.thinkingRenderer` output when `chatItemExpand`/`chatItemExpandAll` is set (Unit 2 will further special-case the running-trailing variant).
  - Implemented as `writeThinkingItem`; the `◇ reasoning` label row is retained (unconditionally, matching FR-10.7's "settled item ... plain, non-animated ◇ reasoning label" and the design note that the pulse only swaps this label's content), with full/collapsed content following it.
- [x] 1.7 Confirm (or adjust) that `chatItemLineRanges`, `chatItemExpand` persistence across `setChatPage`, and `defaultExpandEditCodeItems` behave correctly for thinking items with the narrowed marker rule; add missing cases if the existing tests only assumed the old unconditional-marker behavior.
  - Confirmed via `TestThinkingOversizedCollapseExpandPersistsAcrossReload`; no existing test assumed unconditional-marker behavior (full suite passed unchanged).
- [x] 1.8 Add/update table-driven tests in `monitor_transcript_items_test.go` and `monitor_transcript_items_view_test.go` per the proof artifacts above.
  - Added a dedicated `monitor_transcript_thinking_test.go` instead (file named after the concern it owns, per `docs/CONVENTIONS.md`).
- [x] 1.9 `gofmt -w` changed files; run `go test ./internal/tui/monitor/... -run Thinking` and `go vet ./internal/tui/monitor/...`.

### [x] 2.0 Animate the running step's active thinking item with a fixed-width, frame-loop-quantized pulse

#### 2.0 Proof Artifact(s)

- Test: `lipgloss.Width` is asserted equal across every frame in the default pulse glyph set, and separately across every frame in the ASCII-fallback glyph set, demonstrating FR-10.5.
- Test: a table-driven test calls the pulse-frame-selection function with fixed `time.Time` inputs (no live clock, no sleep) and asserts a deterministic frame index per timestamp with no package-level counter, demonstrating quantization to the existing 100 ms frame loop and FR-10.6 (label always present alongside the glyph).
- Test: a model-level test builds a running step whose trailing item is a thinking block, drives two `TickMsg`s with distinct timestamps, and asserts the rendered pulse glyph differs while the `reasoning` label persists, demonstrating the tick-driven repaint path feeding FR-10.4.
- Test: a settled (non-trailing, or step-not-running) thinking item renders the plain `◇ reasoning` label with no pulse frame and no animation-driving state across repeated ticks, demonstrating FR-10.7.
- Test: the ASCII-fallback glyph set produces a non-empty, single-cell frame for every index, demonstrating FR-10.8.

#### 2.0 Tasks

- [x] 2.1 Add the default and ASCII-fallback pulse glyph frame sets to `internal/tui/shared/icons.go` (or a new `internal/tui/shared` file if warranted), each glyph verified single-cell, following the qualified "pending Slice 14 preset table" phrasing already used for other glyph pairs.
  - Added `PulseFrames`/`PulseFramesASCII` (breathing-dot frames) to `icons.go`.
- [x] 2.2 Create `monitor_transcript_pulse.go` with a pure `pulseFrame(t time.Time, frames []string) string` (or equivalent) computing `int(t.UnixMilli()/100) % len(frames)` — no new field, counter, or ticker.
- [x] 2.3 Add the trailing-running-thinking-item derivation to `monitor_transcript_items.go`/`monitor_transcript_items_view.go`: an item is the active running thinking item when `stepRunning` is true and it is the trailing item in the built sequence with no item after it (mirroring `standaloneToolItem`'s existing running check).
  - Added a `running bool` field on `transcriptItem`, set at the end of `buildTranscriptItems`.
- [x] 2.4 In `writeTranscriptItem`'s thinking case, render `<pulse glyph> reasoning` in place of the static label when the item is the active running one, using the current tick's timestamp (threaded from the model, e.g. via a `m.lastTick`/render-time argument) to select the frame; otherwise render the plain `◇ reasoning` label from Unit 1 unchanged.
  - `m.lastTick` added to `Model`, updated in the `TickMsg` handler; `writeThinkingItem` selects the glyph from it when `item.running`.
- [x] 2.5 Select the ASCII vs. default glyph set using the same qualification other pending glyphs already use in this package (Unicode by default; ASCII-fallback configuration where already supported).
  - No ASCII-fallback runtime switch exists anywhere in this codebase today (verified during planning); production code uses `shared.PulseFrames` unconditionally. `shared.PulseFramesASCII` is defined and tested (`TestPulseFramesAreSingleCell`/`TestPulseFramesASCIINonEmpty`) so it is ready for Slice 14's preset table, matching this task list's stated assumption.
- [x] 2.6 In `monitor_update.go`'s `TickMsg` case, set `dirtyChat = true` when the currently visible step has an active running trailing thinking item (in addition to the existing `dirtyList` on `anyRunning()`), so `chatBody()` is re-invoked each tick and the pulse actually advances; keep this gated (not a blanket `dirtyChat` on every tick) to avoid rerendering the transcript when no pulse is visible, per the "avoid rerendering unchanged documents" convention.
  - Implemented via a new `hasActiveThinkingPulse()` helper on `Model`.
- [x] 2.7 Add/update tests: `monitor_transcript_pulse_test.go` (width/determinism/ASCII-fallback), `monitor_transcript_items_test.go`/`_view_test.go` (running vs. settled rendering), `monitor_test.go` (tick-driven glyph change + dirty-chat gating).
  - Running/settled and tick-driven cases added to `monitor_transcript_thinking_test.go` alongside Unit 1's tests instead of a separate `monitor_test.go` addition, keeping all thinking-item behavior in one file per `docs/CONVENTIONS.md`'s "name files after the concern they own."
- [x] 2.8 `gofmt -w` changed files; run `go test ./internal/tui/monitor/... -run Pulse` and `-run Tick`, and `go vet ./internal/tui/monitor/...`.

### [ ] 3.0 Verify no regression to selection, expansion, copy, search, and line-range behavior for thinking items

#### 3.0 Proof Artifact(s)

- Test: existing `chatItemLineRanges`, selection-cursor, and clipboard/copy tests covering thinking items pass unchanged after Units 1-2 land, run via `go test ./internal/tui/monitor/...`.
- Test: a search test confirms a query matching thinking prose still locates and expands the item under the new always-visible rendering, demonstrating no regression from the prior collapsed-stub default.
- CLI: `go build ./cmd/jig && go test ./... && go vet ./...` output demonstrates no repository-wide regression, with `gofmt -l` reporting no diffs on changed files.

#### 3.0 Tasks

- [ ] 3.1 Review and, if needed, extend `monitor_search_test.go` so a search match on thinking prose still locates/expands the item under default-visible rendering (search previously operated on collapsed stubs).
- [ ] 3.2 Review and, if needed, extend `clipboard_test.go` so copy-selected-item behavior for a thinking item (settled and running) matches its newly rendered content.
- [ ] 3.3 Confirm `chatItemLineRanges` entries for thinking items remain correct (non-zero height, stable start/end) for under-threshold, oversized-collapsed, oversized-expanded, and running-pulse states.
- [ ] 3.4 Run the full verification pass: `gofmt -l` on all changed files (expect empty output), `go build ./cmd/jig`, `go test ./...`, `go vet ./...`; record results as the task's CLI proof artifact.
- [ ] 3.5 Review the final diff for scope creep (no unrelated file changes) before marking the spec's tasks complete.
