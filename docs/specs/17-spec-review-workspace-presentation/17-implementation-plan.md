# Implementation Plan: Rich Document Review Presentation

**Status:** Proposed; ready for human review  
**Primary surfaces:** `internal/tui/review`, `internal/tui/shared`  
**Supporting surfaces:** `internal/tui/monitor`, `.agents/jig`  
**Schema changes:** none  
**Persistence changes:** none  
**New dependencies:** none  
**Risk:** medium  
**Estimated focused implementation time:** 16–20 hours  
**Delivery shape:** two independently verifiable phases

---

## Summary

Make the document review workspace pleasant to read without changing its
immutable-document, logical-source-line, or atomic-submission contracts. Phase 1
turns the existing Glamour Markdown preview into a discoverable, polished review
surface; Phase 2 adds a format-aware, syntax-highlighted source viewer with a
stable gutter, comment markers, and horizontal panning while preserving exact
line and range comments.

The main risk is allowing rendered terminal rows to become interaction state.
They must not: document identity, cursor position, selected ranges, comment
anchors, draft projection, and submission validation continue to use the
existing immutable source document and one-based logical source line numbers.

## Feature summary

Present Markdown, source code, diffs, and plain text with the existing Charmtone
theme while retaining every current review action: document navigation,
source/preview switching, line and range selection, typed comments, comment
editing/deletion, reviewed acknowledgements, summary entry, and final verdict
submission.

## Approach

Deepen presentation behind the existing `internal/tui/review.Model` instead of
replacing the review workspace or teaching the domain layer about terminal
rendering. Phase 1 keeps Goldmark as the source-address mapping implementation
and Glamour as the Markdown renderer, but makes preview the default for valid
Markdown, adds an obvious mode switch, applies the shared custom Chroma code
formatter, and decorates preview blocks with source ranges and comment state.
After that UI has been manually accepted, Phase 2 adds an internal source
rendering module that tokenizes whole documents with Chroma, returns exactly one
styled string per logical source line, and lets `view.go` add gutters, selection,
comments, and ANSI-aware horizontal clipping. No engine, runner, workflow,
datastore, review-domain, or submission changes are required.

The key design decision is to separate **content rendering** from **review row
decoration**:

```text
immutable document content
        │
        ├── Markdown preview: Goldmark source blocks → Glamour rendered blocks
        │
        └── Source view: Chroma tokens/diff rules/plain text → styled source lines
                                                                    │
                                                                    ▼
                     logical-line gutter + cursor/range + comments + clipping
```

Renderers may color and lay out content, but they do not own the cursor, range,
active comment, reviewed status, or input handling. This keeps the rendering
module deep and leaves the existing model interface unchanged for Monitor.

---

## Existing behavior that must remain true

1. `review.Document.Content` or the immutable snapshot file remains the reviewed
   source of truth. Presentation never edits it.
2. `Model.cursor` and `Model.rangeEnd` remain one-based logical source lines.
3. A `review.Anchor` continues to contain the exact document ID, SHA-256 digest,
   inclusive start/end lines, quote, prefix, and suffix produced by
   `document.anchor`.
4. Source mode supports single-line and multiline comments.
5. Preview comments anchor complete Goldmark source blocks. The renderer's
   physical terminal rows are never persisted as anchors.
6. Comments retain their kinds and suggestion replacement validation.
7. Draft and submission messages retain their current shape and timing.
8. The review workspace remains a child of the Monitor's queued gate and does
   not acquire its own engine or persistence responsibilities.
9. Narrow and wide layouts continue to work through `EmbeddedView` and the
   Monitor's final `fitBlock` composition.
10. Persistence-off remains unaffected because this feature adds no writers and
    changes no snapshot/draft paths.
11. Non-Markdown content is never passed through Glamour. In particular, diffs,
    code files, and plain text must remain verbatim apart from ANSI styling and
    horizontal clipping.
12. All new Lip Gloss styles live under the `shared.Styles` singleton in
    `internal/tui/shared/styles.go`; render files must not create ad-hoc colors.

