# TUI engineering

Read for changes to `internal/tui`, its child packages, or `internal/helpchat`.
The root opens Home (workflows and runs) and then Monitor. Detail/chart is a
Home overlay. Standalone chat and helpchat have their own agent interactions;
they are not the source of Monitor transcript truth.

## Ownership and message flow

Use the Elm-style model/update/command boundary: messages enter `Update`, the
returned model owns the next state, commands perform effects and return
messages, and `View` renders that state. Never mutate a model from a command
closure or a background goroutine. Capture immutable inputs and send an
identified result back to `Update`.

Keep blocking work out of `Update` and `View`: engine waits, filesystem reads,
subprocesses, clipboard operations, and network/backend calls belong in
commands or existing asynchronous services. Re-arm stream subscriptions
through messages and handle channel closure. Results that can arrive after a
run switch, resize, or reset need enough identity to reject stale work.

`tea.Batch` does not guarantee command order. Use `tea.Sequence` for ordered
effects; use a result message when the next action depends on the previous
result being applied to model state. Verify these semantics in the installed
[Bubble Tea API](https://pkg.go.dev/charm.land/bubbletea/v2).

The root composes child models and owns global overlays. Child screens must not
import `internal/tui`. Reuse `internal/tui/shared` for common presentation;
keep review domain/persistence logic in `internal/review` and datastore.

## Charm v2 contract

Use the `charm.land/{bubbletea,bubbles,lipgloss,glamour}/v2` dependencies pinned
in `go.mod`, and inspect the installed APIs before copying examples.

- A model implementing `tea.Model` returns `tea.View`; child rendering helpers
  may return strings. Root declares `AltScreen` and `BackgroundColor` on the view.
- Handle key presses with `tea.KeyPressMsg`. Tests construct `Code`, `Text`, and
  `Mod` as needed; old v1 `Type`/`Runes` snippets are not compatible.
- Terminal features belong to the owning root view; avoid competing terminal
  readers or direct stdout writes while Bubble Tea owns the terminal.

These are v2 API requirements, documented in the
[official migration guide](https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md).
The following presentation choices are jig-specific.

## Theme and layout

`internal/tui/shared/styles.go` owns `Styles`, `DefaultTheme`, and the `Theme`
singleton. The current theme is dark-only. Add semantic tokens/styles there;
do not introduce local color palettes, terminal-background probing, or runtime
theme mutation from worker goroutines. Use shared panel, Markdown, input, and
help helpers instead of independently rebuilding them in screens.

Measure terminal cells, not bytes or rune counts. Use Lip Gloss frame sizes
and the existing ANSI-aware width/truncation helpers. Account for borders,
padding, footer, status, and gate bar exactly once. Clamp inner dimensions at
zero, handle resize before the first normal frame, and exercise very small
terminals as well as narrow/wide layouts.

A panel frames caller-sized content; it does not own scrolling or wrapping.
Titles are composited into borders using the shared panel primitive. The root
canvas fills the viewport; the global footer stays outside panel borders.
Resize must rebuild width-dependent Glamour renderers and invalidate affected
caches while preserving meaningful cursor and scroll state.

## Focus and keyboard interaction

Focus determines who receives input; selection identifies a row/document.
Route an input event to its owner once. Respect text capture before global
navigation shortcuts, including pasted Unicode and multiline input. Use the
shared key/help/palette definitions so the footer advertises actions that are
actually available. Keep keyboard paths for mouse-accessible actions.

A pending Gate does not freeze the rest of Monitor. The input queue retains
all pending entries; an empty gate bar is not a focus target. Esc/back should
close the nearest transient surface and restore focus predictably. Do not turn
ordinary navigation into run cancellation. A focused review uses the Monitor
body as its workspace while retaining run status, gate bar, and footer.

Show focus and status with text/markers as well as color. Preserve readable
empty, loading, disabled, error, and unknown states. Keep actions discoverable
through help and the palette, and avoid rendering backend-specific jargon
unless the operator needs it to act.

### Mouse navigation

jig remains keyboard-primary: mouse navigation is optional, requires no configuration,
and does not replace contextual keyboard help. A plain primary
click selects and focuses a visible row in Home's Workflows or Runs list and
Monitor's Steps list, but it does not activate, open, start, resume, delete, or
otherwise execute that row. A primary click in Monitor's Transcript body
focuses Transcript, moves the block cursor to the clicked row, and, when
that row is expandable, toggles its expansion state — the same view state
change `enter`/`space` performs from the keyboard. Tool group headers,
compact-group child cards, standalone tool exchanges, oversized text
messages, and oversized reasoning rows are expandable; short text,
reasoning under the collapse threshold, boundary banners, per-turn metadata
rows, blank spacers, and file view are not. A click on an already-expanded
item's body (any line past its header) selects the enclosing item without
re-collapsing it so a click into a long expanded exchange never folds it
shut. Auto-scroll follow is paused on any transcript click that resolves
to a hit. Detail clicks remain inactive.

A plain vertical wheel targets the eligible panel under the pointer and
preserves keyboard focus. In Workflows, Runs, and Steps, one wheel event moves
the existing selection by three rows and keeps it visible. In Transcript and
Detail, one wheel event scrolls content by three rendered lines. All movement
is clamped; horizontal or modified wheels are ignored.

Mouse input is consumed without pass-through while modals, confirmations,
help, the command palette, review workspaces, focused Gate controls, search, or
text input are active. Borders, titles, footers, status and security strips,
list headers, pagination, row gaps, hidden panels, and coordinates outside the
current terminal are not targets. Every supported operation retains its
existing keyboard path; the mouse never becomes an activation or workflow
control surface.

## Transcript rendering

Monitor reads finalized content through `internal/transcript`. Normalize
entries into transcript items before search, navigation, expansion, and
rendering. Pair tool use/result only within the same generation, iteration,
and attempt. Missing counterparts and unknown blocks remain inspectable;
they must not be labeled as success or silently discarded.

Render prose through Glamour; render command output and literal tool content
through the verbatim path. Respect the existing role/block classification:
a user-role tool result is not human guidance. Preserve truncation indicators
and bounded window reads. Follow the live tail only while the user is already
at the tail; new output must not steal the position of someone reading history.

Adjacent local-file read exchanges at one execution coordinate form a
page-local **grouped read** when at least two are loaded without an intervening
item. A grouped read is one navigation and expansion target; its compact tree
retains every distinct loaded target, and a search or filter match on any member
retains the complete group. Expansion and selected-item copy use only the
members present on the loaded page, so grouping never implies off-page evidence.

Other recognized tools can use opt-in compact groups. Press `c` in Transcript
or choose the command-palette action to toggle `compact_tool_groups` for the
current session; the default (`false`) comes from `config.toml`'s `[tui]`
table (`compact_tool_groups`), which a persistent default can override, but
the in-session toggle itself is not written back to disk. Eligible groups
contain at least two adjacent, settled, successful calls of the same canonical
kind and execution coordinate. Failures, running or incomplete calls, malformed
or targetless calls, unknown tools, and `askuserquestion` remain standalone and
split a run.

Compact non-read groups ignore settled reasoning hidden by the default view.
Enabling the reasoning filter restores those items and splits groups at their
original positions. Live reasoning and execution-coordinate changes still split
groups. Search and other filters run after grouping, so hiding prose, failures,
or targetless calls with a filter cannot join calls across those boundaries.

A collapsed non-read group shows at most the first three calls, an omitted-count
row, and the final call. Expanding the group with `enter` or space reveals every
member as its existing bordered summary card. The group header and visible child
cards are independent `n`/`N` stops; `enter` or space on a child toggles its
existing detail, and `o` applies global expansion. Search and filters inspect
members but retain the complete group, while copying a child copies that exchange
and copying the group header copies every loaded member. Press `x` to clear the
current transcript search and filters.

Keep render caches local to their owner and include all inputs that affect
rendering in invalidation decisions (width, content/version, expansion, and
presentation mode as applicable). Shared map storage under a value receiver
is not automatically concurrency-safe. Preserve raw evidence separately from
ANSI decoration; use the existing display/clipboard normalization paths for
terminal controls and hyperlinks.

## Review identity

`internal/tui/review` presents immutable documents. Markdown preview, syntax
highlighting, folding, and old/new diff gutters are transient projections.
Comments, anchors, drafts, and submissions remain tied to document identity
and one-based logical source lines. For diffs, identity is the raw patch line,
not the old/new file coordinate or rendered terminal row.

Keep per-document presentation, pan, fold, and cursor state. Source content
uses horizontal clipping with fixed gutters. On parser/highlighter failure,
fall back to verbatim source without changing line identity. Comment modals
must restore the document viewport when closed. Preserve draft/submission
validation through the shared review contract rather than duplicating it in
keyboard handlers.

## Verification

Drive model messages and commands in tests, asserting returned state and
rendered content. Include text capture, focus restoration, resize, stale
results, empty/unknown content, and queued gates for the behavior changed.
Use existing chart goldens for graph rendering and review tests for anchor
invariance. Run targeted race checks for asynchronous changes. Add a real
terminal smoke check for visual/input changes when feasible; model tests alone
cannot prove terminal ergonomics. Record dimensions and interactions observed.
See [Testing](TESTING.md) for commands and scope.
