# 25-tasks-optional-mouse-navigation.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/root_update.go` | Owns root-level message precedence and currently limits mouse routing to Home; must dispatch supported events to the active screen while consuming them behind global overlays and confirmations. |
| `internal/tui/home.go` | Owns Home's wide/stacked geometry, focus, workflow-to-runs synchronization, Detail overlay routing, and the existing workflow click handler. |
| `internal/tui/root_test.go` | Existing root mouse fixtures and Home click tests; extend for cross-pane routing, focus, activation safety, modal/text-capture exclusion, narrow/wide geometry, and keyboard parity. |
| `internal/tui/selector/hit.go` | Resolves rendered workflow rows using the Bubbles list's filter, paginator, delegate height, spacing, and panel origin; extend owner-local selection/wheel targeting without duplicating that geometry in root. |
| `internal/tui/selector/hit_test.go` | Existing workflow hit-test coverage; extend for wheel movement, clamping, content rectangles, filtered/paginated data, spacing gaps, blank fill, and invalid dimensions. |
| `internal/tui/selector/model.go` | Owns the workflow list, selected item, pane dimensions, delegate row height/spacing, loading/error state, and keyboard filtering contract. |
| `internal/tui/runs/model.go` | Owns filtered run rows, cursor, viewport, pane dimensions, and run selection state used by Home. |
| `internal/tui/runs/update.go` | Owns keyboard selection movement, viewport synchronization, and cursor visibility; mouse selection must reuse these state transitions without emitting run actions. |
| `internal/tui/runs/view.go` | Defines the one-line rendered run-row layout used for owner-local hit testing. |
| `internal/tui/runs/runs_test.go` | Existing selection/viewport/filter tests; extend with click and wheel behavior over filtered/scrolled rows and all boundary states. |
| `internal/tui/monitor/monitor_update.go` | Owns Monitor message routing, focus transitions, text-input/review dispatch, Steps selection, Transcript scrolling, and overlay precedence. |
| `internal/tui/monitor/monitor_layout.go` | Owns Monitor vertical budgets, wide/narrow panel sizing, viewport dimensions, and variable-height cursor visibility; mouse rectangles and row ranges must derive from these calculations. |
| `internal/tui/monitor/monitor_view.go` | Defines which Monitor panels or review workspace are actually rendered and where noninteractive status/security/gate/footer chrome appears. |
| `internal/tui/monitor/monitor_steps.go` | Renders ordinary step, fan-out child, and output-file rows with different physical heights; supplies the evidence that row hit testing must match. |
| `internal/tui/monitor/monitor_model.go` | Owns flattened `visibleRows`, focus, Gate/review/help state, transcript/file selection, and text-capture state used by mouse eligibility checks. |
| `internal/tui/monitor/monitor_transcript.go` | Owns bounded transcript/file content, vertical scrolling, auto-follow state, and append behavior that wheel handling must preserve. |
| `internal/tui/monitor/monitor_mouse_test.go` | New focused test file for Monitor panel geometry, row hit testing, pointer routing, unsupported input, narrow-layout hiding, and exclusion matrices. |
| `internal/tui/monitor/monitor_transcript_test.go` | Existing transcript fixtures; extend for three-line wheel movement and streaming follow/unfollow behavior. |
| `internal/tui/detail/model.go` | Owns Detail viewport dimensions and list/chart mode state needed for content-region hit testing. |
| `internal/tui/detail/update.go` | Owns Detail viewport updates and resizing; admit only eligible vertical wheel events after content-rectangle validation. |
| `internal/tui/detail/view.go` | Defines Detail panel/footer geometry and rendered viewport placement for the shared layout calculation. |
| `internal/tui/detail/detail_test.go` | Existing Detail list/chart/viewport tests; extend for wheel bounds, click no-ops, loading/empty/chrome/outside cases, and Home-state isolation. |
| `internal/tui/shared/panel.go` | Existing panel frame/content-origin primitives used to derive terminal-cell rectangles consistently; reuse rather than adding competing border constants. |
| `docs/TUI.md` | Authoritative operator/developer guidance where supported and excluded mouse behavior must be documented. |
| `internal/tui/mouse_documentation_contract_test.go` | New documentation contract test ensuring the discoverable guide retains the five supported surfaces, selection-versus-activation rule, wheel increments, and exclusions. |
| `docs/specs/25-spec-optional-mouse-navigation/artifacts/` | New sanitized proof directory for deterministic terminal captures and focused/regression command output. |

