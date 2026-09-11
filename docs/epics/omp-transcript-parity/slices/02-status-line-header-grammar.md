# Slice 02 — Four-slot status-line header grammar

- **Slice ID:** `status-line-header-grammar`
- **Outcome:** Every tool exchange header is composed from one shared function
  with four independently-styled slots — icon, title, description, meta — and
  execution state is carried by the icon and border rather than by appended
  words.
- **Why this slice exists:** The card (slice 01) gives the header a place to
  live; this slice gives it a grammar. Separating them keeps the primitive free
  of tool semantics and lets the header rules be tested without a frame. Every
  later renderer (diffs, grouped reads, arg previews) composes its header through
  this function, so it must exist before them.
- **Depends on:** Slice 01.

---

## omp reference

`packages/coding-agent/src/tui/status-line.ts:32-54`, `renderStatusLine` — the
single header builder used by all 30 registered renderers.

```ts
let line = icon ? `${icon} ${title}` : title;
if (options.description) line += `: ${theme.fg("muted", flatten(options.description))}`;
if (options.badge)       line += ` ${theme.fg(color, bracketLeft + label + bracketRight)}`;
if (meta.length > 0)     line += ` ${theme.fg("dim", meta.join(theme.sep.dot))}`;
```

### The shape

```
<icon> <Title>: <description> ⟦badge⟧ <meta · meta · meta>
  │       │          │           │          └─ dim, joined by " · "
  │       │          │           └─ bracketed, status-colored
  │       │          └─ muted, introduced by ": "
  │       └─ accent (or `toolTitle`), the tool's name
  └─ status glyph, or the current spinner frame
```

Real headers observed in the source:

```
🔍 Grep: renderStatusLine  3 matches · 2 files · in src
⏳ Grep: renderStatusLine  case:insensitive
✎ Edit: src/tools/bash.ts ⟦+12/-3⟧
⣻ Edit: 3 more files pending…
💡 LSP references
```

### Rules worth copying exactly

- **`: ` introduces the description**, a bare space introduces the meta list.
  The two separators are different on purpose — the colon binds the description
  to the title, the space detaches the metadata.
- **Meta joins with `" · "`** (`sep.dot`, which ships pre-padded in omp's symbol
  table at `modes/theme/symbols.ts:433`). Empty/whitespace meta entries are
  filtered out before joining.
- **`flattenForHeader`** replaces every `\r\n?|\n` with a space so a header can
  never expand into two rows and break the enclosing box (`status-line.ts:29`).
  Tabs are deliberately left alone; renderers that need tab safety call
  `replaceTabs` themselves.
- **`iconOverride` beats `icon`** — used to swap the generic status glyph for a
  per-tool signature glyph on success.

### Status glyph vocabulary

`modes/theme/symbols.ts:369-379`, bound to colors in
`tools/render-utils.ts:247-270`:

| Status | Glyph | Color |
|---|---|---|
| success | `✔` | success |
| **done** | `•` | success |
| error | `✘` | error |
| warning | `⚠` | warning |
| info | `ⓘ` | accent |
| pending | `⏳` | muted |
| running | spinner frame, else `⟳` | accent |
| aborted | `⏹` | error |

**`•` (done), not `✔`, is the ordinary completed-tool icon.** `✔` carries
stronger "verified" semantics and is used sparingly. `renderCodeCell` maps
`status: "complete"` → `"done"` → `•` (`tui/code-cell.ts:64`).

### Per-tool signature glyphs on success

`modes/theme/symbols.ts:621-645`. On a settled successful call the generic status
icon is replaced by the tool's own mark:

`❯` bash · `✎` write/edit · `🗑` delete · `➜` move · `🔍` grep/glob · `⇶` task ·
`☑` todo · `💡` lsp · `⎇` github · `⌕` web search · `🧠` memory · `▶` eval ·
`🌐` browser · `🔌` mcp

### The anti-jitter rule (CC-4)

`task/render.ts:963-979`, in-source:

> *"Live (or queued) agents use the same dot finished rows keep… Finished rows
> keep the dot but settle from accent to the plain foreground: completion reads
> as a color change, not a new glyph."*

A long list therefore does not twitch as entries complete — only its colors
change. This is a deliberate choice, not an oversight.

---

## Current jig state

