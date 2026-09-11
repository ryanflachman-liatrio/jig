# Slice 01 — The transcript card primitive

- **Slice ID:** `transcript-card-primitive`
- **Outcome:** A reusable `shared.RenderCard(...)` primitive renders a rounded,
  full-width box with an inline header label, optional labeled section dividers,
  a state-driven border color, and an optional state background tint — and one
  transcript item kind (the tool exchange) is drawn with it.
- **Why this slice exists:** This is the single change that makes the panel look
  like omp. Everything downstream (tool sections, diffs, grouped reads) renders
  *into* a card. It is separated from those consumers because the primitive
  itself carries the epic's one blocking unknown (CC-9, background tinting under
  glamour output) and deserves to be proven alone.
- **Depends on:** Slice 00.

---

## omp reference

`packages/coding-agent/src/tui/output-block.ts:66-200`, `renderOutputBlock`.

### Anatomy

```
╭─── ✎ Edit: src/tools/bash.ts ⟦+12/-3⟧ ──────────────────────╮
│ 118│  s.Chat.Hint = lipgloss.NewStyle()…                     │
│-119│  …Foreground(primary)                                   │
│+119│  …Foreground(accent)                                    │
├─── Output ──────────────────────────────────────────────────┤
│ wrote 3 lines                                                │
╰──────────────────────────────────────────────────────────────╯
```

### Exact construction rules

| Element | Rule | omp ref |
|---|---|---|
| Cap | `cap = horizontal.repeat(3)` — the corner is always followed by **exactly three** horizontals before the label. | `:70` |
| Labeled bar | `╭` + `───` + `" " + label + " "` + `─`×fill + `╮`. Fill runs flush to the corner; **no gap**. | `:165-174` |
| Unlabeled bar | `╭───` + solid fill + `╮`, described in-source as "a clean, continuous top/separator bar (no 1-col gap)". | `:159-163` |
| Section divider | `├───` + `" Label "` + fill + `┤`. Drawn when the section has a label, **or** when `separator: true` **and** `sectionIndex > 0` — "leading with one would just double the header bar". | `:118-134` |
| Label color | The label is **not** wrapped in the border color. Only the left glyphs, fill, and right corner pass through `border()`. The label keeps whatever colors the caller baked in. | `:155-174` |
| Content row | `│` + leftPad + line padded to `contentWidth` + rightPad + `│`. | `:181-182` |
| Content width | `max(1, w - 2 - padL - padR)`; defaults `padL = padR = 1`, so **`w - 4`**. | `:56-64` |
| Wrapping | ANSI-aware wrap of `line.trimEnd()` to `contentWidth`; never truncate. | `:137-143` |
| Bottom | `╰───` + fill + `╯`. | `:176-180` |
| Final pass | Every row padded to full width with the tint applied, so the fill reaches edge to edge. | `:196` |

### Rounded boxes have sharp junctions

There is no rounded tee in Unicode. omp's `theme.boxRound.teeRight/teeLeft/cross`
deliberately alias the **sharp** `├ ┤ ┼` (`modes/theme/theme-class.ts:507-523`),
documented in omp's own `docs/theme.md`. A rounded frame's dividers are `├───┤`.

### Border color by state — success recedes

`output-block.ts:73-81`:

```ts
const borderColor =
    options.borderColor ??
    (state === "error"   ? "error"
   : state === "warning" ? "warning"
   : state === "running" || state === "pending" ? "accent"
   : "dim");
```

This is CC-3. A finished successful tool has a **dim** border; accent is reserved
for work in flight. Several renderers go further and override to `borderMuted` on
success — described in-source as the "muted 'legacy' tool frames that should not
visually compete with framed-output tools".

### Background tint by state

`tui/utils.ts:99-103`:

```
success → toolSuccessBg
error   → toolErrorBg
else    → toolPendingBg
```

Values from omp's `titanium` theme against its `#151820` page:

| Token | Value | Character |
|---|---|---|
| `toolPendingBg` | `#0f1216` | darker well |
| `toolSuccessBg` | `#0f1216` | same darker well |
| `toolErrorBg` | `#1a0f10` | faint red-black |

The tint is a **recessed well** — a 4–12 point luminance shift. A whisper, not a
panel.

### The SGR-reset hazard (CC-9 — the blocking unknown)

`output-block.ts:83-94`:

