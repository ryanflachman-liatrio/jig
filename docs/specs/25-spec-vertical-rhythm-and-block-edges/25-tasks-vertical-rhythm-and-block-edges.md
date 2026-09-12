# 25-tasks-vertical-rhythm-and-block-edges.md

## Planning Basis

### Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Read implementation and tests before changing behavior; delete replaced code paths rather than dual-path them (pre-v1); file-is-truth (transcript.jsonl) with the bus for liveness only; persistence-off is first class; use synthetic fixtures — never real `.jig/` data. | none |
| `CLAUDE.md` | yes | Follow the workflow reading map; do not maintain a second copy of shared rules. | none |
| `docs/ARCHITECTURE.md` | yes | Shared TUI primitives belong in `internal/tui/shared`; child TUI packages must not import root TUI; shared must not import Monitor. Slice 04 stays inside `internal/tui/monitor`, so no shared/monitor boundary is crossed. | none |
| `docs/CONVENTIONS.md` | yes | Small consumer-owned interfaces; keep APIs narrow; distinguish absent from explicit zero; bounded caches. | none |
| `docs/TUI.md` | yes | Measure cells with `lipgloss.Width`; ANSI-aware helpers for width; semantic styles owned by `Styles`; complete cache invalidation identity; persistence-off is first class. | none |
| `docs/TESTING.md` | yes | Table-driven synthetic TUI cases; grep- and width-based assertions; targeted race for TUI; supplement with recorded terminal capture. | none |
| `docs/adr/0001-manual-border-title-compositing.md` | yes | One manual titled-border compositor; slice 04 does not touch it. | none |
| `go.mod` | yes | Go 1.25.12; Charm v2 (Lip Gloss 2.0.5, Glamour 2.0.1, ANSI 0.11.7). No dependency addition. | none |
| `mise.toml` | yes | Go 1.25 series toolchain. | none |
| `docs/epics/omp-transcript-parity/epic.md` | yes | CC-2 (state carried by border/tint/glyph); CC-9 (SGR reset stabilization already resolved by slice 01's `finishRow`); delivery order lists slice 04 after slice 01 and states that slice 09 depends on slice 04 for the raw-bytes trim; slice 12 sits inside the two-line coordinate gap. | none |
| `docs/epics/omp-transcript-parity/slices/04-vertical-rhythm-and-block-edges.md` | yes | FR-04.1–FR-04.6 (source-slice numbering); Q-04.1 / Q-04.2; the semantic inversion between omp's `isPlainBlank` and jig's `stripBlankEdges`; the note that jig's two-line coordinate gap is stronger than omp's flat rhythm and should be preserved. | none |
| `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-spec-vertical-rhythm-and-block-edges.md` | yes | FR-04.1–FR-04.13; two-unit demoable structure; execution-coordinate gap preserved; card cache identity untouched; tinted padding bytes referenced from `card.go`'s `finishRow`. | none |
| `docs/specs/25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md` | yes | Card cache identity, `renderToolExchangeCard` prefix flow, `finishRow` SGR stabilization for tinted rows. | none |
| `docs/specs/25-spec-status-line-header-grammar/25-spec-status-line-header-grammar.md` | yes | Header composition and selection styling remain unchanged; slice 04 does not touch `composeToolHeader` or `renderToolExchangeCard`. | none |
| `docs/specs/25-spec-selection-affordance/25-spec-selection-affordance.md` | yes | Two-cell selection prefix contract; `chatItemLineRanges` stability across cursor movement. Slice 04 must preserve that stability across the refactor. | none |
| `internal/tui/monitor/monitor_transcript.go` | yes | `stripBlankEdges` + `stripSGR` are the existing edge-trim helpers; both correct for glamour output normalization. New helpers colocate here without disturbing existing consumers. | none |
| `internal/tui/monitor/monitor_transcript_items_view.go` | yes | Current `itemTranscriptBody` loop writes directly to a single builder and calls `itemSpacingBefore` between every neighbor pair; case arms always write "content\n" for existing item kinds. `renderToolExchangeCard` returns cached bytes with no trailing newline; the case arm appends `"\n"` explicitly. | none |
| `internal/tui/monitor/monitor_transcript_items.go` | yes | `itemSpacingBefore` returns 2 across a coord change, 0 across consecutive same-role text at the same coord, 1 otherwise. Doc comment already explains the no-theme-dependency contract. | none |
| `internal/tui/shared/card.go` | yes | `finishRow` hard-codes `"\x1b[48;2;26;25;31m"` (neutral) and `"\x1b[48;2;42;26;30m"` (error); the tinted-padding fixture references those exact sequences. Card body has no blank content rows today. | none |
| `CONTRIBUTING.md` | not found | No additional contribution policy present. | none |
| `.github/pull_request_template.md` | not found | No PR template present. | none |
| `.github/workflows/` | not found | No checked-in CI workflow defines additional gates. | none |

The spec resolves every material design choice: the raw-bytes
predicate over an explicit card-side marker (Q-04.3 deferred),
preservation of `itemSpacingBefore` as the join function (Q-04.1
deferred), and the two-line execution-coordinate gap (Q-04.2 tuned by
slice 12, not here). There is no design alternative to record beyond
what the spec already lists under Non-Goals and Open Questions.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_transcript.go` | Owns `stripBlankEdges` and `stripSGR`. The two new helpers (`isStructuralBlank`, `trimStructuralBlankEdges`) colocate here so a maintainer sees both semantics side by side. The `stripBlankEdges` doc comment is refreshed to reference its raw-bytes sibling. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Owns `itemTranscriptBody`. The refactor introduces a per-item scratch buffer, applies `trimStructuralBlankEdges`, implements the zero-height guard, and computes the separator against the last item that actually contributed content. The case arms and their calls to `renderToolExchangeCard`, `writeItemDetail`, `writeNewCodeCards`, `writeToolActivityDetails`, and `writeVerbatim` do not change. |
| `internal/tui/monitor/monitor_transcript_items.go` | Owns `itemSpacingBefore`. The function body does not change; the doc comment is refreshed to (a) state that the separator only fires between two items that both contributed content and (b) note that a coordinate change remains a two-line gap because the boundary banner (slice 12) sits inside it. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Hosts most new behavioral tests (zero-height guard, edge-trim, tinted padding, coordinate gap, consecutive text no-gap, line-range accuracy). A sibling `monitor_vertical_rhythm_test.go` is acceptable if grouping helps readability. |
| `internal/tui/monitor/monitor_transcript_test.go` | Hosts the two helper unit tests (`TestIsStructuralBlankRawBytesSemantics`, `TestTrimStructuralBlankEdges`) and the grep-based call-site test. |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Existing slice 01 card cache/line-range assertions must keep passing without weakened expectations. Confirm during Task 3.0; extend only if a regression appears. |
| `internal/tui/monitor/monitor_selection_affordance_test.go` | Existing slice 03 line-range and column-parity assertions must keep passing. Confirm during Task 3.0. |
| `internal/tui/shared/card.go` | Read-only reference: hard-codes the tinted background SGR sequences (`\x1b[48;2;26;25;31m`, `\x1b[48;2;42;26;30m`) the FR-04.3 fixture reuses. No change needed here. |
| `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-proofs/` | New sanitized test outputs, terminal captures, and acceptance-check transcripts produced during implementation. Includes an ANSI baseline captured from the recorded pre-implementation `baseline_commit` for the same synthetic scene, plus the fixture-only patch needed to reproduce it in an isolated worktree. |

### Notes

- Keep table-driven tests beside `monitor_transcript_items_view.go` and
  `monitor_transcript.go`. Do not read real `.jig/` data for fixtures
  or proofs.
- Use `lipgloss.Width` for width comparisons and `stripSGR` only where
  SGR-stripped blankness is the correct predicate. Use
  `isStructuralBlank` where raw-bytes blankness is correct. Both live
  in the Monitor package for slice 04.
- The tinted-padding fixture may reference the exact SGR sequences
  from `card.go`'s `finishRow`. That coupling is intentional: if the
  palette hex values move, the tinted-padding assertion documents the
  dependency and fails fast.
- Format only changed Go files with `gofmt -w`. Do not rewrite
  unrelated files.
- Run focused Monitor tests during iteration
  (`go test ./internal/tui/monitor -run
  'TestTranscript|TestIsStructuralBlank|TestTrimStructuralBlank' -v`),
  then the root build, test, vet, targeted TUI race, and whitespace
  checks recorded under Task 4.

### Requirement-to-Test Traceability

| Requirement | Task | Planned Test Artifact |
| --- | --- | --- |
| FR-04.1 | 1.1, 1.3 | `TestIsStructuralBlankRawBytesSemantics` — raw-bytes truth table over plain-whitespace, ANSI-escape-only, tinted-padding-shaped, Glamour-shaped, and Unicode-non-breaking-space inputs. |
| FR-04.2 | 1.2, 1.3 | `TestTrimStructuralBlankEdges` — empty/single/leading/trailing/interior/tinted cases with byte-equality expectations. |
| FR-04.3 | 1.1, 1.2, 2.5 | Both helper tests + `TestTranscriptPreservesTintedPaddingRow`: assert `\x1b[48;2;26;25;31m` and `\x1b[48;2;42;26;30m` byte sequences survive the trim, byte-for-byte. |
| FR-04.4 | 1.5 | `TestVerticalRhythmHelperCallSites` — grep-based `_test.go` assertion that `stripBlankEdges` is used only from `renderNewCodeCard` and `fileBody` and `trimStructuralBlankEdges` is used only from `itemTranscriptBody`. |
| FR-04.5 | 2.1, 2.2 | Refactor coverage: `TestTranscriptLineRangesMatchRenderedRows` observes the emitted bytes and confirms each item's `lineRange` maps to a coherent block, which is only possible when each item was rendered as an atomic unit before being written. |
| FR-04.6 | 2.3, 2.4 | `TestTranscriptZeroHeightItemContributesNothing` — asserts exact separator count around a skipped item, no `chatItemLineRanges` entry for the skipped item, and neighbors' `lineRange` matches the no-skipped-item baseline. |
| FR-04.7 | 2.4, 2.5 | `TestTranscriptTrimsStructuralEdgeBlanksBetweenItems` — asserts blank-line count around an edge-blank-emitting item equals `itemSpacingBefore(previous, current)`. |
| FR-04.8 | 2.6 | `TestTranscriptConsecutiveTextItemsSameRoleNoGap` — asserts zero blank lines between two adjacent user text items at the same coord. |
| FR-04.9 | 2.7 | `TestTranscriptLineRangesMatchRenderedRows` — independently splits the transcript body on `"\n"` and compares each item's `lineRange` to the actual row indices its bytes occupy. |
| FR-04.10 | 3.1, 3.2 | Rerun `TestTranscriptCardLineRangesCachedAndFresh`, `TestTranscriptCardCacheLifecycleBounded`, `TestTranscriptCardPageBoundsSearchExpansionAndClipboard`, and the slice-03 line-range/column-parity tests unchanged; assert green in the acceptance capture. |
| FR-04.11 | 3.1 | Rerun the slice-01 cache lifecycle test; assert `chatItemRendered` size behavior is byte-for-byte identical to today. |
| FR-04.12 | 2.6, 2.8 | `TestTranscriptExecutionCoordinateGapPreserved` — asserts exactly two blank lines between two items whose `coord.iteration` differs. Doc-comment update on `itemSpacingBefore` verified by inspection. |
| FR-04.13 | 3.4 | Persistence-off regression: `TestMonitorEmptyChatWhenRunDirUnset` (existing) must still exercise the empty-state path without entering the item loop; verified in `25-proofs/25-task-3-persistence-off.txt`. |

## Tasks

### [x] 1.0 Add the raw-bytes structural blank predicate and edge trimmer

Introduce `isStructuralBlank` and `trimStructuralBlankEdges` in
`internal/tui/monitor/monitor_transcript.go`, next to the existing
`stripBlankEdges` and `stripSGR` helpers. Refresh `stripBlankEdges`'s
doc comment to name its raw-bytes sibling so the two semantics are
visibly distinct to a future reader. Cover the new helpers with
table-driven unit tests and lock the call sites with a grep-based
regression.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestIsStructuralBlankRawBytesSemantics|TestTrimStructuralBlankEdges' -v` passes the raw-bytes predicate truth table and the wrapper's split-trim-join cases including the tinted-padding-shaped input (`\x1b[48;2;26;25;31m   \x1b[49m`) surviving byte-for-byte (FR-04.1, FR-04.2, FR-04.3).
- Test: `go test ./internal/tui/monitor -run 'TestVerticalRhythmHelperCallSites' -v` reads `monitor_transcript.go` and `monitor_transcript_items_view.go` via `os.ReadFile` and asserts (a) `stripBlankEdges(` appears only inside `renderNewCodeCard` and `fileBody`, and (b) `trimStructuralBlankEdges(` appears only inside `itemTranscriptBody`. This locks FR-04.4 without policing other packages.

#### 1.0 Tasks

- [x] 1.1 Add `isStructuralBlank(line string) bool` to `internal/tui/monitor/monitor_transcript.go`, immediately above `stripSGR`. Implementation: iterate the string's bytes; return `false` on any byte that is not one of `' '`, `'\t'`, `'\r'`, `'\v'`, `'\f'`; explicitly return `false` on `\x1b` (0x1B) via the same branch (since it is not one of the whitespace bytes, this is automatic — the explicit call-out belongs in the doc comment, not a special-case). Empty string returns `true`. Include a doc comment stating the raw-bytes semantic and contrasting it with `stripBlankEdges`.
- [x] 1.2 Add `trimStructuralBlankEdges(s string) string` to `internal/tui/monitor/monitor_transcript.go`, immediately above `stripBlankEdges`. Implementation: `strings.Split(s, "\n")`; advance `start` while `start < end && isStructuralBlank(lines[start])`; retreat `end` while `end > start && isStructuralBlank(lines[end-1])`; return `""` when `start >= end`; otherwise `strings.Join(lines[start:end], "\n")`. Do not append a trailing newline. Include a doc comment stating that the caller reintroduces its own line terminator.
- [x] 1.3 Add `TestIsStructuralBlankRawBytesSemantics` and `TestTrimStructuralBlankEdges` to `internal/tui/monitor/monitor_transcript_test.go` (create the file if it does not exist; use `package monitor` and the existing test helpers). Table-drive each per the spec's Proof Artifacts list: empty, single ASCII space, mixed spaces/tabs, `"\r"`, `"\v"`, plain glyph, single `\x1b`, tinted-padding row (`"\x1b[48;2;26;25;31m   \x1b[49m"`), Glamour blank row (`"\x1b[38;2;80;80;80m\x1b[0m"`), and Unicode NBSP (`"\u00a0"`). Assert the exact expected trim output for each wrapper case, including an "entirely blank input returns empty string" case.
- [x] 1.4 Refresh the doc comment on `stripBlankEdges` (`monitor_transcript.go` around lines 704–708) to reference `trimStructuralBlankEdges` as its raw-bytes sibling and state that the two are semantically opposite. Do not change `stripBlankEdges`'s body.
- [x] 1.5 Add `TestVerticalRhythmHelperCallSites` to `internal/tui/monitor/monitor_transcript_test.go`. Load `monitor_transcript.go` and `monitor_transcript_items_view.go` via `os.ReadFile`. Assert (a) every `stripBlankEdges(` occurrence in `monitor_transcript.go` is inside `renderNewCodeCard` or `fileBody` (or is inside the helper's own definition), (b) `stripBlankEdges(` in `monitor_transcript_items_view.go` occurs only inside `renderNewCodeCard`, and (c) `trimStructuralBlankEdges(` occurs only inside `itemTranscriptBody`. Use `strings.Index` + function-body extraction rather than a regex; keep the test simple and readable.
- [x] 1.6 Run `gofmt -w` on every file this task modifies. Run `go build ./cmd/jig` and `go test ./internal/tui/monitor -run 'TestIsStructuralBlank|TestTrimStructuralBlank|TestVerticalRhythmHelperCallSites' -v` to confirm the helpers are green before proceeding.

### [x] 2.0 Refactor `itemTranscriptBody` with per-item scratch, edge trimming, and the zero-height guard

Route each visible item through a per-iteration scratch buffer, apply
`trimStructuralBlankEdges` to the item's rendered bytes, skip items
whose trimmed output is empty, and compute the separator against the
last item that actually contributed content. Preserve every existing
invariant the loop carries (case-arm bodies, `renderToolExchangeCard`
prefix flow, `writeItemDetail` and `writeNewCodeCards` writes,
`chatItemLineRanges` accuracy, `itemSpacingBefore` return values, no
change to `chatItemRendered` cache identity).

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestTranscriptZeroHeightItemContributesNothing|TestTranscriptTrimsStructuralEdgeBlanksBetweenItems|TestTranscriptPreservesTintedPaddingRow|TestTranscriptStructuralEdgesAtNarrowWidth|TestTranscriptConsecutiveTextItemsSameRoleNoGap|TestTranscriptExecutionCoordinateGapPreserved|TestTranscriptLineRangesMatchRenderedRows' -v` passes the six behavioral assertions listed in the spec's Unit 2 proofs (FR-04.5 through FR-04.12) plus the narrow-width regression required by repository TUI guidance.
- Test: `go test ./internal/tui/monitor -run 'TestToolExchangeHeaderCardStatesAndWidths|TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes|TestTranscriptCardLineRangesCachedAndFresh' -v` continues to pass unchanged, demonstrating that the refactor is behavior-preserving for existing card fixtures (FR-04.10, FR-04.11).

#### 2.0 Tasks

- [x] 2.1 Refactor `itemTranscriptBody` in `internal/tui/monitor/monitor_transcript_items_view.go`. Introduce a `lastRenderedIdx := -1` counter before the loop. Inside each iteration, declare `var scratch strings.Builder` and move every case-arm write from `b` to `scratch`. After the switch, compute `body := trimStructuralBlankEdges(scratch.String())`. If `body == ""`, `continue` before writing any separator or `chatItemLineRanges` entry. Otherwise, if `lastRenderedIdx >= 0`, run `for range itemSpacingBefore(m.chatVisibleItems[lastRenderedIdx], item) { b.WriteString("\n"); line++ }`. Then `start := line`, `b.WriteString(body)`, `b.WriteString("\n")`, `line += strings.Count(body, "\n") + 1`, `m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}] = lineRange{start: start, end: line - 1}`, `lastRenderedIdx = i`.
- [x] 2.2 Update the doc comment on `itemTranscriptBody` to state: (a) each item renders into a per-iteration scratch buffer and is edge-trimmed with `trimStructuralBlankEdges` before it decides whether to contribute content; (b) an item whose trimmed output is empty contributes neither content nor a separator, and is not entered into `chatItemLineRanges`; (c) `itemSpacingBefore` is applied only between two items that both contributed content. Keep the existing note that a matched tool result is never a second row.
- [x] 2.3 Update the doc comment on `itemSpacingBefore` in `internal/tui/monitor/monitor_transcript_items.go` (around lines 150–164). Add a sentence stating that the caller (`itemTranscriptBody`) applies this rule only between two items that both contributed content, so a filtered or zero-height item does not leave a ghost gap behind. Add a sentence stating that the two-line execution-coordinate gap remains stronger than the ordinary one-line gap because slice 12's boundary banner will render inside it. Do not change the function body.
- [x] 2.4 Add `TestTranscriptZeroHeightItemContributesNothing` to `internal/tui/monitor/monitor_transcript_items_view_test.go` (or a new sibling `monitor_vertical_rhythm_test.go`). Fixture: three synthetic items — a user text item, a text item whose glamour render is stubbed to return `""` (or an assistant text item whose `block.Text` is empty and whose renderer produces an empty string; verify the empty-render path by first checking `m.renderMarkdown(key, "")` returns `""`), and a second user text item. Render the page, assert (a) `stripANSI(body)` between the two visible items contains exactly `itemSpacingBefore(items[0], items[2])` blank lines, (b) `chatItemLineRanges` has no entry for the middle item's key, (c) the second visible item's `lineRange.start` equals the first item's `lineRange.end + 1 + itemSpacingBefore(items[0], items[2])`.
- [x] 2.5 Add `TestTranscriptTrimsStructuralEdgeBlanksBetweenItems`, `TestTranscriptPreservesTintedPaddingRow`, and `TestTranscriptStructuralEdgesAtNarrowWidth` to the same file. For the trim test, construct a synthetic system/verbatim item whose renderer emits leading and trailing plain blank lines, using fabricated `transcript.Entry` / `transcript.BlockText` content. Assert the number of blank lines between it and its neighbor equals `itemSpacingBefore(previous, current)`, not `itemSpacingBefore + 1` or `+2`. For the tinted-padding test, inject a synthetic system/verbatim item whose rendered bytes contain a tinted-padding row constructed from `"\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) + "\x1b[49m"`; assert both byte preservation and item-level separator behavior. Do not add a production field or rendering branch used only by tests. For the narrow-width case, render the zero-height, edge-blank, and tinted-padding fixture with `transcriptInnerW = 24`; assert the skipped item has no range, the visible-item gaps equal `itemSpacingBefore`, and the tinted bytes survive. The existing width-60 behavioral cases plus this width-24 case cover ordinary and narrow layouts without expanding the visual-proof matrix.
- [x] 2.6 Add `TestTranscriptConsecutiveTextItemsSameRoleNoGap` and `TestTranscriptExecutionCoordinateGapPreserved`. First test: two synthetic user text items at the same generation/iteration/attempt; assert zero blank lines between them. Second test: two synthetic items whose `coord.iteration` differs by 1; assert exactly two blank lines between them. Both tests use `stripANSI` on the transcript body and count `\n\n` occurrences between the two items' `lineRange` boundaries.
- [x] 2.7 Add `TestTranscriptLineRangesMatchRenderedRows` to the same file. Fixture: the synthetic multi-item page including one zero-height, one edge-blank-trimmed, and one tinted-padding item. Render the body, `strings.Split(body, "\n")`. For every key in `chatItemLineRanges`, assert `end - start + 1` equals the count of rows the item occupies (from `start` to `end` inclusive in the split slice); assert no `chatItemLineRanges` entry exists whose `start > end`; assert every entry's rows contain the item's identifying content (a title glyph, tool ID, or role label, whichever is characteristic for that kind).
- [x] 2.8 Run `gofmt -w` on every file this task modifies. Run `go build ./cmd/jig` and `go test ./internal/tui/monitor -run 'TestTranscript|TestIsStructuralBlank|TestTrimStructuralBlank|TestToolExchange|TestSelectionAffordance' -v`. The Monitor slice must be green — new tests pass, existing tests untouched — before moving on.

### [x] 3.0 Confirm the refactor is behavior-preserving for slice-01 / slice-03 fixtures and persistence-off

Rerun every existing test in `internal/tui/monitor/` that observes
`lineRange` values, card row counts, cache size, prefix widths, or the
persistence-off empty state and confirm none of their expectations
weaken. If any expected value changes, investigate rather than update
the expectation — FR-04.10 makes any drift a regression.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestTranscriptCardLineRangesCachedAndFresh|TestTranscriptCardCacheLifecycleBounded|TestTranscriptCardPageBoundsSearchExpansionAndClipboard|TestSelectionAffordanceLineRangesStable|TestSelectionAffordanceCardWidthStableAcrossSelection|TestSelectionAffordanceCardCacheDoesNotGrowOnSelection|TestToolExchangeHeaderCardStatesAndWidths|TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes' -v` passes with no expectation changed relative to `main`. Recorded output under `25-proofs/25-task-3-preserved-fixtures.txt`.
- Test: the existing persistence-off assertion for the empty transcript path (`go test ./internal/tui/monitor -run 'TestMonitorEmptyChatWhenRunDirUnset' -v`, or the equivalent test that exercises `chatBody` with `RunDir == ""`) passes unchanged, demonstrating FR-04.13.
- Screenshot pair: `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-proofs/25-task-3-rhythm.{ansi,html,png}` and `25-task-3-rhythm-baseline.ansi` (the baseline is captured from the pre-implementation `baseline_commit` and stored ANSI-only) — the same 80-column synthetic Monitor scene rendered before and after the refactor. `25-task-3-baseline-harness.patch` contains the sanitized fixture-only test needed to reproduce the baseline in an isolated worktree. Companion `.notes.txt` records the baseline commit, terminal size, `transcriptInnerW`, cursor position, and the blank-line count observed between each pair of visible items in both captures. The two ANSI captures diff cleanly at the intended sites (one fewer blank line where the item had structural edge blanks; no blank lines where the zero-height item was skipped; unchanged rows elsewhere).

#### 3.0 Tasks

- [x] 3.1 Rerun `go test ./internal/tui/monitor -run 'TestTranscriptCardLineRangesCachedAndFresh|TestTranscriptCardCacheLifecycleBounded|TestTranscriptCardPageBoundsSearchExpansionAndClipboard|TestSelectionAffordanceLineRangesStable|TestSelectionAffordanceCardWidthStableAcrossSelection|TestSelectionAffordanceCardCacheDoesNotGrowOnSelection|TestToolExchangeHeaderCardStatesAndWidths|TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes' -v` and capture the output under `25-proofs/25-task-3-preserved-fixtures.txt`. If any test fails with a diff in expected `lineRange`, expected prefix, or expected cache size, stop and investigate — do not update the expectation. Weakening any of these expectations is a regression per FR-04.10 / FR-04.11.
- [x] 3.2 Extend `syntheticTranscriptCardVisualPage` (or add a new sibling fixture next to it) with (a) one filtered/zero-height item between two visible cards, and (b) one item whose rendered bytes carry a leading and a trailing plain blank line. Do not modify the positions of existing items so slice-01 / slice-02 / slice-03 visual proofs keep rendering their expected chatItems indices.
- [x] 3.3 Add `TestVerticalRhythmVisualProof` to `internal/tui/monitor/monitor_transcript_items_view_test.go` (following the pattern of `TestSelectionAffordanceVisualProof`). Capture the Monitor at 80×30 into `25-proofs/` when `JIG_UI_SNAPSHOT_DIR` is set. Before Task 1.1 changes production code, record `git rev-parse HEAD` as `baseline_commit` in the companion notes and save a sanitized, self-contained fixture-only patch as `25-proofs/25-task-3-baseline-harness.patch`; that patch must add only the visual proof test/harness and compile against `baseline_commit`. Capture `-baseline.ansi` in an isolated checkout: set `repo_root=$(pwd)`, `baseline_root=$(mktemp -d)`, and `baseline_dir="$baseline_root/worktree"`; run `git worktree add --detach "$baseline_dir" "$baseline_commit"`, run `git -C "$baseline_dir" apply "$repo_root/docs/specs/25-spec-vertical-rhythm-and-block-edges/25-proofs/25-task-3-baseline-harness.patch"`, then run `(cd "$baseline_dir" && JIG_UI_SNAPSHOT_DIR="$repo_root/docs/specs/25-spec-vertical-rhythm-and-block-edges/25-proofs" go test -run TestVerticalRhythmVisualProof ./internal/tui/monitor)`. Finally run `git worktree remove --force "$baseline_dir"` and `rmdir "$baseline_root"`. Do not stash, check out, or remove files in the active worktree. Commit the baseline ANSI, fixture-only patch, and `.notes.txt`; do not commit the temporary baseline test file outside the patch artifact.
- [x] 3.4 Confirm the persistence-off path is unchanged. Run `go test ./internal/tui/monitor -run 'TestMonitorEmptyChatWhenRunDirUnset|TestMonitor.*EmptyChat' -v` (adjust the regex if the test name differs; grep `monitor_test.go` for "empty" to locate it). Capture the output under `25-proofs/25-task-3-persistence-off.txt`. FR-04.13 passes when the test remains green with no expectation change.

### [x] 4.0 Record acceptance evidence and integrated capture

Produce reviewer-safe screenshot evidence at the epic's 80-column
target, run the applicable repository acceptance checks, and record any
limitation explicitly rather than replacing it with a component-only
claim. Follow the acceptance pattern established by slice 03.

#### 4.0 Proof Artifact(s)

- CLI: captured outputs for `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/...`, `gofmt -l <changed-go-files>`, and repository-wide `git diff --check` under `25-proofs/25-task-4-acceptance/`. Any unavailable check is recorded as a limitation in `25-proofs/25-task-4-limitations.md`, not replaced by a substitute. A supplemental Monitor-only diff is allowed, but it does not replace the repository-wide whitespace check.
- Screenshot: `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-proofs/25-task-4-monitor-rhythm.{ansi,html,png}` — the integrated Monitor scene at 80×30 with (i) a text item, (ii) a tool-exchange card, (iii) a zero-height/skipped filtered item, (iv) another tool-exchange card, and (v) an item across an execution-coordinate boundary. Companion `.notes.txt` records terminal size, `transcriptInnerW`, cursor position, and the blank-line count between each visible pair.
- Limitations note: `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-proofs/25-task-4-limitations.md` records (a) whether the PNG rendering pipeline was available in the validation environment and, if not, that the HTML capture is the deterministic ground truth; (b) any pre-existing `go test ./...` failures unrelated to slice 04 (engine scheduler timeouts and the harness security-monitor scenario are the known ones from slices 02 and 03 — document whether they reproduced at the recorded `baseline_commit`); (c) any check that could not be run and why.
- Final-diff review recorded in the PR body confirming the change does not touch other epic slices (no card primitive change, no header-grammar change, no selection-affordance change, no detail-section conversion, no truncation-vocabulary edit, no diff renderer, no grouped-read tree, no message framing, no glyph-preset table, no wire/format/harness change, no palette hex additions, no new `lipgloss.NewStyle`).

#### 4.0 Tasks

- [x] 4.1 Extend or add `TestVerticalRhythmVisualProof` (from 3.3) to also emit the integrated 80×30 capture referenced above. Include a coordinate-boundary transition in the fixture so the two-line gap is visible. Emit both ANSI and HTML; PNG conversion runs outside the test process (headless Chrome as in slice 03).
- [x] 4.2 Run and capture the applicable repository checks in the order specified above. Save each command's stdout+stderr to a separate file under `25-proofs/25-task-4-acceptance/` named `build.txt`, `test.txt`, `vet.txt`, `test-race-tui.txt`, `gofmt.txt`, `git-diff-check.txt`. The final whitespace command must be the repository-wide `git diff --check`; do not scope it to `internal/tui/monitor/`. Prepend every command line and record the exit code inline (`EXIT=<code>`), matching the slice-03 acceptance format.
- [x] 4.3 Write `25-proofs/25-task-4-limitations.md`. Include the acceptance-checks table (Result / Artifact / EXIT columns). For any failure in `go test ./...`, reproduce against the `baseline_commit` recorded by Task 3.3 without modifying the active worktree: set `repro_root=$(mktemp -d)` and `repro_dir="$repro_root/worktree"`; run `git worktree add --detach "$repro_dir" "$baseline_commit"`, run `(cd "$repro_dir" && go test ./...)`, record the exact command, commit SHA, exit code, and matching failure text, then run `git worktree remove --force "$repro_dir"` and `rmdir "$repro_root"`. Follow the slice-03 pre-existing-failures template — engine scheduler timeouts and the harness security-monitor scenario are the two known families to check, but report only failures that actually reproduce. Do not use `git stash`, `git checkout` over active files, wildcard removal, or placeholder file lists.
- [x] 4.4 Final-diff review: read the cumulative diff and confirm the touched files are limited to `internal/tui/monitor/monitor_transcript.go`, `internal/tui/monitor/monitor_transcript_items_view.go`, `internal/tui/monitor/monitor_transcript_items.go` (doc comment only), any new `_test.go` files scoped to vertical-rhythm regressions, and files under `docs/specs/25-spec-vertical-rhythm-and-block-edges/`. No `internal/tui/shared/` change, no `palette.go` change, no other Monitor file change, no engine/harness/scheduler change.
