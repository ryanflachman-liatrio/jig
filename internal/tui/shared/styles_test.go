package shared

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
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
		{name: "gutter", style: theme.Review.Gutter, text: " 42 │"},
		{name: "cursor gutter", style: theme.Review.GutterCursor, text: " 42 │"},
		{name: "range gutter", style: theme.Review.GutterRange, text: " 42 │"},
		{name: "cursor rail", style: theme.Review.CursorRail, text: "▌"},
		{name: "range rail", style: theme.Review.RangeRail, text: "▌"},
		{name: "active source comment", style: theme.Review.ActiveCommentMarker, text: "●2"},
		{name: "language", style: theme.Review.Language, text: "Go"},
		{name: "horizontal hint", style: theme.Review.HorizontalHint, text: "← col 5 →"},
		{name: "comment modal title", style: theme.Review.CommentModalTitle, text: "Edit comment"},
		{name: "comment modal metadata", style: theme.Review.CommentModalMeta, text: "C003 · L12–L18"},
		{name: "comment modal hint", style: theme.Review.CommentModalHint, text: "esc close"},
		{name: "syntax base", style: theme.Review.SyntaxBase, text: "plain source"},
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
	if theme.Review.CommentModal.GetBorderLeftForeground() == nil {
		t.Fatal("comment modal has no themed border")
	}

	for _, token := range []chroma.TokenType{
		chroma.Text, chroma.Comment, chroma.CommentPreproc, chroma.Keyword,
		chroma.KeywordReserved, chroma.KeywordType, chroma.Operator, chroma.Punctuation,
		chroma.NameBuiltin, chroma.NameFunction, chroma.NameClass, chroma.LiteralString,
		chroma.LiteralStringEscape, chroma.LiteralNumber, chroma.GenericInserted,
		chroma.GenericDeleted, chroma.GenericSubheading, chroma.Error,
	} {
		if entry := theme.Review.Syntax.Get(token); !entry.Colour.IsSet() {
			t.Errorf("syntax token %s has no theme color", token)
		}
	}
}
