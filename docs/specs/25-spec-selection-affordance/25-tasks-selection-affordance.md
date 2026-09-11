# 25-tasks-selection-affordance.md

## Planning Basis

### Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Read implementation/tests before changing behavior; delete replaced code rather than dual-path it; use `internal/tui/shared` for shared presentation; preserve persistence-off; use fabricated fixtures. | none |
| `CLAUDE.md` | yes | Route through documented workflow reading map; do not maintain a second copy of shared rules. | none |
| `docs/ARCHITECTURE.md` | yes | Shared TUI primitives belong in `internal/tui/shared`; child TUI packages must not import root TUI; shared must not import Monitor. | none |
| `docs/CONVENTIONS.md` | yes | Small consumer-owned interfaces; keep APIs narrow; distinguish absent from explicit zero; bounded caches. | none |
| `docs/TUI.md` | yes | Measure cells with `lipgloss.Width` and ANSI-aware helpers; semantic styles owned by `Styles`; complete cache invalidation identity; persistence-off is first class. | none |
| `docs/TESTING.md` | yes | Table-driven synthetic TUI cases; grep- and width-based assertions; targeted race for TUI; supplement with recorded terminal capture. | none |
| `docs/adr/0001-manual-border-title-compositing.md` | yes | One manual titled-border compositor; do not add a second — this slice does not touch the compositor. | none |
| `go.mod` | yes | Go 1.25.12; Charm v2 (Lip Gloss 2.0.5, Glamour 2.0.1, ANSI 0.11.7). No dependency addition needed. | none |
| `mise.toml` | yes | Go 1.25 series toolchain. | none |
| `docs/epics/omp-transcript-parity/epic.md` | yes | CC-7 (no hardcoded glyphs at call sites; route through the shared vocabulary); the recommended first slice after 00 is 03; delivery order lists slice 03 as independent. | none |
| `docs/epics/omp-transcript-parity/slices/03-selection-affordance.md` | yes | Option A recommendation; FR-03.1..FR-03.4; audit `writeItemDetail`/`writeNewCodeCards` and add a regression; do not switch to Option B. | none |
| `docs/specs/25-spec-selection-affordance/25-spec-selection-affordance.md` | yes | FR-03.1..FR-03.6; six proof artifacts; card cache identity retains `selected` but collapses width variants. | none |
| `docs/specs/25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md` | yes | Card cache identity, header-only card contract, prefix flow into `renderToolExchangeCard`. | none |
| `docs/specs/25-spec-status-line-header-grammar/25-spec-status-line-header-grammar.md` | yes | Selection styles only the title slot (FR-02.18); the composed header differs by selection, so the card cache still needs to key on `selected`. | none |
| `CONTRIBUTING.md` | not found | No additional contribution policy present. | none |
| `.github/pull_request_template.md` | not found | No PR template present. | none |
| `.github/workflows/` | not found | No checked-in CI workflow defines additional gates. | none |

The spec resolves every material design choice: Option A (gutter bar,
same visible width in both branches), the shared `CursorBar`/`SelectedBar`
call pattern, and the six FRs. There is no design alternative to record
here beyond Option B, which the spec explicitly defers.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Owns the transcript items view. The selected-branch `+=` on the item prefix is the entire mechanical defect; the fix replaces `+=` with `=` and routes the glyph through `shared.CursorBar`. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Existing tests assert the pre-fix four-cell selected prefix (`"  ▌ ╭"`, `"\n  ▌ ╰"`). Update those expectations and add the new prefix-width, column-parity, line-range, and card-cache-width regressions here. |
| `internal/tui/monitor/monitor_layout.go` | Line 252 carries a stale comment referencing the removed `withBar` helper. Refresh the comment to reference `writeNewCodeCards`'s four-space indent; do not change the math. |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Slice 01 cache tests observe card width per selection. Add a sub-case (or a sibling test) asserting cache size stays stable across selection toggling of a single item. |
| `internal/tui/monitor/monitor_test.go` | Contains the block-cursor navigation tests (`TestMonitorItemNavigationKeepsCursorVisible`, `TestMonitorTallExpandedBlockKeepsHeaderVisible`). Confirm they still pass; extend them only if a regression appears. |
| `internal/tui/shared/icons.go` | Read-only reference: exposes `CursorBar = "▌"` and `BarThick = "▌"`. The fix consumes `CursorBar`; no change needed here. |
| `internal/tui/shared/styles.go` | Read-only reference: `Theme.SelectedBar` is the bold Charple foreground reused from the Steps panel cursor. No change needed here. |
| `docs/specs/25-spec-selection-affordance/25-proofs/` | New sanitized test outputs, terminal captures, and acceptance-check transcripts produced during implementation. |

