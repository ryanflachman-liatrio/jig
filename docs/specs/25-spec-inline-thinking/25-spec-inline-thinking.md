# 25-spec-inline-thinking.md

## Introduction/Overview

Render agent reasoning as italic, muted markdown prose inline with the rest of
the Monitor transcript instead of a collapsed one-line `◇ reasoning` stub, and
replace the static running-step label with a fixed-width animated pulse driven
by the existing 100 ms frame loop. Reasoning is the most informative content a
workflow agent produces; hiding it behind a collapse defeats the point of
showing it at all, and a static label makes a long-running step look stalled.

Source: [OMP transcript parity, Slice 10](../../epics/omp-transcript-parity/slices/10-inline-thinking.md).
Dependencies: Slice 06 supplies the oversized-collapse vocabulary and helpers
this spec extends to thinking blocks; Slice 09 supplies the default-expand
precedent (`defaultExpandEditCodeItems`) this spec follows for item-kind
defaults. This spec does not depend on Slice 13 (liveness/spinners): Slice 13
is not yet implemented, so the pulse in this spec is scoped to quantized,
frame-loop-native frames rather than Slice 13's eventual shared ticker
abstraction; see Technical Considerations.

Two of the epic slice's cited line references are stale relative to the
current codebase and are corrected here: the static `"typing…"` label at the
slice's cited `monitor_transcript.go:850` no longer exists (it was output-file
placeholder text, not a running-step indicator, and there is currently no
running-step label or pulse anywhere in the transcript view); and
`transcript.BlockThinking` is already populated consistently by both harnesses
(`internal/harness/claude.go:377-378` and `internal/harness/acp.go:615`, both
funneling through `internal/runner/agent.go:352`), resolving the slice's
Q-10.3 with no gap to address.

## Goals

- Make reasoning readable by default, at the same horizontal offset and
  through the same markdown pipeline as ordinary assistant prose, differing
  only in style (italic, muted).
- Keep very long reasoning bounded using the existing oversized-collapse
  mechanism rather than a new one.
- Give a running step a legible, animated indicator that is quantized to the
  existing 100 ms repaint cadence, with zero new tickers or animation loops.
- Preserve existing selection, expansion-toggle, copy, search, and
  `chatItemLineRanges` behavior for thinking items.

## User Stories

- **As an operator reading an agent's transcript**, I want reasoning to read
  as quiet prose in the main column so that I can follow the agent's thinking
  without an extra keystroke per block.
- **As an operator watching a long-running step**, I want a legible animated
  indicator so that I can tell the step is still alive without staring at an
  unchanging label.
- **As an operator on a plain or non-animating terminal**, I want the running
  indicator's text label to remain legible even if the glyph never appears to
  change, so that the state is never conveyed by animation alone.
- **As an operator reviewing a step with unusually long reasoning**, I want
  that block to collapse to a summary the same way an oversized user message
  does, so that one verbose block cannot dominate the transcript.

## Demoable Units of Work

### Unit 1: Inline thinking prose

**Purpose:** Replace the collapsed `◇ reasoning` stub with italic, muted
markdown prose rendered inline at the same offset as assistant text, bounded
by the existing oversized-collapse mechanism for unusually long blocks.

**Functional Requirements:**

- **FR-10.1:** A thinking block shall render as markdown prose in an italic,
  muted style at the same horizontal offset as assistant prose. The block
  shall route through a dedicated glamour renderer variant configured with
  `Theme.Chat.Thinking`'s italic/muted styling on the renderer's document
  style, following the existing `chatStyle`/`fileStyle`/`insetRenderer`
  pattern in `rebuildRenderer` (`internal/tui/monitor/monitor_layout.go:219-256`).
  Post-styling glamour's rendered ANSI output is out of scope: it fights the
  ANSI markdown rendering already contains.
- **FR-10.2:** A thinking block at or under the oversized-collapse threshold
  shall render fully expanded with no collapse marker, matching how
  under-threshold user text already renders unconditionally today. This
  requires narrowing `itemHasDetail`'s current unconditional `true` for
  `transcriptItemThinking` (`internal/tui/monitor/monitor_transcript_items_view.go:330-332`)
  to the same oversized-gated rule already applied to
  `transcriptItemText`.
- **FR-10.3:** A thinking block exceeding the collapse threshold shall render
  as a dim summary row when collapsed and its full prose when expanded, reusing
  the Slice 06/09 oversized mechanism (`chatTextCollapseBytes`,
  `collapseSummaryLabel`, `buildCollapseSummary` in
  `internal/tui/monitor/monitor_transcript_collapse.go`) rather than a
  parallel threshold or summary format. The existing oversized condition
  (`internal/tui/monitor/monitor_transcript_items.go:28`, currently gated to
  `transcriptItemText` and `transcript.RoleUser`) shall extend to thinking
  blocks regardless of role.

