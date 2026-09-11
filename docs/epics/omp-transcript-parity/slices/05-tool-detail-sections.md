# Slice 05 — Tool detail bodies as card sections

- **Slice ID:** `tool-detail-sections`
- **Outcome:** An expanded tool exchange renders its locations, input, output,
  and content as labeled sections inside its card, replacing the
  `      Label:` + `      │ row` detail style.
- **Why this slice exists:** Slice 01 puts a frame around the header; without
  this slice the body still renders in the old indented style *below* the frame,
  which looks worse than either style alone. This is the slice that makes the
  card contain something.
- **Depends on:** Slices 01, 02.

---

## Current jig state

`internal/tui/monitor/monitor_transcript_items_view.go:127-140`:

```go
func (m *Model) writeItemDetail(b *strings.Builder, label, content string) {
	shown, hidden := boundTranscriptDetail(content, max(m.transcriptInnerW-8, 1))
	b.WriteString("      " + shared.Theme.Chat.TranscriptLabel.Render(label+":") + "\n")
	for _, row := range strings.Split(shown, "\n") {
		b.WriteString("      " + shared.Theme.Chat.TranscriptDetail.Render("│ "+row) + "\n")
	}
	if hidden > 0 {
		b.WriteString("      " + shared.Theme.Chat.Hint.Render(fmt.Sprintf("… %d lines hidden", hidden)) + "\n")
	}
}
```

Driven by `writeToolActivityDetails` (`:142-181`), which emits in fixed order:

1. `Locations` — `path:line` per line, when `activity.Locations` is non-empty
2. `Input` — `prettyToolInput(activity.Input)` (JSON-indented, else raw)
3. `Output` — `prettyToolInput(activity.Output)`
4. `Content` — one block per `content.Text` and per `content.Raw`
5. `Edit` — `"Adapter did not provide edit details."` when a completed edit
   produced none

Structured diffs short-circuit the whole function via `writeNewCodeCards`
(`:145-147`).

Rendered today:

```
  ▾ ◈ Edit · internal/tui/shared/styles.go
      Locations:
      │ internal/tui/shared/styles.go:120
      Input:
      │ {
      │   "file_path": "internal/tui/shared/styles.go"
      │ }
      … 14 lines hidden
```

### What is good and should survive

- **The section vocabulary is already right.** `Locations` / `Input` / `Output` /
  `Content` maps cleanly onto omp's labeled sections.
- **`boundTranscriptDetail`** (`monitor_transcript_detail.go:19`) already
  truncates by **visual wrapped lines at the render width**, not logical lines —
  the same discipline omp uses. It keeps head + tail with the middle elided
  (`transcriptDetailRows = 12`, `transcriptDetailTailRows = 3`,
  `transcriptDetailBytes = 4096`).
- **`prettyToolInput`** (`items_view.go:281`) JSON-indents when parseable and
  falls back to raw, returning `"(empty)"` for nothing.

### What is wrong

- The `      ` + `│ ` prefix is a second, competing frame inside a card.
- `TranscriptDetail` is a single style for all body content — no syntax
  highlighting, no per-section treatment.
- The `… %d lines hidden` wording is off-vocabulary (slice 06).
- Section order is fixed and unconditional; a `bash` call shows `Input` as raw
  JSON rather than as a command.

---

## omp reference

Sections are a first-class field of the card: `renderOutputBlock`'s
`sections: Array<{label?, lines, separator?}>`
(`packages/coding-agent/src/tui/output-block.ts:16`). A labeled section draws
`├─── Label ───┤`; an unlabeled one draws nothing unless it explicitly asks for a
rule and is not first (`:118-134`).

Labels observed across the codebase: `Output` (bash, code cells, eval),
`Details` (read image), `Response` (lsp), `Answer` / `Sources` / `Metadata`
(web search), `failed logs` (github run watch). Labels are styled by the caller —
usually `theme.fg("toolTitle", "Output")`.

`renderCodeCell` (`tui/code-cell.ts:195-201`) shows the canonical two-section
composition:

```ts
const sections = [{ lines: codeLines }];
if (outputLines.length > 0) {
    sections.push({ label: theme.fg("toolTitle", "Output"), lines: outputLines });
}
return renderOutputBlock({ header: title, headerMeta: meta, state, sections, width }, theme);
```

Producing:

```
╭─── ✎ Write: test/parse-sel.test.ts · 17 lines ───────────────╮
│   9   it("parses a single line range", () => {               │
│  10     expect(parseSel("42-58")).toEqual({                  │
├─── Output ───────────────────────────────────────────────────┤
│ wrote 17 lines                                               │
╰──────────────────────────────────────────────────────────────╯
```

### Content-aware bodies

omp's generic fallback (`tools/default-renderer.ts:96-141`) sniffs the output
before choosing a body:

- output starting with `{` or `[` → parse and render as a **JSON tree** with
  `├─`/`└─` connectors, depth-capped (2 collapsed / 6 expanded), line-capped
  (6 / 200), scalar-capped (60 / 2000 chars)
- otherwise → plain lines in the `toolOutput` color, capped at 4 collapsed / 12
  expanded

Budgets, `tools/render-utils.ts:88-108`:

```
COLLAPSED_LINES 3    EXPANDED_LINES 12    COLLAPSED_ITEMS 8
OUTPUT_COLLAPSED 3   OUTPUT_EXPANDED 10   COMPUTER_CODE_COLLAPSED 10
DIFF_COLLAPSED_HUNKS 8   DIFF_COLLAPSED_LINES 40
DEFAULT_TERMINAL_PREVIEW_LINES 10
```

### Flush bodies