### Notes

- Keep table-driven tests beside `monitor_transcript_items_view.go`. Do
  not read real `.jig/` data for fixtures or proofs.
- Use `lipgloss.Width` for every width assertion. Never count runes or
  bytes to reason about visible cells.
- The Steps panel already renders its cursor via
  `shared.Theme.SelectedBar.Render(shared.CursorBar)` at three call
  sites in `monitor_steps.go`. That is the reference pattern; the
  Monitor's transcript items view aligns to it.
- Format only changed Go files with `gofmt -w`. Do not rewrite
  unrelated files.
- Run focused Monitor tests during iteration, then the root build,
  test, vet, targeted TUI race (`go test -race ./internal/tui/...`),
  and whitespace checks recorded under Task 3.

### Requirement-to-Test Traceability

| Requirement | Task | Planned Test Artifact |
| --- | --- | --- |
| FR-03.1 | 1.1, 1.4, 1.5 | `monitor_transcript_items_view_test.go` prefix-width table asserting `lipgloss.Width(stripANSI(prefix)) == 2` for selected and unselected across all item kinds at widths 40 and 80. |
| FR-03.2 | 1.1, 1.6 | Grep-based `_test.go` assertion that `monitor_transcript_items_view.go` contains no bare `"▌"` literal used to build the item prefix; the sole surface is `shared.Theme.SelectedBar.Render(shared.CursorBar)`. |
| FR-03.3 | 1.4, 1.7 | Column-parity test that renders the synthetic multi-item page with cursor on item *i* then item *j*, strips ANSI, and asserts every line's leading non-space column matches between the two renders. Covers text, system, thinking, unsupported, header card, and orphan rows. |
| FR-03.4 | 1.7, 2.1 | `chatItemLineRanges` equality test: render the same page twice with different cursor positions and assert the per-item `lineRange` values are byte-identical. |
| FR-03.5 | 2.1, 2.2 | Card-cache stability test: move the cursor onto and off a tool-exchange card; assert `renderToolExchangeCard`'s `available` width is `transcriptInnerW - 2` in both branches and `len(chatItemRendered)` does not grow. |
| FR-03.6 | 1.1, 1.6 | Grep-based `_test.go` assertion that the diff introduces no new `lipgloss.NewStyle` in `internal/tui/monitor/*.go` and no new hex constant anywhere; combined with the FR-03.2 grep, no bare `"▌"` literal appears in transcript-item prefix construction. |

## Tasks

### [ ] 1.0 Apply the Option A fix and cover the new invariant

