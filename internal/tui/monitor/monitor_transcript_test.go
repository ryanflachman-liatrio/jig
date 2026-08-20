package monitor

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

func TestSplitMarkdownFences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		markdown   string
		wantFences int
	}{
		{
			name: "backtick and tilde fences",
			markdown: "before\n\n```go\nfmt.Println(\"one\")\n```\n\n" +
				"between\n\n~~~~json\n{\"two\": 2}\n~~~~~\n\nafter",
			wantFences: 2,
		},
		{
			name:       "up to three leading spaces",
			markdown:   "   ```text\nindented\n   ```",
			wantFences: 1,
		},
		{
			name:       "four spaces is ordinary markdown",
			markdown:   "    ```text\nindented code\n    ```",
			wantFences: 0,
		},
		{
			name:       "unmatched opener stays prose",
			markdown:   "before\n```go\nincomplete",
			wantFences: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parts := splitMarkdownFences(tt.markdown)
			gotFences := 0
			var rebuilt strings.Builder
			for _, part := range parts {
				rebuilt.WriteString(part.source)
				if part.isFence {
					gotFences++
				}
			}
			if gotFences != tt.wantFences {
				t.Fatalf("fence count = %d, want %d: %#v", gotFences, tt.wantFences, parts)
			}
			if got := rebuilt.String(); got != tt.markdown {
				t.Fatalf("split did not preserve source:\ngot:  %q\nwant: %q", got, tt.markdown)
			}
		})
	}
}

func TestRenderMarkdownCodeFencesInSeparateBlocks(t *testing.T) {
	m := Model{
		transcriptInnerW: 48,
		chatRendered:     make(map[blockKey]string),
	}
	m.rebuildRenderer()

	out := m.renderMarkdown(blockKey{seq: 1}, "Before.\n\n```go\nfmt.Println(\"one\")\n```\n\n"+
		"Between.\n\n~~~json\n{\"two\": 2}\n~~~\n\nAfter.")
	plain := ansiStrip(out)

	if got := strings.Count(plain, "╭"); got != 2 {
		t.Fatalf("rounded block tops = %d, want 2:\n%s", got, plain)
	}
	if got := strings.Count(plain, "╰"); got != 2 {
		t.Fatalf("rounded block bottoms = %d, want 2:\n%s", got, plain)
	}
	firstBottom := strings.Index(plain, "╰")
	between := strings.Index(plain, "Between.")
	secondTop := strings.LastIndex(plain, "╭")
	if firstBottom < 0 || between < firstBottom || secondTop < between {
		t.Fatalf("prose did not remain between independent code blocks:\n%s", plain)
	}
	for i, line := range strings.Split(out, "\n") {
		if width := lipgloss.Width(line); width > m.transcriptInnerW {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i, width, m.transcriptInnerW, plain)
		}
	}
	if _, unset := shared.Theme.Chat.CodeBlock.GetBackground().(lipgloss.NoColor); unset {
		t.Fatal("code block background is not configured")
	}
}
