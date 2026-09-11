# charm-clickable.md

## Introduction/Overview

Make the startup workflow selector respond to a mouse click on a visible
workflow row by selecting that row, the same as moving the keyboard cursor to
it with `j`/`k`. The selector currently supports keyboard navigation and
Enter, but the Bubble Tea v2 list does not translate mouse coordinates into
list selections; this feature adds that event flow. Opening the Detail
overlay remains exclusively a keyboard action (the `d` key); a click never
opens Detail.

The primary goal is predictable mouse interaction: a click must select exactly
the workflow row rendered at that location, including after filtering,
pagination, scrolling, resizing, and changes in terminal width.

## Goals

1. Enable terminal cell-motion mouse delivery at the root Bubble Tea view so
   primary mouse clicks can reach the selector.
2. Make a valid click on a visible workflow row move the selector's keyboard
   cursor to that row's item, the same as pressing `j`/`k` until it is
   reached — without opening the Detail overlay.
3. Keep hit testing synchronized with the selector's actual rendered geometry,
   including the outer panel, list content area, row spacing, filtering,
   pagination, scrolling, borders, and Unicode display widths.
4. Treat clicks on empty space, borders, the footer, loading/error/empty states,
   other screens, and out-of-bounds coordinates as no-ops without panics or
   accidental navigation.
5. Preserve all existing keyboard behavior and pass the repository's formatting,
   vet, build, and test checks.

## User Stories

- As an operator browsing workflows, I want to click a visible workflow row so
  that I can select it without first moving the keyboard cursor there.
- As an operator using a filtered or scrolled list, I want the click target to
  match the row I see so that mouse selection never selects a different
  workflow than the one I clicked.
- As an operator clicking outside the list, I want nothing to happen so that
  decorative UI and footer hints are not treated as workflows.
- As an operator, I want a click to only change the selection so that opening
  Detail remains a deliberate `d` keypress, never an accidental side effect of
  a click.
- As a maintainer, I want mouse hit testing to use the selector's rendered
  layout state so that future layout changes do not silently make click targets
  inaccurate.

## Demoable Units of Work

### Unit 1 — Deliver mouse input through the root view

**Purpose:** Make mouse clicks available to the active Bubble Tea model while
preserving the existing full-screen TUI configuration.

**Functional Requirements:**

- The system shall configure the root `tea.View` to request cell-coordinate
  mouse events, including primary click events, using Bubble Tea v2's
  view-level mouse mode configuration.
- The system shall route mouse events through the root update path to the active
  screen without changing navigation behavior on detail, runs, or monitor
  screens.
- The system shall not enable mouse handling by reading a process-wide
  environment variable or by adding a workflow configuration field.

**Proof Artifacts:**

- Unit test: a root-model test demonstrates that a mouse event is delivered to
  the selector while the selector screen is active and is not converted into a
  navigation message on another screen.
- Render/configuration assertion: the root view demonstrates that cell-motion
  mouse mode is enabled alongside the existing alternate-screen and background
  settings.

### Unit 2 — Select a workflow row from a valid visible row click

**Purpose:** Add the end-to-end click-to-select interaction: a click moves the
keyboard cursor to the clicked row without opening Detail.

**Functional Requirements:**

- The system shall identify the workflow item occupying the clicked visible row
  in the selector's current list state.
- For a click inside a valid workflow row, the system shall move the
  selector's keyboard cursor to that item, the same as if `j`/`k` had been
  pressed until it was reached.
- A click shall not require the clicked row to be the keyboard-selected row
  before selecting it, and shall not open the Detail overlay regardless of
  whether the clicked row was already selected.
- The root shall not treat a click as a `ShowDetailMsg` or any other
  Detail-opening trigger; only the `d` key opens Detail.
- Mouse selection shall remain disabled while the selector's filter editor is
  capturing text; clicks in the filter/editor area shall not change the
  selection.

**Proof Artifacts:**

- Table-driven selector-model test: representative row coordinates move the
  keyboard cursor to the expected workflow item, including a row other than
  the current keyboard selection.
- Root-model test: a valid row click updates `SelectedPath()` to the clicked
  row's workflow and leaves the Detail overlay closed.

### Unit 3 — Keep hit regions aligned with rendered selector geometry

**Purpose:** Make coordinate mapping correct across all selector states that
change what is visible or where it is drawn.

**Functional Requirements:**

- The system shall derive clickable row regions from the same dimensions and
  visible-item state used to render the Bubbles v2 list and the surrounding
  selector panel; it shall not use a separately maintained hard-coded row map.
- The mapping shall account for the panel's outer border and title, the list's
  inner origin, row height and spacing, the externally rendered footer, and the
  list viewport height.
