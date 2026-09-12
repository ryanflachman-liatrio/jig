# 25-tasks-truncation-vocabulary.md

## Planning Basis

### Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Read implementation/tests before changing behavior; pre-v1 removes replaced mechanisms in the same change; shared TUI primitives belong in `internal/tui/shared`; preserve persistence-off; use fabricated fixtures. | none |
| `CLAUDE.md` | yes | Route through the workflow reading map; do not maintain a second copy of shared rules. | none |
| `docs/ARCHITECTURE.md` | yes | Shared TUI primitives belong in `internal/tui/shared`; the shared package must not import Monitor; child TUI packages preserve acyclic ownership. | none |
| `docs/CONVENTIONS.md` | yes | Prefer cohesive owner-local helpers; keep APIs narrow; distinguish absent from explicit zero (relevant to `hasMore == false`); bound inputs before rendering. | none |
| `docs/TUI.md` | yes | Measure cells with `lipgloss.Width` and ANSI-aware helpers; semantic styles owned by `Styles`; complete cache invalidation identity; persistence-off is first class; rebuild width-baked renderers on resize. | none |
| `docs/TESTING.md` | yes | Table-driven synthetic TUI cases; grep- and width-based assertions; targeted race for TUI; visual supplements; accurate blocked-check reporting. | none |
| `docs/adr/0001-manual-border-title-compositing.md` | yes | One manual titled-border compositor; do not add a second — this slice does not touch the compositor. | none |
| `go.mod` | yes | Go 1.25.12; Charm v2 (Lip Gloss 2.0.5, Bubbles 2.x for `keybind`); no new dependency required. | none |
| `mise.toml` | yes | Go 1.25 series toolchain. | none |
| `docs/epics/omp-transcript-parity/epic.md` | yes | CC-8 fixes the truncation vocabulary for the epic; CC-7 forbids hardcoded new glyphs at call sites; CC-11 keeps jig's per-item toggle alongside the global expand-all; the epic delivery order lists slice 06 as independent and consumed by 05/07/08. | none |
| `docs/epics/omp-transcript-parity/slices/06-truncation-vocabulary.md` | yes | Helper signatures, FR-06.1..FR-06.8, wrong-end problem, live-key derivation from `Toggle`, grep-based conformance test, page markers left as-is, Steps-panel streaming as the surviving live-tail. | none |
| `docs/specs/25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md` | yes | Card cache identity; header composition; the state field discriminates cache entries so an anchor change piggybacks on state-based invalidation. | none |
| `docs/specs/25-spec-status-line-header-grammar/25-spec-status-line-header-grammar.md` | yes | Selection styles only the title slot (FR-02.18); composed header already discriminates selection in the cache — no additional cache key is required for anchor. | none |
| `docs/specs/25-spec-selection-affordance/25-spec-selection-affordance.md` | yes | Two-cell prefix contract is stable; the six/four-space detail indents remain the writer's responsibility. | none |
| `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-spec-vertical-rhythm-and-block-edges.md` | yes | Per-item edge-trim discipline; a prepended hint row must be a non-blank body row so it survives the edge trim (`isStructuralBlank` treats SGR-styled content as non-blank). | none |
| `docs/specs/25-spec-tool-detail-sections/25-spec-tool-detail-sections.md` | yes | Slice 05 declares slice 06 as its prerequisite for `MoreItems`/`EarlierItems`/`ExpandHint`; the shape and location documented here match slice 05's `Relevant Files` expectations. | none |
| `CONTRIBUTING.md` | not found | No additional contribution policy present. | none |
| `.github/pull_request_template.md` | not found | No PR template present. | none |
| `.github/workflows/` | not found | No checked-in CI workflow defines additional gates. | none |