### Notes

- Keep root composition and global-overlay precedence in `internal/tui`; keep selection, viewport, and hit-test mutations inside the owning child package. Child packages must not import `internal/tui`.
- Derive terminal-cell rectangles from `homeLayout`, `verticalLayout`/`panelSplit`, and `shared.PanelFrame`/`PanelContentOrigin`; do not create a second layout model or use byte widths for rendered content.
- Use the pinned `charm.land/bubbletea/v2` and `charm.land/bubbles/v2` mouse/viewport APIs already present in `go.mod`; this feature adds no dependency or configuration surface.
- Write behavior-first model tests with fabricated workflows, runs, transcripts, and files. Never copy local `.jig/` data, `.env` content, credentials, or private identifiers into fixtures or proof artifacts.
- During implementation, format only changed Go files. Run focused package tests while iterating, followed by `go test ./internal/tui/...`, `go test -race ./internal/tui/...`, `go build ./cmd/jig`, `go test ./...`, and `go vet ./...`; report blocked checks accurately.

## Tasks

### [x] 1.0 Complete Home click and pointer-targeted list navigation

Implement the keyboard-primary mouse contract for the rendered Workflows and
Runs panels: plain primary clicks focus and select real rows without activation,
while plain vertical wheel events move the targeted list selection by three
rows without changing keyboard focus. Reuse each list's filtered,
paginated/scrolled selection model and the same Home geometry used for sizing
and rendering. All chrome, gaps, invalid coordinates, empty states, unsupported
mouse messages, and text-capture states remain bounded no-ops. Covers FR-01
through FR-04.

#### 1.0 Proof Artifact(s)

- Test: `internal/tui/selector/hit_test.go` passes cases for filtered and paginated workflow rows, nonzero offsets, row-spacing gaps, pane chrome, trailing blank space, and zero-sized content, demonstrating FR-02 and FR-04 hit testing against rendered rows.
- Test: `internal/tui/runs/runs_test.go` passes click and three-row wheel cases for filtered/scrolled Runs data, first/last clamping, empty/loading/error states, and focus preservation, demonstrating FR-02 through FR-04 without activation.
- Test: `internal/tui/root_test.go` retains `TestRootViewMouseModeCellMotion` and passes wide and stacked Home routing cases with Workflows/Runs focus crossed against pointer location, proving mouse delivery stays enabled without configuration, selection-driven workflow synchronization still occurs, keyboard focus survives wheel input, and no Detail/Monitor/run action command is emitted for FR-01 through FR-04.
- Terminal capture: `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-1-home-mouse-navigation.txt` records a synthetic multi-workflow/multi-run Home session at 120x35 and 60x24 in which pointer selection is followed by keyboard opening, demonstrating mouse remains optional and non-activating.

#### 1.0 Tasks

