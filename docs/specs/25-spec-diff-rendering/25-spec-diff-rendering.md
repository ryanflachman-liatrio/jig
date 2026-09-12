# 25-spec-diff-rendering.md

## Introduction/Overview

Give the Monitor's transcript an actual before/after diff for tool calls that
carry a `Diff` payload with a non-nil `OldText`. Today the renderer intentionally
shows the resulting file (`writeNewCodeCards`) even though the adapter already
carries the old text; that decision is out of step with the rest of the panel
because the operator cannot see *what changed*, only what the file now looks
like. This feature computes a unified patch from `Diff.OldText` and
`Diff.NewText`, projects it through the existing `internal/tui/diffview` parser,
and renders it with a fused line-number-plus-marker gutter, syntax-highlighted
context lines, word-level intra-line highlighting for one-to-one replacements,
indentation visualization, and the shared slice-06 truncation vocabulary. The
resulting-source path survives as the fallback for file creation
(`OldText == nil`) and for any case where diff computation fails, so the
existing intent of `writeNewCodeCards` remains available where it is genuinely
useful.

Source: [OMP transcript parity, slice 07](../../epics/omp-transcript-parity/slices/07-diff-rendering.md).
Dependencies: slice 01 provides `shared.Card` and `shared.RenderCard`; slice 06
provides `shared.MoreItems` / `shared.EarlierItems` / `shared.ExpandHint` /
`shared.HintLine` / `shared.CaptureTruncatedHint` and the `detailAnchor` mode on
`boundTranscriptDetail`. Slice 05 (tool-detail sections) is *not* a dependency:
this feature replaces the content that `writeNewCodeCards` emits below the
exchange card today, and continues to emit at the writer's four-cell indent so a
future slice-05 re-hosting into a `CardSection` remains a small, follow-up
integration. Cross-cutting decisions CC-1, CC-8, CC-10, and CC-12 govern this
work; NG5 (no wire-format changes) and CC-12 (no changes to `toolcall.Activity`)
are non-negotiable.

## Goals

- Compute a bounded unified diff from `Diff.OldText`/`Diff.NewText` and render
  it in the transcript panel with a fused line-number-plus-marker gutter,
  right-aligned as one token and floored at width 3.
- Show word-level intra-line highlighting via reverse video for one-to-one
  replacements, excluding leading whitespace from the highlighted span.
- Batch-highlight context lines through the existing Chroma/Glamour path so
  syntax remains legible across multi-line grammars; keep added and removed
  lines flat so the change reads before the syntax.
- Preserve the "resulting source" view for file creation (`OldText == nil`) and
  for any case where diff computation fails, so no exchange loses evidence.
- Consume slice-06's truncation vocabulary for the hunk-and-line collapse
  budgets so the whole Transcript panel keeps one hint grammar.

## User Stories

- **As an operator reviewing a completed edit**, I want to see the changed
  lines with a line-number gutter and word-level highlights so I can judge the
  change without opening the file.
- **As an operator inspecting a many-hunk edit**, I want long diffs to stay
  bounded with a clear "N more hunks, N more lines" hint so a large refactor
  does not push the rest of the transcript off screen.
- **As an operator inspecting a *new* file**, I want the resulting source card
  I already trust, because there is no prior version to compare against.
- **As an operator inspecting an edit whose payload was clamped at write
  time**, I want the renderer to say so before I trust the diff, because a
  diff over truncated text may claim changes that are artifacts of the clamp.
- **As an implementer of a future slice** (05 tool-detail sections, or a
  review-workspace consolidation), I want the diff generator to be a pure
  content producer that a card section or a review pane can host without
  touching its algorithm.

## Demoable Units of Work

### Unit 1: Diff computation and diffview projection

**Purpose:** Turn `OldText`/`NewText` into a bounded unified patch, feed it
through the existing `diffview.Parse`, and expose a small pure interface that
Unit 2 renders. This unit resolves the epic's blocking question Q-07.1 and
records the choice as an ADR.

**Functional Requirements:**

- **FR-07.1** The Monitor shall compute a unified diff from `Diff.OldText` and
  `Diff.NewText` for every `toolcall.Content.Diff` whose `OldText` is non-nil,
  and project it through `diffview.Parse` to obtain a
  `*diffview.Presentation`. The path field on `Diff` shall be used as both old
  and new filenames in the patch header so `diffview.Files` reports the correct
  name.
- **FR-07.2** Computation shall use option A from the epic slice: an external
  Go package that emits a unified patch string, chosen and pinned by an ADR
  under `docs/adr/`. The recommended module is
  `github.com/hexops/gotextdiff` (a dependency-free, canonical port of
  `golang.org/x/tools/internal/{myers,unified}`) already present in `go.sum`
  transitively. Any alternative shall satisfy: no non-stdlib runtime
  dependencies of its own, a permissive OSI license, an emit function that
  produces `--- a/<path>` / `+++ b/<path>` / `@@ -a,b +c,d @@` shaped output
  that `diffview.Parse` accepts unchanged, and a public review of behavior on
  the four canonical shapes (single-line replacement, multi-line replacement,
  pure insertion, pure deletion). The ADR shall be numbered by continuing the
  existing `docs/adr/` sequence (the next unused slot at the time of writing
  is `0013`; the implementer verifies the next unused number before creating
  the file).