The spec resolves every material design choice: three helpers (`MoreItems`,
`EarlierItems`, `ExpandHint`) plus a small `HintLine` combinator; a
package-private `detailAnchor` enum on `boundTranscriptDetail`; the
`toolDisplayRunning` → tail-anchor / else → head-anchor policy; ASCII
brackets in the expand hint; the Steps-panel drop indicator without an
expand hint; and a grep-based Monitor regression test with an explicit
allowlist for `expandView`'s KB-elision marker. There is no design
alternative to record beyond the recommended answers to the four
non-blocking open questions in the spec.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/shared/truncation.go` | New file. Owns `MoreItems`, `EarlierItems`, `ExpandHint`, and the `HintLine` combinator. String-only, no `lipgloss` import. This is the source slice 05's task file already expects (`docs/specs/25-spec-tool-detail-sections/25-tasks-tool-detail-sections.md`). |
| `internal/tui/shared/truncation_test.go` | New file. Table-driven pluralization, short-circuit, and combinator tests plus the source-shape assertion (no `lipgloss` import at package scope). |
| `internal/tui/monitor/monitor_transcript_detail.go` | Owns `boundTranscriptDetail`, `transcriptDetailBytes`, `transcriptDetailRows`, `transcriptDetailTailRows`. Introduce `detailAnchor` and add the tail branch here; keep the head branch behavior byte-for-byte. |
| `internal/tui/monitor/monitor_transcript_detail_test.go` | Existing UTF-8 and byte-first coverage. Extend with the head- and tail-anchor table cases (distinctive first/last rows at width 80). |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Owns `writeItemDetail`, `writeNewCodeCards`, `writeToolActivityDetails`, and the write-truncation notice. Thread the anchor through the two detail writers, replace both `… %d lines hidden` sites, route the write-capture literal through `shared.CaptureTruncatedHint`, and use the shared expand hint. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Existing renderer regressions. Extend with the behavioural fixtures listed under Task 2 and Task 3 proofs (hidden-lines hint text, rebind test, anchor-per-state table). |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Currently asserts `strings.Contains(expanded, "lines hidden")`. Update to the new wording in the same commit (`"more lines"` for the settled expand path). |
| `internal/tui/monitor/monitor_steps.go` | Owns `writeStreamingOutput` and the `outputMaxLines` tail behaviour. Prepend the `EarlierItems` notice when the accumulated non-empty rows exceed `outputMaxLines`. |
| `internal/tui/monitor/monitor_steps_test.go` (or `monitor_test.go`) | Add or extend a fixture covering the streaming-output indicator (Task 3). If a focused steps-panel test file does not exist yet, create `monitor_steps_test.go` for this regression alone. |
| `internal/tui/monitor/monitor_transcript_vocabulary_test.go` | New file. The retired-literal grep-lock regression (FR-06.8) with an inline allowlist for `expandView`'s KB-elision marker. Kept small so a reviewer can inspect the allowlist without opening a fixture. |
| `internal/tui/monitor/keys.go` | Read-only reference: exposes `Toggle` and `ExpandAll`. The renderer reads `m.keys.Toggle.Help().Key`; no change to bindings. |
| `internal/tui/shared/styles.go` | Read-only reference: `Theme.Chat.Hint` (`fgDim` foreground) is the style the shared hint output is wrapped in at every call site. No new style token is added. |
| `docs/specs/25-spec-truncation-vocabulary/25-proofs/` | New sanitized test outputs, terminal captures, and acceptance-check transcripts produced during implementation. |

### Notes

- The shared helpers must remain Monitor-agnostic: `truncation.go`
  takes a `keyHelp` string parameter and never imports the Monitor
  package. This preserves the ARCHITECTURE.md ownership rule and
  matches slice 05's task file expectation that
  `internal/tui/shared/truncation.go` is the sole owner of these
  contracts.
- Keep table-driven tests beside the file they cover. Do not read
  real `.jig/` data for fixtures or proofs; the streaming-output
  fixture builds a synthetic `Model.stepOutput` entry directly.
- Use `lipgloss.Width` for every width assertion. Never count runes
  or bytes to reason about visible cells.
- The prepended `EarlierItems` marker inside `writeItemDetail` /
  `writeNewCodeCards` must remain a non-blank body row after
  `shared.Theme.Chat.Hint.Render(...)` wraps it, so slice 04's
  per-item edge trim (`isStructuralBlank` treats SGR-styled content
  as non-blank) preserves it. A trailing zero-count case must not
  render the marker at all.
- The grep-lock test uses `os.ReadFile` on the Monitor package's
  non-test Go files (walked from the package directory) rather than
  shelling out to `rg`. The exception list is a small in-test
  literal so an added allowed marker is a one-line diff.
- Format only changed Go files with `gofmt -w`. Do not rewrite
  unrelated files.
- Run focused Monitor tests during iteration, then the root build,
  test, vet, targeted TUI race (`go test -race ./internal/tui/...`),
  and whitespace checks recorded under Task 4.

### Requirement-to-Test Traceability

| Requirement | Task | Planned Test Artifact |
| --- | --- | --- |
| FR-06.1 | 1.1, 1.2, 1.5 | `internal/tui/shared/truncation_test.go` table asserts `MoreItems`/`EarlierItems` singular/plural switching for `n = 0, 1, 2, 240`. |
| FR-06.2 | 1.1, 1.3, 1.5 | Same test file asserts `ExpandHint` returns `""` when `expanded == true`, when `hasMore == false`, when `keyHelp` is empty/whitespace, and returns `"[<key>: Expand]"` otherwise. |
| FR-06.3 | 1.1, 1.4, 1.5 | Same test file asserts `HintLine` joins with a single space, drops the separator when either half is empty, and yields the count phrase alone when the hint is empty. |
| FR-06.4 | 1.1, 1.6 | Source-shape assertion in the same test file uses `os.ReadFile` on `truncation.go` and asserts no `charm.land/lipgloss/v2` import; the callers wrap output in `shared.Theme.Chat.Hint.Render`. |
| FR-06.5 | 1.5 | Combined table-driven fixture covers `n = 0/1/2/240`, both `expanded` states, both `hasMore` states, empty/non-empty/whitespace `keyHelp`. |
| FR-06.6 | 2.1, 2.2, 2.6 | `TestWriteItemDetailHiddenLinesHint`/`TestWriteNewCodeCardsHiddenLinesHint` assert the exact rendered text `"… 4 more lines [enter: Expand]"` after `stripANSI` at both call sites; `TestCaptureTruncatedHint` asserts the shared write-capture helper is used. |
| FR-06.7 | 2.3, 2.6 | `TestWriteItemDetailHintRespectsToggleRebind` rebinds `m.keys.Toggle` to `"ctrl+o"` and asserts the rendered hint contains `"[ctrl+o: Expand]"`. |
| FR-06.8 | 2.4 | `TestTruncationVocabularyRetirement` walks `internal/tui/monitor/*.go` (excluding `_test.go`), asserts no `"lines hidden"` occurrence, asserts no bare `"… "` + `%d` occurrence outside the `expandView` allowlist, and asserts `writeItemDetail`/`writeNewCodeCards` contain the shared helper calls. |
| FR-06.9 | 2.5 | `monitor_transcript_card_test.go` assertion moves from `"lines hidden"` to `"more lines"`; the existing card cache and expand paths remain green, proving the hint still fires. |
| FR-06.10 | 3.1, 3.5 | `TestBoundTranscriptDetailHeadAnchor`/`TestBoundTranscriptDetailTailAnchor`/`TestBoundTranscriptDetailBothPreserveUTF8` extend `monitor_transcript_detail_test.go` with a 16-row fixture at width 80, asserting head-anchor keeps the first 9 + last 3 rows with `hidden == 4`, tail-anchor keeps the last 12 rows with `hidden == 4`, and both remain valid UTF-8. |
| FR-06.11 | 3.2, 3.3 | `TestWriteItemDetailAnchorFromDisplayState` table over `{running, success, error, warning, unknownResult}` asserts the anchor selection is correct at both writers. |
| FR-06.12 | 3.2, 3.3, 3.6 | Same fixture and `TestWriteNewCodeCardsAnchorFromDisplayState` assert the marker is prepended for tail cases and appended for head cases with the surrounding indent/style preserved. |
| FR-06.13 | 3.4, 3.6 | `TestWriteStreamingOutputEarlierLinesIndicator` seeds a `stepOutput` buffer with more than `outputMaxLines` non-empty rows for a `StatusRunning` step and asserts the rendered pane contains `"… N earlier lines"` immediately before the first tail row; the tail rows are the newest `outputMaxLines`. |
| FR-06.14 | 3.7 | `TestChatItemLineRangesStableAcrossAnchorMode` renders a synthetic exchange twice (running vs. success) and asserts `chatItemLineRanges` covers the whole item body in both cases and `n`/`N` lands on the intended anchor row. |
| FR-06.15 | 3.7 (source-diff review) | Final-diff review in Task 4 confirms no change to `internal/transcript`, `internal/toolcall`, or the runner/harness normalization. |

## Tasks

### [ ] 1.0 Publish the shared truncation and expand-hint helpers

Add `internal/tui/shared/truncation.go` with `MoreItems`, `EarlierItems`,
`ExpandHint`, and the `HintLine` combinator. Cover them with the
table-driven fixture at `n = 0/1/2/240`, both `expanded` states, both
`hasMore` states, empty and non-empty `keyHelp`, and the ASCII-brackets
contract. Prove the file does not import `lipgloss` so the styling
contract stays at the caller.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/shared -run 'TestTruncation(MoreItems|EarlierItems|ExpandHint|HintLine)' -v` — table-driven fixture exercises `n = 0/1/2/240`, both `expanded` states, both `hasMore` states, empty/whitespace/non-empty `keyHelp`, and the combinator's separator behavior; demonstrates FR-06.1 through FR-06.5.
- Test: `go test ./internal/tui/shared -run 'TestTruncationSourceShape' -v` — `os.ReadFile` on `internal/tui/shared/truncation.go` asserts no `charm.land/lipgloss/v2` package import; demonstrates FR-06.4.
- Terminal capture: `docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-1-shared-gallery.txt` — records helper output for representative inputs verbatim. Companion `.notes.txt` names the calling test and function.

#### 1.0 Tasks

- [ ] 1.1 Create `internal/tui/shared/truncation.go` with (a) `MoreItems(n int, singular, plural string) string`, (b) `EarlierItems(n int, singular, plural string) string`, (c) `ExpandHint(expanded, hasMore bool, keyHelp string) string`, (d) `HintLine(more, hint string) string`, (e) `CaptureTruncatedHint() string` returning the fixed literal `"… capture truncated at write"`. The file imports only `"strings"` — no `lipgloss`, no `keybind`, no Monitor package.
- [ ] 1.2 In `truncation.go`, define `MoreItems` and `EarlierItems` to switch between `singular` and `plural` when `n == 1` vs otherwise, and to render exactly `"… <n> more <word>"` / `"… <n> earlier <word>"` with a single ASCII space between fields and a single U+2026 (`…`) prefix. No trailing punctuation; no leading space.
- [ ] 1.3 Define `ExpandHint` to short-circuit to `""` when `expanded == true`, `hasMore == false`, or `strings.TrimSpace(keyHelp) == ""`. Otherwise return `"[" + keyHelp + ": Expand]"` (verbatim ASCII brackets, one colon, one space).
- [ ] 1.4 Define `HintLine(more, hint string)` to return `more` when `hint == ""`, `hint` when `more == ""`, and `more + " " + hint` otherwise. Never emit a leading or trailing space and never emit a double space.
- [ ] 1.5 Create `internal/tui/shared/truncation_test.go`. Add `TestTruncationMoreItems`, `TestTruncationEarlierItems`, `TestTruncationExpandHint`, `TestTruncationHintLine` as table-driven fixtures. Cover: `n = 0` (renders `"… 0 more lines"` / `"… 0 earlier lines"`; helpers do not gate on `n > 0` — the caller decides whether to render); `n = 1` (singular); `n = 2` and `n = 240` (plural); `expanded == true` short-circuit; `hasMore == false` short-circuit; `keyHelp = ""` and `keyHelp = "  "` short-circuit; the ASCII-bracket contract; and every `HintLine` combination.
- [ ] 1.6 Add `TestTruncationSourceShape` to the same file. Read `internal/tui/shared/truncation.go` via `os.ReadFile` and assert `!strings.Contains(src, "charm.land/lipgloss/v2")`. This locks the "no styling in the helper" invariant.
- [ ] 1.7 Run `gofmt -w` on every file touched. Run `go test ./internal/tui/shared -run 'TestTruncation' -v` and record the output for the gallery text file. Generate `25-proofs/25-task-1-shared-gallery.txt` by invoking the helpers with a tiny standalone test (or by inspecting the table-driven fixture's rendered outputs). Write `25-proofs/25-task-1-shared-gallery.notes.txt` with the test file/function and Go version.

### [ ] 2.0 Retire the four Monitor phrasings and grep-lock the vocabulary

Replace the two `… %d lines hidden` sites in `writeItemDetail` and
`writeNewCodeCards` with `shared.MoreItems` + `shared.ExpandHint` +
`shared.HintLine`, route the write-capture literal through
`shared.CaptureTruncatedHint`, thread the live `Toggle.Help().Key`
into the hint, and add the grep-lock regression that fails if any
retired literal reappears.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestWriteItemDetailHiddenLinesHint|TestWriteNewCodeCardsHiddenLinesHint|TestCaptureTruncatedHint' -v` — asserts the exact rendered text at both writers and the shared write-capture helper is invoked; demonstrates FR-06.6.
- Test: `go test ./internal/tui/monitor -run 'TestWriteItemDetailHintRespectsToggleRebind' -v` — rebinds `m.keys.Toggle` and asserts the hint reflects the new key text; demonstrates FR-06.7.
- Test: `go test ./internal/tui/monitor -run 'TestTruncationVocabularyRetirement' -v` — the grep-lock regression from FR-06.8 with an inline allowlist for `expandView`'s `"\n… %d KB elided …\n"`; demonstrates FR-06.8.
- Test: `go test ./internal/tui/monitor -run 'TestTranscriptCardPreservesHiddenLinesNotice' -v` — the existing 300-entry expand test with its assertion migrated from `"lines hidden"` to `"more lines"` continues to pass; demonstrates FR-06.9.
- Terminal capture: `docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-2-monitor-vocabulary.txt` — an 80-column synthetic exchange whose Input detail exceeds `transcriptDetailRows`. `.notes.txt` records `Toggle.Help().Key`, the item's `displayState`, terminal geometry.

#### 2.0 Tasks

- [ ] 2.1 In `internal/tui/monitor/monitor_transcript_items_view.go`, change `writeItemDetail`'s `hidden > 0` branch (~line 240) to:
  ```go
  hint := shared.ExpandHint(expanded, hidden > 0, m.keys.Toggle.Help().Key)
  line := shared.HintLine(shared.MoreItems(hidden, "line", "lines"), hint)
  b.WriteString("      " + shared.Theme.Chat.Hint.Render(line) + "\n")
  ```
  Thread an `expanded bool` parameter through to `writeItemDetail` (its callers already know their per-item expand state from `m.chatItemExpandAll || m.chatItemExpand[item.key]`). Do not import a new package.
- [ ] 2.2 In `writeNewCodeCards` (~line 306), make the same substitution with the writer's four-space indent. The `expanded` parameter is passed by `writeToolActivityDetails`'s caller in `writeTranscriptItem`.
- [ ] 2.3 Route the write-time capture-truncated literal (`monitor_transcript_items_view.go:127`) through `shared.CaptureTruncatedHint()`. The rendered output stays byte-identical to the current `"… capture truncated at write"` after `stripANSI`, but the string constant lives in one place.
- [ ] 2.4 Create `internal/tui/monitor/monitor_transcript_vocabulary_test.go` with `TestTruncationVocabularyRetirement`. Walk `internal/tui/monitor` for non-test `.go` files via `os.ReadDir` + `filepath.Ext`, read each with `os.ReadFile`, and assert:
  - No file contains the substring `"lines hidden"`.
  - No file except `monitor_transcript.go` contains a `"… "` string literal followed on the same line by a `%d` conversion. The `expandView` marker is the sole allowed occurrence; the allowlist is a `map[string]struct{}` in the test.
  - `monitor_transcript_items_view.go` contains both `shared.MoreItems(` and `shared.ExpandHint(` (proving the helpers are wired in at the retired sites, not merely renamed inline).
- [ ] 2.5 Add `TestWriteItemDetailHiddenLinesHint`, `TestWriteNewCodeCardsHiddenLinesHint`, `TestCaptureTruncatedHint`, and `TestWriteItemDetailHintRespectsToggleRebind` to `monitor_transcript_items_view_test.go`. Each uses `syntheticExchange`, `newMonitorWithSteps`, and `setChatPage`; each captures the rendered body via `ansiStrip(m.itemTranscriptBody())` and asserts the exact substrings `"… 4 more lines [enter: Expand]"`, `"… capture truncated at write"`, and (in the rebind case) `"[ctrl+o: Expand]"`.
- [ ] 2.6 Update `monitor_transcript_card_test.go`'s `TestTranscriptCardPreservesHiddenLinesNotice` (currently asserts `strings.Contains(expanded, "lines hidden")`) to assert `strings.Contains(expanded, "more lines")`. Do not add a second assertion for the old wording; pre-v1 discipline (AGENTS.md) forbids dual-wording branches.
- [ ] 2.7 Run `gofmt -w` on every file this task modifies. Run `go test ./internal/tui/monitor -run 'TestWriteItemDetail|TestWriteNewCodeCards|TestCaptureTruncated|TestTruncationVocabularyRetirement|TestTranscriptCardPreservesHiddenLinesNotice' -v -count=1` and record the output.
- [ ] 2.8 Generate the `25-task-2-monitor-vocabulary.txt` capture and its `.notes.txt` from a synthetic exchange at width 80 whose Input detail exceeds `transcriptDetailRows`. Use `stripANSI` on the captured body and include both the styled and stripped forms in the artifact so the reviewer can see the exact hint text.

### [ ] 3.0 Add the anchor mode to `boundTranscriptDetail` and the live-tail indicator

Introduce the `detailAnchor` enum, extend `boundTranscriptDetail`, thread
the anchor selection through `writeItemDetail`/`writeNewCodeCards`
driven by `item.displayState`, and add the Steps-panel drop indicator
inside `writeStreamingOutput`.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestBoundTranscriptDetail(HeadAnchor|TailAnchor|BothPreserveUTF8)' -v -count=1` — extended `monitor_transcript_detail_test.go` cases; demonstrates FR-06.10.
- Test: `go test ./internal/tui/monitor -run 'TestWriteItemDetailAnchorFromDisplayState|TestWriteNewCodeCardsAnchorFromDisplayState' -v -count=1` — asserts the anchor selection and marker position per `toolDisplayState`; demonstrates FR-06.11, FR-06.12.
- Test: `go test ./internal/tui/monitor -run 'TestWriteStreamingOutputEarlierLinesIndicator' -v -count=1` — asserts the Steps-panel drop indicator; demonstrates FR-06.13.
- Test: `go test ./internal/tui/monitor -run 'TestChatItemLineRangesStableAcrossAnchorMode' -v -count=1` — asserts line-range coverage remains consistent across anchor modes; demonstrates FR-06.14.
- Terminal capture: `docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-3-anchor-gallery.txt` — three cards at 80 columns (settled head-anchor, running tail-anchor, streaming-output tail). `.notes.txt` records `displayState`s, `Toggle.Help().Key`, terminal geometry.

#### 3.0 Tasks

- [ ] 3.1 In `internal/tui/monitor/monitor_transcript_detail.go`, introduce the package-private type `detailAnchor` with constants `detailAnchorHead` and `detailAnchorTail`. Change `boundTranscriptDetail`'s signature to `(content string, width int, anchor detailAnchor) (shown string, hidden int)`. Keep the byte-cap and row-cap constants unchanged. Head branch: preserve the existing head+tail behavior byte-for-byte. Tail branch: after the byte cap and row split, if `len(rows) > transcriptDetailRows`, keep `rows[len(rows)-transcriptDetailRows:]` and set `hidden = len(rows) - transcriptDetailRows`.
- [ ] 3.2 In `internal/tui/monitor/monitor_transcript_items_view.go`, extend `writeItemDetail` and `writeNewCodeCards` to accept a `displayState toolDisplayState` parameter (or an `anchor detailAnchor` computed by the caller — pick the parameter form that keeps the writer generic; the spec's recommendation is `anchor`). Choose the anchor at each caller: `detailAnchorTail` when `displayState == toolDisplayRunning`, else `detailAnchorHead`. Pass it into `boundTranscriptDetail`.
- [ ] 3.3 When `anchor == detailAnchorTail` and `hidden > 0`, emit the hidden-line notice as the *first* body row (six-space or four-space indent + `shared.Theme.Chat.Hint.Render(shared.HintLine(shared.EarlierItems(hidden, "line", "lines"), hint))`), then the body rows. When `anchor == detailAnchorHead` and `hidden > 0`, emit the body rows, then the notice as the *last* row using `shared.MoreItems`. Keep both indentations identical to the surrounding body writer's existing indent.
- [ ] 3.4 In `internal/tui/monitor/monitor_steps.go`, extend `writeStreamingOutput` to capture `dropped := len(recent) - outputMaxLines` before the tail slice, then, after the `"▸ <step id>"` header and before the tail-row loop, if `dropped > 0`, prepend `"    " + shared.Theme.Chat.Hint.Render(shared.EarlierItems(dropped, "line", "lines")) + "\n"`. Do not call `ExpandHint` (no expand affordance is offered in the Steps panel).
- [ ] 3.5 Extend `monitor_transcript_detail_test.go`. Add `TestBoundTranscriptDetailHeadAnchor` (16 rows, width 80, distinctive first and last rows, assert head+tail rows survive with `hidden == 4`), `TestBoundTranscriptDetailTailAnchor` (same fixture, assert last 12 rows survive with `hidden == 4`, distinctive last row present, distinctive first row absent), and extend the existing UTF-8 case with a `detailAnchorTail` sub-case.
- [ ] 3.6 Add `TestWriteItemDetailAnchorFromDisplayState` and `TestWriteNewCodeCardsAnchorFromDisplayState` to `monitor_transcript_items_view_test.go`. Table over `{toolDisplayRunning, toolDisplaySuccess, toolDisplayError, toolDisplayWarning, toolDisplayUnknownResult}`. For each case, assert the rendered body contains the correctly-positioned marker (leading for `Running`, trailing for the others) with the shared vocabulary and, for the `Running` case, the newest content row is the last content row and the distinctive first row is absent.
- [ ] 3.7 Add `TestWriteStreamingOutputEarlierLinesIndicator` to `monitor_steps_test.go` (create the file if it does not exist). Build a `Model` with a `StatusRunning` step whose `stepOutput[<id>]` buffer contains, for example, 15 non-empty rows tagged `line-01`..`line-15`; render the Steps view; assert the rendered pane contains `"… 5 earlier lines"` on the row immediately before the row containing `"line-06"`, and that `"line-01".."line-05"` are absent.
- [ ] 3.8 Add `TestChatItemLineRangesStableAcrossAnchorMode` to `monitor_transcript_items_view_test.go`. Render a synthetic exchange twice — once with `displayState = toolDisplayRunning`, once with `toolDisplaySuccess` — at `transcriptInnerW = 80`, `expandAll = true`. Assert `chatItemLineRanges[key].end - .start + 1` equals the actual number of rows in the rendered body for both cases so `n`/`N` navigation remains correct.
- [ ] 3.9 Run `gofmt -w` on every file this task modifies. Run `go test ./internal/tui/monitor -run 'TestBoundTranscriptDetail|TestWriteItemDetailAnchor|TestWriteNewCodeCardsAnchor|TestWriteStreamingOutputEarlierLinesIndicator|TestChatItemLineRangesStableAcrossAnchorMode' -v -count=1`. Generate the `25-task-3-anchor-gallery.txt` capture and its `.notes.txt` with three sections: (a) settled head-anchor tool exchange, (b) running tail-anchor tool exchange, (c) Steps-panel streaming output with drop indicator. Record which style token the streaming indicator uses (Q-06.3) in the notes.

### [ ] 4.0 Record acceptance evidence and confirm scope integrity

Run the applicable repository acceptance commands, capture their output
alongside a short limitations note for anything that could not run, and
close the loop with a final-diff review that proves the change did not
leak into unrelated slices or into the transcript wire format.

#### 4.0 Proof Artifact(s)

- CLI: `docs/specs/25-spec-truncation-vocabulary/25-proofs/25-task-4-acceptance/{build,test,vet,race-tui,gofmt,git-diff-check}.txt` — the captured outputs of `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/...`, `gofmt -l <changed-go-files>`, and `git diff --check`. Any unavailable check is recorded as a limitation in `25-task-4-limitations.md`, not replaced by a substitute claim.
- Final-diff review recorded in the PR body confirming the change did not touch other epic slices (no header-grammar change, no detail-section conversion beyond the anchor thread, no diff renderer, no grouped-read tree, no glyph-preset table, no wire/format/harness change, no palette hex additions, no new `lipgloss.NewStyle`).

#### 4.0 Tasks

- [ ] 4.1 Run the acceptance commands in order: `go build ./cmd/jig`, `go vet ./...`, `go test -race ./internal/tui/...`, `go test ./...`, `gofmt -l` on the changed Go files, and `git diff --check`. Capture each command's stdout+stderr and exit code to a file under `25-proofs/25-task-4-acceptance/`.
- [ ] 4.2 If any command cannot run in the validation environment (for example, headless-Chrome-dependent visual proofs are unavailable), record the missing check and its exact reason in `25-proofs/25-task-4-limitations.md`. Do not substitute a component-only claim for a blocked check.
- [ ] 4.3 Final-diff review: read the cumulative diff and confirm the touched files are limited to `internal/tui/shared/truncation.go`, `internal/tui/shared/truncation_test.go`, `internal/tui/monitor/monitor_transcript_detail.go`, `internal/tui/monitor/monitor_transcript_detail_test.go`, `internal/tui/monitor/monitor_transcript_items_view.go`, `internal/tui/monitor/monitor_transcript_items_view_test.go`, `internal/tui/monitor/monitor_transcript_card_test.go`, `internal/tui/monitor/monitor_transcript_vocabulary_test.go` (new), `internal/tui/monitor/monitor_steps.go`, `internal/tui/monitor/monitor_steps_test.go` (new or extended), and files under `docs/specs/25-spec-truncation-vocabulary/`. No changes to `internal/transcript/`, `internal/toolcall/`, `internal/runner/`, `internal/harness/`, `internal/tui/shared/palette.go`, or `internal/tui/shared/styles.go`.
- [ ] 4.4 Write `25-proofs/25-task-4-summary.md` recording the exact commands, outcomes, artifact paths, requirement coverage (FR-06.1 through FR-06.15), and any limitation. This is the reviewer-first proof summary.
