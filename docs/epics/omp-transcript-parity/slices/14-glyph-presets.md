# Slice 14 — Glyph presets and ASCII fallback

- **Slice ID:** `glyph-presets`
- **Outcome:** The TUI's glyph vocabulary becomes a selectable preset with a
  complete ASCII fallback, so the panel renders legibly on terminals without
  Unicode box-drawing or wide-glyph support.
- **Why this slice exists:** This epic adds a lot of glyphs — box corners, tees,
  dotted rules, tree connectors, spinner frames, status icons, brackets. Without
  a preset mechanism each one is a latent mojibake bug. CC-7 requires every
  earlier slice to route glyphs through a central vocabulary specifically so this
  slice can convert them all in one place.
- **Depends on:** None. (Every other slice depends on CC-7's discipline.)

---

## omp reference

`packages/coding-agent/src/modes/theme/symbols.ts` — 1425 lines, three complete
`SymbolMap`s keyed by the same ~290 `SymbolKey`s:

```
UNICODE_SYMBOLS  :367
NERD_SYMBOLS     :648
ASCII_SYMBOLS    :1096
```

### Preset selection is a setting, not a detection

`SymbolPreset = "unicode" | "nerd" | "ascii"` (`:5`), default `"unicode"`.
**There is no auto-detection.** Precedence (`loader.ts:177`):

1. user setting `symbolPreset`
2. theme JSON `symbols.preset`
3. fallback `"unicode"`

Themes may also override individual glyphs via `symbols.overrides`, merged in the
`Theme` constructor (`theme-class.ts:183-191`). Invalid override keys are ignored
and logged.

This is worth copying: font capability cannot be probed reliably, and guessing
wrong is worse than a setting.

### ASCII mode collapses features, not just glyphs

The important design point. ASCII is not a naive substitution table — whole UI
affordances disappear when they cannot be represented. All 43 `cmd.*`
slash-command icons are `""` in ASCII, and `getSlashCommandTypeIcon()` returns
`undefined` for the ascii preset so **the icon column is removed entirely**
(`tui-adapters.ts:267-271`). The source comment at `symbols.ts:1219` says as
much: *"unused; the icon column is disabled in ASCII mode."*

### The tables this epic needs

| Semantic | unicode | ascii |
|---|---|---|
| `status.success` | `✔` | `[ok]` |
| `status.error` | `✘` | `[!!]` |
| `status.warning` | `⚠` | `[!]` |
| `status.info` | `ⓘ` | `[i]` |
| `status.pending` | `⏳` | `[*]` |
| `status.running` | `⟳` | `[~]` |
| `status.done` | `•` | `*` |
| `status.enabled` | `●` | `[x]` |
| `boxRound` corners | `╭ ╮ ╰ ╯` | `+` |
| `boxSharp` tees | `├ ┤ ┬ ┴ ┼` | `+` |
| horizontal / vertical | `─` / `│` | `-` / `\|` |
| `boxDotted` | `┄` / `┆` | `-` / `:` |
| `tree.branch` / `.last` | `├─` / `└─` | `\|--` / `'--` |
| `tree.vertical` / `.hook` | `│` / `└` | `\|` / `` `- `` |
| `format.bracketLeft/Right` | `⟦` / `⟧` | `[` / `]` |
| `sep.dot` | `" · "` | `" - "` |
| `nav.expand` / `.collapse` | `▸` / `▾` | `+` / `-` |
| `md.quoteBorder` | `▏` | `\|` |
| spinner status | `⣾⣽⣻⢿⡿⣟⣯⣷` | `\| / - \` |
| `icon.input` / `output` | `⤵` / `⤴` | `in:` / `out:` |

Note ASCII forms are **not** all one cell — `[ok]` is four. That is deliberate:
legibility beats alignment when the alternative is a replacement glyph. Any
layout that assumes single-cell status icons must measure, not assume.

### A rail hierarchy worth preserving

omp uses three different vertical bars deliberately (`symbols.ts:566`):

- `▏` (U+258F, one-eighth block) — markdown blockquotes
- `▎` (U+258E, one-quarter block) — advisor notes, *"so they read as a distinct
  voice"*
- `▌` (U+258C, half block) — the `sep.block` separator

jig currently uses `▌` for both `BarThick` and `CursorBar`
(`internal/tui/shared/icons.go`). If slices 01 and 03 both want a bar, the
hierarchy is a ready-made answer.

---

## Current jig state

`internal/tui/shared/icons.go` is a flat const block of 22 entries:

```go
const (
	IconSuccess  = "✓"
	IconError    = "✗"
	IconPending  = "○"
	IconRunning  = "●"
	IconSkipped  = "—"
	IconReview   = "?"
	IconInput    = "⊙"
	IconValidate = "⇢"

	IconThinking   = "◇"
	IconToolCall   = "▸"
	IconToolResult = "↳"

	CollapsedMarker = "▸"
	ExpandedMarker  = "▾"

	BarThick     = "▌"
	CursorBar    = "▌"
	RuleGlyph    = "─"
	LoopGlyph    = "↺"
	RetryGlyph   = "↻"
	GateGlyph    = "⇢"
	ForEachGlyph = "×"

	ArrowDownGlyph = "▼"
	CondArrowGlyph = "▽"
	ArrowLeftGlyph = "◄"
)
```

The header comment already states the right intent:

> *"Icon vocabulary. Centralized (crush-style) so glyphs stay consistent and a
> single edit re-skins every call site."*

**The centralization is already there; only the indirection is missing.** There
is no ASCII path, no preset, and consumers reference the consts directly.

Additional glyphs are hardcoded at call sites elsewhere — `summarizeActivity`
alone contains `◈`, `⌕`, `$`, `↗`, `⊙`, `?`
(`monitor_transcript_items_view.go` / `monitor_tool_summary.go:38-80`), and
`writeItemDetail` hardcodes `│ ` (`items_view.go:133`). These need collecting.

---

## In Scope

- Convert `icons.go` from consts to a preset-indexed lookup, keeping the existing
  names as the accessor surface so call sites change minimally.
- Author a complete ASCII table for every key, including the glyphs this epic
  adds (box corners, tees, dotted rules, tree connectors, spinner frames,
  brackets).
- Collect the glyphs currently hardcoded at call sites — chiefly in
  `monitor_tool_summary.go` — into the vocabulary.
- A selection mechanism. jig has no user settings file for the TUI, so the
  realistic options are an environment variable or a `--ascii` flag. Follow the
  repo's configuration philosophy: `AGENTS.md` is emphatic that **backend
  selection is TOML-only, never env**. Glyph preset is a display concern rather
  than workflow semantics, so an env var may be acceptable — but the spec must
  make the case explicitly rather than assuming.
- Audit for width assumptions: multi-cell ASCII forms (`[ok]`) will break any
  layout that assumes a single-cell icon. Every consumer must use
  `lipgloss.Width`.

## Out of Scope

- A Nerd Font preset (epic Deferred Work). Two presets, not three.
- Per-theme glyph overrides. jig has one theme.
- Auto-detection of terminal font capability — omp deliberately does not, and
  neither should jig.
- Changing which glyph is used for what; this slice is mechanism, not design.

## Functional Requirements

- **FR-14.1** Every glyph rendered by the TUI shall be sourced from the icon
  vocabulary; no glyph shall be a string literal at a call site.
- **FR-14.2** The vocabulary shall provide a complete ASCII form for every key.
- **FR-14.3** The active preset shall be selectable by the operator.
- **FR-14.4** The default preset shall be Unicode.
- **FR-14.5** Under the ASCII preset the panel shall render with no Unicode
  characters outside the ASCII range.
- **FR-14.6** Layout shall remain correct when a preset's glyph occupies more
  than one cell.
- **FR-14.7** An affordance with no reasonable ASCII form shall be omitted rather
  than substituted with a misleading glyph.

## Technical and Repository Constraints

- Converting consts to a lookup changes them from compile-time constants to
  runtime values. Any use in a `const` block or array size elsewhere will break —
  grep first.
- FR-14.6 is the sharp edge. `[ok]` is 4 cells where `✓` is 1. Every place that
  pads or aligns around an icon must measure with `lipgloss.Width`. This is
  likely to surface latent bugs in existing code.
- Per CC-7, every earlier slice was required to route its glyphs centrally. If
  slices landed without doing so, this slice absorbs that debt — budget for it.
- The selection mechanism must not contradict `AGENTS.md`'s no-env policy without
  an explicit, argued exception recorded in the spec.
- A repository test that greps the TUI packages for non-ASCII string literals
  outside `icons.go` is the enforcement mechanism for FR-14.1.

## Security and Data Considerations

None identified.

## Acceptance Evidence

- A test asserting every key has a non-empty entry in both presets, or is
  explicitly marked as omitted under ASCII (FR-14.7).
- A test rendering a representative transcript under the ASCII preset and
  asserting every rune is < U+0080 (FR-14.5).
- A test asserting panel and card widths are exact under both presets, including
  a multi-cell ASCII icon (FR-14.6).
- The grep-based literal test (FR-14.1).
- Manual: run the TUI under `TERM=linux` with the ASCII preset.

## Inputs for the Child Spec

- `icons.go`'s existing header comment already states the design intent; this
  slice fulfills it.
- The width-assumption audit (FR-14.6) is the real work and will likely find
  existing bugs. Budget for it rather than treating this as a table-entry task.
- CC-7 means earlier slices should have left the vocabulary clean. Verify;
  do not assume.
- The env-var question needs an explicit decision against `AGENTS.md`, not a
  silent one.

## Open Questions

- **Q-14.1** How is the preset selected, given jig has no TUI settings file and
  `AGENTS.md` forbids env-based configuration for backend selection? Is a display
  preset a legitimate exception, or should it live in a future TUI config?
  *Needs an explicit decision; not blocking for the vocabulary refactor, which
  can land with Unicode hardcoded as the only preset initially.*
- **Q-14.2** Should the three-level rail hierarchy (`▏` / `▎` / `▌`) be adopted
  now that slices 01 and 03 both want a bar? *Suggest yes if both ship a bar;
  moot if slice 01's card border replaces them.*
- **Q-14.3** Does anything outside `internal/tui` render glyphs — headless
  output, `runexport`, the CLI? *Check; those surfaces may need ASCII
  unconditionally.*
