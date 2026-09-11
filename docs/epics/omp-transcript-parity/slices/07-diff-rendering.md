# Slice 07 — Real diffs with a fused gutter

- **Slice ID:** `diff-rendering`
- **Outcome:** An `edit` tool exchange shows **what changed** — added and removed
  lines with a line-number gutter, word-level highlighting of the changed span,
  and syntax-highlighted context — instead of the resulting file.
- **Why this slice exists:** This is the largest content gap between jig and omp
  and the one with real algorithmic work behind it (diff computation). It is
  independent of grouping (slice 08) and can be built in parallel once the card
  and truncation vocabulary exist.
- **Depends on:** Slices 01, 06.

---

## The unblocking finding

The original review flagged "does the adapter carry old text?" as an open
question. **It does.** `internal/toolcall/toolcall.go:38-44`:

```go
// Diff is an adapter-provided snapshot of a file modification. A nil OldText
// means the file did not exist before the change; an empty pointed-to value is
// an existing empty file.
type Diff struct {
	Path    string  `json:"path,omitempty"`
	OldText *string `json:"old_text,omitempty"`
	NewText string  `json:"new_text,omitempty"`
}
```

And it is populated end to end:

| Stage | File |
|---|---|
| ACP harness fills it | `internal/harness/acp.go:684-688` |
| runner redacts secrets in it | `internal/runner/agent.go:566-568` |
| transcript writer clamps it | `internal/transcript/writer.go:161-162, 197-198` |
| search indexes it | `internal/tui/monitor/monitor_search.go:248-249` |
| export sanitizes it | `internal/runexport/transcript.go:119-121` |
| sentinel prefilter reads it | `internal/sentinel/prefilter.go:108-109` |

**Only the monitor's renderer ignores it.** `writeNewCodeCards`
(`monitor_transcript_items_view.go:186-206`) uses `content.Diff.NewText` alone:

```go
// writeNewCodeCards deliberately shows the resulting source rather than a
// before/after patch. The inset Glamour renderer uses the shared Charm v2 code
// formatter, whose Lip Gloss code-block style owns the rounded card.
shown, hidden := boundTranscriptDetail(content.Diff.NewText, max(m.transcriptInnerW-8, 1))
```

The comment states an intent, not a constraint. The data for a real diff is
already on disk in every run.

---

## omp reference

### Wire format

`crates/pi-edit/src/diff_string.rs:69-71`:

```rust
fn format_numbered_diff_line(prefix: char, line_number: u32, content: &str) -> String {
    format!("{prefix}{line_number}|{content}")
}
```

Rows are `<marker><lineNo>|<content>`, marker ∈ `{'+', '-', ' '}`. The default
producer emits **no `@@` headers**; non-contiguous regions are separated by a
bare empty line. Default context is 2 lines.

### The fused gutter — the signature detail

`packages/coding-agent/src/tools/render-utils.ts:395-405`:

```ts
export function formatCodeFrameLine(
    marker: CodeFrameMarker,       // "" | " " | "*" | "+" | "-" | ">"
    lineNumber: string | number,
    content: string,
    lineNumberWidth: number,
): string {
    const markerText = marker.trim();
    const lineNumberText = String(lineNumber).trim();
    const gutterText = markerText && lineNumberText ? `${markerText}${lineNumberText}` : lineNumberText || markerText;
    return `${gutterText.padStart(lineNumberWidth + 1, " ")}│${content}`;
}
```

Marker and number are **concatenated into one token, right-aligned together**,
then a bare `│`, then content with no space:

```
 313│  const offset = args.offset ?? 1;
-314│  const limit = args.limit ?? 2000;
+314│  const limit = args.limit ?? 4000;
 315│  const raw = await Bun.file(path).text();
```

The marker rides *in* the gutter, not in a first content column. Compared with a
classic `+`/`-` first-column diff this reads as one aligned block and leaves the
code column undisturbed.