`internal/tui/monitor/monitor_transcript_items_view.go:76-101` builds the row
inline, and encodes state **as appended prose**:

```go
s := summarizeActivity(activity)
label := s.label
if item.toolUse == nil {
    label = shared.IconToolResult + " Result (unknown origin)"
}
if item.displayState == toolDisplayError {
    label += " failed"
} else if item.displayState == toolDisplayRunning {
    label += " · running"
} else if item.displayState == toolDisplayUnknownUse || item.displayState == toolDisplayUnknownResult {
    label += " · incomplete"
}
row := marker + " " + label
if s.preview != "" { row += " " + s.preview }
```

Then the **entire row** goes through one style — `TranscriptError`,
`TranscriptSelected`, or `TranscriptActivity`.

### What jig already has

`summarizeActivity` (`internal/tui/monitor/monitor_tool_summary.go:29`) is
genuinely close to omp's model. It already produces a three-part summary:

```go
type toolCallSummary struct {
	icon    string
	action  string
	detail  string
	label   string   // icon + " " + action
	preview string   // "· " + detail
}
```

with per-kind mapping at `:51-80`:

| kind | icon | action | detail |
|---|---|---|---|
| read/edit/write | `◈` | Read/Edit/Write | `shortFile(path)` |
| glob | `⌕` | Find | pattern |
| grep | `⌕` | Search | pattern |
| bash | `$` | Run | command |
| websearch | `↗` | Search web | query |
| webfetch | `↗` | Fetch | `shortHost(url)` |
| task | `⊙` | (subagent) | description |
| todowrite | `⊙` | Update | `N tasks` |
| askuserquestion | `?` | Ask | first question |
| skill | `⊙` | Use skill | skill name |

**Gap:** `action` and `detail` are concatenated into `label` and `preview`
strings and then styled as one unit. The information is already separated; only
the *rendering* fuses it.

### Glyph comparison

| Concept | jig (`shared/icons.go`) | omp |
|---|---|---|
| success | `✓` | `✔` / `•` |
| error | `✗` | `✘` |
| pending | `○` | `⏳` |
| running | `●` | spinner / `⟳` |
| tool call | `▸` | per-tool signature |
| tool result | `↳` | (merged into the call) |
| thinking | `◇` | `✻` pulse |
| file ops | `◈` (all of read/edit/write) | `✎` edit/write, `🗑` delete, `➜` move |

jig uses one `◈` for read, edit, and write; omp distinguishes them.

---

## In Scope

- A `shared.StatusLine` builder, e.g.:

  ```go
  type StatusLine struct {
      Icon        string          // pre-styled, or "" 
      Title       string
      TitleStyle  lipgloss.Style  // defaults to Theme.Chat.ToolTitle
      Description string
      Badge       string
      BadgeStyle  lipgloss.Style
      Meta        []string
  }

  func RenderStatusLine(s StatusLine) string
  ```

  with the separator rules above and newline flattening.
- A status-icon resolver mapping `toolDisplayState` → glyph + style, replacing
  the appended-prose approach at `items_view.go:84-90`.
- Extend `summarizeActivity` to expose `action` and `detail` to the caller
  without pre-fusing them (the struct already has the fields; stop building
  `label`/`preview` at the summary layer, or keep them and have the renderer
  ignore them).
- Add per-tool signature glyphs to the icon vocabulary and use them on settled
  success. Split `◈` into distinct read/edit/write marks.
- Add a `Meta` list to the tool exchange: at minimum, the item's error hint moves
  from the label into meta or badge, not the title.
- Add `Theme.Chat.ToolTitle`, `.ToolDescription`, `.ToolMeta`, `.ToolBadge*`
  styles.

## Out of Scope

- The `⟦+12/-3⟧` diff-stats badge — slice 07 supplies the numbers; this slice
  supplies the badge slot.
- The spinner frame in the icon slot — slice 13 supplies the ticker; this slice
  supplies the `Icon` field it writes into.
- Inline argument previews — slice 15.
- Changing the Steps panel indicators (`stepIndicator`). CC-4's no-glyph-change
  rule applies to the **Transcript** only; the Steps panel is out of scope per
  epic NG6.

## Functional Requirements

- **FR-02.1** The system shall compose tool headers exclusively through
  `RenderStatusLine`; no renderer shall concatenate a header by hand.
