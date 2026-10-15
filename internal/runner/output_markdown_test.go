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
		{"research_report", "Research Report"},
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

func TestAgentOutputMarkdown(t *testing.T) {
	base := json.RawMessage(`{"summary":"done","status":"succeeded"}`)
	documents := json.RawMessage(`{"summary":"done","status":"succeeded","report":"# Findings\n\nDetailed evidence.","plan":"# Plan\n\nNext steps."}`)
	for _, tt := range []struct {
		name string
		text string
		raw  json.RawMessage
		want string
	}{
		{"prose only", "# Findings\n\nEvidence.\n", nil, "# Findings\n\nEvidence.\n"},
		{"prose with base result", "# Findings\n\nEvidence.", base, "# Findings\n\nEvidence."},
		{"matching envelope", "# Findings\n\nEvidence.\n\n```json\n{\"status\":\"succeeded\",\"summary\":\"done\"}\n```\n", base, "# Findings\n\nEvidence."},
		{"structured documents", "```json\n" + string(documents) + "\n```", documents, "## Plan\n\n# Plan\n\nNext steps.\n\n## Report\n\n# Findings\n\nDetailed evidence.\n\n"},
		{"bare structured documents", string(documents), documents, "## Plan\n\n# Plan\n\nNext steps.\n\n## Report\n\n# Findings\n\nDetailed evidence.\n\n"},
		{"structured only", "", documents, "## Plan\n\n# Plan\n\nNext steps.\n\n## Report\n\n# Findings\n\nDetailed evidence.\n\n"},
		{"prose and documents", "Explanation.\n\n```json\n" + string(documents) + "\n```", documents, "Explanation.\n\n## Plan\n\n# Plan\n\nNext steps.\n\n## Report\n\n# Findings\n\nDetailed evidence.\n\n"},
		{"base fallback", string(base), base, "## Status\n\nsucceeded\n\n## Summary\n\ndone\n\n"},
		{"empty", "", nil, ""},
		{"invalid structured preserves prose", "Evidence.", json.RawMessage(`{`), "Evidence."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentOutputMarkdown(tt.text, tt.raw); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripOutputEnvelopePreservesExamples(t *testing.T) {
	raw := json.RawMessage(`{"summary":"done"}`)
	for _, text := range []string{
		"Example:\n```json\n{\"summary\":\"example\"}\n```",
		"Example:\n```json\n" + string(raw) + "\n```\nExplanation follows.",
		"Example:\n```json\n{invalid}\n```",
		"Example:\n```json\n" + string(raw),
		"Example uses ```json inline.\n" + string(raw) + "\n```",
	} {
		if got := stripOutputEnvelope(text, raw); got != text {
			t.Errorf("changed example: %q -> %q", text, got)
		}
	}
	example := "Example:\n```json\n{\"value\":42}\n```\n\nConclusion."
	text := example + "\n\n```json\r\n" + string(raw) + "\r\n```\r\n"
	if got := stripOutputEnvelope(text, raw); got != example {
		t.Errorf("earlier example lost: %q", got)
	}
	// Comparing with float64 would incorrectly consider these distinct integers equal.
	text = "```json\n{\"value\":9007199254740992}\n```"
	if got := stripOutputEnvelope(text, json.RawMessage(`{"value":9007199254740993}`)); got != text {
		t.Errorf("different numeric envelope removed: %q", got)
	}
}

func TestMetadataToMarkdownExcludesDeclaredDocuments(t *testing.T) {
	raw := json.RawMessage(`{"summary":"Need a source","status":"blocked","block_reason":"Missing dataset","report":"Full research document"}`)
	md := metadataToMarkdown(raw)
	for _, want := range []string{"## Status", "blocked", "## Summary", "Need a source", "## Block Reason", "Missing dataset"} {
		if !strings.Contains(md, want) {
			t.Errorf("metadata missing %q", want)
		}
	}
	if strings.Contains(md, "Full research document") || strings.Contains(md, "## Report") {
		t.Errorf("metadata contains declared output: %s", md)
	}
}

func TestAgentOutputMarkdownPreservesNumbers(t *testing.T) {
	raw := json.RawMessage(`{"status":"succeeded","identifier":9007199254740993,"measurements":[0.1234567890123456789],"details":{"total":9007199254740995}}`)
	got := agentOutputMarkdown(string(raw), raw)
	for _, want := range []string{"9007199254740993", "0.1234567890123456789", "9007199254740995"} {
		if !strings.Contains(got, want) {
			t.Errorf("readable output rounded %s: %s", want, got)
		}
	}
}
