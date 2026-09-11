# 24-spec-fuzzy-command-palette.md

## Introduction/Overview

jig's TUI has a `ctrl+k` command palette (`internal/tui/palette`) that lists the active screen's key bindings and lets the operator filter them with a plain substring match, then re-dispatches the matching keypress on Enter. `docs/plans/open-goals.md` (B3) asks for this to grow into "richer named actions" with `fzf`-style fuzzy filtering. Today the palette can only surface commands that already have a single-key binding on the current screen, and its filtering is an unranked, order-preserving substring scan. This feature upgrades the palette's matching to real fuzzy ranking with match highlighting, and adds a small set of new commands that don't correspond to an existing single-key binding, using a direct-execution path alongside the existing key-redispatch path.

## Goals

- Replace the palette's substring filter with fuzzy ranking (best match first) and inline highlighting of the matched characters, matching the `fzf`/`k9s` feel referenced in the originating goal.
- Extend the fuzzy match text to include each command's section/category label, not just its title and key binding, so typing a category name surfaces all commands in that category.
- Add a direct-execution path (`Run func() tea.Cmd`) to the palette's `Command` type so a palette entry can perform an action that has no corresponding single keypress on the current screen.
- Ship two concrete new commands on that path: "Go to Home" (from anywhere in Monitor) and "Go to Monitor" (from anywhere in Home, for the currently-selected run).
- Lock in today's implicit guarantee — that the palette catalog reflects every reachable Home/Monitor sub-state — with an explicit regression test, so a future change to `homeHelpSections`/`helpSections` can't silently drop a state's commands from the palette.

## User Stories

- **As an operator driving jig with the keyboard**, I want the `ctrl+k` palette to rank results by how well they match what I typed (and show me why they matched) so that I can find a command in a few keystrokes instead of scanning an unranked list.
- **As an operator**, I want to search the palette by category (e.g. "run", "step") as well as by command name, so that I can discover related commands even when I don't remember the exact title.
- **As an operator deep in the Monitor's Transcript or a gate focus**, I want a single palette command that jumps straight back to Home, so that I don't have to first return to the Steps focus and then leave.
- **As an operator on Home**, I want a single palette command that opens the Monitor for the run I currently have selected, so that I don't have to focus the Runs pane and press Enter first.
- **As a maintainer of the TUI**, I want a test that fails if a future refactor of `homeHelpSections` or `helpSections` accidentally drops a reachable sub-state's bindings from the palette catalog, so that palette coverage doesn't silently regress.

## Demoable Units of Work

### Unit 1: Fuzzy ranking and category-aware matching

**Purpose:** Turn the palette's flat substring filter into the ranked, highlighted fuzzy-match experience the originating goal asks for, using the category label as additional match text.

**Functional Requirements:**
- The system shall rank visible palette commands by fuzzy match score against the current filter text, best match first, replacing the existing fixed catalog order while a filter is active.
- The system shall include each command's section/category label (the `prefix` passed when the command was built) as part of the text a filter is matched against, in addition to the existing title and key-binding text.
- The system shall visually highlight, within each visible row's rendered title, the specific characters that matched the current filter text.
- The system shall fall back to the full catalog in its original (unranked) order when the filter text is empty, matching today's behavior.
- The system shall continue to omit commands whose underlying binding is disabled, exactly as today.

**Proof Artifacts:**
- Test: `internal/tui/palette` unit tests demonstrate that a filter query returns matches ordered by fuzzy score (not catalog order) and that a query matching only a category label still returns the expected commands.
- Screenshot: the palette open in the TUI with a partial query typed, showing highlighted matched characters within at least one visible row, demonstrates the highlighting behavior end-to-end.

### Unit 2: Direct-execution commands — "Go to Home" and "Go to Monitor"

**Purpose:** Deliver the first genuinely new named actions the palette can offer: one-step cross-screen jumps that today require navigating to a specific focus state first.

