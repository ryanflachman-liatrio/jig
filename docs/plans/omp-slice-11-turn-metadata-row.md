# Implementation Plan: OMP parity slice 11 — per-turn metadata row

**Status:** Planned — omp-transcript-parity epic, slice
[`11-turn-metadata-row`](../epics/omp-transcript-parity/slices/11-turn-metadata-row.md)
**Risk:** **low** — presentation-only, single package. No schema, harness,
transcript, or engine mutation. One new dim row inside an existing render loop,
guarded by data availability. The chief hazard is `chatItemLineRanges` accounting
(`n`/`N` navigation) if the row is not counted like every other rendered item.
**Depends on:** epic slice 04 (vertical rhythm and block edges), already landed
in [`docs/specs/25-spec-vertical-rhythm-and-block-edges`](../specs/25-spec-vertical-rhythm-and-block-edges/25-spec-vertical-rhythm-and-block-edges.md);
the two-line coordinate gap and the raw-bytes zero-height discipline are the
insertion contract slice 11 rides on. Uses monitorStep timing/cost/token fields
already populated by `internal/tui/monitor/monitor_events.go`.
**Complements:** goal
[T7](open-goals.md#c-transcript-experience--path-to-best-in-class)
(per-turn timing and tokens); slice 12 (boundary banners) will render in the
same coordinate gap; slice 04's `itemSpacingBefore == 2` transition is the
insertion site.
**Breaks:** the terse per-step header `iter N · attempt N` in `chatBody`
(`monitor_transcript.go:407-418`) is subsumed by the new row and deleted in the
same change, per pre-v1 policy — no compatibility wrapper.

---

## Summary

Add one dim, two-space-joined metadata row per completed turn boundary in the
Transcript panel, carrying local completion time, elapsed wall time, iteration
and attempt when non-zero, and cost and token figures when the harness reported
them. Fold the current terse `iter N · attempt N` step header into the same
register. The top risk is keeping `chatItemLineRanges` accurate — a new row that
does not participate in the line ledger silently breaks `n`/`N` navigation.

## Approach

Extend the epic slice-04 loop in `itemTranscriptBody`
(`internal/tui/monitor/monitor_transcript_items_view.go:32`) to emit one
non-item **boundary row** whenever it crosses a coordinate change (turn boundary
inside a step) or reaches the last visible item that belongs to the current
turn (step-end boundary). The row is rendered by a small helper
`renderTurnMetadataRow` that consumes only what the monitor already has: the
per-turn entry timestamps parsed from `transcript.Entry.Ts`, plus the
`monitorStep`'s final cumulative `cost`, `tokens`, `iteration`, `attempt`,
and `end` fields on the settled-step case. Every part is conditional; a turn
with no derivable metric emits no row. Style comes from
`shared.Theme.Chat.Hint`; no new palette, style, or glyph enters the codebase.
Deletion of the existing per-step iter/attempt header at
`monitor_transcript.go:407-418` lands in the same change so operators do not see
the same fact twice.

## Problem

Today jig's Transcript panel carries **one** per-step affordance: the header
composed at `monitor_transcript.go:407-418` — a status word plus, when non-zero,
`iter N` and `attempt N` joined by `" · "`. That is the entire per-step
metadata surface. There is no local completion timestamp, no elapsed wall time,
no per-turn cost, and no per-turn token figure inside the transcript — even
though the engine already delivers all of it on `engine.StepStatus`
(`internal/engine/event.go:51-78`) and `monitor_transcript_items.go`'s
`transcriptItem.coord` already carries the (generation, iteration, attempt)
tuple that would let a per-turn row exist.

The consequence is that operators watching a multi-iteration agent step have to
context-switch to the Steps panel (`stepDuration` / `stepCostStr` /
`stepTokensStr` in `monitor_steps.go`) to answer questions like:

- how long did the last agent turn take,
- how much did *this* iteration cost, versus the whole step,
- when did iteration 2 begin,

or accept "I don't know" — which is exactly the T7 gap catalogued in
[`docs/plans/open-goals.md`](open-goals.md).

The presentation grammar for this row is fully described by omp's
`usage-row.ts` (see slice document) and by epic decisions **CC-8** (dim
register), **CC-1** / **CC-2** (state carried by color/border, not appended
words), and **CC-12** (no transcript wire changes). The remaining work is:
answer Q-11.1 with an availability audit, choose from Q-11.2/Q-11.3/Q-11.4 with
defensible defaults, and hook one row into the existing bounded item loop
without breaking any of slice 04's invariants.

---

## Data availability audit (Q-11.1)

The slice document asserts the audit is the slice; here is the audit.

| Datum | Source | Live-truth path | Persisted path | Available for slice 11? |
|---|---|---|---|---|
| **Turn start Ts** | `transcript.Entry.Ts` (RFC3339, UTC, second precision) — `transcript.go:172-181` | `chatEntries[]` already loaded and paged | `transcript.jsonl` | **Yes.** First entry of each `(gen, iter, attempt)` coordinate tuple. Bounded per loaded page (`chatWindowMax = 300`); older turns need the page. |
| **Turn end Ts** | Same | Same | Same | **Yes.** Last entry of each coordinate tuple within the loaded page. |
| **Turn elapsed** | Derived: end − start | Same | Same | **Yes.** Deterministic from Ts alone. Second-precision only (transcript stamps at 1s granularity), so 4.3s becomes `4s` and 184ms becomes empty; document this rather than pretending sub-second precision. |
| **Step start / end (wall)** | `monitorStep.start`, `monitorStep.end` — set in `monitor_events.go:79-89` on `StepStatus{To: Running / Succeeded / Failed / Skipped}` using `time.Now()` | in-memory monitor state | Journal replay reconstructs the same `StepStatus` transitions on reopen — so `monitorStep.start/.end` are re-derived on load. | **Yes** for the settled-step row. Live steps: `end.IsZero()` — handled the way `stepDuration` already handles it. |
| **Local completion timestamp** | `monitorStep.end.Local()` — set on terminal status | Same | Same (from `StepStatus`) | **Yes.** Availability matches step end. |
| **Iteration** | `transcript.Entry.Iteration` and `monitorStep.iteration` (`monitor_events.go:73`) | Both | Both | **Yes.** The entry field is authoritative per-turn; the monitorStep field carries the *current* iteration only. |
| **Attempt** | `transcript.Entry.Attempt` and `monitorStep.attempt` (`monitor_events.go:74`) | Both | Both | **Yes.** Same shape as iteration. |
| **Generation** | `transcript.Entry.Generation` | `chatEntries[]` | `transcript.jsonl` | **Yes.** Currently used only for coordinate keying; slice 11 does not need to display it. |
| **Cumulative step cost (USD)** | `monitorStep.cost *float64` — set from every `StepStatus.Cost` (`monitor_events.go:93`) | in-memory monitor state | Journal (`StepStatus`) | **Yes for step-end; NOT reliable per-turn.** `StepStatus.Cost` accrues across attempts inside one step; the engine does not publish a per-turn Cost delta. Slice 11 therefore shows cost only on the step-end row, not on interior turn rows. |
| **Cumulative step tokens** | `monitorStep.tokens int` — set from every `StepStatus.Tokens` (`monitor_events.go:94`) | Same | Same | **Same as cost.** Step-end only. |
| **TTFT (time-to-first-token)** | Not surfaced | — | — | **No.** Omit the `⏱ Xs` field. |
| **Tokens/second throughput** | Not surfaced | — | — | **No.** Omit the `⚡` field. |
| **Cache-read tokens** | Not surfaced | — | — | **No.** Omit the `💾` field. |
| **Input / output token split** | Not surfaced; `step.Result.Usage` breaks it down but is a per-step artifact and is not written to a per-turn field | `result.json` on step end | Same | **No for per-turn.** Available only on step end from `result.json`, but the total is already exposed via `monitorStep.tokens`. Slice 11 renders the summed count and defers a per-direction split. |

### Field selection (audit outcome)

The row's per-turn fields become:

| # | Field | Format | Condition |
|---:|---|---|---|
| 1 | local time | `HH:MM:SS` | turn has at least one entry whose `Ts` parses (see Q-11.2 below) |
| 2 | elapsed | `Δ <dur>` where `<dur>` is `stepDuration`-style compact (`5s`, `1m20s`, `184ms`) | turn end > turn start when both parse |
| 3 | iteration | `iter N` | `entry.Iteration > 0` for at least one entry in the turn (matches current header rule) |
| 4 | attempt | `attempt N` | `entry.Attempt > 0` for at least one entry in the turn |

The step-end row (emitted after the last visible item that belongs to the
current step) additionally carries:

| # | Field | Format | Condition |
|---:|---|---|---|
| 5 | cost | `$<X.XXXX>` | `monitorStep.cost != nil` |
| 6 | tokens | `<Nk> tok` via `humanTokens` | `monitorStep.tokens > 0` |
| 7 | subtype | (already surfaced elsewhere) | omitted from this row |

`humanTokens` and the cost formatter already live in `monitor_steps.go` and are
reused; nothing new enters the palette or icon vocabulary. Slice 14's ASCII
preset already covers every glyph slice 11 needs (`Δ` is bare, matching omp's
`usage-row.ts:47`).

---

## Open-question resolutions

Each is defaulted so implementation can proceed; each records the rationale so
a reviewer can overturn it before code lands.

- **Q-11.1** — Answered by the audit above. Cost and tokens are the only
  harness-surfaced figures; they arrive per-step, not per-turn. Per-turn rows
  carry only what is derivable from `transcript.Entry.Ts` + coordinate. TTFT,
  throughput, and cache-read fields are dropped.
- **Q-11.2** (timestamp format) — **Time-only `15:04:05`**, matching the
  existing `t.Local().Format("15:04:05")` at `monitor_transcript.go:529-531`
  in the dead render-plan path. Rationale: a single jig run is one operator
  sitting; the date adds width without information. Defer full RFC-style
  formatting until a run share/export contract demands it.
- **Q-11.3** (failed step row) — **Yes to the row, no to the error.** The
  metadata row renders after the last item just like the succeeded case. The
  error surface stays at `chatBody`'s top (`monitor_transcript.go:380-384`);
  duplicating it in the metadata row would violate epic CC-2 (state carried
  by border/color, not appended prose).
- **Q-11.4** (per-step vs per-iteration granularity) — **Per turn (per
  `(gen, iter, attempt)` triple)** for the timestamp/elapsed row; **once per
  step** for the cost/tokens tail. Rationale: elapsed is a per-turn signal,
  cost is engine-cumulative and cannot be split without harness changes
  (CC-12). Slice 12 will place its boundary banner in the same coordinate
  gap; slice 11 sits inside that same gap without displacing it.

---

## Row grammar

Two rows exist. Both are indented with the same two-space transcript prefix
as text items (`monitor_transcript_items_view.go:64` uses `"  "` for
unselected items; the metadata row is never selectable and never wears the
cursor bar, so it always uses `"  "`). Both are rendered through
`shared.Theme.Chat.Hint` and contain no icon slot — the register itself
signals reference material.

### Turn-boundary row (interior)

Rendered inside the two-line gap emitted by `itemSpacingBefore` when the next
item's coordinate differs from the previous item's coordinate.

```
  15:04:05  Δ 4s  iter 1  attempt 0
```

Ordering: `time`, `Δ elapsed`, `iter`, `attempt`. Any field whose data is
unavailable is dropped; the join collapses adjacent gaps so there are no
double spaces. When *every* field is empty (a coordinate change we could not
timestamp), the row is not rendered at all — the plain slice-04 two-line gap
remains and slice 12's banner (when it lands) has the same room it had before.

### Step-end row (terminal)

Rendered after the last visible item that belongs to the current step, when
the step has a terminal status (`Succeeded`, `Failed`, `Skipped`). Not
rendered while the step is still `Running`, `AwaitingReview`, `NeedsInput`,
or `AwaitingRecovery`.

```
  15:04:05  Δ 12s  iter 2  attempt 0  $0.0412  4.2k tok
```

Extra fields (cost, tokens) attach only here. Same dim style. Same drop-on-
empty rule.

### What the row is not

- Not a card. No border. No tint. No state.
- Not selectable — never appears in `chatVisibleItems` and never gets a
  `chatItemLineRanges` key of its own. Instead, its rows count toward the
  *previous* rendered item's `end` line so `n`/`N` block navigation lands on
  the item and treats the row as its trailing chrome. See "Line accounting"
  below for the exact rule.
- Not a status label. When the step failed, the failure banner keeps its
  existing home at `chatBody`'s top. The metadata row carries no `failed`
  word.

---

## Where the row inserts

Slice 04's loop shape (transcribed from
`monitor_transcript_items_view.go:32-57` and its doc comment) is:

```
for i, item := range m.chatVisibleItems {
    body := trimStructuralBlankEdges(scratch.String())
    if body == "" { continue }
    if lastRenderedIdx >= 0 {
        for range itemSpacingBefore(prev, item) {
            b.WriteString("\n"); line++
        }
    }
    start := line
    b.WriteString(body); b.WriteString("\n")
    line += strings.Count(body, "\n") + 1
    m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}] = lineRange{start, line - 1}
    lastRenderedIdx = i
}
```

Slice 11 modifies this in exactly two places:

1. **Interior boundary insertion.** When
   `itemSpacingBefore(prev, item) == 2` (coordinate change), split the two
   newlines: emit one `\n`, then a rendered metadata row for the *previous*
   turn if `renderTurnMetadataRow` returns non-empty, then one more `\n`.
   The gap total stays at two rows when the metadata row is empty; it grows
   by one row when the metadata row renders (this is the intentional
   layout difference from slice 04 and the correct place for slice 12 to
   choose its own budget later).
2. **Step-end insertion.** After the `for` loop, if the step is terminal
   (`monitorStep.end.IsZero() == false`) and there was at least one
   rendered item, emit `\n` + the step-end row + `\n`. The row is skipped
   when the render collapses to empty.

Both insertions go through the same helper — `renderMetadataRow(fields
[]string) string` — which joins non-empty parts with two spaces (omp
`usage-row.ts:19`) and wraps the whole line in `shared.Theme.Chat.Hint`. A
zero-length field slice returns `""`; the caller treats that as "no row".

