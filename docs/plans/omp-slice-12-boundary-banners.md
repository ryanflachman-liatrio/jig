# Implementation Plan: OMP parity slice 12 — centered boundary banners

**Status:** Planned — omp-transcript-parity epic, slice
[`12-boundary-banners`](../epics/omp-transcript-parity/slices/12-boundary-banners.md)
**Risk:** **low** — presentation-only, single Go package plus one new helper
in `internal/tui/shared`. No schema, harness, transcript, engine, or persistence
change. The chief hazards are (a) `chatItemLineRanges` accounting when a new
row lands in the coord gap and (b) coexisting with the slice-11 metadata row
which already occupies that gap.
**Depends on:** epic slice 04 (vertical rhythm and block edges) — landed under
[`docs/specs/25-spec-vertical-rhythm-and-block-edges`](../specs/25-spec-vertical-rhythm-and-block-edges/25-spec-vertical-rhythm-and-block-edges.md);
the raw-bytes zero-height discipline and the two-line execution-coordinate gap
are the insertion contract this slice rides on. Also depends conceptually on
slice 00 (dead render-plan removal) — landed — so this slice reintroduces the
banner on the live path only, without touching the retired render-plan surface.
**Complements:** slice 11 (per-turn metadata row) — landed under
[`internal/tui/monitor/monitor_transcript_metadata.go`](../../internal/tui/monitor/monitor_transcript_metadata.go);
the two features share the coord gap. Slice 12 slots one banner row into that
gap without expanding it and folds its rows into the same "prior item" line
range slice 11 already extends.
**Breaks:** nothing at present — the legacy left-anchored banner literals
(`── re-run N ──`, `── iteration N ──`, `── retry N ──`) were part of the
render-plan path that slice 00 already deleted. Slice 12 is the reintroduction
on the live item path, using width-aware centered form and the schema-aligned
`reset N` label instead of the `re-run N` jargon.

---

## Summary

Reintroduce execution-boundary markers on the live transcript item path as
centered, width-aware labeled rules that sit inside the existing two-line
execution-coordinate gap. Consume the already-computed, presentation-neutral
`visibleExecutionBoundaries` output — no boundary is invented here. Rules are
built by one new shared helper `shared.Rule(...)` so slice 12 accrues no
palette, style, or glyph debt. Coexists with slice 11's per-turn metadata row
by placing the banner immediately above the arriving turn's first item and by
folding its rows into the closing item's `chatItemLineRanges` entry — the same
discipline slice 11 established.

## Approach

Extend the slice-04/slice-11 loop in `itemTranscriptBody`
(`internal/tui/monitor/monitor_transcript_items_view.go`) with one call site
that, on a coordinate change (i.e. `itemSpacingBefore(prev, cur) == 2`), emits
one rendered banner row for the *arriving* turn's coord. Reuse the
`visibleExecutionBoundaries` reporter as the truth of what constitutes a
boundary in the visible item slice — the loop already has the previous and
current items in hand, so no re-walk of the slice is required; the reporter
is called once per rebuild to sanity-check that the on-loop and off-loop views
agree in tests.

