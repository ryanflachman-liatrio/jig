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

func TestBreadcrumbTitle(t *testing.T) {
	t.Run("full hierarchy", func(t *testing.T) {
		got := shared.BreadcrumbTitle(
			[]string{"a1b2c3d4 · feature", "implement", "output.json"},
			80,
		)
		want := "a1b2c3d4 · feature › implement › output.json"
		if got != want {
			t.Fatalf("BreadcrumbTitle() = %q, want %q", got, want)
		}
	})

	t.Run("elides middle before current content", func(t *testing.T) {
		const maxWidth = 34
		got := shared.BreadcrumbTitle(
			[]string{"a1b2c3d4 · very-long-workflow", "implement", "output.json"},
			maxWidth,
		)
		if lipgloss.Width(got) > maxWidth {
			t.Fatalf("breadcrumb width = %d, want <= %d: %q", lipgloss.Width(got), maxWidth, got)
		}
		for _, want := range []string{"a1b2c3d4", "…", "output.json"} {
			if !strings.Contains(got, want) {
				t.Fatalf("compacted breadcrumb %q missing %q", got, want)
			}
		}
		if strings.Contains(got, "implement") {
			t.Fatalf("compacted breadcrumb retained middle segment: %q", got)
		}
	})

	t.Run("sanitizes presentation data", func(t *testing.T) {
		got := shared.BreadcrumbTitle(
			[]string{"\x1b[31ma1b2c3d4\x1b[0m · feature\nprod", "out\tput.json"},
			80,
		)
		if strings.ContainsAny(got, "\x1b\n\t") {
			t.Fatalf("breadcrumb retained control characters: %q", got)
		}
		if got != "a1b2c3d4 · feature prod › out put.json" {
			t.Fatalf("sanitized breadcrumb = %q", got)
		}
	})

	t.Run("bounds a long leaf while retaining identity", func(t *testing.T) {
		const maxWidth = 20
		got := shared.BreadcrumbTitle(
			[]string{"a1b2c3d4 · feature", "implement", strings.Repeat("artifact", 8) + ".json"},
			maxWidth,
		)
		if lipgloss.Width(got) > maxWidth {
			t.Fatalf("breadcrumb width = %d, want <= %d: %q", lipgloss.Width(got), maxWidth, got)
		}
		if !strings.HasPrefix(got, "a1b2c3") || !strings.Contains(got, "art") {
			t.Fatalf("breadcrumb did not retain identity and leaf: %q", got)
		}
	})
}

func TestBreadcrumbPanelPreservesDimensions(t *testing.T) {
	const (
		width  = 34
		height = 6
	)
	out := shared.BreadcrumbPanel(
		[]string{"a1b2c3d4 · feature", "implement", "output.json"},
		"body",
		width,
		height,
		true,
	)
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Errorf("line %d width = %d, want %d: %q", i, got, width, line)
		}
	}
	if got := lipgloss.Height(out); got != height {
		t.Fatalf("height = %d, want %d", got, height)
	}
}
