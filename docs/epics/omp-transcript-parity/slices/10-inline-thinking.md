# Slice 10 — Inline thinking and the streaming pulse

- **Slice ID:** `inline-thinking`
- **Outcome:** Agent reasoning renders as italic, muted prose inline with the
  conversation instead of a collapsed one-line stub, and a running step shows an
  animated pulse rather than a static label.
- **Why this slice exists:** Reasoning is the most informative content a
  workflow agent produces, and jig currently hides it behind a collapsed row
  labeled `reasoning`. Making it visible by default is a one-line change to the
  render branch; the pulse is what makes a long-running step feel alive.
- **Depends on:** Slice 09.

---

## omp reference

### Thinking is prose

`packages/coding-agent/src/modes/components/assistant-message.ts:1065-1069`:

```ts
new Markdown(thinkingText, 1, 0, getMarkdownTheme(), {
    color: (text) => theme.fg("thinkingText", text),
    italic: true,
})
```

Same 1-column inset as ordinary prose, same markdown pipeline — only the color
(`thinkingText`, a muted gray) and the italic flag differ. There is **no
collapse, no header, no glyph**. Reasoning simply reads as a quieter voice in the
same column.

### The streaming pulse

`assistant-message.ts:80-98`:

```ts
const THINKING_DOTS_FRAMES = ["✻", "✼", "❉", "❊", "✺", "✹", "✸", "✶"] as const;
const THINKING_DOTS_FRAME_MS_MIN = 70;
const THINKING_DOTS_FRAME_MS_MAX = 230;
```

In-source description:

> *"A single fixed-width starburst cycles through facets (✻ ✼ ❉ ❊ ✺ ✹ ✸ ✶) so the
> indicator animates in place without shifting the line or the trailing speed
> badge."*

The dwell per frame eases on a raised cosine across each revolution
(`:485-489`):

```ts
const phase = (1 - Math.cos((2 * Math.PI * frame) / FRAMES.length)) / 2;
return MIN + (MAX - MIN) * phase;
```

Quickest at the cycle start, slowest at its midpoint — it *breathes* rather than
ticking. Mean ≈ 150 ms. It is a self-rescheduling `setTimeout`, not a fixed
interval, precisely so each frame can choose its own dwell.

**Fixed-width frames are the point.** Every glyph is one cell, so the line and
the badge after it never reflow.

### The label and speed badge

`assistant-message.ts:451-474`:

```
✻ Thinking · 1,284 · 42.3 toks/s
│     │        │        └─ lerped dim → accent by rate, truecolor only
│     │        └─ total provider tokens, dim
│     └─ muted, always present (screen-reader / non-animating terminals)
└─ thinkingText color
```

Two judgment calls worth copying:

- The numeric badge renders **only while genuinely streaming**. A block that has
  observed no token delta, or whose rate has decayed below 0.05, drops it
  entirely — *"the persistent text label keeps the pulse descriptive for
  terminals and screen readers."*
- The rate color eases with `sqrt(rate / SPEED_MAX)` so typical mid-stream rates
  already read as accent-tinted, rather than staying gray until a rarely-hit
  ceiling.

Rate is a windowed average over 3 s, clamped at 200 tok/s, reset per block so a
previous turn's rate never leaks (`:100-146`).

---

## Current jig state

`internal/tui/monitor/monitor_transcript_items_view.go:105-109`:

```go
case transcriptItemThinking:
    b.WriteString(prefix + marker + " " + shared.Theme.Chat.Thinking.Render(shared.IconThinking+" reasoning") + "\n")
    if expanded {
        m.writeItemDetail(&b, "Reasoning", block.Text)
    }
```

Collapsed by default to `▸ ◇ reasoning`. Expanding routes the text through
`writeItemDetail`, which renders it as **indented plain lines with a `│ ` prefix**
— not as markdown, and not italic.

`Theme.Chat.Thinking` (`shared/styles.go:265`) is already
`lipgloss.NewStyle().Italic(true).Foreground(fgDim)` — the right style exists and
is applied only to the *stub*, not to the content.

For the running state, `chatBody` prints a static label
(`monitor_transcript.go:850`):

```go
b.WriteString("  " + shared.Theme.Question.Render("typing…") + "\n")
```

---

## In Scope

- Render thinking blocks as **markdown prose** in the italic muted style, inline,
  at the same column as assistant prose — not as a collapsed stub.
- Keep a collapse affordance for very long reasoning, using the slice 06
  vocabulary and slice 09's threshold mechanism, rather than collapsing by
  default.
