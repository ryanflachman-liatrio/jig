package shared

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderStatusLineSlotFilteringAndSeparators(t *testing.T) {
	tests := []struct {
		name           string
		in             StatusLine
		wantContains   []string
		wantNoContains []string
	}{
		{
			name:         "icon + title only",
			in:           StatusLine{Icon: IconToolRead, Title: "Read"},
			wantContains: []string{IconToolRead + " Read"},
		},
		{
			name:         "title + description binds with colon",
			in:           StatusLine{Title: "Read", Description: "main.go"},
			wantContains: []string{"Read: main.go"},
		},
		{
			name:         "description without title has no leading colon",
			in:           StatusLine{Description: "orphan"},
			wantContains: []string{"orphan"},
			wantNoContains: []string{
				": orphan",
			},
		},
		{
			name:         "meta joins with dot separator",
			in:           StatusLine{Title: "Search", Meta: []string{"3 files", "2 hits"}},
			wantContains: []string{"Search 3 files · 2 hits"},
		},
		{
			name:         "empty and whitespace meta entries drop out",
			in:           StatusLine{Title: "Edit", Meta: []string{"", "   ", "diff", "\t"}},
			wantContains: []string{"Edit diff"},
			wantNoContains: []string{
				"·",
				"Edit  diff",
			},
		},
		{
			name:         "badge introduced by single space",
			in:           StatusLine{Title: "Edit", Badge: "+12/-3"},
			wantContains: []string{"Edit +12/-3"},
			wantNoContains: []string{
				"Edit: +12/-3",
			},
		},
		{
			name:         "full grammar preserves order",
			in:           StatusLine{Icon: IconToolEdit, Title: "Edit", Description: "cmd/jig/main.go", Badge: "+12/-3", Meta: []string{"1 hunk", "3s"}},
			wantContains: []string{IconToolEdit + " Edit: cmd/jig/main.go +12/-3 1 hunk · 3s"},
		},
		{
			name: "empty StatusLine returns empty string",
			in:   StatusLine{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.Strip(RenderStatusLine(tt.in))
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Fatalf("output missing %q:\n%q", want, got)
				}
			}
			for _, unwanted := range tt.wantNoContains {
				if strings.Contains(got, unwanted) {
					t.Fatalf("output contains unwanted %q:\n%q", unwanted, got)
				}
			}
			if tt.name == "empty StatusLine returns empty string" && got != "" {
				t.Fatalf("empty input rendered %q", got)
			}
		})
	}
}

func TestRenderStatusLineNoTruncation(t *testing.T) {
	long := strings.Repeat("very long title ", 30)
	out := RenderStatusLine(StatusLine{Title: long})
	if !strings.Contains(ansi.Strip(out), strings.TrimSpace(long)) {
		t.Fatalf("RenderStatusLine truncated its input; length=%d", lipgloss.Width(out))
	}
	if strings.Contains(out, "…") {
		t.Fatalf("RenderStatusLine emitted an ellipsis: %q", out)
	}
}

func TestRenderStatusLineFlattensNewlines(t *testing.T) {
	slots := StatusLine{
		Icon:        "A\nB",
		Title:       "line1\nline2",
		Description: "desc\rone",
		Badge:       "b1\r\nb2",
		Meta:        []string{"m1\nm2", "m3"},
	}
	out := RenderStatusLine(slots)
	if lipgloss.Height(out) != 1 {
		t.Fatalf("status line height = %d, want 1: %q", lipgloss.Height(out), out)
	}
	plain := ansi.Strip(out)
	if strings.ContainsAny(plain, "\n\r") {
		t.Fatalf("flattening failed: %q", plain)
	}
	for _, want := range []string{"A B", "line1 line2", "desc one", "b1  b2", "m1 m2 · m3"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("output missing %q:\n%q", want, plain)
		}
	}
}

func TestRenderStatusLinePerSlotStyling(t *testing.T) {
	callerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(hexBok))
	out := RenderStatusLine(StatusLine{
		Icon:        IconToolRead,
		IconStyle:   Theme.Card.BorderSuccess,
		Title:       "Read",
		Description: "main.go",
		Badge:       "b",
		Meta:        []string{"m"},
	})
	if !strings.Contains(out, Theme.Card.BorderSuccess.Render(IconToolRead)) {
		t.Fatalf("icon slot missing state-driven styling: %q", out)
	}
	if !strings.Contains(out, Theme.Chat.ToolTitle.Render("Read")) {
		t.Fatalf("title slot missing default ToolTitle style: %q", out)
	}
	if !strings.Contains(out, Theme.Chat.ToolDescription.Render("main.go")) {
		t.Fatalf("description slot missing default ToolDescription style: %q", out)
	}
	if !strings.Contains(out, Theme.Chat.ToolMeta.Render("m")) {
		t.Fatalf("meta slot missing default ToolMeta style: %q", out)
	}

	overridden := RenderStatusLine(StatusLine{
		Title:      "Selected",
		TitleStyle: callerStyle,
	})
	if !strings.Contains(overridden, "\x1b[") {
		t.Fatalf("caller-supplied title style produced no SGR: %q", overridden)
	}
}

func TestRenderStatusLineIconStylingIsOptional(t *testing.T) {
	// A caller-supplied icon with no style renders plain: RenderStatusLine
	// does not apply a default icon style so a plain caller-supplied glyph
	// (e.g. the spinner from slice 13) keeps its own ANSI sequence.
	out := RenderStatusLine(StatusLine{Icon: IconToolRead, Title: "Read"})
	if strings.Contains(out[:lipgloss.Width(IconToolRead)+1], "\x1b[") {
		t.Fatalf("unstyled icon slot picked up an SGR escape: %q", out)
	}
}

func TestRenderStatusLinePreservesGraphemesAndANSI(t *testing.T) {
	styledTitle := Theme.Title.Render("界界🙂")
	out := RenderStatusLine(StatusLine{Title: styledTitle})
	if !strings.Contains(out, styledTitle) {
		t.Fatalf("pre-styled title lost its escape sequence: %q", out)
	}
	if strings.Contains(ansi.Strip(out), "\x1b") {
		t.Fatalf("stripped output retained raw escape bytes: %q", ansi.Strip(out))
	}
}