```ts
const bgFn = (text: string) => {
    const stabilized = text
        .replace(/\x1b\[(?:0)?m/g, m => `${m}${bgAnsi}`)
        .replace(/\x1b\[49m/g,     m => `${m}${bgAnsi}`);
    return `${bgAnsi}${stabilized}\x1b[49m`;
};
```

In-source rationale: *"Keep block background stable even if inner content
contains SGR resets (e.g. `\x1b[0m`), which would otherwise clear the outer
background mid-line."*

This matters for jig because glamour and Chroma emit `\x1b[0m` liberally inside
rendered code fences. A naive lipgloss `.Background()` around that content shows
holes. omp's broader discipline is that it **never emits `\x1b[0m` inside content
at all** — only `\x1b[39m`, `\x1b[49m`, `\x1b[22m`, `\x1b[23m`, `\x1b[27m`. jig
cannot impose that on glamour, so a stabilization pass is the available option.

---

## Current jig state

There is no card. `itemTranscriptBody`
(`internal/tui/monitor/monitor_transcript_items_view.go:16`) emits flat rows
(`:91-101`):

```go
row := marker + " " + label
if s.preview != "" { row += " " + s.preview }
if item.displayState == toolDisplayError {
    row += " · " + toolErrorHint(m, item)
    b.WriteString(prefix + shared.Theme.Chat.TranscriptError.Render(row) + "\n")
} else if selected {
    b.WriteString(prefix + shared.Theme.Chat.TranscriptSelected.Render(row) + "\n")
} else {
    b.WriteString(prefix + shared.Theme.Chat.TranscriptActivity.Render(row) + "\n")
}
```

`TranscriptActivity` is plain `fgBase` (`internal/tui/shared/styles.go:277`), so
every finished tool call is exactly as loud as every other.

### jig already has most of the chrome

`internal/tui/shared/panel.go` is a manual border-title compositor, added under
`docs/adr/0001-manual-border-title-compositing.md` precisely because lipgloss v2
has no border-title API:

- `PanelTopEdge(title string, width int, border lipgloss.Style) string` — builds
  `╭─ Title ─────╮` at exactly `width` visible cells (`panel.go:158`)
- `PanelTitleBudget(width int) int` (`:151`)
- `TruncateTitle(s string, max int) string`, width-aware (`:193`)
- `PanelFrame()` (`:214`)

The gaps versus omp are small: a 1-dash cap instead of 3, no `├───┤` divider
variant, and no state-driven color argument.

---

## In Scope

- A new primitive in `internal/tui/shared` (suggested `card.go`), roughly:

  ```go
  type CardState int // CardPending, CardRunning, CardSuccess, CardWarning, CardError

  type CardSection struct {
      Label string   // "" = unlabeled
      Lines []string // already styled; not yet wrapped or padded
      Rule  bool     // draw a divider even when unlabeled (ignored at index 0)
  }

  type Card struct {
      Header      string // pre-styled; caller owns its colors
      HeaderMeta  string // joined to Header with " · "
      Sections    []CardSection
      State       CardState
      Width       int
      PadLeft     int  // default 1; 0 for bodies with their own gutter
      PadRight    int  // defaults to PadLeft
      Tint        bool
      BorderMuted bool // force the recessed success look
  }

  func RenderCard(c Card) string
  func CardContentWidth(width, padLeft, padRight int) int
  ```

- Extend or generalize `PanelTopEdge` to accept the cap width and tee glyph pair
  so the card and the panel share one implementation (CC-6). Do **not** write a
  second border compositor.
- Add a `Card` sub-struct to `Styles` in `shared/styles.go`: `BorderPending`,
  `BorderRunning`, `BorderSuccess`, `BorderWarning`, `BorderError`,
  `BorderMuted`, `TintNeutral`, `TintError`. Derive each from existing tokens.
- Add two background tokens to `shared/palette.go`. Charmtone has nothing darker
  than `hexPepper` (`#201F26`), so this slice **introduces** jig-local values — a
  recessed well (e.g. `#1A191F`) and a faint error well (e.g. `#2A1A1E`). Name
  them in the file's existing style and comment that they are jig additions, not
  upstream Charmtone.
- Resolve or explicitly abandon CC-9, with a test that renders a card containing
  glamour output and asserts the background escape survives on every line.
- Convert **one** consumer: the tool-exchange branch of `itemTranscriptBody`.

## Out of Scope

- The header's internal grammar — slice 02.
- Moving `writeItemDetail` bodies into sections — slice 05.
- Diff bodies — slice 07.
- Text, system, thinking, and unsupported item kinds — each keeps its current
  rendering until its own slice.

## Functional Requirements

- **FR-01.1** The system shall render every card row at exactly `Width` visible
  cells, verified with `lipgloss.Width`.
- **FR-01.2** The system shall place the header label inside the top border after
  a three-glyph cap and a single space, and shall not apply the border style to
  the label.
- **FR-01.3** The system shall select border color from state: error→danger,
  warning→warning, running/pending→primary, success→dim.
- **FR-01.4** The system shall draw labeled section dividers with the sharp tees
  `├`/`┤`, and shall suppress an unlabeled divider at section index 0.
- **FR-01.5** The system shall truncate an over-long header via `TruncateTitle`
  and shall never allow the header to occupy more than one row.
- **FR-01.6** The system shall wrap content to `CardContentWidth` in an
  ANSI-aware manner without truncating.
- **FR-01.7** When `Tint` is set, the system shall apply the state background
  across the full row width **including** rows whose content contains nested SGR
  reset sequences.
- **FR-01.8** The system shall render correctly down to `Width` 40, degrading the
  header label rather than breaking the frame.
- **FR-01.9** With `RunDir == ""` the transcript shall continue to render its
  empty state without entering the card path.

## Technical and Repository Constraints

- **Styles:** each new style is a `Styles` field set in `DefaultTheme()`. No
  package-level `var`; no hardcoded hex on a style — hex lives only in
  `palette.go`. (`CLAUDE.md`, "TUI styling".)
- **Width math:** `lipgloss.Width` throughout; never `len()`.
- **Caching:** cards cost more per line than flat rows. Reuse
  `m.chatItemRendered` (`transcriptRenderKey`) and confirm the key covers width
  and expansion. For reference, omp hashes width, both paddings, header, meta,
  state, border color, tint flag, and every section line
  (`output-block.ts:220-256`).
- **Nesting:** the Transcript panel is already bordered; a card costs 4 more
  columns of content width. Prototype at realistic widths before committing.
- **Line accounting:** `itemTranscriptBody` derives `chatItemLineRanges` from
  `strings.Count` over the item's emitted bytes. The mechanism is height-agnostic
  and should hold, but must be covered by a test.
- **glamour width:** if card bodies use `m.insetRenderer`, its wrap width must
  account for the card frame and be rebuilt on `WindowSizeMsg`.

## Security and Data Considerations

None identified. Presentation only. Transcript content is already redacted
upstream in `internal/runner/agent.go` (`redactSecrets`) before it is written.

## Acceptance Evidence

- `internal/tui/shared` unit tests: exact width at several widths; cap and label
  spacing; sharp tees; divider suppression at index 0; label truncation; and the
  nested-reset tint case (FR-01.7).
- An `internal/tui/monitor` test asserting the tool-exchange item renders as a
  card.
- A screenshot showing a failed tool call with a red border beside a succeeded
  one with a dim border.
- `go test ./... && gofmt -l -w . && go vet ./...` clean.

## Inputs for the Child Spec

- CC-9 is **blocking**. Resolve it before designing the tint API. If lipgloss
  cannot hold a background across nested resets without post-processing, the
  documented fallback is border-only state signaling — which still satisfies G1
  and G2. State that outcome explicitly rather than shipping holes.
- CC-6: extend `shared/panel.go`. ADR 0001 governs.
- The palette additions are the first jig-local colors outside upstream
  Charmtone; document that inline.
- Convert exactly one consumer.

## Open Questions

- **Q-01.1 (blocking, CC-9)** Can a lipgloss `.Background()` survive nested
  `\x1b[0m` from glamour/Chroma, or is post-processing required?
- **Q-01.2** At jig's realistic `transcriptInnerW`, does a bordered card inside a
  bordered panel read as cluttered? If so, does a borderless tinted block (omp
  has this variant) deliver G1/G2 adequately? *Resolve with a prototype; may
  change the primitive's default.*
- **Q-01.3** Should the card degrade to a flat row below some minimum width?
  *Defer until Q-01.2 is answered.*
- **Q-01.4** Do the `Theme.Chat.Bar*` styles have any consumer left after slice
  00? *Mechanical; resolve by grep.*
