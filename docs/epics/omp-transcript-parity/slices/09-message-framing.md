# Slice 09 — Message framing: bubbles, not labels

- **Slice ID:** `message-framing`
- **Outcome:** Operator-authored input renders as a background-tinted bubble
  rather than a literal `User` label; large synthetic inputs collapse to a single
  dim summary row and build their markdown only when expanded.
- **Why this slice exists:** The `User` label is the last piece of explicit
  role chrome in the panel, and the lazy-build behavior for large injected
  context is a real performance fix, not just a cosmetic one.
- **Depends on:** Slice 04.

---

## omp reference

### No prefixes, no labels, no timestamps

This is the finding that most contradicts intuition. omp draws **no role prefix,
no gutter rail, no role label, and no per-message timestamp**. Both roles render
as flush-left markdown at a 1-column inset. The only difference is fill.

**User** (`packages/coding-agent/src/modes/components/user-message.ts:78-83`):

```ts
const bgColor = (value: string) => theme.bg("userMessageBg", value);
const md = new Markdown(text, 1, 1, getMarkdownTheme(), { bgColor, color });
md.setIgnoreTight(true);
```

`paddingX = 1`, `paddingY = 1`. The `paddingY` rows are emitted as
`padding(width)` run through the background function — i.e. **tinted** blank rows
spanning the full terminal width. Content lines are
`applyBackgroundToLine(leftMargin + line + rightMargin, width, bgFn)`, so the
tint extends to the right edge of the terminal, not to the text width.

**Assistant** (`assistant-message.ts:1042`):

```ts
const md = new Markdown(trimmed, 1, 0, this.#getProseTheme(), mdOptions, 0);
```

Same 1-column inset, `paddingY = 0`, no background.

Rendered at width 60 (`·` marks tint):

```
‹                                                            ← tinted blank row
‹ add a health check to the server endpoint                  ← tint to col 59
‹                                                            ← tinted blank row
‹                                                            ← plain separator
‹ I'll check the server entrypoint first.                    ← assistant, no tint
```

Both roles begin at **column 1**. Column alignment is identical; only the fill
distinguishes them. This is why slice 04's ANSI-aware blank test matters — the
tinted padding rows must survive edge trimming while the plain separator does
not.

Theme values: `userMessageBg` = `#221d1a` (dark) / `#e8e8e8` (light);
`userMessageText` = `""` (inherit terminal default).

### The collapsed synthetic message

`user-message.ts:158-211`. Agent-attributed and developer inputs — omp's example
is the advisor's `Session update` replay dumps — render **one dim row** and build
**no markdown at all** until expanded:

```
 Session update · 412.3 KB · 8134 lines · ctrl+o
```

The in-source rationale (`:120-131`) is explicitly a performance fix:

> *"…which can each be hundreds of KiB of Markdown and, on cold open, blocked the
> TUI for tens of seconds while every historical body was laid out before the
> viewport clip (issue #6308). Collapsed by default: renders one dim summary row
> … and builds NO Markdown. The heavy `UserMessageComponent` is constructed
> lazily only when expanded via `ctrl+o`, so blocks above the viewport never pay
> layout cost until the reader asks to see them."*

Summary format (`:196-211`): `<label> · <size> · <n> line(s)`, where `label` is
the first markdown heading's text, else `Synthetic input`. Truncated with `…` to
`width - 1`.

### Reaction badge

`user-message.ts:92-96`. If the following assistant reply opens with an emoji,
that emoji is lifted out of the reply and drawn **right-aligned into the bubble's
top tinted padding row**. Derived, never stored. Charming, and out of scope here.

---

## Current jig state

`internal/tui/monitor/monitor_transcript_items_view.go:45-52`:

```go
case transcriptItemText:
    if item.role == transcript.RoleUser {
        b.WriteString(prefix + shared.Theme.Chat.UserGuidance.Render("User") + "\n" + m.renderMarkdown(item.primary.key, block.Text))
    } else {
        b.WriteString(prefix + m.renderMarkdown(item.primary.key, block.Text))
    }
case transcriptItemSystem:
    b.WriteString(prefix)
    writeVerbatim(&b, block.Text)
```

- A literal `"User"` label, styled `UserGuidance` (`fgMuted`, `PaddingLeft(1)`).
- No tint, no bubble.
- Assistant and user markdown are otherwise identical.
- `transcriptItemSystem` (command output, role `system` or `result`) goes to
  `writeVerbatim` — correct, and consistent with the `CLAUDE.md` rule that only
  prose goes through glamour.

There is **no lazy-build path**. Every text item's markdown is rendered through
glamour on first paint, cached in `chatItemRendered`. For a step whose input
includes a large injected context document, that cost is paid on the first
render of the page regardless of whether the item is on screen.

---

## In Scope

- Replace the `"User"` label with a background tint spanning the panel's content
  width, plus one tinted blank row above and below.
- Add `Theme.Chat.UserBubble` (background) and a matching foreground to `Styles`,
  and a `hexBubble` token to `palette.go` — a value slightly lighter than
  `hexPepper`, e.g. `hexBBQ` (`#2D2C36`) which already exists.
- Keep assistant prose exactly as it is: 1-column inset, no tint.
- Add a collapsed form for large text items: when a text block exceeds a
  threshold, render one dim summary row (`<label> · <size> · <n> lines`) and
  **skip the glamour render entirely** until expanded.
- Derive the label from the block's first markdown heading, falling back to a
  generic label.

## Out of Scope

- The reaction badge.
- OSC 133 prompt zones — epic NG2.
- `transcriptItemSystem` rendering, which is already correct.
- Role-based timestamps. jig previously rendered `#seq role … ts` in the
  dead `renderEntryHeader` path (removed by slice 00); omp has no such thing and
  this slice does not reintroduce it.

## Functional Requirements

- **FR-09.1** An operator-authored text item shall render with a background tint
  and no role label.
- **FR-09.2** The tint shall span the panel's content width, and shall include
  one tinted blank row above and below the content.
- **FR-09.3** Assistant prose shall render with no tint, at the same horizontal
  offset as operator input.
- **FR-09.4** A text item whose content exceeds the collapse threshold shall
  render one summary row stating a label, a byte size, and a line count.
- **FR-09.5** A collapsed text item shall not invoke the markdown renderer.
- **FR-09.6** Expanding a collapsed text item shall render its full markdown.
- **FR-09.7** The summary label shall be the block's first markdown heading when
  present, and a generic label otherwise.
- **FR-09.8** The summary row shall be truncated to the available width with an
  ellipsis.
- **FR-09.9** A tinted padding row shall survive slice 04's edge trimming.

## Technical and Repository Constraints

- **FR-09.9 is the coupling to slice 04.** If slice 04 resolves Q-04.1 toward
  "the separator owns all vertical space," the tinted padding rows go away and
  this slice's FR-09.2 changes shape. Sequence accordingly.
- lipgloss backgrounds against glamour output hit CC-9 — the same hazard slice 01
  resolves. Reuse whatever slice 01 concluded; do not solve it twice.
- `renderMarkdown` caches by `blockKey` in `chatItemRendered`
  (`monitor_transcript.go:1037`). The lazy path must not populate that cache with
  a summary string, or expanding would show the summary.
- The collapse threshold should be expressed in bytes or rendered lines, not
  characters, and should be a named constant beside the existing budgets in
  `monitor_model.go`.
- Per `CLAUDE.md`, only prose goes through glamour — do not change the
  `transcriptItemSystem` verbatim path.

## Security and Data Considerations

- Operator input can contain secrets the operator typed. It is already subject to
  the same redaction as the rest of the transcript.
- A full-width background makes any trailing content on the line visible as
  tinted space; confirm no trailing whitespace leaks content width information
  that was previously hidden. Cosmetic, not a real exposure.

## Acceptance Evidence

- A test asserting a user text item renders no literal `User` and carries a
  background escape on its content and padding rows.
- A test asserting assistant and user text begin at the same column.
- A test asserting a large text block renders a summary row and that the markdown
  renderer was **not** invoked (assert via a counting fake renderer).
- A test asserting expansion renders the full body.
- A test asserting the summary label comes from the first heading.

## Inputs for the Child Spec

- The lazy-build behavior (FR-09.5) is the substantive win; the bubble is
  cosmetic. If the slice has to be cut, keep the lazy build.
- omp's issue #6308 rationale is the evidence that this matters at scale.
- CC-9's resolution from slice 01 is a prerequisite for the tint.
- `hexBBQ` already exists in `palette.go` and is probably the right value — check
  before adding a token.

## Open Questions

- **Q-09.1** What is the collapse threshold? omp does not use a size threshold at
  all — it collapses by *provenance* (synthetic/agent-attributed inputs always
  collapse, real user prompts never do). jig's transcript has
  `transcript.RoleUser` but may not distinguish operator-typed from
  engine-injected. *If that distinction exists in the transcript, prefer it over
  a size threshold — it is more predictable.* **Needs investigation of the
  transcript writer before the spec is written.**
- **Q-09.2** Does the bubble read well when the panel is narrow and the message
  is one short line? A full-width tint for `yes` may look odd. *Prototype.*
- **Q-09.3** Should system/command output also get a distinguishing fill? omp
  tints custom messages (`customMessageBg`). *Suggest not in this slice — the
  verbatim path is already visually distinct.*
