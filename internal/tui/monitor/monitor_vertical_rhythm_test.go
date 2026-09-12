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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
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
	empty := m.chatItems[1]
	if rendered := m.renderMarkdown(empty.primary.key, ""); trimStructuralBlankEdges(rendered) != "" {
		t.Fatalf("empty markdown rendered %q, want structurally empty output after edge trim", rendered)
	}
	body := m.itemTranscriptBody()

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
			// Leading and trailing newlines here become edge whitespace in
			// the verbatim rendering path. The loop must absorb both so only
			// the structural separator lands between this item and the next.
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "\nsynthetic system prose\n\n\n"}}},
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

// structuralEdgesPage combines the zero-height, plain-edge, and tinted-edge
// cases in one fabricated page. It is shared by the narrow-layout and
// line-range tests so both verify the same bytes through the production item
// loop without adding a test-only rendering branch.
func structuralEdgesPage() transcript.Page {
	tintedPadding := "\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) + "\x1b[49m"
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "narrow first visible"}}},
		{Seq: 2, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: ""}}},
		{Seq: 3, Role: transcript.RoleSystem,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "\nedge content\n\n"}}},
		{Seq: 4, Role: transcript.RoleSystem,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: tintedPadding + "\nnarrow tinted content\n" + tintedPadding}}},
		{Seq: 5, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "narrow final visible"}}},
	}}
}

// TestTranscriptStructuralEdgesAtNarrowWidth exercises the combined edge
// discipline at transcriptInnerW=24, where wrapping and short card rows make
// line-accounting mistakes easiest to expose.
func TestTranscriptStructuralEdgesAtNarrowWidth(t *testing.T) {
	tintedPadding := "\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) + "\x1b[49m"
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 24
	m.setChatPage(structuralEdgesPage())
	body := m.itemTranscriptBody()

	if _, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]; ok {
		t.Fatalf("narrow zero-height item acquired a line range")
	}
	if got := strings.Count(body, tintedPadding); got != 2 {
		t.Fatalf("narrow tinted padding occurrences = %d, want 2", got)
	}

	rendered := []int{0, 2, 3, 4}
	for i := 1; i < len(rendered); i++ {
		previous := m.chatItems[rendered[i-1]]
		current := m.chatItems[rendered[i]]
		previousRange := m.chatItemLineRanges[transcriptLineKey{itemKey: previous.key}]
		currentRange := m.chatItemLineRanges[transcriptLineKey{itemKey: current.key}]
		if got, want := currentRange.start-previousRange.end-1, itemSpacingBefore(previous, current); got != want {
			t.Fatalf("narrow gap %d->%d = %d, want %d\nbody:\n%s", rendered[i-1], rendered[i], got, want, stripANSI(body))
		}
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
	m.setChatPage(structuralEdgesPage())
	body := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")

	characteristic := map[transcriptItemKey]string{
		m.chatItems[0].key: "narrow first visible",
		m.chatItems[2].key: "edge content",
		m.chatItems[3].key: "narrow tinted content",
		m.chatItems[4].key: "narrow final visible",
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

// verticalRhythmVisualPage is the proof-only fixture. It contains one
// user text item, a running tool-exchange card, a zero-height empty
// assistant text (the skipped item), a settled-success tool card, an
// item across an execution-coordinate boundary (Iteration = 1), and a
// system verbatim item whose block text intentionally carries trailing
// blank lines the trimmer must absorb. Fabricated content only; no
// real .jig/ data.
func verticalRhythmVisualPage() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Let me check the synthetic entrypoint."}}},
		{Seq: 2, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "read-live", Kind: "read", Title: "Read synthetic/config.toml"}}}},
		// Zero-height guard fixture: an empty-text assistant block. Its
		// rendered bytes are structurally blank; the loop must skip it
		// entirely rather than leave a ghost gap between the running
		// card above and the completed card below.
		{Seq: 3, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: ""}}},
		{Seq: 4, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "read-ok", Kind: "read", Title: "Read synthetic/entrypoint.go"}}}},
		{Seq: 5, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "read-ok", Kind: "read", Status: "completed"}}}},
		// Coordinate change: Iteration bumps to 1. itemSpacingBefore
		// returns 2 for this transition; the resulting two-line gap is
		// where slice 12's boundary banner will render.
		{Seq: 6, Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleSystem,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "iteration 1 begin\n\n\n"}}},
		{Seq: 7, Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "grep-live", Kind: "grep", Title: "Search fabricated sources after coord change"}}}},
	}}
}

