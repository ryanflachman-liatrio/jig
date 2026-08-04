# 01-spec-tui-bordered-screens.md

> **Status:** grilled and settled. The Open Questions in the original draft have
> been resolved into the Resolved Decisions section below. Supporting docs:
> [`CONTEXT.md`](../../../CONTEXT.md) (glossary: Screen, Panel, Focus, Gate),
> [`docs/adr/0001-manual-border-title-compositing.md`](../../adr/0001-manual-border-title-compositing.md),
> [`docs/adr/0002-gates-are-nonblocking-focus-regions.md`](../../adr/0002-gates-are-nonblocking-focus-regions.md).

## Introduction/Overview

jig's TUI screens currently render as plain full-width content with a dim help
line joined underneath — there is no visual frame around a screen and no
consistent, always-visible title telling the user where they are. This feature
wraps every top-level screen in a **rounded border with its title embedded in
the top border edge**, in the style of lazygit's panels. The run monitor — today
a single view that toggles between a step list and a per-step transcript — becomes
a **two-panel, side-by-side layout** (step list on the left, transcript/detail on
the right) where the focused panel's border is highlighted. The standalone
streaming chat client is brought into the same visual language.

The primary goal is a more legible, spatially-oriented TUI: the user always sees
a titled frame around content, and in the monitor can see the step list and the
selected step's transcript at the same time, with a clear indication of which
region has keyboard focus. A secondary win: human-in-the-loop **gates** (review,
input, questions) stop freezing navigation — the reviewer can read the transcript
a verdict is *about* while the gate waits.

## Goals

- Wrap all four workflow screens (selector, detail, runs, monitor) — and the
  standalone chat client — in a rounded border whose title is rendered inline in
  the top border edge.
- Convert the run monitor from a mode-switching single view into two
  simultaneously-visible, side-by-side titled panels (step list + transcript).
- Make human-in-the-loop gates non-blocking, focusable regions rather than modal
  states that freeze navigation.
- Indicate keyboard focus by border color: the focused region uses the primary
  (Charple) border; blurred regions use the dim (Iron) border — reusing the
  existing `theme.Viewport.Focused` / `.Blurred` convention.
- Keep the footer/keybinding hints as a single plain line directly below the
  box(es), mirroring lazygit's global bottom keybind bar.
- Introduce exactly one reusable, tested, **pure-presentation** "titled panel"
  rendering helper so the frame is defined once and applied consistently, with all
  styling flowing from the existing theme tokens in `internal/tui/styles.go`.

## Domain Vocabulary

Defined in [`CONTEXT.md`](../../../CONTEXT.md); summarized here because the
requirements use these terms precisely:

- **Screen** — one top-level view in the workflow flow (Selector, Detail, Runs,
  Monitor). The chat client is a separate root model, not a screen, but is paneled
  the same way.
- **Panel** — a rounded border with a title composited into its top edge, wrapping
  a body. A *pure-presentation* primitive: it never owns viewports, wrapping, or
  content sizing; the caller fits the body to the panel's inner area.
- **Focus** (of a region) — the property of holding keyboard input. The focused
  region's border is Charple; blurred regions are Iron. On a single-panel screen
  the sole panel always holds focus.
- **Gate** — a human-in-the-loop prompt (review verdict, `block_on` input,
  AskUserQuestion, or `from="user"` prompt) rendered as a full-width strip beneath
  the panels. A pending gate does not freeze navigation.

## User Stories

- **As a jig operator driving a workflow**, I want each screen surrounded by a
  titled box so that I always know which screen I'm on without reading the help
  line.
- **As a reviewer watching a run**, I want the step list and the selected step's
  transcript visible at the same time so that I can follow agent output while
  keeping the run's overall progress in view.
- **As a reviewer answering a gate**, I want to move focus away from the gate to
  read the transcript it concerns and then return to answer, rather than being
  frozen the moment the gate appears.
- **As a keyboard-only user**, I want the focused region's border highlighted so
  that I know which region my keys (scroll, select, expand) will act on.
- **As a maintainer of the TUI**, I want a single reusable titled-panel helper
  driven by the theme so that the border style, title placement, and focus
  coloring stay consistent and are changed in one place.

