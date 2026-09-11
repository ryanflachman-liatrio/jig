# 25-spec-optional-mouse-navigation.md

## Introduction/Overview

B6 in [open goals](../../plans/open-goals.md) adds optional click-to-focus and
wheel navigation to jig's keyboard-primary TUI. Extend existing workflow-row
click selection to Home's Runs list and Monitor's core panels, and enable
vertical wheel navigation in those panels and the read-only Detail overlay.
Keep current keyboard paths and require no mouse setting or workflow change.

The accepted [questions](25-questions-1-optional-mouse-navigation.md) are
**1B, 2A, 3A, 4A, 5A**. This specification extends the completed
[charm-clickable slice](../charm-clickable/charm-clickable.md); its historical
scope remains unchanged. This document specifies future behavior, not a claim
that the feature has been implemented.

## Goals

1. Allow primary clicks to focus and select visible rows in Workflows, Runs,
   and Monitor Steps, and to focus Monitor Transcript without activating actions.
2. Route vertical wheel events to the eligible visible panel under the pointer,
   preserving keyboard focus and moving by a bounded, deterministic increment.
3. Preserve list selection visibility and existing selection-driven previews,
   while allowing Transcript/Detail content to scroll independently of cursors.
4. Prevent mouse events from reaching hidden panels, overlays' underlying
   content, text capture, borders, or noninteractive chrome.
5. Prove geometry, focus, selection, and keyboard parity through model tests
   and a reproducible terminal smoke at narrow and wide sizes.

## User Stories

- As an operator on Home, I want to select a workflow or run by clicking its
  row, then use the existing keys to inspect or open it.
- As an operator in Monitor, I want to select a step and see its existing
  transcript preview without accidentally stopping, resuming, or resetting it.
- As an operator reading output, I want the wheel to scroll the panel under
  the pointer without moving the focus that determines my next keypress.
- As an operator using only the keyboard, I want navigation, filtering, review,
  help, and run controls to continue working without configuration.

## Demoable Units of Work

### Unit 1: Home click and list-wheel navigation

**Purpose:** Complete mouse navigation across Home's visible Workflows and Runs
panels using their existing selection and filtering state.

**Functional Requirements:**

- **FR-01:** The system shall retain `tea.MouseModeCellMotion` on the root view
  without adding configuration. Every supported mouse behavior shall retain a
  keyboard path; mouse use shall remain optional as an input modality.
- **FR-02:** The system shall accept a plain primary-button click on a visible
  selectable row in Workflows or Runs, focus that panel, and select the row
  under the pointer using its current filtered/paginated/scrolled content.
  It shall not open Detail/Monitor, start/resume/delete a run, or emit an
  activation command. Existing workflow selection shall still update the Runs
  filter and load workflow context through the current asynchronous path.
- **FR-03:** The system shall route a plain vertical wheel event within the
  eligible list content rectangle to that list, even when the other panel has
  keyboard focus. Each event shall move the existing selected index by three
  selectable rows in its direction, clamp at the first/last item, and keep the
  selected item visible. It shall preserve keyboard focus and shall not wrap,
  activate a row, or create an independent off-screen list selection.
- **FR-04:** The system shall ignore clicks on borders, titles, pagination,
  headers, row-spacing gaps, and trailing blank rows. Wheel targeting shall
  use the actual list content rectangle (including blank fill within that
  rectangle), excluding list header/filter/pagination and panel chrome. Empty,
  loading, error, and zero-content-area lists shall produce no changes.

**Proof Artifacts:**

- Model tests: root/selector/runs cases at wide and stacked Home layouts prove
  row selection, focus, filtering, viewport offsets, and no activation messages.
- Boundary tests: first/last indices, pagination/filter results, spaces between
  workflow rows, and blank/error/loading panels demonstrate bounded no-op cases.
- Terminal capture: a synthetic workflow/run inventory demonstrates click
  selection and wheel selection in both panels, followed by keyboard opening.

### Unit 2: Monitor selection and pointer-targeted scrolling

