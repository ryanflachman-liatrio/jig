# Slice 11 — Per-step metadata row

- **Slice ID:** `turn-metadata-row`
- **Outcome:** Each completed step boundary in the transcript carries one dim
  metadata line — timestamp, elapsed, and whatever token/cost figures jig
  actually has — replacing the terse `iter 2 · attempt 1` hint.
- **Why this slice exists:** jig's engine already measures step timing and the
  transcript already carries execution coordinates; none of it reaches the
  Transcript panel. This is a small, self-contained density win.
- **Depends on:** Slice 04.

---

## omp reference

`packages/coding-agent/src/modes/components/usage-row.ts:35-72`.

```ts
return parts.join("  ");   // two spaces between every part
```

Parts, in order, each conditional:

| # | Part | Condition |
|---|---|---|
| 1 | `YYYY-MM-DD HH:mm:ss` (local, zero-padded) | timestamp finite and > 0 |
| 2 | `Δ <duration>` | elapsed > 0 |
| 3 | `⤵ <input + cacheWrite>` | always |
| 4 | `⤴ <output>` | always |
| 5 | `💾 <cacheRead>` | cacheRead > 0 |
| 6 | `⏱ <ttft>s` | ttft > 0 |
| 7 | `⚡ <tok/s>` | duration > 100 ms and output > 0 |

Rendered:

```
 2026-01-02 03:04:05  Δ 1m  ⤵ 4.2K  ⤴ 7  💾 12.1K  ⏱ 0.6s  ⚡ 42.3/s
```

Design notes worth copying:

- **The whole line is `dim`.** It is reference material, not content.
- **No alignment, no right-justification** — the source comment calls it
  "log-line style" (`:45`).
- **Two-space separation**, not `" · "`. The metadata row is the one place omp
  does *not* use its universal dot separator, precisely so it reads as a distinct
  register.
- **`Δ` is deliberately bare** (no icon) *"so it scans apart from the TTFT figure
  below (which keeps the clock icon)."*
- Timings come from **pure local timestamps**
  (`turnElapsedMs = message.completedAt - turnStartedAt`, `:25-32`), never from a
  provider-reported duration.
- The row is **deferred**: `#flushPendingUsage`
  (`chat-transcript-builder.ts:233-265`) only emits once the turn's tool results
  have materialized, so it lands *below* the turn's tool blocks, never above
  them.
- For read-only turns it is **nested into the read group** instead of standing
  alone (`read-tool-group.ts:483-509`), as a tree continuation row.

Icons (`modes/theme/symbols.ts:452-468`): `⤵` input, `⤴` output, `💾` cache,
`⏱` time, `⚡` throughput. ASCII forms: `in:`, `out:`, `cache`, `t:`, `tok/s:`.

---

## Current jig state

`internal/tui/monitor/monitor_transcript.go:691-703` — the header block in the
non-item path (which slice 00 largely removes, but the pattern is the current
intent):

```go
indicator, _ := stepIndicator(s.status)
header := indicator + "  " + statusStyle(s.status).Render(string(s.status))
var context []string
if s.iteration > 0 { context = append(context, fmt.Sprintf("iter %d", s.iteration+1)) }
if s.attempt > 0   { context = append(context, fmt.Sprintf("attempt %d", s.attempt)) }
if len(context) > 0 {
    header += "  " + shared.Theme.Chat.Hint.Render(strings.Join(context, " • "))
}
```

That is the entire per-step metadata surface: a status word and, when non-zero,
an iteration and attempt count. No timing, no sizes, no counts.

### What jig actually has available

**This is the first question the spec must answer, and it is not yet answered.**
Candidate sources, all requiring verification:

- `internal/datastore` writes `result.json` per step — check what fields it
  carries (duration? exit code? token usage?).
- `internal/manifest` holds the run manifest.
- The engine emits step lifecycle events; `internal/tui/monitor/monitor_events.go`
  consumes them and may already carry timestamps.
- `transcript.Entry` has `Ts` (RFC3339), parsed in the dead path at
  `monitor_transcript.go:529-531`. First and last entry timestamps bound a step's
  wall time **from the transcript alone**, with no new plumbing.
- Token usage: unknown. The Claude SDK reports it; whether jig's harness
  surfaces it through to the transcript needs checking. If it does not, **the
  row ships without token figures** — that is fine and expected.

The transcript-derived elapsed (first→last entry `Ts`) is the pragmatic floor:
it needs no new data path and works for both harnesses.

---

## In Scope

- Audit what per-step metrics jig can access without changing the harness
  contract (CC-12). Write the answer into the spec.
- A dim, two-space-joined metadata row rendered at step boundaries in the
  transcript.
- At minimum: local timestamp and elapsed wall time derived from entry
  timestamps.
- Opportunistically: token counts, cost, exit code — whatever the audit finds,
  each part conditional on being present.
- Fold `iter N` / `attempt N` into this row rather than the step header, since
  they are the same register.
- Icons through the icon vocabulary (CC-7) with ASCII fallbacks.

## Out of Scope

