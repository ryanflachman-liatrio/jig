package monitor

import (
	"strings"
	"testing"

	"jig/internal/transcript"
)

// FR-09.6: toggling the expand control on a collapsed item renders the full
// markdown body inside the bubble, and toggling again restores the single
// summary row.
func TestExpandTogglesCollapsedUserTextRoundTrip(t *testing.T) {
	body := strings.Repeat("word ", 2000) // > chatTextCollapseBytes
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: body}}},
	}})
	key := m.chatItems[0].key

	collapsed := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(collapsed, "User input") {
		t.Fatalf("expected collapsed summary before expansion:\n%.200s", collapsed)
	}
	if strings.Contains(collapsed, "word word") {
		t.Fatalf("collapsed render unexpectedly contains block content:\n%.200s", collapsed)
	}

	m.chatItemExpand[key] = true
	expanded := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(expanded, "word word") {
		t.Fatalf("expanded render missing full block content:\n%.200s", expanded)
	}
	if strings.Contains(expanded, "User input") {
		t.Fatalf("expanded render unexpectedly still shows the collapsed summary:\n%.200s", expanded)
	}

	m.chatItemExpand[key] = false
	recollapsed := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(recollapsed, "User input") {
		t.Fatalf("expected summary row again after re-collapsing:\n%.200s", recollapsed)
	}
}

// FR-09.5/FR-09.6: expansion populates chatRendered (keyed by blockKey) with
// the true markdown render; the summary text is never written to that cache
// surface, matching the Technical Considerations note that the collapsed
// path bypasses renderMarkdown entirely while expansion uses it normally.
func TestExpansionPopulatesMarkdownCacheNotSummary(t *testing.T) {
	body := strings.Repeat("word ", 2000)
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: body}}},
	}})
	key := m.chatItems[0].primary.key

	m.itemTranscriptBody()
	if _, ok := m.chatRendered[key]; ok {
		t.Fatalf("collapsed render must not populate the markdown cache")
	}

	m.chatItemExpand[m.chatItems[0].key] = true
	m.itemTranscriptBody()
	cached, ok := m.chatRendered[key]
	if !ok {
		t.Fatalf("expansion did not populate the markdown cache")
	}
	if strings.Contains(cached, "User input") {
		t.Fatalf("markdown cache holds the collapsed summary instead of the real render: %q", cached)
	}
	if !strings.Contains(cached, "word") {
		t.Fatalf("markdown cache does not hold the real render: %q", cached)
	}
}

// Technical Considerations (search interplay): a search hit landing on a
// collapsed item selects it without requiring expansion for the match to be
// found, since search matches the raw block text regardless of collapse
// state.
func TestSearchHitSelectsCollapsedItemWithoutRequiringExpansion(t *testing.T) {
	body := "# Session update\n\n" + strings.Repeat("filler ", 2000) + "needle-term"
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "unrelated assistant text"}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: body}}},
	}})
	if !m.chatItems[1].oversized {
		t.Fatalf("expected the second item to be flagged oversized")
	}

	m.searchQuery = "needle-term"
	m.rerunSearch()
	if len(m.searchHits) != 1 {
		t.Fatalf("expected exactly one search hit, got %d", len(m.searchHits))
	}
	m.applyCurrentSearchHit()

	if got := m.selectedTranscriptItemKey(); got != m.chatItems[1].key {
		t.Fatalf("search hit did not select the collapsed item: got %+v, want %+v", got, m.chatItems[1].key)
	}
}
