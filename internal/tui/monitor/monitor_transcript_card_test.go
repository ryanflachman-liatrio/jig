package monitor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

func syntheticExchangePage(count int, detailRows int) transcript.Page {
	entries := make([]transcript.Entry, 0, count*2)
	seq := 1
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("tool-%03d", i)
		entries = append(entries, transcript.Entry{
			Seq: seq, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{
				ID: id, Kind: "read", Title: fmt.Sprintf("Read synthetic-%03d", i),
			}}},
		})
		seq++
		content := []toolcall.Content(nil)
		if detailRows > 0 && i == count/2 {
			content = []toolcall.Content{{Type: "text", Text: strings.Repeat("synthetic detail row\n", detailRows)}}
		}
		entries = append(entries, transcript.Entry{
			Seq: seq, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{
				ID: id, Kind: "read", Status: "completed", Content: content,
			}}},
		})
		seq++
	}
	return transcript.Page{Entries: entries}
}

func cloneLineRanges(in map[transcriptLineKey]lineRange) map[transcriptLineKey]lineRange {
	out := make(map[transcriptLineKey]lineRange, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func TestTranscriptCardLineRangesCachedAndFresh(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.chatVP.SetHeight(6)
	m.setChatPage(syntheticExchangePage(3, 30))
	m.chatItemExpand[m.chatItems[1].key] = true

	fresh := m.itemTranscriptBody()
	freshRanges := cloneLineRanges(m.chatItemLineRanges)
	if len(freshRanges) != 3 {
		t.Fatalf("fresh line ranges = %d, want 3", len(freshRanges))
	}
	if first := freshRanges[transcriptLineKey{itemKey: m.chatItems[0].key}]; first != (lineRange{start: 0, end: 1}) {
		t.Fatalf("first card range = %+v, want {0 1}", first)
	}
	tall := freshRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	if tall.end-tall.start+1 <= m.chatVP.Height() {
		t.Fatalf("expanded exchange range %+v is not taller than viewport", tall)
	}
	third := freshRanges[transcriptLineKey{itemKey: m.chatItems[2].key}]
	if third.start != tall.end+2 {
		t.Fatalf("inter-item spacing entered a line range: tall=%+v third=%+v", tall, third)
	}

	cached := m.itemTranscriptBody()
	if cached != fresh || !reflect.DeepEqual(m.chatItemLineRanges, freshRanges) {
		t.Fatalf("cached render changed output or ranges\nfresh=%+v\ncached=%+v", freshRanges, m.chatItemLineRanges)
	}
	for i := 0; i < 2; i++ {
		m, _ = m.updateTranscript(key("n"))
		assertChatCursorVisible(t, m)
	}
	if m.chatItemCursor != 2 {
		t.Fatalf("card navigation cursor = %d, want 2", m.chatItemCursor)
	}
}

func TestTranscriptCardCacheLifecycleBounded(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 64
	page := syntheticExchangePage(4, 4)
	m.setChatPage(page)
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) != 4 {
		t.Fatalf("initial cache = %d, want 4", len(m.chatItemRendered))
	}

	for i := 0; i < 20; i++ {
		m.chatItemCursor = i % len(m.chatItems)
		m.chatItemExpand[m.chatItems[i%len(m.chatItems)].key] = i%2 == 0
		_ = m.itemTranscriptBody()
		if len(m.chatItemRendered) > len(m.chatItems) {
			t.Fatalf("selection/expansion iteration %d grew cache to %d for %d items", i, len(m.chatItemRendered), len(m.chatItems))
		}
	}

	replacement := syntheticExchangePage(4, 0)
	replacement.Entries[0].Blocks[0].Tool.Title = "Changed synthetic header"
	replacement.Entries[0].Blocks[0].Tool.Kind = "custom"
	replacement.Entries[1].Blocks[0].Tool.Kind = "custom"
	replacement.Entries[1].Blocks[0].Tool.Status = "failed"
	m.setChatPage(replacement)
	failed := m.itemTranscriptBody()
	if !strings.Contains(stripANSI(failed), "failed") || !strings.Contains(stripANSI(failed), "Changed synthetic header") {
		t.Fatalf("same-key failed/header replacement stayed stale:\n%s", stripANSI(failed))
	}
	if len(m.chatItemRendered) != 4 {
		t.Fatalf("replacement cache = %d, want 4", len(m.chatItemRendered))
	}

	replacement.Entries[1].Blocks[0].Tool.Status = "completed"
	m.setChatPage(replacement)
	completed := m.itemTranscriptBody()
	if strings.Contains(stripANSI(completed), "failed") || completed == failed {
		t.Fatalf("same-key success replacement stayed stale:\n%s", stripANSI(completed))
	}

	for _, width := range []int{48, 80, 52, 64} {
		m.transcriptInnerW = width
		m.rebuildRenderer()
		if len(m.chatItemRendered) != 0 {
			t.Fatalf("resize to %d retained %d cache entries", width, len(m.chatItemRendered))
		}
		_ = m.itemTranscriptBody()
		if len(m.chatItemRendered) != len(m.chatItems) {
			t.Fatalf("resize to %d cached %d entries, want %d", width, len(m.chatItemRendered), len(m.chatItems))
		}
	}

	selected := m.chatItems[2].key
	m.chatItemCursor = 2
	m.setChatPage(replacement)
	if got := m.chatVisibleItems[m.chatItemCursor].key; got != selected {
		t.Fatalf("same-step refresh selected %+v, want %+v", got, selected)
	}

	m.chatStep = "b"
	m.reloadTranscript()
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("step change retained %d card entries", len(m.chatItemRendered))
	}
}