**Gutter width floors at 3, unconditionally** (`modes/components/diff.ts:119-128`),
with this rationale in-source:

> *"a streaming preview re-renders this diff as it grows, and a width derived
> purely from the current max line number widens at the 100-line crossing —
> re-padding every already-rendered row… A constant gutter through 999 lines
> keeps streamed rows byte-identical to the final result render."*

### Duplicate line-number suppression

`diff.ts:138-149`. When a row's trimmed number equals the previously emitted
row's, the number is blanked, leaving only the marker:

```
-315│  old line
   +│  new line          ← number suppressed; it repeats 315
```

Two cases trigger it: a single-line replacement (`-N` then `+N`) and an insertion
followed by context.

### Word-level intra-line diff — but only for 1↔1

`diff.ts:185-198`. The renderer greedily collects a run of `-` lines then a run of
`+` lines. **Only when both runs are exactly length 1** does it compute a word
diff:

```ts
if (removedLines.length === 1 && addedLines.length === 1) {
    const { removedLine, addedLine } = renderIntraLineDiff(...);
```

`renderIntraLineDiff` (`:55-99`) wraps changed spans in **`theme.inverse(...)` —
SGR 7 reverse video**, not a background color. Leading whitespace of the first
changed part is stripped out of the inverse so indentation is never highlighted
(`:70-76, 82-88`).

Multi-line change blocks get no intra-line highlight — flat red/green.

### Syntax-highlighted context

`diff.ts:231-267`. Context (` `) rows are batch-highlighted **in consecutive
runs** so multi-line grammars tokenize correctly across the run, then mapped back
per index. Collapse markers (`...` / `…`) are excluded from the run so they are
not lexed as spread operators and do not stitch unrelated blocks together.

**Added and removed lines are deliberately *not* syntax-highlighted** — they stay
flat `toolDiffAdded` / `toolDiffRemoved` so the change reads before the syntax.

### Indentation visualization

`diff.ts:16-34`:

- leading space → `\x1b[2m·\x1b[22m` (dim middle dot)
- leading tab → dim `" → "` (one space, arrow, one space at `DEFAULT_TAB_WIDTH = 3`)
- non-leading tabs → plain spaces

Applied to `+`/`-` rows and to unhighlighted context rows.

### Colors — foreground only

```ts
renderTheme.fg("toolDiffRemoved", ...)   // #fc3a4b
renderTheme.fg("toolDiffAdded",   ...)   // #89d281
renderTheme.fg("toolDiffContext", ...)   // #777d88
```

**No per-line background.** The card's state tint owns the background; a
per-line background would fight it. The whole line — gutter included — takes the
row's color.

### Gap rows

Any unparseable line, plus `""`, `"..."`, `"…"`, collapses to a single dim `…`
(`diff.ts:156-166`). A `@@` header therefore falls through as plain gray text —
it is *not* specially styled.

### The stats badge

`edit/renderer.ts:776-784`:

```ts
` ⟦` + green(`+3`) + dim(`/`) + red(`-2`) + `⟧`
```

Numbers colored, brackets and the slash dim. Goes in the header's badge slot
(slice 02).

### Wrapped continuation rows

`edit/renderer.ts:823-860`: wrapped rows get `" "×(prefixWidth-1) + "│"` as their
continuation prefix — blank gutter, same `│` column. Each row is terminated with
`\x1b[27m\x1b[39m` (close inverse, close fg) so frame padding is not painted as
an inverse block.

### Budgets

`render-utils.ts:102-104`: `DIFF_COLLAPSED_HUNKS = 8`,
`DIFF_COLLAPSED_LINES = 40`. Combined footer wording
(`edit/renderer.ts:813-818`): `… (3 more hunks, 12 more lines) ⟦Ctrl+O: Expand⟧`.

---

## The computation problem (CC-10)

