package monitor

// Behavioral tests for the per-turn metadata row (omp-transcript-parity
// slice 11) as it flows through the itemTranscriptBody loop:
//   - the row appears in a coord-change gap when the closing turn has any
//     derivable field, and lives inside the previous item's line range so
//     n/N navigation still lands on the item;
//   - the step-end row appears below the last item once monitorStep.end
//     is set, and never before;
//   - when the row is empty (Ts absent, iter=attempt=0, no cost/tokens)
//     the loop's output matches slice 04 byte-for-byte.

import (
	"strings"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// twoTurnPage returns a two-turn synthetic page whose entries carry
// parseable RFC3339 timestamps so turnTimestamps produces a real
// interior metadata row at the coord boundary.
func twoTurnPage() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn 0 user prose"}}},
		{Seq: 2, Ts: "2026-09-14T10:00:04Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn 0 assistant prose"}}},
		{Seq: 3, Ts: "2026-09-14T10:00:07Z", Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn 1 user prose"}}},
	}}
}

func TestTurnMetadataRowAtCoordinateBoundary(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(twoTurnPage())
	body := m.itemTranscriptBody()
	plain := stripANSI(body)

	// The row for turn 0 must appear between the last turn-0 item and
	// the first turn-1 item. It has no cost/tokens (interior) and no
	// iter/attempt (turn 0 iter=0), so only the time slot and Δ remain.
	wantTime := time.Date(2026, 9, 14, 10, 0, 4, 0, time.UTC).Local().Format("15:04:05")
	wantRow := wantTime + "  Δ 4s"
	if !strings.Contains(plain, wantRow) {
		t.Fatalf("expected interior metadata row %q in body:\n%s", wantRow, plain)
	}

	// The row must sit between the last turn-0 item and the first
	// turn-1 item. Locate them by their characteristic content.
	rows := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	rowIdx := -1
	turn0LastIdx := -1
	turn1FirstIdx := -1
	for i, r := range rows {
		switch {
		case strings.Contains(r, "turn 0 assistant prose"):
			turn0LastIdx = i
		case strings.Contains(r, "turn 1 user prose"):
			turn1FirstIdx = i
		case strings.Contains(r, wantRow):
			rowIdx = i
		}
	}
	if turn0LastIdx < 0 || turn1FirstIdx < 0 || rowIdx < 0 {
		t.Fatalf("could not locate anchors: turn0Last=%d row=%d turn1First=%d in body:\n%s",
			turn0LastIdx, rowIdx, turn1FirstIdx, plain)
	}
	if !(turn0LastIdx < rowIdx && rowIdx < turn1FirstIdx) {
		t.Fatalf("row not between anchors: turn0Last=%d row=%d turn1First=%d", turn0LastIdx, rowIdx, turn1FirstIdx)
	}
}

func TestTurnMetadataRowFoldedIntoPreviousItemLineRange(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(twoTurnPage())
	body := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")

	// The last turn-0 item is chatItems[1] (assistant prose). Its
	// lineRange must now include both the slice-11 metadata row and
	// the slice-12 boundary banner so n/N navigation still lands on
	// items rather than on this trailing chrome.
	prev := m.chatItems[1]
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: prev.key}]
	if !ok {
		t.Fatalf("previous turn item has no line range")
	}
	if rng.end >= len(rows) {
		t.Fatalf("range end %d out of bounds for %d rows", rng.end, len(rows))
	}
	// The metadata row and the banner both live inside the folded
	// range; the banner sits after the metadata row so rng.end covers
	// the banner (slice 12), not the metadata row.
	wantTime := time.Date(2026, 9, 14, 10, 0, 4, 0, time.UTC).Local().Format("15:04:05")
	rangePlain := stripANSI(strings.Join(rows[rng.start:rng.end+1], "\n"))
	if !strings.Contains(rangePlain, wantTime) {
		t.Fatalf("folded range missing metadata row time %q; range:\n%s\nfull body:\n%s",
			wantTime, rangePlain, stripANSI(body))
	}
	if !strings.Contains(rangePlain, "iteration 2") {
		t.Fatalf("folded range missing boundary banner label; range:\n%s\nfull body:\n%s",
			rangePlain, stripANSI(body))
	}
	if got := stripANSI(rows[rng.end]); !strings.Contains(got, "iteration 2") {
		t.Fatalf("range end row = %q, want banner row (slice 12 sits after slice 11's row)\nfull body:\n%s",
			got, stripANSI(body))
	}
}

