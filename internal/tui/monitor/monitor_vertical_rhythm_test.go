package monitor

// Vertical-rhythm regressions (slice 04). itemTranscriptBody must skip
// zero-height items entirely (no separator, no chatItemLineRanges entry),
// trim structural edge blanks so an item's own leading/trailing blanks do
// not stack on the inter-item separator, preserve a background-tinted
// padding row byte-for-byte (so slice-09's user-message bubble can rely on
// the raw-bytes trim), and derive chatItemLineRanges accurately from the
// bytes actually written to the transcript body. Tests here lock the FRs
// from docs/specs/25-spec-vertical-rhythm-and-block-edges/.

import (
	"reflect"
	"strings"
	"testing"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

// verticalRhythmPage returns a fabricated multi-kind page whose items
// exercise the four rendering paths the vertical-rhythm rule cares about:
// user text with content, empty assistant text (a zero-height item), a
// second user text at the same coordinate, and a tool exchange whose coord
// differs by iteration. The order (visible, empty, visible, coord-change)
// is deliberate so a single body renders every discipline the loop owes.
func verticalRhythmPage() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "first synthetic user prose"}}},
		{Seq: 2, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: ""}}},
		{Seq: 3, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "second synthetic user prose"}}},
		{Seq: 4, Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "rhythm-read", Kind: "read", Title: "Read after coord change"}}}},
	}}
}

// TestTranscriptZeroHeightItemContributesNothing locks slice-04 FR-04.5 /
// FR-04.6: an assistant text item whose renderMarkdown returns an empty
// string produces no visible bytes; the loop must skip it entirely so the
// separator between its neighbors is computed against those neighbors, and
// chatItemLineRanges must contain no entry for the skipped item's key.
func TestTranscriptZeroHeightItemContributesNothing(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(verticalRhythmPage())
	if len(m.chatItems) < 3 {
		t.Fatalf("verticalRhythmPage produced %d items, want at least 3", len(m.chatItems))
	}
	body := m.itemTranscriptBody()

	empty := m.chatItems[1]
	if _, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: empty.key}]; ok {
		t.Fatalf("zero-height item leaked a chatItemLineRanges entry: %+v", m.chatItemLineRanges[transcriptLineKey{itemKey: empty.key}])
	}

	first := m.chatItems[0]
	second := m.chatItems[2]
	firstRng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: first.key}]
	if !ok {
		t.Fatalf("first visible item missing from chatItemLineRanges")
	}
	secondRng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: second.key}]
	if !ok {
		t.Fatalf("second visible item missing from chatItemLineRanges")
	}

	// Separator between the two visible items must equal
	// itemSpacingBefore(first, second): the skipped item must not leave
	// any trace, so the gap is computed as if it were absent from the page.
	want := itemSpacingBefore(first, second)
	got := secondRng.start - firstRng.end - 1
	if got != want {
		t.Fatalf("separator between visible neighbors = %d blank lines, want %d (zero-height item leaked a gap)\nbody:\n%s",
			got, want, stripANSI(body))
	}
}

// TestTranscriptTrimsStructuralEdgeBlanksBetweenItems locks FR-04.7: the
// inter-item separator is exactly itemSpacingBefore(previous, current)
// blank lines regardless of what edge whitespace the item's renderer
// happened to produce. writeVerbatim on system text with a trailing blank
// line adds one, and the trim must eat it before the separator is applied.
func TestTranscriptTrimsStructuralEdgeBlanksBetweenItems(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "leading synthetic prose"}}},
		{Seq: 2, Role: transcript.RoleSystem,
			// Trailing newlines here would be edge whitespace the item's
			// own writer emits; the loop must absorb them so only one
			// structural separator lands between this item and the next.
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "synthetic system prose\n\n\n"}}},
		{Seq: 3, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "trailing synthetic prose"}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	body := m.itemTranscriptBody()

	systemRng := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	trailingRng := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[2].key}]

	gap := trailingRng.start - systemRng.end - 1
	want := itemSpacingBefore(m.chatItems[1], m.chatItems[2])
	if gap != want {
		t.Fatalf("gap between edge-blank-emitting item and next = %d blank lines, want %d\nbody:\n%s",
			gap, want, stripANSI(body))
	}
}

// TestTranscriptPreservesTintedPaddingRow locks FR-04.3 at the loop level:
// a synthetic verbatim item whose content contains bytes shaped like the
// shared card's tinted padding row must survive the per-item edge trim
// byte-for-byte, because slice-09's user-message bubble will emit tinted
// padding through the same code path. The exact SGR sequences are the
// ones hard-coded in shared/card.go finishRow.
func TestTranscriptPreservesTintedPaddingRow(t *testing.T) {
	tintedPadding := "\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) + "\x1b[49m"
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "before synthetic bubble"}}},
		{Seq: 2, Role: transcript.RoleSystem,
			Blocks: []transcript.Block{{Type: transcript.BlockText,
				Text: tintedPadding + "\nvisible content\n" + tintedPadding}}},
		{Seq: 3, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "after synthetic bubble"}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	body := m.itemTranscriptBody()

	if !strings.Contains(body, tintedPadding) {
		t.Fatalf("tinted padding sequence stripped by per-item trim; body:\n%q", body)
	}
	// Both padding rows must survive, not just one.
	if got := strings.Count(body, tintedPadding); got != 2 {
		t.Fatalf("tinted padding occurrences = %d, want 2 (both rows must survive)", got)
	}

	systemRng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	if !ok {
		t.Fatalf("tinted padding item missing from chatItemLineRanges")
	}
	// Three visible content lines plus writeVerbatim's per-line indent
	// mean the item occupies 3 rows: top padding, "visible content",
	// bottom padding. If either padding row is trimmed, this drops to 2.
	if want := 3; systemRng.end-systemRng.start+1 != want {
		t.Fatalf("tinted padding item spans %d rows, want %d", systemRng.end-systemRng.start+1, want)
	}
}