## Resolved Decisions

These were the Open Questions and design forks; they are now settled and the
Functional Requirements below encode them.

1. **Panel helper is pure presentation.** Signature shape:
   `panel(title string, body string, width, height int, focused bool) string`.
   It draws the border, composites the title, and paints the focus color. The
   *caller* renders `body` pre-fit to the inner area, doing the frame-size math
   with `GetHorizontalFrameSize` / `GetVerticalFrameSize`. The helper never touches
   viewports, glamour, or content sizing.
2. **Title compositing: build the top edge by hand.** The panel style omits the
   top border; the helper prepends a hand-built top line
   (`corner + ─ + styled title + fill dashes + corner`), width-matched with
   `lipgloss.Width`. Chosen over splicing the title into a fully-rendered box's
   first line (fragile ANSI string-surgery). See ADR 0001.
3. **Over-long titles truncate with `…`** so total panel width stays stable.
4. **Single-panel screens always use the primary/focused border.** The sole panel
   always holds focus; the dim Iron border therefore means exactly one thing
   app-wide — "a panel that exists but does not currently hold your keys" — which
   only occurs in the monitor's two-panel layout.
5. **Selector strips its bubbles-list chrome.** `SetShowTitle(false)`,
   `SetShowHelp(false)` (status bar already off); the panel supplies the title and
   an externally-rendered footer supplies the hints (may branch on
   `list.FilterState()`).
6. **Runs gains a viewport.** It currently has none; it gets a `viewport.Model`
   sized to the panel's inner area so a long run list scrolls inside the frame.
7. **Monitor state is `focus ∈ {Steps, Transcript, Gate}`**, replacing the
   `mode` (`modeList`/`modeChat`) toggle. Both panels are always visible.
8. **Gates are non-blocking focus regions** rendered as a full-width strip beneath
   the two panels; they do not freeze navigation. A gate auto-focuses on arrival
   (so answering is a zero-navigation default) but the user can focus away and
   back. See ADR 0002.
9. **Keymap:** `tab` / `shift+tab` cycle focus across present regions
   (Steps → Transcript → Gate); `left` / `right` alias for moving between the two
   side-by-side panels. The chat block cursor moves off `tab` to **`n` / `N`**;
   `enter`/`space` still toggle the cursored block, `o` still toggles all.
10. **Eager transcript reload.** Moving the Steps selection re-points and reloads
    the Transcript panel on every selection change (not only on an explicit open).
    Per-step view state (`chatBlockCursor`, `chatExpandAll`, `chatExpand`) resets
    on each change.
11. **Monitor split:** left Steps panel width = `max(32, width/3)`, clamped so the
    Transcript keeps a minimum inner width (~40); Transcript takes the remainder.
    Frame size is subtracted from each for inner content width.
12. **Default monitor focus is Steps** (a pending gate overrides by auto-focusing
    itself).
13. **Right-panel title is the selected step's id**, falling back to
    "Transcript" when nothing is selected.
14. **Narrow-terminal fallback (< ~76 cols).** When the two panels can't both meet
    their minimum widths (`32 + 40 + border cells`), fall back to showing only the
    *focused* panel full-width; the focus key toggles which one is visible. The
    gate strip still renders beneath.