func TestTranscriptCardPageBoundsSearchExpansionAndClipboard(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunDir = "/synthetic/nonexistent/run"
	m.transcriptInnerW = 72
	m.setChatPage(syntheticExchangePage(150, 40)) // 300 transcript entries.
	if len(m.chatEntries) != chatWindowMax || len(m.chatItems) != 150 {
		t.Fatalf("bounded page = entries:%d items:%d, want %d/150", len(m.chatEntries), len(m.chatItems), chatWindowMax)
	}
	first := m.itemTranscriptBody()
	second := m.itemTranscriptBody()
	if first != second || len(m.chatItemRendered) != 150 {
		t.Fatalf("repeated render changed output or cache bound: cache=%d", len(m.chatItemRendered))
	}

	m.searchQuery = "synthetic-042"
	m.rebuildTranscriptItemState(transcriptItemKey{})
	if len(m.chatVisibleItems) != 1 {
		t.Fatalf("search membership = %d, want 1", len(m.chatVisibleItems))
	}
	payload := runItemLoader(t, m.chatVisibleItems[0], m.chatEntries)
	if payload.Err != nil || !strings.Contains(payload.Payload, "tool-042") || strings.ContainsAny(payload.Payload, "╭╮╰╯") {
		t.Fatalf("searched-item clipboard payload invalid: err=%v payload=%q", payload.Err, payload.Payload)
	}

	m.searchQuery = ""
	m.rebuildTranscriptItemState(transcriptItemKey{})
	m.chatItemExpandAll = true
	expanded := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(expanded, "lines hidden") {
		t.Fatalf("expanded 300-entry page did not retain detail bound notice")
	}
	if len(m.chatItemRendered) > len(m.chatItems) {
		t.Fatalf("expanded page cache = %d, want <= %d", len(m.chatItemRendered), len(m.chatItems))
	}
}

func BenchmarkTranscriptCardPage(b *testing.B) {
	m := New("benchmark")
	m.transcriptInnerW = 100
	m.setChatPage(syntheticExchangePage(150, 0)) // 300 transcript entries.
	_ = m.itemTranscriptBody()                   // Populate the card cache.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.itemTranscriptBody()
	}
}
