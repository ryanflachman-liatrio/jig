# Implementation Plan: Parsed Review Diffs, Source Line Gutters, and Hunk Navigation

**Status:** Proposed; ready for implementation review  
**Primary surface:** `internal/tui/review`  
**Supporting surfaces:** `internal/tui/shared`, `go.mod`, `.agents/jig`  
**Schema, engine, runner, and persistence changes:** none  
**New dependency:** `github.com/bluekeyes/go-gitdiff/gitdiff` (pin `v0.8.1`)  
**Risk:** medium  
**Estimated focused implementation time:** 10–14 hours

---

## Summary

Replace the review workspace's prefix-only unified-diff renderer with a parsed,
lossless diff presentation. A reviewer will see old/new source line numbers,
navigate between hunks, and fold hunk bodies while comments, drafts, and review
submissions remain anchored to the immutable patch snapshot's logical lines.

The main risk is confusing displayed source-file lines with the review
document's patch lines. The former are navigation and comprehension aids; the
latter remain the only stable coordinates for selections and `review.Anchor`s.

## Problem

`internal/tui/review/source.go` currently detects a diff and assigns a style
from each raw line's first character. `internal/tui/review/view.go` then renders
the review document's physical line number as the one gutter. This is visually
valid unified diff text, but it does not answer the questions a reviewer needs
to answer:

- Which old and new file lines does this change affect?
- Where is the next independent change section?
- Can I collapse a hunk after I have checked it?

The required behavior is modeled after LazyGit's interactive patch explorer:
it parses a patch into hunks and typed lines, maps a selected patch row to a
source-file line, and exposes hunk navigation. We should copy that architecture,
not embed LazyGit or delegate rendering to an external pager. A pager such as
`delta` returns terminal-formatted text, which would discard the structured
coordinates needed for cursoring, folding, and stable comments.

## Goals

1. In a diff review document, show an **old** and **new** source-file line
   gutter alongside each hunk body row:
   - context rows show both values;
   - removed rows show only the old value;
   - added rows show only the new value;
   - metadata, hunk headers, and `\\ No newline` markers show neither.
2. Treat every parsed `@@ … @@` section as a hunk that can be reached with
   `[` / `]` and identified in the header as `Hunk N/M`.
3. Let `z` fold or unfold the active hunk body without changing its cursor
   anchor. Folded sections remain visibly represented and leave the active hunk
   readable.
4. Preserve all existing review operations: source-line/range comments,
   comment navigation and editing, review acknowledgement, summary, draft
   persistence, and atomic submission.
5. Render Git file headers, file mode changes, renames/copies, binary patches,
   malformed literal patches, and the existing no-newline marker safely.

## Explicit non-goals

1. No staging, unstaging, discarding, patch application, or modification of the
   reviewed worktree. This is a read-only review workspace.
2. No live `git diff` execution from the TUI. The snapshot supplied by the
   engine remains the reviewed evidence.
3. No side-by-side source-content view, intraline/word diff, editor launching,
   terminal hyperlinks, mouse selection, or new workflow fields.
4. No source-file comments. Comments remain attached to the immutable review
   document, even though its visible gutters use source-file coordinates.
5. No file/hunk sidebar in the first delivery. The existing document list and a
   compact in-panel `Hunk N/M` indicator are enough to make hunk navigation
   discoverable without restructuring the review layout.

## Existing contracts that must remain true

1. `review.Document.Content` or its `SnapshotPath` remains the immutable source
   of truth. `document.anchor` continues to use its one-based physical lines.
2. `Model.cursor` and `Model.rangeEnd` remain one-based patch-document lines;
   no new cursor is persisted in `review.ViewState`.
3. A saved `review.Anchor` retains the current document ID, SHA-256, quote,
   prefix, suffix, and inclusive logical line range. The old/new line gutters
   must never leak into an anchor or a draft.
4. `sourceXOffsets` remains per document and `h` / `l` / `0` retain horizontal
   pan behavior in source mode. A two-gutter layout changes only its fixed
   width calculation.
5. Plain text and Markdown source rendering remain unchanged. Markdown preview
   continues to use source-addressable Goldmark blocks.