- **FR-07.3** The computation shall be bounded before it runs. Total input
  size (`len(OldText) + len(NewText)`) shall be capped at a documented
  package-private constant (initial value 128 KiB); over-limit inputs shall
  bypass computation and fall back to the resulting-source path with a
  slice-06-vocabulary label recorded via a shared helper (see FR-07.15). Line
  count shall be capped at a package-private constant (initial value 4000
  lines per side); over-limit inputs shall behave identically to the byte cap.
  Both caps shall be documented as tunable via constants, not env variables.
- **FR-07.4** A parse or emit error shall not surface a stack trace to the
  transcript. The Monitor shall fall back to the resulting-source card with
  the same "diff unavailable, showing resulting source" label used for
  computation-bypassed inputs (FR-07.15) so an operator always sees the diff
  payload's evidence in some shape.
- **FR-07.5** The computation and projection shall be pure with respect to the
  `Diff` inputs and the caps: given the same bytes and the same caps, the same
  `*diffview.Presentation` and the same set of derived rows shall be produced.
  This guarantees the Monitor's per-item render cache can key the diff on the
  block's stable identity without recomputing per frame.

**Proof Artifacts:**

- Test: table-driven cases in `internal/tui/monitor/monitor_diff_compute_test.go`
  exercise (a) single-line replacement, (b) multi-line replacement, (c) pure
  insertion, (d) pure deletion, (e) unchanged file (empty diff), (f) file
  creation (`OldText == nil`, computation skipped), and (g) byte-cap and
  line-cap bypass. Each asserts the resulting `*diffview.Presentation`'s
  `Files`, `Hunks`, and the counts of `RowAdd`/`RowDelete`/`RowContext` rows.
- Test: a `diffview` round-trip case renders the emitted unified patch through
  `diffview.Parse` and confirms `ParseErr == nil` on every shape above.
- Test: a determinism case computes the same input twice and asserts the
  produced patch strings are byte-equal.