## Explicit non-goals

1. No in-place editing, LSP integration, diagnostics, completion, or undo tree.
2. No column-level selection or arbitrary selection of rendered Markdown text.
3. No attempt to map each Glamour output row back to a source line.
4. No mouse text-range selection.
5. No automatic application of suggestions.
6. No external `$EDITOR` integration.
7. No new workflow fields such as `language`, `theme`, or `default_view`.
8. No user-selectable syntax theme in this change; the existing dark-only
   Charmtone theme remains authoritative.
9. No soft wrapping of code in Phase 2. Long code lines use horizontal panning
   so one source line always occupies one source row.
10. No replacement of the review workspace with a generic third-party editor
    widget. Charm does not provide a source-review bubble, and the existing
    review state is already the correct interaction model.

---

## Libraries and package responsibilities

Use the versions already pinned by `go.mod`; do not add or upgrade dependencies
as part of this work.

| Package | Pinned version | Responsibility in this plan |
|---|---:|---|
| `charm.land/bubbletea/v2` | `v2.0.8` | Existing update loop and key messages; no new program-level behavior. |
| `charm.land/bubbles/v2` | `v2.1.1` | Existing textareas and key bindings. Do not add a nested viewport to the review model. |
| `charm.land/lipgloss/v2` | `v2.0.5` | Mode tabs, gutters, rails, badges, comment state, and layout through the shared theme. |
| `charm.land/glamour/v2` | `v2.0.1` | Render Markdown preview blocks using `shared.Theme.Markdown`. |
| `github.com/yuin/goldmark` | `v1.7.13` | Existing Markdown AST parsing and source-block-to-line mapping. |
| `github.com/alecthomas/chroma/v2` | `v2.27.0` | Existing code-block highlighting in Phase 1 and whole-document source tokenization in Phase 2. |
| `github.com/charmbracelet/x/ansi` | `v0.11.7` | Width-safe clipping of already-styled source lines with `ansi.Cut`; already used elsewhere in the TUI. |

### Why not use a nested `bubbles/viewport`

The review model already windows source by logical line around `Model.cursor`,
and Monitor clips the complete workspace to its assigned rectangle. Adding a
second viewport would create another vertical cursor/offset that must be kept in
sync with line selection and comment composition. Phase 2 needs only horizontal
panning; a small per-document `sourceXOffset` plus `ansi.Cut` preserves locality
and avoids duplicate vertical state. Bubbles remains responsible for the
existing Monitor viewports and textareas.

### Chroma lexer selection

Phase 2 must choose a lexer deterministically in this order:

1. `Document.Format == "diff"`, or a literal source ending in `.diff` or
   `.patch`: use the dedicated diff renderer, not Chroma. Literal diff files are
   currently classified as `text` by the engine, so the extension check belongs
   in presentation and does not require an engine/schema change.
2. `Document.Format == "markdown"`: use `lexers.Get("markdown")` in source mode.
3. Otherwise call `lexers.Match(Document.Source)` so literal files such as
   `.go`, `.toml`, `.json`, and `.sh` use their filename extension.
4. If no useful lexer matches, use plain text. Do not use `lexers.Analyse` as an
   implicit fallback; content guessing can make the same `text` artifact change
   appearance after a small edit and is unnecessary when the source name is
   available.

Tokenize the complete document with `chroma.Coalesce(lexer).Tokenise` and
`TokeniseOptions{EnsureLF: true}`. Do not tokenize each line independently:
whole-document tokenization preserves multiline strings, comments, heredocs,
and other lexer state. Split rendered token values at newlines afterward so the
result contains exactly `len(document.lines)` logical rows.

---

# Phase 1 — Discoverable, polished Markdown preview

## Phase goal

Make Markdown review immediately pleasant and self-explanatory using the preview
capability that already exists. After this phase, a reviewer can open a Markdown
document, see a rendered document by default, recognize the active Markdown
block and its exact source range, see whether comments intersect that block,
switch visibly to source, and perform all existing comment/review actions.