6. Existing snapshots may have been written by an earlier build. A parse failure
   must visibly fall back to the current raw, styled source display rather than
   failing the review gate or losing review data.
7. The feature creates no new writers. Persistence-off is therefore unchanged;
   draft writes remain owned by the Monitor path already in place.
8. All visual styles are added to `shared.Theme.Review`; no source/view file
   creates a local Lip Gloss style or hard-coded color.

## Design

### 1. Keep raw patch text, add an ephemeral projection

Do not add diff presentation data to `internal/review`, the engine event, a
draft, or a run snapshot. Build it in `internal/tui/review` when
`Model.NewWithDraft` constructs its existing `sourcePresentation` values.

Introduce `internal/tui/review/diff.go` with unexported presentation types:

```go
type diffRowKind uint8

const (
    diffRowMetadata diffRowKind = iota
    diffRowHunkHeader
    diffRowContext
    diffRowDelete
    diffRowAdd
    diffRowNoNewline
)

type diffRow struct {
    patchLine            int // one-based logical line in document.lines
    fileIndex, hunkIndex int // -1 outside a parsed file/hunk
    kind                 diffRowKind
    oldLine, newLine     int
    hasOld, hasNew       bool
}

type diffHunk struct {
    fileIndex                 int
    ordinal                   int // one-based across this review document
    startPatchLine, endPatchLine int
    header                    string
}

type diffPresentation struct {
    rows       []diffRow // exactly one entry per document logical line
    hunks      []diffHunk
    folded     map[int]bool // transient, keyed by hunk ordinal
    parseErr   error
}
```

`patchLine` is the bridge to the existing model: comment markers,
`Model.cursor`, range selection, and `document.anchor` all continue to consume
it. `oldLine` and `newLine` are display-only and use `hasOld` / `hasNew` so the
valid source line number `0` is never confused with an omitted gutter.

The `rows` slice must always match `len(document.lines)`, including an empty
last row created by a trailing newline. That lets source rendering and comment
anchoring use identical row indices even when the semantic parser rejects a
patch.

### 2. Use a mature parser for semantic validation; preserve raw row identity

Add `github.com/bluekeyes/go-gitdiff/gitdiff` at `v0.8.1`. Its stable parsing
API accepts Git and standard unified patches, handles multiple files plus
rename/copy/mode/binary metadata, and exposes each text fragment's old/new
starts and typed context/add/delete lines. It supports Go 1.21, so it is
compatible with this repository's Go 1.25 toolchain.

The dependency does not expose the original physical patch row for every parsed
line. Therefore, construct the UI projection in two deliberate steps:

1. Parse the complete immutable content with `gitdiff.Parse(strings.NewReader(content))`.
   A parser error is recorded, not propagated out of `NewWithDraft`.
2. If parsing succeeds, make a lossless lexical pass over `document.lines`.
   Recognize file headers and hunk headers, then walk every hunk body while
   incrementing the old counter for context/deletion rows and the new counter
   for context/addition rows. Use the parsed file and text-fragment sequence to
   validate boundaries and hunk starts/counts; do not infer a hunk from a bare
   `+` or `-` outside a validated hunk.

The raw pass is not a second competing parser. It supplies only the source-row
mapping the dependency cannot supply. If its file/hunk/body sequence diverges
from the dependency's parsed model, treat the projection as unavailable and
render raw source with a concise `diff navigation unavailable: …` diagnostic.

This avoids the fragile alternative of re-serializing `gitdiff.File`: that
serialization may normalize the patch and would invalidate stable comments on
the original snapshot.

### 3. Source numbers and special rows

Within a validated hunk, initialize counters from the parsed fragment's
`OldPosition` and `NewPosition`.

| Raw patch row | Old gutter | New gutter | Counter update |
|---|---:|---:|---|
| ` context` | old | new | increment both |
| `-removed` | old | blank | increment old |
| `+added` | blank | new | increment new |
| `\\ No newline at end of file` | blank | blank | neither |
| `@@ -a,b +c,d @@ section` | blank | blank | initialize next body counters |
| file/rename/mode/binary metadata | blank | blank | neither |