**Purpose:** Make long run inventories and transcripts navigable while
preserving Monitor's existing selection, evidence, and live-follow semantics.

**Functional Requirements:**

- **FR-05:** The system shall focus Steps and select the visible row receiving
  a valid primary click, using the current flattened row model and viewport
  offset. This includes ordinary steps, visible fan-out children, and visible
  output-file rows. It shall update the Transcript/file preview exactly as
  existing keyboard row selection does, without expanding/collapsing trees,
  entering Transcript focus, leaving Monitor, or executing lifecycle actions.
- **FR-06:** The system shall focus Transcript on a primary click inside its
  visible content area without changing the selected step, transcript item,
  file selection, expansion state, or scroll offset. A Steps click that does
  not hit a selectable row shall be a no-op, including no focus change.
- **FR-07:** The system shall route vertical wheel input in Steps to a
  three-row, end-clamped selection movement over its current flattened rows.
  It shall preserve keyboard focus, keep selection visible, and refresh the
  selection-driven preview once for the final selected row. It shall not
  expand/collapse rows or invoke run/step actions. Monitor row heights shall
  come from the rendered row model, not a fixed rows-per-step assumption.
- **FR-08:** The system shall route vertical wheel input inside visible
  Transcript content to a three-rendered-line viewport movement, clamped to
  available content. This shall work for the existing transcript and literal
  file-preview presentations, without moving the selected item/file or
  triggering disclosure, copying, editing, or navigation.
- **FR-09:** The system shall update existing transcript follow state after a
  wheel scroll: scrolling away from the bottom disables following, and reaching
  the bottom restores it according to the current follow contract. New output
  shall not pull a reader away from a position above the bottom. Scrolling
  shall remain within loaded/bounded content rather than scanning all history.
- **FR-10:** The system shall hit-test only panels actually rendered. In narrow
  Monitor layout, the hidden panel shall not receive events at its former or
  nominal coordinates. Security summaries, status, gate bar, footer, and panel
  borders/titles shall not be scrolling or click targets.

**Proof Artifacts:**

- Monitor model tests: ordinary/fan-out/file rows with variable heights and
  nonzero viewport offsets prove the clicked row matches rendered evidence.
- Routing tests: Steps/Transcript focus crossed with pointer over each visible
  panel prove exactly one owner changes and keyboard focus survives wheels.
- Streaming fixture: wheel away, append synthetic finalized content, verify
  position remains; wheel to bottom, append again, verify follow behavior.
- Terminal capture: long synthetic transcript and expanded Steps at 120×35 and
  60×24 show pointer-targeted movement and unchanged keyboard controls.

### Unit 3: Detail scrolling and input isolation

**Purpose:** Extend the read-only overlay while preventing newly routed mouse
messages from bypassing existing modal and text-input boundaries.

**Functional Requirements:**

- **FR-11:** The system shall route a plain vertical wheel event within the
  Detail overlay's content viewport to a clamped three-line scroll in both
  list and chart modes. It shall not change mode, horizontal offset, underlying
  Home selection/focus, or open/start a run. Detail clicks shall remain no-ops;
  its existing keyboard controls and exit behavior shall remain intact.
- **FR-12:** The system shall consume mouse events before base-panel dispatch
  when global help, palette, confirmation, Monitor helpchat, review workspace,
  an open focused Gate overlay, or active text capture is present. Active text
  capture includes workflow filtering, transcript search, and input editors.
  These excluded interactions shall gain no new mouse behavior, and clicks or
  wheels outside their visible box shall not pass through to base panels.
  Detail is the explicit exception: while it is visible, only FR-11 applies.
  A pending but closed Gate bar shall not disable ordinary panel navigation.
- **FR-13:** The system shall ignore mouse motion, release, secondary buttons,
  horizontal wheels, and modifier-bearing clicks/wheels for this slice. It
  shall reject negative/out-of-terminal coordinates and zero-sized regions
  without panicking. A resize shall update the geometry before the next event;
  stale coordinates shall never address a hidden or neighboring panel.
