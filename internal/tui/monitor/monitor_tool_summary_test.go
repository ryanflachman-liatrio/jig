package monitor

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

func TestSummarizeToolCallUsesSemanticFallbackAndSanitizesControls(t *testing.T) {
	tests := []struct {
		name       string
		block      transcript.Block
		wantIcon   string
		wantAction string
		wantDetail string
		wantKind   string
	}{
		{
			name:       "read path",
			block:      transcript.Block{Name: "Read", Input: json.RawMessage(`{"file_path":"/tmp/project/main.go"}`)},
			wantIcon:   "◈",
			wantAction: "Read",
			wantDetail: "main.go",
			wantKind:   "read",
		},
		{
			name:       "unknown strips terminal controls",
			block:      transcript.Block{Name: "vendor__weird\x1b[31m tool", Input: json.RawMessage(`{"description":"hello\nworld"}`)},
			wantIcon:   "▸",
			wantAction: "weird [31m tool",
			wantDetail: `description="hello\nworld"`,
			wantKind:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeToolCall(tt.block, 80)
			if got.icon != tt.wantIcon || got.action != tt.wantAction || got.detail != tt.wantDetail || got.kind != tt.wantKind {
				t.Fatalf("summary = %+v, want icon=%q action=%q detail=%q kind=%q",
					got, tt.wantIcon, tt.wantAction, tt.wantDetail, tt.wantKind)
			}
		})
	}
}

// FR-02.13: summarizeActivity keeps `icon`, `action`, and `detail` distinct;
// the pre-fused `label`/`preview` fields have been removed. This test locks
// the shape callers depend on and documents that the four fields (adding
// `kind`) are the only presentation contract.
func TestSummarizeActivityKeepsSlotsDistinct(t *testing.T) {
	block := transcript.Block{Name: "Read", Input: json.RawMessage(`{"file_path":"/tmp/project/main.go"}`)}
	got := summarizeToolCall(block, 80)
	if got.action == got.detail {
		t.Fatalf("action and detail collapsed: %+v", got)
	}
	if got.kind == "" {
		t.Fatalf("kind not populated for a canonical tool: %+v", got)
	}
}

// TestSummarizeActivityUnmappedToolUsesMultiKeyPreview locks in that an
// unmapped/MCP-style activity (no per-kind mapping) now shows more than one
// argument, replacing the old single-arbitrary-value primaryToolArg fallback
// (epic slice 15, FR-15.1/FR-15.2).
func TestSummarizeActivityUnmappedToolUsesMultiKeyPreview(t *testing.T) {
	block := transcript.Block{
		Name: "mcp__example__custom_tool",
		Input: json.RawMessage(`{
			"owner": "acme",
			"repo": "widgets",
			"issue_number": 42
		}`),
	}
	got := summarizeToolCall(block, 80)
	for _, key := range []string{"owner", "repo", "issue_number"} {
		if !strings.Contains(got.detail, key+"=") {
			t.Errorf("detail %q missing key %q", got.detail, key)
		}
	}
}

// TestSummarizeActivityUnmappedToolBudgetsAgainstRealWidth demonstrates the
// preview is budgeted before render (fair-share), not clipped after: the
// same fixture rendered at a narrow width produces a shorter, in-budget
// preview than at a wide width.
func TestSummarizeActivityUnmappedToolBudgetsAgainstRealWidth(t *testing.T) {
	block := transcript.Block{
		Name: "mcp__example__custom_tool",
		Input: json.RawMessage(`{
			"owner": "acme",
			"repo": "widgets",
			"issue_number": 42,
			"labels": ["bug", "P1", "needs-triage"]
		}`),
	}
	narrow := summarizeToolCall(block, 15)
	wide := summarizeToolCall(block, 200)

	if w := lipgloss.Width(narrow.detail); w > 15 {
		t.Errorf("narrow width: rendered width %d exceeds budget 15 (%q)", w, narrow.detail)
	}
	if w := lipgloss.Width(wide.detail); w > 200 {
		t.Errorf("wide width: rendered width %d exceeds budget 200 (%q)", w, wide.detail)
	}
	if lipgloss.Width(narrow.detail) >= lipgloss.Width(wide.detail) {
		t.Errorf("expected narrow preview shorter than wide preview: narrow=%q wide=%q", narrow.detail, wide.detail)
	}
}

// TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta demonstrates
// grep's existing curated pattern detail stays in Description, while its
// secondary arguments (path, case, gitignore) populate the new Meta slot
// (epic slice 15 Unit 2, second proof artifact).
func TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta(t *testing.T) {
	block := transcript.Block{
		Name: "Grep",
		Input: json.RawMessage(`{
			"pattern": "TODO",
			"path": "internal/tui",
			"case": true
		}`),
	}
	got := summarizeToolCall(block, 80)
	if got.detail != "TODO" {
		t.Errorf("detail = %q, want unchanged curated pattern %q", got.detail, "TODO")
	}
	if len(got.meta) == 0 {
		t.Fatalf("meta is empty, want secondary grep arguments")
	}
	joined := strings.Join(got.meta, " ")
	if !strings.Contains(joined, "path=") || !strings.Contains(joined, "case=") {
		t.Errorf("meta = %q, want path= and case= entries", joined)
	}
}

