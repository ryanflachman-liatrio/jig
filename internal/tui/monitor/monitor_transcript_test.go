package monitor

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

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

func TestRenderMarkdownCodeFenceInsideBlockquote(t *testing.T) {
	m := Model{
		transcriptInnerW: 64,
		chatRendered:     make(map[blockKey]string),
	}
	m.rebuildRenderer()

	out := m.renderMarkdown(blockKey{seq: 1}, "> Example:\n>\n> ```go\n> fmt.Println(\"nested\")\n> ```")
	plain := ansiStrip(out)

	if got := strings.Count(plain, "╭"); got != 1 {
		t.Fatalf("rounded block tops = %d, want 1:\n%s", got, plain)
	}
	for _, want := range []string{"Example:", `fmt.Println("nested")`} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered blockquote missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "```") {
		t.Fatalf("rendered blockquote leaked Markdown fence markers:\n%s", plain)
	}
}
