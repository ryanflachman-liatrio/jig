package monitor

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundTranscriptDetailPreservesUTF8AndTailRows(t *testing.T) {
	lines := make([]string, 16)
	for i := range lines {
		lines[i] = "row " + string(rune('a'+i)) + " 世界"
	}
	lines[15] = "final status: failed"
	got, hidden := boundTranscriptDetail(strings.Join(lines, "\n"), 80)
	if !utf8.ValidString(got) {
		t.Fatal("bounded detail is not valid UTF-8")
	}
	if hidden != 4 {
		t.Fatalf("hidden rows = %d, want 4", hidden)
	}
	if !strings.Contains(got, "final status: failed") {
		t.Fatalf("tail row missing from %q", got)
	}
}

func TestBoundTranscriptDetailLimitsBytesBeforeRows(t *testing.T) {
	got, _ := boundTranscriptDetail(strings.Repeat("世", transcriptDetailBytes), 10000)
	if len(got) > transcriptDetailBytes {
		t.Fatalf("got %d bytes, want <= %d", len(got), transcriptDetailBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("byte boundary split UTF-8")
	}
}
