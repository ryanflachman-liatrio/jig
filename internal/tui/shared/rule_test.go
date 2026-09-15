package shared

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRuleExactWidth(t *testing.T) {
	for _, tt := range []struct {
		width int
		label string
	}{
		{width: 12, label: "reset 2"},
		{width: 24, label: "iteration 3"},
		{width: 40, label: "retry 1"},
		{width: 60, label: "iteration 2"},
		{width: 80, label: "reset 12"},
	} {
		t.Run(tt.label, func(t *testing.T) {
			row := Rule(tt.width, tt.label)
			if got := lipgloss.Width(row); got != tt.width {
				t.Fatalf("Rule(%d, %q) width = %d, want %d\nrow: %q",
					tt.width, tt.label, got, tt.width, row)
			}
		})
	}
}

func TestRuleCentersLabelWithinOneCell(t *testing.T) {
	label := "iteration 2"
	labelWidth := lipgloss.Width(label)
	for _, width := range []int{24, 40, 60, 80} {
		row := Rule(width, label)
		plain := ansi.Strip(row)
		labelByte := strings.Index(plain, label)
		if labelByte < 0 {
			t.Fatalf("width=%d: label not present in %q", width, plain)
		}
		// Convert byte offsets to visible-cell columns so wide-glyph fill
		// counts as its rendered width, not its byte length.
		leftCells := lipgloss.Width(plain[:labelByte])
		rightCells := lipgloss.Width(plain[labelByte+len(label):])
		left := leftCells - 1
		right := rightCells - 1
		if delta := left - right; delta < -1 || delta > 1 {
			t.Fatalf("width=%d: centering delta = %d (left=%d right=%d)\nplain: %q",
				width, delta, left, right, plain)
		}
		if leftCells+labelWidth+rightCells != width {
			t.Fatalf("width=%d: components sum = %d (left=%d label=%d right=%d)\nplain: %q",
				width, leftCells+labelWidth+rightCells, leftCells, labelWidth, rightCells, plain)
		}
	}
}

func TestRuleDegradesToBareLabelBelowMinimum(t *testing.T) {
	// Label width 11 + 2*minCap(1) + 2 = 15 cells minimum for a ruled form.
	row := Rule(12, "iteration 2")
	plain := ansi.Strip(row)
	if lipgloss.Width(row) != 12 {
		t.Fatalf("degradation width = %d, want 12\nrow: %q", lipgloss.Width(row), row)
	}
	if !strings.Contains(plain, "iteration 2") {
		t.Fatalf("degradation lost label: %q", plain)
	}
	if strings.ContainsRune(plain, '─') {
		t.Fatalf("degradation still drew rule glyphs: %q", plain)
	}
}

func TestRuleDegradesWhenLabelExceedsWidth(t *testing.T) {
	row := Rule(4, "iteration 2")
	if got := ansi.Strip(row); got != "iteration 2" {
		t.Fatalf("label wider than width should render bare label, got %q", got)
	}
}

func TestRuleEmptyLabelFillsWidth(t *testing.T) {
	row := Rule(20, "")
	plain := ansi.Strip(row)
	if plain != strings.Repeat("─", 20) {
		t.Fatalf("empty label = %q, want 20 rule glyphs", plain)
	}
	if lipgloss.Width(row) != 20 {
		t.Fatalf("empty label width = %d, want 20", lipgloss.Width(row))
	}
}

func TestRuleAcceptsSGRStyledLabel(t *testing.T) {
	styled := Theme.Chat.Hint.Render("iteration 2")
	row := Rule(60, styled)
	if got := lipgloss.Width(row); got != 60 {
		t.Fatalf("SGR-styled label produced width %d, want 60\nrow: %q", got, row)
	}
	if !strings.Contains(ansi.Strip(row), "iteration 2") {
		t.Fatalf("SGR-styled label lost its text: %q", ansi.Strip(row))
	}
}

func TestRuleWidthOneOrLess(t *testing.T) {
	if got := Rule(0, "x"); got != "" {
		t.Fatalf("Rule(0, ...) = %q, want empty", got)
	}
	if got := Rule(-3, "x"); got != "" {
		t.Fatalf("Rule(-3, ...) = %q, want empty", got)
	}
}

func TestRuleWideRuneLabel(t *testing.T) {
	// "本" is a common CJK glyph rendered at 2 cells; verify the helper
	// measures with lipgloss.Width so it never overflows.
	label := "本 本 本"
	labelWidth := lipgloss.Width(label)
	for _, width := range []int{labelWidth + 5, labelWidth + 20} {
		row := Rule(width, label)
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("wide-rune label at width=%d produced width %d\nrow: %q",
				width, got, row)
		}
	}
}

func TestRuleUsesRuleGlyphNotBareDash(t *testing.T) {
	// Regression: the helper must draw with shared.RuleGlyph so slice 14's
	// ASCII preset can substitute a "-" without editing the rule renderer.
	row := Rule(24, "reset 1")
	plain := ansi.Strip(row)
	if !strings.Contains(plain, RuleGlyph) {
		t.Fatalf("rule glyph missing from %q", plain)
	}
}
