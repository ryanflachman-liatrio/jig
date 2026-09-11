package runexport

import (
	"strings"
	"testing"

	"jig/internal/transcript"
)

func TestDecodeTranscriptPrefixSkipsMalformedAndContinues(t *testing.T) {
	data := `{"seq":1,"ts":"2030-01-01T00:00:00Z","role":"system","blocks":[{"type":"text","text":"ok"}]}` + "\n" +
		"not json\n" +
		`{"seq":2,"ts":"2030-01-01T00:00:01Z","role":"system","blocks":[{"type":"text","text":"still ok"}]}` + "\n"
	lines, gaps := decodeTranscriptPrefix(strings.NewReader(data), "step-0001")
	if len(lines) != 2 {
		t.Fatalf("decoded %d lines, want 2 (malformed line skipped, not fatal)", len(lines))
	}
	if len(gaps) != 1 || gaps[0].Reason != GapMalformedRecord {
		t.Fatalf("gaps = %+v, want one malformed_record gap", gaps)
	}
}

func TestDecodeTranscriptPrefixTornFinalLine(t *testing.T) {
	data := `{"seq":1,"ts":"2030-01-01T00:00:00Z","role":"system","blocks":[]}` + "\n" +
		`{"seq":2,"ts":"2030-01-01T00:00:01Z","role":"system"` // torn, no newline
	lines, gaps := decodeTranscriptPrefix(strings.NewReader(data), "step-0001")
	if len(lines) != 1 {
		t.Fatalf("decoded %d lines, want 1", len(lines))
	}
	if len(gaps) != 1 || gaps[0].Reason != GapTornRecord {
		t.Fatalf("gaps = %+v, want one torn_record gap", gaps)
	}
}

func TestProjectTranscriptEntryOmitsThinkingAndUnknownBlocks(t *testing.T) {
	aliases := newAliasTable()
	anchor := &timeAnchor{}
	identity := func(s string) string { return s }
	entry := transcript.Entry{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
		{Type: transcript.BlockThinking, Text: "secret reasoning"},
		{Type: transcript.BlockType("attachment")},
		{Type: transcript.BlockText, Text: "hello"},
	}}
	rec := projectTranscriptEntry("step-0001", entry, anchor, aliases, "scope", identity)
	if len(rec.Blocks) != 3 {
		t.Fatalf("Blocks = %+v, want 3", rec.Blocks)
	}
	if rec.Blocks[0].Type != "thinking" || rec.Blocks[0].Text != "" || rec.Blocks[0].Omitted == "" {
		t.Fatalf("thinking block = %+v, want content-free omission marker", rec.Blocks[0])
	}
	if rec.Blocks[1].Type != "unknown" || rec.Blocks[1].Omitted == "" {
		t.Fatalf("unknown block = %+v, want fixed unknown type with omission marker", rec.Blocks[1])
	}
	if rec.Blocks[2].Text != "hello" {
		t.Fatalf("text block = %+v, want passthrough text", rec.Blocks[2])
	}
}

func TestSanitizeJSONPayloadHandlesMalformedAndNested(t *testing.T) {
	identity := func(s string) string { return s }
	if got := sanitizeJSONPayload([]byte("{not valid json"), identity); !strings.Contains(got, "omitted") {
		t.Fatalf("sanitizeJSONPayload(malformed) = %q, want an omission marker", got)
	}
	sanitize := func(s string) string { return strings.ReplaceAll(s, "secret", "[REDACTED]") }
	got := sanitizeJSONPayload([]byte(`{"outer":{"inner":"has secret value"}}`), sanitize)
	if strings.Contains(got, "secret") {
		t.Fatalf("sanitizeJSONPayload did not sanitize a nested value: %q", got)
	}
}