**Proof Artifacts:**

- Test: a thinking block under the collapse threshold renders its full
  markdown content with no expansion action, demonstrating FR-10.1/FR-10.2.
- Test: the rendered thinking output carries the italic escape sequence and
  the muted foreground from `Theme.Chat.Thinking`, demonstrating FR-10.1.
- Test: a thinking block over the collapse threshold renders the shared
  collapse-summary row when collapsed and full prose when expanded, and its
  collapse state persists across a reload of the same step the way
  `chatItemExpand` already persists for other kinds, demonstrating FR-10.3.

### Unit 2: The running-step pulse

**Purpose:** Replace the absence of any running-step indicator with a
fixed-width animated pulse that runs only while a step is executing, quantized
to the existing 100 ms frame loop with no second ticker.

**Functional Requirements:**

- **FR-10.4:** While a step is running, its currently active thinking item
  (the trailing thinking block with no following item, mirroring
  `standaloneToolItem`'s existing trailing-without-result running check at
  `internal/tui/monitor/monitor_transcript_items.go:193-197`) shall render an
  animated pulse label in place of the plain `reasoning` stub text. The label
  format is `<pulse glyph> reasoning`, using the same position the static
  label occupies today.
- **FR-10.5:** Every pulse frame shall occupy exactly one visible cell
  (`lipgloss.Width` equal across all frames in both the default and
  ASCII-fallback glyph sets), so neither the label nor anything after it
  shifts as it animates.
- **FR-10.6:** The pulse shall always render its persistent text label
  (`reasoning`) alongside the glyph, so the running state stays legible on a
  non-animating terminal or for a screen reader, independent of glyph
  animation.
- **FR-10.7:** A thinking item is visible in the transcript's default view
  only while it is the running step's active trailing block. The moment a
  step advances past it (any thinking block that is not the running step's
  trailing item), the block is dropped from the default display list
  (`filteredTranscriptItems`) entirely — it does not persist with a settled,
  non-animated label. Reasoning is a live-progress signal, not part of the
  durable conversation record. The operator can still opt into settled
  reasoning by enabling the existing `reasoning` transcript filter
  (`internal/tui/monitor/monitor_search.go`), which surfaces it like any
  other filtered content; the drop is a display-only default, not a change to
  the underlying transcript item sequence that read-exchange grouping and
  search operate over.
- **FR-10.8:** The pulse shall render a single-cell ASCII fallback glyph set
  under the repository's existing ASCII-fallback configuration, matching the
  qualification already used for other glyph pairs pending Slice 14's preset
  table (e.g. `internal/tui/monitor/monitor_transcript_items_view.go`'s tree
  glyphs: "Unicode and any already-supported fallback configuration").

**Proof Artifacts:**

- Test: `lipgloss.Width` is equal across every frame in the default pulse
  glyph set and equal across every frame in its ASCII-fallback set,
  demonstrating FR-10.5.
- Test: the pulse frame selected for a given `TickMsg` timestamp is a pure,
  deterministic function of that timestamp (no package-level counter or
  second ticker), demonstrating quantization to the existing 100 ms frame
  loop.
- Test: a thinking item that is no longer the running step's trailing item
  does not appear in the default visible transcript items, and does not
  render at all, demonstrating FR-10.7.
- Test: the ASCII-fallback configuration produces a non-empty, single-cell
  glyph for every pulse frame, demonstrating FR-10.8.

## Non-Goals (Out of Scope)

1. **The tokens/sec speed badge:** jig's transcript does not carry per-block
   token deltas; adding them touches the harness contract and is deferred
   until a slice surfaces per-step token counts.
2. **A reasoning-visibility toggle:** there is no `ctrl+T`-style display-mode
   switch in this spec; reasoning is always visible subject to the existing
   oversized-collapse mechanism.
3. **Slice 13's shared ticker or eased dwell timing:** the pulse in this spec
   quantizes to the existing 100 ms frame loop. A smoother, eased 70-230 ms
   dwell schedule is deferred until Slice 13 lands a shared animation
   abstraction; this spec introduces no second ticker to get there sooner.
4. **Slice 14's general glyph-preset table:** this spec adds a pulse-specific
   default/ASCII glyph pair using the same qualified phrasing other pending
   glyphs already use; it does not build the general preset infrastructure.
5. **Any change to `internal/transcript`, harness event mapping, or
   persisted data:** both harnesses already populate `transcript.BlockThinking`
   correctly; this is a pure Monitor presentation change.
6. **The numeric speed/rate badge's color easing:** out of scope along with
   the badge itself.