- The mapping shall use the Bubbles list's current filtered item set and visible
  window, so pagination and vertical scrolling select the item displayed at the
  clicked coordinate rather than the unfiltered or first-page item.
- The mapping shall remain correct after `tea.WindowSizeMsg`, including narrow
  terminals and dimensions clamped to the selector's minimum usable size.
- The mapping shall compare terminal cell coordinates with terminal display
  geometry and shall not be based on byte offsets or rune counts; Unicode names,
  descriptions, and wide glyphs shall not shift the row boundary calculation.
- The system shall exclude the panel border, title area, footer, filter input
  area, list padding, and any blank space below the final visible row from
  clickable row regions.
- A pure geometry helper or equivalent deterministic model logic shall expose
  enough behavior for table-driven tests and optional fuzz testing without
  starting a Bubble Tea program or reading the filesystem.

**Proof Artifacts:**

- Table-driven geometry tests demonstrate correct item selection for filtered,
  paginated, scrolled, resized, Unicode-width, border, footer, and blank-space
  cases.
- Boundary test: clicks immediately above, inside, and immediately below each
  row demonstrate that only the row interior opens a workflow.
- Optional fuzz test: arbitrary non-negative and out-of-bounds coordinates do
  not panic and never return an item outside the current visible window.
- Fixed-size selector render fixture: the rendered rows and their tested hit
  regions demonstrate that the map follows the visible output at more than one
  terminal width.

### Unit 4 — No-op safety and regression verification

**Purpose:** Ensure mouse support is narrow, safe, and does not alter existing
selector or non-selector interactions.

**Functional Requirements:**

- The system shall perform no state change and no selection change for
  non-primary mouse actions, negative or out-of-bounds coordinates,
  panel-border, footer, blank-space, loading, error, or empty-selector clicks.
- The system shall ignore mouse clicks when the active root screen is detail,
  runs, or monitor unless those screens already define an unrelated mouse
  interaction; this feature shall not introduce cross-screen navigation or
  selection changes.
- The system shall preserve keyboard Enter, filtering, cursor navigation,
  pagination, and existing global help/quit behavior.
- The implementation shall be race-safe under the repository's normal test
  execution and shall not mutate shared selector state from a mouse event in a
  way that introduces data races.

**Proof Artifacts:**

- Negative-case table test: invalid coordinates and non-selector states
  demonstrate no-op behavior (no selection change, no Detail overlay, no
  panics).
- Regression test: existing selector keyboard tests continue to pass, including
  Enter during and outside filtering.
