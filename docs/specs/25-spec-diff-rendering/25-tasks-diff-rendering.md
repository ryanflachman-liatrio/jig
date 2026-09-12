# 25-tasks-diff-rendering.md

## Planning Basis

### Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Pre-v1 discipline; shared TUI primitives belong in `internal/tui/shared` (with a Monitor-agnostic API); preserve persistence-off; do not preserve deprecated code paths or feature flags; fabricated fixtures only. | none |
| `CLAUDE.md` | yes | Route through the workflow reading map; do not maintain a second copy of shared rules. | none |
| `docs/ARCHITECTURE.md` | yes | Shared TUI primitives may not import Monitor; child TUI packages keep acyclic ownership; a diff-specific renderer that assumes Monitor state stays inside `internal/tui/monitor`. | none |
| `docs/CONVENTIONS.md` | yes | Prefer cohesive owner-local helpers; keep APIs narrow; bound inputs before rendering; distinguish absent from explicit zero (`OldText *string` semantics). | none |
| `docs/TUI.md` | yes | Measure cells with `lipgloss.Width` and ANSI-aware helpers; semantic styles owned by `Styles`; rebuild width-baked renderers on resize; complete cache invalidation identity. | none |
| `docs/TESTING.md` | yes | Table-driven synthetic TUI cases; assert visible behavior after `stripANSI`; targeted race for TUI; deterministic terminal captures; visual supplements optional. | none |
| `docs/adr/0001-manual-border-title-compositing.md` | yes | One manual titled-border compositor; do not add a second (this slice does not touch it). | none |
| `go.mod` / `go.sum` | yes | Go 1.25.12; Charm v2 (Lip Gloss 2.0.5); `github.com/hexops/gotextdiff v1.0.3` already in `go.sum` transitively via `alecthomas/chroma/v2` test tree. | none |
| `mise.toml` | yes | Go 1.25 series toolchain. | none |
| `docs/epics/omp-transcript-parity/epic.md` | yes | CC-1 (card is the unit), CC-8 (one truncation vocabulary), CC-10 (diff computation is new work; three options with recommendation A), CC-12 (no `toolcall.Activity` changes); NG5 (no transcript wire changes); EC-4 (diff for `OldText != nil`, resulting-source for `OldText == nil`). | none |
| `docs/epics/omp-transcript-parity/slices/07-diff-rendering.md` | yes | Full slice source: gutter shape, width floor at 3, duplicate suppression, 1↔1 word diff, batch-highlighted context, indentation viz, foreground-only colors, `+N/-M` badge, collapse budgets, wrap terminator, clamped-content hazard. Q-07.1..Q-07.4 with recommendations. | none |
| `docs/epics/omp-transcript-parity/slices/06-truncation-vocabulary.md` | yes | Head/tail anchor policy; the shared vocabulary is fixed for the epic and slice 07 consumes it. | none |
| `docs/epics/omp-transcript-parity/slices/05-tool-detail-sections.md` | yes | Slice 05 (not yet implemented) will re-host detail bodies inside `CardSection`; slice 07 keeps its producer content-only so slice 05's re-host swaps the caller, not the producer. | none |
| `docs/specs/25-spec-transcript-card-primitive/25-spec-transcript-card-primitive.md` | yes | `shared.Card`, `shared.RenderCard`, `CardContentWidth`, tint SGR-reset stabilization; state-based cache identity; wholesale invalidation on width change. | none |
| `docs/specs/25-spec-status-line-header-grammar/25-spec-status-line-header-grammar.md` | yes | Four-slot header grammar (Icon/Title/Description/Meta); Meta today carries the error hint; badge belongs in Meta on expanded non-error exchanges; collapsed row stays single-line. | none |
| `docs/specs/25-spec-truncation-vocabulary/25-spec-truncation-vocabulary.md` | yes | Slice-06 helpers (`MoreItems`, `EarlierItems`, `ExpandHint`, `HintLine`, `CaptureTruncatedHint`) and the `detailAnchor` enum; live `m.keys.Toggle.Help().Key` for the expand hint. | none |
| `docs/specs/25-spec-vertical-rhythm-and-block-edges/25-spec-vertical-rhythm-and-block-edges.md` | yes | Per-item edge-trim discipline (`isStructuralBlank` treats SGR-styled content as non-blank); a prepended hint row is preserved as intentional content. | none |
| `internal/toolcall/toolcall.go` | yes | `Content.Diff` is `Diff{Path, OldText *string, NewText string}`; `IsEdit()` returns true when `Kind == "edit"` or any content carries a `Diff`. Wire contract is frozen (CC-12). | none |
| `internal/transcript/writer.go` | yes | `clampActivity` clamps `Diff.OldText`, `Diff.NewText`, and `Diff.Path` per-value and per-aggregate; sets `Block.Truncated = true` when it does. | none |
| `internal/tui/diffview/diff.go` | yes | `Parse(string) *Presentation` accepts `--- a/<name>` / `+++ b/<name>` / `@@ -a,b +c,d @@` unified patches; `Presentation.Rows`, `Hunks`, and `Files` are populated; `RenderHunkHeader` and `RenderRawLine` reuse `shared.Theme.Diff.*`. | none |
| `internal/tui/monitor/monitor_transcript_items_view.go` | yes | `writeNewCodeCards` currently uses `content.Diff.NewText` alone at four-space indent; `writeToolActivityDetails` skips content with `Diff != nil`. The renderer to replace lives here. | none |
| `internal/tui/monitor/monitor_layout.go` | yes | `insetRenderer` is a `glamour.TermRenderer` rebuilt on width change; `chatItemRendered` is cleared wholesale when `transcriptInnerW` changes. | none |
| `internal/tui/monitor/monitor_model.go` | yes | `transcriptRenderKey`, `transcriptRenderSurface` (`transcriptRenderMarkdown`, `transcriptRenderDetail`, `transcriptRenderCard`); adding a `transcriptRenderDiff` constant is the intended extension. | none |
| `internal/tui/shared/styles.go` | yes | Charm palette tokens (`fgDim`, `fgMuted`, `success`, `danger`); `Theme.Diff.Add/Remove/Hunk` exist as foreground styles; new `Diff.Context`, `Diff.Intraline`, `Diff.Gutter`, `Diff.Indent` bind to existing tokens (no new hex). | none |
| `internal/tui/shared/truncation.go` | yes | Slice-06-owned helpers; `CaptureTruncatedHint()` is the pattern for a fixed labelled message living in this file. `DiffClampedHint()` and `DiffUnavailableHint()` extend that pattern for slice 07. | none |
| `internal/tui/shared/card.go` | yes | `CardContentWidth`, tint SGR-reset stabilization; `RenderCard` hard-wraps content at `contentWidth`; the diff producer's `[]string` output is a plain rows list, so wrapping stays owned by the producer. | none |
| `CONTRIBUTING.md` | not found | No additional contribution policy present. | none |
| `.github/pull_request_template.md` | not found | No PR template present. | none |
| `.github/workflows/` | not found | No checked-in CI workflow defines additional gates. | none |

