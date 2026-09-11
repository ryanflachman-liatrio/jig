# Slice 04 — Vertical rhythm and block edge trimming

- **Slice ID:** `vertical-rhythm-and-block-edges`
- **Outcome:** Transcript spacing follows one stated rule; blocks that render
  nothing consume no vertical space; and a card's own padding does not stack with
  the inter-item separator.
- **Why this slice exists:** Once items become cards (slice 01) they carry
  intrinsic padding, and the current per-kind spacing table will double it. The
  rule that makes omp's transcript feel even is small and worth stating once,
  centrally, rather than tuning per item kind forever.
- **Depends on:** Slice 01.

---

## omp reference

### The whole rule

`packages/coding-agent/src/modes/components/transcript-container.ts:532-545`:

```ts
for (const entry of this.#entries) {
    const block = this.#renderEntry(entry, width);
    if (block.length === 0) continue;      // empty blocks contribute NOTHING
    if (rows.length > 0) rows.push("");    // ← the single separator
    this.#childStartRows.set(entry.component, rows.length);
    rows.push(...block);
}
```

**Exactly one blank line between top-level blocks. That is the entire rule.**
The same logic is repeated at `:314-319`, `:523-525`, and `:636-638`.

Notably there is **no extra gap** between a message and its tool calls, or
between consecutive tool calls. The rhythm is uniform:

```
[user bubble]
<blank>
[assistant prose]
<blank>
[tool call #1]
<blank>
[tool call #2]
<blank>
[metadata row]
```

### The trick that makes it work

Components liberally prepend `new Spacer(1)` — the metadata row does
(`usage-row.ts:93`), message frames do, execution footers do. Those edge spacers
are then **discarded at the top level**, `transcript-container.ts:128-134`:

```ts
export function trimBlankEdges(rows: readonly string[]): readonly string[] {
    let start = 0; let end = rows.length;
    while (start < end && isPlainBlank(rows[start]!)) start++;
    while (end > start && isPlainBlank(rows[end - 1]!)) end--;
    ...
}
```

with (`:106-108`):

```ts
const isPlainBlank = (line: string) => !/\S/.test(line);
```

**The subtlety worth the whole slice:** `isPlainBlank` tests the **raw string,
including ANSI bytes**. A background-tinted padding row contains
`\x1b[48;2;…m` — non-whitespace — so it is **not** plain-blank and **survives
trimming**. An untinted `Spacer(1)` gets eaten.

Net effect, from one rule:

> Tinted cards keep their blank row of breathing space. Flush, untinted blocks
> sit tight. No per-component spacing configuration exists anywhere.

### Zero-height blocks

`ToolActivityContainer.render` returns `[]` when hidden
(`modes/components/tool-activity.ts:40-43`), and `stripped-tool-calls-placeholder`
does the same. Because of the `if (block.length === 0) continue` guard, a hidden
block emits **neither content nor a separator** — filtering leaves no gaps. omp
cites issue #9483 for this.

---

## Current jig state

`internal/tui/monitor/monitor_transcript_items.go`, `itemSpacingBefore`:

```go
func itemSpacingBefore(previous, current transcriptItem) int {
	if previous.coord.generation != current.coord.generation ||
		previous.coord.iteration != current.coord.iteration ||
		previous.coord.attempt != current.coord.attempt {
		return 2
	}
	if previous.kind == transcriptItemText && current.kind == transcriptItemText && previous.role == current.role {
		return 0
	}
	return 1
}
```

Consumed at `monitor_transcript_items_view.go:22-27`. The doc comment is good and
worth preserving:

> *"It deliberately has no theme dependency so filtered views and the default
> view retain the same conversation rhythm."*

This is a reasonable design and is **closer to correct than omp's** in one
respect: the 2-line gap at an execution-coordinate change (generation /
iteration / attempt) is genuine structural information that omp has no analogue
for, since omp has no loop or retry model.

### What is missing

1. **No per-item edge trimming.** Nothing strips leading/trailing blank lines
   from an item's own output before the separator is applied. Today items happen
   not to produce them; cards will.
2. **No zero-height guard.** If a filtered or empty item renders to `""`, the
   loop still emits `itemSpacingBefore` newlines for it — a gap with no content.
   Reachable today via `filteredTranscriptItems` combined with an item whose
   renderer yields nothing.