Rows outside a hunk retain their existing presentation and a blank source-number
gutter. A binary or mode-only file is still visible as metadata, but has no
hunk and is skipped by hunk navigation. New-file and deleted-file hunks work
naturally because the parser supplies a zero-length old or new range.

### 4. Rendering and layout

Keep `sourcePresentation` responsible for ANSI-styled content. Diff source
presentation must additionally retain its `*diffPresentation`; normal source
and Markdown paths remain untouched.

For parsed diff rows, replace the single logical-line display with this fixed
gutter shape:

```text
rail marker  old  new │ content
 ▌             41   41 │  context
 ▌             42      │ -old call
 ▌                  42 │ +new call
```

- Right-align each old/new column to the maximum width needed by that diff's
  largest visible source line, with a one-cell separator between columns.
- Keep the cursor/range rail and comment marker exactly where they are today.
  A comment on raw patch line 17 stays visibly marked even if the row's source
  gutter says `42` or is blank.
- Apply current `Theme.Diff.Add`, `.Remove`, and `.Hunk` styles to content.
  New gutter styles distinguish absent, context, add, delete, active-cursor,
  and selected-range coordinates without coloring ANSI content itself.
- Feed only the content segment to `ansi.Cut`; neither rail, markers, nor old/
  new gutters may pan. Recompute `sourceGutterWidth`, `sourceContentWidth`,
  `sourceMaxWidth`, and offset clamping from the active presentation.
- Render hunk headers as full-width, non-wrapping rows. Their `@@` range stays
  visible as a textual fallback even when source gutters are blank.

For malformed diffs, `writeSourceRow` retains the current logical-line gutter
and prefix styles. The visible diagnostic is intentionally additive: reviewers
can still comment and submit rather than being blocked by a non-standard
literal patch.

### 5. Navigation and folding interaction

Add three bindings to `internal/tui/review/keys.go`, all active only when the
active document is a successfully parsed diff in source mode:

| Key | Action | Details |
|---|---|---|
| `[` | Previous hunk | Wrap to the final hunk; set `cursor` and `rangeEnd` to the target hunk header's `startPatchLine`. |
| `]` | Next hunk | Wrap to the first hunk; same cursor behavior. |
| `z` | Fold current hunk | Toggle only the current hunk's body. The header remains visible and the cursor remains on its header. |

`j` / `k` continue to move a single raw logical line. When a user attempts to
move into a folded body, move to the next/previous visible row rather than
silently entering hidden content. Starting a range selection on a folded header
must first unfold that hunk; it must not create a comment whose hidden body is
ambiguous. Comment navigation into a folded hunk unfolds it before positioning
the cursor on the comment's raw patch line.

`h`, `l`, `0`, `{`, `}`, `n`, `N`, `r`, `c`, `S`, and editor capture semantics
remain unchanged. Do not reuse LazyGit's `h` / `l` hunk bindings because jig
already uses them for horizontal source panning.

Display a compact diff-only status segment after the source language in the
document header, for example:

```text
Cumulative implementation diff    [ SOURCE ]     Diff · Hunk 2/5
```

When no hunk has focus, report `Hunks 5`; when parsing failed, report `Raw
diff`. Help must advertise the three new bindings only for a parsed diff.

### 6. Folding rules

- Folding is transient, per document, and intentionally absent from
  `review.ViewState`. Reopening a persisted review starts fully unfolded.
- A folded hunk displays its header plus a dim one-line placeholder such as
  `… 14 patch rows folded; press z to expand`.
- Folding a hunk containing a selected range clears `ModeSelectRange` and
  returns to `ModeBrowse`; never leave an invisible range active.
- Folding a hunk containing the active comment preserves `activeComment` but
  moves the cursor to the hunk header. Expanding restores no cursor position;
  normal `n` / `N` provides deterministic comment navigation.
- Do not hide file headers or the first row of any hunk. The user must always
  be able to understand what a collapsed section represents.

## File-by-file implementation tasks

### Phase 1 — Dependency and pure parsed projection

