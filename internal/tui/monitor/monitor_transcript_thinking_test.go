package monitor

import (
	"strings"
	"testing"

	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// FR-10.1/FR-10.2: an under-threshold thinking block renders its full
// markdown content inline with no collapse marker, matching how an
// under-threshold user text item already renders unconditionally.
func TestThinkingUnderThresholdRendersFullyExpanded(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "**considering** the options"}}},
	}})
	if m.chatItems[0].oversized {
		t.Fatalf("under-threshold thinking item must not be flagged oversized")
	}
	if itemHasDetail(m.chatItems[0]) {
		t.Fatalf("under-threshold thinking item must not carry a collapse marker")
	}
	raw := m.itemTranscriptBody()
	plain := stripANSI(raw)
	if !strings.Contains(plain, "considering") || !strings.Contains(plain, "options") {
		t.Fatalf("full reasoning content missing from rendered body:\n%s", plain)
	}
	if strings.Contains(plain, shared.CollapsedMarker) || strings.Contains(plain, shared.ExpandedMarker) {
		t.Fatalf("under-threshold thinking item rendered a collapse marker:\n%s", plain)
	}
}

// FR-10.1: the rendered thinking output carries the italic escape sequence
// and the muted foreground styling sourced from Theme.Chat.Thinking, applied
// through the dedicated thinkingRenderer variant (not the plain renderer).
func TestThinkingRendersItalicMutedStyling(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "quiet reasoning prose"}}},
	}})
	raw := m.itemTranscriptBody()
	if !strings.Contains(raw, "\x1b[3") {
		t.Fatalf("rendered thinking output missing italic SGR escape:\n%q", raw)
	}
	rendered := m.renderThinkingMarkdown(m.chatItems[0].primary.key, "quiet reasoning prose")
	if !strings.Contains(rendered, "\x1b[3") {
		t.Fatalf("thinkingRenderer output missing italic SGR escape:\n%q", rendered)
	}
}

// FR-10.3: an oversized thinking block renders the shared collapse-summary
// row when collapsed and full prose when expanded, and its expansion state
// persists across a reload of the same step (setChatPage), matching how
// chatItemExpand already persists for other item kinds.
func TestThinkingOversizedCollapseExpandPersistsAcrossReload(t *testing.T) {
	body := strings.Repeat("reasoning ", (chatTextCollapseBytes/len("reasoning "))+1)
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: body}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	if !m.chatItems[0].oversized {
		t.Fatalf("expected thinking item over %d bytes to be flagged oversized", chatTextCollapseBytes)
	}
	if !itemHasDetail(m.chatItems[0]) {
		t.Fatalf("oversized thinking item must carry a collapse marker")
	}

	collapsed := stripANSI(m.itemTranscriptBody())
	if strings.Contains(collapsed, "reasoning reasoning") {
		t.Fatalf("collapsed oversized thinking item leaked full content:\n%.200s", collapsed)
	}

	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	expanded := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(expanded, "reasoning reasoning") {
		t.Fatalf("expanded oversized thinking item did not render full content:\n%.200s", expanded)
	}

	// Reload the same step (setChatPage): the operator's expansion choice
	// must survive, the same way it does for other item kinds.
	m.setChatPage(transcript.Page{Entries: entries})
	if !m.chatItemExpand[key] {
		t.Fatalf("expansion state did not persist across a reload of the same step")
	}
	reloaded := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(reloaded, "reasoning reasoning") {
		t.Fatalf("reloaded oversized thinking item lost its expanded content:\n%.200s", reloaded)
	}
}