### Turn identification (interior case)

The previous turn's `(gen, iter, attempt)` is `lastRenderedItem.coord`. Its
first and last entries in the loaded page can be found by scanning
`m.chatEntries` for entries whose coordinate matches. This is the same
`toolCorrelationKey`-style walk `visibleExecutionBoundaries` already does in
`monitor_transcript_items.go:116-133`; slice 11 factors it out into
`turnTimestamps(entries, coord) (start, end time.Time, ok bool)` colocated
with `visibleExecutionBoundaries` so the boundary computation and the
metadata row share one source of truth.

A page edge may not contain the turn's first or last entry — the audit
already noted this. When only one of `start`, `end` is available, the row
renders whichever field it can derive: elapsed is dropped, the timestamp is
rendered from whichever endpoint parsed. When neither parses, the row is
empty and skipped.

### Line accounting

The metadata row extends the *previous* item's `chatItemLineRanges` entry to
cover its rendered rows. Concretely:

- Before writing the metadata row, remember `line` as `metaStart`.
- Write the row plus its trailing `\n`. Advance `line` by
  `strings.Count(rowBody, "\n") + 1`.
- Update `m.chatItemLineRanges[transcriptLineKey{itemKey: prevItem.key}]` so
  its `end` is `line - 1`. `start` is unchanged.

