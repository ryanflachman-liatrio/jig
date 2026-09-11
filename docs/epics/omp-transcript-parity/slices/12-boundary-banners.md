# Slice 12 — Centered boundary banners

- **Slice ID:** `boundary-banners`
- **Outcome:** Iteration, retry, and re-run boundaries render as centered
  full-width rules with an inline label, replacing the current left-anchored
  fixed-width markers.
- **Why this slice exists:** Small, self-contained, independent of everything
  else, and it fixes a real defect — the current banners are a fixed-width string
  that does not adapt to the panel.
- **Depends on:** None.

---

## Current jig state

The banners are constructed in `rebuildActiveState`
(`internal/tui/monitor/monitor_transcript.go:506-523`) — which slice 00 deletes,
because it feeds the unreachable render plan:

```go
if lastGen != -1 && e.Generation > lastGen {
    m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
        kind: renderEntrySep,
        sep:  fmt.Sprintf("── re-run %d ──", e.Generation+1),
    })
}
if lastIter != -1 && e.Iteration > lastIter {
    ... fmt.Sprintf("── iteration %d ──", e.Iteration+1)
}
if lastAttempt != -1 && e.Attempt > lastAttempt {
    ... fmt.Sprintf("── retry %d ──", e.Attempt)
}
```

rendered at `:796` as `"\n  " + Marker.Render(sep) + "\n\n"`.

Two problems:

1. **They are dead.** After slice 00 they are gone entirely, so the live item
   renderer has **no boundary markers at all** — only `itemSpacingBefore`'s
   two-line gap (`monitor_transcript_items.go`). This slice restores them on the
   live path.
2. **Fixed width.** `── iteration 2 ──` is a literal string; it does not span or
   center in the panel.

The live path does have the *detection* logic already:
`visibleExecutionBoundaries` (`monitor_transcript_items.go`) walks the item list
and reports every generation/iteration/attempt transition as a
`transcriptBoundary{before int, coord toolCorrelationKey}`. Its doc comment is
explicit about the separation of concerns:

> *"transcriptBoundary is a presentation-neutral execution transition. Renderers
> decide how to style it; normalization only reports changes visible in the
> loaded and filtered item sequence."*

**The detection exists and is unused by the renderer.** This slice is wiring it
up plus a rule-drawing helper.

---

## omp reference

`packages/coding-agent/src/modes/components/compaction-summary-message.ts:57-76`
— one renderer shared by `/compact`, handoff, and branch summaries *"so every
history-collapse point reads as one consistent banner"*:

```ts
const rule = theme.tree.horizontal;             // "─"
const hint = `${theme.sep.dot.trim()} ctrl+o`;  // "· ctrl+o"
const plainWidth = width(`${label} ${hint}`);
const remaining  = width - plainWidth - 2;
if (remaining < 4) return theme.fg("muted", label);   // too narrow: bare label
const left = Math.floor(remaining / 2), right = remaining - left;
return theme.fg("dim", rule.repeat(left))
     + ` ${theme.fg("muted", label)} ${theme.fg("dim", hint)} `
     + theme.fg("dim", rule.repeat(right));
```

At width 60:

```
────────────────── 📷 compacted · ctrl+o ───────────────────
```

With a badge and warning:

```
────────── 📷 remote-compacted · 256K→20K ⚠ · ctrl+o ───────────
```

Structure: `["", divider, ""]` — the outer blanks are plain-blank and get trimmed
by the container (slice 04), which then restores exactly one blank on each side.

Details worth copying:

- **Centered**, with a single space on each side of the label group.
- **Degrades to a bare label** when the panel is too narrow for a rule.
- The rule is `dim`, the label `muted`, the hint `dim` — three registers.
- An **expand affordance folded into the banner** (`· ctrl+o`), because the
  banner can reveal detail.

### The left-anchored short-rule variant

`cache-invalidation-marker.ts:89-111` uses a fixed 10-cell rule for lighter
markers:

```
────────── ⊘ cache miss · 12.3K tokens
```

Worth having both: centered for major boundaries (re-run, iteration), left
short-rule for minor ones.

---

## In Scope

- A `shared.Rule(...)` helper producing a centered, width-aware labeled rule with
  the narrow-panel degradation, plus a left-anchored short-rule variant.
- Wire `visibleExecutionBoundaries` into `itemTranscriptBody` so boundaries
  render on the live path.
- Preserve the current label wording (`re-run N`, `iteration N`, `retry N`) and
  the existing off-by-one conventions: generation and iteration display `+1`,
  attempt displays raw. **Verify these against the engine's semantics** — the
  inconsistency is suspicious and may itself be a bug.
- Glyphs through the icon vocabulary (CC-7).
- Coordinate spacing with slice 04 so the banner sits inside the existing
  two-line execution-coordinate gap rather than adding to it.

## Out of Scope

- Expandable banner detail (omp's `ctrl+o` affordance). jig's boundaries have no
  hidden detail to reveal.
- Compaction/handoff banners — jig has no compaction model.
- Changing when a boundary occurs; `visibleExecutionBoundaries` already decides.

## Functional Requirements

- **FR-12.1** A generation, iteration, or attempt transition between adjacent
  visible items shall render a labeled rule.
- **FR-12.2** The rule shall span the panel's content width with the label
  centered.
- **FR-12.3** When the width cannot accommodate a rule, the system shall render
  the bare label rather than a broken rule.
- **FR-12.4** The rule and label shall use distinct styles.
- **FR-12.5** Banners shall not stack with the inter-item separator to produce
  more than the intended vertical gap.
- **FR-12.6** A boundary at the very start of a loaded page shall render when the
  coordinate is non-zero, matching `visibleExecutionBoundaries`' existing
  behavior for `i == 0`.
- **FR-12.7** Banner lines shall be accounted for in `chatItemLineRanges` so
  navigation offsets stay correct.

## Technical and Repository Constraints

- `visibleExecutionBoundaries` returns `before` as an **index into the item
  slice**, so the renderer must insert the banner before rendering that item —
  and the line accounting in `itemTranscriptBody` must include those lines.
  FR-12.7 is the easy thing to get wrong here.
- The helper must be width-aware via `lipgloss.Width`, never `len()`.
- `shared.Theme.Marker` and `shared.RuleGlyph` already exist — reuse them.
- Slice 04 owns the surrounding blank lines; this slice owns the rule itself.
  Land them in a known order or the gap will be wrong once.

## Security and Data Considerations

None identified. Labels are engine-derived integers and static text.

## Acceptance Evidence

- Tests: the rule is exactly the content width; the label is centered within one
  cell of true center; a narrow width degrades to a bare label.
- A test asserting an iteration transition renders a banner on the live item
  path.
- A test asserting `chatItemLineRanges` offsets remain correct with a banner
  present.
- A test asserting the total blank lines around a banner match the intended gap.

## Inputs for the Child Spec

- `visibleExecutionBoundaries` already exists, is tested, and is deliberately
  presentation-neutral. **This slice is its first consumer** — do not duplicate
  its logic.
- Slice 00 deletes the old banner strings; this slice reintroduces them on the
  live path. If slice 00 has not landed, note that the old ones were unreachable.
- The `+1` / raw off-by-one inconsistency between iteration and attempt is worth
  verifying rather than copying.

## Open Questions

- **Q-12.1** Are the current labels right? `re-run N` for generation is jargon;
  the workflow schema may have better terms. *Check `docs/workflow-schema.md`.*
- **Q-12.2** Should a retry banner carry the failure reason that caused it?
  *Suggest no — the failed step's own card carries it, and a banner should stay
  scannable.*
- **Q-12.3** Do minor boundaries warrant the left-anchored short-rule variant, or
  is one centered form enough? *Suggest one form until there is a second kind of
  boundary.*