jig depends on `github.com/bluekeyes/go-gitdiff v0.8.1`, which **parses** unified
patches. It does not compute them. `internal/tui/diffview.Parse(content string)`
(`diff.go:73`) is built on it and therefore also expects a patch string.

So jig has `OldText` and `NewText` but nothing that turns them into hunks. Three
options:

| Option | Cost | Notes |
|---|---|---|
| **A.** Add a diff library (e.g. a Myers/LCS line-differ) and emit a unified patch, then feed the existing `diffview.Parse`. | One new module. | Maximum reuse — `diffview.DisplayRows`, `RenderHunkHeader`, `RenderRawLine`, and the review workspace's presentation all keep working unchanged. |
| **B.** Hand-roll a line-level LCS (~100 lines) producing an op list, and render directly without a patch string. | No new dependency; new code to own and test. | Avoids a lossy round-trip through text. Word-level diff needs a second, token-level pass. |
| **C.** Ask the harness to supply a patch. | Changes the `toolcall` contract. | **Rejected** — epic NG5/CC-12 forbid wire-format changes, and not every backend can produce one. |

**Recommendation: A.** The diff string is also directly reusable by the review
workspace and by `runexport`, and `diffview` already handles hunk projection,
folding, and raw-line rendering. Record the choice as an ADR under `docs/adr/`.

Word-level diff (`diffWords`) is a separate, smaller problem and can be a
hand-rolled token LCS over a single line pair regardless of which option is
chosen.

---

## In Scope

- Compute a diff from `Diff.OldText` / `Diff.NewText` (option A unless the spec
  argues otherwise) and render it in a card section.
- The fused gutter: `formatCodeFrameLine` equivalent, width floored at 3,
  duplicate-number suppression.
- Word-level intra-line highlighting via reverse video for 1↔1 replacements only,
  with leading whitespace excluded.
- Syntax-highlighted context lines, batch-highlighted in runs.
- Indentation visualization (dim `·` for spaces, dim `→` for tabs).
- Foreground-only add/remove/context colors; no per-line background.
- The `⟦+N/-M⟧` stats badge in the header slot slice 02 provides.
- Flush body (`PadLeft: 0`) so the gutter sits against the card border.
- Collapsed budget (hunks and lines) with slice 06's wording.
- **Keep the resulting-source view for file creation.** When `OldText == nil`,
  the file is new and there is no diff to show — render the existing new-code
  card. This is the case the current comment was genuinely serving.

## Out of Scope

- Side-by-side diffs. omp is unified-only; so is `diffview`.
- Enclosing-block context injection via tree-sitter
  (`diff_string.rs:180-237`) — epic Deferred Work.
- Changing the review workspace's diff rendering
  (`internal/tui/review/view.go`), though it should be consulted for
  consistency and may later adopt the same gutter.
- Streaming/partial diff previews. jig renders from settled transcript entries.

## Functional Requirements

- **FR-07.1** When an activity's content carries a `Diff` with non-nil `OldText`,
  the system shall render added, removed, and context lines rather than the
  resulting file.
- **FR-07.2** When `OldText` is nil, the system shall render the resulting source
  as it does today.
- **FR-07.3** Each diff row shall carry a gutter whose marker and line number are
  right-aligned as one token, followed by `│` and the content.
- **FR-07.4** Gutter width shall be at least 3 regardless of the file's line
  count.
- **FR-07.5** A line number identical to the previously emitted row's shall be
  suppressed, leaving the marker.
- **FR-07.6** A change consisting of exactly one removed and one added line shall
  highlight the differing spans with reverse video, excluding leading whitespace.
- **FR-07.7** Multi-line change blocks shall not receive intra-line highlighting.
- **FR-07.8** Context lines shall be syntax-highlighted when the language is
  derivable from the path; added and removed lines shall not be.
- **FR-07.9** Leading spaces shall render as a dim middle dot and leading tabs as
  a dim arrow.
