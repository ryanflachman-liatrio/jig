# 25-tasks-status-line-header-grammar.md

## Planning Basis

### Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Read implementation/tests before changing behavior; delete replaced code rather than dual-path it; use `internal/tui/shared` for shared presentation; preserve persistence-off; use fabricated fixtures. | none |
| `CLAUDE.md` | yes | New styles live in `Styles` set by `DefaultTheme()`; never bare `lipgloss.NewStyle()` in renderers; no hardcoded hex outside `palette.go`. | none |
| `docs/ARCHITECTURE.md` | yes | Shared TUI primitives belong in `internal/tui/shared`; child TUI packages must not import root TUI; the shared package must not import Monitor. | none |
| `docs/CONVENTIONS.md` | yes | Small consumer-owned interfaces; keep APIs narrow; distinguish absent from explicit zero; bounded caches. | none |
| `docs/TUI.md` | yes | Measure cells with `lipgloss.Width` and ANSI-aware helpers; semantic styles owned by `Styles`; complete cache invalidation identity; persistence-off is first class. | none |
| `docs/TESTING.md` | yes | Table-driven synthetic TUI cases; grep- and width-based assertions; targeted race for TUI; supplement with recorded terminal capture. | none |
| `docs/adr/0001-manual-border-title-compositing.md` | yes | One manual titled-border compositor; do not add a second. | none |
| `go.mod` | yes | Go 1.25.12; Charm v2, Lip Gloss 2.0.5, Glamour 2.0.1, ANSI 0.11.7; no dependency addition needed. | none |
| `mise.toml` | yes | Go 1.25 series toolchain. | none |
| `docs/epics/omp-transcript-parity/slices/02-status-line-header-grammar.md` | yes | Four-slot grammar; state is icon+border, not prose; `toolErrorHint` moves out of the title. | none |
| `docs/specs/25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md` | yes | The card frame owns width, `header` is the cache identity, orphan results stay flat, sanitize inputs. | none |
| `CONTRIBUTING.md` | not found | No additional contribution policy is present. | none |
| `.github/pull_request_template.md` | not found | No pull-request template is present. | none |
| `.github/workflows/` | not found | No checked-in CI workflow defines additional gates. | none |