- **FR-02.2** The system shall introduce the description with `": "` and the meta
  list with a single space, and shall join meta entries with `" · "`.
- **FR-02.3** The system shall drop empty or whitespace-only meta entries before
  joining, so no header ends in a dangling separator.
- **FR-02.4** The system shall replace every CR and LF in the icon, title,
  description, badge, and meta fields with a space, so a header is always exactly
  one row.
- **FR-02.5** The system shall convey execution state through the status icon and
  its color, and shall not append `" failed"`, `" · running"`, or
  `" · incomplete"` to the title.
- **FR-02.6** The system shall render the title, description, and meta in three
  distinct styles.
- **FR-02.7** On a settled successful exchange with a known tool kind, the system
  shall render that kind's signature glyph in place of the generic status icon.
- **FR-02.8** When the tool kind is unknown, the system shall fall back to a
  generic icon and the tool's reported title, never to an empty header.
- **FR-02.9** An unmatched tool result (`toolUse == nil`) shall render with an
  explanatory title and a warning-state icon rather than a success icon.

## Technical and Repository Constraints

- All new styles are `Styles` fields set in `DefaultTheme()` from existing
  tokens (`CLAUDE.md`).
- Per CC-7, new glyphs must not be hardcoded at call sites; route them through
  `shared/icons.go` so slice 14 can convert the whole vocabulary to presets in
  one place.
- `summarizeActivity` already runs `sanitizeToolSummary` over icon/action/detail
  (`monitor_tool_summary.go:87-89`). Confirm it strips control characters;
  FR-02.4's newline flattening is additional, not a replacement.
- Header truncation is the **card's** job (`TruncateTitle`, slice 01), not this
  function's. `RenderStatusLine` returns an unbounded string; the card clips it.
  Document that boundary.
- Keep `toolErrorHint` (`items_view.go:243`) — it produces a useful first line of
  the failure. Move it from the title into the meta or badge slot.

## Security and Data Considerations

Tool descriptions and meta are derived from agent-controlled tool arguments. Two
concerns:

- **Control characters / ANSI injection.** A malicious or malformed tool argument
  containing escape sequences could corrupt the frame. `sanitizeToolSummary`
  exists for this; FR-02.4 extends it to newlines. Verify it also strips `\x1b`.
- **Path disclosure.** Descriptions carry file paths. These are already in the
  transcript and already redacted for secrets upstream; no new exposure. Prefer
  `shortFile` for display, as the existing summary does.

## Acceptance Evidence

- Table-driven tests for `RenderStatusLine` covering: icon present/absent,
  description present/absent, badge present/absent, meta with 0/1/N entries,
  empty-string meta filtering, and embedded `\n` in every field.
- A test asserting that no rendered tool row contains the literal substrings
  `" failed"`, `" · running"`, or `" · incomplete"` (FR-02.5).
- A test that a settled successful `edit` exchange renders the edit signature
  glyph, not the generic tool glyph.
- Visual: a screenshot where the tool name, its argument, and its metadata are
  in three visibly different weights.

## Inputs for the Child Spec

- `summarizeActivity` is the existing seam and is already structured correctly.
  Reuse it; do not write a parallel summarizer.
- CC-4 (color, not glyph, for state transitions) applies here and constrains
  FR-02.7: the signature glyph swap happens **once**, on settling — running and
  pending both use the pending/spinner glyph, and the running→success transition
  is the only glyph change permitted.
- The badge slot is populated by slice 07; ship it empty but present.
- The icon slot is written by slice 13; ship it accepting a caller-supplied
  pre-rendered glyph.

## Open Questions

- **Q-02.1** Should `Title` default to accent (omp's default) or to `toolTitle`
  (plain foreground, which most omp renderers actually pass)? Accent on every
  tool row may be too loud in a dense transcript. *Suggest plain foreground with
  accent reserved for running; decide with a prototype.*
- **Q-02.2** Does jig want emoji signature glyphs (`🔍`, `💡`, `🧠`)? They are
  double-width and vary by terminal font. *Suggest geometric alternatives from
  the existing Charmtone-adjacent set; slice 14 will need ASCII forms regardless.*
- **Q-02.3** Where does `toolErrorHint` belong — meta, badge, or a first body
  line inside the card? *Leaning body line, since it can be long; resolve with
  slice 05.*
