# Implementation Plan: OMP parity slice 14 — glyph presets and ASCII fallback

**Status:** Planned — omp-transcript-parity epic, slice
[`14-glyph-presets`](../epics/omp-transcript-parity/slices/14-glyph-presets.md)
**Risk:** **medium** — the mechanism (a preset-indexed vocabulary and a
selection switch) is small; the migration debt CC-7 was supposed to prevent
is not. Every previous slice was required to route new glyphs through the
central vocabulary, but a handful of literals survive at high-traffic sites
(`internal/tui/shared/card.go` box drawing, `internal/tui/shared/panel.go`
title fill, `internal/tui/monitor/monitor_diff_render.go` gutter,
`internal/tui/monitor/monitor_transcript_items_view.go` detail rail,
`internal/tui/monitor/monitor_transcript.go` review overview marks,
`internal/tui/detail/view.go` chart marker text, and the whole of
`internal/tui/review/view.go` presentation), and this slice absorbs them.
The sharpest technical hazard is **FR-14.6**: ASCII forms are not all
single-cell (`[ok]` is 4 where `✔` is 1), so any renderer that pads or
aligns around an icon by rune count rather than `lipgloss.Width` breaks
silently under the ASCII preset. Neither `internal/runexport` nor
`internal/headless` emits any of these glyphs today (verified), so the
scope stays inside `internal/tui`.
**Depends on:** None in the epic dependency graph; the slice is
independent by design so it can absorb the CC-7 debt without waiting on
other work. In practice this plan consumes the CC-7 groundwork already
landed by
[slice 02](../specs/25-spec-status-line-header-grammar/25-spec-status-line-header-grammar.md)
(`IconStatus*`, `IconTool*`, `shared.ToolStatusIcon`),
[slice 03](../specs/25-spec-selection-affordance/25-spec-selection-affordance.md)
(`shared.CursorBar` at every transcript selection site),
[slice 06](../specs/25-spec-truncation-vocabulary/25-spec-truncation-vocabulary.md)
(`MoreItems`, `EarlierItems`, `ExpandHint`, `HintLine`, and the
`Capture/Diff*Hint` family — already plain ASCII),
[slice 12](../specs/25-spec-boundary-banners) (`shared.Rule` and the
`monitor_transcript_banner.go` grep test that already forbids a bare
`─` there), and
[slice 13](../specs/25-spec-liveness-and-spinners) (`SpinnerFrames.ASCII`
is populated and width-checked). Two later slices have inputs recorded
for slice 14: slice 10's thinking pulse ASCII fallback and slice 15's
inline argument bracket vocabulary; both are consumers, not blockers.
**Complements:** epic goals G7 ("the panel degrades cleanly under
`TERM=linux` / no-Nerd-Font terminals via an explicit glyph preset, not
mojibake") and EC-8 ("the panel renders legibly with the ASCII glyph
preset active"). Establishes the vocabulary substitutions slice 10 and
slice 15 will register at implementation time.
**Breaks:** the `IconSuccess`, `IconError`, `IconPending`, `IconRunning`,
`IconSkipped`, `IconReview`, `IconInput`, `IconValidate`, `IconThinking`,
`IconToolCall`, `IconToolResult`, `IconStatus*`, `IconTool*`,
`CollapsedMarker`, `ExpandedMarker`, `TreeBranchGlyph`, `TreeLastGlyph`,
`TreeContinueGlyph`, `CursorBar`, `RuleGlyph`, `LoopGlyph`, `RetryGlyph`,
`GateGlyph`, `ForEachGlyph`, `ArrowDownGlyph`, `CondArrowGlyph`, and
`ArrowLeftGlyph` identifiers in `internal/tui/shared/icons.go` change
from compile-time `const` to package-level `var` so their value can be
substituted at process start. No callers today embed them in a `const`
block or a fixed-size array literal (verified by grep), so the surface
change is name-preserving. `BarThick` has no callers (only its
declaration) and is deleted in the same change per pre-v1 policy.
`internal/tui/chart/render.go`'s `[]rune(shared.ArrowDownGlyph)[0]`
idiom is single-cell-only by construction; the ASCII forms for chart
grid glyphs must remain single-cell (spec'd in the vocabulary table)
so the grid does not overflow, and the plan carries that constraint
into the ASCII table rather than into the chart renderer.

---

## Summary

Turn the flat const vocabulary in `internal/tui/shared/icons.go` into a
preset-indexed lookup with a complete `unicode` table (verbatim of
today's values) and a complete `ascii` table, promote every remaining
bare-glyph literal at a renderer call site into a vocabulary key, add
one operator-facing selection mechanism (a `--ascii` global flag on the
`jig` binary, no environment variable), and lock the retirement with a
repository grep test that forbids new bare-glyph literals in
`internal/tui/**` outside `icons.go` and `spinner.go`. The mechanism
change is one file plus a handful of call-site replacements; the real
work is the FR-14.6 width audit — every place that pads or aligns
around an icon must measure with `lipgloss.Width` rather than assuming
one cell, because ASCII forms are deliberately not all single-cell.

## Approach

Add `internal/tui/shared/symbols.go` with `type SymbolPreset int` (only
`PresetUnicode` and `PresetASCII`; the Nerd Font preset is Deferred
Work), a `symbolTable` struct carrying one string field per vocabulary
key, two package-level tables (`unicodeSymbols` and `asciiSymbols`), a
private `activeSymbols` pointer defaulting to `unicodeSymbols`, and one
public `SetPreset(p SymbolPreset)` entry point that swaps the pointer
and re-populates every exported vocabulary `var` in `icons.go` so
existing call sites (`shared.IconSuccess`, `shared.CursorBar`, ...) keep
their names and their read-once-per-render semantics. Extend the
vocabulary with **new** keys for glyphs currently hardcoded at call
sites (`BoxCornerTL`, `BoxCornerTR`, `BoxCornerBL`, `BoxCornerBR`,
`BoxTeeL`, `BoxTeeR`, `BoxVertical`, `DiffGutter`, `EllipsisGlyph`;
the horizontal fill glyph is already `shared.RuleGlyph`, and the
chart's cell-single arrow forms are already in `icons.go`). Migrate `shared/card.go`, `shared/panel.go`,
`monitor/monitor_diff_render.go`, `monitor/monitor_transcript.go` (the
review overview marks), `monitor/monitor_transcript_items_view.go` (the
`│ ` detail rail), `detail/view.go` (the `↺ route→` / `⇢ gate` marker
text — already covered by `LoopGlyph` and `GateGlyph`), and
`review/view.go` (`○`, `✓`, `●`, `▌`, `│`, `─`, `…`) to the vocabulary.
Add a repository test that greps every `.go` file under `internal/tui`
outside `icons.go` / `spinner.go` and rejects non-ASCII glyph literals
that appear in the vocabulary. Wire selection with one flag: `jig
--ascii` (and the same flag on `jig run` for headless invocations that
render terminal-facing hints, chiefly the `--ci` expansion warning) sets
`shared.SetPreset(PresetASCII)` before the TUI starts; no environment
variable is added.

## Problem

The Transcript panel's glyph vocabulary lives at
`internal/tui/shared/icons.go`, already centralized in the crush-style
per its own header comment: *"Icon vocabulary. Centralized (crush-style)
so glyphs stay consistent and a single edit re-skins every call site."*
Reading the file makes the design intent clear — the constants exist so
one edit can retheme every consumer — but there is no ASCII path, no
preset selector, and no runtime substitution. A terminal that cannot
render `⣾` gets a replacement character; a terminal that renders
`◈`, `⌕`, or `⤵` as emoji-presentation double-width breaks the panel's
column arithmetic silently.

Meanwhile the omp reference `packages/coding-agent/src/modes/theme/
symbols.ts` publishes three complete `SymbolMap`s keyed by the same
~290 `SymbolKey`s, with an explicit lesson buried in the source: **ASCII
is not a naive substitution table.** Whole affordances disappear when
they cannot be represented (the entire slash-command icon column is
dropped in the ASCII preset; the `getSlashCommandTypeIcon` accessor
returns `undefined` for `"ascii"` so the callers know to elide). Some
forms are not one cell (`[ok]` is 4). And font capability is not
probed — omp deliberately does not detect, and guessing wrong is worse
than a setting. Slice 14's brief is to bring exactly those three lessons
into jig without importing the parts that do not fit (no Nerd Font
preset, no theme-JSON overrides, no auto-detection).

The forcing function is CC-7: **every earlier slice was required to
route its glyphs centrally.** Slices 02, 03, 06, 12, and 13 did (the
`IconStatus*`, `IconTool*`, `shared.CursorBar`, the truncation
vocabulary, `shared.Rule`, and `spinner.SpinnerFrames.ASCII`); each one
either landed a repository grep test at its own file (slice 12's
`monitor_transcript_banner.go` literal rule check, slice 03's
`monitor_transcript_items_view.go` `▌` check, slice 02's
`renderer_discipline_test.go`) or extended the vocabulary. The
remaining literals sit in the pre-CC-7 code: `internal/tui/shared/
card.go` composes borders from `"╭"`, `"╮"`, `"├"`, `"┤"`, `"╰"`,
`"╯"`, `"│"`; `internal/tui/shared/panel.go` writes `"─"` twice inside
`composeBorderBar`; `internal/tui/monitor/monitor_diff_render.go`
carries the gutter's `"│"` at two positions; `internal/tui/monitor/
monitor_transcript_items_view.go` prints `"│ "` in `writeItemDetail`;
`internal/tui/monitor/monitor_transcript.go` uses `"○"` and `"✓"` in
the review overview; `internal/tui/detail/view.go` composes `"↺
route→"` and `"⇢ gate"` marker strings by hand; and every rail /
gutter / mark inside `internal/tui/review/view.go` is a bare literal.
None of these are wrong today; each becomes a mojibake bug the moment
the ASCII preset is enabled.

Consequence today: the panel is legible on terminals that render the
existing Unicode vocabulary correctly and silently broken on those that
do not. The mechanism to close the gap is small (one preset switch, one
lookup table); the migration debt is where the work sits.

---

## Q-14.3 audit — glyph render sites outside the vocabulary

The slice document asks whether anything outside `internal/tui` renders
glyphs; the audit answers no. The table below inventories every glyph
literal in `internal/tui/**` today so the migration list is unambiguous.
Every row is either a **vocabulary key already exists** (migrate the
call site) or **a new key** (add to `icons.go` in this slice), with one
row marked **exempt** (per NG6 the Steps panel state indicator, which
must keep its `○ → ● → ✓` transition and is not part of the epic).

| Site | Glyph | Where | Resolution |
|---|---|---|---|
| Card corners | `╭ ╮ ╰ ╯` | `internal/tui/shared/card.go:183, 202`; `card.borderStyle` composes them via `composeBorderBar` | New keys `BoxCornerTL/TR/BL/BR`; substitute in the two call sites. |
| Card tees | `├ ┤` | `internal/tui/shared/card.go:188` | New keys `BoxTeeL/R`; substitute. |
| Card vertical | `│` | `internal/tui/shared/card.go:196` | New key `BoxVertical`; substitute. |
| Panel/card horizontal | `─` | `internal/tui/shared/panel.go:178, 191`; already exposed as `shared.RuleGlyph` | Substitute the two literals with `RuleGlyph`. |
| Diff gutter | `│` | `internal/tui/monitor/monitor_diff_render.go:178, 556` | New key `DiffGutter` (kept distinct from `BoxVertical` so slice 07's fused gutter can theme independently if it must); substitute both call sites. |
| Detail rail | `│ ` | `internal/tui/monitor/monitor_transcript_items_view.go:382` in `writeItemDetail` | Use `BoxVertical + " "`; substitute. |
| Review overview marks | `○ ✓` | `internal/tui/monitor/monitor_transcript.go:524, 528, 533` in `writeReviewOverview` | Use `IconPending` and `IconSuccess`; substitute. |
| Chart marker text | `↺ route→…`, `⇢ gate` | `internal/tui/detail/view.go:144, 151` | Route through `LoopGlyph` and `GateGlyph`; substitute. |
| Review workspace glyphs | `○ ✓ ● ▌ │ ─ …` | `internal/tui/review/view.go:35, 48, 126, 128, 132, 144, 330, 334, 458, 460, 462, 548, 551, 561, 568, 676, 694` | Migrate to `IconPending`, `IconSuccess`, `IconRunning` / a new `CommentMarker`, `CursorBar`, `BoxVertical`, `RuleGlyph`, `EllipsisGlyph`. The epic NG6 exempts *reworking* the review workspace, not migrating its glyph literals; the migration keeps the render byte-identical under the Unicode preset. |
| Home selection | `▌` | `internal/tui/runs/view.go:109` | Already routes through `shared.CursorBar`. No change. |
| Home ellipsis | `…` | `internal/tui/runs/view.go:149` | Use new key `EllipsisGlyph`; substitute. |
| Panel title `…` | `…` | `internal/tui/shared/panel.go:203` (`TruncateTitle`, `ansi.Truncate(..., "…")`) | Use `EllipsisGlyph`; substitute. |
| Truncation vocabulary `…` | `…` | `internal/tui/shared/truncation.go:26, 32` | Use `EllipsisGlyph`; substitute the two literal `"… "` prefixes. |
| Steps panel state indicators | `○ ● ✓ ✗ — ⇢ ⊙` | `internal/tui/monitor/monitor_steps.go:330–344` `stepIndicator` | **Exempt** per NG6 — Steps stays with its per-state glyph transition. `stepIndicator` gains a preset-aware pair only if a future non-Steps consumer needs it; not in this slice. |
| Steps panel structural | `▸`, `×N` | `monitor_steps.go:260, 107` | Route through `CollapsedMarker` / `ForEachGlyph` (already exist); substitute. |
| Chart cell arrows | `▼ ▽ ◄` | `internal/tui/chart/render.go:304, 306, 324` used as `[]rune(x)[0]` | Already central via `ArrowDownGlyph` / `CondArrowGlyph` / `ArrowLeftGlyph`. **ASCII forms must remain single-rune** (`v`, `V`, `<`) so the chart grid does not overflow; the constraint lives in the vocabulary table below. |
| Chart marker suffixes | `↻ N` etc. | `chart/render.go:396–408` | Already central via `LoopGlyph` / `RetryGlyph` / `GateGlyph` / `ForEachGlyph`. Under ASCII these become multi-rune (`->`, `<-`), which appear in text spans, not the grid; safe. |
| Spinner status set | `⣾⣽⣻⢿⡿⣟⣯⣷` / `\| / - \` | `internal/tui/shared/spinner.go:74` | Already preset-indexed via `SpinnerFrames.Unicode/ASCII`; slice 14 flips the `spinnerASCIIPreset()` predicate to consult the vocabulary preset. One-line change. |
| `internal/runexport/*` | — | grep returns no glyph literals | **N/A.** Export bundles carry no rendered glyphs; the exported transcript is JSONL truth. |
| `internal/headless/*` | — | grep returns no glyph literals | **N/A.** Headless output is plain text; a `--ci` warning line uses `strings.Contains` for `--ci` but no drawing glyphs. |
| `cmd/jig/*` | — | grep returns no glyph literals | **N/A.** |

Two follow-ups the audit surfaces but slice 14 does not resolve:

1. **Steps panel `stepIndicator` under ASCII.** NG6 excludes the Steps
   panel from the epic. The current glyphs render as one cell in both
   presets; when ASCII is active they will render `[o] [x] [!] [~]` or
   similar. Slice 14 leaves the panel on its current Unicode glyphs by
   default; a follow-up (post-epic) can extend the vocabulary to cover
   the Steps panel when a Steps-focused slice is authored.
2. **Gate panel glyphs.** Same reasoning as Steps; `monitor_gate*.go`
   uses no vocabulary that this slice reroutes.

---

## Open-question resolutions

Each is defaulted so implementation can proceed; each records the
rationale so a reviewer can overturn it before code lands.

- **Q-14.1** (preset selection) — Ship a **`--ascii` global flag on
  `jig` and `jig run`; no environment variable, no config file.** The
  flag is checked once at process start (before the TUI initializes)
  and passed to `shared.SetPreset(PresetASCII)`; the default is
  `PresetUnicode` (FR-14.4). Explicit argument against an env var: the
  repository's non-negotiable design constraint is "Do not add
  compatibility wrappers or environment aliases speculatively"
  (`AGENTS.md:37`), and `AGENTS.md:60-62` extends that to backend
  selection with the strict rule "Do not use or reintroduce
  `JIG_HARNESS` or `harness.FromEnv`." A glyph preset is a display
  concern, not a workflow semantic, so the rule does not literally
  forbid it — but the *pattern* it forbids is exactly "let one process
  behave differently because of an environment variable set outside the
  invocation." A CLI flag lives at the invocation boundary, matches
  the pattern of `jig run --quiet` and `jig run --ci`, and reads
  identically on-screen and in shell history. `TERM` inspection is
  explicitly not added: the epic's G7 wording ("degrades cleanly under
  `TERM=linux`") is an *outcome* the operator gets by setting the
  flag; the omp precedent at `symbols.ts:36` (*"font capability cannot
  be probed reliably, and guessing wrong is worse than a setting"*)
  is the reason to leave `TERM` alone. If a future spec argues for
  `NO_COLOR`-style ambient behavior, that spec owns the exception.
- **Q-14.2** (three-level rail hierarchy `▏` / `▎` / `▌`) — **Moot;
  delete `BarThick` and keep `CursorBar`.** A grep of `internal/**`
  and `cmd/**` returns zero call sites for `shared.BarThick`; it is
  a bare declaration in `icons.go`. Its only historical purpose was
  the omp accent rail on chat blocks, which slice 01's card border
  replaced. Removal in this slice is a pre-v1 cleanup that keeps the
  vocabulary honest. Adopting the three-level hierarchy would need
  two consumers (an advisor-note style and a `sep.block` rule) that
  jig does not have; if a later slice needs a distinct rail weight,
  it adds `RailNarrow` and `RailQuarter` then.
- **Q-14.3** — Answered by the audit above. No `internal/runexport`,
  `internal/headless`, or `cmd/jig` code renders glyphs; the migration
  scope stays inside `internal/tui`.

---

## Vocabulary and preset table

The vocabulary the mechanism substitutes has two invariants:

1. **Every key present in one preset is present in the other**, either
   with a concrete form or with an explicit empty string (`""`). An
   empty ASCII form means "the affordance disappears under the ASCII
   preset" — omp's slash-command icon column rule (FR-14.7). Callers
   check for `""` and elide the column, they do not draw a placeholder.
2. **The width contract for chart grid glyphs is single-cell in both
   presets.** Every other glyph may exceed one cell in the ASCII
   preset (see `IconStatus*` below); those consumers must already be
   `lipgloss.Width`-based, and the FR-14.6 audit locks that.

Notation in the table: a `unicode` cell contains the value on `main`
today; an `ascii` cell records the proposed substitute. Multi-cell
ASCII values are marked `(N cells)` so reviewers can see the FR-14.6
implication at a glance. All values are the caller-visible glyph
without SGR styling.

### Status vocabulary (line-anchored; multi-cell ASCII allowed)

| Key | unicode | ascii | Notes |
|---|---|---|---|
| `IconSuccess` | `✓` | `[ok]` (4 cells) | Steps panel and detail badges; consumers wrap in a colored style. |
| `IconError` | `✗` | `[!!]` (4 cells) | Consumers must measure with `lipgloss.Width`. |
| `IconPending` | `○` | `[ ]` (3 cells) | Steps `Pending` state. |
| `IconRunning` | `●` | `[o]` (3 cells) | Steps `Running` state. |
| `IconSkipped` | `—` | `-` | Steps `Skipped`. Single-cell in both presets. |
| `IconReview` | `?` | `?` | ASCII-safe already; entry retained for completeness. |
| `IconInput` | `⊙` | `[i]` (3 cells) | Input queue affordance. |
| `IconValidate` | `⇢` | `->` (2 cells) | Steps `AwaitingValidate`. |
| `IconThinking` | `◇` | `~` | Reasoning stub; slice 10's thinking pulse will register a spinner, not a static glyph, so this stays a marker. |
| `IconToolCall` | `▸` | `>` | Collapsed marker synonym for callers using the generic tool glyph. |
| `IconToolResult` | `↳` | `->` (2 cells) | Rare in header text (slice 02 refactor eliminated most consumers). |
| `IconStatusSuccess` | `•` | `*` | Card header, generic success. |
| `IconStatusError` | `✗` | `!` | Card header, error slot. |
| `IconStatusRunning` | `○` | `.` | Anti-jitter — pending and running share this. |
| `IconStatusPending` | `○` | `.` | Equal to running (CC-4). |
| `IconStatusWarning` | `!` | `!` | Unchanged. |
| `IconToolRead` | `◈` | `r` | Settled-success signature glyphs. Single-cell everywhere. |
| `IconToolEdit` | `✎` | `e` | |
| `IconToolWrite` | `✎` | `w` | Distinct ASCII form so read/edit/write remain readable at a glance. |
| `IconToolSearch` | `⌕` | `s` | |
| `IconToolShell` | `$` | `$` | Already ASCII. |
| `IconToolWeb` | `↗` | `@` | |
| `IconToolAgent` | `⊙` | `A` | |
| `IconToolTodo` | `⊙` | `T` | |
| `IconToolAsk` | `?` | `?` | Already ASCII. |

**Width note.** The `Icon*` and `IconStatus*` families move into the
FR-14.6 audit's blast radius because they can render at cells `> 1` in
ASCII. Every consumer of these glyphs today must reach them through
`lipgloss.Width` for padding (already true for `shared.StatusLine`,
verified) or must accept a wider row (true for `writeReviewOverview`,
where the mark is followed by two ASCII spaces already).

### Structural vocabulary (single-cell in both presets)

| Key | unicode | ascii | Notes |
|---|---|---|---|
| `CollapsedMarker` | `▸` | `>` | Item-detail toggle. |
| `ExpandedMarker` | `▾` | `v` | |
| `TreeBranchGlyph` | `├─` | `\|-` | Read-group tree connectors; two cells in both presets so `shared.TreePrefix` continues to return a fixed 3-cell prefix. |
| `TreeLastGlyph` | `└─` | `'-` | Two cells. |
| `TreeContinueGlyph` | `│ ` | `\|` + space | Two cells; `shared.TreeContinuationPrefix` unchanged. |
| `CursorBar` | `▌` | `\|` | Selection rail. |
| `RuleGlyph` | `─` | `-` | `shared.Rule` fill, panel top edge, review divider. |
| `EllipsisGlyph` (new) | `…` | `...` (3 cells) | Truncation vocabulary and title clipper. **Multi-cell in ASCII;** every consumer of `shared.TruncateTitle` and `ansi.Truncate("…")` must accept a three-cell tail. Confirmed acceptable — the truncation budget already measures with `lipgloss.Width`. |

### Chart vocabulary (single-cell in both presets — grid invariant)

| Key | unicode | ascii | Notes |
|---|---|---|---|
| `LoopGlyph` | `↺` | `L` | Used both in the grid (`chart/render.go:325`) and in text (`detail/view.go:144`, `chart/render.go:399`); the ASCII form is single-rune so `[]rune(glyph)[0]` is still valid at the grid call site. |
| `RetryGlyph` | `↻` | `R` | Grid never uses this; free to be any single letter. |
| `GateGlyph` | `⇢` | `G` | Text-only in the chart (`:396`) and the detail view (`:151`); single-cell for safety. |
| `ForEachGlyph` | `×` | `x` | Runtime fan-out `×N` badge. |
| `ArrowDownGlyph` | `▼` | `v` | Grid arrow; single-rune. |
| `CondArrowGlyph` | `▽` | `V` | Grid arrow; single-rune. |
| `ArrowLeftGlyph` | `◄` | `<` | Grid arrow; single-rune. |

### Box drawing (new keys; single-cell in both presets)

| Key | unicode | ascii | Notes |
|---|---|---|---|
| `BoxCornerTL` | `╭` | `+` | Card and panel top-left. |
| `BoxCornerTR` | `╮` | `+` | Card and panel top-right. |
| `BoxCornerBL` | `╰` | `+` | Card bottom-left. |
| `BoxCornerBR` | `╯` | `+` | Card bottom-right. |
| `BoxTeeL` | `├` | `+` | Card section divider left. |
| `BoxTeeR` | `┤` | `+` | Card section divider right. |
| `BoxVertical` | `│` | `\|` | Card body edge; `writeItemDetail` rail. |
| `DiffGutter` (new) | `│` | `\|` | Diff-row terminator; kept distinct from `BoxVertical` so a future slice can retheme the diff column without touching card frames. |

### Spinner sets (already preset-indexed)

The `spinner.SpinnerFrames` registry already carries `Unicode` and
`ASCII` slices per set (`internal/tui/shared/spinner.go:47-58`). This
slice does **one** edit there: replace the private
`spinnerASCIIPreset()` predicate (currently a hardcoded `return false`)
with `func spinnerASCIIPreset() bool { return activePreset() ==
PresetASCII }`. Registered sets today: `"status"` (slice 13). Later
slices (slice 10's `"thinking"`) will register with an already-working
predicate.

---

## Selection mechanism

`--ascii` is the only user-facing surface.

### Where it plumbs

`cmd/jig/main.go` is the sole entry point. Today it enters the TUI when
`len(os.Args) > 1` is false and dispatches subcommands otherwise. The
flag lands as a small strip-then-dispatch step at the top of `main`:

```go
if presetASCII := stripASCIIFlag(&os.Args); presetASCII {
    shared.SetPreset(shared.PresetASCII)
}
```

`stripASCIIFlag` scans `os.Args[1:]` for `--ascii` (and no other flag
form — no short flag, no `=value` form, no boolean toggle) and, if
present, removes it before the switch on `os.Args[1]` runs. This lets
`jig --ascii`, `jig --ascii run wf.toml`, and `jig run --ascii wf.toml`
all resolve the flag identically; a subcommand's own `flag.FlagSet`
sees no `--ascii` because it was already consumed. The flag is
process-global by design — a mid-run preset change would need cache
invalidation across every renderer, and the operator's need is
"launch with ASCII when the terminal cannot render Unicode," not
"toggle during a session."

### Where it does not plumb

- **`.jig/run/<id>` on disk.** No preset is written to disk; a
  resumed run inherits the invocation's preset.
- **Journal replay.** Persistence-off (`RunDir == ""`) works with
  either preset; `shared.SetPreset` is safe to call before any file is
  opened.
- **Backend selection.** Not touched. `AGENTS.md`'s TOML-only rule
  applies to `harness.For(backend, transport)`; the glyph preset lives
  next to the TUI, not next to the harness.
- **Environment.** Explicitly no `JIG_TUI_ASCII` variable, no `TERM`
  inspection, no `NO_COLOR` inspection. If an operator wants ASCII in a
  CI environment, `jig run --ascii --ci …` is the incantation.
- **Help text and shell completions.** `jig help` gains one line
  documenting `--ascii`; no completion machinery exists yet, so
  nothing to update.

### Interaction with the existing `--quiet` / `--ci` flags

`jig run --quiet` and `jig run --ci` are parsed by
`internal/headless.ParseFlags` (`internal/headless/warn.go`). The
`--ascii` strip happens *before* subcommand dispatch, so the headless
parser never sees the flag; the strip returns a bool to `main` which
calls `shared.SetPreset` before either the TUI or the headless runner
initializes. The `--ci` `EffectiveCIFlags` printout does not carry any
glyphs today (plain ASCII already), so `--ascii` does not change its
output.

---

## Architecture and ownership

```text
internal/tui/shared
  ├─ symbols.go                    (new file)
  │   • type SymbolPreset int; PresetUnicode, PresetASCII.
  │   • type symbolTable struct   (one string field per vocabulary key).
  │   • unicodeSymbols, asciiSymbols   package-level values populated
  │       at init from a single authoring source.
  │   • activeSymbols *symbolTable   default &unicodeSymbols.
  │   • SetPreset(p SymbolPreset)   swaps activeSymbols and calls the
  │       private refreshVocabulary() that copies every field into the
  │       exported vars in icons.go. Idempotent; safe to call before
  │       any TUI code runs.
  │   • activePreset() SymbolPreset   read-only accessor for
  │       spinnerASCIIPreset and any future preset-aware branch.
  │
  ├─ icons.go
  │   • change `const (...)` to `var (...)`; keep the same names.
  │   • add new keys: BoxCornerTL/TR/BL/BR, BoxTeeL/R, BoxVertical,
  │       DiffGutter, EllipsisGlyph.
  │   • delete `BarThick` (no callers).
  │   • add `func init() { refreshVocabulary() }` so imports pick up
  │       the Unicode preset even if SetPreset is never called.
  │   • header comment updated: "Values are populated from the active
  │       symbol preset by symbols.go; do not reassign at call sites."
  │
  ├─ symbols_test.go               (new file)
  │   • every key has a non-empty entry in unicodeSymbols and either
  │       a non-empty entry or an explicit-empty sentinel in
  │       asciiSymbols (FR-14.2, FR-14.7).
  │   • every glyph in unicodeSymbols and asciiSymbols has
  │       lipgloss.Width matching its documented width (single-cell
  │       for chart glyphs, arbitrary for status glyphs).
  │   • table-test SetPreset(Unicode)/SetPreset(ASCII) toggles the
  │       exported vars and returns them to Unicode.
  │
  ├─ spinner.go
  │   • replace spinnerASCIIPreset() body with activePreset() == PresetASCII.
  │
  ├─ card.go
  │   • substitute the six bare box literals with vocabulary keys.
  │   • no other change.
  │
  ├─ panel.go
  │   • substitute strings.Repeat("─", ...) → strings.Repeat(shared.RuleGlyph, ...).
  │   • substitute the "…" literal in TruncateTitle with EllipsisGlyph.
  │
  ├─ truncation.go
  │   • substitute the two "… " prefixes in MoreItems/EarlierItems
  │       with EllipsisGlyph + " ".
  │
  └─ renderer_discipline_test.go
      • extend the existing slice-02-scoped audit with
        TestVocabularyLiteralsAreCentralized (below), scoped to every
        .go file under internal/tui/** except icons.go, spinner.go, and
        _test.go.

internal/tui/monitor
  ├─ monitor_diff_render.go        (2 literal substitutions)
  ├─ monitor_transcript_items_view.go (writeItemDetail rail)
  ├─ monitor_transcript.go         (writeReviewOverview marks)
  ├─ monitor_read_group_view.go    (no change — already central)
  ├─ monitor_steps.go              (▸ and ×N routed through vocabulary;
  │                                 state indicator glyphs stay Unicode
  │                                 per NG6)
  └─ monitor_transcript_banner_test.go  (unchanged; already forbids
                                   the bare "─" literal in the sibling
                                   file)

internal/tui/detail
  └─ view.go                       (LoopGlyph + " route→" and
                                    GateGlyph + " gate")

internal/tui/review
  └─ view.go                       (bulk substitution of ○ ✓ ● ▌ │ ─ …;
                                    the review model's semantics are
                                    untouched — this is a glyph pass
                                    only, not the rework NG6 forbids.
                                    Every substitution keeps the
                                    Unicode rendering byte-identical
                                    to main.)

internal/tui/runs
  └─ view.go                       (EllipsisGlyph substitution;
                                    CursorBar already central.)

cmd/jig
  └─ main.go                       (stripASCIIFlag then SetPreset;
                                    ~10 lines including help text.)
```

Zero new types cross the `internal/tui/shared` boundary. No test file
imports another package's internals. The shared package gains one file
and one test file; `icons.go` changes from `const` to `var` with the
same identifiers (grep-verified safe). No monitor/engine/harness/
runexport/headless package is touched beyond the six file-level
migrations above.

### Not touched by slice 14

- `internal/engine`, `internal/harness/*`, `internal/transcript`,
  `internal/step`, `internal/workflow`, `internal/journal`,
  `internal/datastore`, `internal/runexport`, `internal/headless`,
  `internal/notify`, `internal/security`, `internal/observability`,
  `internal/toolcall`, `internal/review`.
- The Steps panel `stepIndicator` mapping (NG6).
- The Gate panel presentation (NG6).
- Slice 10's thinking pulse (Deferred to slice 10; the vocabulary is
  ready for it).
- Slice 15's inline argument bracket vocabulary (Deferred to slice 15).
- Any bubbletea/lipgloss/glamour version pin.

---

## Delivery phases

Large enough to land as two PRs: the mechanism + one call-site pilot
in the first, the migration debt + the selection flag in the second.
Splitting protects the first PR's diff from bloat while keeping the
second PR mechanical.

### Phase 1 — vocabulary mechanism and one pilot consumer (PR 1)

1. Add `internal/tui/shared/symbols.go` with `SymbolPreset`,
   `symbolTable`, `unicodeSymbols` (verbatim from today's `icons.go`
   values), `asciiSymbols` (per the vocabulary table above),
   `activeSymbols`, `SetPreset`, `activePreset`, and
   `refreshVocabulary`. New keys (`BoxCornerTL/TR/BL/BR`, `BoxTeeL/R`,
   `BoxVertical`, `DiffGutter`, `EllipsisGlyph`) are declared here
   alongside the existing ones.
2. Convert `icons.go` from `const` to `var`, add the new keys as
   `var`s, delete `BarThick`, add `init()` that calls
   `refreshVocabulary()`. Do not delete any existing key.
3. Add `internal/tui/shared/symbols_test.go` covering FR-14.2 (every
   key defined in both presets), FR-14.6 (each preset's glyphs match
   the width contract per vocabulary category), preset toggling
   semantics, and the CC-7 grep test's happy path.
4. Migrate `internal/tui/shared/card.go` (6 substitutions) and
   `internal/tui/shared/panel.go` (2 substitutions plus the ellipsis)
   as the pilot; these are the highest-traffic renderers.
5. Extend the existing `renderer_discipline_test.go` to add
   `TestVocabularyLiteralsAreCentralized` (see Test matrix). Scope the
   audit to `internal/tui/**` excluding `_test.go`, `icons.go`, and
   `spinner.go`. Under the Unicode preset, this test passes today with
   the pilot migrations done; it fails on any other file until phase 2
   migrates it.

**Exit:** the mechanism is live behind an always-Unicode default; the
two pilot files draw through the vocabulary; the grep test is a
sentinel that must pass before phase 2's migrations may land.

### Phase 2 — migrate the residual literals (PR 2)

1. Migrate `internal/tui/monitor/monitor_diff_render.go` (2 gutter
   substitutions).
2. Migrate `internal/tui/monitor/monitor_transcript_items_view.go`
   (`writeItemDetail` rail).
3. Migrate `internal/tui/monitor/monitor_transcript.go`
   (`writeReviewOverview` marks).
4. Migrate `internal/tui/monitor/monitor_steps.go`'s `×N` badge and
   the `▸ ` sub-step prefix through `ForEachGlyph` and
   `CollapsedMarker`; leave `stepIndicator` alone (NG6).
5. Migrate `internal/tui/detail/view.go` (2 marker substitutions).
6. Migrate `internal/tui/runs/view.go` (`EllipsisGlyph`).
7. Migrate `internal/tui/shared/truncation.go` (2 leading ellipses).
8. Migrate `internal/tui/review/view.go` (bulk pass; every literal
   listed in the Q-14.3 audit).

**Exit:** `TestVocabularyLiteralsAreCentralized` passes for every
production file under `internal/tui/**`; the Unicode preset renders
byte-identically to `main` (verified by the existing
`JIG_UI_SNAPSHOT_DIR` galleries under the Unicode default).

### Phase 3 — selection mechanism and ASCII proofs (PR 2 tail)

1. Add `stripASCIIFlag(*[]string) bool` to `cmd/jig/main.go`; call it
   before the `switch os.Args[1]` dispatch; call
   `shared.SetPreset(PresetASCII)` on `true`. Add the `--ascii` line
   to `printHelp()`.
2. Add `internal/tui/monitor/monitor_ascii_test.go` (or extend an
   existing gallery test) that captures the four canonical monitor
   scenes under both presets. Diff the Unicode output against the
   pre-slice-14 golden captures for byte identity; diff the ASCII
   output against a new golden.
3. Add `TestVocabularyASCIIOutputHasNoUnicode` (FR-14.5): a test that
   renders a representative synthetic transcript under
   `PresetASCII`, `ansi.Strip`s the result, and asserts every rune is
   `< 0x80`.
4. Add `TestCardWidthUnderASCIIPreset` (FR-14.6): render a card with
   an `IconStatusSuccess` header under both presets and assert
   `lipgloss.Width` of each row equals the configured card width. The
   ASCII `IconStatusSuccess` is `*` (single-cell), but the same test
   with `IconSuccess` (multi-cell `[ok]`) proves the padding math
   under the wider form.

**Exit:** `go test ./...` passes under both presets (the tests
programmatically toggle); `go run ./cmd/jig --ascii` renders every
panel without a replacement character on a terminal that supports
only ASCII; the four monitor gallery captures ship with `.ansi`,
`.html`, and `-notes.txt` for each preset.

### Phase 4 — proofs, docs, and open-question closure

1. Capture deterministic 80-column monitor scenes under
   `docs/specs/25-spec-glyph-presets/25-proofs/` (new spec directory)
   showing: (a) the transcript panel under Unicode, (b) the same
   panel under ASCII, (c) a card with a settled-error header under
   ASCII (proves the multi-cell status glyph does not break the
   frame), and (d) a chart under ASCII (proves the single-rune grid
   glyph substitutions do not overflow).
2. Record the Q-14.1 / Q-14.2 / Q-14.3 resolutions in
   `docs/epics/omp-transcript-parity/slices/14-glyph-presets.md`;
   mark Q-14.1 answered by the CLI-flag argument; mark Q-14.2 answered
   by the `BarThick` deletion; mark Q-14.3 answered by the audit above.
3. Cross-link the slice from
   [`docs/plans/open-goals.md`](open-goals.md) A15 (Narrow / mobile
   terminal contract) — glyph preset is one component of the broader
   responsive contract, and A15 should reference the mechanism.
4. Add a `--ascii` note to `docs/operations.md`'s TUI section (one
   line under an existing subsection; do not create a new one).

**Exit:** proofs recorded, epic slice document reflects resolved
questions, open-goals cross-linked, operator-facing docs updated.

---

## Ordered implementation tasks

Estimates are focused-agent wall time; every substantive code change
has a sibling test task. Task areas are `<Go import path> — <file>` so
the mapping is unambiguous.

| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `symbols.go` with `SymbolPreset`, `symbolTable`, `unicodeSymbols`, `asciiSymbols`, `activeSymbols`, `SetPreset`, `activePreset`, `refreshVocabulary`, and the new key set | `internal/tui/shared — symbols.go` (new) | 45 min |
| 2 | Convert `icons.go` const block to var block; add new keys; delete `BarThick`; add `init()` calling `refreshVocabulary` | `internal/tui/shared — icons.go` | 20 min |
| 3 | Table-test `symbols.go`: every key present in both presets, width contract per category, `SetPreset` round-trip | `internal/tui/shared — symbols_test.go` (new) | 40 min |
| 4 | Migrate `shared/card.go` box literals to the vocabulary (6 substitutions) | `internal/tui/shared — card.go` | 15 min |
| 5 | Migrate `shared/panel.go` `─` (2) and `…` (1) to `RuleGlyph` / `EllipsisGlyph` | `internal/tui/shared — panel.go` | 10 min |
| 6 | Flip `spinner.go`'s `spinnerASCIIPreset()` to `activePreset() == PresetASCII` | `internal/tui/shared — spinner.go` | 5 min |
| 7 | Extend `renderer_discipline_test.go` with `TestVocabularyLiteralsAreCentralized` (whitelist: `icons.go`, `spinner.go`, `_test.go`) | `internal/tui/shared — renderer_discipline_test.go` | 30 min |
| 8 | Migrate `monitor/monitor_diff_render.go` gutter (2 substitutions) | `internal/tui/monitor — monitor_diff_render.go` | 10 min |
| 9 | Migrate `monitor/monitor_transcript_items_view.go` `writeItemDetail` rail | `internal/tui/monitor — monitor_transcript_items_view.go` | 10 min |
| 10 | Migrate `monitor/monitor_transcript.go` `writeReviewOverview` marks | `internal/tui/monitor — monitor_transcript.go` | 10 min |
| 11 | Migrate `monitor/monitor_steps.go` `▸` and `×N` (keep `stepIndicator` glyphs Unicode per NG6) | `internal/tui/monitor — monitor_steps.go` | 15 min |
| 12 | Migrate `detail/view.go` (`↺ route→`, `⇢ gate`) | `internal/tui/detail — view.go` | 10 min |
| 13 | Migrate `runs/view.go` ellipsis | `internal/tui/runs — view.go` | 5 min |
| 14 | Migrate `shared/truncation.go` `… ` prefixes | `internal/tui/shared — truncation.go` | 5 min |
| 15 | Migrate `review/view.go` bulk pass (17 substitutions per audit) | `internal/tui/review — view.go` | 45 min |
| 16 | Add `stripASCIIFlag` in `cmd/jig/main.go`; wire `SetPreset(PresetASCII)`; add `--ascii` help line | `cmd/jig — main.go` | 20 min |
| 17 | New test `TestSetPresetToggles` covering unicode→ascii→unicode and the exported var re-population | `internal/tui/shared — symbols_test.go` | 15 min |
| 18 | New test `TestVocabularyASCIIOutputHasNoUnicode` rendering a synthetic transcript under `PresetASCII` and asserting every rune `< 0x80` | `internal/tui/monitor — monitor_ascii_test.go` (new) | 35 min |
| 19 | New test `TestCardWidthUnderASCIIPreset` rendering a card with `IconStatusSuccess` and `IconSuccess` headers under both presets and asserting exact row widths | `internal/tui/shared — card_test.go` | 30 min |
| 20 | New test `TestChartGridSingleCellUnderBothPresets` asserting `lipgloss.Width(ArrowDownGlyph)`, `LoopGlyph`, etc. are 1 in both presets (chart grid invariant) | `internal/tui/shared — symbols_test.go` | 15 min |
| 21 | New test `TestReviewOverviewRendersUnderBothPresets` seating a review document and asserting the marks appear (Unicode: `○ ✓`, ASCII: `[ ] [ok]`) | `internal/tui/monitor — monitor_transcript_test.go` | 25 min |
| 22 | New test `TestSpinnerActivePresetSwitchesFrames` proving `SetPreset(PresetASCII)` causes `SpinnerFrame("status", ...)` to return the ASCII rotor and back | `internal/tui/shared — spinner_test.go` | 20 min |
| 23 | Capture Unicode/ASCII proofs under `docs/specs/25-spec-glyph-presets/25-proofs/` for the four documented scenes (transcript, error card, chart, review overview) | `docs/specs/25-spec-glyph-presets/25-proofs/` (new) | 40 min |
| 24 | Record Q-14.1 / Q-14.2 / Q-14.3 resolutions in `docs/epics/omp-transcript-parity/slices/14-glyph-presets.md` | `docs/epics/omp-transcript-parity/slices/14-glyph-presets.md` | 15 min |
| 25 | Cross-link the slice from `docs/plans/open-goals.md` A15 and add the `--ascii` line to `docs/operations.md` | `docs/plans/open-goals.md`, `docs/operations.md` | 10 min |

Estimated focused implementation time: **6.5 hours across two PRs**
(phase 1 ~2 h, phase 2+3 ~3.5 h, phase 4 ~1 h). The FR-14.6 audit
(tasks 19, 21) is the largest single risk driver — budget for review
churn if it surfaces existing width bugs.

---

## Test matrix

### New unit and behavioral tests

| Test | Purpose | Fixture shape |
|---|---|---|
| `TestSymbolTablesComplete` | Every key present in both presets; asciiSymbols may declare a key as explicitly empty (`""`) but not omit it (FR-14.2, FR-14.7) | table over the field names of `symbolTable` |
| `TestStatusGlyphWidthContracts` | `IconStatus*` are single-cell under Unicode; ASCII widths equal the vocabulary table's `(N cells)` annotation (FR-14.6) | table over the status key set |
| `TestChartGlyphSingleCellUnderBothPresets` | Grid glyphs (`ArrowDownGlyph`, `CondArrowGlyph`, `ArrowLeftGlyph`, `LoopGlyph`) have `lipgloss.Width == 1` under both presets so `chart/render.go`'s `[]rune(x)[0]` remains valid | table over the chart key set |
| `TestSetPresetTogglesExportedVars` | `SetPreset(PresetASCII)` mutates `IconSuccess`, `CursorBar`, `BoxVertical`, etc. to their ASCII forms; `SetPreset(PresetUnicode)` restores them | direct assertion after each call |
| `TestSetPresetIsIdempotent` | Two consecutive `SetPreset(PresetASCII)` calls leave the vars in the same state; a Unicode→ASCII→Unicode sequence restores the original values byte-for-byte | direct assertion |
| `TestSpinnerActivePresetSwitchesFrames` | After `SetPreset(PresetASCII)`, `SpinnerFrame("status", now)` returns from the ASCII rotor; after `SetPreset(PresetUnicode)` it returns from the Unicode rotor | one anchor timestamp per branch |
| `TestVocabularyLiteralsAreCentralized` (FR-14.1) | Repository grep: every `.go` file under `internal/tui/**` except `icons.go`, `spinner.go`, and `_test.go` may not contain a bare glyph literal listed in the vocabulary. Reads sources via `os.ReadFile`; skips files that fail to open (matching the existing slice-02-scoped audit's pattern) | authoritative set of glyphs to forbid; test iterates and errors on match |
| `TestVocabularyASCIIOutputHasNoUnicode` (FR-14.5) | Render a synthetic transcript (one text item, one tool exchange, one error, one boundary banner, one review overview) under `PresetASCII`; strip ANSI; assert every rune has code point `< 0x80` | in-memory `Model` with three items |
| `TestCardWidthUnderASCIIPreset` (FR-14.6) | Render `shared.Card{Header: "…", HeaderMeta: "…", State: CardError}` at widths 40/60/80 under both presets; assert every row's `lipgloss.Width` equals the configured card width | table over widths × presets |
| `TestPanelTopEdgeWidthUnderASCIIPreset` (FR-14.6) | `PanelTopEdge` renders exactly `width` cells under both presets, with the `+` corner and `-` fill in ASCII | table over widths |
| `TestReviewOverviewRendersUnderBothPresets` | Under `PresetUnicode` the overview reads `○ label` and `✓ label` as today; under `PresetASCII` it reads `[ ] label` and `[ok] label`; the row width delta is accepted by the enclosing panel (which measures with `lipgloss.Width`) | one review request with two documents |
| `TestChartRendersUnderASCIIPreset` | `chart.Render` on a small fixture produces no `▼`/`▽`/`◄`/`↺`/`⇢`/`×`/`↻` runes and every grid cell remains one column | reuse an existing chart golden fixture and re-run under the ASCII preset |
| `TestStripASCIIFlag` | `stripASCIIFlag` mutates `os.Args` in place: `[]{"jig", "--ascii"}` → `[]{"jig"}, true`; `[]{"jig", "run", "--ascii", "wf.toml"}` → `[]{"jig", "run", "wf.toml"}, true`; without the flag returns `false` and leaves `os.Args` unchanged | table-driven |

### Regression tests to re-run unchanged

| Test | File | Expected outcome |
|---|---|---|
| Every existing test asserting a specific glyph (`IconSuccess`, `IconStatusError`, `CursorBar`, `RuleGlyph`, ...) | `internal/tui/**/*_test.go` | Passes unchanged under the Unicode default. The vocabulary substitution happens through the same variable name, so the test's `strings.Contains(..., shared.IconSuccess)` reads the current preset's value. |
| `TestSlice02RenderersUseCentralizedTheme` | `internal/tui/shared/renderer_discipline_test.go` | Passes unchanged. |
| `TestSlice02IconGlyphsAreCentralized` | Same | Passes unchanged; new keys are checked in a sibling test but the existing list stays. |
| `TestSpinnerFrameSameTimestampSameFrame` and friends | `internal/tui/shared/spinner_test.go` | Passes unchanged under both presets after the `spinnerASCIIPreset` flip; each test's `SpinnerFrame("status", ...)` reads whichever preset is active at call time. |
| Every gallery test that writes under `JIG_UI_SNAPSHOT_DIR` | `internal/tui/**/*_test.go` | Byte-identical under `PresetUnicode` (default); when re-run under `PresetASCII` regenerates goldens for the new preset. |
| `TestMonitorLiveClockNoDuplicateLoops` | `internal/tui/monitor/monitor_test.go` | Passes unchanged; the pulse mechanism is not touched. |
| `TestTranscriptCardLineRangesCachedAndFresh` | `internal/tui/monitor/monitor_transcript_card_test.go` | Passes unchanged under Unicode; a new sibling test asserts card widths under ASCII (FR-14.6). |

### Release verification

```bash
gofmt -l -w <changed-go-files>
go test ./internal/tui/shared -race -count=1
go test ./internal/tui/monitor -race -count=1
go test ./internal/tui/... -race -count=1
go test ./...
go vet ./...
go build ./cmd/jig
go run ./cmd/jig validate .agents/jig/sdd.toml
```

Terminal-capture proofs are regenerated by rerunning the slice-14
gallery test twice — once with the default preset, once with
`--ascii` — against `JIG_UI_SNAPSHOT_DIR`.

---

## Security and failure handling

- **No new sensitive surface.** The preset is a display choice; no
  operator data, credentials, prompts, or tool output flow through the
  vocabulary or the selection mechanism. The `--ascii` flag is echoed
  in shell history like every other CLI flag.
- **No new persistence.** The preset is process-local; nothing about
  it is written to `.jig/`, the journal, or `transcript.jsonl`. A
  resumed run inherits the invocation's preset — which is the correct
  semantic (the ASCII operator resuming their own run continues to
  see ASCII).
- **Persistence-off.** With `RunDir == ""` every renderer still works
  under either preset; `SetPreset` is called before any file is
  opened.
- **Journal replay after crash.** Journal entries have no visual
  content; the preset does not appear in any replayed event. Reopen
  proceeds identically under either preset.
- **Non-Unicode terminals.** Setting `--ascii` produces a legible
  panel with no code point `> 0x7F` in the rendered bytes (locked by
  `TestVocabularyASCIIOutputHasNoUnicode`). Terminals that render
  Unicode incorrectly *without* `--ascii` are undefined behavior — as
  they are today — and the mitigation is documented in `docs/
  operations.md`.
- **Copy/yank into system clipboard.** `internal/tui/monitor/
  clipboard.go` copies the ANSI-stripped payload. Under ASCII the
  copied text is pure ASCII; under Unicode the copied text can carry
  the vocabulary's Unicode glyphs (unchanged from today).
- **`--ci` and `--quiet` interaction.** `stripASCIIFlag` runs before
  the subcommand dispatch, so `jig run --ci --ascii wf.toml` and
  `jig --ascii run --ci wf.toml` are equivalent. The `--ci` warning
  printout uses no glyphs from the vocabulary; changing the preset
  does not change any headless output.

---

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| A caller stores an old glyph value (`local := shared.IconSuccess`) before `SetPreset` runs, so the ASCII toggle leaves the caller's copy stale | `stripASCIIFlag` runs at the top of `main` before any TUI code initializes; `renderer_discipline_test.go`'s existing "styles live in `styles.go`" rule already discourages package-level cached values. Task 7's grep test catches any glyph literal that reappears; if a caller caches through the exported var, its subsequent read still sees the current preset because the var is re-populated in place. |
| A slice-02 constant (e.g. `IconStatusRunning`) that a test asserts by literal `"○"` regresses under ASCII | Test suites already reference `shared.Icon*` symbols, not literal glyphs (verified). The new `TestVocabularyASCIIOutputHasNoUnicode` locks the ASCII path; the Unicode path stays byte-identical to `main`. |
| FR-14.6 surfaces existing width bugs in `internal/tui/review` or `monitor_steps.go` where padding assumed a single cell | Tasks 19 and 21 exercise the multi-cell status glyphs against real card and review renderings. If a bug surfaces, the fix is a `lipgloss.Width` call at the offending padding site; the plan budgets for review churn but does not commit to fixing latent bugs beyond the four files the audit lists as in-scope. Any bug in a file the audit lists as **exempt** (e.g. Steps under NG6) is recorded as a follow-up and does not block. |
| The `[ok]` / `[!!]` / `[ ]` / `[o]` / `[i]` / `[->]` multi-cell status glyphs shift trailing columns in the Steps panel that still uses `stepIndicator` under NG6 | The Steps panel is exempt; slice 14 does not activate ASCII glyphs there. If a later slice enables ASCII on the Steps panel, that slice owns the width audit. |
| The `TestVocabularyLiteralsAreCentralized` grep test flags a legitimate literal in a comment, a docstring, or a test-only fixture | The test scopes itself to `.go` files under `internal/tui/**` excluding `_test.go`, matches only glyph runes from the vocabulary (not every non-ASCII rune), and reads bytes rather than parsing Go so a rune inside a `//` comment is caught. If a legitimate exception surfaces during implementation, it is added to the test's whitelist with an inline comment linking to this plan — not silenced globally. |
| `BarThick` deletion breaks a caller the plan grep missed | Task 2's substitution grep is repeated after task 15 as a post-migration check; if a caller reappears it fails at compile time (the identifier is deleted, not shadowed). |
| The `chart` package's `[]rune(glyph)[0]` idiom fails under a multi-rune ASCII form | Task 20 locks single-cell/single-rune widths for every chart-consumed key. If a future author adds a multi-rune ASCII form to a chart key, the test fails at PR review. |
| `stripASCIIFlag` conflicts with a future long-form flag whose parser expects `--ascii` as a value or an alias | The strip is exact-match on `--ascii` only (no `=`, no short form); a subcommand's own parser is free to define `--ascii` with a different meaning in the future because the strip has already removed it. If that becomes confusing, the follow-up work is to promote the flag into a shared preflag parser — recorded as a follow-up, not blocked. |
| An operator sets `--ascii` on `jig run --ci` and expects the JSON summary to change | The `--ci` summary and every other headless output is plain ASCII already; `--ascii` is a no-op for headless. The `printHelp` line documents that `--ascii` targets the TUI. |
| The review workspace glyph migration is misread as the "rework" that NG6 forbids | Every substitution keeps the Unicode output byte-identical; no color, layout, comment, or draft/anchor semantic changes. The migration is a keystroke-by-keystroke rename of literals, defended in the phase 2 description and enforced by a byte-identity comparison against the pre-slice-14 review capture. |
| A repository grep test on a Windows CI runner sees a different newline pattern that hides a glyph literal | The grep test reads bytes and matches on rune presence, not on line boundaries; CRLF vs LF is irrelevant. |
| The plan misses a glyph literal in a package the audit did not scan | The final `TestVocabularyLiteralsAreCentralized` walks every `.go` file under `internal/tui/**` at test time, not at plan-writing time; a missed literal fails the test. |

---

## Completion criteria

Slice 14 is done when all of the following hold against a real
multi-step run with at least one settled successful step, one settled
failed step, and one still-running step, under both `jig` (default
Unicode) and `jig --ascii`:

1. **CC-14.1** — Every glyph the TUI renders is sourced from
   `internal/tui/shared/icons.go` (via `symbols.go`) — no glyph is a
   string literal at a call site in `internal/tui/**` outside
   `icons.go` and `spinner.go`. Locked by
   `TestVocabularyLiteralsAreCentralized` (FR-14.1).
2. **CC-14.2** — Every key in the vocabulary has a defined value in
   both `unicodeSymbols` and `asciiSymbols`; a key that has no
   reasonable ASCII form is present with an explicit `""` so the
   consumer elides the affordance rather than substituting a
   misleading glyph. Locked by `TestSymbolTablesComplete` (FR-14.2,
   FR-14.7).
3. **CC-14.3** — The active preset is selectable by the operator via
   `jig --ascii` and `jig run --ascii`; the default preset is
   Unicode (FR-14.3, FR-14.4).
4. **CC-14.4** — Under the ASCII preset the panel renders with no
   code point above `0x7F`. Locked by
   `TestVocabularyASCIIOutputHasNoUnicode` (FR-14.5).
5. **CC-14.5** — Card, panel, and status-line widths remain exact
   under both presets, including with multi-cell status glyphs
   (`[ok]`, `[!!]`). Locked by `TestCardWidthUnderASCIIPreset` and
   `TestPanelTopEdgeWidthUnderASCIIPreset` (FR-14.6).
6. **CC-14.6** — Chart grid glyphs remain single-cell in both
   presets so `chart/render.go`'s `[]rune(glyph)[0]` idiom stays
   valid. Locked by `TestChartGlyphSingleCellUnderBothPresets`.
7. **CC-14.7** — Slice 13's `SpinnerFrame("status", now)` returns the
   ASCII rotor after `SetPreset(PresetASCII)` and the Unicode rotor
   after `SetPreset(PresetUnicode)`. Locked by
   `TestSpinnerActivePresetSwitchesFrames`.
8. **CC-14.8** — `BarThick` is deleted and no caller references it.
9. **CC-14.9** — Persistence-off runs still render every panel under
   both presets; the review workspace continues to open under either
   preset with byte-identical Unicode output vs `main`.
10. **CC-14.10** — `go build ./cmd/jig`, `go test ./...`,
    `go test -race ./internal/tui/...`, `go vet ./...`,
    `gofmt -l <changed-go-files>` (empty), and
    `go run ./cmd/jig validate .agents/jig/sdd.toml` pass under both
    presets (tests programmatically toggle).
11. **CC-14.11** — Terminal-capture proofs under
    `docs/specs/25-spec-glyph-presets/25-proofs/` include the four
    documented scenes under both presets.
12. **CC-14.12** — The slice document
    [`slices/14-glyph-presets.md`](../epics/omp-transcript-parity/slices/14-glyph-presets.md)
    records Q-14.1 as answered by the CLI-flag argument, Q-14.2 as
    answered by the `BarThick` deletion, and Q-14.3 as answered by
    the audit above.
