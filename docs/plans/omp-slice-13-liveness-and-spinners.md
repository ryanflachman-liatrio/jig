# Implementation Plan: OMP parity slice 13 — liveness: phase-locked spinners

**Status:** Planned — omp-transcript-parity epic, slice
[`13-liveness-and-spinners`](../epics/omp-transcript-parity/slices/13-liveness-and-spinners.md)
**Risk:** **low** — presentation-only. Pure-function frame source with one
production consumer (the Transcript panel's `LIVE` chip), gated on `anyRunning`,
riding the existing 100 ms frame loop with no second timer. No schema, harness,
transcript, engine, or `Update` control-flow mutation. The chief hazard is the
`ticking` invariant (`monitor_frame.go:49-58` and
`TestMonitorLiveClockNoDuplicateLoops` at `monitor_test.go:3129-3151`): a second
ticker or a self-arming `EnsureFrame` call in the consumer would produce
duplicate loops and dirty the `-race` tests. Every mitigation preserves the
single-owner rule.
**Depends on:** epic slice 02 (four-slot status-line header grammar), already
landed in
[`docs/specs/25-spec-status-line-header-grammar`](../specs/25-spec-status-line-header-grammar/25-spec-status-line-header-grammar.md);
`shared.ToolStatusIcon`, `IconStatus*`, and `Card.Border*` establish the icon
contract this slice writes into and the border-based running state that keeps
card headers still (FR-13.7). Uses the existing frame loop in
`internal/tui/monitor/monitor_frame.go` (`monitorFrameInterval = 100ms`,
`EnsureFrame`, `anyRunning`) without modification.
**Complements:** goal
[T10](open-goals.md#c-transcript-experience--path-to-best-in-class)
(richer tool lifecycle — progress/spinner cue for running work). Establishes
the mechanism that slice 10 (`inline-thinking`) will consume for its thinking
pulse without inventing a parallel ticker.
**Breaks:** the static `LIVE` chip composed at
`monitor_view.go:256-263`/`:469-476` becomes a **glyph + label** pair while the
Transcript panel is following a running step; the persistent text label
survives so accessibility, ASCII fallback, and search text remain identical.
No test that asserts the literal substring `"LIVE"` regresses (the label stays
verbatim; the pulse glyph precedes it).

---

## Summary

Introduce a pure-function shared frame source
`shared.SpinnerFrame(set, now) → (glyph, ok)` derived from wall time, plus one
named frame set `"status"` (8-frame braille rotor, one cell) with a
four-frame ASCII fallback (`| / - \`) under the ASCII glyph preset. Wire a
single production consumer: the Transcript panel's `LIVE` crumb becomes
`<frame> LIVE` while `chatAutoScroll` is true and a running step exists.
Gate the ticker on the pre-existing `anyRunning` check so the pulse consumes
no periodic work when nothing is running. Establish the API and vocabulary so
slice 10 registers `spinner.thinking` at implementation time and slice 14
picks up ASCII fallback in one place — do not preempt either. The top risk is
the `ticking` single-owner invariant; the mitigation is that the consumer only
reads a frame from wall-clock time and never arms a timer itself.

## Approach

Add `internal/tui/shared/spinner.go` with:

- `SpinnerFrameSets`, a package-level registry of `map[string]SpinnerFrames`
  seeded with `"status"` (braille rotor) and each set's ASCII fallback.
- `SpinnerFrame(name string, now time.Time) (glyph string, ok bool)`, a
  wall-time-derived frame lookup: `now.UnixMilli() / SpinnerAdvanceMS` modulo
  the set's frame count under the active glyph preset. `ok` is false when the
  set is unknown so callers can no-op cleanly. There is no per-caller state.
- `SpinnerAdvanceMS = 100`, matching `monitor.monitorFrameInterval` so no
  frame ever arrives that the monitor has not already scheduled a repaint for
  (FR-13.5).

Wire one consumer in the Transcript panel chrome. Extend
`monitor_view.go:transcriptPanelTitleParts` and `statusLineView` so that, when
the current `selectedContent()` is `contentTranscript`,
`showsTranscriptFollow()` is true, `chatAutoScroll` is true, **and**
`m.anyRunning()` reports true, the `"LIVE"` label becomes `"<frame> LIVE"`
where `<frame>` comes from `shared.SpinnerFrame("status", time.Now())`. When
any precondition fails, render the same static `"LIVE"` word the panel renders
today — no regression on non-live/non-following/paused/no-step-running paths.

The frame loop is already ticking whenever `anyRunning()` is true
(`monitor_frame.go:53`), so the `TickMsg` handler in `monitor_update.go:78-93`
already re-arms until the run settles. The mechanism therefore piggybacks on
the existing 100 ms cadence without adding a `tea.Tick`, without touching
`EnsureFrame`, and without arming the loop from a `View` call — `View` remains
pure per the TUI ownership rule (`docs/TUI.md:8-15`).

## Problem

The Transcript panel's identity strip and title carry a `"LIVE"` static label
while the panel is following a running step (`monitor_view.go:256-263`,
`:470-476`). The word is legible but reads as a stamp, not a live signal. omp
publishes a broader animation surface — a shared wall-time frame consumed by
several renderers so every concurrent indicator spins in lockstep
(`packages/coding-agent/src/tui/output-block.ts:73-81`,
`modes/components/tool-execution.ts:190-250`,
`modes/theme/symbols.ts:1379-1392`, transcribed in
[`slices/13-liveness-and-spinners.md`](../epics/omp-transcript-parity/slices/13-liveness-and-spinners.md)).

Two forces converge on this slice:

1. **A ticker will be added anyway.** Slice 10 will need a pulse for
   reasoning; slice 08 (grouping) already has a running-group state; slice 02
   left `IconStatusRunning` deliberately equal to `IconStatusPending` so a
   settling row would not swap between spinners in the header slot (CC-4).
   Without a shared source, each future consumer will bring its own timer and
   the panel will jitter with independent loops. Establishing the source once
   preempts three subsequent regressions.
2. **The existing 100 ms frame loop is the right foundation.**
   `monitor_frame.go` already coalesces engine events into a bounded repaint
   and only ticks while an unfinished step or a dirty panel exists
   (`EnsureFrame` at `:49-58`, `anyRunning` at `:82-88`). It solves the "no
   repaint per event" and "idle costs nothing" problems that FR-13.5 asks
   for. What is missing is a *frame index* callers can read.

Consequence today: the transcript's only live indicator sits at 0 fps. An
operator watching a long-running agent turn cannot tell from the panel chrome
whether the stream is progressing or the process has stalled — only from
transcript text (which is throttled behind glamour) and from the Steps panel
(which paints out of the same frame loop but through a different route). The
liveness gap is real; the mechanism to close it is small.

The presentation grammar for the panel-header pulse is fully described by
omp's `usage-row.ts` and by epic decisions **CC-2** / **CC-3** / **CC-4** (no
appended state prose; success recedes; state transitions are color changes,
not glyph changes) and **CC-7** (glyphs through the icon vocabulary). Card
headers do not animate per FR-13.7. The remaining work is: answer Q-13.1
with a consumer audit, choose from Q-13.2/Q-13.3 with defensible defaults,
add the frame source, and hook one row into the existing chrome without
breaking the `ticking` invariant.

---

## Consumer availability audit (Q-13.1)

The slice document asks whether a spinner has any consumer besides the
thinking pulse (slice 10). Here is the audit; the resolution follows.

| Candidate consumer | Site | Currently animated? | State carried instead | Available for slice 13? |
|---|---|---|---|---|
| **Card header status glyph** | `monitor_transcript_items_view.go:194` via `composeToolHeader` → `shared.ToolStatusIcon` (`internal/tui/shared/status_icon.go:30-40`) | No. `IconStatusRunning == IconStatusPending == "○"` (`shared/icons.go:26-28`). | Border color (`Card.BorderRunning`, `styles.go:295`). | **No.** FR-13.7 forbids animating card headers; CC-4 forbids per-state glyph swaps. The border already conveys running. Explicitly excluded. |
| **Tool-exchange card border** | `shared.RenderCard` in `renderToolExchangeCard` (`items_view.go:246-268`) | No (static color). | Same as above. | **No.** A pulsing border would repaint the whole card each frame; slice 04's line-range invariants would need to survive a per-frame render. Out of scope. |
| **Read-group card** | `monitor_read_group_view.go` | No. | Border color + collapsed count. | **No.** Same reasoning as the exchange card. |
| **Boundary banner** | `monitor_transcript_banner.go` | No. | Centered rule label (`"reset N"`, `"iteration N"`, `"retry N"`). | **No.** Banners describe a completed transition, not an ongoing state. Animating them would be nonsense. |
| **Turn metadata row** | `monitor_transcript_metadata.go` | No. | Dim text (time / Δ / iter / attempt / cost / tokens). | **No.** The row is reference material; a spinner would make it look like data. |
| **Steps panel `stepIndicator`** | `monitor_steps.go:327` | No (settles from `●` running to `✓` succeeded). | Colored glyph transition — a **glyph change**, allowed here per NG6 (Steps is out of the CC-4 rule). | **No.** NG6 excludes the Steps panel from this epic. Its indicator is out of scope. |
| **Live tail placeholder / typing label** | previously `monitor_transcript.go:850` | Not present. | — | **N/A.** Already removed on `main`; no `"typing…"` string remains in `internal/tui/monitor/` (verified). The only remaining `Theme.Question.Render(placeholder)` in `monitor_transcript.go:811` is the `fileBody` empty-file placeholder, not a liveness cue. |
| **Slice 10 thinking pulse** | `assistant-message.ts:80-98` in omp; jig has `Theme.Chat.Thinking` (`shared/styles.go:265`) but no pulse yet. | N/A — slice 10 not landed. | Static `▸ ◇ reasoning` stub at `items_view.go:207-211`. | **Future.** The natural consumer, deferred to slice 10 per the epic dep graph (10 depends on 09, and 09 has no plan yet). Slice 13 must ship the mechanism so slice 10 can register `spinner.thinking` in one line. |
| **Transcript panel `LIVE` chip** | `monitor_view.go:256-263` (status line) and `:470-476` (panel title crumb) | No (static). | Word `"LIVE"`. | **Yes.** Q-13.3 argues for one indicator per panel over one per item; this is that indicator. Preconditions match the ticker's own gating (`chatAutoScroll` and a running step whose stream is being followed). |

### Resolution (Q-13.1)

Ship the mechanism *with one production consumer today*, not folded into
slice 10. The consumer is the Transcript panel's `LIVE` chip, promoted from a
static word to `<frame> LIVE`. Rationale:

- **Demoable outcome.** Slice 13 lands with a visible, testable liveness cue;
  otherwise the mechanism sits unused until slice 10, whose own dependency
  chain (09 → 10) has no plan yet.
- **No consumer collision.** The chip is already gated on the exact
  preconditions the ticker uses: `chatAutoScroll && anyRunning() &&
  showsTranscriptFollow()`. Idle-panel-does-no-work (FR-13.5) is preserved by
  construction.
- **No epic invariants broken.** FR-13.7 (card headers do not animate), CC-2
  (state via border/color, not appended prose), and CC-4 (no glyph swaps
  mid-state) are all respected because the header stays static and only the
  chrome above the panel body animates.
- **Slice 10 gets the API.** `shared.SpinnerFrame("thinking", now)` becomes
  slice 10's one-line consumer; the frame source is written once, registered
  frame sets accumulate as later slices add them.

## Open-question resolutions

Each is defaulted so implementation can proceed; each records the rationale so
a reviewer can overturn it before code lands.

- **Q-13.1** — Answered by the audit. The panel `LIVE` chip is the single
  production consumer today; slice 10 registers `spinner.thinking` when it
  lands and reuses the same source. **Do not fold slice 13 into slice 10:**
  the mechanism is the point, and a mechanism without a consumer is untested.
- **Q-13.2** (cadence) — **Quantize to the existing 100 ms tick.** Set
  `SpinnerAdvanceMS = 100` so `SpinnerFrame` yields the same index at any
  timestamp inside the same 100 ms window that `TickMsg` already schedules a
  flush for. 10 fps for an 8-frame set is one revolution in 800 ms —
  comfortably readable, and matches the frame loop's rate without adding a
  second timer (FR-13.5, technical constraint 1).
- **Q-13.3** (per-item vs per-panel pulse) — **Per panel.** The `LIVE` chip is
  the single indicator; card headers stay static and boundary banners, turn
  rows, and text items never carry a spinner. Slice 10 will add a *second*
  panel-scoped indicator (the thinking pulse next to the reasoning body), not
  a per-item spinner. One indicator per panel is calmer than one per item and
  matches omp's `[t.description]` posture in `transcript-container.ts`.

---

## Row grammar

There is exactly one animated site in this slice. Its grammar is:

```
… › [TRANSCRIPT] › ⣾ LIVE
```

Rendered where the current `LIVE` word already renders — the trailing
breadcrumb crumb (`monitor_view.go:469-476`) and the status line
(`monitor_view.go:256-263`). Both call sites share a single helper
`liveCrumb(now time.Time) string` that returns `"⣾ LIVE"` (frame chosen by
`SpinnerFrame`) under the four preconditions and `"LIVE"` otherwise. Style is
already `shared.Theme.StatusLine` for the status line and
`shared.Theme.Panel.Title` for the crumb; the frame glyph inherits from those
styles and is never independently colored. The persistent `"LIVE"` label is
mandatory — per omp `assistant-message.ts:451-474`, the label keeps the pulse
descriptive for terminals and screen readers, and slice 14's ASCII fallback
will replace the glyph in one place without losing the word.

### What the pulse is not

- Not per-item. No card, no metadata row, no boundary banner, no text item
  gains a spinner.
- Not per-step. The Steps panel `stepIndicator` (`monitor_steps.go:327`) is
  untouched — NG6 excludes it from this epic.
- Not a rate badge. omp's `1,284 · 42.3 toks/s` badge is deferred to slice
  10's `Inputs for the Child Spec` and requires per-block token deltas the
  harness does not currently surface (CC-12).
- Not a border pulse. Card borders stay a fixed color per state; per-frame
  border repainting would fight slice 01's tint stabilization (CC-9).
- Not a `View`-armed timer. `View` reads the frame and returns; the tick that
  drives the next paint comes from the existing `TickMsg` loop.

---

## Where the frame arrives

The current tick pipeline (transcribed from `monitor_frame.go` and
`monitor_update.go`):

```
EngineEventMsg ─► m.dirtyChat |= eventAffectsChat        (monitor_update.go:60-67)
                  m.flushDirty()  (leading edge only)
                  return m.EnsureFrame()  (arms tick if !ticking && (dirty || anyRunning))

TickMsg ────────► if m.anyRunning() { m.dirtyList = true }
                  m.flushDirty()  (chat repaints via SetContent(m.chatBody()))
                  if anyRunning || (ready && (dirtyList || dirtyChat)) {
                      return monitorTickCmd()  // re-arm
                  }
                  m.ticking = false
```

Slice 13 modifies this pipeline in exactly zero places. Instead, the
animation site reads the current frame during `View` construction:

1. `transcriptPanelTitleParts` (`monitor_view.go:456-478`) calls
   `m.liveCrumb(time.Now())` where the current `"LIVE"` literal is composed
   (line 471).
2. `statusLineView` (`monitor_view.go:239-277`) does the same at its
   `"LIVE"` composition (line 259).
3. `liveCrumb` calls `shared.SpinnerFrame("status", now)` and prepends the
   glyph and a space when all four preconditions hold; otherwise it returns
   `"LIVE"` unchanged.

Because `anyRunning()` is already true whenever the pulse should be visible,
the frame loop is already re-arming (`TickMsg` handler at
`monitor_update.go:89-91`). No new call to `EnsureFrame`, no new `tea.Tick`,
no self-arming from `View`. The `ticking` invariant is preserved by omission.

### Line accounting

Slice 13 does not add, remove, or resize any row inside the transcript body.
`chatItemLineRanges`, `n`/`N` navigation, and slice-04 rhythm are untouched.
The animated site is the panel title (outside the viewport) and the status
line (below the panels).

---

## Architecture and ownership

```text
internal/tui/shared
  ├─ spinner.go                (new file)
  │   • SpinnerAdvanceMS = 100  — matches monitor.monitorFrameInterval.
  │   • SpinnerFrames struct   — unicode []string, ascii []string.
  │   • SpinnerFrameSets       — map[string]SpinnerFrames seeded with "status".
  │   • RegisterSpinnerSet(name string, set SpinnerFrames)  — slice 10 hook.
  │   • SpinnerFrame(name string, now time.Time) (glyph string, ok bool).
  │   • spinnerPresetIsASCII() bool  — reads the same glyph preset switch that
  │     slice 14 will introduce; today the ASCII branch is unreachable and the
  │     unicode set always wins. The branch exists so slice 14 flips one
  │     boolean rather than touching every caller.
  │
  ├─ spinner_test.go           (new file)
  │   • frame-index equality across concurrent lookups at one timestamp.
  │   • frame-index rotates by SpinnerAdvanceMS.
  │   • every unicode frame has lipgloss.Width == 1.
  │   • every ascii frame has lipgloss.Width == 1.
  │   • unknown-set lookup returns ("", false).
  │
  └─ icons.go
      • no change. Frame glyphs live in spinner.go's frame-set table so slice
        14's preset flip is one file; icons.go stays the vocabulary for
        singleton glyphs, not families.

internal/tui/monitor
  ├─ monitor_view.go            (primary edit site)
  │   • add liveCrumb(now time.Time) string.
  │   • replace the literal "LIVE" at :259 (status line) and :471 (title
  │     crumb) with liveCrumb calls.
  │
  ├─ monitor_view_test.go       (or a new monitor_liveness_test.go)
  │   • assert liveCrumb renders "<glyph> LIVE" when all four preconditions
  │     hold; "LIVE" otherwise.
  │   • assert `lipgloss.Width(liveCrumb(t))` is constant across a full 800 ms
  │     revolution (frames are single-cell + " " + "LIVE" is fixed).
  │
  └─ monitor_test.go            (existing suite additions)
      • one test proving no additional tea.Tick is scheduled by adding the
        consumer: TickMsg count over an 800 ms simulated advance is identical
        to the count with the consumer disabled.
```

Zero new types cross the `internal/tui/monitor` boundary. The shared package
gains one file; nothing depends on it besides `monitor_view.go` today.
`internal/tui/shared/spinner.go` uses only `time` and (transitively) the
package-scope frame-set table; no import of `internal/tui/monitor` (which
would create a cycle per `docs/ARCHITECTURE.md` and the renderer discipline
test at `internal/tui/shared/renderer_discipline_test.go`).

### Not touched by slice 13

- `internal/engine` — no new event, no new envelope field.
- `internal/transcript` — wire format unchanged (epic CC-12).
- `internal/harness/*` — no capability change.
- `internal/tui/monitor/monitor_frame.go` — the ticker owner is unchanged;
  slice 13 rides existing re-arm semantics.
- `internal/tui/monitor/monitor_update.go` — no new `TickMsg` handling.
- `chatItemLineRanges` and every slice-04 line-count invariant.
- `IconStatusRunning` / `IconStatusPending` — they stay equal (CC-4).
- `Card.Border*` styles — running is a border color, not an animation.
- Every transcript body renderer (cards, banners, metadata rows, thinking
  stub, text items, read groups).

---

## Delivery phases

Small enough to land as one PR, sequenced for reviewability.

### Phase 1 — the frame source as a pure function

1. Add `internal/tui/shared/spinner.go` with `SpinnerAdvanceMS`,
   `SpinnerFrames`, the package-level frame-set registry seeded with
   `"status"` (`⣾ ⣽ ⣻ ⢿ ⡿ ⣟ ⣯ ⣷` unicode, `| / - \` ascii),
   `RegisterSpinnerSet`, and `SpinnerFrame(name, now)`.
2. Add `internal/tui/shared/spinner_test.go` covering:
   - two concurrent lookups at the same `now` return the same glyph;
   - lookups `SpinnerAdvanceMS` apart return adjacent glyphs (index+1 mod N);
   - every unicode frame has `lipgloss.Width == 1`; every ascii frame has
     `lipgloss.Width == 1`;
   - an unknown set name returns `("", false)` so callers can no-op cleanly.

**Exit:** the source is a pure function, tested, available for wiring.

### Phase 2 — one consumer, gated on the existing ticker

1. Add `liveCrumb(now time.Time) string` in `monitor_view.go` (or a
   colocated helper file). The function returns `"LIVE"` unless all four
   preconditions hold: `selectedContent().kind == contentTranscript`,
   `showsTranscriptFollow()`, `chatAutoScroll`, and `m.anyRunning()`. Under
   those conditions it returns `"⣾ LIVE"` (frame from
   `shared.SpinnerFrame("status", now)`), or the static `"LIVE"` if the
   frame source returns `!ok`.
2. Replace the literal `"LIVE"` at `monitor_view.go:259` and
   `monitor_view.go:472` with `m.liveCrumb(time.Now())`. No other
   `monitor_view.go` change.

**Exit:** the pulse renders in place of the static crumb when the run is live
and a running step is being followed; every other panel state is
byte-identical to `main`.

### Phase 3 — invariants under the existing frame loop

1. Add `TestLiveCrumbPulsesWhileRunning`, `TestLiveCrumbStaticWhenNoRunning`,
   `TestLiveCrumbStaticWhenNotFollowing`,
   `TestLiveCrumbConstantWidthAcrossOneRevolution`, and
   `TestLiveCrumbAddsNoTickers` (see "Test matrix").
2. Re-run `internal/tui/monitor` tests. The existing
   `TestMonitorLiveClockNoDuplicateLoops` (`monitor_test.go:3129-3151`) is
   the sentinel that catches an accidental self-arming: it must pass
   unchanged. Any test asserting the literal `"LIVE"` still passes because
   the label survives verbatim.

**Exit:** `go test ./internal/tui/monitor -race -count=1` and the full
`go test ./...` are clean, `gofmt -l` on changed files is empty,
`go vet ./...` is clean.

### Phase 4 — proof capture and docs

1. Capture a deterministic 80-column monitor scene under
   `docs/specs/25-spec-liveness-and-spinners/25-proofs/` (new spec directory)
   showing: (a) the panel title with `⣾ LIVE` while following a running
   step, (b) the same panel with a settled step showing the static `"LIVE"`,
   (c) the same panel with `chatAutoScroll == false` showing the static
   `"LIVE"`, and (d) an idle monitor with no running steps showing no `LIVE`
   crumb at all. Save `.ansi`, `.html`, and `-notes.txt`, matching slice
   11/12 proof format.
2. Update slice-13 open questions in
   `docs/epics/omp-transcript-parity/slices/13-liveness-and-spinners.md`
   with the resolutions above, marking Q-13.1 answered.
3. Add a cross-link from
   [`docs/plans/open-goals.md`](open-goals.md) T10 to this plan.

**Exit:** proofs recorded, epic slice document reflects resolved questions,
T10 has a plan reference.

---

## Ordered implementation tasks

Estimates are focused-agent wall time; every substantive code change has a
sibling test task. Task areas are `<Go import path> — <file>` so the mapping
is unambiguous.

| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Add `SpinnerAdvanceMS`, `SpinnerFrames`, `SpinnerFrameSets` registry seeded with the `"status"` braille rotor and ASCII fallback, `RegisterSpinnerSet`, and `SpinnerFrame(name, now)` in a new file | `internal/tui/shared — spinner.go` (new) | 25 min |
| 2 | Table-test `SpinnerFrame` over concurrent-timestamp equality, index rotation at `SpinnerAdvanceMS`, single-cell width for every frame in every set, and unknown-set → `("", false)` | `internal/tui/shared — spinner_test.go` (new) | 25 min |
| 3 | Add `liveCrumb(now time.Time) string` in `monitor_view.go` (or `monitor_liveness.go`) reading `SpinnerFrame("status", now)` under the four preconditions | `internal/tui/monitor — monitor_view.go` (or new file) | 15 min |
| 4 | Replace `"LIVE"` at `monitor_view.go:259` and `:471` with `m.liveCrumb(time.Now())` | `internal/tui/monitor — monitor_view.go` | 10 min |
| 5 | New test `TestLiveCrumbPulsesWhileRunning` seating a running step + following-follow, asserting `liveCrumb(t0) != liveCrumb(t0 + SpinnerAdvanceMS)` and both start with a unicode frame followed by ` LIVE` | `internal/tui/monitor — monitor_liveness_test.go` (new) | 20 min |
| 6 | New test `TestLiveCrumbStaticWhenNoRunning` seating no running step, asserting `liveCrumb(t) == "LIVE"` regardless of `now` | `internal/tui/monitor — monitor_liveness_test.go` | 15 min |
| 7 | New test `TestLiveCrumbStaticWhenNotFollowing` seating a running step but `chatAutoScroll == false`, asserting `liveCrumb == "LIVE"` | `internal/tui/monitor — monitor_liveness_test.go` | 15 min |
| 8 | New test `TestLiveCrumbConstantWidthAcrossOneRevolution` iterating an 8-frame revolution, asserting `lipgloss.Width` is identical for every frame so no crumb width jitter shifts the trailing breadcrumb slot | `internal/tui/monitor — monitor_liveness_test.go` | 20 min |
| 9 | New test `TestLiveCrumbAddsNoTickers` counting `TickMsg` schedules over a simulated 800 ms with the consumer wired vs. a control run without it, asserting identical counts (proves FR-13.5 and the `ticking` invariant survive) | `internal/tui/monitor — monitor_liveness_test.go` | 25 min |
| 10 | Regression: re-run `TestMonitorLiveClockNoDuplicateLoops` and every existing test asserting the string `"LIVE"` unchanged; grep-audit `View()` output for a `" LIVE"` substring so a future ASCII preset swap that drops the space is caught | `internal/tui/monitor — monitor_test.go, monitor_view_test.go` | 15 min |
| 11 | Capture deterministic 80-column proofs (running + following, settled, not following, idle) under `docs/specs/25-spec-liveness-and-spinners/25-proofs/` with `.ansi`, `.html`, `-notes.txt` per slice-11/12 conventions | `docs/specs/25-spec-liveness-and-spinners/25-proofs/` (new) | 30 min |
| 12 | Mark `Q-13.1` answered and record the Q-13.2 / Q-13.3 resolutions in the slice document | `docs/epics/omp-transcript-parity/slices/13-liveness-and-spinners.md` | 10 min |
| 13 | Cross-link `T10 Richer tool lifecycle` in `docs/plans/open-goals.md` to this plan | `docs/plans/open-goals.md` | 5 min |

Estimated focused implementation time: **3.5 hours**, one PR.

---

## Test matrix

### New unit and behavioral tests

| Test | Purpose | Fixture shape |
|---|---|---|
| `TestSpinnerFrameSameTimestampSameFrame` | Pure-function determinism (FR-13.1): two lookups at the same `now` return the same glyph | fixed `time.Time`, two concurrent lookups |
| `TestSpinnerFrameAdvancesWithClock` | Adjacent windows return adjacent frames (FR-13.2) | `t`, `t + SpinnerAdvanceMS`, `t + 2*SpinnerAdvanceMS` |
| `TestSpinnerFrameEveryFrameSingleCell` | Every unicode frame has `lipgloss.Width == 1` (FR-13.3) | iterate `spinner.status.unicode` |
| `TestSpinnerFrameASCIIFallbackSingleCell` | Every ASCII frame has `lipgloss.Width == 1` (FR-13.6) | iterate `spinner.status.ascii` |
| `TestSpinnerFrameUnknownSetReturnsNotOK` | Unknown set → `("", false)` so callers no-op | pass an unregistered name |
| `TestLiveCrumbPulsesWhileRunning` | The chip renders `<frame> LIVE` when all four preconditions hold; two different `now` values produce two different frames but the same trailing label | one running step, `chatAutoScroll == true`, transcript follow |
| `TestLiveCrumbStaticWhenNoRunning` | The chip renders `"LIVE"` (no frame) when `anyRunning() == false` (FR-13.4) | one succeeded step, following on |
| `TestLiveCrumbStaticWhenNotFollowing` | The chip renders `"LIVE"` when `chatAutoScroll == false` | running step, follow off |
| `TestLiveCrumbAbsentWhenNoLiveContext` | No crumb at all when the panel shows a file or review (`selectedContent().kind != contentTranscript`) | file selection with a running step |
| `TestLiveCrumbConstantWidthAcrossOneRevolution` | `lipgloss.Width(liveCrumb(t))` is identical for every frame in one 800 ms revolution (FR-13.8) | iterate frames |
| `TestLiveCrumbAddsNoTickers` | Total `TickMsg` count over a simulated 800 ms with the consumer is equal to the control count without it (FR-13.5 and the `ticking` invariant) | simulate frame loop with running + settled runs |

### Regression tests to re-run unchanged

| Test | File | Expected outcome |
|---|---|---|
| `TestMonitorLiveClockNoDuplicateLoops` | `monitor_test.go:3129-3151` | passes unchanged — the sentinel that catches self-arming loops |
| `TestMonitorTickerStopsWhenIdle` (`monitor_test.go:2870-2915` neighborhood) | same | passes unchanged — the ticker still falls silent when no step runs |
| Every test asserting the substring `"LIVE"` in `View()` | `monitor_test.go`, `monitor_view_test.go` | passes unchanged — the label is still emitted verbatim, now preceded by an optional frame glyph |
| Line-range and rhythm regressions (`monitor_vertical_rhythm_test.go`, `monitor_transcript_card_test.go`) | same | passes unchanged — slice 13 emits nothing inside the transcript body |
| `TestTranscriptCardLineRangesCachedAndFresh` | `monitor_transcript_card_test.go` | passes unchanged — the card render cache is not touched |
| Slice-11 metadata row tests | `monitor_transcript_metadata_test.go` | passes unchanged |
| Slice-12 boundary banner tests | `monitor_transcript_banner_test.go` | passes unchanged |

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

`gofmt -l` on the changed files must be empty. Terminal-capture proofs are
regenerated by rerunning the slice-13 visual test with `JIG_UI_SNAPSHOT_DIR`
set.

---

## Security and failure handling

- **No new sensitive surface.** The frame source is a pure function of the
  current wall-clock time. It reads nothing from the environment, the
  filesystem, or the run state. The consumer decides whether to prepend the
  glyph based on already-visible monitor state.
- **No new data path.** Nothing is written; nothing is read from disk. The
  animation cost is one map lookup and one integer modulo per `View`.
- **Persistence-off.** With `RunDir == ""` there is no running step to gate
  on (`anyRunning() == false`), so `liveCrumb` returns `"LIVE"` verbatim and
  the frame source is not consulted. No new branch required.
- **Journal replay after crash.** `anyRunning` is derived from
  reconstructed `step.Status`; the pulse resumes on reopen exactly as on a
  live run once a step re-enters `StatusRunning`. No new persistence.
- **Wall-clock jump.** A monotonic clock jump (system time change, NTP step,
  suspend/resume) shifts the frame index by the jump amount; because the
  frame set is cyclic and single-cell, the visible effect is a one-frame
  discontinuity with no width change. Not a defect — this is what quantized
  wall-time animation *means* — but documented so a reviewer does not read
  it as a bug.
- **Non-Unicode terminals.** Under the ASCII glyph preset the frame source
  serves the ASCII fallback set; every ASCII frame is single-cell, so no
  width jitter. Slice 14 flips the preset in one place; slice 13 must not
  hardcode the unicode frames at the consumer.

---

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| The consumer accidentally arms a second `tea.Tick` (violates the `ticking` single-owner invariant and `TestMonitorLiveClockNoDuplicateLoops`) | The consumer reads a frame from `time.Now()` inside `View`; it does not call `EnsureFrame`, does not return a `tea.Cmd`, and does not spawn a goroutine. Task 9 (`TestLiveCrumbAddsNoTickers`) locks this by counting `TickMsg` schedules with and without the consumer. |
| A future ASCII-preset frame changes the glyph width (a two-cell fallback shifts the trailing breadcrumb slot every tick) | `TestSpinnerFrameASCIIFallbackSingleCell` fails at PR review if a set author adds a wide-glyph fallback. Slice 14 owns the preset flip; the width contract lives in the frame-set test. |
| `View` becomes a hot path once it starts calling `time.Now()` and doing a map lookup every frame | The lookup is O(1) and the two call sites are the panel title crumb and status line — one call each per `View`. This is already the cost profile of the existing static `LIVE` word plus one integer modulo. No optimization required. |
| Slice 10 registers a `"thinking"` frame set incompatible with `SpinnerAdvanceMS` (omp uses an eased 70–230 ms dwell) | Slice 10 must accept the quantized cadence or coordinate a rate change with slice 13; this plan explicitly disallows a second timer. Slice 10's spec inputs already flag Q-10.2 for exactly this coordination. |
| A test asserts the *exact* character `"L"` immediately after the last breadcrumb separator (would break when the glyph precedes it) | Task 10 grep-audits `View()`-facing tests for the substring `" LIVE"` (with leading space) and `"› LIVE"` (with breadcrumb) so any assertion that would need updating is reviewed as an intentional layout delta. |
| A reviewer expects slice 13 to fold into slice 10 per the slice document's Q-13.1 wording | The Consumer audit explicitly enumerates the panel-header pulse as a second consumer today, and the mechanism-first design is the same design slice 10 will reuse. The resolution is recorded in this plan and, in phase 4, in the slice document itself. |
| The pulse implies a run health signal (i.e., animation frozen = process wedged) that it does not actually provide | Documented under "Security and failure handling"; the pulse is quantized to the frame loop and stops when `anyRunning` clears. It is a liveness cue, not a health probe. Slice 10's persistent text label is the reason a persistent word must accompany the glyph — CC-4 and screen-reader parity. |

---

## Completion criteria

Slice 13 is done when all of the following hold against a real multi-step
run with at least one running step, one settled successful step, and one
paused (non-following) transcript view:

1. **CC-13.1** — `shared.SpinnerFrame("status", now)` returns the same glyph
   for every call inside one `SpinnerAdvanceMS` window and rotates to the
   next glyph in the frame set at the next window boundary. Every frame in
   every registered set has `lipgloss.Width == 1`.
2. **CC-13.2** — The Transcript panel title's trailing crumb renders as
   `<frame> LIVE` while (a) content is `contentTranscript`, (b)
   `showsTranscriptFollow()` is true, (c) `chatAutoScroll` is true, and (d)
   `anyRunning()` is true. When any precondition is false the crumb renders
   as the static `"LIVE"` (unchanged from `main`).
3. **CC-13.3** — The status line's `"LIVE"` entry follows the same
   promotion rule.
4. **CC-13.4** — When `anyRunning()` is false, no `TickMsg` is scheduled
   solely on account of the pulse; `TestLiveCrumbAddsNoTickers` proves it.
5. **CC-13.5** — `TestMonitorLiveClockNoDuplicateLoops` passes unchanged.
   The `ticking` field is set and cleared exclusively by
   `monitor_frame.EnsureFrame` and the `TickMsg` handler; no code path in
   `monitor_view.go` mutates it.
6. **CC-13.6** — Every existing test that asserts the substring `"LIVE"`
   in `View()` continues to pass. No test regresses on account of the
   glyph being prepended.
7. **CC-13.7** — Persistence-off runs still render the panel with no `LIVE`
   crumb (no running step); no `RunDir == ""` regression is introduced.
8. **CC-13.8** — `go build ./cmd/jig`, `go test ./...`,
   `go test -race ./internal/tui/...`, `go vet ./...`,
   `gofmt -l <changed-go-files>` (empty), and
   `go run ./cmd/jig validate .agents/jig/sdd.toml` pass.
9. **CC-13.9** — Terminal-capture proofs under
   `docs/specs/25-spec-liveness-and-spinners/25-proofs/` include the four
   documented scenes and the pulse appears in each expected position.
10. **CC-13.10** — The slice document
    [`slices/13-liveness-and-spinners.md`](../epics/omp-transcript-parity/slices/13-liveness-and-spinners.md)
    records Q-13.1 as answered by the audit above, and Q-13.2/Q-13.3 record
    the resolutions applied.