- **FR-14:** The system shall preserve existing keyboard bindings, focus cycles,
  filtering, help/palette access, review anchors/drafts, and engine event
  processing. Mouse handling shall use the model's normal update ownership;
  it shall not introduce goroutines, terminal readers, or blocking I/O for
  coordinate mapping. Selection-driven existing load commands remain allowed.
- **FR-15:** The system shall document supported surfaces, primary-click versus
  activation behavior, wheel selection on lists, and excluded surfaces in
  discoverable TUI help or the TUI guide, without replacing contextual keyboard
  hints. Documentation shall not describe mouse as required or configurable.

**Proof Artifacts:**

- Detail tests: list/chart content, top/bottom, loading/empty, chrome, and
  outside coordinates demonstrate viewport-only changes and no Home mutation.
- Root/Monitor exclusion matrix: each overlay/text-capture state consumes events
  with identical underlying focus, selection, scroll, and no action commands.
- Regression output: focused TUI tests, TUI race checks, root build/tests/vet,
  and formatting of changed files demonstrate keyboard/runtime preservation.
- Documentation plus manual capture: locate the mouse guidance, use Detail's
  wheel, close with the keyboard, and show unchanged Home state.

## Non-Goals (Out of Scope)

- Mouse controls in Gate inputs, reviews, help/chat modals, palette, or
  standalone chat; mouse-based text selection or review comment anchoring.
- Row activation, double-click actions, drag/hover interaction, context menus,
  panel resizing, horizontal/modified wheel gestures, or touch-specific input.
- Mouse preference persistence, environment variables, flags, workflow schema
  fields, theme changes, or dependency/toolchain upgrades.
- Changing engine state, backend selection, transcript storage format, or
  introducing a second list-selection/scroll model.
- Rewriting completed charm-clickable artifacts or claiming implementation
  completion in the open-goals catalog before proof exists.

## Design Considerations

| Surface | Primary click | Vertical wheel | Keyboard focus on wheel |
|---|---|---|---|
| Home Workflows | Focus and select valid row | Move selection three rows | Preserved |
| Home Runs | Focus and select valid row | Move selection three rows | Preserved |
| Monitor Steps | Focus/select visible row and refresh preview | Move selection three rows and refresh preview | Preserved |
| Monitor Transcript | Focus content; keep cursor/offset | Scroll content three lines | Preserved |
| Detail list/chart overlay | No-op | Scroll content three lines | Preserved |
| Excluded surface/chrome | No new behavior | No new behavior | Preserved |

Question 5 is the specific exception to Question 3's selection wording: list
wheels move selection, while content wheels do not. “Do not activate” means
no screen opening, execution, expansion, or lifecycle action; it does not
suppress existing preview updates caused by selection. Three rows/lines,
unmodified vertical gestures, and conservative overlay exclusion are explicit
implementation choices filling gaps left by the recommendations.

Reuse existing theme/focus markers and geometry. No hover indicator or new
panel chrome is necessary. Within a list, a click requires an actual selectable
row; a wheel may target blank content space and still move its selection.
Focus and selection remain separate: wheeling an unfocused list may change the
preview/filter that selection already controls while keyboard focus stays put.

## Repository Standards

- Follow [AGENTS.md](../../../AGENTS.md), [Go conventions](../../CONVENTIONS.md),
  [TUI engineering](../../TUI.md), and [Testing](../../TESTING.md).
- Keep root composition in `internal/tui`; screen-local selection and viewport
  behavior belongs in selector, runs, monitor, and detail. Child packages must
  not import the root package. Shared presentation stays in `internal/tui/shared`.
- Use the existing `charm.land/*/v2` APIs and pinned versions in `go.mod`.
  Remove superseded routing branches in the same implementation change.
- Keep tests behavior-based with synthetic fixtures and no live models. Use
  ANSI-aware terminal-cell dimensions and model state assertions, not byte
  widths or implementation-duplicating formulas.