**Functional Requirements:**
- The system shall support a palette command that executes a direct action (not tied to re-dispatching a single keypress) alongside the existing key-redispatch path, without breaking any existing key-redispatch command.
- The system shall offer a "Go to Home" command in the Monitor's palette catalog, reachable regardless of the Monitor's current focus (Steps, Transcript, or an open gate), that returns to Home using the same navigation path as Monitor's existing leave behavior (including the unsaved-review-compose confirmation when applicable).
- The system shall offer a "Go to Monitor" command in the Home palette catalog that opens the Monitor for the currently-selected run in the Runs pane, using the same navigation path as pressing Enter on that run today.
- The system shall omit the "Go to Monitor" command from the palette catalog when Home has no runs, or no run is currently selected.

**Proof Artifacts:**
- Test: `internal/tui` unit tests demonstrate that invoking "Go to Home" from a non-Steps Monitor focus and invoking "Go to Monitor" for the selected run both produce the expected screen transition.
- Screenshot or terminal capture: the `ctrl+k` palette open on Monitor showing "Go to Home" listed, and on Home showing "Go to Monitor" listed for a selected run, demonstrates the new commands are discoverable.

### Unit 3: Palette catalog parity regression tests

**Purpose:** Convert today's implicit "the palette always reflects the active screen's reachable keybindings" guarantee into an explicit, tested contract, since tracing the code found no actual coverage gap to close.