| # | Task | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `github.com/bluekeyes/go-gitdiff v0.8.1`; run `go mod tidy`; confirm the module has no incompatible Go/toolchain directive and retain only direct requirements actually imported by jig. | `go.mod`, `go.sum` | 15 min |
| 2 | Add the unexported diff presentation types, parser integration, raw-line projection, hunk boundary validation, and raw fallback diagnostic. Keep all state local to review presentation. | `internal/tui/review/diff.go` | 90 min |
| 3 | Add table-driven parser/projection tests for context/add/delete rows, omitted hunk counts, hunk section text, multi-file patches, new/deleted files, rename/copy/mode headers, binary indicators, no-newline markers, CRLF input, a trailing empty source row, and malformed input fallback. Assert every projected row retains its original `patchLine`. | `internal/tui/review/diff_test.go` | 90 min |
| 4 | Extend source presentation construction so only diff documents acquire the parsed projection, while source highlighting still preserves one ANSI-styled content line per immutable document row. | `internal/tui/review/source.go` | 40 min |
| 5 | Add tests proving the existing renderer selection and ANSI-stripped source content remain unchanged for plain, Markdown, and valid/invalid diff documents; assert parser failure preserves raw text. | `internal/tui/review/source_test.go` | 45 min |

### Phase 2 — Two-coordinate source rendering

| # | Task | Area | Estimate |
|---:|---|---|---:|
| 6 | Add semantic Review styles for old/new gutters, hunk status, and folded placeholders using existing palette tokens; preserve all pre-existing Review style meanings. | `internal/tui/shared/styles.go` | 25 min |
| 7 | Add style tests for ANSI stripping and width of the new gutters/placeholder. | `internal/tui/shared/styles_test.go` | 20 min |
| 8 | Generalize source gutter measurement and row rendering to select raw logical-line gutters for ordinary source and dual old/new gutters for a parsed diff. Keep clipping limited to styled content and update horizontal-offset clamping. | `internal/tui/review/view.go` | 90 min |
| 9 | Add view tests for every row type's old/new values, stable cursor/range/comment rails, Unicode content, horizontal panning with fixed two-number gutters, narrow widths, and raw fallback diagnostic visibility. | `internal/tui/review/model_test.go` | 75 min |

### Phase 3 — Hunk navigation and folding

| # | Task | Area | Estimate |
|---:|---|---|---:|
| 10 | Define previous-hunk, next-hunk, and fold-hunk bindings; expose them in contextual help only when the active diff projection is available. | `internal/tui/review/keys.go` | 20 min |
| 11 | Add hunk focus helpers and per-document transient folded state; retain the current raw patch-line cursor as the only anchor coordinate. | `internal/tui/review/model.go` | 45 min |
| 12 | Update movement, range-selection entry, comment navigation, and document changes so no hidden row can retain interaction focus. | `internal/tui/review/update.go` | 45 min |
| 13 | Render diff header status (`Hunk N/M` / `Hunks M` / `Raw diff`) and folded-body placeholders, then window only visible rows around the logical cursor without changing non-diff source behavior. | `internal/tui/review/view.go` | 60 min |
| 14 | Add model tests for wrapped hunk navigation, fold/unfold cursor behavior, skipped hidden rows, selection cancellation, comment navigation auto-expand, per-document fold isolation, and draft/reopen reset. | `internal/tui/review/model_test.go` | 75 min |
| 15 | Extend the existing compact diff fixture with a new file, deleted file, rename/mode metadata, and at least three separated hunks; retain it as deterministic manual acceptance data rather than capturing a live worktree diff. | `.agents/jig/fixtures/review-ui-demo.diff` | 20 min |
| 16 | Update the review UI demo comments with the new hunk keys and the distinction between source-file gutters and raw patch-line comment anchors. | `.agents/jig/review-ui-demo.toml` | 10 min |
| 17 | Document the dual-coordinate gutter, immutable patch-line anchors, hunk keys, and fold behavior in the architecture reference. | `docs/ARCHITECTURE.md` | 15 min |
| 18 | Add Monitor-level interaction coverage proving a review workspace keeps `[` / `]` / `z` inside the Gate, persists ordinary draft changes, and still fits the workspace at 80×24 and 120×40. | `internal/tui/monitor/review_workspace_interaction_test.go` | 45 min |

