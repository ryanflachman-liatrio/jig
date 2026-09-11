# tasks-charm-clickable.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/root.go` | `rootModel.View()` sets `v.MouseMode = tea.MouseModeCellMotion` alongside the existing `AltScreen`/`BackgroundColor`. |
| `internal/tui/root_test.go` | Root-model tests: `MouseMode` assertion, click-to-open happy path (independent of keyboard selection), and the full no-op table (non-primary button, release/wheel/motion, out-of-bounds, filtering, Detail already open, Monitor active). |
| `internal/tui/root_update.go` | Root's `Update` switch: `tea.MouseMsg` is dispatched to `updateHome` only when `m.active == screenHome`; a no-op otherwise (Monitor/other root surfaces). |
| `internal/tui/home.go` | `homeLayout()` is the new single source of truth for the narrow/wide split (replacing duplicated math previously inline in `homeView`/`sizeHomeChildren`); `updateHomeMouse` translates a root-level click into workflow-pane-local coordinates and calls `m.selector.SelectItemAt`, moving the keyboard cursor to the clicked row — it does **not** open Detail; only the `d` key does that. |
| `internal/tui/selector/model.go` | `Model` gained `itemHeight`/`itemSpacing` fields (copied from the delegate at construction) so hit-testing's row math can never drift from what the list actually renders. |
| `internal/tui/selector/hit.go` (new) | `itemAtPoint(x, y int) (int, workflowItem, bool)` is the shared hit-testing seam, mapping a pane-local point to a visible-window item's global list index and value. `Model.ItemAt(x, y) (string, bool)` (path lookup, used by tests) and `Model.SelectItemAt(x, y) (Model, bool)` (moves the list's keyboard cursor via `list.Model.Select`) both compose it. |
| `internal/tui/selector/hit_test.go` (new) | Table-driven geometry tests: unfiltered, pagination, filtering, resize, Unicode width, boundary rows, a two-width render fixture, and a bounded `FuzzItemAt`. |
| `internal/tui/shared/panel.go` | Added `PanelContentOrigin()` — the pane-local (x, y) of the first content cell inside a `Panel`-rendered pane, reused by `ItemAt` instead of re-deriving border/padding offsets. |
| `charm.land/bubbles/v2/list` (vendor, read-only) | `VisibleItems()`, `Paginator.GetSliceBounds`, `ItemDelegate.Height()/Spacing()` — the existing primitives `ItemAt` composes; confirmed empirically (not just from reading source) that the list always reserves one title/filter row and one pagination row regardless of state, which is why item rows start 2 cells below the panel's top-left content origin. |
| `internal/tui/runs/*.go` | Confirmed (grep, no `MouseMsg` references) to have no mouse handling of its own, so clicks landing in the Runs pane rectangle correctly no-op via bounds checking rather than needing a special case. |

### Notes

- Unit tests live alongside the code they test, following the repository's existing pattern.
- `go test ./internal/tui/selector/... -v` and `go test ./internal/tui/... -run TestHomeMouse -v` for targeted runs; `go test -race ./internal/tui/...` for the final race pass.
- **Implementation-time refinement (user-confirmed, superseded — see below):** the spec's original wording ("same detail view as pressing Enter") referred to the *standalone* selector's `Open`/Enter binding, which emits `selector.ShowDetailMsg`. Inside the actual embedded Home screen, keyboard Enter on a workflow row focuses the Runs pane, not Detail — only the `d` key opens Detail. Asked the user which behavior a click should match; the user first confirmed a click should open the Detail overlay (matching `d`). This was implemented as `ItemAt` returning a path and `updateHomeMouse` calling `m.openDetailOverlay` directly, mirroring the `d`-key handler.
- **Post-implementation revision (user-directed):** after seeing the click-opens-Detail behavior in practice, the user asked for it to be changed: a click on a row that is not currently keyboard-selected should *select* that row (the same as `j`/`k`), not jump straight to Detail; Detail-opening stays exclusively on the `d` key. Reworked accordingly: `hit.go` now factors the shared geometry into `itemAtPoint`, `ItemAt` (path lookup, still used by `hit_test.go`) is unchanged in behavior, and a new `Model.SelectItemAt(x, y) (Model, bool)` moves the list's keyboard cursor via `list.Model.Select(idx)`. `updateHomeMouse` calls `SelectItemAt` and no longer calls `openDetailOverlay`. The standalone `selector.ShowDetailMsg`/root `case selector.ShowDetailMsg` path is unchanged and still exists for driving the selector outside Home (e.g. tests) — it was never wired to the mouse path in either revision.