15. **Chat client is in scope** (supersedes the original Non-Goal #6): full
    restructure into a Conversation panel + Message panel with header/turn/status
    chrome folded into titles. See Unit 3.

## Demoable Units of Work

### Unit 1: Reusable titled-panel helper and simple single-box screens

**Purpose:** Establish the one shared, pure-presentation "rounded box with an
inline top-border title" primitive and apply it to the three single-view screens
(selector, detail, runs). This proves the frame, the title compositing, and the
layout math before the monitor's more complex two-panel work builds on it.

**Functional Requirements:**
- The system shall provide a single reusable panel-rendering helper
  `panel(title, body string, width, height int, focused bool) string` in a new
  `internal/tui/panel.go` that renders a body string inside a rounded border with
  a title composited into the top border edge. The helper is **pure presentation**:
  it does not create or size viewports and does not wrap content.
- The panel helper shall composite the title by **omitting the top border and
  building the top edge by hand** (corner + `─` + styled title + fill dashes +
  corner), width-matched with `lipgloss.Width`, keeping total
  `lipgloss.Width`/`Height` equal to the requested outer dimensions.
- The panel helper shall **truncate an over-long title with `…`** so the panel's
  total width never exceeds the requested width.
- The panel helper shall place the title left-aligned, offset one cell in from the
  top-left corner, with one space of padding either side of the text
  (`╭─ Title ─────╮`).
- The panel helper shall accept a focus flag and render the focused state with the
  primary (Charple) border color and the blurred state with the dim (Iron) border
  color, deriving both from existing theme tokens.
- The panel helper shall expose (or callers shall compute via lipgloss frame
  helpers) the horizontal/vertical frame size so callers can size their body to
  `width - hFrame` × `height - vFrame` rather than magic numbers.
- The system shall render **single-panel screens (selector, detail, runs) always
  with the primary/focused border** — the sole panel always holds focus.
- The system shall render the **selector** inside a titled panel titled
  "Workflows", after disabling the bubbles-list internal title, help line, and
  status bar; the selector renders its own footer line below the panel (it may
  branch the hint text on `list.FilterState()`).
- The system shall render the **detail** screen inside a titled panel titled with
  the workflow name (falling back to the file path when the name is empty).
- The system shall render the **runs** screen inside a titled panel titled "Runs",
  building its rows into a `viewport.Model` sized to the panel's inner area so a
  long run list scrolls within the frame; runs gains the scroll-key plumbing and
  `resize()` path this requires.
- The system shall keep the footer/keybinding hints as a single plain (unboxed)
  line rendered directly below the panel on every screen.
- The system shall re-fit every panel and its inner content on `WindowSizeMsg`
  so no content wraps to a stale width or overflows the border.
- All new lipgloss styles (border/title/focus styles) shall live in
  `internal/tui/styles.go` and derive from the existing color tokens; no bare
  package-level style vars shall be added and no hex shall be hardcoded at a call
  site.

**Proof Artifacts:**
- Screenshot: selector screen shows a rounded box titled "Workflows" with the
  workflow list inside and no list-internal title/help — demonstrates the titled
  frame renders on a bubbles-list screen with its chrome stripped.
- Screenshot: runs screen shows a rounded box titled "Runs" with a scrolling run
  list inside and the footer hint line below the box.
- Test: a table-driven test in `internal/tui` asserts the panel helper's rendered
  output contains the rounded corner runes and the title text on the top edge, that
  an over-long title is truncated with `…`, and that `lipgloss.Width`/`Height` of
  the result match the requested outer dimensions.
- CLI: `go test ./internal/tui` passes — demonstrates no regression in existing
  TUI tests.

### Unit 2: Two-panel run monitor with focus-aware borders and non-blocking gates

**Purpose:** Re-lay out the run monitor as two side-by-side titled panels (Steps +
Transcript) shown simultaneously, replacing the current `modeList`↔`modeChat`
toggle, with the focused region's border highlighted, focus switchable by keyboard,
and human-in-the-loop gates rendered as non-blocking strips beneath the panels.

**Functional Requirements:**
- The system shall render the monitor as two horizontally-joined
  (`lipgloss.JoinHorizontal`) titled panels: a left **"Steps"** panel containing
  the step list and a right panel titled with the **selected step's id** (falling
  back to "Transcript") containing that step's transcript/chat chain.
- The system shall show both panels at the same time; the `mode` field and the
  `modeList`/`modeChat` toggle are removed.
- The system shall replace `mode` with a **`focus`** field taking the values
  Steps, Transcript, or Gate, and render only the focused region's border in the
  primary color, with the other regions' borders dimmed.
- The system shall size the panels as: left width `= max(32, width/3)`, clamped so
  the right panel keeps an inner width of at least ~40; right width is the
  remainder. Each panel's inner content width/height is its outer size minus its
  border frame size, computed with lipgloss frame helpers, re-fit on
  `WindowSizeMsg`.