- ADR: `docs/adr/0013-diff-computation-library.md` (verified as the next
  unused slot in `docs/adr/`) records the evaluated options, the choice,
  the license, the supply-chain assessment (transitive vs direct, existing
  usage inside jig's module graph), the caps, and the rejection reasons for
  options B and C.

### Unit 2: Fused-gutter rendering, intra-line diff, indentation, and colors

**Purpose:** Turn a `*diffview.Presentation` into a slice of styled string rows
that read as omp's diff cards do — one fused gutter token per row, foreground-
only add/remove/context colors, word-level highlighting on 1↔1 changes, batch
syntax highlighting on context runs, and visible indentation. This unit is the
largest body of new code and produces content that a caller (Unit 3) drops into
a diff section without concerning itself with the layout math.

**Functional Requirements:**

- **FR-07.6** Each visible diff row shall carry a gutter of the shape
  `<marker><line-no>│<content>` where `<marker>` is one of `' '`, `'+'`, `'-'`,
  the marker and the line number are concatenated into one token and
  right-aligned together, then a single `│` glyph, then the content with no
  intervening space. Rows whose `HasOld` or `HasNew` is false shall show a
  blank line number and only the marker.
- **FR-07.7** Gutter width (marker + line-number token) shall be at least 3
  cells regardless of the file's largest line number. The floor eliminates a
  re-pad at the 10/100/1000-line crossings and matches omp's rationale
  (`modes/components/diff.ts:119-128`).
- **FR-07.8** When a row's rendered line-number text is identical to the
  previously emitted row's, the number shall be suppressed, leaving only the
  marker. This applies to the single-line replacement pattern (`-N` followed
  by `+N`) and to an insertion followed by a context row that continues the
  same new-line number, and shall not leak across hunk boundaries.
- **FR-07.9** A change block consisting of exactly one removed row and one
  added row shall receive word-level intra-line highlighting: the differing
  runs shall be styled with reverse video (`lipgloss.Style.Reverse(true)`),
  and leading whitespace of the first changed part shall be excluded from the
  reverse-video span so indentation is never inverted. Multi-line change
  blocks shall not receive intra-line highlighting.
- **FR-07.10** Context rows shall be syntax-highlighted through the existing
  Glamour/Chroma inset path (`m.insetRenderer` with `fencedCode(path, code)`)
  in *consecutive runs* — one call per contiguous run of context rows — so
  multi-line grammars (comments, docstrings, template literals) tokenize
  across the run rather than one row at a time. Added and removed rows shall
  stay flat (`shared.Theme.Diff.Add` / `shared.Theme.Diff.Remove` foreground
  only) so the change reads before the syntax. Language derivation shall use
  the existing `codeLanguage(path)` helper.
- **FR-07.11** Leading spaces shall render as a dim middle dot (`·`) and
  leading tabs as a dim right-arrow padded to the file's tab width (initial
  constant `diffTabWidth = 3`, matching omp's `DEFAULT_TAB_WIDTH`). Non-leading
  tabs shall render as plain spaces. Indentation visualization shall apply to
  added, removed, and context rows.
- **FR-07.12** Add, remove, and context colors shall be foreground only. No
  per-row background shall be introduced. The card's state tint (slice 01)
  owns the row background; overlaying a per-row background would fight it.
- **FR-07.13** Wrapped continuation rows (a content line that exceeds the diff
  section's content width) shall use a blank gutter followed by the same `│`
  column so the wrap aligns to the content column. Wrapped rows shall
  terminate with an SGR sequence that closes reverse video and any foreground
  color so subsequent frame padding is not painted as an inverse block.
- **FR-07.14** A collapsed diff shall bound both hunks and lines. Initial
  budgets: `diffCollapsedHunks = 8`, `diffCollapsedLines = 40`. When either
  budget clips the presentation, the section shall render a single footer row
  built through the slice-06 combinator, e.g.
  `shared.HintLine(shared.MoreItems(hh, "hunk", "hunks") + ", " + strings.TrimPrefix(shared.MoreItems(hl, "line", "lines"), "… "), hint)`,
  wrapped once in `shared.Theme.Chat.Hint.Render`, with `hint` from
  `shared.ExpandHint(expanded, hh+hl > 0, m.keys.Toggle.Help().Key)`. The
  ellipsis appears once at the beginning of the composed row. The exact
  composition helper shall be a small owner-local function whose test
  asserts the composed string for representative pairs (`hh=0/hl=12`,
  `hh=3/hl=0`, `hh=3/hl=12`, `hh=0/hl=0`).

**Proof Artifacts:**

- Test: `internal/tui/monitor/monitor_diff_render_test.go` table cases assert
  the fused-gutter shape (marker + number + `│` with no space), width floor at
  3 for a 5-line file, correct alignment at 1000 lines (width 4), and
  duplicate-number suppression at a single-line replacement, an insert-then-
  context pair, and *not* across a hunk boundary.
- Test: an intra-line case asserts the 1↔1 replacement has reverse-video runs
  and that a leading-whitespace-only difference is not inverted; a multi-line
  case asserts no reverse-video escape appears.
- Test: a syntax-highlighting case renders context that includes a multi-line
  string literal and asserts a chroma foreground escape appears on the
  context rows and does *not* appear on the added or removed rows.
- Test: an indentation case renders rows containing leading spaces and leading
  tabs and asserts the dim `·` and dim `→` render sequences appear.
- Test: a background-freedom case renders a full card and asserts no
  `\x1b[48` sequence originates from the diff renderer (grep after
  `stripANSI` inversion is not required — a source-shape assertion in the
  renderer test suffices; see FR-07.12).
- Test: a wrap case renders a line wider than the content column and asserts
  the continuation row has a blank gutter and a terminating SGR close for
  reverse video and foreground.
- Test: a collapse case renders a diff with 12 hunks and 200 changed lines
  and asserts the footer row contains both the hunk count and the line count
  through the slice-06 vocabulary with a single leading ellipsis, and that
  the expand key text comes from the live `Toggle` binding.
- Terminal capture:
  `docs/specs/25-spec-diff-rendering/25-proofs/25-task-2-diff-gallery.txt`
  showing single-line replacement (with intra-line span), multi-line
  replacement (without), pure insertion, pure deletion, and a collapsed
  many-hunk case at widths 60 and 90.

### Unit 3: Monitor integration, fallback, clamped-text label, and header badge

**Purpose:** Wire Unit 1 and Unit 2 into `writeNewCodeCards` so a real diff
replaces the resulting-source view when `OldText` is present; preserve the
resulting-source view for file creation and computation failures; label a diff
that was computed over write-time-clamped content so the operator does not
trust artifacts of truncation; and emit the `+N/-M` stats badge into the
header's Meta slot (slice-02 grammar).

**Functional Requirements:**

- **FR-07.15** When `Diff.OldText != nil` and computation is not bypassed
  (FR-07.3, FR-07.4), the Monitor shall render the diff rows produced by
  Unit 2 in place of the resulting-source card body. The section label shall
  read `Diff · <path>` (mirroring the existing `New code · <path>` label
  wording so operators recognize the surface). When computation is skipped or
  fails, the Monitor shall render the existing resulting-source card and
  prepend a shared helper hint
  (`shared.DiffClampedHint()` returning
  `"… content clamped at write; diff may be incomplete"` when the enclosing
  block's `Truncated` flag is set; otherwise
  `"… diff unavailable; showing resulting source"`), styled once through
  `shared.Theme.Chat.Hint`. The helper shall be added to
  `internal/tui/shared/truncation.go` alongside `CaptureTruncatedHint`.
- **FR-07.16** When `Diff.OldText == nil`, the Monitor shall render the
  existing resulting-source card with the current `New code · <path>` label
  and no fallback hint. This is the file-creation case the current
  `writeNewCodeCards` comment was genuinely serving.
- **FR-07.17** When the enclosing `transcript.Block.Truncated` is true and a
  diff was computed anyway (the block's diff fields were partially clamped
  but computation still succeeded), the diff section shall render the shared
  clamped-content hint as its first row so the label is visible before the
  diff itself. This is the correctness hazard highlighted by the epic slice
  and is not a cosmetic concern.
- **FR-07.18** The exchange header shall receive a `+N/-M` badge in the Meta
  slot (slice-02 grammar) when the computed diff is non-empty. The badge shall
  be composed by a small helper that returns
  `"+" + strconv.Itoa(added) + "/-" + strconv.Itoa(removed)` and shall be
  emitted only for expanded exchanges — the collapsed header remains a single
  status-line row per FR-01.5 / FR-02.14 and shall not gain the badge yet.
  Adding it to the collapsed row is a slice-02 follow-up already noted in the
  epic and is out of scope here.
- **FR-07.19** The diff rows shall be produced at the current writer's
  four-space indent (matching today's `writeNewCodeCards` and preserving the
  slice-04 per-item edge-trim discipline; SGR-styled content survives the
  trim). The rows shall not introduce a nested border. When slice 05 later
  re-hosts these bodies inside a `CardSection`, the same producer shall be
  invoked with the section's content width instead of `m.transcriptInnerW - 4`,
  and no diff-generator change shall be required.
- **FR-07.20** The diff renderer's output shall pass through
  `boundTranscriptDetail(..., anchorForState(item.displayState))` for row
  bounding, so a running edit exchange keeps its newest diff rows visible
  (slice-06 anchor). The clamp shall be applied *after* Unit 2's own
  collapse budget (FR-07.14) so the two bounds compose without double
  counting: Unit 2's collapse bounds the diff at authorship time; the
  detail bound clips at rendered rows.
- **FR-07.21** The diff render cache shall be keyed on the exchange's item
  key, the section width, the expanded state, and a stable identifier for
  the `Diff` payload (path + byte lengths of `OldText`/`NewText`), so the
  Monitor's per-frame repaint stays inside the existing 100 ms budget. The
  cache shall be invalidated wholesale on `WindowSizeMsg` through the
  existing `rebuildRenderer` path.
- **FR-07.22** No change shall be made to `internal/transcript`,
  `internal/toolcall`, `internal/runner`, or `internal/harness`. The
  transcript wire format, `toolcall.Activity`, secret redaction, and
  write-time clamping semantics remain the authority; this feature is a
  presentation-only projection.

**Proof Artifacts:**

- Test: table-driven Monitor cases in
  `internal/tui/monitor/monitor_transcript_items_view_test.go` render a
  synthetic exchange whose `Diff.OldText`/`NewText` produce each canonical
  diff shape (single-line replacement, multi-line, pure insert, pure delete,
  file creation, empty diff, byte-cap bypass, parse-error bypass, clamped
  block); assertions cover the section label, the badge presence in the
  header Meta slot for expanded exchanges, and the presence/absence of the
  shared clamped-content and diff-unavailable hints.
- Test: a cache-lifecycle case renders the same exchange twice at the same
  width, then re-renders at a new width, and asserts the diff content
  bytes change on width change and stay stable on identical inputs.
- Test: a persistence-off regression asserts the Monitor loads with
  `RunDir == ""` and produces no diff rows, no card, no hint, and no crash.
- Test: a line-range accounting case (`chatItemLineRanges`) proves that
  the added diff rows are reflected in the range accounting so `n`/`N`
  block navigation lands on the intended exchange when expanded diffs
  add many rows.
- Terminal capture:
  `docs/specs/25-spec-diff-rendering/25-proofs/25-task-3-monitor-diff.txt`
  and a companion `.png` when headless-Chrome is available, showing adjacent
  expanded exchanges: an edit with intra-line highlights, a new-file
  fallback, a clamped-content warning, and a collapsed many-hunk diff at
  80 columns with `transcriptInnerW`, `displayState`, and expansion state
  recorded in the notes.
- CLI: `docs/specs/25-spec-diff-rendering/25-proofs/25-task-3-quality-checks.txt`
  captures `gofmt -l <changed-go-files>`, `go build ./cmd/jig`,
  `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/...`, and
  `git diff --check`.

## Non-Goals (Out of Scope)

1. **Side-by-side diffs.** omp is unified-only; so is jig's `diffview`. A
   two-column layout is not part of this feature and is not on the epic's
   Deferred Work list either.
2. **Enclosing-block context injection via tree-sitter.** omp's
   `crates/pi-edit/src/diff_string.rs:180-237` injects the nearest function or
   type header above a hunk. That requires a language parser per language and
   is explicitly the epic's Deferred Work.
3. **Changing the review workspace's diff rendering.**
   `internal/tui/review/view.go` uses `diffview` for its own presentation.
   Slice 07 does not touch it. Consistency across the two surfaces (Q-07.3)
   is a follow-up.
4. **Streaming / partial diff previews.** jig renders from settled
   transcript entries. A running edit exchange keeps its resulting-source
   view until the block is settled.
5. **Re-hosting the diff inside a `shared.CardSection`.** Slice 05 owns the
   move from the trailing four-space writer into a card section. Slice 07's
   producer is designed to slot in unchanged when that lands (FR-07.19).
6. **Retuning slice-06's `transcriptDetailBytes` / `transcriptDetailRows`
   budgets.** The diff's own budgets (FR-07.3, FR-07.14) are new;
   slice-06's bounds are consumed unchanged (FR-07.20).
7. **Emitting the `+N/-M` badge on collapsed exchanges.** Adding it to the
   collapsed header is a slice-02 grammar refinement; slice-02 currently
   reserves Meta for the error hint. Extending the collapsed row is a
   follow-up whose ownership is slice 02, not slice 07.
8. **Modifying transcript persistence, `toolcall.Activity`, secret
   redaction, harness normalization, or the runner.** Every field this
   feature reads is already produced end-to-end.

## Design Considerations

### The unblocking finding is durable

The epic's "does the adapter carry old text?" open question is settled by
`internal/toolcall/toolcall.go:38-44`: `OldText *string`, populated by the
ACP harness (`internal/harness/acp.go:684-688`), redacted in the runner
(`internal/runner/agent.go:566-568`), clamped in the transcript writer
(`internal/transcript/writer.go:161-162`), and indexed by search
(`internal/tui/monitor/monitor_search.go:248-249`). The renderer is the only
consumer that currently discards it. Slice 07 turns that around.

### Which diff library

`github.com/bluekeyes/go-gitdiff v0.8.1` (already a direct dependency) parses
unified diffs but does not compute them. Three options exist:

| Option | Behavior | Cost | Verdict |
|---|---|---|---|
| A. Add a Go unified-diff producer (e.g. `github.com/hexops/gotextdiff`) and feed `diffview.Parse`. | Compute → emit `@@` patch → parse → project. | One direct-require entry; already transitively downloaded via `alecthomas/chroma/v2` test tree. | **Recommended.** Reuses `diffview.DisplayRows`, `RenderHunkHeader`, `RenderRawLine`, and the review workspace's presentation unchanged. |
| B. Hand-roll a line-level Myers/LCS producing an op list; render directly. | No new dependency; ~150–200 lines of custom code plus a token-level second pass for word diff. | New algorithm to own and test end-to-end. | Reasonable but strictly more work with worse leverage — `diffview`'s projection is not reused. |
| C. Ask the harness to supply a patch. | Contract change on `toolcall.Diff`. | **Rejected.** NG5 and CC-12 forbid wire-format changes, and not every backend can produce a canonical unified patch. |

Option A shall be locked by
`docs/adr/0013-diff-computation-library.md`. `hexops/gotextdiff` is a
dependency-free port of `golang.org/x/tools/internal/{myers,unified}` and
is Apache-2.0-licensed. `myers.ComputeEdits` + `gotextdiff.ToUnified`
produces `--- a/<path>` / `+++ b/<path>` / `@@ …@@` output that
`diffview.Parse` accepts without adjustment. Behavioral alternatives are
evaluated in the ADR.

### The clamped-text hazard

Content is clamped at write time
(`internal/transcript/writer.go:161-162` clamps `OldText`;
`writer.go:200` clamps `NewText`). A diff over clamped text will read as
if the tail of the file was rewritten when in fact the tail was merely
truncated. This is a correctness risk, not a cosmetic one. Two mitigations
work together:

1. `transcript.Block.Truncated` is already available at the render site
   (`writeTranscriptItem` reads it at line 126 to emit the existing
   `shared.CaptureTruncatedHint()` row).
2. Slice 07 adds a diff-specific helper
   `shared.DiffClampedHint()` (returning the fixed literal
   `"… content clamped at write; diff may be incomplete"`) and emits it as
   the diff section's *first* body row when the block is truncated. The
   generic capture-truncated hint at the bottom of the exchange continues to
   render unchanged. Both rows are dim so the diff itself remains the
   dominant reading; the label appears above so the reader sees it before
   trusting the change spans.

### The header badge slot

Slice 02 established the four-slot header grammar `Icon · Title · Description
· Meta`, with Meta reserved today for the `toolErrorHint` when an exchange
failed. The `+N/-M` badge belongs in Meta on the *expanded* variant of a
non-error exchange. Two invariants hold:

- Meta contents are order-sensitive — when both an error hint and a
  `+N/-M` badge are present (an edit that failed with a diff payload), the
  error hint wins; the badge is dropped. This mirrors slice-02's
  precedence and avoids competing prose in a single slot.
- Slice 14's glyph preset (unicode vs ascii) has not landed. The badge
  uses plain ASCII: `+3/-2`, not `⟦+3/-2⟧`. When slice 14 lands, an
  optional glyph preset may wrap the badge in `⟦⟧` under Unicode; that
  wrapping is a slice-14 concern and is exposed to slice 07 only as a
  formatter accepting a preset.

### The four-space writer and slice 05

`writeNewCodeCards` currently emits at a four-space indent below the
exchange header card. Slice 05 (not yet implemented) plans to re-host that
content inside a `CardSection` with zero left padding so the diff's gutter
meets the card's border. Slice 07 keeps the four-space writer shape now
and confines the diff-specific rendering behind a small producer
(`buildDiffSection(diff *toolcall.Diff, width int, ...) []string` or the
equivalent) so slice 05's re-host swaps the caller, not the producer. The
producer's output is a `[]string` of already-styled rows; nothing in it
assumes a card frame. This is the concrete meaning of FR-07.19.

### Cache identity

`transcriptRenderKey` (`internal/tui/monitor/monitor_model.go:472-484`)
discriminates by item key, surface, width, expanded, selected, state, and
header text. The diff cache shall introduce a **new** surface
(`transcriptRenderDiff` alongside `transcriptRenderMarkdown`,
`transcriptRenderDetail`, `transcriptRenderCard`) so cache entries for
diff rendering do not collide with the existing new-code card cache.
Wholesale invalidation on `WindowSizeMsg` (via `rebuildRenderer`) already
covers width changes; state changes evict through the existing state key.

### omp reference — the pieces this slice ports

- Gutter shape and marker fusion: `tools/render-utils.ts:395-405`
  (`formatCodeFrameLine`). Width floor at 3 and the streaming
  re-render rationale: `modes/components/diff.ts:119-128`.
- Duplicate-number suppression: `diff.ts:138-149`.
- 1↔1 word-level highlighting via reverse video with leading-whitespace
  exclusion: `diff.ts:55-99, 185-198` (`renderIntraLineDiff`).
- Batch-highlighted context runs: `diff.ts:231-267`.
- Indentation visualization (dim `·`, dim `→`): `diff.ts:16-34`.
- Foreground-only colors and no per-line background: `diff.ts:156-174`
  (context row) and the epic slice's FR-07.10 discussion.
- `⟦+N/-M⟧` stats badge: `edit/renderer.ts:776-784`. This slice uses
  ASCII `+N/-M` until slice 14 lands its glyph preset.
- Wrapped continuation row terminator and blank gutter:
  `edit/renderer.ts:823-860`.
- Collapse budgets `DIFF_COLLAPSED_HUNKS = 8`, `DIFF_COLLAPSED_LINES = 40`
  and combined footer wording: `render-utils.ts:102-104`,
  `edit/renderer.ts:813-818`.

### A caveat, recorded honestly

omp is internally inconsistent about diff line labels — at least three
phrasings coexist and its `default-renderer.ts:134` hardcodes an unpluralized
`more lines`. Slice 06 already picked the single vocabulary jig uses; this
slice consumes it via `shared.MoreItems` / `shared.EarlierItems` /
`shared.HintLine` and does not add a fourth phrasing.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md), [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), [Testing](../../TESTING.md), and the domain
  terms in [CONTEXT.md](../../../CONTEXT.md).
- Pure diff computation and rendering primitives live in Monitor-local files
  (`internal/tui/monitor/monitor_diff_compute.go`,
  `monitor_diff_render.go`) so `internal/tui/shared` remains free of
  Monitor coupling. `internal/tui/shared/truncation.go` gains one new
  helper (`DiffClampedHint`) alongside `CaptureTruncatedHint`.
- Add styles only through `shared.Styles`/`DefaultTheme()` and existing
  Charmtone palette tokens. Do not add inline hex colors or package-level
  `var xStyle = lipgloss.NewStyle()`. New tokens needed:
  `Theme.Diff.Context`, `Theme.Diff.Intraline`, `Theme.Diff.Gutter`,
  `Theme.Diff.Indent` (dim). All derive from `fgDim` / `fgMuted` /
  `success` / `danger` already in the palette.
- Measure terminal cells with `lipgloss.Width` and ANSI-aware helpers.
  Never count runes or bytes to reason about visible width. Rely on
  `github.com/charmbracelet/x/ansi` for wrapping and truncation, as
  `shared.Card` already does.
- Table-driven tests with fabricated `Diff` fixtures (a small
  `newDiffActivity(path, oldText, newText string) *toolcall.Activity`
  helper) drive Monitor tests. Never read real `.jig/` run data,
  credentials, or prompts. The four canonical shapes (single-line,
  multi-line, insert-only, delete-only) plus the byte/line/parse-error
  bypass cases are covered explicitly.
- Pre-v1 discipline: `writeNewCodeCards` is replaced in-place. The
  resulting-source path stays only for `OldText == nil` and for
  computation failures; no environment variable or feature flag toggles
  between "old behavior" and "new behavior."
- Format only changed Go files with `gofmt -w`. Run change-specific TUI
  tests and the targeted race suite (`go test -race
  ./internal/tui/...`), then the root build/test/vet checks and
  `git diff --check`. The nested ACP module is unaffected (no imports
  cross that boundary).

## Technical Considerations

### Package layout

- `internal/tui/monitor/monitor_diff_compute.go` — new. Owns the
  `computeDiff(activity *toolcall.Activity, content toolcall.Content)
  (*diffview.Presentation, diffStats, computeStatus)` function, the
  byte and line caps, the deterministic patch emission, and the small
  fallback types (`diffStats{added, removed int}` and
  `computeStatus{skipped bool; reason string}`).
- `internal/tui/monitor/monitor_diff_compute_test.go` — new. Table-driven
  cases from FR-07.1–FR-07.5 plus the determinism case.
- `internal/tui/monitor/monitor_diff_render.go` — new. Owns
  `renderDiffRows(pres *diffview.Presentation, path string, width int,
  expanded bool, insetRenderer *glamour.TermRenderer, expandKey string)
  []string`, the gutter formatter, duplicate-number suppression, the
  1↔1 intra-line pass, context-run batch highlighting, indentation
  visualization, the collapse budget, and the wrap/terminator logic.
- `internal/tui/monitor/monitor_diff_render_test.go` — new. Table-driven
  cases from FR-07.6–FR-07.14.
- `internal/tui/monitor/monitor_transcript_items_view.go` — modified.
  `writeNewCodeCards` calls `computeDiff` and either renders diff rows
  via `renderDiffRows` (success) or falls back to `renderNewCodeCard`
  (failure or `OldText == nil`). `composeToolHeader` gains the
  `+N/-M` badge in Meta for expanded non-error exchanges (FR-07.18).
- `internal/tui/monitor/monitor_model.go` — modified. Adds
  `transcriptRenderDiff` to `transcriptRenderSurface`.
- `internal/tui/monitor/monitor_layout.go` — read-only reference. The
  inset renderer already exists and is rebuilt on
  `WindowSizeMsg`; the diff cache clear piggybacks on the existing
  `chatItemRendered` clear at line 260.
- `internal/tui/shared/styles.go` — modified. Adds
  `Theme.Diff.Context`, `Theme.Diff.Intraline`, `Theme.Diff.Gutter`,
  `Theme.Diff.Indent` field entries and their `DefaultTheme()` bindings
  from existing palette tokens.
- `internal/tui/shared/truncation.go` — modified. Adds
  `DiffClampedHint()` and `DiffUnavailableHint()` (or one helper that
  returns the appropriate literal based on a `clamped bool` argument;
  the exact shape is picked in Task 3).
- `internal/tui/shared/truncation_test.go` — modified. Adds
  fixture cases for the new helpers.
- `go.mod` — modified. Promotes `github.com/hexops/gotextdiff v1.0.3`
  from a transitive to a direct require entry so the dependency is
  auditable in the require block.
- `docs/adr/0013-diff-computation-library.md` — new. Records the
  Option A choice, the evaluated alternatives, license, the
  supply-chain assessment, and the caps. The implementer verifies
  the next unused ADR number before creating the file (existing ADRs
  go up to `0012` at the time of this spec).

### Signatures and pure helpers

The Unit 1 output is a small value type:

```
type diffStats struct{ added, removed int }
type computeOutcome int

const (
    computeOK computeOutcome = iota
    computeFileCreation      // OldText == nil
    computeSkippedOversize
    computeFailed            // emit or parse error
)

func computeDiff(diff *toolcall.Diff) (*diffview.Presentation, diffStats, computeOutcome)
```

The Unit 2 renderer signature is a small function that takes the
projection plus the width and the live expand key:

```
func renderDiffRows(
    pres *diffview.Presentation,
    path string,
    width int,
    expanded bool,
    insetRenderer *glamour.TermRenderer,
    expandKey string,
) []string
```

Both signatures are Monitor-package-private. Slice 05's future re-host
supplies its own width and keeps the same producer.

### Cache identity

`transcriptRenderSurface` gains one constant. The Monitor's
`chatItemRendered` map already keys by surface, width, expanded, selected,
state, and header. The diff renderer's stable payload identifier (path +
`len(OldText)` + `len(NewText)`) rides on the header field: the
`+N/-M` badge is part of the composed header, so an edit whose diff
changes (a re-run producing different `OldText`) evicts naturally when
the header changes. A dedicated `payload` field in the key is *not*
introduced; the existing header discrimination is sufficient and the
map's memory footprint stays flat.

