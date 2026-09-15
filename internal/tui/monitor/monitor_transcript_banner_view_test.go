package monitor

// Integration tests for the boundary banner as it flows through
// itemTranscriptBody (omp-transcript-parity slice 12). Verifies:
//   - all three banner labels appear in a page that steps through
//     retry → iteration → generation transitions (FR-12.2, FR-12.3);
//   - banner rows fold into the closing item's line range so n/N
//     block navigation still lands on items (FR-12.5);
//   - a page whose first item's coord is non-zero renders a
//     page-edge banner above that item, sitting *outside* any
//     item's line range so the item's start still points at its
//     body (FR-12.6);
//   - the coexistence matrix with slice 11 (both rows, one row,
//     neither row) matches docs/plans/omp-slice-12-boundary-banners.md
//     §Layout matrix.

import (
	"strings"
	"testing"

	"jig/internal/transcript"
)

// fourCoordPage returns a fixture that steps through every banner
// variant in one page: retry within an attempt, iteration rewind,
// then manual generation reset.
func fourCoordPage() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn A"}}},
		{Seq: 2, Generation: 0, Iteration: 0, Attempt: 1, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn B"}}},
		{Seq: 3, Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn C"}}},
		{Seq: 4, Generation: 1, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn D"}}},
	}}
}

func TestBoundaryBannerAllThreeLabelsInOrder(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(fourCoordPage())
	body := stripANSI(m.itemTranscriptBody())

	// The three interior banners are ordered by the transitions:
	// A→B (attempt bump), B→C (iteration bump), C→D (generation
	// reset). Locate each by its label + the item that follows it.
	idxRetry := strings.Index(body, "retry 1")
	idxIter := strings.Index(body, "iteration 2")
	idxReset := strings.Index(body, "reset 2")
	idxTurnB := strings.Index(body, "turn B")
	idxTurnC := strings.Index(body, "turn C")
	idxTurnD := strings.Index(body, "turn D")

	if idxRetry < 0 || idxIter < 0 || idxReset < 0 {
		t.Fatalf("missing banner label(s): retry=%d iter=%d reset=%d\n%s",
			idxRetry, idxIter, idxReset, body)
	}
	if !(idxRetry < idxTurnB && idxTurnB < idxIter && idxIter < idxTurnC && idxTurnC < idxReset && idxReset < idxTurnD) {
		t.Fatalf("banners are not interleaved with their arriving items:\n"+
			"retry=%d turnB=%d iter=%d turnC=%d reset=%d turnD=%d\n%s",
			idxRetry, idxTurnB, idxIter, idxTurnC, idxReset, idxTurnD, body)
	}
}

func TestBoundaryBannerFoldsIntoClosingItemLineRange(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(fourCoordPage())
	body := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")

	// The retry banner (turn A → turn B, attempt bump) must sit
	// inside the folded range of chatItems[0] (turn A). The banner
	// row is the *last* row of the fold per plan §Row grammar.
	prev := m.chatItems[0]
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: prev.key}]
	if !ok {
		t.Fatalf("turn A has no line range")
	}
	if rng.end < rng.start || rng.end >= len(rows) {
		t.Fatalf("turn A range %+v out of bounds for %d rows", rng, len(rows))
	}
	rangePlain := stripANSI(strings.Join(rows[rng.start:rng.end+1], "\n"))
	if !strings.Contains(rangePlain, "retry 1") {
		t.Fatalf("retry banner not folded into turn A's range:\n%s", rangePlain)
	}
	// Turn B's start must skip past the banner (n/N navigation
	// lands on the item body, not the banner).
	second := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	if second.start <= rng.end {
		t.Fatalf("turn B start=%d must be after turn A end=%d (banner fold breach)", second.start, rng.end)
	}
	if got := stripANSI(rows[second.start]); !strings.Contains(got, "turn B") &&
		// glamour may render a "User" header above prose; body must
		// still appear within the item's own range, not the banner.
		!strings.Contains(got, "User") {
		endRow := second.end + 1
		if endRow > len(rows) {
			endRow = len(rows)
		}
		t.Fatalf("turn B does not begin at row=%d: %q\nrange rows:\n%s",
			second.start, got, strings.Join(rows[second.start:endRow], "\n"))
	}
}

func TestBoundaryBannerPageEdge(t *testing.T) {
	// A page whose first item's coord is non-zero simulates a
	// scroll-loaded window that begins mid-run (FR-12.6). The banner
	// announces the arriving coord above the first rendered item and
	// sits *outside* any chatItemLineRanges entry.
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 2, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "starts on iteration 2"}}},
	}})
	body := m.itemTranscriptBody()
	plain := stripANSI(body)
	if !strings.Contains(plain, "iteration 3") {
		t.Fatalf("page-edge banner missing (want 'iteration 3'):\n%s", plain)
	}
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")
	first := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[0].key}]
	if first.start == 0 {
		t.Fatalf("page-edge banner is inside the first item's line range (start=0); should sit above the item")
	}
	if got := stripANSI(rows[0]); !strings.Contains(got, "iteration 3") {
		t.Fatalf("expected banner at row 0, got %q\nfull body:\n%s", got, plain)
	}
}

func TestBoundaryBannerAbsentWhenNoCoordChange(t *testing.T) {
	// Two items sharing the same coord must not trigger a banner —
	// the interior turn gap belongs to slice 09's user-bubble spacing
	// and slice 04's vertical rhythm.
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "same coord left"}}},
		{Seq: 2, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "same coord right"}}},
	}})
	body := stripANSI(m.itemTranscriptBody())
	for _, forbidden := range []string{"reset ", "iteration ", "retry "} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("interior-turn body contains banner label %q; no coord change should suppress the banner\n%s",
				forbidden, body)
		}
	}
}

func TestBoundaryBannerCoexistsWithMetadataRow(t *testing.T) {
	// Coexistence lock: on a coord change the metadata row (slice 11)
	// and the banner (slice 12) both sit inside the two-line gap in
	// order metadata → banner. The full 4-row composition is:
	//     <blank> <metadata row> <banner> <blank>
	// per plan §Layout matrix ("present metadata + present banner"
	// case). Uses twoTurnPage() so turnTimestamps produces a real
	// Δ row.
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(twoTurnPage())
	body := stripANSI(m.itemTranscriptBody())
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")

	// Locate the metadata row and the banner row by their unique
	// content ("Δ" for the metadata row, "iteration 2" for the
	// banner) and verify the order.
	deltaIdx := -1
	bannerIdx := -1
	for i, r := range rows {
		if deltaIdx == -1 && strings.Contains(r, "Δ") {
			deltaIdx = i
		}
		if bannerIdx == -1 && strings.Contains(r, "iteration 2") {
			bannerIdx = i
		}
	}
	if deltaIdx < 0 || bannerIdx < 0 {
		t.Fatalf("missing metadata (%d) or banner (%d):\n%s", deltaIdx, bannerIdx, body)
	}
	if !(deltaIdx < bannerIdx) {
		t.Fatalf("banner (%d) must sit *after* the metadata row (%d) in the gap\n%s",
			bannerIdx, deltaIdx, body)
	}
	// The two rows must sit directly adjacent (no blank between
	// them): the plan's row grammar places them back-to-back inside
	// the two-line spacing budget.
	if bannerIdx != deltaIdx+1 {
		t.Fatalf("banner (%d) is not directly below the metadata row (%d)\n%s",
			bannerIdx, deltaIdx, body)
	}
}