Rules are drawn by a new helper `shared.Rule(width, label, ...RuleOption)`
that produces a single line of exactly `width` visible cells consisting of
`RuleGlyph` fill with the label centered inside the fill, degrading to the
bare label when `width` cannot accommodate at least a minimum rule (matches
omp's `compaction-summary-message.ts:57-76` degradation). Styles: rule glyphs
in `Theme.Marker` (the existing muted foreground), label in a bold `Marker`
derivative for one-register contrast. The helper is width-aware via
`lipgloss.Width`, so wide runes or SGR-styled labels do not overflow.

Coord labels follow the schema vocabulary rather than the retired
render-plan strings: `reset N` for generation (matches `Run.Reset`, the only
trigger that bumps generation in [`docs/workflow-schema.md`](../workflow-schema.md)),
`iteration N` for a bounded-route rewind, `retry N` for a `[step.retry]`
attempt bump. Off-by-one conventions are audited and documented rather than
inherited unexamined.

## Problem

`itemTranscriptBody` currently emits no execution-boundary marker at all —
the live item path has never had one. `visibleExecutionBoundaries` computes
generation/iteration/attempt transitions from the loaded item slice and is
tested in `monitor_transcript_items_test.go:81-91`, but no renderer consumes
it. Operators reading a multi-attempt or multi-iteration step therefore see:

- one card, then a two-line gap, then a card (possibly with a slice-11
  metadata row inside the gap), and no signal that the second card belongs to
  a different attempt or iteration.

The retired render-plan surface used to emit left-anchored fixed-string
markers:

```go
fmt.Sprintf("── re-run %d ──", e.Generation+1)
fmt.Sprintf("── iteration %d ──", e.Iteration+1)
fmt.Sprintf("── retry %d ──", e.Attempt)
```

rendered as `"\n  " + Marker.Render(sep) + "\n\n"`
(pre-slice-00 `monitor_transcript.go:506-523,796`). Those were removed by
slice 00 because they fed the unreachable render-plan path. This slice is the
correct-surface reintroduction: on the live item path, width-aware, centered,
one shared helper, one grammar.

Two secondary problems the slice fixes at the same time:

1. **Vocabulary drift.** `re-run` is not the schema's word for a manual
   `Run.Reset`; the schema calls the counter `generation` and the user-facing
   trigger `reset`. Users see "Reset confirmation" dialogs in the Steps
   panel (`monitor_gate.go:193`). `reset N` is the right label.
2. **Off-by-one inconsistency.** The retired code displayed
   `generation+1`, `iteration+1`, and raw `attempt`. That mixture reads as
   accidental. It is not (see [Vocabulary and display conventions](#vocabulary-and-display-conventions))
   but it deserves an audit and a comment before it is copied forward.

---

## Data availability audit

The slice document delegates most of the audit to the epic's cross-cutting
context; here is the concrete availability table.

| Datum | Source | Live-truth path | Persisted path | Available for slice 12? |
|---|---|---|---|---|
| Coord change between adjacent visible items | `visibleExecutionBoundaries(items)` — pure function over `transcriptItem.coord` | `m.chatVisibleItems` after `buildTranscriptItems` | `transcript.jsonl` (via `Generation`, `Iteration`, `Attempt` fields on `Entry`) | **Yes.** Already tested at `monitor_transcript_items_test.go:81`. |
| Arriving-turn generation | `curr.coord.generation` | `m.chatVisibleItems[i].coord` | Same | **Yes.** |
| Arriving-turn iteration | `curr.coord.iteration` | Same | Same | **Yes.** |
| Arriving-turn attempt | `curr.coord.attempt` | Same | Same | **Yes.** |
| Failure reason that caused a retry | Not on `toolCorrelationKey`; would require reading the *prior* attempt's terminal `StepStatus` | `monitorStep` — but reason strings are not persisted per-attempt on `monitorStep` | not persisted per-attempt | **No.** Consistent with Q-12.2's "no, keep the banner scannable"; the failed step's own card carries its error surface already. |
| Banner width | `m.transcriptInnerW - lipgloss.Width(prefix)` — the same "available" the card primitive uses | Monitor layout state | — | **Yes.** Two-cell prefix per slice 03 gives `transcriptInnerW - 2`; live cell budget. |
| Panel-too-narrow degradation trigger | Derived: `width < lipgloss.Width(label) + 2*minCap + 2` where `minCap = 1` | Same | — | **Yes.** Pure width math via `lipgloss.Width`. |

### Field selection

The banner carries exactly one label. The label is composed by
`boundaryLabel(prev, curr)` and prioritizes the *outermost* coordinate that
changed:

| Precedence | Trigger | Label |
|---:|---|---|
| 1 | `curr.generation != prev.generation` | `reset N` where `N = curr.generation + 1` |
| 2 | `curr.iteration != prev.iteration` | `iteration N` where `N = curr.iteration + 1` |
| 3 | `curr.attempt != prev.attempt` | `retry N` where `N = curr.attempt` |
| — | none of the above | (no banner emitted — `visibleExecutionBoundaries` would not have reported this pair) |

Rationale for the outermost-only choice: a bump on the outer coordinate
implicitly resets the inner ones (a new generation restarts iterations and
attempts; a new iteration restarts attempts), so displaying `reset 2 ·
iteration 1 · retry 0` would triple-count the same event. The precedence
matches how `visibleExecutionBoundaries` currently marks a boundary at index
`0` — it inspects any non-zero coordinate on the first item and does not
attempt to compose them.

---

## Vocabulary and display conventions

Two questions belong on the plan record and not the slice document because
both are trivially resolvable but easy to get wrong.

### Q-12.1 — Label wording

**Resolved.** Use `reset N`, `iteration N`, `retry N`. Rationale:

- **`generation → reset N`.** The schema
  ([`docs/workflow-schema.md:534-540`](../workflow-schema.md)) is explicit:
  "`Generation` bumps on a manual `Run.Reset`". `Run.Reset` is the user-facing
  verb, exposed as the Steps-panel `r` binding
  (`monitor_transcript.go` → `keys.go:76,148`) and the "Reset confirmation"
  gate. `re-run` appears nowhere in the schema; it was a rendering
  neologism.
- **`iteration N`.** Matches the schema exactly (`Iteration` bumps on a
  bounded route rewind). Already the term operators see in
  `monitor_steps.go`'s per-step chrome.
- **`retry N`.** Matches the schema's `[step.retry]` block name and the
  monitor's per-step retry counter. `attempt N` would be a valid alternative
  but is engine-internal; `retry` reads better as a banner label.

### Q-12.2 — Failure reason on the retry banner

**Resolved: no** (concurs with the slice suggestion). The failed step's own
tool-exchange card already carries its error surface via slice 02's Meta
slot and slice 05's error-detail section, plus the top-of-transcript status
label (`monitor_transcript.go` top-of-body chrome). Duplicating the failure
into the banner would violate epic CC-2 ("state carried by color/border,
not appended words") and add width the banner cannot always accommodate.

### Q-12.3 — Left-anchored short-rule variant

**Resolved: no, not yet.** The epic proposes both a centered rule (for major
boundaries — `reset`, `iteration`, `retry`) and a left-anchored short-rule
variant (for minor markers). Slice 12 has exactly one boundary kind (the
execution-coordinate transition), so a second visual form is not yet
needed. `shared.Rule` is designed to support a `LeftAnchored(dashes int)`
option so a later slice can add a minor form without a second helper, but
this slice ships only the centered form. Recorded so a reviewer knows the
choice was deliberate.

### Off-by-one convention (audit, not a change)

The retired code applied `+1` to generation and iteration but rendered
attempt raw. That reads as inconsistency; it is not, and the plan preserves
the same display so operators do not have to relearn the labels:

- **generation / iteration** display the 1-indexed **pass number**.
  `generation=0` is "first run"; `visibleExecutionBoundaries` does not emit
  a boundary for it (except the special `i==0` case when the loaded page
  starts mid-run). A boundary into `generation=1` is "reset 2" — the second
  run overall. `iteration=0` is "first pass"; a boundary into `iteration=1`
  is "iteration 2" — the second pass.
- **attempt** displays the 1-indexed **retry count**. `attempt=0` is "no
  retry yet"; a boundary into `attempt=1` is "retry 1" — the first retry.

Both are 1-indexed and both are internally consistent. The apparent
inconsistency comes from anchoring on *pass number* vs *retry count*, which
are different quantities (one is 1-indexed from the first run, the other is
0-indexed from "no retries occurred yet"). Adding an in-code comment at
`boundaryLabel` explains this so a future reader does not "fix" it by
adding `+1` to attempts.

---

## Row grammar

One row per boundary. Two-space indented like every other transcript row
(matching the fixed cursor gutter established by slice 03). Not selectable;
never wears the cursor bar; never enters `chatVisibleItems`. Rendered
through the new `shared.Rule` helper.

### Centered form (the only form this slice ships)

At `transcriptInnerW - 2 = 60`:

```
  ─────────────────── iteration 2 ───────────────────
```

At `transcriptInnerW - 2 = 40`:

```
  ─────────── retry 1 ────────────
```

At `transcriptInnerW - 2 = 12` (below `lipgloss.Width(label) + 2*minCap + 2`):

```
  iteration 2
```

Structure:

- Two-space prefix (unselected transcript gutter; slice 03).
- `capLeft` glyphs of `RuleGlyph` (muted).
- Single space.
- Label (dim, bold register — one step lighter than `Theme.Chat.Hint` so
  the banner reads as a boundary rather than as noise).
- Single space.
- `capRight` glyphs of `RuleGlyph` (muted).

Where `capLeft = floor((width - labelWidth - 2) / 2)` and
`capRight = width - labelWidth - 2 - capLeft`.

Degradation (`width < labelWidth + 2*minCap + 2`): render the bare label
with the two-space prefix, no fill glyphs. This is the omp
"narrow-panel-degradation" spelling.

### What the banner is not

- Not a card. No border, no tint, no state color. Boundaries are
  register-only.
- Not selectable. No `chatVisibleItems` entry; no per-banner
  `chatItemLineRanges` key. Its row folds into the *closing* item's line
  range so `n`/`N` block navigation lands on the newly-arriving item, not
  the banner — consistent with slice 11's discipline.
- Not stateful. The banner does not glow, animate, or spin. Slice 13
  (liveness) owns motion; slice 12 owns markers.
- Not a footer for the closing turn. The slice-11 metadata row does that.
  The banner announces the *arriving* turn.
- Not a stack. Multiple coord components changing at once emit exactly one
  banner (the outermost-changed component wins per the precedence table).

---

## Where the banner inserts

Slice 12's insertion point is the exact spot slice 11 already carved out.
Current loop shape (verbatim from `monitor_transcript_items_view.go:32-92`
with slice 11 wiring shown):

```go
if lastRenderedIdx >= 0 {
    prev := m.chatVisibleItems[lastRenderedIdx]
    spacing := itemSpacingBefore(prev, item)
    if spacing >= 2 {
        b.WriteString("\n"); line++
        row := m.renderTurnMetadataRow(prev.coord, false, false)
        if row != "" {
            b.WriteString(row); b.WriteString("\n")
            line += strings.Count(row, "\n") + 1
            // fold row into prev's chatItemLineRanges
        }
        for range spacing - 1 {
            b.WriteString("\n"); line++
        }
    } else {
        for range spacing { b.WriteString("\n"); line++ }
    }
}
```

Slice 12 modifies exactly the `spacing >= 2` branch, adding the banner
*after* the slice-11 metadata row (if any) and *before* the trailing blank:

```go
if spacing >= 2 {
    b.WriteString("\n"); line++
    row := m.renderTurnMetadataRow(prev.coord, false, false)
    if row != "" {
        b.WriteString(row); b.WriteString("\n")
        line += strings.Count(row, "\n") + 1
    }
    if banner := m.renderBoundaryBanner(prev.coord, item.coord); banner != "" {
        b.WriteString(banner); b.WriteString("\n")
        line += strings.Count(banner, "\n") + 1
    }
    for range spacing - 1 {
        b.WriteString("\n"); line++
    }
    // Fold the metadata row and banner into prev's chatItemLineRanges entry.
    if row != "" || banner != "" {
        key := transcriptLineKey{itemKey: prev.key}
        rng := m.chatItemLineRanges[key]
        rng.end = line - 1
        m.chatItemLineRanges[key] = rng
    }
}
```

### Layout matrix

| Metadata row | Banner | Rows in the gap | Composition |
|---|---|---:|---|
| absent | absent | 2 | `<blank> <blank>` (slice-04 baseline) |
| present | absent | 3 | `<blank> <metadata> <blank>` (slice 11) |
| absent | present | 3 | `<blank> <banner> <blank>` |
| present | present | 4 | `<blank> <metadata> <banner> <blank>` |

The banner **always** renders on a coord change (its input is engine-derived
coordinates that are always available; it never degrades to empty in the way
the metadata row can). The gap therefore grows by one row versus slice 11
alone whenever a coord change occurs. That is the intended layout delta from
this slice and is why the plan lists a targeted update to slice-04's
`TestTranscriptExecutionCoordinateGapPreserved` fixture.

FR-12.5 ("banners shall not stack with the inter-item separator to produce
more than the intended vertical gap") is satisfied by the fixed matrix above
— the banner never emits a duplicate blank of its own. The row growth from
absent-banner to present-banner is exactly `1`, not `2`.

### Line accounting

The banner extends the *closing* item's `chatItemLineRanges` entry to cover
the banner's rendered rows (banners are always single-row, so the extension
is `+1`; the plan does not assume this in code — `strings.Count(banner,
"\n") + 1` is used verbatim). The rule is the same as slice 11's: the
closing item is the story the banner + metadata row are telling; `n`/`N`
should treat both as its trailing chrome and skip past them to the arriving
item.

If the banner alone renders (metadata row is empty), the same fold-into-prev
rule applies. If both are empty (impossible here since banner is always
non-empty on a coord change, but the code path is guarded anyway for
symmetry), no fold occurs and the layout matches slice 04 exactly.

### Non-interaction with the `i == 0` boundary

`visibleExecutionBoundaries` reports `before: 0` when the first loaded item
has a non-zero coordinate — meaning the page starts mid-run and the first
visible item belongs to a non-first turn. FR-12.6 asks the renderer to
respect this. The `itemTranscriptBody` loop does *not* emit spacing before
the first rendered item today; slice 12 therefore adds a small
"page-edge banner" call at the top of the loop, guarded on the same
condition `visibleExecutionBoundaries` uses (`coord != {0,0,0}` on the first
item). The banner emits a single row followed by exactly one blank line, so
the first item still starts on a stable line. No metadata row prepends here
(the *arriving* turn has not "closed" any prior turn on this page).

---

## Architecture and ownership

```text
internal/tui/shared
  └─ rule.go                        (new file)
      • Rule(width int, label string, opts ...RuleOption) string
      • RuleOption: Label styles (labelBold, labelDim); reserved future:
        LeftAnchored(dashes int) for the minor short-rule variant.
      • Uses Theme.Marker and RuleGlyph; no new palette or icon addition.
      • ANSI-safe: width computed with lipgloss.Width, never len().

internal/tui/monitor
  ├─ monitor_transcript_items_view.go (primary edit site)
  │   • Extend the spacing >= 2 branch to emit renderBoundaryBanner
  │     after renderTurnMetadataRow, before the trailing blank.
  │   • Add the page-edge banner call above the loop for the
  │     visibleExecutionBoundaries i==0 case (FR-12.6).
  │   • Extend the prev item's chatItemLineRanges entry to cover the
  │     banner rows (FR-12.7). Uses the same discipline slice 11
  │     established.
  │
  └─ monitor_transcript_banner.go   (new file)
      • renderBoundaryBanner(m *Model, prev, curr toolCorrelationKey) string
      • boundaryLabel(prev, curr toolCorrelationKey) string
      • bannerWidth(m *Model) int   — transcriptInnerW - 2 (prefix width).
      • bannerPrefix()             — "  " (unselected transcript gutter);
        banners never wear the cursor bar so this is always plain two
        spaces regardless of chatItemCursor.
```

Zero new types cross the `internal/tui/monitor` boundary. No transcript,
engine, or harness import is added. The new `shared.Rule` helper joins the
same family as `shared.PanelTopEdge` / `composeBorderBar` (ADR 0001's
titled-border compositors) but is deliberately independent — banners do not
compose a border, they draw an untitled rule with a floating label. Adding a
capLength argument to `composeBorderBar` was considered and rejected: that
helper is border-corner-aware and its label sits between two corner glyphs;
a floating-label rule would require every caller to pass empty corner
glyphs and a zero cap, which is a strictly worse contract.

### Not touched by slice 12

- `internal/engine`, `internal/step`, `internal/transcript`, `internal/harness/*`
  — wire format unchanged (epic CC-12); no new event or field.
- `internal/tui/shared/palette.go` — no new hex token. Reuses `fgMuted`
  (`hexSquid`) through `Theme.Marker` for the rule glyphs and
  `fgDim` (`hexOyster`) through a `Theme.Chat.Hint`-derived bold register
  for the label.
- `internal/tui/shared/icons.go` — `RuleGlyph = "─"` already exists.
  Slice 14's glyph preset table will substitute the ASCII form
  (`RuleGlyph = "-"`) when active; slice 12 must go through the icon
  vocabulary rather than embed a literal `"─"` (epic CC-7).
- `monitor_transcript_metadata.go` — no change; slice 11 is invariant under
  slice 12's addition.
- `monitor_transcript.go` — no change; the removed render-plan surface stays
  gone.
- `monitor_read_group.go` / `monitor_read_group_view.go` — read groups
  never span a coord boundary (grouping stops at
  `sameExecutionCoordinate`), so no read-group renderer change is required.

---

## Delivery phases

Small enough to land as one PR, but sequenced for reviewability. The four
new tests plus the two fixture updates arrive with the code that requires
them, one phase at a time.

### Phase 1 — the rule helper as a pure data function

1. Add `internal/tui/shared/rule.go` with `Rule(width int, label string,
   opts ...RuleOption) string` and its options. No monitor code depends
   on it yet.
2. Table-test the helper: exact width equality; label centered within one
   cell of true center; narrow-width degradation to bare label; empty
   label degrades to a bare rule of `width` glyphs; wide-rune labels
   measured with `lipgloss.Width`; SGR-styled labels preserved after
   composition; ANSI-safe `lipgloss.Width` of the output equals `width`
   in the non-degraded case.

**Exit:** helper is pure, tested, and available for the monitor caller.

### Phase 2 — the boundary label and banner as pure data functions

1. Add `internal/tui/monitor/monitor_transcript_banner.go` with
   `boundaryLabel(prev, curr toolCorrelationKey) string` and
   `renderBoundaryBanner(m *Model, prev, curr toolCorrelationKey) string`.
2. Table-test `boundaryLabel` over: generation bump (any inner values);
   iteration bump alone; attempt bump alone; simultaneous generation +
   iteration bump (generation wins per precedence); simultaneous
   iteration + attempt bump (iteration wins); no-op input returns "".
3. Table-test `renderBoundaryBanner` at widths 12 / 40 / 60 / 80 /
   an intentionally-narrow 8 with a long label to exercise degradation.

**Exit:** the banner is a pure `(width, prev, curr) → string` function
with no dependency on the transcript loop.

### Phase 3 — insertion into the item loop

1. Extend `itemTranscriptBody` per [Where the banner inserts](#where-the-banner-inserts):
   coordinate-boundary emission with line-range extension of the
   preceding item.
2. Add the page-edge (`i == 0`) banner call above the loop, guarded on
   `chatVisibleItems[0].coord != {0,0,0}` (matches
   `visibleExecutionBoundaries` line 126).
3. Add an in-code comment at `boundaryLabel` explaining the 1-indexed
   pass-number vs retry-count convention, referencing this plan.

**Exit:** banners appear at coord changes on the live path; page-edge
banners appear when the loaded page starts mid-run;
`chatItemLineRanges` extends correctly.

### Phase 4 — invariants under existing slice-04 / slice-11 tests

1. Update `TestTranscriptExecutionCoordinateGapPreserved`
   (`monitor_vertical_rhythm_test.go:243-270`) — the gap now contains a
   banner row on a coord change, so the assertion "gap == 2" changes to
   "gap == 3" (banner alone; no metadata row on the fixture) with a
   reviewer-visible comment explaining the slice-12 cause.
2. Audit `monitor_transcript_metadata_visual_test.go` — its
   `twoTurnVisualPage` crosses an iteration boundary, so the interior
   gap now carries both the metadata row and the banner. Update the
   expected ANSI/HTML capture to include the banner row. The audit is
   in-code (regeneration of the deterministic capture) not manual
   editing.
3. Run `go test ./internal/tui/monitor -race -count=1`. Every remaining
   test whose fixture crosses a coord boundary now sees an extra row in
   the transcript body; adjust the small set of explicit `end` /
   `start` assertions on `chatItemLineRanges` for the last item before
   a coord change. Each adjustment is intentional and receives a
   reviewer-visible comment.

**Exit:** `go test ./internal/tui/monitor -race -count=1`, `go test
./...`, `go vet ./...`, and `gofmt -l <changed-go-files>` are clean.

### Phase 5 — proof capture and slice-doc closure

1. Add a deterministic 80-column monitor scene under
   `docs/specs/25-spec-boundary-banners/25-proofs/` (new spec directory
   — this slice adopts the same directory convention as slices 03, 04,
   06, and 11) showing:
   1. An interior iteration boundary — banner sits between two turns of
      one running step, with the slice-11 metadata row above it.
   2. A retry boundary — banner sits after a failed attempt; the failed
      exchange's card retains its error state; the banner label is
      `retry 1`.
   3. A reset boundary — banner labeled `reset 2`; the fixture simulates
      a manual `Run.Reset` by emitting entries with `Generation=1`.
   4. A page-edge banner — the loaded page starts on an entry with
      `Iteration=1, Attempt=1`; `visibleExecutionBoundaries` reports
      `before=0`; the banner renders at the top of the transcript body.
   5. A narrow-terminal degradation — same fixture at 24 columns; the
      banner renders as a bare label.
   Save `.ansi`, `.html`, and `-notes.txt`, matching the slice-04 /
   slice-11 proof format.
2. Update the slice-12 open questions in
   `docs/epics/omp-transcript-parity/slices/12-boundary-banners.md` with
   the resolutions above (Q-12.1: `reset` per schema; Q-12.2: no failure
   reason; Q-12.3: single form for now).
3. Add a cross-link from
   [`docs/plans/open-goals.md`](open-goals.md) T-series entries — none
   currently reference banners; a new "T-banner" line is not needed
   because slice 12 is entirely covered by the omp-parity epic. Verify
   the epic's slice-12 row still references this plan implicitly via
   the epic document.

**Exit:** proofs recorded, epic slice document reflects resolved
questions, `go test ./...` green.

---

## Ordered implementation tasks

Focused-agent wall time in "min"; every substantive code change has a
sibling test task. Task areas are `<Go import path> — <file>` for a clean
mapping.

| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `Rule(width int, label string, opts ...RuleOption) string` and its options in a new file; use `RuleGlyph` and `Theme.Marker`; measure widths with `lipgloss.Width` only | `internal/tui/shared — rule.go` (new) | 25 min |
| 2 | Table-test `Rule` at widths 8/12/24/40/60/80, empty label, wide-rune label, SGR-styled label, degradation cases | `internal/tui/shared — rule_test.go` (new) | 30 min |
| 3 | Add `boundaryLabel(prev, curr toolCorrelationKey) string` with the outermost-changed-wins precedence and the in-code comment explaining the 1-indexed pass vs retry-count anchors | `internal/tui/monitor — monitor_transcript_banner.go` (new) | 15 min |
| 4 | Table-test `boundaryLabel` over generation bump, iteration bump, attempt bump, and simultaneous bumps | `internal/tui/monitor — monitor_transcript_banner_test.go` (new) | 20 min |
| 5 | Add `renderBoundaryBanner(m *Model, prev, curr toolCorrelationKey) string` returning `bannerPrefix() + Rule(bannerWidth(m), label, ...)`; return "" when `boundaryLabel` returns "" | `internal/tui/monitor — monitor_transcript_banner.go` | 15 min |
| 6 | Table-test `renderBoundaryBanner` at multiple `transcriptInnerW` values, including a width that triggers degradation | `internal/tui/monitor — monitor_transcript_banner_test.go` | 25 min |
| 7 | Extend `itemTranscriptBody` per [Where the banner inserts](#where-the-banner-inserts): coord-boundary banner insertion after the metadata row, before the trailing blank, and line-range extension of prev | `internal/tui/monitor — monitor_transcript_items_view.go` | 25 min |
| 8 | Add the page-edge banner call above the loop for the `visibleExecutionBoundaries` `i == 0` case (FR-12.6); guard on `chatVisibleItems[0].coord != {0,0,0}` | `internal/tui/monitor — monitor_transcript_items_view.go` | 15 min |
| 9 | New test `TestBoundaryBannerAtCoordinateChange` seating a two-iteration synthetic page and asserting the banner appears between the two turns with the right label | `internal/tui/monitor — monitor_transcript_banner_test.go` | 25 min |
| 10 | New test `TestBoundaryBannerAtPageEdge` seating a page whose first entry has `Iteration=1, Attempt=1` and asserting the banner renders at the top of the transcript body | `internal/tui/monitor — monitor_transcript_banner_test.go` | 25 min |
| 11 | New test `TestBoundaryBannerAndMetadataRowShareGap` seating a fixture where slice 11 emits a metadata row and slice 12 emits a banner on the same coord change; assert composition matches the layout matrix | `internal/tui/monitor — monitor_transcript_banner_test.go` | 30 min |
| 12 | New test `TestBoundaryBannerLineRangesFoldIntoPrevItem` seating a settled multi-turn page and asserting `chatItemLineRanges[prev].end` covers the banner's row so `n`/`N` navigation still lands on the arriving item | `internal/tui/monitor — monitor_transcript_banner_test.go` | 25 min |
| 13 | New test `TestBoundaryBannerDegradesOnNarrowWidth` seating a page at `transcriptInnerW = 10` with an `iteration 12` label and asserting the banner renders as the bare label without a broken rule | `internal/tui/monitor — monitor_transcript_banner_test.go` | 20 min |
| 14 | New test `TestBoundaryBannerLabelUsesResetForGeneration` grep-asserting that a generation transition renders `reset N`, not `re-run N`, so a future regression to the retired vocabulary fails loudly | `internal/tui/monitor — monitor_transcript_banner_test.go` | 15 min |
| 15 | New test `TestBoundaryBannerRoutesThroughSharedRuleGlyph` grep-asserting that `monitor_transcript_banner.go` contains no bare `"─"` literal and every rule glyph is sourced from `shared.RuleGlyph` (epic CC-7) | `internal/tui/monitor — monitor_transcript_banner_test.go` | 15 min |
| 16 | Update `TestTranscriptExecutionCoordinateGapPreserved` in `monitor_vertical_rhythm_test.go` — the gap now contains a banner row on a coord change; adjust the expected gap from 2 to 3 (banner-only path) with a reviewer-visible comment naming this slice | `internal/tui/monitor — monitor_vertical_rhythm_test.go` | 15 min |
| 17 | Regenerate the deterministic ANSI/HTML captures in `monitor_transcript_metadata_visual_test.go`'s `twoTurnVisualPage` scenes to include the banner row; adjust any asserted-line counts | `internal/tui/monitor — monitor_transcript_metadata_visual_test.go` | 25 min |
| 18 | Add the slice-12 visual proof `docs/specs/25-spec-boundary-banners/25-proofs/` with the five scenes listed in Phase 5 | `docs/specs/25-spec-boundary-banners/25-proofs/` (new) | 45 min |
| 19 | Mark Q-12.1 / Q-12.2 / Q-12.3 answered in the slice document with the resolutions above | `docs/epics/omp-transcript-parity/slices/12-boundary-banners.md` | 10 min |

Estimated focused implementation time: **6.5 hours**, one PR.

---

## Test matrix

### New unit tests (Phase 1 and Phase 2)

| Test | Purpose | Fixture shape |
|---|---|---|
| `TestRuleExactWidth` | `lipgloss.Width(Rule(width, label))` equals `width` for every representative width, non-degraded case | table over widths 12/24/40/60/80 |
| `TestRuleCentersLabelWithinOneCell` | `capLeft` and `capRight` differ by at most 1 cell | same table |
| `TestRuleDegradesToBareLabelBelowMinimum` | Below `labelWidth + 2*minCap + 2` the helper returns the bare label with no fill | width < minimum |
| `TestRuleAcceptsSGRStyledLabel` | Label pre-wrapped in an SGR sequence still produces a rule of exact visible width | wide-rune + SGR-styled label |
| `TestBoundaryLabelPrecedenceGenerationWinsOverIteration` | Simultaneous bumps render the outermost | `prev = {0,0,0}`, `curr = {1,0,0}` |
| `TestBoundaryLabelPrecedenceIterationWinsOverAttempt` | Same, one level deeper | `prev = {0,0,0}`, `curr = {0,1,0}` |
| `TestBoundaryLabelReturnsEmptyOnNoChange` | No coord change → no label → no banner | `prev == curr` |
| `TestBoundaryLabelUsesResetForGeneration` | Regression: never render `re-run` | any generation transition |
| `TestRenderBoundaryBannerRoundtrips` | Composes prefix + rule + label at the model's `transcriptInnerW` | model at 60 columns |

### New behavioral tests (Phase 3)

| Test | Purpose | Fixture shape |
|---|---|---|
| `TestBoundaryBannerAtCoordinateChange` | Banner renders between two turns of a running step | 3-item page across an iteration bump |
| `TestBoundaryBannerAtPageEdge` | Banner renders at the top of the transcript when the page begins mid-run | 1-item page whose first item has `iteration=1, attempt=1` |
| `TestBoundaryBannerAndMetadataRowShareGap` | Both slice 11 and slice 12 emit; composition matches the layout matrix | 2-turn page with timestamps |
| `TestBoundaryBannerLineRangesFoldIntoPrevItem` | `chatItemLineRanges[prev].end` covers the banner row so `n`/`N` still lands on the arriving item | any coord-change fixture |
| `TestBoundaryBannerDegradesOnNarrowWidth` | Narrow width renders bare label, not a broken rule | 10-column terminal, long label |
| `TestBoundaryBannerRoutesThroughSharedRuleGlyph` | Grep the file for bare `"─"` literals; assert none | file text |
| `TestBoundaryBannerNotSelectable` | The banner never enters `chatVisibleItems`, so `chatItemCursor` cannot land on it; a synthetic sweep of `n` skips it | 4-item page across a coord change |

### Regression tests to re-run — some unchanged, some deliberately updated

| Test | File | Expected outcome |
|---|---|---|
| `TestTranscriptExecutionCoordinateGapPreserved` | `monitor_vertical_rhythm_test.go` | **Updated** — gap grows from 2 → 3 rows on a coord change with a banner-only path. Reviewer-visible comment names this slice. |
| `TestTranscriptZeroHeightItemContributesNothing` | same | passes unchanged — the banner is not an item |
| `TestTranscriptTrimsStructuralEdgeBlanksBetweenItems` | same | passes unchanged — banner is emitted between the trim and the next item, so its bytes are content, not structural blank |
| `TestTranscriptLineRangesMatchRenderedRows` | same | passes with the intentional numeric delta on coord changes; adjust the small set of asserted `end` values with reviewer-visible comments |
| `TestVisibleExecutionBoundariesUseVisibleItemCoordinates` | `monitor_transcript_items_test.go` | passes unchanged — this slice does not modify the reporter |
| `TestTurnMetadataRowAtCoordinateBoundary` | `monitor_transcript_metadata_test.go` | passes unchanged — banner sits after the row and does not affect its rendering |
| `TestTurnMetadataRowLineRangesCoverAppendedRow` | same | **Updated** — the folded range now covers metadata + banner. Reviewer-visible comment names this slice. |
| `TestVerticalRhythmVisualProof` / `TestTurnMetadataRowVisualProof` | `monitor_transcript_metadata_visual_test.go` | Regenerate ANSI/HTML captures; the banner row appears in the interior-boundary scene |
| `TestToolExchangeHeaderCardStatesAndWidths` | `monitor_transcript_items_view_test.go` | passes unchanged — card output unchanged |
| `TestTranscriptCardLineRangesCachedAndFresh` | `monitor_transcript_card_test.go` | passes unchanged for fixtures without a coord change; adjust the small set of asserted ranges for fixtures that cross one |

### Release verification

```bash
gofmt -l -w <changed-go-files>
go test ./internal/tui/monitor -race -count=1
go test ./internal/tui/shared -race -count=1
go test ./internal/tui/... -race -count=1
go test ./...
go vet ./...
go build ./cmd/jig
go run ./cmd/jig validate .agents/jig/sdd.toml
```

`gofmt -l` on the changed files must be empty. Terminal-capture proofs are
regenerated by rerunning the relevant `*VisualProof` tests with
`JIG_UI_SNAPSHOT_DIR` set.

---

## Security and failure handling

- **No new sensitive surface.** The banner exposes only integer coordinates
  (`generation`, `iteration`, `attempt`) that are already visible in the
  Steps panel per-step chrome and in the transcript's per-turn metadata row.
  It does not read the environment, the filesystem beyond the already-loaded
  transcript page, the scheduler, or any prior-attempt failure reason.
- **No export path change.** The banner renders in the Monitor's Transcript
  panel, which is not part of any exported artifact under `internal/runexport`.
  If a future run share/export contract includes rendered transcript body,
  the export layer owns any redaction; slice 12 does not add per-turn labels
  or coordinate metadata to any export.
- **Persistence-off.** With `RunDir == ""` the transcript loop is not
  entered (`chatBody` short-circuits to an empty state); the banner is
  therefore automatically absent. No new branch is required for the
  persistence-off path.
- **Journal replay after crash.** `transcript.Entry.{Generation, Iteration,
  Attempt}` are durable in `transcript.jsonl` and rebuilt into
  `transcriptItem.coord` on reopen by `buildTranscriptItems` — so the banner
  renders identically on a resumed process as on a live one. No new
  persistence is introduced.
- **Partial pages.** A page edge can hide the first entry of a turn (the
  `visibleExecutionBoundaries` `i == 0` case). The banner renders when the
  first visible item's coord is non-zero, mirroring the reporter's existing
  contract. If a coord change straddles a page boundary such that the
  reporter emits `before: N > 0`, the interior branch of the loop handles it
  naturally.
- **Unicode / narrow terminals.** The rule glyph goes through the icon
  vocabulary (`shared.RuleGlyph`), so slice 14's ASCII preset table
  substitutes `"-"` for `"─"` automatically when the terminal cannot render
  the block-drawing character. The banner never embeds a bare `"─"` literal
  (regression-tested by `TestBoundaryBannerRoutesThroughSharedRuleGlyph`).
- **Wide-rune labels.** The rule helper measures with `lipgloss.Width` and
  degrades to the bare label when the panel is too narrow, so no wide rune
  in a future label can cause the rule to overflow the transcript width.

---

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| `chatItemLineRanges` drifts, breaking `n`/`N` navigation | `TestBoundaryBannerLineRangesFoldIntoPrevItem` asserts the invariant on every emission path; the banner extends the *previous* item's range rather than owning its own key — same discipline slice 11 established. |
| Slice 11's metadata row and slice 12's banner produce the wrong visual order | `TestBoundaryBannerAndMetadataRowShareGap` locks the layout matrix; the code composes them in a single branch so the order is not scattered. |
| A test asserts the exact `lineRange.end` for the last item before a coord change | Tasks 16–17 update the small set of affected assertions with in-line comments linking to this plan; each update is a reviewed intentional delta. |
| The retired `re-run N` vocabulary regresses if a future refactor copies it back | `TestBoundaryBannerLabelUsesResetForGeneration` fails loudly on any regression to the retired string. |
| A bare `"─"` glyph literal creeps back at a monitor call site (violates epic CC-7) | `TestBoundaryBannerRoutesThroughSharedRuleGlyph` grep-audits the file. |
| The banner width miscomputes on selection (four-cell selected prefix regression) | Slice 03 already landed the fixed-width two-cell gutter; `bannerPrefix()` always returns `"  "` and the banner is not selectable, so no cursor bar ever inflates it. |
| The rule renders as a broken row on very narrow terminals | `TestBoundaryBannerDegradesOnNarrowWidth` asserts the bare-label degradation; the `Rule` helper computes the minimum width once and returns the bare label below it. |
| Off-by-one confusion causes the banner to read `iteration 1` for the second pass instead of `iteration 2` | In-code comment at `boundaryLabel` documents the 1-indexed pass-number and 1-indexed retry-count anchors, plus `TestBoundaryLabelPrecedence*` covers the arithmetic. |
| Wide-rune labels overflow the rule | `Rule` measures with `lipgloss.Width`, never `len()`. Table-tested with a wide-rune fixture in Phase 1. |
| A future slice adds a new coord component (e.g. sub-attempts under recovery) and `boundaryLabel` silently ignores it | Adding a new coord field to `toolCorrelationKey` is a repo-wide change that will surface at every reference; `boundaryLabel` is the smallest such site and the precedence table is the canonical list. Explicitly documented in the in-code comment. |

---

## Completion criteria

Slice 12 is done when all of the following hold against a real multi-attempt,
multi-iteration run with at least one manual reset:

1. **CC-12.1** — Every coordinate transition between adjacent visible
   items emits exactly one centered, width-aware labeled rule in the
   two-line execution-coordinate gap, spanning `transcriptInnerW - 2`
   visible cells.
2. **CC-12.2** — A loaded page whose first visible item's coord is
   non-zero emits a banner at the top of the transcript body (FR-12.6).
3. **CC-12.3** — A terminal width that cannot accommodate a rule renders
   the bare label with the same two-space prefix; no broken row appears.
4. **CC-12.4** — Every banner label uses the schema vocabulary: `reset
   N` for generation, `iteration N` for iteration, `retry N` for attempt.
   `re-run` appears nowhere.
5. **CC-12.5** — When both a slice-11 metadata row and a slice-12 banner
   render on the same coord change, they compose per the layout matrix:
   `<blank> <metadata> <banner> <blank>` (4 rows in the gap), not more.
6. **CC-12.6** — `chatItemLineRanges` continues to satisfy `end - start
   + 1 == rendered rows for that item + trailing metadata rows + trailing
   banner rows` for every item and every emission path; `n`/`N` block
   navigation lands exactly on items, never on the metadata row or the
   banner.
7. **CC-12.7** — `internal/tui/monitor/monitor_transcript_banner.go`
   contains no bare `"─"` glyph literal; every rule glyph flows through
   `shared.RuleGlyph` (epic CC-7).
8. **CC-12.8** — `shared.Rule` measures widths with `lipgloss.Width`
   only; no code path uses `len()` for a visible-cell calculation.
9. **CC-12.9** — Persistence-off runs still render the empty transcript
   state; no `RunDir == ""` regression is introduced.
10. **CC-12.10** — `go build ./cmd/jig`, `go test ./...`, `go test -race
    ./internal/tui/...`, `go vet ./...`, `gofmt -l <changed-go-files>`
    (empty), and `go run ./cmd/jig validate .agents/jig/sdd.toml` pass.
11. **CC-12.11** — Terminal-capture proofs under
    `docs/specs/25-spec-boundary-banners/25-proofs/` include the five
    documented scenes and the banner appears in each expected position.
12. **CC-12.12** — The slice document
    [`slices/12-boundary-banners.md`](../epics/omp-transcript-parity/slices/12-boundary-banners.md)
    records Q-12.1 / Q-12.2 / Q-12.3 as answered by the resolutions
    above.