- Format changed Go files only. Run `go test ./internal/tui/...`,
  `go test -race ./internal/tui/...`, `go build ./cmd/jig`, `go test ./...`,
  and `go vet ./...` for the implementation, reporting blocked checks honestly.

## Technical Considerations

Current ownership and implementation evidence:

- [root_update.go](../../../internal/tui/root_update.go) presently routes mouse
  only to Home; [home.go](../../../internal/tui/home.go) ignores Detail mouse
  and supports only workflow-row clicks. Root overlay precedence must be
  checked explicitly for mouse messages, not inferred from keyboard handling.
- `homeLayout` already supplies pane geometry for render/size/hit testing.
  [selector/hit.go](../../../internal/tui/selector/hit.go) accounts for filter,
  pagination, row height/spacing, and panel origin. Preserve that behavior.
- [runs/model.go](../../../internal/tui/runs/model.go) owns filtered rows,
  cursor, and viewport. Hit testing must resolve the rendered filtered index
  plus current viewport offset rather than the unfiltered backing slice.
- [monitor_layout.go](../../../internal/tui/monitor/monitor_layout.go) owns
  panel splitting, vertical budgets, and cursor visibility;
  [monitor_update.go](../../../internal/tui/monitor/monitor_update.go) owns
  selection-triggered preview refresh. Use `visibleRows` and actual row heights
  for ordinary steps, runtime children, and file rows.
- [monitor_transcript.go](../../../internal/tui/monitor/monitor_transcript.go)
  already provides `scrollTranscript`; integrate with its follow-state handling
  instead of feeding wheel events to whichever viewport currently has focus.
- [detail/update.go](../../../internal/tui/detail/update.go) already owns
  viewport updates and list/chart mode. Gate events at the rendered content
  rectangle before passing them to its viewport.

Mouse coordinates are terminal cells. Use shared frame/content origins and
owner-local hit tests, with global-to-local conversion once per dispatch.
Keep one geometry definition per surface and derive it from the same layout
used for rendering; exact helper names remain implementation details.

The installed Bubble Tea v2.0.8 exposes `MouseClickMsg`, `MouseWheelMsg`, button
and coordinate data. Bubbles v2.1.1's viewport supports wheel scrolling with a
three-line default, but does not establish jig's cross-panel routing policy.
The upstream [mouse API](https://github.com/charmbracelet/bubbletea/blob/main/mouse.go)
and [viewport implementation](https://github.com/charmbracelet/bubbles/blob/main/viewport/viewport.go)
were checked as primary references; use installed source for exact signatures.
No dependency upgrade is required. Pointer-targeting and list selection are
jig product decisions, not behavior guaranteed by Bubble Tea automatically.

## Security Considerations

Treat terminal coordinates as untrusted input: clamp/check bounds before slice
indexing and never derive an execution path or command from coordinates or
rendered text. Use existing row identity and selection operations. Mouse input
must not bypass confirmation, text capture, review, or lifecycle boundaries.
This feature introduces no credentials, external service, or persistent data.
Proof captures and fixtures must use fabricated run/output data without raw
real transcripts, secrets, or user filesystem details.

## Success Metrics

1. Every FR has an observable test or manual proof, with all five eligible
   surfaces represented in the implementation's acceptance evidence.
2. Cross-panel wheel tests preserve keyboard focus in every case, move exactly
   the specified rows/lines unless clamped, and never activate a row.
3. Geometry cases pass for filtered/paginated/scrolled data, variable-height
   rows, Unicode, first/last bounds, 120×35, 60×24, and tiny/zero dimensions.
4. Every excluded overlay/text-capture state rejects base-panel mutations,
   including pointer locations outside the overlay's own box.
5. Existing keyboard tests and applicable build/test/vet/race checks pass;
   manual evidence demonstrates mouse use followed by keyboard continuation.

## Open Questions

No blocking product questions remain. The accepted recommendation conflict
and implementation assumptions are resolved explicitly above. Helper naming
and test-file organization are implementation details, not deferred behavior.
