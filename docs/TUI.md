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

### Remapping keys

Every matched binding is a named action in its screen's keymap
(`shared.Action`: a stable ID, the regions where it is live, and a pointer to
the binding handlers match). `config.toml`'s `[keys]` table remaps actions by
ID; each value is one chord or a list, and project entries override user
entries per action:

```toml
[keys]
"monitor.copy_item" = "ctrl+y"
"global.palette" = ["ctrl+k", "ctrl+p"]
```

A remap replaces the action's default keys. Handlers, footers, the help
overlay, and the command palette all read the same binding, so they show and
accept the new chord. `tui.ConfigureKeymap` validates the table when the TUI
starts and refuses to launch on an unknown ID, a fixed action, an empty chord
list, or a chord that two actions would share in overlapping regions (a region
encloses its dot-separated children; global chords are live everywhere). Keys
may repeat across regions that are never live together, such as the Steps
panel and a gate. Use `shared.Relabel` for a contextual description, never
`SetHelp` with a literal key.

Fixed actions keep their defaults but still block conflicting remaps: focus
movement, paired-direction bindings whose handler reads the pressed key (j/k
scroll, J/K, n/N, `[`/`]` entry navigation), the `gg` chord, gate submit and
blur, the review-open key, the workflow list's own navigation and filter, and
Detail's back key (chart mode drops `h`/`left` from it). Review workspaces,
question forms, and overlay-internal keys (palette, help, filters) are not
remappable yet.

Remappable action IDs:

| Scope | Actions |
|---|---|
| Global | `global.quit`, `global.help`, `global.help_typing`, `global.palette`, `global.notification_diagnostics` |
| Home | `home.pane`, `home.detail`, `selector.open`, `runs.up`, `runs.down`, `runs.open`, `runs.new_run`, `runs.resume`, `runs.delete`, `runs.copy_id`, `runs.back` |
| Detail | `detail.run`, `detail.runs`, `detail.toggle_chart` |
| Monitor (all regions) | `monitor.toggle_help_agent`, `monitor.toggle_simple`, `monitor.gate_context` |
| Monitor Steps | `monitor.down`, `monitor.up`, `monitor.open_transcript`, `monitor.toggle_tree`, `monitor.steps_leave`, `monitor.stop_step`, `monitor.reset_step`, `monitor.resume_step`, `monitor.toggle_bell`, `monitor.copy_all` |
| Monitor Transcript | `monitor.transcript_to_steps`, `monitor.transcript_leave`, `monitor.toggle`, `monitor.expand_all`, `monitor.follow`, `monitor.search`, `monitor.filters`, `monitor.compact_tools`, `monitor.clear_view`, `monitor.page_older`, `monitor.page_newer`, `monitor.copy_item`, `monitor.toggle_bell`, `monitor.copy_all` |
| Monitor gates | `monitor.recover_retry`, `monitor.recover_guide`, `monitor.recover_skip`, `monitor.recover_abort`, `monitor.integration_resolve`, `monitor.integration_agent`, `monitor.final_merge_approve`, `monitor.final_merge_discard`, `monitor.reset_confirm`, `monitor.reset_cancel` |

The command palette still runs an entry by re-dispatching the action's current
primary chord, so it follows remaps; it does not yet invoke actions by ID.

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

Every uninterrupted run of local-file read exchanges at one execution
coordinate forms a page-local **grouped read**, including a run of one, so a
lone read opens and collapses the same way. A grouped read is a tool group
(see below) whose policy is always on: it groups whether or not compact tool
groups are enabled, and it admits running and failed members. A single failed
read is the exception: it stays standalone so its error detail renders inline.
Collapsed, its tree lists every distinct loaded target once with its line
selectors instead of the first-three/last preview. Otherwise it shares every
tool-group behavior below: expansion, navigation, search, filters, and copy.
Expansion and copy use only the members present on the loaded page, so
grouping never implies off-page evidence.

Other recognized tools can use opt-in compact groups. Press `c` in Transcript
or choose the command-palette action to toggle `compact_tool_groups` for the
current session; the default (`false`) comes from `config.toml`'s `[tui]`
table (`compact_tool_groups`), which a persistent default can override, but
the in-session toggle itself is not written back to disk. Eligible groups
contain one or more adjacent, settled, successful calls of the same canonical
kind and execution coordinate. Failures, running or incomplete calls, malformed
or targetless calls, unknown tools, and `askuserquestion` remain standalone and
split a run.

The run monitor can also ring the terminal bell (BEL) when a gate starts
waiting, so an operator in another pane, tab, or tmux window notices a blocked
run. It is off by default; set `bell = true` in `config.toml`'s `[tui]` table
to opt in. Press `B` in Steps or Transcript, or choose the command-palette
action, to toggle it for the current session; the footer/help label reads
`bell: on` or `bell: off`, and the toggle is not written back to disk. The bell
rings once when the gate queue goes from empty to non-empty, not for gates that
join an already-waiting queue, never when the operator was already focused on
the gate, and at most once per 5-second cooldown. BEL is emitted through
`tea.Raw` so it stays sequenced with renderer output; terminals and tmux decide
whether it sounds, flashes, or only flags the window.

Compact tool groups, including grouped reads in compact mode, ignore settled
reasoning hidden by the default view. Enabling the reasoning filter restores
those items and splits groups at their original positions. Live reasoning and execution-coordinate changes still split
groups. Search and other filters run after grouping, so hiding prose, failures,
or targetless calls with a filter cannot join calls across those boundaries.

A collapsed group other than a grouped read shows at most the first three
calls, an omitted-count row, and the final call. Expanding any group with
`enter` or space reveals every member as its existing bordered summary card. The group header and visible child
cards are independent `n`/`N` stops; `enter` or space on a child toggles its
existing detail, and `o` applies global expansion. Search and filters inspect
members but retain the complete group, while copying a child copies that exchange
and copying the group header copies every loaded member. Press `x` to clear the
current transcript search and filters.

The search input, search/filter status, and filter picker are pinned above the
Transcript viewport, not rendered into its content. The viewport shrinks by the
pinned rows, so the controls stay visible at any scroll offset and item line
ranges keep indexing the content alone. Route viewport content refreshes through
`setChatContent`, and offset transcript mouse hit-testing by the pinned rows.

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