Phase 1 must be independently mergeable. It must not contain unused source-code
highlighting scaffolding intended only for Phase 2.

## Deterministic manual-review workflow

Phase 1 adds `.agents/jig/review-ui-demo.toml` as a durable visual acceptance
fixture. It should contain one review step and no agent or command steps, so it
opens immediately and never depends on a configured backend. Use this shape:

```toml
[workflow]
name        = "review-ui-demo"
version     = "1"
description = "Deterministic visual fixture for the document review workspace."

[[step]]
id          = "review_ui"
type        = "review"
output_type = { enum = ["accept", "revise"] }

[[step.review]]
source = "../../docs/ARCHITECTURE.md"
label  = "Markdown architecture document"

[[step.review]]
source = "../../internal/tui/review/view.go"
label  = "Go source"

[[step.review]]
source = "../../go.mod"
label  = "Plain text / module file"

[[step.review]]
source = "fixtures/review-ui-demo.diff"
label  = "Unified diff fixture"
```

The workflow-relative paths must be validated from `.agents/jig`. Keep the diff
fixture small and synthetic; it is test data, not a snapshot of whichever local
working tree happens to run the demo. Do not use `source = "diff"` here: that
special source collects captured diffs from dependent worktree-isolated agent
steps and would be empty in a review-only workflow.

## Phase 1 design decisions

### P1-D1: Preview is per document

Replace the single global `Model.documentMode` with per-document presentation
state, for example `documentModes []DocumentMode`. Initialize each valid
Markdown document to `DocumentPreview`; initialize `diff` and `text` documents
to `DocumentSource`. Switching documents restores that document's last mode for
the lifetime of the workspace.

This fixes the current accidental behavior where a preview selection can carry
onto a diff/text document. Do not add this transient preference to
`review.Draft` in this feature: draft persistence remains concerned with review
work, and reopening a round should use the new readable defaults.

### P1-D2: Preview availability is explicit

Only documents with `Format == "markdown"` may enter preview. Build Goldmark
preview state only for those documents. `s` on diff/text should remain a safe
no-op and set a short model error such as `preview is available for Markdown`;
it must never feed the document to Glamour.

If Goldmark cannot produce source-addressable blocks, initialize that document
in source mode and show the existing preview-unavailable error only when the
operator requests preview.

### P1-D3: Render a visible mode switch

Replace the subtle `LABEL · PREVIEW` text with a compact two-state control in the
document body header:

```text
Plan summary                    [ PREVIEW ]  SOURCE     Markdown
```

- Active mode uses `shared.Theme.Review.ModeActive`.
- Inactive available mode uses `shared.Theme.Review.ModeInactive`.
- For diff/text, omit `PREVIEW` rather than displaying a control that cannot be
  entered.
- Keep `s` as the binding; label it `toggle source/preview` in full help.
- Include `s view` in compact help when the active document is Markdown.
- The switch is visual only; no mouse behavior is introduced.

### P1-D4: Decorate source-addressable preview blocks

Every Goldmark top-level block retains its current `startLine/endLine` mapping.
Render it as:

```text
▌ L12–L18  ● 2 comments
│ <Glamour-rendered block>
│ <continued rendered rows>
```

- The active block uses the primary `Review.BlockRailActive` and
  `Review.BlockMetaActive` styles.
- Inactive blocks use muted rail/meta styles.
- `commentsForRange(documentID, start, end)` counts comments whose inclusive
  source ranges overlap the block; it does not require exact range equality.
- If the active comment intersects the block, render its ID in the metadata,
  e.g. `● C003 active`, so `n`/`N` navigation has visible feedback.
- Keep the complete comment list and composer below the document. Block badges
  are navigation context, not a replacement for comment bodies.
- Do not place a Lip Gloss background across Glamour output; nested ANSI resets
  make a solid selection background unreliable. Use the rail and metadata for
  selection instead.

### P1-D5: Use the shared code-block formatter

Construct the preview Glamour renderer with:

- `glamour.WithStyles(shared.Theme.Markdown)`
- `glamour.WithWordWrap(innerPreviewWidth)`
- `glamour.WithChromaFormatter(shared.CodeBlockFormatter(codeWidth))`

