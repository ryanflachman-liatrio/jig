package shared

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/x/ansi"
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
		{name: "absent diff gutter", style: theme.Review.DiffGutterAbsent, text: "   "},
		{name: "context diff gutter", style: theme.Review.DiffGutterContext, text: " 42"},
		{name: "add diff gutter", style: theme.Review.DiffGutterAdd, text: " 42"},
		{name: "remove diff gutter", style: theme.Review.DiffGutterRemove, text: " 42"},
		{name: "cursor diff gutter", style: theme.Review.DiffGutterCursor, text: " 42"},
		{name: "range diff gutter", style: theme.Review.DiffGutterRange, text: " 42"},
		{name: "hunk status", style: theme.Review.HunkStatus, text: "Hunk 2/5"},
		{name: "hunk title", style: theme.Review.HunkTitle, text: " Hunk 2 "},
		{name: "folded placeholder", style: theme.Review.FoldedPlaceholder, text: "… 14 patch rows folded; press z to expand"},
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
			if got := ansi.Strip(tt.style.Render(tt.text)); got != tt.text {
				t.Fatalf("ANSI-stripped text = %q, want %q", got, tt.text)
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

func TestCardStylesUseSemanticPaletteRoles(t *testing.T) {
	theme := DefaultTheme()
	if theme.Card.BorderPending.GetForeground() != theme.Title.GetForeground() ||
		theme.Card.BorderRunning.GetForeground() != theme.Title.GetForeground() {
		t.Fatal("pending/running card borders do not use primary")
	}
	if theme.Card.BorderSuccess.GetForeground() != theme.Footer.GetForeground() {
		t.Fatal("success card border does not use dim")
	}
	if theme.Card.BorderWarning.GetForeground() != theme.Warning.GetForeground() {
		t.Fatal("warning card border does not use warning")
	}
	if theme.Card.BorderError.GetForeground() != theme.Error.GetForeground() {
		t.Fatal("error card border does not use danger")
	}
	if theme.Card.BorderMuted.GetForeground() != theme.Panel.BlurredBorder.GetBorderLeftForeground() {
		t.Fatal("muted card border does not reuse subdued panel border")
	}
	if theme.Card.TintNeutral.GetBackground() == nil || theme.Card.TintError.GetBackground() == nil ||
		theme.Card.TintNeutral.GetBackground() == theme.Card.TintError.GetBackground() {
		t.Fatal("card tint backgrounds are missing or indistinguishable")
	}
}

func TestChatToolStylesAndStatusIconStylesDeriveFromTokens(t *testing.T) {
	theme := DefaultTheme()

	if theme.Chat.ToolTitle.GetForeground() != theme.Chat.TranscriptActivity.GetForeground() {
		t.Fatalf("ToolTitle foreground = %v, want plain foreground base (matching TranscriptActivity)",
			theme.Chat.ToolTitle.GetForeground())
	}
	if theme.Chat.ToolDescription.GetForeground() != theme.Chat.TranscriptDetail.GetForeground() {
		t.Fatalf("ToolDescription foreground = %v, want muted (matching TranscriptDetail)",
			theme.Chat.ToolDescription.GetForeground())
	}
	if theme.Chat.ToolMeta.GetForeground() != theme.Chat.Hint.GetForeground() {
		t.Fatalf("ToolMeta foreground = %v, want dim (matching Chat.Hint)",
			theme.Chat.ToolMeta.GetForeground())
	}
	if !theme.Chat.ToolBadge.GetBold() {
		t.Fatal("ToolBadge should be bold to read as a discrete pill")
	}
	if theme.Chat.ToolBadge.GetBackground() == nil {
		t.Fatal("ToolBadge should have a background so it visually detaches from the row")
	}

	// Per-state header icons share their foreground with the corresponding
	// Card.Border* style, so the header icon and card border read as one
	// indicator of state (spec FR-02.9).
	stateCases := []struct {
		name  string
		state ToolDisplayState
		want  lipgloss.Style
	}{
		{"success", ToolDisplaySuccess, theme.Card.BorderSuccess},
		{"error", ToolDisplayError, theme.Card.BorderError},
		{"running", ToolDisplayRunning, theme.Card.BorderRunning},
		{"unknown use", ToolDisplayUnknownUse, theme.Card.BorderWarning},
		{"unknown result", ToolDisplayUnknownResult, theme.Card.BorderWarning},
	}
	for _, tc := range stateCases {
		t.Run(tc.name, func(t *testing.T) {
			_, style := ToolStatusIcon(tc.state, "")
			if got, want := style.GetForeground(), tc.want.GetForeground(); got != want {
				t.Fatalf("state %v icon fg = %v, want %v", tc.name, got, want)
			}
		})
	}
}