- Command output: `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, and
  `gofmt -l .` demonstrate build, test, static-analysis, and formatting
  cleanliness.

## Non-Goals (Out of Scope)

- Adding mouse interactions to detail, runs, monitor, help, confirmation, or
  other screens.
- Adding mouse wheel scrolling, drag selection, hover styling, right-click
  actions, context menus, double-click semantics, or touch support.
- Replacing Bubbles list navigation, filtering, pagination, or the existing
  keyboard Enter behavior.
- Changing workflow discovery, TOML schema, workflow validation, path
  authorization, or direct workflow execution semantics.
- Adding environment-variable or command-line configuration for mouse mode.
- Upgrading Go, Bubble Tea, Bubbles, Lipgloss, or unrelated dependencies as
  part of this interaction change; any needed dependency maintenance should be
  tracked separately.

## Design Considerations

- A primary-button click on a rendered workflow row is the supported gesture.
  The interaction should feel equivalent to moving the keyboard cursor to that
  row, and it must select the row actually under the pointer even when another
  row is keyboard-selected. It must never open the Detail overlay by itself —
  that remains the `d` key's job.
- Hit testing should be owned by the selector because the selector owns list
  state and knows filtering, visible items, panel sizing, and footer geometry.
  The root owns view-level mouse configuration and event delivery, then forwards
  the event to the active selector. This keeps the root compositor thin while
  avoiding geometry logic duplicated in the root.
- The selector should use one shared layout calculation for rendering-related
  dimensions and hit testing. If the current list API does not expose enough
  layout information, introduce a small selector-local layout/hit-region value
  rather than duplicating coordinate formulas in tests and event handlers.
- No visual redesign is required. Existing Charmtone styles, panel borders,
  footer hints, filtering presentation, and list row rendering remain unchanged.
- Invalid clicks are intentionally silent: no error message, cursor movement,
  focus change, or detail navigation is required.

## Repository Standards

- Keep the feature within the existing `internal/tui` root and
  `internal/tui/selector` package boundaries. The selector remains responsible
  for workflow-picker state; the root remains responsible for screen
  composition and `tea.View` configuration.
- Follow Bubble Tea v2 and Bubbles v2 APIs already used by the repository; do
  not introduce a second UI framework or a parallel event bus.
- Reuse the list's existing selection primitive (`list.Model.Select`) rather
  than introducing a parallel selection-state field; a click must not route
  through `selector.ShowDetailMsg` or any other Detail-opening flow.
- Keep all styling through the existing shared theme singleton. This feature
  requires no new colors or ad-hoc Lipgloss styles.
- Keep files focused by concern. Layout or hit-testing helpers belong near the
  selector layout code, while root mouse-mode configuration belongs with root
  view/update code.
- Add table-driven tests following the repository's existing TUI selector and
  root test patterns. Prefer deterministic model/geometry tests over terminal
  timing or screenshot-only assertions.
- Comments shall explain non-obvious reasons, especially any coordinate-system
  conversion or coupling to Bubbles list rendering.
- Preserve persistence-off behavior and backend-neutral TUI boundaries; this
  interaction must not import engine, runner, harness, or workflow execution
  concerns into selector hit testing.
- Before completion, run `gofmt -l -w .`, `go vet ./...`, `go build ./cmd/jig`,
  and `go test ./...`; examples and documentation must remain valid.

## Technical Considerations

- Bubble Tea v2 mouse delivery is opt-in on `tea.View`; configure the root view
  for cell-coordinate mouse events and handle the resulting mouse message type
  in the existing value-receiver model flow.
- Mouse coordinates are terminal cells, while rendered strings contain runes
  and possibly multi-cell Unicode glyphs. Use terminal-width-aware geometry
  (`lipgloss.Width` or the equivalent existing layout primitive) for horizontal
  calculations, and keep row hit testing primarily vertical so wide text cannot
  create false row boundaries.
- The selector's panel body is smaller than the full terminal because of the
  outer frame and footer. The same width/height values passed to the list during
  resize must be the basis for the list origin and visible-row calculations.
- Filtering changes the list's item set and may change the input/footer state;
  pagination and scrolling change the visible window. Hit testing must query
  current list state at event time, not cache item indices from discovery.
- Resizing can occur before or between mouse events. Recompute or invalidate
  layout state when `tea.WindowSizeMsg` is processed so stale dimensions cannot
  route a click to a neighboring row.
- Keep event handling synchronous and deterministic: a valid click updates the
  list's selection directly (`list.Model.Select`), the same as a `j`/`k`
  keypress, with no goroutine, filesystem access, or network operation in hit
  testing.
- If Bubble Tea's concrete mouse message exposes multiple mouse actions, accept
  only the primary-button press/click action needed by this feature and ignore
  motion, release, wheel, and other buttons. The implementation should match
  the actual v2 message API present in `go.mod` rather than adding compatibility
  shims for older versions.
- Tests should include race execution where practical and may fuzz pure
  coordinate mapping. Fuzz inputs must be bounded to avoid turning a geometry
  test into an unbounded resource consumer.

## Security Considerations

- Workflow names, descriptions, and paths are untrusted local metadata. Mouse
  hit testing shall use the already discovered item identity and the list's
  existing selection primitive; it shall not construct shell commands,
  evaluate TOML content, or bypass existing workflow loading/validation
  boundaries.
- The feature does not add credentials, authentication, authorization, network
  access, or filesystem write behavior. No secrets, terminal captures, or run
  artifacts may be added to tests or committed as proof artifacts.
- Out-of-bounds and malformed coordinates must be treated as data errors/no-ops,
  not as reasons to index into a slice or panic. This is the relevant safety
  boundary for terminal input.
- Direct execution or new path trust decisions are outside this feature. Any
  future change that lets mouse input trigger execution would require separate
  authorization and audit design.

## Success Metrics

1. At least 100% of valid visible-row cases in the table-driven geometry suite
   map to the rendered item, including filtered, paginated, scrolled, resized,
   and Unicode-width fixtures.
2. 100% of invalid, out-of-bounds, footer, border, loading, error, empty, and
   non-selector cases produce no selection change, no Detail overlay, and no
   panic.
3. A manual TUI smoke test can select a workflow by clicking each visible row
   at two terminal sizes and after filtering/scrolling, with the keyboard
   cursor moving to the clicked row and the Detail overlay staying closed
   until `d` is pressed.
4. Existing keyboard selector and root navigation tests remain green, and the
   full repository build, test, vet, and formatting checks complete successfully.
5. The implementation adds no new configuration surface and no dependency
   upgrade outside the feature's required API usage.

## Open Questions

No open questions at this time. The supported gesture is a primary-button click
on a visible selector row, which selects that row (matching keyboard `j`/`k`
navigation); all other mouse actions and selector regions are defined as
no-ops for this feature, and opening Detail remains exclusively the `d` key
(user-confirmed behavior change from the original click-opens-Detail design —
see the task list's implementation-time note).
