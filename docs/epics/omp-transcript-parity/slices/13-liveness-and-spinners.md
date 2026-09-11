# Slice 13 — Liveness: phase-locked spinners

- **Slice ID:** `liveness-and-spinners`
- **Outcome:** Running work animates from one shared, phase-locked ticker, so
  concurrent indicators move in lockstep and no renderer owns its own timer.
- **Why this slice exists:** Several slices want an animated indicator (10's
  thinking pulse, 02's running icon, 08's in-flight group). Without a single
  owner they will each add a timer, and the panel will jitter. This slice
  establishes the mechanism once.
- **Depends on:** Slice 02.

---

## omp reference

### One ticker, shared phase

`packages/coding-agent/src/modes/components/tool-execution.ts:190-250`:

```ts
export const SPINNER_RENDER_INTERVAL_MS = 80;
export const SPINNER_GLYPH_ADVANCE_MS = 80;

function sharedSpinnerFrame(frameCount: number, now: number): number {
    return frameCount > 0 ? Math.floor(now / SPINNER_GLYPH_ADVANCE_MS) % frameCount : 0;
}
```

The frame is a **pure function of wall time**, not of a per-component counter.
Consequences:

- All concurrent indicators show the same frame — they spin in lockstep.
- A component that mounts mid-animation joins at the correct phase.
- There is **one** process-wide interval, and each tick issues a
  *component-scoped* repaint rather than a full-transcript repaint.

The transcript allocator passes a single shared clock to every block as
`AnimationFrame = { tick, now }`, where `tick = Math.floor(performance.now()/80)`
(`modes/composer.ts:277`).

### Opt-in

A renderer must declare `animatedPendingPreview` / `animatedPartialResult`
(`tools/renderers.ts:75-80`) to receive a `spinnerFrame` at all. Renderers that
do not consume it get **no ticker** — omp's `github` renderer documents this
explicitly (`gh-renderer.ts:419-421`). Animation is never on by default.

### Frame sets

`modes/theme/symbols.ts:1379-1392`, two spinner *types* per glyph preset:

```
unicode.status   ⣾ ⣽ ⣻ ⢿ ⡿ ⣟ ⣯ ⣷            (8 frames, braille rotor)
unicode.activity ⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏       (10 frames, classic dots)
ascii.status     | / - \
ascii.activity   - \ | /
```

`theme.spinnerFrames` with no argument is the **status** set — the braille rotor
is what appears in tool headers. All frames are single-cell, so nothing reflows.

### The counter-intuitive part: some things deliberately do not animate

This is the finding most worth carrying over.

**Framed card headers do not spin.** `write.ts:1693-1696` and
`edit/renderer.ts:925-929`:

> *"No status icon on the head row: it's the head of the framed block… an
> animated glyph would pin the commit boundary at the top… The liveness cue rides
> the trailing `(preview)` / `(streaming)` line instead."*

The stated reason is omp's native-scrollback commit model, which jig does not
have — but the *visual* argument stands on its own: an animated glyph inside a
card's top border draws the eye to chrome rather than to content, and a still
header with a live footer reads calmer.

**Subagent rows do not spin.** `task/render.ts:963-975`:

> *"Live (or queued) agents use the same dot finished rows keep: detached async
> spawns can stay 'pending' while real work is running, so a pending/hourglass or
> spinner glyph reads wrong in the transcript. Keep the row static."*

This is CC-4 again: state is a color change, not a glyph change.

So omp's actual animation surface is narrow: the thinking pulse, the loader line
under an execution frame, and a few in-progress tool bodies. **Most of the
transcript is still.**

---

## Current jig state

`internal/tui/monitor` has a 100 ms throttled frame loop — the monitor coalesces
engine events into a throttled repaint with dirty flags and step-gated glamour
(`monitor_frame.go`). That is the right foundation; it already solves the
"don't repaint per event" problem.

What exists for liveness in the transcript:

- `shared.IconRunning` (`●`) — static.
- `shared.Theme.Running`, `shared.Theme.Spinner` styles.
- The static `typing…` label at `monitor_transcript.go:850`.
- `toolDisplayRunning` state on an item, currently rendered by appending
  `" · running"` to the label (`items_view.go:87`) — which slice 02 removes.

There is **no animation in the Transcript panel at all** today.

---

## In Scope

- A shared frame source: a pure function of wall time yielding a frame index,
  so any renderer can compute its glyph without owning a timer.