// TestTranscriptConsecutiveTextItemsSameRoleNoGap locks FR-04.8: two
// adjacent user text items at the same execution coordinate render with
// zero blank lines between them. itemSpacingBefore already returns 0 for
// this transition; the slice-04 loop must preserve that.
func TestTranscriptConsecutiveTextItemsSameRoleNoGap(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "first synthetic user turn"}}},
		{Seq: 2, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "second synthetic user turn"}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	_ = m.itemTranscriptBody()

	if got, want := itemSpacingBefore(m.chatItems[0], m.chatItems[1]), 0; got != want {
		t.Fatalf("itemSpacingBefore for consecutive same-role text = %d, want %d (kind table regression)", got, want)
	}

	first := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[0].key}]
	second := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	gap := second.start - first.end - 1
	if gap != 0 {
		t.Fatalf("gap between consecutive same-role user text = %d blank lines, want 0", gap)
	}
}

// TestTranscriptExecutionCoordinateGapPreserved locks FR-04.12: two items
// whose generation/iteration/attempt differs render with exactly two blank
// lines between them, because slice 12's boundary banner will render
// inside that gap and depends on its width remaining stable.
func TestTranscriptExecutionCoordinateGapPreserved(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "before coord change"}}},
		{Seq: 2, Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "after coord change"}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	_ = m.itemTranscriptBody()

	if got, want := itemSpacingBefore(m.chatItems[0], m.chatItems[1]), 2; got != want {
		t.Fatalf("itemSpacingBefore across coord change = %d, want %d", got, want)
	}

	first := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[0].key}]
	second := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	gap := second.start - first.end - 1
	if gap != 2 {
		t.Fatalf("gap across execution-coordinate change = %d blank lines, want 2\nfirst=%+v second=%+v",
			gap, first, second)
	}
}

// TestTranscriptLineRangesMatchRenderedRows locks FR-04.9: every entry in
// chatItemLineRanges must map to a contiguous slice of the transcript body
// whose row count equals end-start+1 and whose rows contain the item's
// characteristic content. Independent of the loop's internal accounting.
func TestTranscriptLineRangesMatchRenderedRows(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(verticalRhythmPage())
	body := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")

	characteristic := map[transcriptItemKey]string{
		m.chatItems[0].key: "first synthetic user prose",
		m.chatItems[2].key: "second synthetic user prose",
		// The tool-card header renders the summarized kind ("Read"), not
		// the block's Title field, so the coord-change item's
		// characteristic is the header token, not the title text.
		m.chatItems[3].key: "Read",
	}

	for _, item := range m.chatItems {
		want, keyed := characteristic[item.key]
		rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}]
		if !keyed {
			// The empty assistant text (index 1) is the zero-height item.
			if ok {
				t.Fatalf("zero-height item %v acquired a range %+v", item.key, rng)
			}
			continue
		}
		if !ok {
			t.Fatalf("visible item %v missing from chatItemLineRanges", item.key)
		}
		if rng.start < 0 || rng.end < rng.start || rng.end >= len(rows) {
			t.Fatalf("item %v range %+v is out of bounds for %d rows", item.key, rng, len(rows))
		}
		found := false
		for i := rng.start; i <= rng.end; i++ {
			if strings.Contains(stripANSI(rows[i]), want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("item %v range %+v contains no row with characteristic %q; rows:\n%s",
				item.key, rng, want, strings.Join(rows[rng.start:rng.end+1], "\n"))
		}
	}
}

// TestTranscriptZeroHeightSkipStableAcrossRenders confirms the zero-height
// guard is deterministic: rendering the same page twice must produce
// identical bytes and identical chatItemLineRanges even though the skipped
// item participates in both iterations.
func TestTranscriptZeroHeightSkipStableAcrossRenders(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(verticalRhythmPage())

	first := m.itemTranscriptBody()
	firstRanges := make(map[transcriptLineKey]lineRange, len(m.chatItemLineRanges))
	for k, v := range m.chatItemLineRanges {
		firstRanges[k] = v
	}
	second := m.itemTranscriptBody()
	if first != second {
		t.Fatalf("repeated render diverged")
	}
	if !reflect.DeepEqual(firstRanges, m.chatItemLineRanges) {
		t.Fatalf("chatItemLineRanges diverged across repeated render\nfirst=%+v\nsecond=%+v",
			firstRanges, m.chatItemLineRanges)
	}
}