**Functional Requirements:**
- The system shall have table-driven tests asserting that `homeHelpSections()` (via the Home palette catalog) returns the expected set of commands for each reachable Home sub-state: Workflows focus, Runs focus, and the Detail overlay.
- The system shall have table-driven tests asserting that `PaletteSections()` (Monitor's full, simple-mode-independent catalog) returns the expected set of commands for each reachable Monitor focus/gate combination: Steps focus, Transcript focus (chat and file selection), and each gate `entry.kind` (request, question, review — including review-open with an active workspace).
- The system shall run these tests as part of the existing `go test ./...` suite with no new build tags or manual steps.

**Proof Artifacts:**
- Test: `go test ./internal/tui/... -run TestPalette` (or equivalent) output showing the new parity tests passing demonstrates the coverage guarantee is now explicit and enforced.

## Non-Goals (Out of Scope)

1. **New top-level screens or palette surfaces beyond Home and Monitor**: no other root screen exists today (`internal/tui/root.go`), so there is nothing else to wire the palette into.
2. **Closing a "missing screen" coverage gap**: round 2 of clarification traced the code and found `ctrl+k` already runs on every reachable Home/Monitor sub-state, including the review-open workspace; there is no gap to close, only a guarantee to make explicit (Unit 3).
3. **A "Toggle simple/advanced mode" or "Show help" palette command**: both already exist and are already included in the palette catalog today; they are unaffected by this spec.
4. **Any new palette commands beyond "Go to Home" and "Go to Monitor"**: no other cross-screen or free-standing action was identified as missing during clarification.
5. **Changing the palette's visual layout, box sizing, or keybindings for opening/closing/navigating it** (`ctrl+k`, arrows, Enter, Esc) — only the ranking, match text, highlighting, and command catalog change.
6. **A `:`-style command-line/argument syntax** — the palette remains a pure list-and-select overlay, consistent with the existing "ctrl+k only, no `:`" design note in `internal/tui/shared/keys.go`.

## Design Considerations

No specific new design requirements identified. The palette keeps its existing centered-overlay layout (`internal/tui/palette/palette.go` `View`); match highlighting should reuse an existing theme token from `internal/tui/shared` (e.g. a variant of `theme.SelectedLine`/`theme.Help.Key`) rather than introducing a new hardcoded style, per the TUI styling conventions in `CLAUDE.md`.

## Repository Standards

- Follow the existing `internal/tui/palette` package structure: `Command`, `Model`, `refilter`, and `FromBindings` are the extension points; add the direct-execution field and fuzzy-ranking logic there rather than introducing a parallel catalog mechanism.
- Follow the TUI styling convention in `CLAUDE.md`: any new highlight style is added to the `Styles` struct in `internal/tui/styles.go`, derived from the existing semantic color tokens, and referenced as `theme.X` — never a bare hardcoded color.
- Follow the repository's table-driven testing convention (`internal/workflow/workflow_test.go` is the reference style) for the new fuzzy-matching and parity-regression tests.
- A new/changed schema-visible behavior is not introduced by this feature, so no `docs/workflow-schema.md` changes are required.

## Technical Considerations

- **Fuzzy matching library**: use `github.com/sahilm/fuzzy` (already an indirect dependency via `charm.land/bubbles/v2`'s own list filtering, per `go.mod`/`go.sum`). It returns both a match score and matched rune indexes per candidate in one call, which covers both the ranking and highlighting requirements without a new dependency. Promote it from an indirect to a direct dependency in `go.mod`.
- **`Command` type change**: add a `Run func() tea.Cmd` field to `palette.Command` (`internal/tui/palette/palette.go:19-25`) alongside the existing `Key string` field. `Model.Update`'s `"enter"` case (`palette.go:82-88`) should prefer `Run` when set, falling back to the existing `DispatchKey(cmd.Key)` path otherwise, so existing key-redispatch commands are unaffected.
- **Match text construction**: `FromBindings` (`palette.go:196-221`) already receives the section `prefix` at catalog-build time; extend the per-command match text (used by `refilter`) to include it without changing the row's rendered `Title`/`Binding` display.
- **New commands' plumbing**: "Go to Home" should reuse Monitor's existing `leaveMonitor()` path (`internal/tui/monitor/monitor_model.go`), which already returns `RequestLeaveConfirmMsg` when a review compose buffer is dirty, or `ShowHomeMsg` otherwise. "Go to Monitor" should reuse the existing `runs.ShowMonitorMsg{RunID}` → `rootModel.openMonitor(runID)` path (`internal/tui/root_update.go:112-113,386+`). Both commands are built where each screen's palette catalog is assembled today (`rootModel.homeHelpSections()` / `monitor.Model.helpSections()`), not inside the `palette` package itself, since only the owning screen has the context (selected run ID, current focus) needed to construct the `tea.Cmd`.
- **Selected-run lookup**: `runs.Model` does not currently expose the cursor's selected run ID publicly; a small accessor will likely be needed on `runs.Model` to let `rootModel` build the "Go to Monitor" command and decide whether to omit it (no runs / no selection).
- **Parity tests**: exercise `homeHelpSections()` and `helpSections(false)`/`PaletteSections()` directly with constructed `rootModel`/`monitor.Model` fixtures for each sub-state, asserting on section titles and binding help text rather than rendered strings, to keep the tests resilient to display-only changes.

## Security Considerations

No specific security considerations identified. This feature only changes in-process TUI navigation and local filtering/ranking of already-visible commands; it does not touch credentials, network calls, or persisted data.

## Success Metrics

1. **Ranking correctness**: for a representative set of filter queries against the existing catalog, the top-ranked result in each case is the command a human would consider the best match (verified via the Unit 1 tests).
2. **Coverage guarantee**: the Unit 3 parity tests pass and cover every enumerated Home sub-state (Workflows, Runs, Detail overlay) and Monitor focus/gate combination (Steps, Transcript × {chat, file}, and each gate `entry.kind` including review-open).
3. **No regression in existing palette behavior**: all existing `internal/tui/palette` and `internal/tui` tests continue to pass after the `Command` type change.

## Open Questions

1. Exact highlight styling (e.g. bold vs. a distinct foreground color, and how it composes with `theme.SelectedLine` on the cursor row) is left to implementation-time judgment within the existing `theme.Chat`/`theme.Help` token set; it does not change scope or acceptance criteria.
2. The precise name/signature of the new `runs.Model` selected-run accessor (Technical Considerations) is an implementation detail to be resolved during task planning, not a scope decision.
