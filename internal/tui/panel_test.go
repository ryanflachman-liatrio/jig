package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// TestPanel verifies the pure-presentation titled-panel helper: it draws the
// rounded corners, composites the title into the top edge, truncates an
// over-long title with …, and keeps total Width/Height equal to the requested
// outer dimensions for both focus states (per ADR 0001).
func TestPanel(t *testing.T) {
	const (
		width  = 30
		height = 6
	)

	cases := []struct {
		name    string
		title   string
		focused bool
		// wantTitle is the (possibly truncated) text that must appear on the top
		// edge; empty means assert truncation with …[first runes].
		wantSub string
		wantEll bool
	}{
		{name: "focused short title", title: "Workflows", focused: true, wantSub: "Workflows"},
		{name: "blurred short title", title: "Steps", focused: false, wantSub: "Steps"},
		{name: "overlong title truncates", title: strings.Repeat("verylongtitle", 5), focused: true, wantEll: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := shared.Panel(tc.title, "body line\nsecond", width, height, tc.focused)

			// Rounded corners present on all four sides.
			for _, corner := range []string{"╭", "╮", "╰", "╯"} {
				if !strings.Contains(out, corner) {
					t.Errorf("panel output missing corner %q\n%s", corner, out)
				}
			}

			// Title text (or an ellipsis) on the first line only.
			firstLine := strings.SplitN(out, "\n", 2)[0]
			if tc.wantSub != "" && !strings.Contains(firstLine, tc.wantSub) {
				t.Errorf("top edge %q missing title %q", firstLine, tc.wantSub)
			}
			if tc.wantEll && !strings.Contains(firstLine, "…") {
				t.Errorf("top edge %q should truncate with …", firstLine)
			}

			// Stable outer dimensions regardless of title length or focus. Assert
			// *every* line is width cells so the top edge stays aligned with the box
			// body (a narrower box under a wider top edge still passes a max-width
			// check, so measure each line).
			for i, line := range strings.Split(out, "\n") {
				if got := lipgloss.Width(line); got != width {
					t.Errorf("line %d width = %d, want %d: %q", i, got, width, line)
				}
			}
			if got := lipgloss.Height(out); got != height {
				t.Errorf("height = %d, want %d\n%s", got, height, out)
			}
		})
	}
}

// TestPanelFrame checks that panelFrame reports a non-zero horizontal and
// vertical frame so callers never fall back to magic numbers, and that the
// vertical frame includes the hand-built top edge row.
func TestPanelFrame(t *testing.T) {
	h, v := shared.PanelFrame()
	if h <= 0 || v <= 0 {
		t.Fatalf("panelFrame() = (%d, %d), want both > 0", h, v)
	}
	// A panel of exactly the frame size renders an empty body with no overflow.
	out := shared.Panel("T", "", 10, v+1, true)
	if got := lipgloss.Height(out); got != v+1 {
		t.Errorf("height = %d, want %d", got, v+1)
	}
}