## Design Considerations

- Reasoning prose occupies the same column as assistant text; it is not
  boxed, bordered, or headed by a label beyond the existing `reasoning` text.
- The pulse glyph and its label occupy the exact position the current static
  `◇ reasoning` label occupies; only the running-item variant changes.
- No specific design requirements beyond matching the omp reference's
  "quieter voice in the same column" behavior described in the epic slice.

## Repository Standards

- Follow `AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/CONVENTIONS.md`,
  `docs/TESTING.md`, `docs/TUI.md`, and the transcript vocabulary in
  `CONTEXT.md`.
- Reuse `Theme.Chat.Thinking`, the Slice 06/09 oversized-collapse helpers, and
  the `defaultExpandEditCodeItems`-style per-item-kind default pattern rather
  than inventing parallel mechanisms.
- All new glyphs belong in `internal/tui/shared`'s icon vocabulary, not as
  literals at Monitor call sites; styling belongs in `shared.Styles` using
  existing semantic palette tokens.
- Prefer table-driven unit tests for the pulse frame/width/ASCII-fallback
  behavior and model-message or render-fixture tests for the collapse and
  default-visibility behavior.
- Format only changed Go files with `gofmt -w`. Required verification
  includes targeted Monitor tests, `go test ./...`, and `go vet ./...`.

## Technical Considerations

- Add a dedicated glamour renderer variant (alongside `m.renderer`,
  `m.fileRenderer`, `m.insetRenderer` in `rebuildRenderer`) with its
  `ansi.StyleConfig.Document`/`StylePrimitive` italic flag and muted color set
  to match `Theme.Chat.Thinking`, built once per renderer rebuild rather than
  per render.
- Extend the oversized condition in
  `internal/tui/monitor/monitor_transcript_items.go:28` to cover
  `transcriptItemThinking` regardless of role, and narrow
  `itemHasDetail` (`monitor_transcript_items_view.go:330-332`) so a thinking
  item only carries a collapse marker when oversized, matching the existing
  text-item rule instead of thinking's current unconditional marker.
- Derive the trailing/active thinking item's running state the same way
  `standaloneToolItem` already derives a trailing tool item's running state
  (`monitor_transcript_items.go:193-197`): the step's `stepRunning` flag plus
  "this item has no item after it in the built sequence."
- The pulse frame index must be a pure function of the `TickMsg` timestamp
  already delivered by the existing 100 ms `monitorTickCmd`
  (`internal/tui/monitor/monitor_frame.go:18-23`) — e.g.
  `int(t.UnixMilli()/100) % len(frames)` — so no new counter field, ticker, or
  animation loop is introduced. This satisfies the epic slice's "do not
  introduce a second independent animation loop" constraint given that Slice
  13's shared ticker does not yet exist.
- Making reasoning visible by default increases rendered height. Verify
  `chatItemLineRanges` and scroll-follow behavior against a fixture with long
  reasoning content once thinking blocks default-render at full length.
- No latest-technology standards research is required. The feature introduces
  no new external dependency, protocol, storage format, or framework; the
  applicable current contracts are glamour, lipgloss, and the repository's
  existing Go/Charm v2/TUI conventions already reviewed above.

## Security Considerations

Reasoning text is model output that is already redacted and clamped upstream
by the existing transcript pipeline; this spec introduces no new data path.
Making it visible by default means it now appears on screen without an
explicit expand action, which is relevant if an operator screen-shares during
a run — worth noting for operator awareness, not a new security control to
build.

## Success Metrics

1. **Legibility:** reasoning under the collapse threshold is fully visible
   with zero required keystrokes; only oversized reasoning requires
   expansion.
2. **Liveness:** a running step's active thinking item visibly animates at
   the existing 100 ms cadence with an equal-width glyph and a persistent
   text label at every frame.
3. **Behavioral integrity:** FR-10.1 through FR-10.8 all have deterministic
   test evidence, including equal-width and ASCII-fallback glyph checks.
4. **Quality:** targeted Monitor tests, `go test ./...`, `go vet ./...`, and
   changed-file formatting checks pass with no regression to existing
   thinking-item selection, copy, search, or line-range behavior.

## Open Questions

1. Default-visible reasoning substantially lengthens transcripts for
   reasoning-heavy models. This spec follows the epic slice's own
   recommendation (default-visible, bounded by the collapse threshold) as a
   non-blocking assumption; revisit if operators find transcripts too long in
   practice.
2. The exact pulse glyph sequence (e.g. a starburst-style rotation) is an
   implementation detail left to Phase 3, constrained only by FR-10.5's
   equal-width requirement and FR-10.8's ASCII fallback; it does not affect
   scope or acceptance criteria.
