package review

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	domain "jig/internal/review"
	"jig/internal/tui/shared"
)

func sourceDocument(source, format, content string) document {
	return document{
		meta:  domain.Document{ID: "doc", Source: source, Format: format, Content: content},
		lines: strings.Split(content, "\n"),
	}
}

func TestSourcePresentationSelectsDeterministicRenderers(t *testing.T) {
	tests := []struct {
		name, source, format, content, language string
	}{
		{name: "go", source: "main.go", format: "text", content: "package main", language: "Go"},
		{name: "markdown", source: "README", format: "markdown", content: "# Title", language: "Markdown"},
		{name: "toml", source: "config.toml", format: "text", content: "enabled = true", language: "TOML"},
		{name: "go module plain text", source: "go.mod", format: "text", content: "module jig", language: "Plain text"},
		{name: "json", source: "data.json", format: "text", content: `{\"ok\":true}`, language: "JSON"},
		{name: "unknown", source: "artifact.unknown-jig", format: "text", content: "plain", language: "Plain text"},
		{name: "format diff", source: "captured", format: "diff", content: "+added", language: "Diff"},
		{name: "diff extension", source: "change.diff", format: "text", content: "+added", language: "Diff"},
		{name: "patch extension", source: "change.patch", format: "text", content: "-removed", language: "Diff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			presentation := buildSourcePresentation(sourceDocument(tt.source, tt.format, tt.content))
			if presentation.err != nil {
				t.Fatal(presentation.err)
			}
			if presentation.language != tt.language {
				t.Fatalf("language = %q, want %q", presentation.language, tt.language)
			}
			if got := ansi.Strip(strings.Join(presentation.lines, "\n")); got != tt.content {
				t.Fatalf("visible content = %q, want %q", got, tt.content)
			}
		})
	}
}

func TestHighlightSourceContainsRendererPanics(t *testing.T) {
	d := sourceDocument("broken.go", "text", "package broken")
	if _, err := highlightSource(d, nil); err == nil || !strings.Contains(err.Error(), "highlight source") {
		t.Fatalf("panic fallback error = %v", err)
	}
	fallback := plainSourcePresentation(d, errSentinel{})
	if got := strings.Join(fallback.lines, "\n"); got != d.meta.Content || fallback.language != "Plain text" || fallback.err == nil {
		t.Fatalf("plain fallback = %#v", fallback)
	}
}

type errSentinel struct{}

func (errSentinel) Error() string { return "renderer failed" }

func TestSourcePresentationPreservesLogicalLinesAndLexerState(t *testing.T) {
	content := "package p\n\nvar message = `first\nsecond 世界`\n"
	d := sourceDocument("main.go", "text", content)
	presentation := buildSourcePresentation(d)
	if presentation.err != nil {
		t.Fatal(presentation.err)
	}
	if len(presentation.lines) != len(d.lines) {
		t.Fatalf("rendered lines = %d, want %d", len(presentation.lines), len(d.lines))
	}
	for i, line := range presentation.lines {
		if got := ansi.Strip(line); got != d.lines[i] {
			t.Fatalf("line %d = %q, want %q", i+1, got, d.lines[i])
		}
	}
	if !strings.Contains(presentation.lines[2], "\x1b[") || !strings.Contains(presentation.lines[3], "\x1b[") {
		t.Fatal("multiline string did not retain syntax styling across lines")
	}
}

func TestSourcePresentationNormalizesCRLFWithoutLosingRows(t *testing.T) {
	d := sourceDocument("main.go", "text", "package p\r\n\r\nvar n = 1\r\n")
	presentation := buildSourcePresentation(d)
	if presentation.err != nil {
		t.Fatal(presentation.err)
	}
	if len(presentation.lines) != len(d.lines) {
		t.Fatalf("rendered lines = %d, want %d", len(presentation.lines), len(d.lines))
	}
	for i, line := range presentation.lines {
		if got, want := ansi.Strip(line), strings.TrimSuffix(d.lines[i], "\r"); got != want {
			t.Fatalf("line %d = %q, want normalized %q", i+1, got, want)
		}
	}
}

func TestDiffPresentationClassifiesEveryLineKind(t *testing.T) {
	lines := []string{
		"diff --git a/a b/a", "index 111..222 100644", "old mode 100644", "new mode 100755",
		"rename from old", "rename to new", "similarity index 90%", "--- a/a", "+++ b/a",
		"@@ -1 +1 @@", "-old", "+new", " context", "\\ No newline at end of file",
	}
	presentation := buildSourcePresentation(sourceDocument("change.diff", "text", strings.Join(lines, "\n")))
	if presentation.language != "Diff" || len(presentation.lines) != len(lines) {
		t.Fatalf("diff presentation = %#v", presentation)
	}
	for i, line := range lines {
		if got := ansi.Strip(presentation.lines[i]); got != line {
			t.Fatalf("line %d changed to %q", i+1, got)
		}
	}
	for _, index := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 13} {
		if got, want := presentation.lines[index], shared.Theme.Review.Gutter.Render(lines[index]); got != want {
			t.Errorf("metadata line %d style mismatch", index+1)
		}
	}
	if got, want := presentation.lines[9], shared.Theme.Diff.Hunk.Render(lines[9]); got != want {
		t.Error("hunk style mismatch")
	}
	if got, want := presentation.lines[10], shared.Theme.Diff.Remove.Render(lines[10]); got != want {
		t.Error("remove style mismatch")
	}
	if got, want := presentation.lines[11], shared.Theme.Diff.Add.Render(lines[11]); got != want {
		t.Error("add style mismatch")
	}
	if got, want := presentation.lines[12], shared.Theme.Review.SyntaxBase.Render(lines[12]); got != want {
		t.Error("context line base style mismatch")
	}
}