The spec resolves the material choices: shared `RenderStatusLine`, shared
`ToolStatusIcon` returning `(glyph, style)`, `Chat.Tool{Title,Description,
Meta,Badge}` styles, per-tool signature glyphs on settled success only
(CC-4), error hint into `Meta`. Two design alternatives are recorded
explicitly (moving `toolDisplayState` into `shared` vs. a parallel shared
enum with a translation, and reverting write→edit glyph if width fails).

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/shared/status_line.go` | New owner of `StatusLine`, per-slot rendering, separator/filter rules, and newline flattening. |
| `internal/tui/shared/status_line_test.go` | New table-driven contract tests for slot combinations, style application, ANSI/newline handling, and grapheme preservation. |
| `internal/tui/shared/icons.go` | Extended with per-state header icons (`IconStatus*`) and per-tool signature glyphs (`IconTool*`), keeping all glyphs centralized (CC-7). |
| `internal/tui/shared/status_icon.go` | New `ToolStatusIcon` resolver mapping `(state, kind)` → `(glyph, style)`; anti-jitter (CC-4) enforced by making running/pending icons identical. |
| `internal/tui/shared/status_icon_test.go` | New table-driven cases for every state, every documented kind, unknown kinds/states, and running/pending glyph equality. |
| `internal/tui/shared/styles.go` | Adds `Chat.ToolTitle`, `Chat.ToolDescription`, `Chat.ToolMeta`, `Chat.ToolBadge`; wires per-state icon styles derived from `Card.Border*` foregrounds. |
| `internal/tui/shared/styles_test.go` | Verifies all new `Chat.Tool*` styles initialize from expected palette roles. |
| `internal/tui/shared/palette.go` | No new hex expected; changes only if the write signature glyph fallback demands a new token (unlikely). |
| `internal/tui/monitor/monitor_tool_summary.go` | Drops the pre-fused `label`/`preview` fields (or leaves them and stops relying on them); keeps `icon`, `action`, `detail` as the contract. |
| `internal/tui/monitor/monitor_tool_summary_test.go` | Adjusts existing assertions to the pruned surface; adds a regression proving `action` and `detail` remain distinct. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Rewrites the tool-exchange header composition to `RenderStatusLine`; removes the appended state prose; moves `toolErrorHint` into `Meta`; optionally migrates the orphan flat path. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Adds grep-based "no state prose" assertions, icon mapping cases, and selection-only-title cases. |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Extends existing tests so the cache invalidation regression demonstrates a state transition changes the composed `header`. |
| `internal/tui/monitor/monitor_model.go` | Only if `toolDisplayState` migrates to `shared`: retains an alias for existing callers or updates references en-bloc. |
| `docs/specs/25-spec-status-line-header-grammar/25-proofs/` | New sanitized test, gallery, and terminal-capture evidence produced during implementation. |

### Notes

- Keep table-driven tests beside their owning package. Do not read real
  `.jig/` data for fixtures or proofs.
- Use `lipgloss.Width` for every width assertion; do not count runes or
  bytes to reason about visible cells.
- Every new glyph is a `shared.Icon*` constant. Any renderer that still
  references a bare Unicode literal for a status/tool glyph is a
  regression.
- Format only changed Go files with `gofmt -w`. Do not rewrite unrelated
  files.
- Run focused shared and Monitor tests during iteration, then the root
  build, test, vet, targeted TUI race (`go test -race
  ./internal/tui/...`), and whitespace checks recorded under Task 4.

### Requirement-to-Test Traceability

| Requirement | Task | Planned Test Artifact |
| --- | --- | --- |
| FR-02.1  | 1.1, 1.5 | `status_line_test.go` slot-combination table asserting rendered substrings per slot. |
| FR-02.2  | 1.2, 1.5 | `status_line_test.go` separator/filter cases; empty-meta filtering; description colon binding. |
| FR-02.3  | 1.2, 1.5 | `status_line_test.go` newline/CR-flattening cases with `lipgloss.Height == 1`. |
| FR-02.4  | 1.1, 1.5 | Godoc assertion by presence-check plus a "no truncation" test that a very long title is not clipped by `RenderStatusLine`. |
| FR-02.5  | 1.3, 1.6 | `status_icon_test.go` state-only cases; unknown-state fallback. |
| FR-02.6  | 1.4 | Grep-based test that `internal/tui/monitor/*.go` contains no bare status/tool glyph literals, only `shared.Icon*` references. |
| FR-02.7  | 1.4, 1.6 | `icons.go` compile-time symbols and `status_icon_test.go` per-kind cases. |
| FR-02.8  | 1.4, 1.6 | Width-audit test in `status_icon_test.go` asserting every signature glyph measures exactly one cell via `lipgloss.Width`, plus documented fallback if `✍` fails. |
| FR-02.9  | 1.7, 1.8 | `styles_test.go` cases asserting `Chat.ToolTitle`/`ToolDescription`/`ToolMeta`/`ToolBadge` foregrounds; per-state icon styles reuse `Card.Border*` foregrounds. |
| FR-02.10 | 1.7, 1.9 | Grep-based test in shared package asserting no `lipgloss.NewStyle` outside `styles.go` and no hex constants outside `palette.go`. |
| FR-02.11 | 2.1, 2.3, 2.6 | `monitor_transcript_items_view_test.go` renders through `RenderStatusLine`; the removed inline construction is not reachable. |
| FR-02.12 | 2.2, 2.6 | Grep assertion over `stripANSI(body)` for `" failed"`, `" · running"`, `" · incomplete"` returning zero matches. |
| FR-02.13 | 2.1, 2.7 | `monitor_tool_summary_test.go` verifies `icon`/`action`/`detail` remain distinct; `label`/`preview` removal is compile-verified (no non-test callers). |
| FR-02.14 | 2.1, 2.6 | Assert composed title equals `action`, description equals `detail`, and the marker prefix is outside the `RenderStatusLine` output. |
| FR-02.15 | 2.4, 2.6 | Table-driven per-state test asserting the observed glyph via `ToolStatusIcon` and that running/pending share their glyph. |
| FR-02.16 | 2.5, 2.6 | Orphan `Result (unknown origin)` renders through `RenderStatusLine`; or, when deferred, a locked-current-behavior test plus a proof-file limitation note. |
| FR-02.17 | 2.2, 2.6 | Assert `toolErrorHint` output appears in the meta segment of the header row and does not appear inside the title. |
| FR-02.18 | 2.3, 2.6 | Assert `TranscriptSelected` styling is applied only around the title fragment (not the full row); state color is not applied to the full row. |
| FR-02.19 | 2.8, 3.1 | Extend slice-01 cache tests: state transition produces a different composed `header` and refreshes the card. |
| FR-02.20 | 2.9, 3.2 | Persistence-off regression: `RunDir == ""` retains the existing empty state and never enters `RenderStatusLine`. |

## Tasks

### [ ] 1.0 Establish the shared status-line grammar, icon resolver, and styles

Create the reusable `internal/tui/shared` status-line API, the tool
status-icon resolver, the per-tool signature glyph vocabulary, and the
`Chat.Tool*` styles. Demonstrate that the primitive composes each slot
independently, filters empty entries, flattens newlines, preserves
grapheme and ANSI boundaries, and never truncates. Prove the anti-jitter
rule (CC-4) at the vocabulary layer by construction.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/shared -run 'Test(RenderStatusLine|ToolStatusIcon|StatusLineStyles)' -v` passes the slot-combination, separator, filtering, newline flattening, ANSI/grapheme preservation, per-state icon mapping, per-kind signature glyph, unknown-state/kind fallback, running/pending glyph equality, single-cell width audit, and no-truncation cases; this covers FR-02.1–FR-02.10.
- Test: a grep-based repository assertion (documented command, executed as a shared-package test) confirms that renderer files reference `shared.Icon*` constants rather than bare Unicode literals for status and tool glyphs.
- Terminal capture: `docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-1-status-line-gallery.txt` records the sanitized gallery of every state × kind × slot combination at 40/60/90 columns, with per-row `lipgloss.Width` measurements recorded alongside each row.

#### 1.0 Tasks

- [ ] 1.1 Add `internal/tui/shared/status_line.go` with the `StatusLine` struct (`Icon`, `IconStyle`, `Title`, `TitleStyle`, `Description`, `DescriptionStyle`, `Badge`, `BadgeStyle`, `Meta`, `MetaStyle`), documented Go doc comments including the "no truncation" boundary (FR-02.4), and a `RenderStatusLine` implementation that composes slots left-to-right using the spec's separator rules.
- [ ] 1.2 Implement per-slot handling: default `TitleStyle` to `Theme.Chat.ToolTitle`, `DescriptionStyle` to `Theme.Chat.ToolDescription`, and `MetaStyle` to `Theme.Chat.ToolMeta` when the caller supplies a zero-value style; introduce description with `": "` and meta with a single leading space; join meta with `" · "` after filtering entries that are empty or whitespace-only.
- [ ] 1.3 Add `internal/tui/shared/status_icon.go` with `ToolStatusIcon(state, kind string) (glyph string, style lipgloss.Style)`; make running and pending return the same glyph (CC-4), success-with-known-kind return the signature glyph, and every other case fall back to the generic pending/warning/error glyph. Choose one of the alternatives in the spec's Open Questions (moving `toolDisplayState` into shared vs. a parallel shared enum) and note the choice at the top of the file.
- [ ] 1.4 Extend `internal/tui/shared/icons.go` with the `IconStatus*` and `IconTool*` constants documented in the spec; do not introduce a preset table (slice 14's scope). Add a package-level width-audit test (Task 1.6) proving every new glyph is single-width.
- [ ] 1.5 Add table-driven `status_line_test.go` cases covering every FR from 02.1–02.4 and 02.10; include one case whose title contains a valid caller-applied SGR style and assert the styled substring is present in the rendered output; assert `lipgloss.Height` of every rendered row is exactly one line.
- [ ] 1.6 Add `status_icon_test.go` cases for every documented `(state, kind)` combination in the spec, plus unknown state and unknown kind fallbacks; assert running and pending share their glyph; assert every signature glyph measures exactly one cell.
- [ ] 1.7 Extend `internal/tui/shared/styles.go` with `Chat.ToolTitle` (from `fgBase`), `Chat.ToolDescription` (from `fgMuted`), `Chat.ToolMeta` (from `fgDim`), and `Chat.ToolBadge` (reusing `Badge.Neutral`). Ensure `DefaultTheme()` initializes them; do not remove existing chat styles.
- [ ] 1.8 Add `styles_test.go` cases asserting each new `Chat.Tool*` foreground token matches the spec, and that per-state icon styles returned by `ToolStatusIcon` share their foreground with the corresponding `Card.Border*` style (so the header icon and card border read as one indicator).
- [ ] 1.9 Add or extend a grep-based `internal/tui/shared` test that asserts renderer files contain no bare `lipgloss.NewStyle()` (outside `styles.go`) and no hex constants (outside `palette.go`) newly introduced by this slice; and that renderer references for status/tool glyphs go through `shared.Icon*` constants.

### [ ] 2.0 Compose Monitor tool-exchange headers through `RenderStatusLine`

Convert `itemTranscriptBody` to build the tool-exchange header entirely
through `RenderStatusLine`, remove the appended state prose, apply per-slot
styling, place `toolErrorHint` in `Meta`, and either migrate the orphan
`Result (unknown origin)` path to the same builder or lock the current
flat behavior with a documented limitation. Preserve slice-01's card
frame, cache identity, line-range accounting, and persistence-off path.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'Test.*(StatusLineHeader|NoStateProse|SignatureGlyph|SelectedTitleOnly|ErrorHintMeta|OrphanHeader|PersistenceOff)' -v` demonstrates prose removal, per-state icon mapping, edit-vs-generic glyph selection on settled success, selection-only title emphasis, meta placement of the error hint, orphan behavior (migrated or locked), and persistence-off retention; this covers FR-02.11–FR-02.20.
- Test: `go test ./internal/tui/monitor -run 'TestToolExchangeCardCache.*|TestTranscriptCard.*' -v` continues to pass (slice-01 cache/width/line-range regressions) with the new composed header, demonstrating identity through `transcriptRenderKey.header`.
- Terminal capture: `docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-2-monitor-headers.txt` and a companion PNG capture the Monitor at ~100 columns with adjacent success/error/running/incomplete headers, showing title/description/meta in visibly different styles.

#### 2.0 Tasks

- [ ] 2.1 Refactor `itemTranscriptBody` at `internal/tui/monitor/monitor_transcript_items_view.go:76-96` so tool-exchange rows are composed via `RenderStatusLine` with `Title = summary.action`, `Description = summary.detail`, `Icon`/`IconStyle` from `ToolStatusIcon(displayState, kind)`, and `Meta = nil` (populated below). Delete the `label += " failed" / " · running" / " · incomplete"` branches and the `preview` concatenation.
- [ ] 2.2 When `displayState == toolDisplayError`, append `toolErrorHint(m, item)` to `Meta` after sanitizing; when the hint is empty, do not append. Ensure the hint no longer participates in the title. Keep `toolErrorHint` unchanged in signature/return value.
- [ ] 2.3 Apply `Theme.Chat.TranscriptSelected` to `Title` (via `TitleStyle`) only when the row is selected. Do not wrap the full row in `TranscriptError` or `TranscriptSelected`; the border color and icon carry state, and the selection cue remains slice-01's outside `▌` rail plus the emphasized title.
- [ ] 2.4 Ensure the settled-success glyph is the tool's signature glyph via `ToolStatusIcon` and that running/pending settle to the same generic glyph so no state change produces a per-frame glyph change until success. Adjust the summary source (`monitor_tool_summary.go`) so the summary's own `icon` is not fed into the composed header — the shared resolver is the sole source for the header icon slot.
- [ ] 2.5 Choose one option for the orphan `Result (unknown origin)` row and record the choice in the task file's Standards Evidence Table:
  - **Migrate:** replace the `label = shared.IconToolResult + " Result (unknown origin)"` at `items_view.go:81` with a `RenderStatusLine` call using the warning icon and warning meta styles; the row remains outside the card.
  - **Defer:** keep the current flat rendering and add a locked-behavior test that asserts the current output. Record the deferral in `25-proofs/25-task-2-limitations.md` with the rationale.
- [ ] 2.6 Add table-driven Monitor cases covering (state × kind × selection × prefix):
  - Prose-free rows for success/error/running/incomplete (grep against `stripANSI(body)`).
  - Icon mapping per state via `ToolStatusIcon`.
  - Settled `edit` renders the edit signature glyph; a pending/running `read` renders the generic pending glyph.
  - Selected row emphasizes only the title fragment (assert the styled substring boundary via a substring test on the raw ANSI output).
  - Error hint appears in the meta segment of the header and not inside the title.
- [ ] 2.7 Prune `toolCallSummary.label` and `toolCallSummary.preview` (or, if kept for external callers, mark them deprecated with a Go doc note and update every caller inside `internal/tui/monitor` to use `icon`/`action`/`detail`). Update `monitor_tool_summary_test.go` to reflect the new contract; add a regression that `action` and `detail` remain distinct after `summarizeActivity`.
- [ ] 2.8 Extend `monitor_transcript_card_test.go` cache lifecycle cases so a running → success transition (via `setChatPage`) produces a differing composed `header` string and a fresh cache entry; the running → success transition changes the icon, not the border alone.
- [ ] 2.9 Add a persistence-off regression demonstrating that `RunDir == ""` retains the existing empty-state path and does not invoke `RenderStatusLine` — reuse slice-01's persistence-off fixture and assert the header-card code path is not entered.

### [ ] 3.0 Demonstrate integrated presentation and record acceptance checks

Produce reviewer-safe evidence at realistic dimensions and run the
repository's applicable checks. Compare the resulting frames to slice
01's captures to prove the header composition is the sole visible change.

#### 3.0 Proof Artifact(s)

- Screenshot: `docs/specs/25-spec-status-line-header-grammar/25-proofs/25-task-3-monitor-headers.png` shows the Monitor with adjacent success/error/running/incomplete headers at ~100 columns, with title/description/meta in three visibly different styles; a companion `.notes.txt` records terminal size, `transcriptInnerW`, selection, expansion state, and observed state colors.
- Screenshot: narrow (~58 cols) and wide (~132 cols) Monitor captures under `25-proofs/` show the header composition truncating on the title (via slice-01 `TruncateTitle`) rather than dropping meta first, and demonstrate stable one-row height at each width.
- CLI: captured outputs for `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/...`, `gofmt -l <changed-go-files>`, and `git diff --check` demonstrate the applicable acceptance gates. Any unavailable terminal screenshot is recorded as a limitation in `25-proofs/25-task-3-limitations.md`, not replaced by a component-only claim.

#### 3.0 Tasks

- [ ] 3.1 Extend the slice-01 synthetic visual fixture (`syntheticTranscriptCardVisualPage` in `monitor_transcript_card_test.go`) with cases that exercise every state × kind combination this slice ships, without regressing slice-01 captures. Reuse the existing fabricated fixture source.
- [ ] 3.2 Capture the integrated Monitor at the primary review size and record `.ansi`, `.html`, and `.png` proofs plus companion notes. Confirm slice-01's captures still render (they are proofs that live in their own directory; do not overwrite them).
- [ ] 3.3 Capture narrow and wide Monitor views from the same fixture and verify one-row headers, title-first truncation, and preserved state colors. Store both captures under `25-proofs/`.
- [ ] 3.4 Run and record the focused shared and Monitor tests from Tasks 1–2, plus `go test -race ./internal/tui/...`, `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `gofmt -l <changed-go-files>`, and `git diff --check`. Distinguish ordinary pass, intentional skip, assertion failure, and environment/toolchain failure.
- [ ] 3.5 Review the final diff against every FR and non-goal: confirm no detail-section conversion (slice 05), no diff-badge population (slice 07), no grouped-read work (slice 08), no spinner ticker (slice 13), no inline-arg formatting (slice 15), no truncation-vocabulary change (slice 06), no glyph-preset table (slice 14), and no wire/format/harness change. Record any unavailable proof as a precise limitation in `25-proofs/25-task-3-limitations.md`.
