package monitor

// Selection-affordance regressions (slice 03). The transcript's block cursor
// must change only color and glyph — never the horizontal position of any
// content. Tests here lock the six FRs from
// docs/specs/25-spec-selection-affordance/25-spec-selection-affordance.md.

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

// selectionSyntheticPage returns a fabricated multi-kind page: text, system,
// thinking, unsupported, a paired tool exchange, and an orphan tool result.
// It exercises every renderer path in itemTranscriptBody that emits a leading
// two-cell prefix, so a single page suffices for the prefix-width and
// column-parity regressions.
func selectionSyntheticPage() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "synthetic assistant prose"}}},
		{Seq: 2, Role: transcript.RoleSystem, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "synthetic system prose"}}},
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "synthetic reasoning"}}},
		{Seq: 4, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockType("future"), Text: "synthetic future content"}}},
		{Seq: 5, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "ok", Kind: "read", Title: "Read synthetic config"}}}},
		{Seq: 6, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "ok", Kind: "read", Status: "completed"}}}},
		{Seq: 7, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "orphan", Kind: "read", Status: "failed"}}}},
	}}
}

// selectedPrefix is the exact byte sequence itemTranscriptBody emits for the
// selected leading gutter after stripping ANSI. Two visible cells: bar +
// space. Kept as a constant so the tests read naturally.
const (
	selectedPrefix   = "▌ "
	unselectedPrefix = "  "
)

// stripCursorPrefix removes exactly one two-cell leading gutter (selected or
// unselected) from a stripped-ANSI line, so the remainder can be compared
// column-for-column across cursor positions. Unknown prefixes are returned
// unchanged so blank lines and unusual writers surface as diffs.
func stripCursorPrefix(line string) string {
	switch {
	case strings.HasPrefix(line, selectedPrefix):
		return line[len(selectedPrefix):]
	case strings.HasPrefix(line, unselectedPrefix):
		return line[len(unselectedPrefix):]
	default:
		return line
	}
}

// TestSelectionAffordancePrefixesEqualTwoCells locks FR-03.1. The
// selected and unselected leading prefixes each measure exactly two
// visible cells, so cursor movement swaps a bar for two spaces without
// shifting content. Only the first line of any item carries an explicit
// item prefix (multi-line text items delegate to Glamour for internal
// wrapping and inherit no per-line bar); the tool-exchange card is the
// one item whose every rendered row is prefixed, and that
// card-specific sub-assertion runs below.
//
// The check runs at 40 and 80 columns to cover the epic's
// narrow-terminal target and a comfortable default; the prefix must not
// depend on width.
func TestSelectionAffordancePrefixesEqualTwoCells(t *testing.T) {
	if got := lipgloss.Width(selectedPrefix); got != 2 {
		t.Fatalf("selectedPrefix width = %d, want 2 (glyph vocabulary regression)", got)
	}
	if got := lipgloss.Width(unselectedPrefix); got != 2 {
		t.Fatalf("unselectedPrefix width = %d, want 2 (glyph vocabulary regression)", got)
	}

	for _, width := range []int{40, 80} {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = width
		m.setChatPage(selectionSyntheticPage())

		for cursor := 0; cursor < len(m.chatItems); cursor++ {
			m.chatItemCursor = cursor
			body := m.itemTranscriptBody()
			lines := strings.Split(strings.TrimRight(body, "\n"), "\n")

			for idx, itm := range m.chatItems {
				rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: itm.key}]
				if !ok || rng.start >= len(lines) {
					t.Fatalf("width=%d cursor=%d item %d missing line range: %+v", width, cursor, idx, rng)
				}
				first := stripANSI(lines[rng.start])
				want := unselectedPrefix
				if idx == cursor {
					want = selectedPrefix
				}
				if !strings.HasPrefix(first, want) {
					t.Fatalf("width=%d cursor=%d item %d first line = %q, want prefix %q",
						width, cursor, idx, first, want)
				}
				if got := lipgloss.Width(first[:len(want)]); got != 2 {
					t.Fatalf("width=%d cursor=%d item %d prefix width = %d, want 2: %q",
						width, cursor, idx, got, first)
				}
			}
		}
	}
}