The step-end row does the same for the very last rendered item's key. This
keeps `n`/`N` block navigation (`monitor_layout.go:339`) landing on the item
whose story the row is telling. Slice 04's `TestTranscriptLineRangesMatch
RenderedRows` regression must continue to pass by simply picking up the new
end values; the invariant "`end - start + 1` equals rendered rows" is
preserved.

If the row itself renders empty (all fields unavailable), no line accounting
change occurs and the visible layout matches slice 04 exactly.

---

## Architecture and ownership

```text
internal/tui/monitor
  ├─ monitor_transcript.go
  │   • delete the iter/attempt hint composed at :407-418 (subsumed).
  │
  ├─ monitor_transcript_items.go
  │   • add turnTimestamps(entries, coord) (start, end time.Time, ok bool).
  │
  ├─ monitor_transcript_items_view.go  (primary edit site)
  │   • extend the slice-04 loop to insert renderTurnMetadataRow on
  │     coordinate change and step-end boundaries.
  │   • adjust the previous item's chatItemLineRanges entry to cover
  │     the appended metadata row.
  │
  └─ monitor_transcript_metadata.go     (new file)
      • renderTurnMetadataRow(m, coord, includeStepTotals) string.
      • formatMetaFields(...) []string    — omp two-space joiner reused.
      • formatMetaTime(t time.Time) string — HH:MM:SS.
      • formatMetaDuration(d time.Duration) string — reuses monitor_steps
        formatting rules (Round to second for ≥1s, milliseconds otherwise).