func TestTurnMetadataRowAtStepEnd(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(twoTurnPage())

	stepIdx := m.index["a"]
	cost := 0.1234
	m.steps[stepIdx].cost = &cost
	m.steps[stepIdx].tokens = 4200
	m.steps[stepIdx].status = step.StatusSucceeded
	m.steps[stepIdx].end = time.Date(2026, 9, 14, 10, 0, 12, 0, time.UTC)

	body := stripANSI(m.itemTranscriptBody())

	// The step-end row must appear below the last item (turn 1) and
	// carry both cost and tokens. Its time slot derives from
	// monitorStep.end, not the last transcript entry's Ts.
	wantEndTime := m.steps[stepIdx].end.Local().Format("15:04:05")
	wantStepEnd := wantEndTime + "  Δ" // Δ elapsed inside the row (turn 1 is one entry so elapsed is 0s → dropped, but time+cost still present)
	if !strings.Contains(body, wantEndTime) {
		t.Fatalf("expected step-end time %q in body:\n%s", wantEndTime, body)
	}
	if !strings.Contains(body, "$0.1234") {
		t.Fatalf("expected cost slot in step-end row:\n%s", body)
	}
	if !strings.Contains(body, "4.2k tok") {
		t.Fatalf("expected tokens slot in step-end row:\n%s", body)
	}

	// The step-end row must appear after the last visible item.
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	last1 := -1
	stepEndIdx := -1
	for i, r := range lines {
		if strings.Contains(r, "turn 1 user prose") {
			last1 = i
		}
		if strings.Contains(r, "$0.1234") {
			stepEndIdx = i
		}
	}
	if last1 < 0 || stepEndIdx < 0 || stepEndIdx <= last1 {
		t.Fatalf("step-end row not after last item: last1=%d stepEnd=%d\n%s", last1, stepEndIdx, body)
	}

	// The last item's line range must extend to the step-end row so
	// n/N block navigation still lands on the item.
	prev := m.chatItems[2]
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: prev.key}]
	if !ok {
		t.Fatalf("last item missing line range")
	}
	_ = wantStepEnd
	if !strings.Contains(stripANSI(lines[rng.end]), wantEndTime) {
		t.Fatalf("last item range end %d does not cover step-end row; row=%q\n%s",
			rng.end, lines[rng.end], body)
	}
}

func TestTurnMetadataRowSkippedForRunningStep(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(twoTurnPage())
	// Populate cost/tokens as though the harness reported them, but
	// keep monitorStep.end zero so the step is still Running.
	stepIdx := m.index["a"]
	cost := 0.5
	m.steps[stepIdx].cost = &cost
	m.steps[stepIdx].tokens = 9999
	m.steps[stepIdx].status = step.StatusRunning
	// end explicitly zero.

	body := stripANSI(m.itemTranscriptBody())
	if strings.Contains(body, "$0.5000") || strings.Contains(body, "10.0k tok") || strings.Contains(body, "9.9k tok") {
		t.Fatalf("step-end cost/tokens surfaced while step is still Running:\n%s", body)
	}
}