func TestFormatArgsInline(t *testing.T) {
	rawStr := func(s string) json.RawMessage {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %q: %v", s, err)
		}
		return json.RawMessage(b)
	}

	t.Run("long value does not starve trailing keys", func(t *testing.T) {
		args := map[string]json.RawMessage{
			"content": rawStr(strings.Repeat("x", 500)),
			"path":    rawStr("src/foo.ts"),
			"limit":   json.RawMessage("50"),
			"mode":    rawStr("strict"),
		}
		got := formatArgsInline(args, 60)
		for _, key := range []string{"content", "path", "limit", "mode"} {
			if !strings.Contains(got, key+"=") {
				t.Errorf("missing key %q in %q", key, got)
			}
		}
		if w := lipgloss.Width(got); w > 60 {
			t.Errorf("width = %d, want <= 60 (%q)", w, got)
		}
	})

	t.Run("budget exhaustion appends an ellipsis", func(t *testing.T) {
		args := map[string]json.RawMessage{
			"aaaaaaaaaa": rawStr(strings.Repeat("y", 200)),
			"bbbbbbbbbb": rawStr(strings.Repeat("z", 200)),
		}
		got := formatArgsInline(args, 20)
		if !strings.HasSuffix(got, shared.EllipsisGlyph) {
			t.Fatalf("got %q, want suffix %q", got, shared.EllipsisGlyph)
		}
		if w := lipgloss.Width(got); w > 20 {
			t.Errorf("width = %d, want <= 20 (%q)", w, got)
		}
	})

	t.Run("array and object arguments render as counts", func(t *testing.T) {
		args := map[string]json.RawMessage{
			"items": json.RawMessage(`[1,2,3]`),
			"opts":  json.RawMessage(`{"a":1,"b":2}`),
		}
		got := formatArgsInline(args, 80)
		if !strings.Contains(got, "items=[3 items]") {
			t.Errorf("got %q, want to contain items=[3 items]", got)
		}
		if !strings.Contains(got, "opts={2 keys}") {
			t.Errorf("got %q, want to contain opts={2 keys}", got)
		}
	})

	t.Run("newline and tab are escaped and output stays one line", func(t *testing.T) {
		args := map[string]json.RawMessage{
			"note": rawStr("line one\nline two\tend"),
		}
		got := formatArgsInline(args, 80)
		if strings.ContainsAny(got, "\n\t") {
			t.Fatalf("output contains a raw control char: %q", got)
		}
		want := `note="line one\nline two\tend"`
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty and noise-only arguments produce empty output", func(t *testing.T) {
		if got := formatArgsInline(nil, 80); got != "" {
			t.Errorf("nil args: got %q, want empty", got)
		}
		if got := formatArgsInline(map[string]json.RawMessage{}, 80); got != "" {
			t.Errorf("empty args: got %q, want empty", got)
		}
		noise := map[string]json.RawMessage{"__partialJson": json.RawMessage(`true`)}
		if got := formatArgsInline(noise, 80); got != "" {
			t.Errorf("noise-only args: got %q, want empty", got)
		}
	})

	t.Run("secret-shaped key is redacted", func(t *testing.T) {
		args := map[string]json.RawMessage{
			"api_key": rawStr("sk-super-secret-value"),
		}
		got := formatArgsInline(args, 80)
		want := "api_key=<redacted>"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		if strings.Contains(got, "sk-super-secret-value") {
			t.Fatalf("secret value leaked into preview: %q", got)
		}
	})

	t.Run("width budget is never exceeded, including extreme narrow widths", func(t *testing.T) {
		args := map[string]json.RawMessage{
			"path":    rawStr("internal/tui/monitor/monitor_tool_summary.go"),
			"pattern": rawStr("formatArgsInline"),
			"case":    json.RawMessage(`true`),
			"content": rawStr(strings.Repeat("z", 1000)),
		}
		for _, width := range []int{0, 1, 2, 3, 5, 10, 20, 40, 80, 200} {
			got := formatArgsInline(args, width)
			if w := lipgloss.Width(got); w > width {
				t.Errorf("width %d: rendered width %d exceeds budget (%q)", width, w, got)
			}
		}
	})

	t.Run("deterministic across repeated calls on the same unordered map", func(t *testing.T) {
		args := map[string]json.RawMessage{
			"zeta":  rawStr("z"),
			"alpha": rawStr("a"),
			"path":  rawStr("p"),
			"mu":    rawStr("m"),
		}
		first := formatArgsInline(args, 80)
		for i := 0; i < 20; i++ {
			if got := formatArgsInline(args, 80); got != first {
				t.Fatalf("iteration %d: got %q, want %q", i, got, first)
			}
		}
	})
}