Match the Monitor's existing rule that Glamour width is construction-time state:
when the preview width changes, rebuild the renderer and clear the per-block
cache. Store one renderer per `previewState`/width rather than constructing a
renderer for every uncached block.

Factor only the width calculation that is genuinely shared. If moving
`newMarkdownRenderer` or `markdownCodeWidth` out of
`internal/tui/monitor/monitor_layout.go` would broaden Phase 1 or disturb Monitor
rendering, keep the review constructor local and cover its width behavior in
`preview_test.go`.

### P1-D6: Keep block interaction source-based

- `j`/`k` in preview move between Goldmark blocks.
- `c` comments the active block's complete source range.
- `n`/`N` continue to navigate comments using their source anchor, then select
  the containing preview block with `blockForLine`.
- `s` maps preview to source at the active block's `startLine`; switching back
  selects the block containing the source cursor.
- Preserve `v` range selection in source mode. Do not advertise rendered-text
  range selection in preview; preview's supported annotation unit is a complete
  block.
- If `v` is pressed in preview, keep the active block selected and show `c` as
  the action. Do not enter a misleading multiline rendered-row selection mode.

## Phase 1 file-by-file tasks

| # | Task | Area | Estimate |
|---:|---|---|---:|
| 1 | Add a `Review` style group containing mode-active/inactive, block rail/meta, comment marker, and active-comment styles, deriving every value from existing palette tokens. | `internal/tui/shared/styles.go` | 30 min |
| 2 | Add focused style assertions proving the review styles come from the shared theme and render without changing expected visible width. | `internal/tui/shared/styles_test.go` | 20 min |
| 3 | Change review presentation mode to per-document state, default valid Markdown to preview, force diff/text to source, and expose small active-mode/preview-availability helpers used by view/update. | `internal/tui/review/model.go` | 40 min |
| 4 | Restrict preview construction to Markdown, cache a width-specific Glamour renderer, use `shared.CodeBlockFormatter`, and retain exact Goldmark block mappings. | `internal/tui/review/preview.go` | 55 min |
| 5 | Add tests for Markdown-only preview availability, renderer reuse, width invalidation, fenced-code highlighting, Unicode, empty Markdown, and exact source ranges. | `internal/tui/review/preview_test.go` | 50 min |
| 6 | Update mode toggling, document changes, preview comment opening, and comment navigation so each document restores its presentation and preview always lands on a source-addressable block. | `internal/tui/review/update.go` | 45 min |
| 7 | Render the visible mode switch, block rails, line-range metadata, overlapping-comment counts, and active-comment state while keeping the existing comment list/composer and responsive panels. | `internal/tui/review/view.go` | 75 min |
| 8 | Clarify compact/full help labels for preview behavior without changing the `s` binding or editor capture behavior. | `internal/tui/review/keys.go` | 15 min |
| 9 | Expand model/view tests for default modes, per-document restoration, non-Markdown fallback, source/preview cursor mapping, comment anchoring, active-comment decoration, narrow layout, and composer sizing. | `internal/tui/review/model_test.go` | 60 min |
| 10 | Add Monitor integration assertions that an open workspace still owns its keys, `s` reaches the child model, global help reflects the child bindings, and the polished workspace fits at 120×40 and 80×24. | `internal/tui/monitor/review_workspace_interaction_test.go` | 45 min |
| 11 | Add a deterministic, no-agent `review-ui-demo` workflow with literal Markdown, Go, plain-text, and `.diff` review targets so both phases can be manually inspected from the real selector. | `.agents/jig/review-ui-demo.toml` | 20 min |
| 12 | Add a compact unified-diff fixture containing file headers, metadata, multiple hunks, additions, removals, context, and a no-newline marker. | `.agents/jig/fixtures/review-ui-demo.diff` | 15 min |
| 13 | Record the visible preview behavior and source-addressing invariant in the TUI architecture documentation. | `docs/ARCHITECTURE.md` | 20 min |