- The system shall recompute the transcript renderer's glamour word-wrap width from
  the **Transcript panel's inner width** (not the full terminal width) and
  invalidate the per-`blockKey` render cache on width change (the existing
  `rebuildRenderer` rule).
- The system shall remove the internal title/header rows currently rendered by
  `listBody()` and `chatBody()`, since the panel border now carries the title.
- The system shall route navigation keys by focused region: with Steps focused,
  `j`/`k` move the selection cursor; with Transcript focused, `j`/`k` (and
  `ctrl+d`/`ctrl+u`/`pgup`/`pgdn`) scroll the transcript viewport, `n`/`N` move the
  block cursor, and `enter`/`space` toggle the cursored block, `o` toggles all.
- The system shall move keyboard focus with `tab`/`shift+tab` (cycling Steps →
  Transcript → Gate across the regions currently present) and shall accept
  `left`/`right` as an alias for moving between the two side-by-side panels; the
  footer hint shall reflect the focus-switch keys.
- The system shall **reload the selected step's transcript eagerly on every
  selection change**, resetting per-step view state
  (`chatBlockCursor`/`chatExpandAll`/`chatExpand`).
- The system shall render each pending human-in-the-loop gate (review verdict
  picker, `block_on` agent-input textarea, AskUserQuestion option list, and
  `from="user"` prompt) as a **full-width strip beneath the two panels**, above the
  footer.
- The system shall treat a gate as a **non-blocking focus region**: it does not
  freeze navigation; the user can `tab`/`left`/`right` between Steps, Transcript,
  and the Gate. A gate **auto-focuses on arrival**. Keys dispatch to the focused
  region first — a focused gate/textarea consumes them; only Steps-focus reads
  `j`/`k` as select, only Transcript-focus reads them as scroll.
- The system shall retain the existing gate-resolution semantics (verdict digits,
  multi-select toggles + confirm, `m` message compose, textarea submit on `enter`,
  cancellation delivering the appropriate response so no reporter goroutine hangs).
- The system shall provide a **narrow-terminal fallback**: below the width where
  both panels can meet their minimums (~76 cols), render only the focused panel
  full-width, with the focus key toggling which panel is shown; the gate strip
  still renders beneath.
- The system shall keep the monitor's footer/status hint line as a single plain
  line below the panels (and below the gate strip when present).
- The persistence-off path (`runDir == ""`) shall remain unaffected: no new disk
  dependency, transcript-unavailable states render as today.

**Proof Artifacts:**
- Screenshot: monitor showing both the "Steps" panel and the transcript panel side
  by side, with the focused panel's border visibly highlighted.
- Screenshot: monitor with a review gate active as a strip beneath the panels,
  with focus on the transcript panel (demonstrating the gate no longer freezes
  navigation).
- Screenshot: monitor at a narrow width showing the single-panel fallback with an
  intact border.
- Test: a table-driven test in `internal/tui` constructs a `monitorModel` with a
  few steps, renders it, and asserts the output contains both panel titles and that
  a focus-switch key (`tab`) toggles which region's border style is primary.
- Test: a table-driven test asserts that with a gate pending, a focus-switch key
  still moves focus (navigation is not frozen) and that gate keys resolve the gate.
- Test: a table-driven test asserts eager transcript reload — moving the Steps
  cursor changes the rendered transcript panel body to the newly selected step.
- CLI: `go test ./...` and `go vet ./...` pass; `gofmt -l .` reports no files.

### Unit 3: Paneled streaming chat client

**Purpose:** Bring the standalone streaming chat client (`chatModel`, reached via
`go run ./cmd/jig`) in line with the paneled screens. This is a restructure, not a
wrap: the chat already has a `focusInput`/`focusOutput` toggle and bordered regions,
so it adopts the titled-panel helper and folds its loose header/turn/status chrome
into panel titles.

**Functional Requirements:**
- The system shall render the chat's output/conversation region as a titled panel
  ("Conversation") and its input region as a titled panel ("Message"), using the
  panel helper and focus-border convention, reusing the existing
  `focusInput`/`focusOutput` toggle for which panel's border is primary.
- The Conversation panel title shall carry turn navigation info (e.g.
  "Conversation · Turn 2 of 3") when more than one turn exists.