- Decide and document the animation cadence relative to the existing 100 ms
  throttle. Options: quantize animation to the existing 100 ms tick (simplest,
  no new timer), or raise the tick rate while something is animating.
  **Do not add a second independent loop.**
- Wire the frame into slice 02's `Icon` slot for running exchanges.
- Wire the frame into slice 10's thinking pulse.
- Frame sets in the icon vocabulary (CC-7) with an ASCII fallback.
- An explicit opt-in: only items in a running state consume a frame; everything
  else renders a static glyph.
- Ensure animation stops entirely when nothing is running, so an idle monitor
  does no periodic work.

## Out of Scope

- Elapsed-time counters on running items (omp shows these only in its squeezed
  compact card). Consider after slice 11's metrics audit.
- Live output previews for running tools — the live tail already exists
  (`monitor_transcript.go:849-859`) and slice 06 fixes its truncation.
- Animating anything in the Steps panel (epic NG6).
- omp's per-component scoped repaint. jig repaints the panel; that is fine at
  jig's scale.

## Functional Requirements

- **FR-13.1** All concurrently animating indicators shall display the same frame
  at any instant.
- **FR-13.2** The frame index shall derive from wall time, not from a per-item
  counter, so an item entering the animation joins at the current phase.
- **FR-13.3** Every frame in a set shall occupy the same number of terminal
  cells.
- **FR-13.4** Only items in a running state shall animate.
- **FR-13.5** When no item is animating, the panel shall perform no periodic
  repaint on account of animation.
- **FR-13.6** Frame sets shall have an ASCII fallback under the ASCII glyph
  preset.
- **FR-13.7** A card's header shall not animate; liveness for a running card
  shall be conveyed by its border color and, where present, a trailing status
  line.
- **FR-13.8** Animation shall not alter the rendered line count of any item.

## Technical and Repository Constraints

- **The 100 ms throttle is the governing constraint.** jig's monitor coalesces
  events into a 100 ms repaint. omp's 80 ms is faster. Either accept 100 ms
  (a 10 fps spinner, perfectly acceptable) or make the interval conditional on
  something animating. Prefer the former for simplicity; state the choice.
- FR-13.5 matters for battery and for CI: an idle TUI must be genuinely idle.
  Gate the ticker on "any running item currently visible."
- FR-13.7 encodes omp's deliberate choice and CC-4. Slice 02's `Icon` slot should
  therefore receive a spinner only for **non-card** contexts, or the card's
  running state should be expressed purely through the border. **Resolve this
  with slice 01/02 before implementing** — it determines whether the spinner has
  any consumer in a card-based transcript at all.
- Glyphs via the icon vocabulary (CC-7).
- FR-13.8 prevents scroll jitter: an animating item whose height changes would
  shift everything below it every tick.

## Security and Data Considerations

None identified.

## Acceptance Evidence

- A test asserting two indicators computed at the same timestamp yield the same
  frame.
- A test asserting all frames in each set have equal `lipgloss.Width`.
- A test asserting a non-running item renders a static glyph.
- A test asserting no repaint is scheduled when nothing is running.
- A test asserting an item's line count is identical across frames.

## Inputs for the Child Spec

- **Resolve FR-13.7 first.** If cards convey running state through their border
  (slice 01) and headers must not animate (omp's rule), the spinner's only
  transcript consumer may be slice 10's thinking pulse. That would be a fine
  outcome — but it means this slice is smaller than it looks and should be
  sequenced *after* slice 01 lands visually.
- The wall-time-derived frame is the whole mechanism; it is a few lines.
- Do not introduce a `tea.Tick` loop independent of the existing frame loop.
- omp's narrow animation surface is a feature. Resist animating more than the
  pulse and, at most, one running indicator.

## Open Questions

- **Q-13.1 (resolve before implementing)** Given FR-13.7 and slice 01's
  state-colored borders, does a spinner have any consumer besides the thinking
  pulse? *If not, fold this slice into slice 10 and close it.*
- **Q-13.2** Quantize to the existing 100 ms tick, or raise the rate while
  animating? *Suggest quantize; 10 fps is smooth enough for a rotor and costs
  nothing.*
- **Q-13.3** Should the running *step* (as opposed to a running tool exchange)
  carry the pulse in the panel header rather than inline? *Suggest yes — one
  indicator per panel is calmer than one per item.*