## Tasks

### [x] 1.0 Deliver mouse input through the root view

#### 1.0 Proof Artifact(s)

- Test: `TestRootViewMouseModeCellMotion` (`internal/tui/root_test.go`) asserts `rootModel.View().MouseMode == tea.MouseModeCellMotion` on Home and with the Detail overlay open.
- Test: `TestHomeMouseClickNoOpCases/while_Monitor_is_active` and `.../while_Detail_overlay_is_already_open` demonstrate a `tea.MouseMsg` does not reach selector navigation outside Home.
- CLI: `go test ./internal/tui/... -run 'TestRootViewMouseModeCellMotion|TestHomeMouseClick' -v` — see `docs/specs/charm-clickable/proofs/task-1-proofs.md`.

#### 1.0 Tasks

- [x] 1.1 Set `v.MouseMode = tea.MouseModeCellMotion` in `rootModel.View()` (`internal/tui/root.go`).
- [x] 1.2 Add `case tea.MouseMsg:` to `rootModel.Update` (`internal/tui/root_update.go`) dispatching to `updateHome` only when `m.active == screenHome`.
- [x] 1.3 Add `case tea.MouseMsg:` to `updateHome`'s switch (`internal/tui/home.go`) routing to `updateHomeMouse`, gated only on `!m.showDetailOverlay` (not on `homeFocus`, since a click must work regardless of pane focus).
- [x] 1.4 `TestRootViewMouseModeCellMotion`.
- [x] 1.5 `TestHomeMouseClickNoOpCases` subtests for Detail-open and Monitor-active.
- [x] 1.6 Ran `go test ./internal/tui/... -run 'TestRootViewMouseModeCellMotion|TestHomeMouseClick' -v` — all pass (proofs file).

### [x] 2.0 Select a workflow row from a valid visible row click

#### 2.0 Proof Artifact(s)

- Test: `TestItemAtClickIsIndependentOfKeyboardSelection`, `TestItemAtPagination`, `TestItemAtFiltered` (`internal/tui/selector/hit_test.go`) — a click resolves by rendered row, not keyboard cursor.
- Test: `TestHomeMouseClickSelectsRowWithoutOpeningDetail` and `TestHomeMouseClickSelectsRowNotKeyboardSelection` (`internal/tui/root_test.go`) — full click → selection-only path (`SelectedPath()` updates to the clicked row; Detail overlay stays closed), including for a row other than the current keyboard selection.
- Test: `TestHomeMouseClickNoOpCases/while_filtering` — click during active filtering is a no-op.
- CLI: `go test ./internal/tui/... ./internal/tui/selector/...` passes — see proofs file.

#### 2.0 Tasks

- [x] 2.1 Added `Model.ItemAt(x, y int) (string, bool)` and `Model.SelectItemAt(x, y int) (Model, bool)` (`internal/tui/selector/hit.go`), sharing geometry via `itemAtPoint`.
- [x] 2.2 Added `homeLayout()` (`internal/tui/home.go`) as the single source of truth for pane rectangles; `updateHomeMouse` translates root coordinates into workflow-pane-local coordinates and bounds-checks against it.
- [x] 2.3 A valid `SelectItemAt` hit moves the list's keyboard cursor via `list.Model.Select(idx)` and does **not** call `m.openDetailOverlay` (revised post-implementation per user direction — see the note above; Detail-opening is exclusively the `d` key).
- [x] 2.4 Guarded with `m.selector.CapturesText()` in `updateHomeMouse`.
- [x] 2.5 `TestItemAtClickIsIndependentOfKeyboardSelection`, `TestItemAtPagination`, `TestItemAtFiltered`.
- [x] 2.6 `TestHomeMouseClickSelectsRowWithoutOpeningDetail`, `TestHomeMouseClickSelectsRowNotKeyboardSelection`.
- [x] 2.7 Ran `go test ./internal/tui/... ./internal/tui/selector/...` — all pass, including pre-existing `selector_test.go`/`root_test.go`.

