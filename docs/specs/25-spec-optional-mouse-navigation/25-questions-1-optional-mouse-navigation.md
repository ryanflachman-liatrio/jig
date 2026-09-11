# 25 Questions Round 1 - Optional Mouse Navigation

The user accepted the recommended answers by instructing: “Continue using the
recommendations in the questions file.” Decisions recorded on 2026-09-11:
**1B, 2A, 3A, 4A, 5A**. These answers define the B6 specification; no
implementation work has started.

## Context

The requested source is `docs/plans/open-goals.md`, B6: optional mouse support
with click-to-focus and wheel interaction while keeping jig keyboard-primary.

The existing completed `docs/specs/charm-clickable/` slice already enables
Bubble Tea cell-motion mouse events and lets a primary click select a visible
workflow row on Home. It explicitly excludes wheel handling and ignores clicks
on the Runs pane, Monitor, Detail, and other surfaces. This new spec should
extend that behavior without rewriting its historical artifacts.

Repository review found these relevant interaction owners:

- Home has Workflows and Runs focus regions plus a Detail overlay.
- Monitor has Steps and Transcript focus regions, an optional Gate, and modal
  or focused review/help surfaces.
- Runs, Detail, Monitor Steps, Monitor Transcript, chat output, and review
  documents use either Bubbles viewports or owner-managed cursor/scroll state.
- Root already requests `tea.MouseModeCellMotion`. In the pinned Bubble Tea
  v2.0.8 API, that mode delivers click, release, and wheel events; the pinned
  Bubbles v2.1.1 viewport accepts `tea.MouseWheelMsg`.

Current upstream reference points are the living Bubble Tea v2 source and
migration guide:

- <https://github.com/charmbracelet/bubbletea/blob/main/mouse.go>
- <https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md>

## 1. Mouse-enabled surfaces

Which jig surfaces should B6 cover?

- [ ] (A) Home and Monitor core panels only: Workflows, Runs, Steps, and
  Transcript. Preserve the existing workflow-row click behavior; leave Detail,
  Gate inputs, review workspaces, help/chat modals, and standalone chat unchanged.
- [x] (B) Option A plus the read-only Detail overlay viewport.
- [ ] (C) Every root TUI surface with a scroll owner, including Detail, review,
  and help surfaces; text-entry regions still retain their existing input rules.
- [ ] (D) Home only: complete Workflows/Runs mouse support but do not add Monitor
  behavior.
- [ ] (E) Other (describe)

**Recommended answer(s):** [(B)]

**Why these are recommended:**

- `(B)` covers the primary Home-to-Monitor operator journey and the read-only
  Detail viewport while keeping the first slice bounded.
- `(A)` leaves an obvious read-only scrolling surface inconsistent, while `(C)`
  expands into review editors and help/chat overlays with different focus and
  text-capture contracts.
- `(D)` would not address wheel navigation where long run transcripts make it
  most valuable.

## 2. Primary-click behavior inside list panels

After a primary click lands on a valid visible row, what should happen in Runs
and Monitor Steps?

- [x] (A) Focus the clicked panel and select the clicked row, but never activate
  or open it. Existing workflow-row click selection remains the model.
- [ ] (B) Focus the panel only; keep its current row selection unchanged.
- [ ] (C) Focus and activate the clicked row immediately (for example, enter a
  run from Runs or switch the transcript step from Steps).
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` makes the visible pointer target and keyboard selection agree without
  triggering a potentially surprising navigation action.
- It extends the already implemented workflow-row behavior and preserves the
  repository distinction between focus, selection, and activation.
- `(B)` can feel broken because the click lands on a row but a different row
  remains selected; `(C)` makes a single click perform more than B6's stated
  click-to-focus behavior.

## 3. Wheel routing and focus

When the pointer is over a scrollable panel and the wheel moves, which region
should scroll?

- [x] (A) Scroll the eligible panel under the pointer without changing keyboard
  focus or selection. Ignore wheel events outside an eligible panel.
- [ ] (B) Move keyboard focus to the panel under the pointer, then scroll it.
- [ ] (C) Ignore pointer location and scroll only the currently keyboard-focused
  region.
- [ ] (D) Other (describe)

**Current best-practice context:** Bubble Tea v2 exposes wheel coordinates and
the Bubbles viewport already implements bounded wheel movement. Routing by the
rendered panel under the pointer can reuse owner-local viewport behavior while
keeping mouse activity from silently moving keyboard focus.

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` matches common pointer-wheel expectations and preserves keyboard focus,
  so a later keypress still goes to the region the operator explicitly focused.
- `(B)` couples scrolling to a focus change that may be hard to notice, while
  `(C)` makes pointer placement irrelevant and can scroll a different panel
  than the one under the pointer.

## 4. Meaning of “optional”

How should operators opt into mouse use?

- [x] (A) Mouse is an optional input modality, not a setting: keep mouse event
  delivery enabled as it is today, preserve every keyboard path, and require no
  configuration.
- [ ] (B) Add an application-level setting to enable or disable mouse support.
- [ ] (C) Add workflow TOML configuration for mouse support.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` matches the existing root behavior and B6's “not mouse-first” wording:
  operators may ignore the mouse and retain complete keyboard control.
- `(B)` adds configuration, persistence, and discoverability work that the goal
  does not request. `(C)` would put a terminal preference into workflow
  orchestration, contrary to repository ownership boundaries.

## 5. Wheel step and selection coupling

For lists such as Runs and Monitor Steps, should wheel movement also move the
selected row, or only scroll the visible window?

- [x] (A) Keep the selected row visible and move selection by a small,
  deterministic number of rows per wheel event, matching list-style navigation.
- [ ] (B) Scroll the viewport independently while preserving the selected row,
  even when selection moves off-screen.
- [ ] (C) Do not support wheel on list panels; support it only on content
  viewports such as Transcript and Detail.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` preserves the current invariant that selected list rows remain visible
  and avoids introducing a second, independent list viewport position.
- `(B)` weakens selection visibility and would require new rules for activation
  of an off-screen row. `(C)` is simpler but only partially fulfills wheel
  navigation across the core panels proposed in Question 1.

## Decision reconciliation

Question 5 is the list-specific exception to Question 3's “without changing
selection” phrase: wheel input preserves **keyboard focus** everywhere, but
moves the existing selection on Workflows, Runs, and Monitor Steps. Transcript
and Detail wheel input scrolls content without moving their selection/cursor.

Question 2's “never activate or open” excludes screen navigation and execution,
not the existing selection-driven preview: selecting a Monitor row still
refreshes its Transcript/file preview, and selecting a workflow still updates
the Runs filter. No second selection state is introduced to suppress these
existing behaviors.

Implementation-level choices in the spec use three logical list rows or three
content lines per vertical wheel event, clamped at the ends; accept only plain
primary clicks and vertical wheel events; and prevent mouse routing behind
modal/text-capture surfaces. These fill in behavior not fixed by the options.

Resulting specification: [25-spec-optional-mouse-navigation.md](25-spec-optional-mouse-navigation.md).