- The Conversation panel title shall show connection state ("connecting…") only
  until connected, then drop it; it shall never show a persistent "Connected".
- A fatal connection/stream error shall render as its own full-width line beneath
  the panels (never inside a truncatable title) — the chat's analogue of a Gate.
- The streaming indicator shall render as "Message · responding…" on the input
  panel's title while a turn streams, and clear when idle; the animated spinner
  glyph shall not be embedded in the border edge.
- The input panel shall own its frame; the textarea's own border (from
  `newInputTextarea`) is removed to avoid a double box.
- The separate `headerView`, `turnIndicatorView`, and `statusLineView` lines shall
  be removed, their information folded into the panel titles / fatal-error line as
  above.
- All new styling shall flow from existing theme tokens; the chat continues to
  return `tea.View` (it is a standalone root model, not a sub-model).

**Proof Artifacts:**
- Screenshot: the chat client showing a "Conversation" panel (with turn info in the
  title) above a "Message" panel, with the focused panel's border highlighted.
- Screenshot: the chat mid-stream, with "Message · responding…" in the input panel
  title.
- Test: a table-driven test in `internal/tui` renders `chatModel` and asserts both
  panel titles appear and that toggling focus moves the highlighted border.
- CLI: `go test ./...` and `go vet ./...` pass.

## Non-Goals (Out of Scope)

1. **No light/alternate theme**: the TUI remains dark-only (Charmtone Pantera);
   this feature does not add theme switching or terminal-background detection.
2. **No mouse support / draggable or resizable panels**: the split ratio is
   fixed (width-derived), not user-adjustable at runtime by dragging.
3. **No changes to engine, runner, workflow, or transcript logic**: this is a
   presentation-layer change only; step execution, event flow, and the
   file-is-truth transcript model are untouched.
4. **No new panels beyond the monitor's two** (plus the gate strip): the selector,
   detail, and runs screens get a single box each.
5. **No scrollbars, tab bars, or status-bar redesign** beyond keeping the
   existing footer hint line below the boxes.
6. *(Superseded — the chat client is now in scope; see Unit 3.)*

## Design Considerations

- **Border:** rounded corners (`lipgloss.RoundedBorder()`), matching the existing
  `theme.Viewport` styles.
- **Title placement:** left-aligned in the top border edge, one cell offset from
  the top-left corner, one space of padding around the text
  (e.g. `╭─ Workflows ─────╮`) — the lazygit convention.
- **Focus coloring:** focused border uses the primary token (Charple `#6B50FF`);
  blurred border uses Iron `#4D4C57` — the same pair already defined on
  `theme.Viewport.Focused` / `.Blurred`. Single-panel screens always render the
  focused color.
- **Footer:** a single dim plain line (`theme.Footer`) directly below the box(es)
  (and below the gate strip in the monitor), not enclosed in a border.
- **Gate strip:** full width, beneath the two monitor panels, above the footer;
  auto-focused on arrival; border reflects focus like any other region.
- **Full-screen background:** the Charmtone Pepper canvas continues to be painted
  edge-to-edge by the compositor via `rootModel.View`'s `tea.View`; panels sit on
  top of it.
- **Monitor split:** left "Steps" panel `= max(32, width/3)`, right transcript
  panel the remainder (min inner ~40), both filling the full terminal width; single
  focused panel below ~76 cols.

## Repository Standards

- **All lipgloss styles live in `internal/tui/styles.go`**, organized into the
  `Styles` struct and built in `DefaultTheme()` from the ~7 semantic color tokens.
  Any new style (panel border/title, focus pair) is added as a field and set in
  `DefaultTheme()`; no bare `var xStyle = lipgloss.NewStyle()` at package scope, and
  no hardcoded hex on a style outside the token set.
- **Layout math uses lipgloss frame helpers**, never magic numbers (per the
  existing `GetVerticalFrameSize()` house rule).
- **Comments explain the non-obvious "why"** (e.g. why the title is composited
  manually into the border — cross-reference ADR 0001), matching the house style.
