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
	got, hidden := boundTranscriptDetail(strings.Join(lines, "\n"), 80, detailAnchorHead)
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
	got, _ := boundTranscriptDetail(strings.Repeat("世", transcriptDetailBytes), 10000, detailAnchorHead)
	if len(got) > transcriptDetailBytes {
		t.Fatalf("got %d bytes, want <= %d", len(got), transcriptDetailBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("byte boundary split UTF-8")
	}
}

// TestBoundTranscriptDetailHeadAnchor locks the pre-slice-06 head+tail
// behaviour byte-for-byte: for 16 rows at width 80, the head anchor keeps
// the first (transcriptDetailRows - transcriptDetailTailRows) rows and the
// final transcriptDetailTailRows rows, dropping the middle. The distinctive
// first row survives; a middle row does not.
func TestBoundTranscriptDetailHeadAnchor(t *testing.T) {
	lines := make([]string, 16)
	for i := range lines {
		lines[i] = "row-" + string(rune('a'+i))
	}
	lines[0] = "FIRST"
	lines[15] = "LAST"
	got, hidden := boundTranscriptDetail(strings.Join(lines, "\n"), 80, detailAnchorHead)
	if hidden != 4 {
		t.Fatalf("head anchor hidden = %d, want 4", hidden)
	}
	if !strings.Contains(got, "FIRST") || !strings.Contains(got, "LAST") {
		t.Fatalf("head anchor missing distinctive first or last row:\n%s", got)
	}
	// Rows 10, 11, 12 (indices 9, 10, 11) fall inside the elided middle.
	if strings.Contains(got, "row-k") || strings.Contains(got, "row-l") {
		t.Fatalf("head anchor kept a middle row (indices 10 or 11):\n%s", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("head anchor produced invalid UTF-8")
	}
}

// TestBoundTranscriptDetailTailAnchor locks the running-content behaviour:
// the tail anchor keeps only the newest transcriptDetailRows rows. The
// distinctive last row survives; the distinctive first row does not.
func TestBoundTranscriptDetailTailAnchor(t *testing.T) {
	lines := make([]string, 16)
	for i := range lines {
		lines[i] = "row-" + string(rune('a'+i))
	}
	lines[0] = "FIRST"
	lines[15] = "LAST"
	got, hidden := boundTranscriptDetail(strings.Join(lines, "\n"), 80, detailAnchorTail)
	if hidden != 4 {
		t.Fatalf("tail anchor hidden = %d, want 4", hidden)
	}
	if !strings.Contains(got, "LAST") {
		t.Fatalf("tail anchor dropped distinctive last row:\n%s", got)
	}
	if strings.Contains(got, "FIRST") {
		t.Fatalf("tail anchor kept distinctive first row (should be dropped):\n%s", got)
	}
	rows := strings.Split(got, "\n")
	if len(rows) != transcriptDetailRows {
		t.Fatalf("tail anchor kept %d rows, want %d", len(rows), transcriptDetailRows)
	}
	if !utf8.ValidString(got) {
		t.Fatal("tail anchor produced invalid UTF-8")
	}
}

// TestBoundTranscriptDetailBothAnchorsUnderRowLimit locks the passthrough:
// when the row count is at or below transcriptDetailRows, both anchors
// return the content unchanged with hidden == 0.
func TestBoundTranscriptDetailBothAnchorsUnderRowLimit(t *testing.T) {
	lines := make([]string, transcriptDetailRows)
	for i := range lines {
		lines[i] = "row-" + string(rune('a'+i))
	}
	content := strings.Join(lines, "\n")
	for _, anchor := range []detailAnchor{detailAnchorHead, detailAnchorTail} {
		got, hidden := boundTranscriptDetail(content, 80, anchor)
		if hidden != 0 {
			t.Fatalf("anchor %v: hidden = %d, want 0", anchor, hidden)
		}
		if got != content {
			t.Fatalf("anchor %v: content was rewritten:\n want %q\n got  %q", anchor, content, got)
		}
	}
}