- [x] 1.1 Add failing table-driven cases to `internal/tui/selector/hit_test.go` for plain primary-click selection and three-row wheel movement over filtered and paginated items, including first/last clamping, nonzero page offsets, Unicode rows, row-spacing gaps, panel chrome, trailing blank content, loading/error/empty state, and zero/tiny dimensions (FR-02 through FR-04).
- [x] 1.2 Extend the selector's owner-local hit/selection API in `internal/tui/selector/hit.go` so a click can resolve and select only an actual rendered row, while a wheel can validate the full list content rectangle and move the existing Bubbles selection by exactly three visible items with clamping and selected-row visibility; return whether state changed so root can synchronize only when needed (FR-02 through FR-04).
- [x] 1.3 Add failing cases to `internal/tui/runs/runs_test.go` for one-line rendered row clicks and three-row wheel movement over filtered and scrolled `visibleRows`, proving selection identity, viewport visibility, first/last clamping, blank content targeting, and no commands for empty/loading/error-equivalent or invalid geometry states (FR-02 through FR-04).
- [x] 1.4 Add an owner-local Runs pointer API, colocated with its selection/viewport logic, that maps pane-local clicks through the current viewport offset to `visibleRows`, moves the existing cursor for clicks or a three-row wheel delta, and calls the existing viewport synchronization/visibility path without invoking Open, NewRun, Resume, Delete, Copy, or Back actions (FR-02 through FR-04).
- [x] 1.5 Retain the existing `TestRootViewMouseModeCellMotion` assertion, then extend `internal/tui/root_test.go` with initially failing wide and stacked Home routing tests that cross current keyboard focus with pointer location over Workflows and Runs; assert a valid click focuses and selects only the target panel, wheel movement preserves focus, workflow selection still produces only the existing async load/synchronization command, and Runs input emits no activation command (FR-01 through FR-04).
- [x] 1.6 Refine `homeLayout`/Home mouse routing so rendering, child sizing, and pointer targeting share the same outer-pane and inner-content geometry after every resize; reject negative/out-of-terminal coordinates before converting to pane-local coordinates (FR-02 through FR-04 and FR-13).
- [x] 1.7 Replace the workflow-click-only branch in `updateHomeMouse` with explicit handling for unmodified `tea.MouseClickMsg` using `tea.MouseLeft` and unmodified vertical `tea.MouseWheelMsg` using `tea.MouseWheelUp`/`tea.MouseWheelDown`; delegate to the selected pane owner, change focus only after a valid row click, preserve focus on wheels, and call `maybeSyncHomeSelection` only when workflow selection changes (FR-01 through FR-04).
- [x] 1.8 Expand the Home negative matrix for release, motion, secondary click, horizontal wheel, modifier-bearing click/wheel, row gaps, headers/filter/pagination, panel borders/titles, trailing coordinates outside content, and active workflow filtering; assert identical selection/focus/overlay state and nil action commands (FR-03, FR-04, FR-12, and FR-13).
- [x] 1.9 Run the focused selector, Runs, and root Home mouse tests, then record a sanitized synthetic wide/narrow Home terminal capture at `artifacts/25-1-home-mouse-navigation.txt` showing pointer selection in both panels followed by existing keyboard opening (FR-01 through FR-04).

### [x] 2.0 Add Monitor row selection and pointer-targeted transcript scrolling

Route supported mouse input only to the visible Monitor panel under the
pointer. Steps clicks select ordinary steps, fan-out children, and output-file
rows through the existing preview-refresh path; Steps wheels move the flattened
selection by three rows using rendered row heights. Transcript clicks focus its
content without moving state, and Transcript wheels scroll transcript or file
content by three rendered lines while preserving its existing bounded-history
and follow-state contract. Hidden narrow-layout panels and Monitor chrome remain
non-targets. Covers FR-05 through FR-10.

#### 2.0 Proof Artifact(s)

- Test: `internal/tui/monitor/monitor_mouse_test.go` passes click cases for ordinary, fan-out-child, and output-file rows with variable rendered heights and nonzero viewport offsets, proving FR-05 selects the visible evidence row and refreshes exactly its existing transcript/file preview without activation.
- Test: `internal/tui/monitor/monitor_mouse_test.go` passes a Steps/Transcript focus-by-pointer routing matrix at wide and narrow sizes, including hidden panels, borders, titles, status/security/gate/footer chrome, invalid coordinates, and blank Steps content, demonstrating FR-06, FR-07, and FR-10 ownership and no-op boundaries.
- Test: `internal/tui/monitor/monitor_transcript_test.go` passes three-line wheel and streaming-follow fixtures for transcript and literal file preview content, proving FR-08 and FR-09 clamp within loaded content, preserve item/file selection, remain above-bottom after appended content, and resume following after reaching bottom.
- Terminal capture: `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-2-monitor-mouse-navigation.txt` records expanded Steps and a long synthetic transcript at 120x35 and 60x24, followed by keyboard navigation, demonstrating FR-05 through FR-10 end to end without lifecycle actions.

