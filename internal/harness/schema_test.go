package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInjectSchemaPrompt(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":     map[string]any{"type": "boolean"},
			"status": map[string]any{"type": "string", "enum": []any{"succeeded", "failed"}},
		},
		"required": []any{"ok", "status"},
	}
	got := injectSchemaPrompt("Do the thing.", schema)
	if !strings.Contains(got, "Do the thing.") {
		t.Errorf("original prompt missing: %q", got)
	}
	// Human-readable field list.
	if !strings.Contains(got, "- ok:") {
		t.Errorf("field 'ok' missing from field list: %q", got)
	}
	if !strings.Contains(got, `"succeeded"`) {
		t.Errorf("enum values missing: %q", got)
	}
	// JSON Schema present for precision.
	if !strings.Contains(got, `"type"`) {
		t.Errorf("JSON Schema missing: %q", got)
	}
	// Instruction to output only JSON.
	if !strings.Contains(got, "ONLY a valid JSON") {
		t.Errorf("JSON-only instruction missing: %q", got)
	}
}

func TestRetrySchemaPrompt(t *testing.T) {
	schema := map[string]any{"type": "object"}
	got := retrySchemaPrompt(schema)
	if !strings.Contains(got, "not valid JSON") {
		t.Errorf("retry prompt missing correction language: %q", got)
	}
	if !strings.Contains(got, `"type"`) {
		t.Errorf("JSON Schema missing from retry prompt: %q", got)
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		want      string
		wantValid bool
	}{
		{
			name:      "plain JSON",
			text:      `{"ok":true}`,
			want:      `{"ok":true}`,
			wantValid: true,
		},
		{
			name:      "whitespace",
			text:      "  \n{\"ok\":true}\n  ",
			want:      `{"ok":true}`,
			wantValid: true,
		},
		{
			name:      "json fence",
			text:      "```json\n{\"ok\":true}\n```",
			want:      `{"ok":true}`,
			wantValid: true,
		},
		{
			name:      "plain fence",
			text:      "```\n{\"ok\":true}\n```",
			want:      `{"ok":true}`,
			wantValid: true,
		},
		{
			name:      "embedded in prose",
			text:      "Here is my response:\n{\"ok\":true}",
			want:      `{"ok":true}`,
			wantValid: true,
		},
		{
			name:      "prose before fenced JSON",
			text:      "Sure, here you go:\n```json\n{\"ok\":true}\n```",
			want:      `{"ok":true}`,
			wantValid: true,
		},
		{
			name:      "invalid returned as-is",
			text:      "not json at all",
			want:      "not json at all",
			wantValid: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.text)
			if string(got) != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
			if tt.wantValid && !json.Valid(got) {
				t.Errorf("result not valid JSON: %q", got)
			}
		})
	}
}

func TestDescribeSchemaNode(t *testing.T) {
	tests := []struct {
		name string
		node map[string]any
		want string
	}{
		{"string type", map[string]any{"type": "string"}, "string"},
		{"boolean type", map[string]any{"type": "boolean"}, "boolean (true or false)"},
		{"number type", map[string]any{"type": "number"}, "number"},
		{"enum", map[string]any{"enum": []any{"a", "b"}}, `one of "a", "b"`},
		{"array of string", map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "array of string"},
		{"nil node", nil, "any value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeSchemaNode(tt.node)
			if got != tt.want {
				t.Errorf("describeSchemaNode() = %q, want %q", got, tt.want)
			}
		})
	}
}