### Charm dependencies and Go version

No new *runtime* module is added beyond promoting `hexops/gotextdiff`
from transitive to direct. Charm v2 (`charm.land/lipgloss/v2 v2.0.5`,
`charm.land/glamour/v2 v2.0.1`) is unchanged. Go 1.25 series
(`mise.toml`) is unchanged.

### Deliberate deviation from external guidance

omp's diff renderer uses `theme.inverse(...)` (SGR 7) directly for
intra-line spans; jig uses `lipgloss.NewStyle().Reverse(true)` on a
package-level `Theme.Diff.Intraline` token so styles remain owned by
`shared.Styles` per the repository standard. omp uses `⟦⟧` for the
badge brackets; jig uses ASCII `+N/-M` until slice 14 lands its glyph
preset table. Neither deviation changes the visual grammar; both keep
jig's style ownership and glyph-preset conventions intact.

## Security Considerations

- `OldText` and `NewText` are already secret-redacted upstream
  (`internal/runner/agent.go:566-568`) and size-clamped at write
  (`internal/transcript/writer.go:161-162`). Slice 07 does not
  re-derive secrets from them; it renders them.
- Reverse video on attacker-influenced content cannot corrupt the card
  frame the way a raw terminal escape could, but the *underlying text*
  is still passed through Chroma/Glamour or written directly. Before
  a raw content row enters the display path, the display projection
  shall strip terminal control sequences that could escape the frame
  (matching slice 05's FR-05.11 rule) while preserving printable
  Unicode, tabs, and line breaks. Syntax-highlighting escapes added by
  jig after sanitization remain intact.
- The diff computation library shall be one whose license and
  provenance are recorded in the ADR. `hexops/gotextdiff` is
  Apache-2.0 and derived from `golang.org/x/tools`. No transitive
  network access or file I/O is introduced by the library.
- Proofs use fabricated `Diff` payloads and synthetic paths. Never
  copy real `.jig/` run content, credentials, or `.env` values into
  fixtures, gallery captures, or ADRs.

## Success Metrics

1. **Real diff for real edits.** An expanded tool exchange whose
   `Diff.OldText != nil` renders added/removed/context rows with the
   fused gutter, not the resulting file, on the four canonical
   shapes.
2. **Fallback preserved.** An expanded exchange whose `Diff.OldText == nil`
   renders the current resulting-source card unchanged (FR-07.16),
   and the same fallback fires (with the diff-unavailable hint) for
   computation failures (FR-07.4) and byte/line bypass (FR-07.3).
3. **Clamped-content correctness label.** When the enclosing block's
   `Truncated` flag is set, the diff section's first body row is the
   shared `DiffClampedHint` (FR-07.17), styled as a dim hint.
4. **One vocabulary.** Every truncation indicator emitted by the diff
   renderer is built through `shared.MoreItems` / `shared.EarlierItems` /
   `shared.HintLine` and the live `m.keys.Toggle.Help().Key` (FR-07.14),
   with no locally-invented `"… "`-prefix literal introduced. The
   slice-06 grep-lock regression continues to pass.
5. **Header badge.** An expanded non-error edit exchange with a
   non-empty diff shows `+N/-M` in the header's Meta slot; an error
   exchange keeps its error hint and does not gain the badge
   (FR-07.18).
6. **No wire changes.** `git diff --stat` on the merged change shows
   no touched files under `internal/transcript/`, `internal/toolcall/`,
   `internal/runner/`, or `internal/harness/`.
7. **Bounded render cost.** A synthetic 300-entry transcript
   containing 20 expanded edit exchanges scrolls without exceeding
   the existing frame budget on the reference environment; the
   per-item render cache keyed on `transcriptRenderDiff` yields on
   repeated frames rather than recomputing. Measurement is a
   benchmark or a `go test -bench` run recorded in the Task 3 proofs;
   any regression outside the existing 100 ms throttle is flagged.
8. **Quality gates.** `go build ./cmd/jig`, `go test ./...`,
   `go vet ./...`, `go test -race ./internal/tui/...`,
   `gofmt -l <changed-go-files>` (empty), and `git diff --check`
   pass on the recorded validation machine. These are implementation
   acceptance targets, not results claimed by this Phase 1 document.

## Open Questions

The following are non-blocking; each has a recorded recommendation so an
implementer or reviewer can act deterministically.

1. **Q-07.1 (was blocking; resolved here)** Which computation option?
   *Resolved: Option A, `hexops/gotextdiff`. The ADR is a required
   Task-1 artifact so a reviewer sees the license, dependency shape,
   and rejected alternatives in one place.*
2. **Q-07.2** How should a diff over clamped text be labeled?
   *A dedicated shared helper `DiffClampedHint()` returning
   `"… content clamped at write; diff may be incomplete"`, styled through
   `shared.Theme.Chat.Hint`. Wording is stable and dim; the label
   appears above the diff so the reader sees it first. See FR-07.15,
   FR-07.17.*
3. **Q-07.3** Should the review workspace adopt the same fused gutter
   for consistency? *Out of scope for slice 07. Flagged as a follow-up
   so the two surfaces do not diverge permanently. The producer's
   `[]string` output is deliberately Monitor-package-private but has no
   Monitor-specific dependency and can be lifted to `shared` when the
   review workspace opts in.*
4. **Q-07.4** Does jig want `@@` hunk headers rendered, or omp's
   bare-gap-row elision? *Keep hunk headers. `diffview.RenderHunkHeader`
   already renders them and the review workspace uses them; spec-driven
   workflow operators benefit from the line anchors. omp's bare-gap-row
   elision is a stylistic choice, not a correctness one; it can be
   revisited if operators report the headers as noise.*