- **FR-07.10** Add, remove, and context colors shall be foreground-only.
- **FR-07.11** The header shall carry a `+N/-M` badge when the diff is non-empty.
- **FR-07.12** A collapsed diff shall bound both hunks and lines, and state what
  it hid using the slice 06 vocabulary.
- **FR-07.13** Wrapped continuation rows shall align to the content column with a
  blank gutter.
- **FR-07.14** A diff whose computation fails shall fall back to the
  resulting-source view rather than rendering nothing.

## Technical and Repository Constraints

- `codeLanguage(path)` already exists (`items_view.go:217-247`) mapping extension
  → Chroma language. Reuse it for FR-07.8.
- Syntax highlighting goes through the existing glamour/Chroma path
  (`fencedCode` + `m.insetRenderer`, `items_view.go:208-215`). Highlighting a
  *run* of context lines means fencing the run, rendering, and splitting — the
  same batching omp does, for the same reason.
- Reverse video: `lipgloss.NewStyle().Reverse(true)`.
- Content is already clamped at write time
  (`internal/transcript/writer.go:161-162` clamps `OldText`), so a diff may be
  computed over **truncated** text. Detect this and label it, or the diff will
  claim changes that are artifacts of truncation. **This is a correctness
  hazard, not a cosmetic one.**
- Per CC-12, do not change `toolcall.Diff`.
- Any new module must be justified; add it to `go.mod` and note the license.
- Diff computation is O(ND); bound the input before computing, and cache the
  result per item in `chatItemRendered`.

## Security and Data Considerations

- `OldText` and `NewText` are already secret-redacted upstream
  (`internal/runner/agent.go:566-568`) and size-clamped at write.
- A new diff dependency is new supply-chain surface — prefer a small,
  well-known, dependency-free module; record the evaluation.
- Reverse video on attacker-influenced content cannot break the frame the way a
  raw escape could, but the underlying text should still be escape-stripped
  before rendering.

## Acceptance Evidence

- Table-driven tests: single-line replacement (with intra-line spans),
  multi-line replacement (without), pure insertion, pure deletion, file creation
  (`OldText == nil`), and a diff over clamped text.
- A test asserting gutter width ≥ 3 for a 5-line file and correct alignment for a
  1000-line file.
- A test asserting duplicate line numbers are suppressed.
- A test asserting no background escape appears on any diff row (FR-07.10).
- A test asserting context lines contain highlighting escapes and `+`/`-` lines
  do not (FR-07.8).
- Visual: an edit card where the changed word is legible at a glance.

## Inputs for the Child Spec

- **`OldText` exists and is populated.** This is the finding that unblocks the
  slice; the spec should not re-litigate it.
- CC-10: choose among options A/B/C above and record an ADR. A is recommended.
- The clamped-text hazard is the subtlest correctness risk in the epic — address
  it explicitly, do not defer it.
- Keep the `OldText == nil` path; the current comment's intent survives as the
  file-creation case.
- `diffview` already provides `Parse`, `DisplayRows`, `RenderHunkHeader`,
  `RenderRawLine`, and hunk folding. Prefer extending it over a parallel
  implementation.

## Open Questions

- **Q-07.1 (blocking)** Which computation option? Needs a dependency review.
- **Q-07.2** How should a diff over clamped text be labeled? *Suggest a warning
  row using slice 06's vocabulary, e.g. `… content clamped at write; diff may be
  incomplete`.*
- **Q-07.3** Should the review workspace adopt the same fused gutter for
  consistency? *Out of scope here; flag as a follow-up so the two surfaces do not
  diverge permanently.*
- **Q-07.4** Does jig want `@@` hunk headers rendered, or omp's bare-gap-row
  elision? jig's `diffview.RenderHunkHeader` exists and the review workspace uses
  it. *Suggest keeping hunk headers — jig already has the concept and operators
  reading a spec-driven workflow benefit from the line anchors.*
