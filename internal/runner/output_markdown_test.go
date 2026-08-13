package runner

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStructuredToMarkdown_BaseFields(t *testing.T) {
	input := map[string]any{
		"status":      "succeeded",
		"confidence":  "high",
		"summary":     "Everything looks good.",
		"assumptions": []any{"Go 1.25 toolchain is available", "mise is configured"},
		"issues":      []any{},
	}
	raw, _ := json.Marshal(input)
	md := structuredToMarkdown(raw)

	checks := []struct {
		section string
		content string
	}{
		{"## Status", "succeeded"},
		{"## Confidence", "high"},
		{"## Summary", "Everything looks good."},
		{"## Assumptions", "Go 1.25 toolchain is available"},
	}
	for _, c := range checks {
		if !strings.Contains(md, c.section) {
			t.Errorf("missing section %q in output", c.section)
		}
		if !strings.Contains(md, c.content) {
			t.Errorf("missing content %q in output", c.content)
		}
	}
	// Empty issues slice should be omitted.
	if strings.Contains(md, "## Issues") {
		t.Error("expected empty Issues section to be omitted")
	}
}

func TestStructuredToMarkdown_SectionOrder(t *testing.T) {
	input := map[string]any{
		"status":      "partial",
		"confidence":  "medium",
		"summary":     "Partial completion.",
		"assumptions": []any{"one assumption"},
	}
	raw, _ := json.Marshal(input)
	md := structuredToMarkdown(raw)

	positions := []string{"## Status", "## Confidence", "## Summary", "## Assumptions"}
	last := -1
	for _, heading := range positions {
		idx := strings.Index(md, heading)
		if idx == -1 {
			t.Errorf("missing heading %q", heading)
			continue
		}
		if idx <= last {
			t.Errorf("heading %q is out of order (pos %d, previous was %d)", heading, idx, last)
		}
		last = idx
	}
}

func TestStructuredToMarkdown_CustomFields(t *testing.T) {
	input := map[string]any{
		"status":         "succeeded",
		"summary":        "Done.",
		"affected_files": []any{"main.go", "agent.go"},
		"test_coverage":  float64(87),
	}
	raw, _ := json.Marshal(input)
	md := structuredToMarkdown(raw)

	if !strings.Contains(md, "## Affected Files") {
		t.Error("expected humanized heading '## Affected Files'")
	}
	if !strings.Contains(md, "- main.go") {
		t.Error("expected list item '- main.go'")
	}
	if !strings.Contains(md, "## Test Coverage") {
		t.Error("expected humanized heading '## Test Coverage'")
	}
	if !strings.Contains(md, "87") {
		t.Error("expected numeric value '87'")
	}

	// Custom fields must appear after base fields.
	summaryIdx := strings.Index(md, "## Summary")
	affectedIdx := strings.Index(md, "## Affected Files")
	if summaryIdx == -1 || affectedIdx == -1 {
		t.Fatal("missing expected sections")
	}
	if affectedIdx <= summaryIdx {
		t.Error("custom field '## Affected Files' should appear after base fields")
	}
}

func TestStructuredToMarkdown_EmptyInput(t *testing.T) {
	if got := structuredToMarkdown(nil); got != "" {
		t.Errorf("expected empty string for nil input, got %q", got)
	}
	if got := structuredToMarkdown(json.RawMessage(`{}`)); got != "" {
		t.Errorf("expected empty string for empty object, got %q", got)
	}
}

func TestHumanize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"raw_result", "Raw Result"},
		{"affected_files", "Affected Files"},
		{"status", "Status"},
		{"test_coverage_pct", "Test Coverage Pct"},
	}
	for _, c := range cases {
		if got := humanize(c.in); got != c.want {
			t.Errorf("humanize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