// TestSelectionAffordanceCardRowsSharePrefix locks the card-specific
// half of FR-03.1 and FR-03.3: every row of a tool-exchange header card
// carries the same two-cell prefix (bar or spaces), so the card frame
// stays column-aligned when the cursor lands on it. This is the case
// the pre-fix "+=" defect broke most visibly (the frame slid two cells
// on selection).
func TestSelectionAffordanceCardRowsSharePrefix(t *testing.T) {
	entries := append(syntheticExchange("one", "completed"), []transcript.Entry{
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "two", Kind: "read", Title: "Reading second"}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "two", Kind: "read", Status: "completed"}}}},
	}...)

	for _, cursor := range []int{0, 1} {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = 60
		m.setChatPage(transcript.Page{Entries: entries})
		m.chatItemCursor = cursor
		body := m.itemTranscriptBody()
		lines := strings.Split(strings.TrimRight(body, "\n"), "\n")

		for idx, itm := range m.chatItems {
			rng := m.chatItemLineRanges[transcriptLineKey{itemKey: itm.key}]
			want := unselectedPrefix
			if idx == cursor {
				want = selectedPrefix
			}
			for i := rng.start; i <= rng.end; i++ {
				plain := stripANSI(lines[i])
				if plain == "" {
					continue
				}
				if !strings.HasPrefix(plain, want) {
					t.Fatalf("cursor=%d item %d card row %d = %q, want prefix %q",
						cursor, idx, i, plain, want)
				}
				if got := lipgloss.Width(plain[:len(want)]); got != 2 {
					t.Fatalf("cursor=%d item %d card row %d prefix width = %d, want 2: %q",
						cursor, idx, i, got, plain)
				}
			}
		}
	}
}

// TestSelectionAffordanceColumnParity locks FR-03.3. Rendering the same
// synthetic page twice with different cursor positions produces identical
// content past the two-cell prefix on every line, so cursor movement never
// slides text sideways. Text, system, thinking, unsupported, tool-exchange
// header card, and orphan tool-result rows are all in the fixture.
func TestSelectionAffordanceColumnParity(t *testing.T) {
	first := renderAt(t, selectionSyntheticPage(), 0)
	second := renderAt(t, selectionSyntheticPage(), 2)

	if len(first) != len(second) {
		t.Fatalf("line count differs across selection: %d vs %d\nfirst:\n%s\nsecond:\n%s",
			len(first), len(second), strings.Join(first, "\n"), strings.Join(second, "\n"))
	}
	for i := range first {
		fw, sw := lipgloss.Width(first[i]), lipgloss.Width(second[i])
		if fw != sw {
			t.Fatalf("line %d width differs across selection: cursor=0 -> %d, cursor=2 -> %d\nfirst=%q\nsecond=%q",
				i, fw, sw, first[i], second[i])
		}
		fr := stripCursorPrefix(first[i])
		sr := stripCursorPrefix(second[i])
		if fr != sr {
			t.Fatalf("line %d content past the 2-cell prefix differs across selection\nfirst= %q\nsecond=%q", i, fr, sr)
		}
	}
}

// renderAt renders the page with the given cursor position and returns the
// stripped-ANSI lines. It also resets the render cache before returning so
// callers can chain calls without leaking state between renders — the two
// captures in TestSelectionAffordanceColumnParity must be independent.
func renderAt(t *testing.T, page transcript.Page, cursor int) []string {
	t.Helper()
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.setChatPage(page)
	m.chatItemCursor = cursor
	body := m.itemTranscriptBody()
	raw := strings.Split(strings.TrimRight(body, "\n"), "\n")
	out := make([]string, len(raw))
	for i, line := range raw {
		out[i] = stripANSI(line)
	}
	return out
}