Replace the `+=` in the transcript items view's selected-prefix branch
with `=`, swap the inline `"▌"` for `shared.CursorBar`, refresh the
stale `withBar` comment in `monitor_layout.go`, update the two existing
test-expectation strings that encode the pre-fix four-cell prefix, and
add the prefix-width, column-parity, and grep-based regression tests
that lock the invariant.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'Test(TranscriptPrefixWidth|TranscriptSelectionColumnParity|TranscriptGutterGlyphSource)' -v` passes the prefix-width equality (both branches = 2 cells) across all item kinds at widths 40 and 80 (FR-03.1); the column-parity assertion between selected-cursor-on-i and selected-cursor-on-j renders (FR-03.3); and the grep-based `"▌"`-literal absence (FR-03.2, FR-03.6).
- Test: `go test ./internal/tui/monitor -run 'TestToolExchangeHeaderCardStatesAndWidths|TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes' -v` still passes with the updated `"▌ ╭"` / `"\n▌ ╰"` expectations, demonstrating the two-cell selected prefix survives the header-card composition unchanged.
- Terminal capture: `docs/specs/25-spec-selection-affordance/25-proofs/25-task-1-selection-column-parity.txt` records the same 80-column synthetic page rendered twice — cursor on item 1, cursor on item 3 — with a `.notes.txt` recording terminal size and cursor position. The two captures shall be byte-identical after stripping ANSI except for the two-cell prefix content (bar vs. spaces).

#### 1.0 Tasks

- [ ] 1.1 In `internal/tui/monitor/monitor_transcript_items_view.go` (around lines 33–36), replace `prefix += shared.Theme.SelectedBar.Render("▌") + " "` with `prefix = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "`. Confirm `prefix` remains scoped to `itemTranscriptBody`; no signature or shared-state change is required.
- [ ] 1.2 Update `internal/tui/monitor/monitor_transcript_items_view_test.go` (around lines 92–95): change `"  ▌ ╭"` → `"▌ ╭"` and `"\n  ▌ ╰"` → `"\n▌ ╰"` in the selected-card assertion, and confirm the existing `lipgloss.Width(row) == width` loop still passes because the card now claims `transcriptInnerW - 2` cells with a 2-cell prefix.
- [ ] 1.3 Update `internal/tui/monitor/monitor_transcript_items_view_test.go` (around line 254) in `TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes`: change `"  ▌ ╭"` → `"▌ ╭"`. Leave `"\n  ╭"` (the unselected assertion) unchanged.
- [ ] 1.4 Add `TestTranscriptPrefixWidthAcrossItemKinds` to `monitor_transcript_items_view_test.go`. Table-drive over item kinds — text, system, thinking, unsupported, tool-exchange header card, orphan tool-result — and over widths 40 and 80. For each row, capture the leading substring up to the first non-prefix character (glyph such as `╭`, `▸`, `▾`, `│`, `U`, etc.) after `stripANSI`, and assert `lipgloss.Width` of that substring is exactly 2 for both selected and unselected renders. Reuse `newMonitorWithSteps` and `setChatPage` with a fabricated `transcript.Page`.
- [ ] 1.5 Add `TestTranscriptSelectionColumnParity` to `monitor_transcript_items_view_test.go`. Build a synthetic page with at least one text item, one thinking item, one tool-exchange card, and one orphan tool-result row. Render with `m.chatItemCursor = 0`, capture, then re-render with `m.chatItemCursor = 2`, capture. Strip ANSI from both and assert that for every line the position of the first non-space character is identical between the two captures. Fail the test with a diff-style report if any line's leading offset differs.
- [ ] 1.6 Add `TestTranscriptGutterGlyphSource` — a grep-based assertion executed as a Monitor package test. Load `internal/tui/monitor/monitor_transcript_items_view.go` via `os.ReadFile`, assert `strings.Contains(src, "shared.Theme.SelectedBar.Render(shared.CursorBar)")` and `!strings.Contains(src, "SelectedBar.Render(\"▌\")")`. This locks the CC-7 discipline for the transcript items view without policing other files.
- [ ] 1.7 Add `TestTranscriptLineRangesStableAcrossSelection`. Render the same synthetic page twice with different `chatItemCursor` values, snapshot `m.chatItemLineRanges` after each render, and assert `reflect.DeepEqual` on the two snapshots. Cover both a collapsed and an expanded item in the fixture so the assertion protects the expand path too.
- [ ] 1.8 In `internal/tui/monitor/monitor_layout.go` (around line 252), update the stale `// "  ▌ " prefix added by withBar` comment to `// "    " prefix added by writeNewCodeCards`. Do not change `insetWidth := wordWrap - 4`; the math is correct because `writeNewCodeCards` still prepends a four-space indent.
- [ ] 1.9 Run `gofmt -w` on every file this task modifies. Run `go build ./cmd/jig` and `go test ./internal/tui/monitor -run 'TestTranscript|TestToolExchange|TestOrphan|TestMonitor' -v` to confirm the focused Monitor slice is green before moving on.

### [ ] 2.0 Confirm the header-card cache collapses to one width variant per item

Extend the slice 01 card-cache regression to prove that moving the
cursor onto and back off a tool-exchange item does not grow
`chatItemRendered`, that the card's `available` width is now
`transcriptInnerW - 2` in both selection branches, and that the
`transcriptRenderKey.selected` component still discriminates the
composed header when slice 02's `TranscriptSelected` style applies to
the title slot.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestToolExchangeCardWidthStableAcrossSelection|TestToolExchangeCardCacheDoesNotGrowOnSelection' -v` demonstrates (a) `renderToolExchangeCard` produces cards of the same visible width in selected and unselected states, and (b) toggling the cursor between two items does not grow the card cache beyond the number of items rendered (FR-03.5).
- Test: existing `TestToolExchangeCardCacheRefreshesOnPageReplacementAndWidthChange` continues to pass unchanged, demonstrating the width-change and page-replacement eviction paths still work — the fix only removes the redundant selection-width variant, not the correct eviction on `setChatPage` or `rebuildRenderer`.

