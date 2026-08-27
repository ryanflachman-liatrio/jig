package shared

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestReviewStylesBelongToThemeAndPreserveVisibleWidth(t *testing.T) {
	theme := DefaultTheme()
	tests := []struct {
		name  string
		style lipgloss.Style
		text  string
	}{
		{name: "active mode", style: theme.Review.ModeActive, text: " PREVIEW "},
		{name: "inactive mode", style: theme.Review.ModeInactive, text: " SOURCE "},
		{name: "active rail", style: theme.Review.BlockRailActive, text: "▌"},
		{name: "inactive rail", style: theme.Review.BlockRailInactive, text: "│"},
		{name: "active metadata", style: theme.Review.BlockMetaActive, text: "L12–L18"},
		{name: "inactive metadata", style: theme.Review.BlockMetaInactive, text: "L12–L18"},
		{name: "comment marker", style: theme.Review.CommentMarker, text: "● 2 comments"},
		{name: "active comment", style: theme.Review.ActiveComment, text: "● C003 active"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.style.GetForeground() == nil {
				t.Fatal("review style has no theme foreground")
			}
			if got, want := lipgloss.Width(tt.style.Render(tt.text)), lipgloss.Width(tt.text); got != want {
				t.Fatalf("rendered width = %d, want %d", got, want)
			}
		})
	}

	if theme.Review.ModeActive.GetForeground() == theme.Review.ModeInactive.GetForeground() {
		t.Fatal("active and inactive modes are not visually distinct")
	}
}
