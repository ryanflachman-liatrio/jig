package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// panel renders body inside a rounded border with title composited into the top
// border edge, lazygit-style: `╭─ Title ─────╮`. It is a *pure-presentation*
// primitive — it never creates or sizes viewports and never wraps content. The
// caller must pre-fit body to the panel's inner area (width-hFrame × height-vFrame,
// see panelFrame). focused selects the primary (Charple) vs. dim (Iron) border.
//
// lipgloss v2 has no native border-title API (Border carries only edge/corner
// runes), so we omit the style's top border and hand-build the titled top line,
// width-matched with lipgloss.Width. This is the single place to replace if a
// future lipgloss adds a border-title hook. See
// docs/adr/0001-manual-border-title-compositing.md.
func panel(title, body string, width, height int, focused bool) string {
	border := theme.Panel.BlurredBorder
	if focused {
		border = theme.Panel.FocusedBorder
	}

	if width < 1 {
		width = 1
	}
	// lipgloss Width/Height set the *total* rendered box size. The box carries the
	// left/right/bottom borders + padding; we reserve one row above it for the
	// hand-built titled top edge, so the box height is height-1.
	boxH := height - 1
	if boxH < 1 {
		boxH = 1
	}

	box := border.Width(width).Height(boxH).Render(body)
	return panelTopEdge(title, width, border) + "\n" + box
}

// panelTopEdge builds the titled top line `╭─ Title ─────╮` at exactly width
// visible cells, coloring the corners/dashes with the border's foreground so it
// joins seamlessly with the box body rendered by the same border style.
func panelTopEdge(title string, width int, border lipgloss.Style) string {
	rb := lipgloss.RoundedBorder()
	edge := lipgloss.NewStyle().Foreground(border.GetBorderLeftForeground())

	// Empty title: a plain dashed edge with the rounded corners.
	if title == "" {
		fill := width - lipgloss.Width(rb.TopLeft) - lipgloss.Width(rb.TopRight)
		if fill < 0 {
			fill = 0
		}
		return edge.Render(rb.TopLeft + strings.Repeat(rb.Top, fill) + rb.TopRight)
	}

	// Fixed decoration around the title: corner, one dash, a space either side of
	// the text, and the closing corner (5 cells). Truncate the title to whatever
	// width remains so the total never exceeds width.
	maxTitle := width - 5
	if maxTitle < 1 {
		// Too narrow for a title; fall back to a plain dashed edge.
		return panelTopEdge("", width, border)
	}
	t := truncateTitle(title, maxTitle)

	// left = ╭─ , then " Title ", then fill dashes, then ╮ — visible width = width.
	left := rb.TopLeft + rb.Top
	fill := width - lipgloss.Width(left) - 2 - lipgloss.Width(t) - lipgloss.Width(rb.TopRight)
	if fill < 0 {
		fill = 0
	}
	return edge.Render(left) + " " + theme.Panel.Title.Render(t) + " " +
		edge.Render(strings.Repeat(rb.Top, fill)+rb.TopRight)
}

// truncateTitle clips s to at most max visible cells, appending … when it must
// cut. Width-aware so wide runes don't overflow the reserved space.
func truncateTitle(s string, max int) string {
	if lipgloss.Width(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > max-1 { // reserve one cell for the …
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// panelFrame returns the horizontal and vertical cells a panel's border+title
// consume, so callers size body content to width-hFrame × height-vFrame without
// magic numbers. Vertical adds one to the style's frame (bottom border only) for
// the hand-built top edge the border style omits.
func panelFrame() (hFrame, vFrame int) {
	b := theme.Panel.FocusedBorder
	return b.GetHorizontalFrameSize(), b.GetVerticalFrameSize() + 1
}
