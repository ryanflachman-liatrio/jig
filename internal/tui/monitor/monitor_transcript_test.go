package monitor

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

func TestSummarizeToolCall(t *testing.T) {
	tests := []struct {
		name        string
		tool        string
		input       any
		wantLabel   string
		wantPreview string
	}{
		{name: "read", tool: "Read", input: map[string]any{"file_path": "/workspace/internal/tui/monitor/monitor.go"}, wantLabel: "◈ Read", wantPreview: "· monitor.go"},
		{name: "edit", tool: "Edit", input: map[string]any{"file_path": `C:\src\monitor.go`}, wantLabel: "◈ Edit", wantPreview: "· monitor.go"},
		{name: "write", tool: "Write", input: map[string]any{"file_path": "/tmp/report.md"}, wantLabel: "◈ Write", wantPreview: "· report.md"},
		{name: "notebook", tool: "NotebookEdit", input: map[string]any{"notebook_path": "/tmp/analysis.ipynb"}, wantLabel: "◈ Edit notebook", wantPreview: "· analysis.ipynb"},
		{name: "glob", tool: "Glob", input: map[string]any{"pattern": "**/*.go"}, wantLabel: "⌕ Find", wantPreview: "· **/*.go"},
		{name: "grep", tool: "Grep", input: map[string]any{"pattern": "writeBlock"}, wantLabel: "⌕ Search", wantPreview: "· writeBlock"},
		{name: "bash", tool: "Bash", input: map[string]any{"command": "go test ./..."}, wantLabel: "$ Run", wantPreview: "· go test ./..."},
		{name: "web search", tool: "WebSearch", input: map[string]any{"query": "tool summaries"}, wantLabel: "↗ Search web", wantPreview: "· tool summaries"},
		{name: "web fetch", tool: "WebFetch", input: map[string]any{"url": "https://docs.anthropic.com/en/docs/agents"}, wantLabel: "↗ Fetch", wantPreview: "· docs.anthropic.com"},
		{name: "task", tool: "Task", input: map[string]any{"subagent_type": "explore", "description": "inspect monitor"}, wantLabel: "⊙ Explore", wantPreview: "· inspect monitor"},
		{name: "todo write", tool: "TodoWrite", input: map[string]any{"todos": []any{map[string]any{"content": "one"}, map[string]any{"content": "two"}}}, wantLabel: "⊙ Update", wantPreview: "· 2 tasks"},
		{name: "ask question MCP name", tool: "mcp__jig__AskUserQuestion", input: map[string]any{"questions": []any{map[string]any{"question": "Which style?"}}}, wantLabel: "? Ask", wantPreview: "· Which style?"},
		{name: "skill", tool: "Skill", input: map[string]any{"skill": "plan"}, wantLabel: "⊙ Use skill", wantPreview: "· plan"},
		{name: "unknown MCP tool", tool: "mcp__github__create_issue", input: map[string]any{"title": "Broken monitor"}, wantLabel: shared.IconToolCall + " create_issue", wantPreview: "· Broken monitor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeToolCall(transcript.Block{
				Name:  tt.tool,
				Input: rawJSON(t, tt.input),
			})
			if got.label != tt.wantLabel || got.preview != tt.wantPreview {
				t.Fatalf("summarizeToolCall() = {%q, %q}, want {%q, %q}", got.label, got.preview, tt.wantLabel, tt.wantPreview)
			}
		})
	}
}

func TestSummarizeKnownToolWithMalformedInputDoesNotLeakRawJSON(t *testing.T) {
	got := summarizeToolCall(transcript.Block{Name: "Read", Input: json.RawMessage(`{"file_path":`)})
	if got.label != "◈ Read" || got.preview != "" {
		t.Fatalf("summarizeToolCall() = {%q, %q}, want label-only Read summary", got.label, got.preview)
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