// TestVerticalRhythmVisualProof captures a deterministic 80×30 Monitor
// frame for the slice-04 proof directory. Opt-in via JIG_UI_SNAPSHOT_DIR
// (headless-Chrome PNG conversion runs outside the test process; the
// .ansi/.html captures are the deterministic ground truth).
func TestVerticalRhythmVisualProof(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture slice-04 Monitor frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{RunID: "run-1", StepID: "a", To: step.StatusRunning}})
	m.chatStep = "a"
	m.focus = focusTranscript
	m.setChatPage(verticalRhythmVisualPage())
	m.chatItemCursor = 0
	m.refreshPanels()

	view := m.View()

	// Independently count the blank-line gaps between each pair of visible
	// items and record them in the notes so a reviewer can diff the
	// baseline capture without re-deriving item boundaries.
	body := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var notes strings.Builder
	notes.WriteString("Tasks 03-04 (slice 04) vertical-rhythm visual fixture\n")
	notes.WriteString("Fixture source: verticalRhythmVisualPage (slice-04 proof-only)\n")
	notes.WriteString("All paths, commands, status text, and code are fabricated.\n\n")
	notes.WriteString("baseline_commit=6b583fa (pre-implementation slice-03 merge)\n")
	fmt.Fprintf(&notes, "terminal=80x30 transcriptInnerW=%d chatItems=%d visibleItems=%d cursor=%d\n",
		m.transcriptInnerW, len(m.chatItems), len(m.chatVisibleItems), m.chatItemCursor)
	notes.WriteString("Rendering: production Model.View ANSI -> deterministic test HTML.\n")
	notes.WriteString("PNG conversion (if performed) uses local headless Chrome — see the task-4 limitations note.\n\n")
	notes.WriteString("Item ranges and inter-item gaps in the transcript body:\n")
	visibleKeys := make([]transcriptItemKey, 0, len(m.chatVisibleItems))
	for _, item := range m.chatVisibleItems {
		if _, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}]; ok {
			visibleKeys = append(visibleKeys, item.key)
		}
	}
	for i, key := range visibleKeys {
		rng := m.chatItemLineRanges[transcriptLineKey{itemKey: key}]
		summary := ""
		if rng.start < len(rows) {
			summary = stripANSI(rows[rng.start])
			if len(summary) > 60 {
				summary = summary[:60] + "…"
			}
		}
		fmt.Fprintf(&notes, "  item[%d] key=%+v range=%+v first=%q\n", i, key, rng, summary)
		if i > 0 {
			prevKey := visibleKeys[i-1]
			prev := m.chatItemLineRanges[transcriptLineKey{itemKey: prevKey}]
			gap := rng.start - prev.end - 1
			fmt.Fprintf(&notes, "    ^ %d blank line(s) since previous visible item\n", gap)
		}
	}
	notes.WriteString("\nZero-height item: the empty assistant block at Seq=3 does not appear in the range list above; the guard skipped it and no ghost gap remains between the surrounding cards.\n")
	notes.WriteString("Coord change: Seq=6 bumps Iteration to 1; itemSpacingBefore returns 2, so the gap between the second card (Seq=4-5) and the coord-change system item is 2 blank lines (slice 12's boundary banner will render inside that gap).\n")

	for _, name := range []string{"25-task-3-rhythm", "25-task-4-monitor-rhythm"} {
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(view), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(terminalHTML(view)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+"-notes.txt"), []byte(notes.String()), 0o644); err != nil {
			t.Fatal(err)
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