// TestSelectionAffordanceGutterGlyphSource locks FR-03.2 / FR-03.6 by
// reading monitor_transcript_items_view.go and asserting the transcript
// items view sources its selection glyph from shared.CursorBar rather than
// a bare "▌" literal. This is the CC-7 discipline for exactly this call
// site; policing other files is out of scope for slice 03.
func TestSelectionAffordanceGutterGlyphSource(t *testing.T) {
	src, err := os.ReadFile("monitor_transcript_items_view.go")
	if err != nil {
		t.Fatalf("read monitor_transcript_items_view.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "shared.Theme.SelectedBar.Render(shared.CursorBar)") {
		t.Fatalf("expected the transcript items view to render the cursor glyph via shared.CursorBar; not found in:\n%s", body)
	}
	if strings.Contains(body, "SelectedBar.Render(\"▌\")") {
		t.Fatalf("transcript items view reintroduced a bare \"▌\" literal alongside the shared vocabulary")
	}
}

// TestSelectionAffordanceLineRangesStable locks FR-03.4. Moving the cursor
// does not change how many lines an item occupies, so chatItemLineRanges is
// byte-identical across cursor positions. Covers a collapsed exchange and an
// expanded exchange so the invariant protects the expand path too.
func TestSelectionAffordanceLineRangesStable(t *testing.T) {
	page := selectionSyntheticPage()
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(page)
	m.chatItemExpand[m.chatItems[2].key] = true

	m.chatItemCursor = 0
	_ = m.itemTranscriptBody()
	first := cloneLineRanges(m.chatItemLineRanges)

	m.chatItemCursor = 4
	_ = m.itemTranscriptBody()
	second := cloneLineRanges(m.chatItemLineRanges)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("chatItemLineRanges drifted across cursor movement\ncursor=0: %+v\ncursor=4: %+v", first, second)
	}
}

// TestSelectionAffordanceCardWidthStableAcrossSelection locks FR-03.5. The
// header-card cache key's `width` component is `transcriptInnerW - 2` in
// both selection branches, so the cache holds at most one width variant per
// item and the frame's visible cells do not change when the cursor lands on
// it.
func TestSelectionAffordanceCardWidthStableAcrossSelection(t *testing.T) {
	entries := append(syntheticExchange("one", "completed"), []transcript.Entry{
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "two", Kind: "read", Title: "Reading second"}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "two", Kind: "read", Status: "completed"}}}},
	}...)

	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})

	widths := map[int][]int{}
	for _, cursor := range []int{0, 1} {
		m.chatItemCursor = cursor
		body := m.itemTranscriptBody()
		for _, row := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			if row == "" {
				continue
			}
			widths[cursor] = append(widths[cursor], lipgloss.Width(row))
		}
	}
	if !reflect.DeepEqual(widths[0], widths[1]) {
		t.Fatalf("card row widths differ across selection\ncursor=0: %v\ncursor=1: %v", widths[0], widths[1])
	}
	for _, w := range widths[0] {
		if w != m.transcriptInnerW {
			t.Fatalf("card row width = %d, want transcriptInnerW=%d", w, m.transcriptInnerW)
		}
	}

	wantAvail := m.transcriptInnerW - 2
	for key := range m.chatItemRendered {
		if key.surface != transcriptRenderCard {
			continue
		}
		if key.width != wantAvail {
			t.Fatalf("cache key width = %d, want %d (both selection branches must yield transcriptInnerW-2)", key.width, wantAvail)
		}
	}
}

// TestSelectionAffordanceCardCacheDoesNotGrowOnSelection locks FR-03.5's
// bounded-cache half. Sweeping the cursor 0 → 1 → 0 → 1 across two exchange
// items must keep the cache at exactly two entries — one card per item — and
// each entry's `selected` flag must match the current selection so slice
// 02's TranscriptSelected title styling remains cache-consistent.
func TestSelectionAffordanceCardCacheDoesNotGrowOnSelection(t *testing.T) {
	entries := append(syntheticExchange("one", "completed"), []transcript.Entry{
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "two", Kind: "read", Title: "Reading second"}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "two", Kind: "read", Status: "completed"}}}},
	}...)

	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})

	for _, cursor := range []int{0, 1, 0, 1} {
		m.chatItemCursor = cursor
		_ = m.itemTranscriptBody()
		if got, want := len(m.chatItemRendered), len(m.chatItems); got > want {
			t.Fatalf("cursor=%d grew card cache to %d for %d items", cursor, got, want)
		}
		found := map[transcriptItemKey]bool{}
		for key := range m.chatItemRendered {
			if key.surface != transcriptRenderCard {
				continue
			}
			found[key.itemKey] = true
			wantSelected := key.itemKey == m.chatItems[cursor].key
			if key.selected != wantSelected {
				t.Fatalf("cursor=%d cache entry for %+v has selected=%v, want %v (composed header must match current selection)",
					cursor, key.itemKey, key.selected, wantSelected)
			}
		}
		if len(found) != len(m.chatItems) {
			t.Fatalf("cursor=%d cached %d distinct items, want %d", cursor, len(found), len(m.chatItems))
		}
	}
}