#### 2.0 Tasks

- [ ] 2.1 Add `TestToolExchangeCardWidthStableAcrossSelection` to `monitor_transcript_items_view_test.go`. Render a fabricated exchange page at `m.transcriptInnerW = 60` with `chatItemCursor = 0`, capture the card row's `lipgloss.Width`. Move the cursor off the item (`chatItemCursor = 1` on a second synthetic item), re-render, and assert both captures report the same width — and that this width equals `transcriptInnerW`. Then inspect `chatItemRendered`: assert exactly one card entry per rendered exchange item, and that the entry's `transcriptRenderKey.width` equals `transcriptInnerW - 2` (not `transcriptInnerW - 4`).
- [ ] 2.2 Add `TestToolExchangeCardCacheDoesNotGrowOnSelection` to `monitor_transcript_items_view_test.go`. With two synthetic exchange items on the page, sweep the cursor 0 → 1 → 0 → 1 through four renders. Assert `len(chatItemRendered)` never exceeds 2 (the number of exchange items) across the sweep, and that the two surviving entries' `transcriptRenderKey.selected` values match the current selection each time (so the cache is discriminating on the composed header, per slice 02 FR-02.18, without duplicating a width variant).
- [ ] 2.3 Rerun `go test ./internal/tui/monitor -run 'TestToolExchange' -v` to confirm the pre-existing exchange/card tests still pass alongside the two new regressions.

### [ ] 3.0 Record acceptance evidence and integrated capture

Produce reviewer-safe screenshot evidence at the epic's 80-column target,
run the applicable repository acceptance checks, and record any
limitation explicitly rather than replacing it with a component-only
claim.

#### 3.0 Proof Artifact(s)

- Screenshot pair: `docs/specs/25-spec-selection-affordance/25-proofs/25-task-3-selection-off.{ansi,png}` and `25-task-3-selection-on.{ansi,png}` — the same synthetic Monitor scene at 80 columns, cursor on a text item vs. cursor on a tool-exchange card. Companion `.notes.txt` records terminal size, `transcriptInnerW`, cursor position, and observed border/gutter colors. The two ANSI captures shall be identical after stripping ANSI except for the two-cell prefix.
- CLI: captured outputs for `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/...`, `gofmt -l <changed-go-files>`, and `git diff --check` under `25-proofs/25-task-3-acceptance/`. Any unavailable check is recorded as a limitation in `25-proofs/25-task-3-limitations.md`, not replaced by a substitute.
- Final-diff review recorded in the PR body confirming the change does not touch other epic slices (no header-grammar change, no detail-section conversion, no truncation-vocabulary edit, no diff renderer, no grouped-read tree, no glyph-preset table, no wire/format/harness change, no palette hex additions, no new `lipgloss.NewStyle`).

#### 3.0 Tasks

- [ ] 3.1 Extend the existing `syntheticTranscriptCardVisualPage` (or a sibling fixture) so it contains at least one text item, one running tool exchange, one settled-success tool exchange, and one errored tool exchange. Do not modify slice-01 or slice-02 fixture positions unless required; append new items so their existing assertions remain valid.
- [ ] 3.2 Capture the integrated Monitor at 80×24 via a new `TestSelectionAffordanceVisualProof` (following the pattern in slice 02's `TestStatusLineHeaderVisualProof`). Emit both ANSI and PNG captures for cursor-on-text and cursor-on-card scenes. Record `.notes.txt`.
- [ ] 3.3 Run and capture the applicable repository checks in the order specified above. If a screenshot backend is unavailable in the environment, record only the ANSI capture and note the PNG limitation in `25-task-3-limitations.md` — do not fabricate a PNG.
- [ ] 3.4 Final-diff review: read the cumulative diff and confirm the touched files are limited to `internal/tui/monitor/monitor_transcript_items_view.go`, `internal/tui/monitor/monitor_transcript_items_view_test.go`, `internal/tui/monitor/monitor_layout.go` (comment only), any new `_test.go` companion file scoped to selection-affordance regressions, and files under `docs/specs/25-spec-selection-affordance/`. No `internal/tui/shared/` change, no `palette.go` change, no additional Monitor file change.