**Phase 1 estimate:** 7–9 hours.

## Phase 1 automated verification

Run in this order:

```bash
gofmt -l -w internal/tui/shared/*.go internal/tui/review/*.go internal/tui/monitor/review_workspace_interaction_test.go
go test ./internal/tui/shared ./internal/tui/review -count=1
go test ./internal/tui/monitor -run 'ReviewWorkspace' -count=1
go test ./internal/tui/... -race -count=1
go run ./cmd/jig validate .agents/jig/review-ui-demo.toml
go run ./cmd/jig validate .agents/jig/feature.toml
go vet ./...
```

Phase 1 is not complete if a renderer error is silently swallowed in a test.
Tests should assert the fallback source presentation and visible error text.

## Phase 1 manual UI checkpoint

Start the actual TUI from the repository root:

```bash
go run ./cmd/jig
```

Select `review-ui-demo`. It must reach the review gate without invoking an agent
or requiring backend credentials. Open the review workspace and check:

1. The Markdown document opens in `PREVIEW` by default.
2. `PREVIEW` is visibly active and `SOURCE` is visibly available.
3. Headings, lists, blockquotes, tables, inline code, and fenced Go code use the
   Charmtone Glamour theme; fenced code has the same rounded/themed treatment as
   Monitor code blocks.
4. `j`/`k` move a clear rail between rendered blocks, and each selected block
   displays its exact source range.
5. Pressing `c` creates a comment for the selected block; after saving it, the
   block shows a comment marker/count and the comments section still shows the
   full body.
6. Create comments of at least `concern` and `suggestion` kinds and confirm the
   composer and replacement textarea still fit.
7. `n`/`N` visibly move the active comment and corresponding block.
8. `s` enters source mode at the same block. Exact line/range selection and
   comment creation remain functional there.
9. Switch to the Go, text, and literal diff documents. They remain in source mode and do
   not get mangled by Glamour. Requesting preview gives a bounded explanatory
   error rather than changing the content.
10. Switch back to Markdown. Its prior preview/source choice is restored.
11. Mark each document reviewed, open Summary, choose a verdict, and verify the
    existing atomic submission flow is unchanged.
12. Repeat at approximately 80×24 and 120×40. Borders must remain complete,
    metadata must not overlap content, and the composer must stay inside the
    document panel.

Stop after Phase 1 and obtain visual approval before starting Phase 2. Any
spacing, color, rail, badge, or default-mode feedback should be resolved here so
Phase 2 can reuse the accepted visual language.

## Phase 1 acceptance gate

- All Phase 1 automated checks pass.
- Manual review at both target sizes is accepted.
- Markdown preview is the default only when it is valid and source-addressable.
- Every preview comment still validates against the immutable source snapshot.
- Diff/text/code content is never sent to Glamour.
- No Phase 2 source renderer types or unused syntax styles have been added.

---

# Phase 2 — Syntax-highlighted, line-addressable source viewer

## Phase goal

Replace the plain source rows with an editor-quality read-only presentation:
syntax-highlighted code/Markdown, semantic diff colors, a stable line-number
gutter, range and cursor rails, comment markers, language labels, and horizontal
panning. All source interaction remains line-addressable and all Phase 1
Markdown behavior remains unchanged.

Phase 2 starts only after Phase 1's manual acceptance gate passes.

## Phase 2 design decisions

### P2-D1: Add an internal source-rendering module, not a new public seam

Create `internal/tui/review/source.go` with a small internal result type, for
example:

```go
type sourcePresentation struct {
    lines    []string // ANSI styled; exactly one entry per document.lines entry
    language string   // Go, Markdown, Diff, Plain text, ...
    err      error    // non-fatal: view falls back to verbatim source
}

func buildSourcePresentation(d document) sourcePresentation
```

This module owns lexer selection, whole-document tokenization, splitting tokens
into logical lines, diff classification, and plain-text fallback. It does not
own cursor/range state, widths, offsets, comments, gutters, panels, or key
handling. There are already several real rendering variants (Chroma, diff, and
plain), so this seam earns its keep while remaining private to the review
package.