- Adding token accounting to the harness or transcript contract (CC-12). If the
  data is absent, omit the figure.
- Nesting the row into a group (omp's read-group variant) — revisit after slice
  08.
- A run-level usage dashboard. This is a per-step row in the transcript.
- Provider-reported durations; use local timestamps as omp does.

## Functional Requirements

- **FR-11.1** The system shall render one dim metadata row per completed step
  boundary in the transcript.
- **FR-11.2** The row shall join its parts with two spaces and shall omit any
  part whose source data is unavailable.
- **FR-11.3** The row shall render the step's local completion timestamp.
- **FR-11.4** The row shall render elapsed wall time when derivable.
- **FR-11.5** The row shall render iteration and attempt when non-zero.
- **FR-11.6** The row shall render entirely in a hint/dim style.
- **FR-11.7** The row shall appear **after** the step's tool exchanges, never
  before them.
- **FR-11.8** The row shall be omitted entirely when no part has data, rather
  than rendering an empty line.
- **FR-11.9** With persistence off, the row shall degrade gracefully to whatever
  is derivable from in-memory state, or be omitted.

## Technical and Repository Constraints

- **The audit is the slice.** Do not design the row before establishing what data
  exists; a row of unavailable figures is worse than no row.
- Timestamps: `transcript.Entry.Ts` is RFC3339 and the existing parse uses
  `t.Local().Format("15:04:05")`. omp uses a full `YYYY-MM-DD HH:mm:ss`; for a
  single run within one session the time-only form is probably better. Decide
  and state why.
- FR-11.7 is the ordering constraint omp solves with deferred flushing. jig's
  item list is built from the transcript in order, so a step-boundary row must be
  emitted at the *end* of a step's items, not at its start.
- Do not use the engine event bus as the data source — "file is truth, bus is
  liveness." Derive from the transcript and from `result.json`.
- Duration formatting needs a helper (`1m`, `4.3s`, `184ms`); check whether one
  already exists in the codebase before adding it.

## Security and Data Considerations

If cost figures are surfaced, they reveal spend. That is the operator's own data
in their own TUI — no exposure concern — but do not add cost to any exported or
shared artifact as part of this slice.

## Acceptance Evidence

- A written audit in the spec listing each candidate metric, its source, and
  whether it is available.
- A test asserting the row renders after a step's tool exchanges.
- A test asserting parts with absent data are omitted and the separator does not
  double.
- A test asserting the row is omitted entirely when nothing is available.
- A test asserting the whole row is dim-styled.

## Inputs for the Child Spec

- Start with the availability audit; everything else follows from it.
- The transcript-derived elapsed (first→last `Ts`) is the guaranteed floor and
  needs no new plumbing.
- omp's two-space separator is a deliberate register change from its universal
  `" · "` — preserve that distinction so the row does not read as content.
- CC-12 forbids extending the harness contract to obtain metrics. Ship with what
  exists.

## Open Questions

Resolved during implementation. See
[`docs/plans/omp-slice-11-turn-metadata-row.md`](../../../plans/omp-slice-11-turn-metadata-row.md)
for the full audit and rationale; the resolutions below are the
minimum record needed inside this slice document.

- **Q-11.1** *(answered)* jig has: per-entry `transcript.Entry.Ts`
  (RFC3339, second precision); per-step `monitorStep.start`, `.end`,
  `.cost` (`*float64`), `.tokens`, `.iteration`, `.attempt` fed from
  `engine.StepStatus`; `step.Result.Duration` and `.TotalCostUSD` on
  `result.json`; and `step.Result.TokenCount()` breaking the usage
  map into a summed total. `StepStatus.Cost` and `.Tokens` are
  **cumulative per step**, not per turn, so per-turn cost/tokens are
  not available without changing the harness contract (CC-12). TTFT,
  throughput, cache-read, and per-direction token split are not
  surfaced anywhere and are dropped from the row.
- **Q-11.2** *(answered)* Time-only `15:04:05` in local time. A run
  is typically one sitting; adding the date widens the row without
  information. The formatter matches the pre-existing
  `t.Local().Format("15:04:05")` at
  `monitor_transcript.go:529-531`.
- **Q-11.3** *(answered)* Yes to the row, no to the error. The
  failure banner keeps its existing home at `chatBody`'s top
  (`monitor_transcript.go:380-384`); duplicating the error in the
  metadata row would violate CC-2 (state carried by border/color,
  not appended prose).
- **Q-11.4** *(answered)* Per-turn timestamps and elapsed, per-step
  cost and tokens. The interior boundary row is emitted at every
  `(gen, iter, attempt)` coordinate change; the step-end row is
  emitted below the last item once `monitorStep.end != zero`, and
  cost/tokens attach only there.

### Persistent notes

- The row's `Δ` slot is dropped for durations under 500 ms so a
  same-second turn does not render as `Δ 0s`. This is a consequence
  of `transcript.Entry.Ts`'s second precision, not a policy choice.
- The row is folded into the *previous* rendered item's
  `chatItemLineRanges` entry rather than owning its own key, so
  `n`/`N` block navigation still lands on items.
