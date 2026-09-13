package monitor

import (
	"strings"
	"testing"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

// diffExchangeEntries builds a two-entry tool exchange whose result
// carries one Diff. Callers control the presence of OldText so the
// tests exercise both the new diff path (OldText != nil) and the
// file-creation fallback (OldText == nil).
func diffExchangeEntries(id, path string, oldText *string, newText string) []transcript.Entry {
	return []transcript.Entry{
		{
			Seq: 1, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{
				ID: id, Kind: "edit", Title: "Editing " + path,
			}}},
		},
		{
			Seq: 2, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{
				ID: id, Kind: "edit", Status: "completed",
				Content: []toolcall.Content{{Diff: &toolcall.Diff{Path: path, OldText: oldText, NewText: newText}}},
			}}},
		},
	}
}

// TestMonitorDiffSectionLabel verifies that a real diff (OldText != nil)
// renders under `Diff · <path>` and not the resulting-source
// `New code · <path>` label (FR-07.15).
func TestMonitorDiffSectionLabel(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "sample.go", &old, "a\nB\nc\n")
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	body := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(body, "Diff · sample.go") {
		t.Fatalf("expected Diff label; body:\n%s", body)
	}
	if strings.Contains(body, "New code · sample.go") {
		t.Fatalf("unexpected fallback label present:\n%s", body)
	}
}

// TestMonitorDiffFileCreationFallback asserts that OldText == nil
// preserves the historical resulting-source card verbatim: the
// `New code · <path>` label appears and no fallback hint (FR-07.16).
func TestMonitorDiffFileCreationFallback(t *testing.T) {
	entries := diffExchangeEntries("e", "brand-new.go", nil, "package new\n")
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	body := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(body, "New code · brand-new.go") {
		t.Fatalf("expected fallback label; body:\n%s", body)
	}
	if strings.Contains(body, "Diff · brand-new.go") {
		t.Fatalf("unexpected diff label on file-creation:\n%s", body)
	}
	if strings.Contains(body, "diff unavailable") {
		t.Fatalf("file-creation case must not emit fallback hint:\n%s", body)
	}
}

// TestMonitorDiffComputeFailureFallback asserts that an oversize diff
// bypasses computation and routes through the resulting-source
// fallback with the shared DiffUnavailableHint (FR-07.4, FR-07.15).
func TestMonitorDiffComputeFailureFallback(t *testing.T) {
	// 200 KiB > diffComputeMaxBytes (128 KiB).
	big := strings.Repeat("x", 200*1024)
	entries := diffExchangeEntries("e", "big.txt", &big, "x")
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	body := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(body, "diff unavailable; showing resulting source") {
		t.Fatalf("expected diff-unavailable hint:\n%s", body)
	}
	if !strings.Contains(body, "New code · big.txt") {
		t.Fatalf("expected fallback label; body:\n%s", body)
	}
	if strings.Contains(body, "Diff · big.txt") {
		t.Fatalf("oversize input must not produce diff label:\n%s", body)
	}
}

// TestMonitorDiffClampedContentHint asserts that a truncated tool-use
// block prepends shared.DiffClampedHint above the computed diff
// (FR-07.17).
func TestMonitorDiffClampedContentHint(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "clamped.go", &old, "a\nB\nc\n")
	// Mark the tool-use block truncated to simulate a write-time clamp.
	entries[0].Blocks[0].Truncated = true
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	body := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(body, "content clamped at write; diff may be incomplete") {
		t.Fatalf("expected clamped-content hint above diff:\n%s", body)
	}
	if !strings.Contains(body, "Diff · clamped.go") {
		t.Fatalf("expected diff label even with clamped block:\n%s", body)
	}
}

// TestMonitorDiffBadgeExpanded asserts the +N/-M badge appears in the
// header Meta slot on an expanded non-error edit exchange (FR-07.18).
func TestMonitorDiffBadgeExpanded(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "s.go", &old, "a\nB\nc\nD\n")
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	body := stripANSI(m.itemTranscriptBody())
	// Two adds (B on line 2, D appended), one remove (b on line 2).
	if !strings.Contains(body, "+2/-1") {
		t.Fatalf("expected +2/-1 badge in header Meta:\n%s", body)
	}
}