Build presentations once in `NewWithDraft`, alongside preview states. Source
coloring does not depend on terminal width, so it must not be rebuilt on resize
or cursor movement.

### P2-D2: Define source syntax colors in the shared theme

Extend `Styles.Review` with:

- `Gutter`, `GutterCursor`, `GutterRange`
- `CursorRail`, `RangeRail`
- `CommentMarker`, `ActiveCommentMarker`
- `Language`, `HorizontalHint`
- `Syntax *chroma.Style` (or an equivalent value stored by the singleton)

Construct the Chroma style in `DefaultTheme` from existing Charmtone semantic
tokens. At minimum define text, comment, preprocessor, keyword/reserved/type,
operator, punctuation, name/builtin/function/class, string/escape, number,
inserted, deleted, subheading, and error entries. Do not select an unrelated
stock Chroma theme such as Dracula: source and Glamour code blocks should feel
like one application.

Use the same conceptual color mapping already present in
`Theme.Markdown.CodeBlock.Chroma`. It is acceptable to factor a private helper
that produces both Glamour's `ansi.Chroma` entries and Chroma's `StyleEntries`
from shared tokens if that reduces drift; do not expose palette hex constants
outside `shared`.

### P2-D3: Preserve one rendered item per logical source line

The source presentation's `lines` length must exactly match
`len(document.lines)`, including:

- an empty final line when content ends in `\n`;
- consecutive blank lines;
- CRLF normalized consistently with the existing `strings.Split(content,
  "\n")` source model;
- tokens that begin before and end after a newline;
- Unicode and wide runes.

On tokenization error or line-count mismatch, record the error and fall back to
the original unstyled `document.lines`. A display failure must never make a
review document unavailable or change its anchor lines.

### P2-D4: Treat diffs as a first-class source format

Classify each diff source line without altering it:

- `+++` / `---` file headers: muted metadata style, not add/remove.
- `@@`: `Theme.Diff.Hunk`.
- `+` excluding `+++`: `Theme.Diff.Add`.
- `-` excluding `---`: `Theme.Diff.Remove`.
- `diff `, `index `, rename/mode metadata, and `\ No newline...`: muted diff
  metadata.
- context and unknown lines: base source text.

The line gutter uses review styles, not the diff content style. Selection and
comment markers remain visible regardless of add/remove color.

### P2-D5: Compose a stable source gutter

Render every visible logical line in this order:

```text
<cursor/range rail> <comment marker> <right-aligned line number> │ <content>
```

Rules:

- Gutter width is based on the total document line count, not the current
  window, so it does not jump while navigating.
- Cursor and selected-range state are indicated in the rail and gutter. Do not
  apply a full-row background over syntax-colored content.
- A comment marker appears on each comment's start line. For a multiline anchor,
  use a quieter continuation glyph on covered lines only if it remains readable
  at 80 columns.
- Multiple comments on one start line show a compact count (`●2`).
- The active comment marker uses the primary/accent style.
- The existing comments section remains below the source window.
- Use `lipgloss.Width`, never byte length, for gutter/content width math.

### P2-D6: Add ANSI-safe horizontal panning

Add per-document horizontal offsets, for example `sourceXOffsets []int`, to the
TUI model. Source mode binds:

- `h` / left arrow: pan left by four cells.
- `l` / right arrow: pan right by four cells.
- `0`: return to column zero, unless `0` is needed by an active text editor or
  Summary verdict input (those modes already capture first).

Apply the offset only to the styled content segment after reserving the gutter.
Use `ansi.Cut(styledLine, offset, offset+contentWidth)` so escape sequences and
wide runes are not split. Clamp offsets to the longest visible/document line as
appropriate, reset an invalid offset after resize, and retain a separate offset
for each document during the workspace lifetime.

Do not soft-wrap source code. One logical source line must remain one physical
source row so vertical windowing, selection, and the fixed-height composer stay
predictable. Show a small `← col N →` hint only when content is horizontally
panned or overflow exists.