#### 2.0 Tasks

- [x] 2.1 Add failing `internal/tui/monitor/monitor_mouse_test.go` cases for primary clicks on ordinary steps, expanded fan-out children, and output-file rows at nonzero viewport offsets, with mixed one-line/two-line row heights; assert the cursor resolves to the rendered row, focus becomes Steps, and the existing transcript/file preview changes once without expansion or action commands (FR-05).
- [x] 2.2 Introduce a Monitor panel-geometry result derived from `verticalLayout`, `panelSplit`, current narrow mode, and `shared.PanelFrame`/`PanelContentOrigin`, and use it from both rendering/resize assumptions and pointer hit testing so only the panel actually rendered exposes a content rectangle (FR-06, FR-08, and FR-10).
- [x] 2.3 Extract one owner-local mapping from flattened `visibleRows` to physical line ranges in the Steps viewport, accounting for ordinary/child row height and file-row height, and reuse it in both `ensureCursorVisible` and click hit testing to prevent the two geometry calculations from drifting (FR-05 and FR-07).
- [x] 2.4 Add a Monitor mouse-routing branch before generic textarea, review-workspace, and focused-viewport dispatch. Accept only unmodified primary clicks and unmodified vertical wheels inside an eligible visible content rectangle, and consume every other mouse message without forwarding it to Bubbles children (FR-06, FR-10, FR-12, and FR-13).
- [x] 2.5 Route a valid Steps click through a single selection helper that clamps/sets the flattened cursor, ensures it is visible, and invokes the same `reloadTranscript`/panel refresh behavior as keyboard selection; clicks on blank Steps content must not change focus or state (FR-05 and FR-06).
- [x] 2.6 Route a Steps wheel to a final cursor delta of exactly three flattened selectable rows in its direction, clamp at the ends, preserve keyboard focus, ensure visibility, and refresh the selected transcript/file preview once after the final cursor is known (FR-07).
- [x] 2.7 Route a Transcript-content click to `focusTranscript` without changing step/item/file selection, expansion, or offsets; route a Transcript wheel through `scrollTranscript(3)`/`scrollTranscript(-3)` so transcript and literal file preview viewports move by three rendered lines and update follow state without disclosure, copy, edit, or navigation actions (FR-06, FR-08, and FR-09).
- [x] 2.8 Extend `internal/tui/monitor/monitor_transcript_test.go` with a bounded streaming fixture: wheel above bottom, append synthetic finalized content and assert the offset is retained; wheel to bottom, append again and assert the current follow contract resumes. Repeat the scroll assertions for literal file preview and verify no selected item/file changes (FR-08 and FR-09).
- [x] 2.9 Complete the Monitor routing matrix for Steps/Transcript focus crossed with pointer location, 120x35 wide and 60x24 narrow layouts, focused Gate behavior, hidden panels, blank content, security/status/input/gate/footer strips, panel borders/titles, and resize-before-next-event coordinates (FR-06, FR-07, FR-08, FR-10, FR-12, and FR-13).
- [x] 2.10 Run the focused Monitor tests and record `artifacts/25-2-monitor-mouse-navigation.txt` from a long fabricated transcript and expanded Steps tree at both required sizes, including pointer movement followed by unchanged keyboard controls and no lifecycle actions (FR-05 through FR-10).

### [x] 3.0 Enable Detail wheel scrolling and enforce mouse input isolation

Allow only plain vertical wheel movement inside the rendered Detail content
viewport, in list and chart modes, with a clamped three-line increment and no
click behavior. At root and Monitor boundaries, consume mouse input whenever a
modal, review/help surface, focused open Gate, or text-capture owner excludes
base-panel interaction. Validate message kind, button, modifiers, coordinates,
current layout, and nonzero geometry before dispatch, without new goroutines or
I/O and without disturbing any keyboard or engine-event path. Covers FR-11
through FR-14.

