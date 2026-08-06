# jig TUI

The presentation vocabulary of jig's Bubble Tea interface — the terms that name
what the user sees and where their keys act. This context covers the framing and
layout language introduced by the bordered-screens work; it is a glossary, not a
spec.

## Language

**Screen**:
One top-level view in the workflow flow: Selector, Detail, Runs, or Monitor.
Exactly one is active at a time. The standalone streaming Chat client is a separate
root model reached via `go run ./cmd/jig` — not part of the four-screen flow, but it
is paneled with the same helper and focus convention.
_Avoid_: Page, view, tab.

**Panel**:
A rounded border with a title composited into its top edge, wrapping a body of
content. A pure-presentation primitive: it frames and titles a pre-rendered body
and paints the focus color; it never owns viewports, wrapping, or content sizing —
the caller fits the body to the panel's inner area using lipgloss frame helpers.
_Avoid_: Box, frame, pane, window.

**Focus** (of a region):
The property of holding keyboard input. The focused region's border is drawn in the
primary color (Charple); every blurred region's border is dim (Iron). On a
single-panel screen the whole screen is the focused region. In the Monitor, focus
moves between the Steps panel, the Transcript panel, and the Gate — the Gate only
while the input queue is non-empty (an empty gate is visible but not focusable and
is skipped by the `tab` cycle).
_Avoid_: Active, selected (reserve "selected" for the list cursor row).

**Gate**:
The always-present full-width strip beneath the two panels through which the
Monitor collects human input. It is the single surface for every kind of
human-in-the-loop request and is drawn even when nothing is pending (then it shows
a placeholder). A pending gate does NOT freeze navigation — the user can still move
focus between the panels to read context while the gate waits.
_Avoid_: Overlay, modal, dialog, prompt (reserve "prompt" for the from="user" entry).

**Input queue**:
The ordered set of all steps currently blocked on a human, surfaced through the
Gate. Arrivals append; nothing is dropped when several steps block at once. A
`[N / M]` indicator names the active position and the total. The user cycles the
queue with `tab`/`shift+tab` while the Gate holds focus and answers entries in any
order.
_Avoid_: Stack, list (reserve "list" for the Steps list), backlog.

**Input entry** (or **entry**):
One item in the input queue: a review verdict, a `block_on` agent-input box, an
AskUserQuestion option list, or a `from="user"` text prompt. An entry carries the
step ID (and tool-use ID for a question) that routes its response, plus any draft
text the user has typed, preserved as they navigate away and back. A review
entry's diff is not shown in the entry — it renders in the Transcript panel when
its step is selected.
_Avoid_: Request, item, gate (a gate is the strip; an entry is what it displays).

**Footer**:
The single unboxed dim hint line rendered directly below a screen's panel(s),
listing the keybindings available in the current state. Never enclosed in a panel.
_Avoid_: Help line, status bar, keybind bar.