### P2-D7: Identify the renderer in the document header

The Phase 1 header's trailing label should report the presentation accurately:

- `Markdown` in rendered preview.
- `Markdown source` in Markdown source mode.
- Chroma lexer config name such as `Go`, `TOML`, `JSON`, or `Shell` for matched
  literal files.
- `Diff` for diff documents.
- `Plain text` for fallback.

This label is informational only and is not persisted.

## Phase 2 file-by-file tasks

| # | Task | Area | Estimate |
|---:|---|---|---:|
| 1 | Extend the accepted Phase 1 review theme with gutter, range/cursor rail, language, horizontal hint, and Charmtone-derived Chroma syntax styles. | `internal/tui/shared/styles.go` | 45 min |
| 2 | Add tests for representative Chroma token mappings, gutter widths, and absence of hard-coded render-file colors. | `internal/tui/shared/styles_test.go` | 30 min |
| 3 | Implement deterministic lexer selection, `.diff`/`.patch` presentation detection, whole-document Chroma tokenization, logical-line reconstruction, diff classification, and verbatim fallback. | `internal/tui/review/source.go` | 90 min |
| 4 | Test Go/Markdown/TOML/JSON selection, unknown extensions, multiline tokens, CRLF, blank/final lines, Unicode, malformed input fallback, and every diff line class. | `internal/tui/review/source_test.go` | 75 min |
| 5 | Add cached source presentations and per-document horizontal offsets without changing draft/submission projection. | `internal/tui/review/model.go` | 30 min |
| 6 | Add source-only horizontal bindings and contextual compact/full help; ensure compose/edit/summary text capture continues to win over navigation. | `internal/tui/review/keys.go` | 25 min |
| 7 | Handle pan left/right/reset, clamp offsets after resize/document changes, and leave preview navigation untouched. | `internal/tui/review/update.go` | 45 min |
| 8 | Replace plain `fmt.Sprintf` source rows with styled content, fixed gutter, cursor/range rails, comment markers, language label, and ANSI-safe clipping. | `internal/tui/review/view.go` | 90 min |
| 9 | Expand model/view tests for exact anchors under ANSI styling, horizontal panning, wide runes, multi-comment gutters, active comments, range selection, composer sizing, and 80/120-column layouts. | `internal/tui/review/model_test.go` | 75 min |
| 10 | Extend the Monitor review integration test to prove source pan keys are routed only while the workspace is open and final composition never emits a row wider than the terminal. | `internal/tui/monitor/review_workspace_interaction_test.go` | 35 min |
| 11 | Update the deterministic demo workflow comments/instructions to call out its Markdown, Go, plain-text, and literal-diff checks; keep all targets valid. | `.agents/jig/review-ui-demo.toml` | 10 min |
| 12 | Document format dispatch, logical-line identity, and horizontal-pan behavior. | `docs/ARCHITECTURE.md` | 20 min |

**Phase 2 estimate:** 9–11 hours.

## Phase 2 automated verification

Run in this order:

```bash
gofmt -l -w internal/tui/shared/*.go internal/tui/review/*.go internal/tui/monitor/review_workspace_interaction_test.go
go test ./internal/tui/shared ./internal/tui/review -count=1
go test ./internal/tui/monitor -run 'ReviewWorkspace' -count=1
go test ./internal/tui/... -race -count=1
go run ./cmd/jig validate .agents/jig/review-ui-demo.toml
go run ./cmd/jig validate .agents/jig/feature.toml
go test ./...
go vet ./...
```

Add at least one test that strips ANSI from every rendered source row and proves
the visible content corresponds to the original logical line after applying the
expected horizontal slice. Also assert that comment anchor quotes are byte-for-
byte unchanged before and after highlighted rendering.

## Phase 2 manual UI checkpoint

Run the same deterministic workflow:

```bash
go run ./cmd/jig
```

Select `review-ui-demo`, open the workspace, and check:

1. The Markdown document still opens in the Phase 1 preview with no visual
   regression.
