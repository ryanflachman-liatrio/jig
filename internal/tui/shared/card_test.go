package shared

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func intptr(v int) *int { return &v }

func assertCardWidth(t *testing.T, out string, width int) {
	t.Helper()
	for n, row := range strings.Split(out, "\n") {
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("row %d width = %d, want %d: %q", n, got, width, row)
		}
	}
}

func TestRenderCardWidth(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4, 5, 40, 60, 90} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			out := RenderCard(Card{
				Width:  width,
				Header: Theme.Title.Render("界e\u0301🙂 an intentionally long styled header"),
				State:  CardRunning,
				Sections: []CardSection{
					{Label: Theme.Chat.TranscriptLabel.Render("Inputs\nwith\ttabs"), Lines: []string{"indented longunbrokenword longunbrokenword"}},
					{Label: "Details", Lines: []string{"two\n\nthree"}},
				},
			})
			assertCardWidth(t, out, width)
			if width >= 3 {
				plain := ansi.Strip(out)
				if !strings.HasPrefix(plain, "╭") || !strings.HasSuffix(plain, "╯") {
					t.Fatalf("width %d did not retain rounded frame: %q", width, plain)
				}
			}
		})
	}
	if got := RenderCard(Card{Width: 0}); got != "" {
		t.Fatalf("zero-width card = %q, want empty", got)
	}
	if got := RenderCard(Card{Width: -1}); got != "" {
		t.Fatalf("negative-width card = %q, want empty", got)
	}
}

func TestCardPaddingPresence(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		left, right *int
		want        int
	}{
		{name: "omitted", width: 40, want: 36},
		{name: "explicit zero", width: 40, left: intptr(0), right: intptr(0), want: 38},
		{name: "right inherits left", width: 40, left: intptr(3), want: 32},
		{name: "asymmetric", width: 40, left: intptr(2), right: intptr(4), want: 32},
		{name: "negative clamps", width: 40, left: intptr(-2), right: intptr(-3), want: 38},
		{name: "narrow reduces padding", width: 3, left: intptr(5), right: intptr(5), want: 1},
		{name: "zero outer width", width: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CardContentWidth(tt.width, tt.left, tt.right); got != tt.want {
				t.Fatalf("CardContentWidth() = %d, want %d", got, tt.want)
			}
			if tt.width > 0 {
				out := RenderCard(Card{Width: tt.width, PadLeft: tt.left, PadRight: tt.right, Sections: []CardSection{{Lines: []string{"x"}}}})
				assertCardWidth(t, out, tt.width)
			}
		})
	}
}

