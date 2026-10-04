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

Every uninterrupted run of local-file read exchanges at one execution
coordinate forms a page-local **grouped read**, including a run of one, so a
lone read opens and collapses the same way. A single failed read is the
exception: it stays standalone so its error detail renders inline. A grouped read is one navigation and expansion target; its compact tree
retains every distinct loaded target, and a search or filter match on any member
retains the complete group. Expansion and selected-item copy use only the
members present on the loaded page, so grouping never implies off-page evidence.

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

## Inspect recorded edit source

In Transcript, `v` or **diff / recorded source** in the command palette switches
an eligible edit between its default diff presentation and **Recorded new
source**. The action expands a collapsed exchange. It uses only structured edit
content captured in the transcript, including unchanged context in `NewText`;
it never reads the current local file. Exchanges without structured edit
content do not offer the action.

A compact-group header first asks which eligible exchange to change. An edit
with several source sections then asks which file section to show. Use `j`/`k`
or arrows, Enter to select, and Esc to cancel without changing a mode. The
selected child changes independently; other file sections retain their labels.
Press `v` to return to diff, then `v` again to choose a different section. Mouse
input cannot confirm a source picker. The nearest picker closes before Esc
leaves Transcript; search, text entry, help, gate and review controls keep their
input ownership.

Diff remains the default when computation yields visible changes. Creation and
identical input show recorded source; skipped or failed computation retains its
unavailable-diff explanation. Empty new text shows **Empty recorded result**.
If either participating use/result block was truncated, a warning above source
says that only captured text is available. Even without that warning, this is
the adapter's recorded text, not independently verified file completeness.
Display strips terminal escapes and normalizes controls before highlighting;
raw copied evidence remains separate.

Mode, section and source byte position are scoped to the loaded exchange's run, step and execution
coordinate. They survive resize, collapse, grouping and temporary filters.
Changed payloads, unloaded pages or replacement steps reset presentation state.
Switching pauses follow and keeps the selected item visible. Item and whole-step
copy remain based on recorded evidence regardless of mode, window or expansion.

Recorded source is browsable in contiguous windows of at most 4096 recorded
bytes and 12 wrapped content rows. Labels, position, warnings and controls sit
outside that content budget. Use these keys or their command-palette actions:

| Key | Recorded source action |
| --- | --- |
| `alt+j` / `alt+k` | Next / previous window |
| `alt+g` / `alt+G` | First / last window |

Logical line numbers and continuation labels show where the window falls,
including within a long line. All captured text remains reachable; these are
window limits, not another capture limit. Ordinary `j`/`k` and viewport keys
still scroll Transcript. Source browsing pauses follow; `G` resumes transcript
follow. A single fallback source section can be browsed directly; use `v` to
choose a section when an exchange has several.

Resize reflows around the saved recorded-byte anchor. One-cell areas use a
placeholder for a grapheme too wide to fit; widening restores its text. With
no content width, widen the terminal to browse. Syntax highlighting operates
on one window and may lack context from earlier windows; literal text remains
available if highlighting fails. The recorded source does not change when the
current file changes or an editor returns. `e` opens that current local file;
`y` and `Y` still copy recorded evidence under the existing clipboard limits.

## Open a recorded file target

In Transcript, press `e` or choose **open file in editor** in the `ctrl+k`
palette to open a recorded file target. Eligible targets come from normalized
tool locations, edit-content paths, and recognized read/edit/write arguments
(`file_path`, `path`) or notebook-edit arguments (`notebook_path`,
`target_notebook`, `path`). Arbitrary prose, output, commands, search roots and
glob-tool arguments are not file references.

A selected exchange or compact-group child exposes only its targets. A group
header collects its loaded members, without fetching another transcript page.
Expansion is not required. Duplicate paths/positions collapse in their original
order; explicit positions take precedence over path-only fallbacks. Different
positions in the same file and equal relative paths from different execution
directories remain separate choices.

One target opens directly. Several targets open a keyboard picker: `j`/`k` or
arrow keys select, Enter opens, and Esc cancels before leaving Transcript.
The picker shows paths and positive recorded positions, checks file availability
asynchronously, and disables unavailable targets with an explanation. A missing
candidate never silently redirects to a valid sibling. Long labels retain their
filename/position end, and the bounded list scrolls to every loaded candidate.
Ordinary mouse selection does not open a file; mouse input is consumed while the
picker is open. Cancel and editor return preserve transcript selection,
expansion, focus and viewport position.

Relative targets resolve against the **originating execution directory recorded
on the same execution's transcript entries**. The runner records the absolute
directory it actually supplies to the harness. It uses the configured repository
root only when that is the session's directory. Paired use/result entries may
share origin metadata within the same generation, iteration and attempt;
conflicting or malformed origins remain unavailable. Historical records with no
origin cannot resolve relative paths. A removed execution view never falls back
to another checkout or jig's current directory. This internal origin metadata
is excluded from exported transcript records and whole-step clipboard text.

The target must currently exist as a regular local file (ordinary symlinks
are followed). Absolute paths retain their identity and may be outside the
project. Missing files, directories, URIs, and paths containing control
characters are rejected without creating a file. Opening shows the **current
local file**, which may differ from recorded transcript evidence. Spaces,
Unicode, colons, and shell characters remain literal path data; a `:line`
suffix is never parsed out of a filename.

Configure an editor in user `config.toml` or project `.jig/config.toml`:

```toml
[tui.opener]
argv = ["vim", "+{line}", "--", "{path}"]
terminal = true
```

| Setting | Behavior |
| --- | --- |
| `argv` | Literal string array. Project argv replaces user argv as a whole. An explicit `[]` restores the `$EDITOR` fallback. |
| `terminal` | Defaults to `true`; merged independently, including for `$EDITOR`. Use `false` for a GUI launcher. |
| `{path}` | Required in configured arguments; substituted with the absolute file path without splitting the argument. |
| `{line}`, `{column}` | Optional positive one-based positions; absent or nonpositive values expand to `1`. |

Placeholders are forbidden in the executable (`argv[0]`); unknown placeholders,
an empty executable, NUL bytes, and invalid field types fail configuration
validation. For example, a GUI editor supporting a position argument can use
`argv = ["code", "--goto", "{path}:{line}:{column}"]` with `terminal = false`.
The editor determines whether it honors the supplied position.

Without a nonempty configured argv, jig tokenizes `$EDITOR` as a command plus
quoted/escaped arguments and appends the absolute path as one argument. It does
not evaluate a shell: variables, substitutions, pipes, redirects, and globs
stay literal. An unset fallback produces configuration guidance; there is no
implicit operating-system opener. `$EDITOR` has no universal position flag, so
when recorded coordinates are present the completion notice explains that the
file opened without position navigation and recommends a configured template.

Terminal mode yields the terminal to the editor and returns to Monitor when
the editor exits. Nonterminal launchers run asynchronously with no terminal
input/output. Both are waited for and reaped; unsuccessful exits and missing
executables produce safe notices. Only one launch may be pending at a time.
Navigation does not cancel a running GUI launcher, and stale preparation or
completion cannot change a different selection. Opening does not cancel engine
work or reset transcript selection, expansion, or viewport position. Search,
text entry, modals, gates, review controls, and output-file view retain their
own input behavior. Optional persistence is not needed to open an already-loaded
absolute target.

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