```

Zero new types cross the `internal/tui/monitor` boundary. No `internal/tui/
shared` addition is required: `Theme.Chat.Hint` already carries the dim
register. The audit's "everything derivable" property means slice 11 does
not import `internal/engine`, `internal/transcript`, or `internal/step`
beyond what `monitor_transcript_items_view.go` already imports.

### Not touched by slice 11

- `internal/engine` — no new event, no new envelope field. `StepStatus.Cost`
  and `StepStatus.Tokens` already carry what the row needs.
- `internal/transcript` — wire format unchanged (epic CC-12).
- `internal/harness/*` — no capability change; TTFT and throughput remain
  out of scope until a harness surfaces them.
- `internal/tui/shared` — no new palette, style, glyph, or component.
- `monitor_steps.go` totals — the Steps-panel `totalsStr()` /
  `humanTokens()` helpers are read-only reused; their behavior is unchanged.
- Journal replay — no journal event is filtered or synthesized; the
  monitorStep fields are already reconstructed on reopen via the existing
  `StepStatus` replay path.

---

## Delivery phases

Small enough to land as one PR, but sequenced for reviewability.

### Phase 1 — audit as a data function

1. Add `turnTimestamps` in `monitor_transcript_items.go` and unit-test it
   over: empty entries, one-entry turn, multi-entry turn, entries with
   unparseable `Ts`, coordinate that doesn't appear.

**Exit:** helper is a pure function, tested, and available for the view
loop.

### Phase 2 — row rendering as a data function

1. Add `monitor_transcript_metadata.go` with `renderTurnMetadataRow`,
   `formatMetaTime`, `formatMetaDuration`, and the two-space joiner.
2. Unit-test the row over: all fields present; each field absent
   individually; every field absent (empty output); iteration+attempt
   with a coord whose transcript never populated cost/tokens (interior
   turn case); step-end with cost and tokens populated; step-end failed
   step with cost populated.

**Exit:** the row is a pure `(monitorStep, coord, includeStepTotals) →
string` function with no dependency on the transcript loop.

### Phase 3 — insertion into the item loop

1. Extend `itemTranscriptBody` per "Where the row inserts" above:
   coordinate-boundary emission and step-end emission, both extending the
   preceding item's `chatItemLineRanges` entry.
2. Delete the iter/attempt header at `monitor_transcript.go:407-418`.
   Retain the status word — the row does not replicate it and CC-2 keeps
   status on the card border for tool items; the top-of-transcript status
   label stays as-is otherwise.

**Exit:** row appears at the two documented boundaries; step-end row
never renders on a still-running step; `chatItemLineRanges` extends
correctly.

### Phase 4 — invariants under the existing slice-04 tests

1. Re-run `internal/tui/monitor` tests. Every existing test whose fixture
   crosses a coordinate boundary or terminates a step gains a metadata
   row unless the fixture disables timestamps. Adjust the small set of
   expectations that observe `chatItemLineRanges[end]` for the last item
   before a coordinate change or at step end — the numeric change is the
   intended one and each adjustment is reviewed as an intentional layout
   delta, not a weakening of an existing expectation.
2. Add the four new tests listed in "Test matrix" below.

**Exit:** `go test ./internal/tui/monitor -race -count=1` and the full
`go test ./...` are clean, `gofmt -l` on changed files is empty,
`go vet ./...` is clean.

### Phase 5 — proof capture and docs

1. Capture a deterministic 80-column monitor scene under
   `docs/specs/25-spec-turn-metadata-row/25-proofs/` (new spec directory)
   showing (a) an interior boundary row across an iteration bump, (b) a
   step-end row for a settled successful step, (c) a settled failed step
   whose error banner stays at `chatBody`'s top and whose metadata row
   still renders, and (d) a still-running step whose step-end row is
   absent by design. Save `.ansi`, `.html`, and `-notes.txt`, matching
   slice 04's proof format.
2. Update the slice-11 open questions in
   `docs/epics/omp-transcript-parity/slices/11-turn-metadata-row.md` with
   the resolutions above, marking Q-11.1 answered.
3. Add a cross-link from
   [`docs/plans/open-goals.md`](open-goals.md) T7 to this plan.

**Exit:** proofs recorded, epic slice document reflects resolved
questions, T7 has a plan reference.

---

## Ordered implementation tasks

Estimates are focused-agent wall time; every substantive code change has a
sibling test task. Task areas are `<Go import path> — <file>` so the mapping
is unambiguous.

| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `turnTimestamps(entries []transcript.Entry, coord toolCorrelationKey) (start, end time.Time, ok bool)` scanning entries in-page and parsing `Ts` with the existing RFC3339 rule | `internal/tui/monitor — monitor_transcript_items.go` | 15 min |
| 2 | Table-test `turnTimestamps` over empty, one-entry, multi-entry, unparseable `Ts`, and coordinate-not-present | `internal/tui/monitor — monitor_transcript_items_test.go` | 20 min |
| 3 | Add `renderTurnMetadataRow(m *Model, coord toolCorrelationKey, includeStepTotals bool) string` and helpers `formatMetaFields`, `formatMetaTime`, `formatMetaDuration` in a new file colocated with slice-04 helpers | `internal/tui/monitor — monitor_transcript_metadata.go` (new) | 25 min |
| 4 | Table-test `renderTurnMetadataRow` (interior + step-end) with each field present, each absent, and every field absent producing `""` | `internal/tui/monitor — monitor_transcript_metadata_test.go` (new) | 30 min |
| 5 | Extend `itemTranscriptBody` per "Where the row inserts": interior boundary emission with previous-item `lineRange` extension | `internal/tui/monitor — monitor_transcript_items_view.go` | 20 min |
| 6 | Extend `itemTranscriptBody` with the terminal step-end emission (guarded by `monitorStep.end.IsZero() == false`) | `internal/tui/monitor — monitor_transcript_items_view.go` | 15 min |
| 7 | Delete the iter/attempt hint composed at `monitor_transcript.go:407-418`; verify no test still asserts the old text | `internal/tui/monitor — monitor_transcript.go` | 10 min |
| 8 | New test `TestTurnMetadataRowAtCoordinateBoundary` seating a two-iteration synthetic page and asserting the row appears between them with the right fields | `internal/tui/monitor — monitor_transcript_metadata_test.go` | 25 min |
| 9 | New test `TestTurnMetadataRowAtStepEnd` seating a settled step (via `EngineEventMsg{Event: engine.StepStatus{To: StatusSucceeded, Cost: &c, Tokens: n}}`) and asserting the step-end row renders below the last item with cost and tokens | `internal/tui/monitor — monitor_transcript_metadata_test.go` | 25 min |
| 10 | New test `TestTurnMetadataRowSkippedWhenAllFieldsAbsent` seating a page whose entries have empty `Ts` and asserting the transcript body contains no dim row and no extra blank line | `internal/tui/monitor — monitor_transcript_metadata_test.go` | 20 min |
| 11 | New test `TestTurnMetadataRowLineRangesCoverAppendedRow` seating a settled multi-item page and asserting `chatItemLineRanges[lastItem].end` covers the metadata row's line so `n`/`N` navigation still lands on the item | `internal/tui/monitor — monitor_transcript_metadata_test.go` | 25 min |
| 12 | Update `TestTranscriptExecutionCoordinateGapPreserved` and `TestTranscriptLineRangesMatchRenderedRows` fixtures — where a coordinate boundary now emits a metadata row — to reflect the intentional row count delta with reviewer-visible comments explaining the slice-11 cause | `internal/tui/monitor — monitor_vertical_rhythm_test.go` | 20 min |
| 13 | Refresh the slice-04 visual proof or add a slice-11 proof `docs/specs/25-spec-turn-metadata-row/25-proofs/` with an interior-boundary scene and a step-end scene | `docs/specs/25-spec-turn-metadata-row/25-proofs/` (new) | 30 min |
| 14 | Mark `Q-11.1` answered and record the Q-11.2 / Q-11.3 / Q-11.4 resolutions in the slice document | `docs/epics/omp-transcript-parity/slices/11-turn-metadata-row.md` | 10 min |
| 15 | Cross-link `T7 Per-turn tokens/timing` in `docs/plans/open-goals.md` to this plan | `docs/plans/open-goals.md` | 5 min |

Estimated focused implementation time: **4.5 hours**, one PR.

---

## Test matrix

### New unit and behavioral tests

| Test | Purpose | Fixture shape |
|---|---|---|
| `TestTurnTimestampsSelectsFirstAndLast` | Pure-function verification of `turnTimestamps` | synthetic entries with a mix of coordinates |
| `TestTurnTimestampsUnparseableTs` | `ok=false` when no entry's `Ts` parses | one entry with `Ts=""` |
| `TestTurnTimestampsCoordinateAbsent` | `ok=false` when the coord has no entries in the loaded page | valid entries but a mismatched coord |
| `TestRenderTurnMetadataRowInteriorAllFields` | The row renders time+Δ+iter+attempt when all four are available | timestamps 4s apart, iter 1, attempt 0 |
| `TestRenderTurnMetadataRowInteriorSomeFieldsAbsent` | The join collapses so no double spaces appear | iter=0 attempt=0 leaves only time+Δ |
| `TestRenderTurnMetadataRowInteriorAllFieldsAbsent` | Returns `""` and the loop emits nothing | one turn, entries with `Ts=""` and iter=0 attempt=0 |
| `TestRenderTurnMetadataRowStepEndWithCostAndTokens` | Adds `$X.XXXX` and `N tok` on the step-end row only | `monitorStep{cost: &c, tokens: 4200, end: time.Now()}` |
| `TestTurnMetadataRowAtCoordinateBoundary` | Row appears between an iteration-0 turn and an iteration-1 turn inside a step | 3-item page across a coord change |
| `TestTurnMetadataRowAtStepEnd` | Row appears below the last item of a terminal step | 2-item page with `StepStatus{To: StatusSucceeded, Cost, Tokens}` |
| `TestTurnMetadataRowSkippedForRunningStep` | Step-end row is not emitted while the step is still `Running` | same page but no terminal `StepStatus` |
| `TestTurnMetadataRowSkippedWhenAllFieldsAbsent` | The transcript body contains no dim row and no extra blank line when nothing is renderable | entries with empty `Ts` and cost=nil, tokens=0 |
| `TestTurnMetadataRowLineRangesCoverAppendedRow` | `chatItemLineRanges[lastItem].end` includes the row's rows so `n`/`N` navigation lands on the item | any settled multi-item page |
| `TestTurnMetadataRowDimStyled` | Every visible character of the row is under `Chat.Hint`'s foreground SGR | rendered bytes checked against the theme's ANSI |

### Regression tests to re-run unchanged

| Test | File | Expected outcome |
|---|---|---|
| `TestTranscriptZeroHeightItemContributesNothing` | `monitor_vertical_rhythm_test.go` | passes unchanged — the row is not an item |
| `TestTranscriptTrimsStructuralEdgeBlanksBetweenItems` | same | passes unchanged — the row is emitted between the trim and the next item, so its bytes are content, not structural blank |
| `TestTranscriptPreservesTintedPaddingRow` | same | passes unchanged — slice 11 emits no tinted rows |
| `TestTranscriptConsecutiveTextItemsSameRoleNoGap` | same | passes unchanged — same-coord same-role text items still have no gap and the row is not emitted between them |
| `TestToolExchangeHeaderCardStatesAndWidths` | `monitor_transcript_items_view_test.go` | passes unchanged — card output unchanged |
| `TestTranscriptCardLineRangesCachedAndFresh` | `monitor_transcript_card_test.go` | first-card range unchanged; only the last-item range at a coord change gains rows, which the test asserts against the new row count |

### Release verification

```bash
gofmt -l -w <changed-go-files>
go test ./internal/tui/monitor -race -count=1
go test ./internal/tui/... -race -count=1
go test ./...
go vet ./...
go build ./cmd/jig
go run ./cmd/jig validate .agents/jig/sdd.toml
```

`gofmt -l` on the changed files must be empty. Terminal-capture proofs are
regenerated by rerunning `TestVerticalRhythmVisualProof` (or its slice-11
sibling) with `JIG_UI_SNAPSHOT_DIR` set.

---

## Security and failure handling

- **No new sensitive surface.** The row exposes only the operator's own
  wall-clock timing, iteration counters, and the same cost/token figures
  already visible on the Steps panel. It does not read the environment,
  the filesystem beyond the already-loaded transcript page, or the
  scheduler.
- **Cost visibility scope.** Slice 11 renders cost only in the Transcript
  panel, which is not part of any exported or shared artifact. If a run
  share/export contract later includes rendered transcript body, the
  export layer owns any redaction; slice 11 does not add cost to
  `internal/runexport` or any similar path.
- **Persistence-off.** With `RunDir == ""` the transcript loop is not
  entered (`chatBody` short-circuits to an empty state); the metadata row
  is therefore automatically absent. No new branch is required.
- **Journal replay after crash.** `monitorStep.start`, `.end`, `.cost`,
  and `.tokens` are all re-derived on reopen from replayed `StepStatus`
  events (`monitor_events.go:72-95`), so the metadata row renders
  identically on a resumed process as on a live one. No new persistence
  is introduced.
- **Partial pages.** A page edge can hide either the first or the last
  entry of a turn. The row's per-field guards drop whichever fact is
  absent; the row is not fabricated. When the page contains no
  timestamped entry for the current coord, the row is skipped entirely.
- **Unparseable timestamps.** Any parse error from `time.Parse(RFC3339,
  entry.Ts)` is treated as "absent" for that endpoint. The row never
  bubbles the error up.

---

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| `chatItemLineRanges` drifts, breaking `n`/`N` navigation | `TestTurnMetadataRowLineRangesCoverAppendedRow` asserts the invariant on every emission path; the row extends the *previous* item's range rather than owning its own key. |
| Row renders on running steps and displays "0s / no cost" during dispatch | Terminal-status guard: step-end row only when `monitorStep.end.IsZero() == false`; interior rows do not include cost/tokens even if the field is set mid-attempt. |
| Slice 12's boundary banner and this row fight for the same coordinate gap | Slice 11 emits exactly one row and increases the gap total by 1 when non-empty; slice 12 authors know the total budget and can slot in above or below. Recorded here so slice 12's spec reads it. |
| Terse `iter N · attempt N` header removal breaks a fixture that asserted its literal text | Task 7 grep-audits before deletion. Any test asserting `"iter "` or `"attempt "` in the header goes away with the header. |
| Timestamp precision is 1-second, so a fast turn shows `Δ 0s` and reads as broken | `formatMetaDuration` collapses `< 500ms` to no elapsed field at all (returns "") so the row's `Δ` slot is dropped rather than reading 0. Time-of-day is still emitted since it is still meaningful. |
| Empty-field join accidentally emits double spaces or a trailing space | `formatMetaFields` filters `""` before joining with `strings.Join(parts, "  ")`; unit test `TestRenderTurnMetadataRowInteriorSomeFieldsAbsent` locks this. |
| A test asserts the exact `lineRange` for the last item at a coord change or step end | Task 12 updates the small set of affected assertions with a code comment linking to this plan; each update is deliberate. |
| Time zone confusion (transcript stores UTC, operator expects local) | `formatMetaTime` calls `t.Local().Format("15:04:05")`, matching the pre-existing formatter at `monitor_transcript.go:529-531`. |

---

## Completion criteria

Slice 11 is done when all of the following hold against a real multi-iteration
run with at least one settled successful step, one settled failed step, and
one still-running step:

1. **CC-11.1** — Every completed turn boundary inside the loaded transcript
   page emits at most one dim metadata row inside the two-line coord gap.
   No row appears where every field is unavailable.
2. **CC-11.2** — Every terminal step in the loaded page emits at most one
   dim metadata row below its last item, carrying cost and tokens when the
   harness reported them. A still-running step emits no step-end row.
3. **CC-11.3** — Fields join with two spaces; no adjacent whitespace ever
   appears; no field renders "0" or "unknown" for a value the audit
   classified as absent.
4. **CC-11.4** — `chatItemLineRanges` continues to satisfy
   `end - start + 1 == rendered rows for that item + trailing metadata rows`
   for every item and every emission path; `n`/`N` navigation still lands
   exactly on items, never on the metadata row.
5. **CC-11.5** — The pre-existing terse `iter N · attempt N` header at
   `monitor_transcript.go:407-418` is deleted; the same facts survive in
   the metadata row.
6. **CC-11.6** — All slice-04 regression tests pass unchanged; any
   `chatItemLineRanges[last].end` adjustments are deliberate and
   commented in-line.
7. **CC-11.7** — Persistence-off runs still render the empty transcript
   state; no `RunDir == ""` regression is introduced.
8. **CC-11.8** — `go build ./cmd/jig`, `go test ./...`,
   `go test -race ./internal/tui/...`, `go vet ./...`,
   `gofmt -l <changed-go-files>` (empty), and
   `go run ./cmd/jig validate .agents/jig/sdd.toml` pass.
9. **CC-11.9** — Terminal-capture proofs under
   `docs/specs/25-spec-turn-metadata-row/25-proofs/` include the four
   documented scenes and the row appears in each expected position.
10. **CC-11.10** — The slice document
    [`slices/11-turn-metadata-row.md`](../epics/omp-transcript-parity/slices/11-turn-metadata-row.md)
    records Q-11.1 as answered by the audit above, and Q-11.2/Q-11.3/
    Q-11.4 record the resolutions applied.