#### 3.0 Proof Artifact(s)

- Test: `internal/tui/detail/detail_test.go` passes list/chart wheel cases at top, middle, and bottom plus loading, empty, chrome, click, outside, and tiny-dimension cases, demonstrating FR-11 changes only the Detail viewport's vertical offset and preserves mode, horizontal offset, and underlying Home state.
- Test: `internal/tui/root_test.go` passes an exclusion matrix for global help, palette, confirmation, Detail, review workspace, and active Home text capture, proving FR-12 prevents click/wheel pass-through while Detail admits only its FR-11 wheel behavior.
- Test: `internal/tui/monitor/monitor_mouse_test.go` passes exclusion cases for helpchat, review workspace, focused open Gate, transcript search, input editors, and a pending-but-closed Gate bar, proving FR-12 distinguishes blocked interaction from ordinary panel navigation.
- Test: focused root, Monitor, and Detail tests pass table-driven cases for motion, release, secondary buttons, horizontal wheels, modifiers, negative/out-of-terminal coordinates, zero-sized regions, and resize-before-next-event behavior, demonstrating FR-13; existing keyboard and engine-event regression tests pass unchanged for FR-14.

#### 3.0 Tasks

- [x] 3.1 Add failing `internal/tui/detail/detail_test.go` cases for unmodified wheel up/down inside list and chart content at top/middle/bottom, asserting an exact three-line clamped offset and unchanged mode/horizontal offset; add loading, empty, border/title/footer, outside, click, modifier, horizontal-wheel, and zero/tiny-dimension no-op cases (FR-11 and FR-13).
- [x] 3.2 Define Detail's viewport content rectangle from the same panel/footer/frame calculations used by `View` and `resize`, and update `detail.Model.Update` to accept only an eligible plain vertical wheel translated into a three-line viewport movement; consume all Detail clicks and unsupported mouse events without forwarding them to the viewport (FR-11 and FR-13).
- [x] 3.3 Add root-level exclusion tests covering global help, command palette, delete/leave confirmation, active workflow filtering, and the Detail overlay. For each state, place the pointer both inside and outside the visible overlay and assert no underlying Home/Monitor focus, selection, scroll, or action command changes; assert Detail's content wheel is the sole exception (FR-12 through FR-14).
- [x] 3.4 Update root mouse precedence in `internal/tui/root_update.go` so global help, palette, and confirmations consume mouse input before active-screen dispatch; route Home Detail events only to Detail; otherwise route supported mouse messages to Home or Monitor without changing the ordering or handling of resize, engine, clipboard, or navigation messages (FR-12 and FR-14).
- [x] 3.5 Add Monitor exclusion tests for helpchat, notification diagnostics/global help overlays as applicable, open review workspace, focused open Gate, transcript search, question/custom-answer input, prompt/recovery editors, and other active text capture. Assert a pending but closed/unfocused Gate still allows ordinary visible-panel navigation (FR-12).
- [x] 3.6 Centralize Monitor mouse eligibility ahead of generic non-key dispatch so excluded mouse input is consumed rather than delivered to helpchat, review, search, textarea, Gate, or a focus-selected viewport; preserve all non-mouse messages and the current keyboard focus/input paths (FR-12 and FR-14).
- [x] 3.7 Complete cross-surface malformed/unsupported-input tests for motion, release, secondary buttons, horizontal wheels, modifiers, negative coordinates, coordinates at or beyond terminal bounds, zero-sized regions, and stale pre-resize panel positions; assert no panic, mutation, command, or hidden-panel addressing (FR-13).
- [x] 3.8 Run focused Detail/root/Monitor exclusion tests plus the existing keyboard focus-cycle, filtering, help/palette, review-anchor/draft, Gate-input, and engine-event tests to prove mouse routing has not become a second scheduler or input owner (FR-14).
- [x] 3.9 Record `artifacts/25-3-detail-input-isolation.txt` with sanitized Detail list/chart wheel evidence, keyboard close, and unchanged underlying Home state, plus an overlay/text-capture pass-through rejection example (FR-11 through FR-14).