## Detailed test matrix

| Scenario | Expected result |
|---|---|
| Context line | Both old and new gutters increment and display. |
| Replacement | Delete gets only old number; subsequent add gets only new number; following context has the correct shifted coordinates. |
| Addition at file start | Addition gets its valid new line number; no fabricated old coordinate. |
| Deletion-only hunk | Deletion gets old coordinates; new counter resumes correctly for any following context. |
| `@@ -4 +4 @@` | Omitted counts parse and project correctly. |
| Multiple files / repeated path | Hunks preserve document order and never merge merely because paths are equal. |
| Rename, copy, mode, binary change | Metadata remains visible; no invented hunk or source coordinates. |
| `\\ No newline` | Does not advance either counter and is not a commentable source-file coordinate. |
| CRLF / final newline | Logical snapshot rows and anchors stay exact; source counters remain correct. |
| Malformed literal patch | Existing raw diff styles and patch-line gutter render; hunk keys are hidden; comments still work. |
| `]` on final hunk / `[` on first | Navigation wraps and moves cursor to target hunk header. |
| Fold active hunk | Header and folded placeholder remain visible; cursor is the header. |
| Move or comment into folded hunk | Navigation skips hidden rows; comment navigation unfolds first; range selection unfolds first. |
| Resize and pan | Dual gutters never pan or overflow; only ANSI-styled content is cut; `sourceXOffsets` clamps. |
| Draft/reopen | Comments and cursor persist as raw patch lines; folds do not persist and are reset. |

## Verification

Run after each phase, and again before handoff:

```bash
gofmt -w internal/tui/review/*.go internal/tui/shared/*.go internal/tui/monitor/review_workspace_interaction_test.go
go test ./internal/tui/review -count=1
go test ./internal/tui/shared -count=1
go test ./internal/tui/monitor -run 'ReviewWorkspace' -count=1
go test ./internal/tui/... -race -count=1
go test ./... -count=1
go run ./cmd/jig validate .agents/jig/review-ui-demo.toml
go run ./cmd/jig validate .agents/jig/feature.toml
go vet ./...
```

Manual acceptance from the repository root:

```bash
go run ./cmd/jig
```

Open `review-ui-demo`, select **Unified diff fixture**, and verify:

1. Every context/addition/deletion has the expected old/new source coordinates.
2. `[` / `]` visibly move between all text hunks and wrap at either end.
3. `z` folds and unfolds one hunk without hiding its header or losing comment
   access.
4. A comment on an added, removed, and hunk-header row still records the exact
   raw patch line in the saved review feedback.
5. `h` / `l` pan only content, not the rails or both line-number columns.
6. The narrow 80×24 layout has no overwide row and its footer advertises only
   applicable keys.
7. Changing the fixture temporarily to malformed text leaves the review
   workspace usable in its raw fallback mode.

## Delivery and rollback

Land Phase 1 before UI work: it supplies a deterministic semantic foundation
and a safe fallback. Phase 2 is a rendering-only change. Phase 3 introduces
new key behavior only for parsed diff documents; it cannot affect Markdown or
plain source review.

If the parser library proves incompatible with the repository's supported patch
shapes, remove the direct dependency and retain the current renderer—there is
no persisted state or schema migration to undo. Do not replace it with a
best-effort parser that invents source coordinates; showing the raw patch is
always safer than showing incorrect line numbers.

## Research references

- LazyGit separates patch parsing from rendering and exposes hunk bounds plus a
  current-file line mapping: <https://raw.githubusercontent.com/jesseduffield/lazygit/master/pkg/commands/patch/patch.go>.
- Its parser represents hunk headers and typed body rows: <https://raw.githubusercontent.com/jesseduffield/lazygit/master/pkg/commands/patch/parse.go>.
- LazyGit documents hunk navigation and hunk-selection behavior in its staging
  surface: <https://github.com/jesseduffield/lazygit/blob/master/docs/keybindings/Keybindings_en.md>.
- `go-gitdiff` parses Git/unified patches, including binary patches, and
  documents its stable parser API: <https://github.com/bluekeyes/go-gitdiff>.