- **Table-driven tests** in `internal/tui/*_test.go`, constructing keys as
  `tea.KeyPressMsg{Code:…, Text:…}` and asserting on rendered strings — following
  `monitor_test.go`, `root_test.go`, and `selector_test.go`.
- **v2 idioms**: sub-models return `string`; only `rootModel` (and the standalone
  `chatModel`) return `tea.View`. Key handling switches on `tea.KeyPressMsg`.
- **Formatting/vetting**: `gofmt -l -w .` and `go vet ./...` are clean before
  completion.

## Technical Considerations

- **lipgloss v2.0.5 has no native border-title API** (`Border` exposes only
  edge/corner runes; the top edge is drawn by an internal `renderHorizontalEdge`
  with no label hook). The chosen approach — **omit the top border and build the
  titled top edge by hand**, width-matched with `lipgloss.Width`, truncating
  over-long titles with `…` — keeps total `Width`/`Height` stable. This is the one
  place to replace if a future lipgloss adds a border-title API. See ADR 0001.
- **Reuse existing focus tokens:** the panel helper builds on the same
  Charple/Iron tokens behind `theme.Viewport.Focused` / `.Blurred` rather than
  introducing a parallel color source.
- **Monitor re-layout:** two panels joined with `lipgloss.JoinHorizontal`; the
  existing dual viewports (`m.vp` list, `m.chatVP` chat) become the two panels'
  content viewports; `mode` is replaced by `focus`. The `blockKey` render/expand
  caches and the `rebuildRenderer` width-invalidation rule still apply, driven off
  the transcript panel's inner width.
- **Gate routing:** the gate's pending pointers (`pendingReview`, `pendingInput`,
  `pendingQuestion`, `pendingPrompt`) are tracked independently of `focus`; a gate
  auto-sets `focus = Gate` on arrival but does not force navigation state. Key
  dispatch is per-focused-region. See ADR 0002.
- **glamour word-wrap:** because the transcript occupies only part of the width,
  the renderer's wrap width and the per-block render cache are rebuilt/invalidated
  on width change using the transcript panel's inner width.
- **Only prose goes through glamour**; verbatim (system/command/tool) content keeps
  its existing verbatim path — unchanged by this feature.
- **Narrow-terminal fallback** re-uses the pre-existing single-viewport render path,
  gated on total width; it is a degradation of the two-panel layout, not a return of
  the `mode` toggle as a first-class state.
- **Persistence-off path** (`runDir == ""`) is unaffected; this is a render-only
  change and must not add any disk dependency.
- **Chat restructure:** `chatModel` already borders its output viewport via
  `theme.Viewport` and its input via `newInputTextarea`, and already toggles the
  border color on `focusInput`/`focusOutput`. Unit 3 retrofits titles onto those
  borders (input panel owns the frame, textarea border removed) and folds
  `headerView`/`turnIndicatorView`/`statusLineView` into titles + a fatal-error line.

## Security Considerations

No specific security considerations identified. This is a local, presentation-only
TUI change: no credentials, network calls, external services, or persisted data are
introduced, and no new files are committed as proof beyond screenshots and test
output (which contain no sensitive data).

## Success Metrics

1. **Consistent framing**: all four workflow screens (selector, detail, runs,
   monitor) and the chat client render inside a rounded border with the correct
   title in the top edge — verifiable by screenshot for each.
2. **Two-panel monitor**: the monitor shows the step list and the selected step's
   transcript simultaneously, with the focused region's border highlighted and
   focus switchable by keyboard — verifiable by screenshot and a focus-switch test.
3. **Non-blocking gates**: with a gate pending, the user can still move focus and
   read the transcript — verifiable by a test that switches focus while a gate is
   pending.
4. **No regressions**: `go test ./...` and `go vet ./...` pass, and `gofmt -l .`
   reports no files.
5. **Responsive layout**: resizing the terminal re-fits every panel with no content
   overflow, stale-width wrapping, or broken borders, including the narrow-terminal
   single-panel fallback — verifiable by resizing during a screenshot session.
6. **Single source of style**: the border/title/focus styling is defined once
   (theme tokens + one panel helper) with no duplicated or hardcoded border colors
   at call sites.