### [x] 4.0 Document and prove the complete keyboard-primary mouse contract

Document the supported click and wheel surfaces, the distinction between
selection and activation, list-selection versus content-scrolling behavior,
and the deliberately excluded surfaces without adding configuration or
replacing contextual keyboard hints. Run the focused and repository-wide
quality gates and collect sanitized, reproducible evidence for the three
demoable units. Covers FR-15 and verifies the cross-cutting preservation
requirements in FR-01 and FR-14.

#### 4.0 Proof Artifact(s)

- Documentation: `docs/TUI.md` contains a discoverable mouse-navigation section that names all five supported surfaces, primary-click behavior, three-row/line vertical wheel behavior, keyboard-focus preservation, and excluded surfaces, demonstrating FR-15 without presenting mouse as required or configurable.
- Test output: `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-4-focused-tests.txt` records passing focused mouse, keyboard-parity, geometry, overlay, follow-state, and documentation-contract tests from `go test ./internal/tui/...` using only synthetic fixtures.
- Regression output: `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-4-regression-checks.txt` records the actual outcomes of `go test -race ./internal/tui/...`, `go build ./cmd/jig`, `go test ./...`, and `go vet ./...`, with any environmental blocker identified rather than reported as passing.
- Review evidence: `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-4-scope-review.txt` records `git diff --check`, the final changed-file inventory, and sanitized artifact/content scans, demonstrating that the three terminal captures and final diff contain no raw `.jig/` run data, credentials, private identifiers, dependency upgrades, mouse settings, workflow-schema changes, or new mouse-driven activation paths.

#### 4.0 Tasks

- [x] 4.1 Add a failing documentation contract test in `internal/tui/mouse_documentation_contract_test.go` that reads `docs/TUI.md` and requires discoverable guidance for Workflows, Runs, Steps, Transcript, and Detail; primary-click selection without activation; three-row list and three-line content wheels; pointer-targeted focus preservation; excluded modal/text-input surfaces; and keyboard-primary/no-configuration wording (FR-15).
- [x] 4.2 Update `docs/TUI.md` with a concise mouse-navigation section satisfying the contract test while preserving contextual keyboard help as the primary in-app guidance and avoiding claims of mouse configuration, activation, or unsupported surfaces (FR-01 and FR-15).
- [x] 4.3 Review all changed production and test files against the spec's non-goals: remove any mouse preference/flag/schema field, dependency upgrade, hover/drag/double-click/activation behavior, engine/backend/transcript-format change, or duplicate selection/scroll model introduced by the implementation (FR-01 and FR-14).
- [x] 4.4 Format only changed Go files with `gofmt -w <changed-go-files>`, run `go test ./internal/tui/...`, and save exact passing/failing output with toolchain details to `artifacts/25-4-focused-tests.txt`; do not include local paths, raw runs, credentials, or private identifiers in the committed artifact (FR-14 and FR-15).
- [x] 4.5 Run `go test -race ./internal/tui/...`, `go build ./cmd/jig`, `go test ./...`, and `go vet ./...`; save exact outcomes to `artifacts/25-4-regression-checks.txt`, distinguishing an assertion failure, environment/toolchain blocker, and intentional skip rather than describing an unrun check as passing (FR-14).
- [x] 4.6 Verify the three terminal-capture artifacts are reproducible from synthetic state, include 120x35 and 60x24 evidence where required, show mouse interaction followed by keyboard continuation, and contain no `.jig/` content, secrets, credential-shaped values, or private identifiers (FR-01, FR-05 through FR-11, FR-14, and FR-15).
- [x] 4.7 Review the final diff for scope and ownership, confirm every FR-01 through FR-15 has its mapped automated/manual evidence, run `git diff --check`, inventory the changed paths, scan the committed proof artifacts for seeded sensitive values, and record those results in `artifacts/25-4-scope-review.txt`; leave `docs/plans/open-goals.md` unchanged until validation proves implementation completion.