func TestTurnMetadataRowSkippedWhenAllFieldsAbsent(t *testing.T) {
	// Slice-11 invariant: when the row's fields are all absent, no
	// dim Δ row is emitted. Slice 12 adds a second occupant of the
	// coord gap — the boundary banner — which does render on every
	// coordinate transition regardless of timestamp availability, so
	// the gap is no longer byte-identical to slice 04. This test now
	// locks the composition: no metadata Δ row, banner present, and
	// the banner folded into the closing item's line range per plan
	// §Layout matrix.
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "no ts left"}}},
		{Seq: 2, Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "no ts right"}}},
	}})
	body := stripANSI(m.itemTranscriptBody())
	if strings.Contains(body, "Δ ") {
		t.Fatalf("metadata Δ row rendered under all-fields-absent input:\n%s", body)
	}
	if !strings.Contains(body, "iteration 2") {
		t.Fatalf("slice-12 boundary banner missing from body:\n%s", body)
	}
	// The banner folds into the first item's line range; the trailing
	// blank between the banner and the next item accounts for the "1"
	// here. See docs/plans/omp-slice-12-boundary-banners.md
	// §Layout matrix ("absent metadata + present banner" case).
	first := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[0].key}]
	second := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	if gap := second.start - first.end - 1; gap != 1 {
		t.Fatalf("post-fold gap = %d, want 1 (banner absorbed, one trailing blank)\nfirst=%+v second=%+v\n%s",
			gap, first, second, body)
	}
}

func TestTurnMetadataRowLineRangesMatchRenderedRows(t *testing.T) {
	// Extends slice-04's FR-04.9 invariant across a page whose coord
	// change AND step end both emit a metadata row: every item's
	// end-start+1 must equal the number of rendered rows in the range.
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(twoTurnPage())
	stepIdx := m.index["a"]
	cost := 0.02
	m.steps[stepIdx].cost = &cost
	m.steps[stepIdx].tokens = 1234
	m.steps[stepIdx].status = step.StatusSucceeded
	m.steps[stepIdx].end = time.Date(2026, 9, 14, 10, 0, 12, 0, time.UTC)

	body := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")

	characteristic := map[transcriptItemKey]string{
		m.chatItems[0].key: "turn 0 user prose",
		m.chatItems[1].key: "turn 0 assistant prose",
		m.chatItems[2].key: "turn 1 user prose",
	}
	for _, item := range m.chatItems {
		rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}]
		if !ok {
			t.Fatalf("item %v has no line range", item.key)
		}
		if rng.start < 0 || rng.end < rng.start || rng.end >= len(rows) {
			t.Fatalf("item %v range %+v out of bounds for %d rows", item.key, rng, len(rows))
		}
		// The item's characteristic content must appear somewhere in
		// the range (glamour renders a "User" header above prose, so
		// the item's actual text is usually on the second row).
		want := characteristic[item.key]
		found := false
		for i := rng.start; i <= rng.end; i++ {
			if strings.Contains(stripANSI(rows[i]), want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("item %v range %+v contains no row with %q; range rows:\n%s",
				item.key, rng, want, strings.Join(rows[rng.start:rng.end+1], "\n"))
		}
	}
}

func TestTurnMetadataRowDimStyledInBody(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 60
	m.setChatPage(twoTurnPage())
	body := m.itemTranscriptBody()

	// Locate the metadata row line, then verify it is wrapped in the
	// Chat.Hint SGR envelope. Grab the row containing "Δ 4s" (the
	// interior boundary row).
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var rowLine string
	for _, r := range rows {
		if strings.Contains(stripANSI(r), "Δ 4s") {
			// Rows are prefixed with structural whitespace by the
			// item loop; the metadata row is emitted without any
			// prefix, so it should start with the Chat.Hint SGR
			// escape or contain it as its wrapper.
			rowLine = r
			break
		}
	}
	if rowLine == "" {
		t.Fatalf("metadata row not present in body:\n%s", body)
	}
	// Rendering the same plain text through Chat.Hint must produce a
	// prefix that matches the row line's SGR wrapping.
	plain := stripANSI(rowLine)
	want := shared.Theme.Chat.Hint.Render(plain)
	if rowLine != want {
		t.Fatalf("metadata row not dim-styled through Chat.Hint\n got=%q\nwant=%q", rowLine, want)
	}
}