// TestMonitorDiffBadgeAbsentCollapsed asserts the badge does not
// appear on a collapsed exchange (FR-07.18: expanded-only).
func TestMonitorDiffBadgeAbsentCollapsed(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "s.go", &old, "a\nB\nc\n")
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 1 {
		t.Fatalf("expected one chat item; got %d", len(m.chatItems))
	}
	m.chatItemExpand[m.chatItems[0].key] = false
	body := stripANSI(m.itemTranscriptBody())
	if strings.Contains(body, "+1/-1") {
		t.Fatalf("collapsed exchange must not carry +N/-M badge:\n%s", body)
	}
}

// TestMonitorDiffBadgeAbsentErrored asserts an errored edit exchange
// carries its error hint and drops the badge (FR-07.18 precedence).
func TestMonitorDiffBadgeAbsentErrored(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "s.go", &old, "a\nB\nc\n")
	// Flip the result to failed with an error text so toolErrorHint kicks in.
	entries[1].Blocks[0].Tool.Status = "failed"
	entries[1].Blocks[0].Tool.Content = append(
		entries[1].Blocks[0].Tool.Content,
		toolcall.Content{Type: "text", Text: "compile error: no such file"},
	)
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	body := stripANSI(m.itemTranscriptBody())
	if strings.Contains(body, "+1/-1") {
		t.Fatalf("errored exchange must not carry +N/-M badge:\n%s", body)
	}
	if !strings.Contains(body, "compile error") {
		t.Fatalf("expected error hint in header Meta:\n%s", body)
	}
}

// TestMonitorDiffPersistenceOff asserts a run without a RunDir does
// not emit a diff section or crash (FR-07.22).
func TestMonitorDiffPersistenceOff(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunDir = ""
	m.chatStep = "a"
	m.reloadTranscript()
	body := stripANSI(m.chatBody())
	if strings.Contains(body, "Diff · ") {
		t.Fatalf("persistence-off body contained a diff section:\n%s", body)
	}
	if strings.ContainsAny(body, "╭╰") && !strings.Contains(body, "Persistence is off") {
		t.Fatalf("persistence-off body acquired an unexpected card frame:\n%s", body)
	}
}

// TestMonitorDiffLineRangesCoverBody asserts chatItemLineRanges span
// every rendered row of the expanded diff exchange so `n`/`N`
// block navigation lands on the intended row range (FR-07.19 line
// accounting).
func TestMonitorDiffLineRangesCoverBody(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "s.go", &old, "a\nB\nc\n")
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	body := m.itemTranscriptBody()
	if len(m.chatItems) != 1 {
		t.Fatalf("expected one item; got %d", len(m.chatItems))
	}
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[0].key}]
	if !ok {
		t.Fatalf("chatItemLineRanges missing entry for the exchange")
	}
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if rng.start < 0 || rng.end >= len(rows) {
		t.Fatalf("range %+v out of bounds for %d rows", rng, len(rows))
	}
	if rng.end < rng.start+2 {
		t.Fatalf("range too small (%+v); expected header + diff rows", rng)
	}
	// The range's last row should include the last diff row content.
	last := stripANSI(rows[rng.end])
	if !strings.Contains(last, "│") && !strings.Contains(last, "New code") {
		t.Fatalf("last row in range does not look like a diff or fallback body: %q", last)
	}
}

// TestMonitorDiffCacheStableOnRepeatedRender asserts the per-exchange
// card cache is stable across repeated calls to itemTranscriptBody
// (FR-07.21).
func TestMonitorDiffCacheStableOnRepeatedRender(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "s.go", &old, "a\nB\nc\n")
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	first := m.itemTranscriptBody()
	before := len(m.chatItemRendered)
	second := m.itemTranscriptBody()
	after := len(m.chatItemRendered)
	if first != second {
		t.Fatalf("repeated render diverged")
	}
	if before != after {
		t.Fatalf("cache grew from %d to %d rows across repeated renders", before, after)
	}
}

// TestMonitorDiffCacheEvictedOnWidthChange asserts a width change
// evicts the per-exchange cache so a diff computed at the old width
// cannot be served at the new one (FR-07.21).
func TestMonitorDiffCacheEvictedOnWidthChange(t *testing.T) {
	old := "a\nb\nc\n"
	entries := diffExchangeEntries("e", "s.go", &old, "a\nB\nc\n")
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.rebuildRenderer()
	m.setChatPage(transcript.Page{Entries: entries})
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) == 0 {
		t.Fatalf("expected some cache entries after first render")
	}
	m.transcriptInnerW = 40
	m.rebuildRenderer()
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("width rebuild retained cache: %d entries", len(m.chatItemRendered))
	}
}