Bodies that carry their own gutter pass `contentPaddingLeft: 0` so the gutter
sits flush against `│` (`edit/renderer.ts:955, 1129`). jig will need this for
slice 07's diffs and for numbered code cells.

---

## In Scope

- Convert `writeToolActivityDetails` to build `[]shared.CardSection` instead of
  writing indented strings.
- Drop the `      ` + `│ ` prefix entirely; the card's `│` is the frame.
- Keep the `Locations` / `Input` / `Output` / `Content` vocabulary and ordering.
- Route JSON input/output through the existing `fenceJSON` → `insetRenderer`
  path so Chroma highlights it, rather than `prettyToolInput`'s plain indent.
  jig already does exactly this for code cards
  (`renderNewCodeCard`, `items_view.go:205`) and for expanded tool content in
  the now-dead `writeCollapsible`. Reuse the mechanism.
- Give `bash`-kind activities a command-shaped body rather than a JSON `Input`
  dump — omp renders the command syntax-highlighted with a dim
  `$ cd <dir> && ` prefix (`tools/bash.ts:1628-1640`).
- Adopt slice 06's truncation wording in place of `… %d lines hidden`.
- Support the flush-body case (`PadLeft: 0`) for slice 07 to build on.

## Out of Scope

- Diff bodies — slice 07. This slice must leave the `writeNewCodeCards`
  short-circuit working, and should move it into a section rather than deleting
  it.
- Grouped-read bodies — slice 08.
- Inline argument previews on the *collapsed* row — slice 15.
- Per-tool bespoke bodies beyond bash's command line (epic Deferred Work).

## Functional Requirements

- **FR-05.1** An expanded tool exchange shall render its details as labeled
  sections inside its card, with no additional indentation prefix.
- **FR-05.2** The system shall preserve the section vocabulary and order:
  Locations, Input, Output, Content.
- **FR-05.3** A section shall be omitted entirely when its source data is empty;
  no empty labeled dividers.
- **FR-05.4** JSON input and output shall be syntax-highlighted, falling back to
  plain text when the payload is not valid JSON.
- **FR-05.5** A `bash`-kind activity shall render its command as a
  syntax-highlighted body rather than as a JSON object.
- **FR-05.6** Every section body shall remain bounded by `boundTranscriptDetail`
  at the card's content width, not the panel width.
- **FR-05.7** A completed edit whose adapter supplied no detail shall still
  produce the existing explanatory message.
- **FR-05.8** Collapsed exchanges shall render no sections at all.

## Technical and Repository Constraints

- **Width**: `boundTranscriptDetail` currently takes `m.transcriptInnerW-8`
  (`items_view.go:129`), hardcoding the old 6-space indent plus the `│ ` prefix.
  It must become `shared.CardContentWidth(...)` from slice 01. Leaving the `-8`
  will silently over-wrap inside a card.
- **glamour width**: `m.insetRenderer` is constructed with a wrap width reserving
  room for the old bar prefix (`monitor_layout.go:220` region). Rebuild it for
  the card's content width and invalidate `chatItemRendered` on
  `WindowSizeMsg` — per `CLAUDE.md`, glamour bakes wrap width in at
  construction.
- **Only prose through glamour.** Command output and tool results must not be
  reflowed as markdown. Fenced JSON is fine (it is a code block); raw text
  output is not. Keep `writeVerbatim` for the latter.
- **Caching**: section rendering is the expensive part. `chatItemRendered` is
  keyed by `transcriptRenderKey`; confirm it includes expansion state and width.
- Styles for section labels go on `Styles`; reuse
  `Theme.Chat.TranscriptLabel` if its weight is right.

## Security and Data Considerations

- Tool input and output are agent- and environment-controlled. They are already
  secret-redacted upstream (`internal/runner/agent.go`, `redactSecrets`) and
  size-clamped at write time (`internal/transcript/writer.go`), and
  `boundTranscriptDetail` clamps again at 4 KiB for display. Preserve **both**
  clamps; the display clamp is what protects layout from a pathological line.
- Rendering untrusted bytes into a framed card raises the ANSI-injection surface:
  an escape sequence inside tool output could break the frame. jig's
  `writeVerbatim` does not currently strip escapes. Consider stripping `\x1b`
  from non-highlighted section bodies, as omp's `sanitizeText` does.

## Acceptance Evidence

- A test rendering an expanded exchange with locations, JSON input, and text
  output, asserting three labeled dividers and no `│ ` prefix inside the body.
- A test that an activity with empty input emits no `Input` section.
- A test that a `bash` activity's body contains the command, not `{"command":`.
- A test that body wrapping uses the card content width (a long line at a known
  width wraps at the expected column).
- A test that a collapsed exchange emits exactly one row.

## Inputs for the Child Spec

- The section vocabulary already exists in `writeToolActivityDetails`; this is a
  re-hosting, not a redesign.
- The `-8` width constant at `items_view.go:129` is a known landmine.
- `writeNewCodeCards` must keep working; slice 07 will replace its internals.
- Slice 06's wording must be adopted here, not invented.

## Open Questions

- **Q-05.1** Should `Locations` remain a section, or move into the header meta
  slot? A single location is header-shaped; several are body-shaped. *Suggest:
  one location → meta, multiple → section.*
- **Q-05.2** Does `toolErrorHint` (slice 02, Q-02.3) become a leading error
  section here? *Leaning yes — it can be long, and a card body handles that
  better than a header.*
- **Q-05.3** Are the current budgets (12 rows, 3 tail rows, 4 KiB) right inside a
  card, which has less content width than the panel? *Not a blocker; tune with
  the prototype.*