- Replace the static `typing…` with an animated pulse: fixed-width frames, eased
  dwell, a persistent text label, and the pulse only while the step is running.
- Add the pulse frames to the icon vocabulary (CC-7) with an ASCII fallback.
- Reuse `Theme.Chat.Thinking` for the prose; add a style for the pulse glyph if
  its color should differ.

## Out of Scope

- The tokens/sec speed badge. jig's transcript does not currently carry per-block
  token deltas, and adding them would touch the harness contract (CC-12). Revisit
  if slice 11 surfaces per-step token counts.
- Thinking-visibility toggles (omp has a `ctrl+T` display mode).
- omp's append-only stable-row publication of thinking prefixes — epic NG1.

## Functional Requirements

- **FR-10.1** A thinking block shall render as markdown prose in an italic muted
  style at the same horizontal offset as assistant prose.
- **FR-10.2** A thinking block shall be visible without expansion.
- **FR-10.3** A thinking block exceeding the collapse threshold shall be
  summarized and expandable, using the slice 06 vocabulary.
- **FR-10.4** While a step is running, the panel shall render an animated pulse
  in place of the static label.
- **FR-10.5** Every pulse frame shall occupy the same number of terminal cells,
  so no content shifts as it animates.
- **FR-10.6** The pulse shall be accompanied by a persistent text label so the
  state is legible without animation.
- **FR-10.7** The pulse shall stop when the step is no longer running.
- **FR-10.8** The pulse shall render an ASCII fallback under the ASCII glyph
  preset.

## Technical and Repository Constraints

- **Animation cost.** jig's monitor coalesces engine events into a **100 ms
  throttled repaint** with dirty flags and step-gated glamour. A 70–230 ms eased
  pulse implies repaints faster than that floor at the quick end of the cycle.
  Either accept a quantized pulse at the existing 100 ms cadence, or coordinate
  with slice 13's shared ticker. **Do not introduce a second independent
  animation loop.**
- Reasoning text goes through glamour. It is prose, so this is consistent with
  the `CLAUDE.md` rule — unlike command output, which must stay verbatim.
- glamour must be given the italic/muted style; `Theme.Chat.Thinking` is a
  lipgloss style and cannot be handed to glamour directly. Either post-style the
  rendered output or configure the renderer's document style. Post-styling
  markdown output risks fighting the ANSI it already contains — prefer a
  configured renderer variant.
- Making thinking visible by default **increases rendered height substantially**.
  Verify `chatItemLineRanges` and scroll-follow behavior on a step with long
  reasoning.
- `defaultExpandEditCodeItems` (`monitor_transcript.go:184`) is the precedent for
  a default-expanded item kind: it records the choice in `chatItemExpand` so a
  later reload does not override an operator's fold. Follow that pattern.

## Security and Data Considerations

Reasoning text is model output and is already redacted and clamped upstream.
Making it visible by default means it appears on screen without an explicit
action — relevant if an operator screen-shares. Not a new data path, but worth
noting in the spec.

## Acceptance Evidence

- A test asserting a thinking block renders its content without expansion.
- A test asserting the rendered thinking output carries the italic escape.
- A test asserting every pulse frame has equal `lipgloss.Width`.
- A test asserting the pulse is absent for a non-running step.
- A test asserting the ASCII preset produces a non-empty single-cell fallback.

## Inputs for the Child Spec

- `Theme.Chat.Thinking` already has the right definition; it is applied to the
  wrong thing.
- The default-expanded pattern already exists in `defaultExpandEditCodeItems` —
  reuse it rather than inventing a new default-state mechanism.
- Coordinate the animation cadence with slice 13 before implementing; two tickers
  would be a defect.
- The speed badge is explicitly deferred — do not partially implement it.

## Open Questions

- **Q-10.1** Should reasoning be visible by default, or default-collapsed with a
  preference? Making it default-visible is the omp behavior and the point of the
  slice, but it substantially lengthens transcripts for reasoning-heavy models.
  *Suggest default-visible with the slice 06 collapse threshold doing the
  bounding; revisit if operators object.*
- **Q-10.2** Can the 100 ms frame loop carry a 70–230 ms eased pulse, or should
  the pulse quantize to 100 ms steps? *Quantizing loses the breathing quality but
  costs nothing. Decide with slice 13.*
- **Q-10.3** Does the transcript distinguish "thinking" from "text" reliably
  across both harnesses (Claude SDK and ACP)? `transcript.BlockThinking` exists,
  but ACP mapping should be verified. *Investigate before the spec.*
