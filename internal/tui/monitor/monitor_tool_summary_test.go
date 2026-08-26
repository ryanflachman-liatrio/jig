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
	}{
		{
			name:       "read path",
			block:      transcript.Block{Name: "Read", Input: json.RawMessage(`{"file_path":"/tmp/project/main.go"}`)},
			wantIcon:   "◈",
			wantAction: "Read",
			wantDetail: "main.go",
		},
		{
			name:       "unknown strips terminal controls",
			block:      transcript.Block{Name: "vendor__weird\x1b[31m tool", Input: json.RawMessage(`{"description":"hello\nworld"}`)},
			wantIcon:   "▸",
			wantAction: "weird [31m tool",
			wantDetail: "hello world",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeToolCall(tt.block)
			if got.icon != tt.wantIcon || got.action != tt.wantAction || got.detail != tt.wantDetail {
				t.Fatalf("summary = %+v, want icon=%q action=%q detail=%q", got, tt.wantIcon, tt.wantAction, tt.wantDetail)
			}
		})
	}
}
