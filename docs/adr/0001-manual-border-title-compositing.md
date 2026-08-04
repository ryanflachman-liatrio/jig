# Manual border-title compositing in the panel helper

lipgloss v2.0.5 exposes no border-title API — the `Border` struct only carries
edge/corner runes and the top edge is drawn by an internal `renderHorizontalEdge`
with no label hook. To render `╭─ Workflows ─────╮` we therefore build the top
edge by hand: the panel style omits the top border, and the helper prepends a
top line of `corner + ─ + styled title + fill dashes + corner`, width-matched to
the body with `lipgloss.Width`.

We chose this "build the top edge" approach over splicing the title into a
fully-rendered box's first line, because the latter requires counting visible
width past the border's ANSI escape codes and re-emitting SGR styling — fragile
string-surgery that breaks silently. Over-long titles are truncated with `…` so
the panel's total width stays stable and surrounding layout math is unaffected.

If a future lipgloss version adds a native border-title API, this helper is the
single place to replace.
