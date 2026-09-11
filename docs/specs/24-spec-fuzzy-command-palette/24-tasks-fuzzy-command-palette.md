# 24-tasks-fuzzy-command-palette.md

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/palette/palette.go` | Core `Command`/`Model` type; adds fuzzy ranking + match highlighting to `refilter`/`View`, and the `Run func() tea.Cmd` direct-execution field + dispatch in `Update`'s `"enter"` case. |
| `internal/tui/palette/palette_test.go` | Existing palette unit tests; extend with fuzzy-ranking, category-match, highlight, and `Run`-dispatch coverage. |
| `internal/tui/shared/styles.go` | Adds a new `Help.Match` (or similarly-named) style token, derived from existing semantic color tokens, for highlighting matched characters. |
| `internal/tui/root.go` | Owns `rootModel` and the `paletteCommands()` builder call site; no structural change expected but read for context when wiring new commands. |
| `internal/tui/root_update.go` | `paletteCommands()`, `handleGlobalKey`, `openMonitor()` — the existing navigation path the new "Go to Monitor" command reuses. |
| `internal/tui/home.go` | `homeHelpSections()` — add the "Go to Monitor" `palette.Command` here (or in a small helper it calls), since only Home has the Runs-pane selection context. |
| `internal/tui/monitor/monitor_model.go` | `helpSections()` / `PaletteSections()` / `leaveMonitor()` — add the "Go to Home" `palette.Command` here, reusing `leaveMonitor()`'s existing dirty-compose-aware navigation. |
| `internal/tui/runs/model.go` | Add a `SelectedID() (string, bool)` accessor mirroring the existing `Open`-key row-selection logic, needed by the "Go to Monitor" command and its omit-when-empty rule. |
| `internal/tui/root_test.go` | Add tests for the new "Go to Home"/"Go to Monitor" commands' navigation behavior and the Home-side palette catalog parity across Workflows/Runs/Detail-overlay sub-states. |
| `internal/tui/monitor/monitor_test.go` | Add tests for the Monitor-side palette catalog parity across Steps/Transcript(chat/file)/each gate `entry.kind` (including review-open), and for the new "Go to Home" command. |
| `internal/tui/runs/runs_test.go` | Add unit test(s) for the new `SelectedID()` accessor (empty list, valid cursor, out-of-range cursor). |
| `go.mod` | Promote `github.com/sahilm/fuzzy` from an indirect to a direct dependency (via `go mod tidy`) since `palette.go` now imports it directly. |

### Notes

- Unit tests live alongside the code they test, following the existing `_test.go` naming in `internal/tui`, `internal/tui/palette`, `internal/tui/monitor`, and `internal/tui/runs`.
- Run `go test ./internal/tui/...` (repository-standard test command per `AGENTS.md`) to execute the full TUI suite, or scope to a package/run-filter while iterating.
- Run `gofmt -l -w .` and `go vet ./...` before considering any task complete, per `AGENTS.md`.
- No new TUI style may hardcode a color — new tokens go in `internal/tui/shared/styles.go`'s `DefaultTheme()`, derived from existing semantic tokens, and are referenced as `shared.Theme.X` at call sites (per `CLAUDE.md` TUI styling conventions).
- Manual/visual proof artifacts (screenshots, terminal captures) should be taken by running `go run ./cmd/jig` against a workflow with at least one existing run, per the repository's normal manual-verification pattern for TUI changes.

## Tasks

### [x] 1.0 Fuzzy ranking and category-aware matching

#### 1.0 Proof Artifact(s)

- Test: `internal/tui/palette` unit tests demonstrate that a filter query returns matches ordered by fuzzy score (not catalog order) and that a query matching only a category label still returns the expected commands.
- Screenshot: the palette open in the TUI with a partial query typed, showing highlighted matched characters within at least one visible row, demonstrates the highlighting behavior end-to-end.

#### 1.0 Tasks

- [x] 1.1 Import `github.com/sahilm/fuzzy` directly in `internal/tui/palette/palette.go` and run `go mod tidy` so `go.mod` records it as a direct dependency.
- [x] 1.2 Add a `Category string` field to `palette.Command` and populate it in `FromBindings` from the existing `prefix` argument, without changing the rendered `Title`/`Binding` display.
- [x] 1.3 Rewrite `Model.refilter` to build a per-command match-source string (title + binding + category) for every command in `m.commands`, call `fuzzy.Find(query, sources)`, and set `m.visible` from the returned matches in score order (best first); keep the existing full-catalog-in-original-order behavior when the filter is empty.
- [x] 1.4 Add a new highlight style token (e.g. `Help.Match`) to the `Styles` struct and `DefaultTheme()` in `internal/tui/shared/styles.go`, derived from an existing semantic color token (e.g. `secondary` or `primary`), not a new hardcoded color.
- [x] 1.5 Extract a small helper (e.g. `highlightMatches(title string, indexes []int) string`) that wraps the runes at the given matched indexes in the new highlight style, and call it from `Model.View`'s row-rendering loop using each `fuzzy.Match`'s `MatchedIndexes`, preserving existing padding/truncation and the selected-row style.
- [x] 1.6 Extend `internal/tui/palette/palette_test.go` with table-driven cases: (a) a query where the best fuzzy match is not the first catalog entry, asserting ranked order; (b) a query matching only a command's category/prefix text; (c) an empty filter falling back to original catalog order; (d) a disabled command remains excluded regardless of match score; (e) `highlightMatches` wraps exactly the given rune indexes for a sample title, asserting on the rendered output.
- [x] 1.7 Run `go run ./cmd/jig`, open `ctrl+k`, type a partial query, and capture a screenshot showing ranked results with highlighted matched characters as the manual proof artifact. (No interactive terminal capture tool is available in this environment; captured an equivalent `View()` frame built from the real `palette.Model` production code — see `artifacts/palette-demo.txt`, following the same substitution precedent as `docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit3-nav.txt`.)

### [x] 2.0 Direct-execution commands — "Go to Home" and "Go to Monitor"

#### 2.0 Proof Artifact(s)

- Test: `internal/tui/palette` unit test demonstrates `Model.Update` dispatches a command's `Run` when set and falls back to `DispatchKey` when it is nil.
- Test: `internal/tui` unit tests demonstrate that invoking "Go to Home" from a non-Steps Monitor focus and invoking "Go to Monitor" for the selected run both produce the expected screen transition.
- Screenshot or terminal capture: the `ctrl+k` palette open on Monitor showing "Go to Home" listed, and on Home showing "Go to Monitor" listed for a selected run, demonstrates the new commands are discoverable.

#### 2.0 Tasks

- [x] 2.1 Add a `Run func() tea.Cmd` field to `palette.Command` in `internal/tui/palette/palette.go`.
- [x] 2.2 Update `Model.Update`'s `"enter"` case so that when the selected command's `Run` is non-nil, the palette hides and returns `Run()` directly; otherwise fall back to the existing `DispatchKey(cmd.Key)` path unchanged.
- [x] 2.3 Add `SelectedID() (string, bool)` to `internal/tui/runs/model.go`, mirroring the existing `Open`-key logic in `update.go` (`visibleRows()[cursor].id`), returning `("", false)` when there are no visible rows or the cursor is out of range.
- [x] 2.4 In `internal/tui/home.go`'s `homeHelpSections()`, append a "Go to Monitor" `palette.Command` (with `Run` returning `func() tea.Msg { return runs.ShowMonitorMsg{RunID: id} }`) to the Global/Runs section only when `m.runs.SelectedID()` returns a valid ID; omit it otherwise. (Implemented via a new `shared.PaletteExtra`/`HelpSection.Extras` field rather than a bare `palette.Command`, since `shared` cannot import `palette` — `palette` already imports `shared` for styling, and that would be an import cycle. `rootModel.paletteCommands()` in `root_update.go` converts `Extras` into real `palette.Command` values.)
- [x] 2.5 In `internal/tui/monitor/monitor_model.go`'s `helpSections()`, add a "Go to Home" `palette.Command` (with `Run` reusing `m.leaveMonitor()`'s existing message, i.e. `RequestLeaveConfirmMsg` when a review compose buffer is dirty, otherwise `ShowHomeMsg`) to a section that is included regardless of the current `focus`/gate branch. (Same `shared.PaletteExtra` mechanism as 2.4; attached to the always-appended "Global" section.)
- [x] 2.6 Verify `palette.FromBindings`'s key-redispatch commands are unaffected (no `Run` set) and that `Command.ID` values for the two new commands don't collide with any existing ID in the same catalog. (`TestPaletteCommandIDsAreUnique`.)
- [x] 2.7 Add a test in `internal/tui/palette/palette_test.go` asserting `Model.Update`'s `"enter"` case invokes a command's `Run` when it is set (without emitting a `DispatchKey` message), and falls back to the existing `DispatchKey(cmd.Key)` behavior when `Run` is nil — covering the direct-execution/key-redispatch coexistence at the palette-package level.
- [x] 2.8 Add tests in `internal/tui/root_test.go` and/or `internal/tui/monitor/monitor_test.go` covering: "Go to Home" invoked while Monitor's focus is Transcript (and while a gate is open) transitions to Home, including the dirty-compose confirmation path when applicable; "Go to Monitor" invoked from Home opens the Monitor for the currently-selected run; "Go to Monitor" is absent from the catalog when Home has no runs or no run selected.
- [x] 2.9 Run `go run ./cmd/jig` with at least one existing run, open `ctrl+k` on Monitor to confirm "Go to Home" is listed and works from a non-Steps focus, then on Home to confirm "Go to Monitor" is listed for the selected run — capture the manual proof artifact. (Same `View()`-frame substitution as 1.7; see `artifacts/palette-demo.txt`.)

### [x] 3.0 Palette catalog parity regression tests

#### 3.0 Proof Artifact(s)

- Test: `go test ./internal/tui/... -run TestPalette` (or equivalent) output showing the new parity tests passing demonstrates the coverage guarantee is now explicit and enforced.

#### 3.0 Tasks

- [x] 3.1 Add a table-driven test in `internal/tui/root_test.go` asserting `homeHelpSections()` returns the expected section titles and binding help text for each reachable Home sub-state: Workflows focus, Runs focus, and the Detail overlay.
- [x] 3.2 Add a table-driven test in `internal/tui/monitor/monitor_test.go` asserting `PaletteSections()` (`helpSections(false)`) returns the expected section titles and binding help text for each reachable Monitor state: Steps focus, Transcript focus with chat selection, Transcript focus with file selection, and each gate `entry.kind` (request, question, review-not-open, review-open with an active workspace). (Landed in a new `internal/tui/monitor/palette_extra_test.go` rather than `monitor_test.go`, to keep the new palette-catalog test surface together.)
- [x] 3.3 In both parity tests, assert the new "Go to Monitor" (Home) and "Go to Home" (Monitor) commands from Task 2.0 appear in the expected catalog states and are correctly omitted where specified (e.g. "Go to Monitor" absent with no runs).
- [x] 3.4 Run `go test ./internal/tui/... -v` and capture the passing output for the new parity tests (and the full suite) as the proof artifact.