The spec resolves every material design choice: Option A with
`hexops/gotextdiff` for computation (Q-07.1, ADR in Task 1);
`DiffClampedHint()` for the correctness hazard (Q-07.2); the review
workspace is out of scope (Q-07.3); hunk headers stay (Q-07.4); the
producer is a Monitor-package-private `[]string` returner; the badge
appears in Meta on expanded non-error exchanges only. There is no
design alternative to record beyond the ADR's evaluation of Options
A/B/C.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_diff_compute.go` | New. Owns `computeDiff(*toolcall.Diff)` → `(*diffview.Presentation, diffStats, computeOutcome)`, the byte/line caps, and the unified-patch emission. Uses `hexops/gotextdiff` and feeds `diffview.Parse`. |
| `internal/tui/monitor/monitor_diff_compute_test.go` | New. Table-driven cases from FR-07.1..FR-07.5 plus determinism. |
| `internal/tui/monitor/monitor_diff_render.go` | New. Owns `renderDiffRows(...)`, the gutter formatter, duplicate-number suppression, the 1↔1 intra-line pass, context-run batch highlighting, indentation visualization, wrap/terminator, and the collapse budget. |
| `internal/tui/monitor/monitor_diff_render_test.go` | New. Table-driven cases from FR-07.6..FR-07.14. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Modified. `writeNewCodeCards` calls `computeDiff` and either renders diff rows via `renderDiffRows` or falls back to `renderNewCodeCard`. `composeToolHeader` receives the `+N/-M` badge in Meta for expanded non-error exchanges. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Extended. Adds diff-rendering behavioral fixtures: canonical shapes, file-creation fallback, computation failure fallback, byte/line bypass, clamped-content hint, badge presence, cache/width lifecycle, persistence-off. |
| `internal/tui/monitor/monitor_model.go` | Modified. Adds `transcriptRenderDiff` to `transcriptRenderSurface`. |
| `internal/tui/monitor/monitor_layout.go` | Read-only reference. `insetRenderer` is already width-baked and cleared on `WindowSizeMsg`; no changes required. |
| `internal/tui/monitor/monitor_transcript_card_test.go` | Extended. Adds line-range accounting cases for expanded diff exchanges so `chatItemLineRanges` covers the added rows. |
| `internal/tui/shared/styles.go` | Modified. Adds `Diff.Context`, `Diff.Intraline`, `Diff.Gutter`, `Diff.Indent` field entries; `DefaultTheme()` binds them to `fgDim` / `fgMuted` / (Reverse) using existing palette tokens. No new hex constants. |
| `internal/tui/shared/styles_test.go` | Extended. Adds coverage asserting the new Diff-token style entries exist and use foreground-only styling. |
| `internal/tui/shared/truncation.go` | Modified. Adds `DiffClampedHint()` and `DiffUnavailableHint()` (fixed literals, ASCII-only). Preserves the "no `lipgloss` import" invariant. |
| `internal/tui/shared/truncation_test.go` | Extended. Adds cases asserting the new helpers' fixed wording, and re-asserts the source-shape invariant (no `lipgloss` import). |
| `go.mod` | Modified. Promotes `github.com/hexops/gotextdiff v1.0.3` from transitive to direct require. |
| `docs/adr/0013-diff-computation-library.md` | New. Records the Option A choice, evaluated alternatives (Options B and C), license (Apache-2.0), supply-chain assessment (already in `go.sum` via `alecthomas/chroma/v2` test tree; no new runtime dependencies of its own), caps, and the rejection reasons for B and C. The implementer verifies the next unused ADR slot (existing ADRs go up to `0012` at the time of this task file) before creating the file. |
| `docs/specs/25-spec-diff-rendering/25-proofs/` | New sanitized proofs, terminal captures, and acceptance-check transcripts produced during implementation. |

### Notes

- Tasks use a red-green-refactor sequence: add the focused failing
  assertion, implement the smallest coherent behavior, then run the
  focused package tests.
- Complete parent tasks in order. Task 2 depends on Task 1's
  `computeDiff` shape; Task 3 depends on both.
- Keep raw agent-supplied text (paths, `OldText`, `NewText`) behind
  the display sanitizer described in the spec's Security Considerations
  before it enters a styled diff row. Do not modify the durable
  transcript; the projection is display-only.
- Generate ANSI and HTML deterministically behind `JIG_UI_SNAPSHOT_DIR`.
  Convert HTML to PNG with the repository's established local
  headless-Chrome process when available; otherwise record the exact
  environment limitation and retain the reproducible ANSI/HTML
  artifacts without fabricating a PNG.
- Run `gofmt` only on changed Go files. Required acceptance commands
  are the focused tests listed per task, `go test -race
  ./internal/tui/... ./internal/helpchat -count=1`, `go build
  ./cmd/jig`, `go test ./...`, `go vet ./...`, and `git diff --check`.
  Record blocked checks in a limitation note rather than claiming
  they passed.

## Requirement-to-Test Traceability

| Requirement | Planned Task(s) | Planned Test Evidence |
| --- | --- | --- |
| FR-07.1 | 1.3, 1.5 | `computeDiff` table asserts each canonical shape produces a non-nil `*diffview.Presentation` with the expected file name, hunk count, and add/remove counts. |
| FR-07.2 | 1.1, 1.2 | ADR at `docs/adr/0013-diff-computation-library.md` (or the next unused slot verified before creation) records the choice and rejection reasons; `go.mod` promotes `hexops/gotextdiff` to a direct require entry. |
| FR-07.3 | 1.4, 1.5 | Cap-bypass cases assert `computeDiff` returns `computeSkippedOversize` when the byte or line cap is exceeded, and the presentation is nil. |
| FR-07.4 | 1.5, 3.3 | A parse-error injection case asserts `computeFailed` is returned; the Monitor test asserts the resulting-source card is rendered with the "diff unavailable" hint. |
| FR-07.5 | 1.6 | Determinism case computes the same input twice and asserts byte-equal patch strings and equal presentations. |
| FR-07.6 | 2.2 | Gutter shape case asserts every visible row matches `<marker><line-no>│<content>` after `stripANSI`. |
| FR-07.7 | 2.2 | Width-floor cases: a 5-line file (gutter width 3) and a 1000-line file (gutter width 4) both align correctly; the 5-line case renders three padding cells. |
| FR-07.8 | 2.3 | Duplicate-suppression case asserts a `-N` immediately followed by `+N` renders the second row with only the marker, and that suppression does not leak across hunks. |
| FR-07.9 | 2.4 | 1↔1 replacement case asserts a reverse-video escape appears in the changed span and that a leading-whitespace-only difference is not inverted. |
| FR-07.10 | 2.5 | Multi-line change case asserts no reverse-video escape appears on the change rows. |
| FR-07.10 (context syntax) | 2.6 | Context-run case renders a multi-line string-literal fixture and asserts chroma foreground escapes appear on context rows and are absent on added/removed rows. |
| FR-07.11 | 2.7 | Indentation case renders leading spaces and tabs and asserts dim `·` and dim `→` render sequences appear. |
| FR-07.12 | 2.8 | Background-freedom case asserts no `\x1b[48` sequence originates from the diff renderer. |
| FR-07.13 | 2.9 | Wrap case renders a line wider than the content width and asserts the continuation row has a blank gutter, a `│` column, and a terminating SGR close for reverse video and foreground. |
| FR-07.14 | 2.10 | Collapse case renders 12 hunks × 200 lines and asserts the footer row is composed through `shared.MoreItems`/`shared.EarlierItems`/`shared.HintLine`, with a single leading ellipsis and the live expand key. |
| FR-07.15 | 3.2, 3.3 | Monitor case asserts the section label is `Diff · <path>` on success, and the "diff unavailable" hint appears when `computeDiff` fails or is skipped. |
| FR-07.16 | 3.2 | File-creation case (`OldText == nil`) asserts the existing `New code · <path>` card renders unchanged with no fallback hint. |
| FR-07.17 | 3.3 | Clamped-block case sets the enclosing block's `Truncated = true` and asserts the shared `DiffClampedHint` is the section's first body row. |
| FR-07.18 | 3.4 | Header-badge case asserts the Meta slot contains `+N/-M` for expanded non-error edits and is absent for collapsed exchanges and errored exchanges. |
| FR-07.19 | 3.2, 3.5 | Section-hosting case asserts `renderDiffRows` output does not introduce a nested border, that indentation is four spaces below the header card, and that the same producer at a hypothetical `CardSection` width returns the same content shape. |
| FR-07.20 | 3.6 | Anchor case renders a running edit exchange whose diff exceeds `transcriptDetailRows` and asserts the tail rows survive with the slice-06 prepended `EarlierItems` marker. |
| FR-07.21 | 3.7 | Cache-lifecycle case renders the same exchange twice at the same width and asserts the rendered bytes are cached; re-rendering at a new width recomputes; state change evicts naturally. |
| FR-07.22 | 4.3 | Final-diff review asserts no touched files under `internal/transcript/`, `internal/toolcall/`, `internal/runner/`, or `internal/harness/`. |

## Tasks

### [ ] 1.0 Compute a unified diff and project it through `diffview`

Add `internal/tui/monitor/monitor_diff_compute.go` with `computeDiff`, the
byte/line caps, and the determinism-preserving patch emission via
`hexops/gotextdiff`. Promote `hexops/gotextdiff` from a transitive to a
direct require and record the ADR at `docs/adr/0013-diff-computation-library.md`
(after verifying the next unused ADR slot). Cover the four canonical
shapes, the file-creation and empty-diff cases, and both cap bypasses.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestComputeDiff(SingleLine|MultiLine|InsertOnly|DeleteOnly|NoChange|FileCreation|OversizeByte|OversizeLine|Deterministic|ParseRoundTrip)' -count=1` passes table-driven cases producing the expected `*diffview.Presentation` shapes and the expected `computeOutcome` on bypass; demonstrates FR-07.1, FR-07.3, FR-07.4, FR-07.5.
- ADR: `docs/adr/0013-diff-computation-library.md` (or the next unused slot verified before creation) records the choice, alternatives, license, supply-chain assessment, and caps; demonstrates FR-07.2.
- Proof summary: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-01-proofs.md` records the exact commands, outcomes, artifact paths, FR-07.1..FR-07.5 coverage, and any limitations.

#### 1.0 Tasks

- [ ] 1.1 Write `docs/adr/0013-diff-computation-library.md` (verify the next unused ADR slot before creating the file; existing ADRs go up to `0012` at the time of this task file). Evaluate Option A (`hexops/gotextdiff`), Option B (hand-rolled Myers/LCS producing a unified string), and Option C (harness-produced patch). Record the license (Apache-2.0 for `hexops/gotextdiff`), the supply-chain footprint (already in `go.sum` transitively via `alecthomas/chroma/v2` test tree; adding it to `require` promotes a known package to a direct dependency without expanding the transitive closure), and the rejection reasons for B and C. Reference the epic's CC-10, NG5, and CC-12 decisions.
- [ ] 1.2 Update `go.mod` to add `github.com/hexops/gotextdiff v1.0.3` to the require block (module version pinned to the same one already listed in `go.sum`). Run `go mod tidy`. Verify `go build ./cmd/jig` still succeeds and no unexpected module transitively promotes.
- [ ] 1.3 Create `internal/tui/monitor/monitor_diff_compute.go`. Define:
  - `diffStats struct{ added, removed int }`
  - `computeOutcome int` with `computeOK`, `computeFileCreation`, `computeSkippedOversize`, `computeFailed`.
  - Package-private caps `diffComputeMaxBytes = 128 * 1024` and `diffComputeMaxLines = 4000`.
  - `func computeDiff(d *toolcall.Diff) (*diffview.Presentation, diffStats, computeOutcome)`.
  Behavior:
  - `d == nil` or `d.OldText == nil` → `(nil, {}, computeFileCreation)`.
  - `len(*d.OldText) + len(d.NewText) > diffComputeMaxBytes` or line count of either side exceeds `diffComputeMaxLines` → `(nil, {}, computeSkippedOversize)`.
  - Otherwise: call `myers.ComputeEdits(span.URIFromPath(path), *d.OldText, d.NewText)`, format with `gotextdiff.ToUnified("a/"+path, "b/"+path, *d.OldText, edits)`, feed the string to `diffview.Parse`, and count added/removed rows over the returned presentation. On any error (empty file names, parse failure) → `(nil, {}, computeFailed)`.
  - The path may be empty; use `"file"` as the neutral filename to match `diffview.FileName`'s fallback.
- [ ] 1.4 Add package-private helpers if useful (line-count without materializing a slice; a small utility to count add/remove rows over `presentation.Rows`). Keep them owner-local and unexported.
- [ ] 1.5 Create `internal/tui/monitor/monitor_diff_compute_test.go`. Table-driven cases:
  - Single-line replacement (`OldText = "a\nb\nc\n"`, `NewText = "a\nB\nc\n"`) → one hunk, one add, one remove, `Files[0].Name == "sample.go"`.
  - Multi-line replacement (`b\nc` → `X\nY`) → two adds, two removes in one hunk.
  - Pure insertion (append `d`) → one add, zero removes.
  - Pure deletion (drop `b`) → zero adds, one remove.
  - No-change (`OldText == NewText`) → `computeOK` with zero adds and zero removes and no hunks.
  - File creation (`OldText == nil`) → `computeFileCreation`, nil presentation.
  - Byte-cap bypass (a synthetic 200 KiB `OldText`) → `computeSkippedOversize`.
  - Line-cap bypass (5000 lines) → `computeSkippedOversize`.
  - Parse-error injection (a monkey-patched `myers.ComputeEdits` is unnecessary; instead invoke `computeDiff` with a well-formed input and simply assert `computeOK` — the parse-error branch is exercised only in the fallback test in Task 3, since a real-world parse error is not reproducible from a legitimate `gotextdiff` output).
- [ ] 1.6 Add `TestComputeDiffDeterministic` computing the same input twice and asserting the emitted patch strings and the `presentation.Hunks` are byte- and shape-equal. Add `TestComputeDiffRoundTripsThroughDiffview` asserting `presentation.ParseErr == nil` on each canonical shape.
- [ ] 1.7 Run `gofmt -w` on every file touched. Run `go test ./internal/tui/monitor -run 'TestComputeDiff' -v -count=1`. Write `25-proofs/25-task-01-proofs.md` with the exact commands, outcomes, artifact paths, requirement coverage, and any limitations.

### [ ] 2.0 Render the fused-gutter diff rows

Add `internal/tui/monitor/monitor_diff_render.go` with `renderDiffRows`,
the gutter formatter, duplicate-number suppression, the 1↔1 intra-line
pass, context-run batch highlighting, indentation visualization, and the
collapse budget wired through the slice-06 vocabulary. Register the new
`Theme.Diff.Context`, `Theme.Diff.Intraline`, `Theme.Diff.Gutter`, and
`Theme.Diff.Indent` style tokens.

#### 2.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestRenderDiff(GutterShape|WidthFloor|DuplicateSuppression|IntralineReverseVideo|MultiLineNoIntraline|ContextSyntax|Indentation|NoBackground|WrapContinuation|CollapseBudget)' -count=1 -v` passes; demonstrates FR-07.6..FR-07.14.
- Test: `go test ./internal/tui/shared -run 'TestThemeDiffTokens' -count=1 -v` asserts the new style tokens exist and use foreground-only style (no `.Background(...)`); demonstrates FR-07.12.
- Terminal capture: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-2-diff-gallery.txt` shows a single-line replacement (with intra-line span), a multi-line replacement (no intra-line), a pure insertion, a pure deletion, and a collapsed many-hunk case at widths 60 and 90.
- Proof summary: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-02-proofs.md` records the commands, outcomes, artifact paths, requirement coverage, and limitations.

#### 2.0 Tasks

- [ ] 2.1 Extend `internal/tui/shared/styles.go` with the `Diff.Context`, `Diff.Intraline`, `Diff.Gutter`, `Diff.Indent` field entries on the existing `Diff` struct. In `DefaultTheme()` bind them to `fgDim` (Context, Gutter, Indent) and `lipgloss.NewStyle().Reverse(true)` (Intraline). Do not introduce a new hex constant. Add `TestThemeDiffTokens` to `internal/tui/shared/styles_test.go` asserting the four new entries exist and none of them calls `.Background(...)`; use `reflect` or a source-shape assertion (`os.ReadFile` on `styles.go` searching for `.Background` inside the `Diff` bindings).
- [ ] 2.2 Create `internal/tui/monitor/monitor_diff_render.go`. Implement `formatDiffGutter(marker byte, lineNo int, hasLineNo bool, width int) string` returning `<pad><marker><line-no>│` with the marker+number concatenated and right-aligned as one token, width floored at `max(width, 3)`. Blank-line-number rows (`hasLineNo == false`) render as `<pad><marker>│`. Add `TestRenderDiffGutterShape` and `TestRenderDiffWidthFloor` (5-line file and 1000-line file) to `monitor_diff_render_test.go`.
- [ ] 2.3 Implement `renderDiffRows(pres *diffview.Presentation, path string, width int, expanded bool, insetRenderer *glamour.TermRenderer, expandKey string) []string`. Iterate `pres.Rows`, respecting `pres.Hunks` for hunk-boundary tracking. Suppress a line number when it equals the previously emitted row's *within the same hunk*. Do not suppress across hunks. Add `TestRenderDiffDuplicateSuppression` covering: a single-line `-N`/`+N` replacement, an insert followed by context sharing the same new-line number, and the negative case where a hunk boundary breaks suppression.
- [ ] 2.4 Implement the 1↔1 intra-line pass. Group consecutive `RowDelete` rows then consecutive `RowAdd` rows. When both runs are length 1, compute a word-level LCS over Unicode word boundaries (`unicode.IsSpace` splits) between the two content strings, wrap the changed spans in `shared.Theme.Diff.Intraline`, and exclude leading whitespace of the first changed part. Add `TestRenderDiffIntralineReverseVideo` asserting a reverse-video escape (`\x1b[7m`) appears in the changed span and does not wrap a leading-whitespace-only difference. Add `TestRenderDiffMultiLineNoIntraline` asserting no reverse-video escape appears for a multi-line change block.
- [ ] 2.5 Implement context-run batch highlighting. Collect consecutive `RowContext` rows into runs; per run, render `fencedCode(path, strings.Join(rows, "\n"))` through `insetRenderer` and split the result back to per-row strings. On any error, fall back to `shared.Theme.Diff.Context` foreground styling per row. Add `TestRenderDiffContextSyntax` with a Go source fixture containing a multi-line string literal and assert chroma foreground escapes appear on context rows and are absent on added/removed rows.
- [ ] 2.6 Implement indentation visualization. Before styling a row's content, convert leading spaces to `\x1b[2m·\x1b[22m` (dim middle dot) and leading tabs to `\x1b[2m→ \x1b[22m` (or the `diffTabWidth = 3` equivalent). Non-leading tabs render as plain spaces. Apply to added, removed, and unhighlighted context rows. Add `TestRenderDiffIndentation` asserting both escapes appear.
- [ ] 2.7 Enforce foreground-only styling. Add `TestRenderDiffNoBackground` reading every rendered row and asserting no `\x1b[48` sequence originates from the diff renderer. (Chroma-emitted background escapes inside the batch-highlighted context are permitted only when the terminal-safe sanitizer removed them; the assertion targets the diff renderer's *own* output.)
- [ ] 2.8 Implement wrap continuation. When a rendered row's `lipgloss.Width` exceeds the section content width, break it at the last cell that fits and emit a continuation row with a blank gutter (spaces to the gutter width) followed by `│` and the wrapped tail. Terminate every emitted row with `\x1b[27m\x1b[39m` so subsequent frame padding is not painted with reverse video or a foreground color. Add `TestRenderDiffWrapContinuation` asserting the continuation row shape and the terminating SGR close.
- [ ] 2.9 Implement the collapse budget. When `len(pres.Hunks) > diffCollapsedHunks (=8)` or the visible row count exceeds `diffCollapsedLines (=40)`, keep the first eight hunks (or the first budget-fitting subset) and the first forty rows, then emit a single footer row composed as: `shared.HintLine(<hunks phrase>, <lines phrase>) + " " + <expand hint>`, with `<hunks phrase>` from `shared.MoreItems(hh, "hunk", "hunks")`, `<lines phrase>` from `strings.TrimPrefix(shared.MoreItems(hl, "line", "lines"), "… ")` so only one leading ellipsis appears, and `<expand hint>` from `shared.ExpandHint(expanded, hh+hl > 0, expandKey)`. Style the footer row through `shared.Theme.Chat.Hint.Render`. When `expanded == true`, do not apply the collapse. Add `TestRenderDiffCollapseBudget` for the `hh=3/hl=12` and `hh=0/hl=0` cases.
- [ ] 2.10 Run `gofmt -w` on every file touched. Run `go test ./internal/tui/monitor -run 'TestRenderDiff' -count=1 -v` and `go test ./internal/tui/shared -run 'TestThemeDiffTokens' -count=1 -v`. Generate `25-proofs/25-task-2-diff-gallery.txt` by driving a small test that writes the rendered rows for each canonical shape at widths 60 and 90; include a `.notes.txt` recording the terminal geometry, the language derived from the sample path, and the expand key.
- [ ] 2.11 Write `25-proofs/25-task-02-proofs.md` with the exact commands, outcomes, artifact paths, FR-07.6..FR-07.14 coverage, and any limitations.

### [ ] 3.0 Wire the diff renderer into the Monitor with fallback and the header badge

Replace `writeNewCodeCards`'s body with the compute + render pipeline;
preserve the resulting-source card for `OldText == nil` and for
computation failures; label the clamped-content and diff-unavailable
cases through new shared helpers; emit the `+N/-M` badge into the header
Meta slot; register the new `transcriptRenderDiff` cache surface; and
prove the line-range accounting, persistence-off, and cache lifecycle
regressions.

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/monitor -run 'TestMonitorDiff(SectionLabel|FileCreationFallback|ComputeFailureFallback|ByteCapFallback|ClampedContentHint|BadgeExpanded|BadgeAbsentCollapsed|BadgeAbsentErrored|AnchorTailRunning|CacheStableOnRepeatedRender|CacheEvictedOnWidthChange|PersistenceOff|LineRangesCoverBody)' -count=1 -v` passes; demonstrates FR-07.15..FR-07.21.
- Test: `go test ./internal/tui/shared -run 'TestDiffClampedHint|TestDiffUnavailableHint|TestTruncationSourceShape' -count=1 -v` asserts the new shared helpers and re-asserts the no-`lipgloss` invariant; demonstrates the FR-07.15/FR-07.17 helper contract.
- Test: `go test -race ./internal/tui/... ./internal/helpchat -count=1` passes the TUI race suite.
- Terminal capture and screenshot: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-3-monitor-diff.txt` and companion `.png` (when headless Chrome is available) show adjacent expanded exchanges — an edit with intra-line highlights, a file-creation fallback, a clamped-content warning, a collapsed many-hunk diff — at 80 columns.
- CLI: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-3-quality-checks.txt` records `gofmt -l <changed-go-files>`, `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/...`, and `git diff --check`.
- Proof summary: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-03-proofs.md` records the commands, outcomes, artifact paths, FR-07.15..FR-07.21 coverage, and any limitations.

#### 3.0 Tasks

- [ ] 3.1 Extend `internal/tui/shared/truncation.go` with `DiffClampedHint()` returning `"… content clamped at write; diff may be incomplete"` and `DiffUnavailableHint()` returning `"… diff unavailable; showing resulting source"`. Both remain ASCII text and do not import `lipgloss`. Extend `internal/tui/shared/truncation_test.go` with cases asserting each helper's exact literal and re-asserting the file's source-shape invariant (`TestTruncationSourceShape` continues to pass with the new helpers present).
- [ ] 3.2 Modify `internal/tui/monitor/monitor_model.go` to add `transcriptRenderDiff` to `transcriptRenderSurface` (immediately after `transcriptRenderCard`). Update the `transcriptRenderKey` doc comment to mention the diff surface.
- [ ] 3.3 Rewrite `writeNewCodeCards` in `internal/tui/monitor/monitor_transcript_items_view.go`. For each `Content` with a `Diff`:
  - Compute the outcome with `computeDiff(content.Diff)`.
  - `computeFileCreation`: render the existing `renderNewCodeCard` output under the `New code · <path>` label, unchanged. No fallback hint.
  - `computeSkippedOversize` / `computeFailed`: render the existing `renderNewCodeCard` output under the `New code · <path>` label, and prepend `shared.Theme.Chat.Hint.Render(shared.DiffUnavailableHint())` as the section's first body row.
  - `computeOK`: call `renderDiffRows(...)` with the current writer's content width (`m.transcriptInnerW - 4`, matching today's four-space indent) and the live `m.keys.Toggle.Help().Key`. Emit the `Diff · <path>` label. When the enclosing block's `Truncated` flag is true, prepend `shared.Theme.Chat.Hint.Render(shared.DiffClampedHint())` as the section's first body row.
  Keep the anchor-aware slice-06 head/tail truncation (`boundTranscriptDetail(..., anchorForState(item.displayState))`) as the outer bound on the rendered rows so a running edit exchange tail-anchors.
- [ ] 3.4 Emit the `+N/-M` badge. In `composeToolHeader`, when the item is `transcriptItemToolExchange`, `expanded == true`, `item.displayState != toolDisplayError`, and the exchange's activity carries at least one `Diff` whose `computeDiff` returned `computeOK` with a non-empty `diffStats`, append the badge to the `meta` slice as `"+" + strconv.Itoa(stats.added) + "/-" + strconv.Itoa(stats.removed)`. Compute the aggregate stats once per exchange (a small pure helper `activityDiffStats(*toolcall.Activity) (diffStats, bool)`) so the header path does not run the diff twice. When both an error hint and a badge would be present, keep only the error hint. Add `TestMonitorDiffBadgeExpanded`, `TestMonitorDiffBadgeAbsentCollapsed`, and `TestMonitorDiffBadgeAbsentErrored` to `monitor_transcript_items_view_test.go`.
- [ ] 3.5 Update the per-exchange render cache. Introduce `transcriptRenderKey{ surface: transcriptRenderDiff, ... }` entries alongside the existing card entries. Ensure the existing wholesale eviction on width change (`rebuildRenderer` at `monitor_layout.go:260`) also covers the diff surface (it already does — the whole map is cleared). Add `TestMonitorDiffCacheStableOnRepeatedRender` and `TestMonitorDiffCacheEvictedOnWidthChange`.
- [ ] 3.6 Add `TestMonitorDiffAnchorTailRunning` asserting a running edit exchange with a long diff renders the tail rows with the slice-06 prepended `EarlierItems` marker (i.e., that `anchorForState(toolDisplayRunning) == detailAnchorTail` flows correctly through the diff section).
- [ ] 3.7 Add persistence-off and line-range regressions: `TestMonitorDiffPersistenceOff` builds a Monitor with `RunDir == ""` and asserts no diff, no card, no hint, and no crash; `TestMonitorDiffLineRangesCoverBody` renders a synthetic expanded edit exchange and asserts `chatItemLineRanges[key].end - .start + 1` equals the observed row count for the item.
- [ ] 3.8 Add the terminal-safe sanitizer for raw content flowing into diff rows. Before `renderDiffRows` receives the `Diff` payload, apply a display-only sanitizer that strips CSI, OSC, and simple ESC- sequences from `OldText`, `NewText`, and `path`. Preserve printable Unicode, tabs, and line breaks. Do not modify the durable `transcript.jsonl`. Add `TestMonitorDiffStripsHostileControls` covering ANSI SGR, CSI cursor moves, OSC hyperlinks/titles, and simple escapes.
- [ ] 3.9 Run `gofmt -w` on every file this task modifies. Run `go test ./internal/tui/monitor -run 'TestMonitorDiff' -count=1 -v` and `go test ./internal/tui/shared -run 'TestDiffClampedHint|TestDiffUnavailableHint|TestTruncationSourceShape' -count=1 -v`. Generate `25-proofs/25-task-3-monitor-diff.txt` and (when headless Chrome is available) its `.png`; otherwise record the missing screenshot in a `.limitations.md`. Run `go test -race ./internal/tui/... ./internal/helpchat -count=1` and capture the output.
- [ ] 3.10 Write `25-proofs/25-task-03-proofs.md` with the exact commands, outcomes, artifact paths, FR-07.15..FR-07.21 coverage, and any limitations.

### [ ] 4.0 Record acceptance evidence and confirm scope integrity

Run the applicable repository acceptance commands, capture their output
alongside a short limitations note for anything that could not run, and
close the loop with a final-diff review that proves the change did not
leak into unrelated slices or into the transcript wire format.

#### 4.0 Proof Artifact(s)

- CLI: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-4-acceptance/{build,test,vet,race-tui,gofmt,git-diff-check}.txt` captures `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/...`, `gofmt -l <changed-go-files>`, and `git diff --check`. Any unavailable check is recorded as a limitation in `25-task-4-limitations.md`, not replaced by a substitute claim.
- Final-diff review recorded in the PR body confirming no changes under `internal/transcript/`, `internal/toolcall/`, `internal/runner/`, `internal/harness/`, or `internal/tui/shared/palette.go`, and confirming no new `lipgloss.NewStyle()` outside `internal/tui/shared/styles.go`.
- Proof summary: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-04-summary.md` records the full requirement coverage FR-07.1 through FR-07.22 and any limitation.

#### 4.0 Tasks

- [ ] 4.1 Run the acceptance commands in order: `go build ./cmd/jig`, `go vet ./...`, `go test -race ./internal/tui/... ./internal/helpchat -count=1`, `go test ./...`, `gofmt -l` on the changed Go files, and `git diff --check`. Capture each command's stdout+stderr and exit code to a file under `25-proofs/25-task-4-acceptance/`. The nested ACP module (`harness/acp`) is not exercised — this slice does not cross that boundary; the omission is expected, not a limitation.
- [ ] 4.2 If any command cannot run in the validation environment (for example, headless Chrome for the screenshot in Task 3.9 is unavailable), record the missing check and its exact reason in `25-proofs/25-task-4-limitations.md`. Do not substitute a component-only claim for a blocked check.
- [ ] 4.3 Final-diff review: read the cumulative diff and confirm the touched files are limited to:
  - `internal/tui/monitor/monitor_diff_compute.go` (new)
  - `internal/tui/monitor/monitor_diff_compute_test.go` (new)
  - `internal/tui/monitor/monitor_diff_render.go` (new)
  - `internal/tui/monitor/monitor_diff_render_test.go` (new)
  - `internal/tui/monitor/monitor_transcript_items_view.go`
  - `internal/tui/monitor/monitor_transcript_items_view_test.go`
  - `internal/tui/monitor/monitor_transcript_card_test.go`
  - `internal/tui/monitor/monitor_model.go`
  - `internal/tui/shared/styles.go`
  - `internal/tui/shared/styles_test.go`
  - `internal/tui/shared/truncation.go`
  - `internal/tui/shared/truncation_test.go`
  - `go.mod` (promotion of `hexops/gotextdiff` to direct)
  - `go.sum` (no new entry expected; the module is already present)
  - `docs/adr/00NN-diff-computation-library.md` (new; `NN` is the next unused ADR slot verified in Task 1.1)
  - `docs/specs/25-spec-diff-rendering/**` (spec, tasks, proofs)
  and no changes to `internal/transcript/`, `internal/toolcall/`, `internal/runner/`, `internal/harness/`, `internal/tui/shared/palette.go`, `internal/tui/shared/card.go` (except through Diff style tokens in `styles.go`), or the review workspace.
- [ ] 4.4 Write `25-proofs/25-task-04-summary.md` recording the exact commands, outcomes, artifact paths, requirement coverage (FR-07.1 through FR-07.22), and any limitation. This is the reviewer-first proof summary.