2. Toggle Markdown to source and confirm Markdown punctuation, headings, links,
   and fenced-code syntax are highlighted while every gutter number remains an
   exact source line.
3. Open the Go document. Keywords, types, functions, strings, numbers, and
   comments are distinguishable and fit the accepted Charmtone palette.
4. Select a multiline Go range with `v` and `j`/`k`, create a concern, and verify
   the stored/displayed range and quote match the original file—not ANSI output.
5. Add two comments starting on the same line and confirm the gutter shows a
   count without shifting neighboring line numbers.
6. Use `n`/`N`; the active comment marker and source cursor move together.
7. Use `h`/`l` or arrow keys on a deliberately long Go line. Only content pans;
   the cursor rail, comment marker, line number, and separator remain fixed.
8. Press `0` to return to the first column. Resize while panned and confirm the
   offset clamps without blanking the document.
9. Open the literal diff fixture. File headers,
   hunks, additions, removals, metadata, and context have distinct but coherent
   styling. Selection remains visible on both red and green lines.
10. Open the plain-text document. Its spacing and punctuation remain verbatim;
    it does not receive a guessed language or Markdown reflow.
11. Enter comment compose, suggestion replacement, and Summary modes. Printable
    `h`, `l`, and `0` must type into the textarea rather than pan content.
12. Repeat at approximately 80×24 and 120×40. Confirm no row crosses the right
    border, wide Unicode characters are not split, and the footer/help remains
    understandable.
13. Complete the review to prove reviewed acknowledgements, comments, summary,
    verdict, and submission are unchanged.

## Phase 2 acceptance gate

- All Phase 2 and full repository checks pass.
- Manual review at both target sizes is accepted.
- Syntax rendering is cached per immutable document and does not rerun on cursor
  movement or resize.
- Styled output contains exactly one entry per logical source line.
- Horizontal panning is ANSI- and wide-rune-safe and never moves the gutter.
- Comment anchors and submission artifacts contain no ANSI sequences and remain
  identical to anchors created from the unstyled source.
- Unknown or failed lexers fall back to usable plain text.
- No schema, engine, runner, datastore, transcript, or review-domain behavior
  changed.

---

## Test matrix across both phases

| Concern | Phase 1 proof | Phase 2 proof |
|---|---|---|
| Markdown readability | Glamour preview snapshots/assertions + manual review | Regression check only |
| Source addressability | Goldmark block ranges and comment anchor tests | Styled-line count and unchanged quote tests |
| Code readability | Fenced code through shared Chroma formatter | Whole-document source Chroma rendering |
| Diff readability | Verbatim source fallback | Dedicated semantic diff renderer |
| Plain text safety | Never sent through Glamour | Plain fallback remains verbatim |
| Selection | Active block rail; existing source range behavior | Fixed gutter/rails over styled content |
| Comments | Overlap badges and active comment state | Per-line markers/counts and unchanged anchors |
| Resize | Preview cache invalidation and panel fit | Offset clamp and ANSI clipping |
| Narrow layout | 80×24 acceptance | 80×24 source/gutter acceptance |
| Wide layout | 120×40 acceptance | 120×40 code acceptance |
| Input capture | Existing composer/summary behavior | Navigation keys type normally in editors |
| Integration | Monitor routes `s` to open child workspace | Monitor routes pan keys only to open child workspace |

## Rollback and failure behavior

- Phase 1 is a complete stopping point. If Phase 2 is deferred, Markdown review
  is still materially improved and source mode remains functionally equivalent
  to today.
- A Phase 1 Glamour or mapping failure falls back to source mode for that
  document and reports a bounded error in the workspace.
- A Phase 2 lexer or tokenization failure falls back to unstyled source lines;
  it must not prevent commenting or submission.
- Since neither phase changes persisted domain data or workflow schema, rollback
  is a normal source revert with no migration or compatibility path.

## Final completion criteria

The feature is complete when both phase gates have been manually accepted and a
reviewer can fluidly move between rendered Markdown and highlighted source,
understand what is selected and commented, inspect long code/diff lines, and
submit exactly the same deterministic review data as before.