func TestRenderCardHeaderAndDividerGrammar(t *testing.T) {
	styledHeader := Theme.Title.Render("Header")
	styledMeta := Theme.Chat.Hint.Render("Meta")
	out := RenderCard(Card{
		Width:      40,
		Header:     styledHeader,
		HeaderMeta: styledMeta,
		Sections: []CardSection{
			{Label: Theme.Chat.TranscriptLabel.Render("First"), Lines: []string{"one"}},
			{Lines: []string{"two"}, Rule: true},
			{Label: "Last", Lines: []string{"three"}},
		},
	})
	plain := ansi.Strip(out)
	for _, want := range []string{"╭─── Header · Meta ", "├─── First ", "├─── Last "} {
		if !strings.Contains(plain, want) {
			t.Fatalf("card missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(out, styledHeader) || !strings.Contains(out, styledMeta) {
		t.Fatalf("caller label styling was not preserved: %q", out)
	}
	rows := strings.Split(plain, "\n")
	if got := strings.Count(plain, "├"); got != 3 {
		t.Fatalf("divider count = %d, want 3:\n%s", got, plain)
	}
	for _, row := range []string{rows[3], rows[len(rows)-1]} {
		if strings.Contains(row, " ") {
			t.Fatalf("unlabeled bar contains a gap: %q", row)
		}
	}

	firstRule := ansi.Strip(RenderCard(Card{Width: 20, Sections: []CardSection{{Rule: true}}}))
	if strings.Contains(firstRule, "├") {
		t.Fatalf("unlabeled first-section rule should be suppressed: %q", firstRule)
	}
}

func TestRenderCardNormalizesAndTruncatesLabels(t *testing.T) {
	out := RenderCard(Card{Width: 20, Header: Theme.Title.Render("界界\n🙂\tvery long"), Sections: []CardSection{{Label: "line\rbreak"}}})
	plain := ansi.Strip(out)
	if strings.ContainsAny(plain, "\t\r") {
		t.Fatalf("label retained control whitespace: %q", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("long styled label was not truncated: %q", plain)
	}
	assertCardWidth(t, out, 20)
}

func TestRenderCardStateBorders(t *testing.T) {
	tests := []struct {
		name  string
		state CardState
		style lipgloss.Style
	}{
		{name: "pending", state: CardPending, style: Theme.Card.BorderPending},
		{name: "running", state: CardRunning, style: Theme.Card.BorderRunning},
		{name: "success", state: CardSuccess, style: Theme.Card.BorderSuccess},
		{name: "warning", state: CardWarning, style: Theme.Card.BorderWarning},
		{name: "error", state: CardError, style: Theme.Card.BorderError},
		{name: "invalid falls back to pending", state: CardState(99), style: Theme.Card.BorderPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := Card{Width: 20, State: tt.state}
			if card.borderStyle().GetForeground() != tt.style.GetForeground() {
				t.Fatalf("card border foreground = %v, want %v", card.borderStyle().GetForeground(), tt.style.GetForeground())
			}
		})
	}
	muted := Card{Width: 20, State: CardError, BorderMuted: true}
	if muted.borderStyle().GetForeground() != Theme.Card.BorderMuted.GetForeground() {
		t.Fatalf("muted override did not win: %v", muted.borderStyle().GetForeground())
	}
}

func TestRenderCardGallery(t *testing.T) {
	states := []struct {
		name  string
		state CardState
	}{
		{name: "pending", state: CardPending},
		{name: "running", state: CardRunning},
		{name: "success", state: CardSuccess},
		{name: "warning", state: CardWarning},
		{name: "error", state: CardError},
	}
	for _, width := range []int{40, 60, 90} {
		for _, state := range states {
			out := RenderCard(Card{
				Width:      width,
				Header:     Theme.Title.Render("Tool activity with a long synthetic label"),
				HeaderMeta: Theme.Chat.Hint.Render(state.name),
				State:      state.state,
				Sections: []CardSection{
					{Label: "Input", Lines: []string{"synthetic/path/to/example.go"}},
					{Lines: []string{"status: synthetic"}, Rule: true},
				},
			})
			assertCardWidth(t, out, width)
			t.Logf("width=%d state=%s rows=%d\n%s", width, state.name, lipgloss.Height(out), ansi.Strip(out))
		}
	}
}

func TestRenderCardHeaderOnly(t *testing.T) {
	out := RenderCard(Card{Width: 40, Header: "tool", State: CardError, Tint: true})
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("header-only card rows = %d, want 2", got+1)
	}
	for _, row := range strings.Split(out, "\n") {
		if got := lipgloss.Width(row); got != 40 {
			t.Fatalf("row width = %d, want 40", got)
		}
	}
}

func TestRenderCardTintRestoresAfterContentReset(t *testing.T) {
	out := RenderCard(Card{Width: 40, State: CardError, Tint: true, Sections: []CardSection{{Lines: []string{"before\x1b[0mafter\x1b[49mend"}}}})
	if got := strings.Count(out, "\x1b[48;2;42;26;30m"); got < 3 {
		t.Fatalf("error tint reapplied %d times, want reset stabilization", got)
	}
	if !strings.HasSuffix(out, "\x1b[49m") {
		t.Fatalf("tinted card did not reset background at row boundary: %q", out)
	}
}

func TestTruncateTitlePreservesStyledGraphemes(t *testing.T) {
	for _, text := range []string{"界界界", "e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301", "🙂🙂🙂"} {
		styled := Theme.Title.Render(text)
		got := TruncateTitle(styled, 5)
		if width := lipgloss.Width(got); width > 5 {
			t.Fatalf("%q width = %d, want <= 5", text, width)
		}
		if !strings.Contains(got, "…") {
			t.Fatalf("truncated title lacks ellipsis: %q", got)
		}
		if strings.Contains(ansi.Strip(got), "\x1b") {
			t.Fatalf("truncated title contains broken ANSI: %q", got)
		}
	}
	if got := TruncateTitle("title", 0); got != "" {
		t.Fatalf("zero-budget title = %q, want empty", got)
	}
}