### [x] 3.0 Keep hit regions aligned with rendered selector geometry

#### 3.0 Proof Artifact(s)

- Test: `TestItemAtUnfiltered`, `TestItemAtFiltered`, `TestItemAtPagination`, `TestItemAtResize`, `TestItemAtUnicodeWidth` (`internal/tui/selector/hit_test.go`).
- Test: `TestItemAtBoundaryRows` — row-above/row-below exclusion.
- Test: `FuzzItemAt`, run for 10s / ~280K executions with no panics or out-of-window results (proofs file).
- Fixture: `TestItemAtFixedSizeFixture` at widths 40 and 70.
- CLI: `go test ./internal/tui/selector/... -v` passes — see proofs file.

#### 3.0 Tasks

- [x] 3.1 `homeLayout()` (Home) and `shared.PanelContentOrigin()`/`PanelFrame()` (shared) are the single shared layout sources; `ItemAt` reuses `PanelContentOrigin`/`PanelFrame` rather than a duplicated formula.
- [x] 3.2 `ItemAt` maps Y to a visible-window row via `list.VisibleItems()` + `Paginator.GetSliceBounds`, not the unfiltered/first-page items.
- [x] 3.3 Hit testing is vertical-only (Y-driven); X is only bounds-checked against `contentW` (derived from `shared.PanelFrame()`, which is already `lipgloss`-width-aware), never used to locate a row — confirmed by `TestItemAtUnicodeWidth` passing with wide CJK glyphs in both name and description.
- [x] 3.4 `ItemAt` excludes the panel title row, the list's own title/filter row, spacing rows between items, the pagination row, and trailing blank fill below the last item on a page — all confirmed empirically against real rendered output before being encoded (see `TestItemAtUnfiltered`/`TestItemAtFixedSizeFixture`).
- [x] 3.5 Table-driven tests for unfiltered/filtered/paginated/resized/Unicode fixtures.
- [x] 3.6 `TestItemAtBoundaryRows`.
- [x] 3.7 `FuzzItemAt` added and run for 10s (bounded to ±1000 cells).
- [x] 3.8 `TestItemAtFixedSizeFixture` at two widths.
- [x] 3.9 Ran `go test ./internal/tui/selector/... -v` — all pass.

### [x] 4.0 No-op safety and full regression verification

#### 4.0 Proof Artifact(s)

- Test: `TestItemAtNoOpCases` (selector) and `TestHomeMouseClickNoOpCases` (root) — negative-case tables for non-primary actions, out-of-bounds, loading/error/empty, filtering, Detail-open, and Monitor-active.
- Test: pre-existing `selector_test.go`/`root_test.go` suites pass unmodified.
- CLI: `go build ./cmd/jig && go test ./... && go test -race ./internal/tui/... && go vet ./... && gofmt -l .` all succeed, `gofmt -l .` reports no files — see proofs file.

#### 4.0 Tasks

- [x] 4.1 Only `tea.MouseClickMsg` with `Button == tea.MouseLeft` is accepted in `updateHomeMouse`; release/wheel/motion and other buttons are explicitly rejected and tested.
- [x] 4.2 `TestItemAtNoOpCases` covers negative/far-out-of-bounds coordinates, loading, error, and empty-selector states.
- [x] 4.3 `TestHomeMouseClickNoOpCases` covers Detail-already-open and Monitor-active; confirmed via grep that Runs defines no mouse handling of its own before asserting its bounds-check no-op.
- [x] 4.4 Full pre-existing `internal/tui/selector` and `internal/tui` suites re-run unmodified — all pass.
- [x] 4.5 Ran `gofmt -l -w .` (one file reformatted), `go vet ./...`, `go build ./cmd/jig`, `go test ./...`, `go test -race ./internal/tui/...` — all clean/pass.
- [x] 4.6 See `docs/specs/charm-clickable/proofs/task-4-proofs.md` for full command output.
