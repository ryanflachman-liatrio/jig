package monitor

import (
	"encoding/json"
	"testing"

	"jig/internal/transcript"
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
			wantDetail: "hello world",
			wantKind:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeToolCall(tt.block)
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
	got := summarizeToolCall(block)
	if got.action == got.detail {
		t.Fatalf("action and detail collapsed: %+v", got)
	}
	if got.kind == "" {
		t.Fatalf("kind not populated for a canonical tool: %+v", got)
	}
}