3. **Spacing is computed from item *kind*, not from what the item actually
   rendered.** Once kinds render as cards with intrinsic padding, the kind-based
   table becomes the wrong input.

---

## In Scope

- Add per-item edge trimming in `itemTranscriptBody` before the separator is
  applied, with jig's equivalent of the ANSI-aware blank test. jig already has
  `stripSGR` (`monitor_transcript.go:1309`) and `stripBlankEdges`
  (`:1291`) — but note `stripBlankEdges` uses `strings.TrimSpace(stripSGR(line))`,
  i.e. it treats a tinted blank row as **blank**, the opposite of omp. A new
  raw-bytes predicate is needed, or an explicit "this row is structural padding"
  marker.
- Add the zero-height guard: an item rendering to empty emits neither content nor
  separator.
- Re-derive spacing from rendered output rather than item kind where the two
  disagree, keeping the execution-coordinate rule (it is genuinely better than
  omp's uniform gap).
- Document the resulting rule in a comment at `itemSpacingBefore`, in the style
  of the existing one.

## Out of Scope

- omp's native-scrollback retirement, append-only stable-row ledger,
  freeze-on-drift, two-phase offer/acknowledge, and pinned-frontier watchdog —
  epic NG1. jig owns a viewport; none of that applies.
- omp's proportional viewport allocator and emergency-row mechanism — epic NG3.
- The boundary **banner** rendered at an execution-coordinate change — slice 12.
  This slice owns the blank lines around it, not its glyphs.

## Functional Requirements

- **FR-04.1** An item that renders no visible content shall contribute neither
  content nor separator lines.
- **FR-04.2** Leading and trailing structural blank rows produced by an item's
  own renderer shall not stack with the inter-item separator.
- **FR-04.3** A background-tinted padding row shall be preserved as content, not
  trimmed as whitespace.
- **FR-04.4** Two consecutive text items from the same role at the same execution
  coordinate shall remain visually continuous (current 0-line behavior).
- **FR-04.5** A change in generation, iteration, or attempt shall remain visually
  stronger than an ordinary item boundary.
- **FR-04.6** `chatItemLineRanges` shall remain accurate after trimming, so
  block navigation and scroll-to-item stay aligned.

## Technical and Repository Constraints

- `stripBlankEdges` **cannot be reused as-is** for FR-04.3 — it strips SGR before
  testing, so it classifies a tinted row as blank. Either add a distinct
  predicate or have the card mark its padding rows structurally. The second is
  more explicit and less clever; prefer it if the card API allows.
- `itemSpacingBefore` must keep its no-theme-dependency property so filtered and
  unfiltered views share one rhythm.
- FR-04.6 is the sharp edge: trimming changes line counts *after*
  `itemTranscriptBody` has begun accumulating offsets. Trim the item's rendered
  bytes **before** measuring, not after.

## Security and Data Considerations

None identified.

## Acceptance Evidence

- A test where a filtered-out or empty item sits between two visible items,
  asserting exactly one separator between them (FR-04.1).
- A test that a tinted card's own padding row survives trimming while a plain
  blank line does not (FR-04.3).
- A test that consecutive same-role text items still render with no gap
  (FR-04.4).
- A test that `chatItemLineRanges` offsets match the actual rendered line indices
  after trimming (FR-04.6).

## Inputs for the Child Spec

- jig's execution-coordinate spacing is **better than omp's** here; keep it. The
  import is the trimming discipline and the zero-height guard, not the flat
  one-blank-line rule.
- The `stripBlankEdges` / `isPlainBlank` semantic inversion is the main trap;
  call it out at the top of the spec.
- Slice 01's card API should expose whether it emitted structural padding, so
  this slice does not have to infer it from bytes.

## Open Questions

- **Q-04.1** Should the card emit padding rows at all, or should the separator
  own all vertical space? omp does the former; the latter is simpler and avoids
  FR-04.3 entirely. *Resolve jointly with slice 01 — this may delete a
  requirement.*
- **Q-04.2** Is the 2-line execution-coordinate gap still right once a banner
  (slice 12) sits in it? *Not a blocker; tune when slice 12 lands.*
