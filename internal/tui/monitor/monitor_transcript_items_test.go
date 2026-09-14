package monitor

import (
	"testing"
	"time"

	"jig/internal/transcript"
)

func TestBuildTranscriptItemsScopedFIFO(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, ToolUseID: "reused", Name: "Read"},
			{Type: transcript.BlockToolUse, ToolUseID: "reused", Name: "Read"},
		}},
		{Seq: 2, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "reused", Content: "first"},
			{Type: transcript.BlockToolResult, ToolUseID: "reused", Content: "second", IsError: true},
		}},
		// An identical ID in another attempt is a different exchange.
		{Seq: 3, Generation: 0, Iteration: 0, Attempt: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "reused", Content: "orphan"},
		}},
	}
	items := buildTranscriptItems(entries, false)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for i, wantState := range []toolDisplayState{toolDisplaySuccess, toolDisplayError} {
		item := items[i]
		if item.kind != transcriptItemToolExchange || item.primary.key != (blockKey{seq: 1, block: i}) || item.displayState != wantState {
			t.Fatalf("item %d = %+v, want FIFO paired exchange", i, item)
		}
	}
	if got := items[2]; got.kind != transcriptItemToolResult || got.displayState != toolDisplayUnknownResult {
		t.Fatalf("attempt-scoped result = %+v, want unknown result-only item", got)
	}
}

func TestBuildTranscriptItemsIncompleteStates(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, ToolUseID: "pending", Name: "Bash"}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, ToolUseID: "missing", Content: "late"}}},
		{Seq: 3, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, ToolUseID: "error", Content: "failed", IsError: true}}},
	}
	if got := buildTranscriptItems(entries, true); got[0].displayState != toolDisplayRunning {
		t.Fatalf("running use-only state = %v, want running", got[0].displayState)
	}
	items := buildTranscriptItems(entries, false)
	if items[0].displayState != toolDisplayUnknownUse {
		t.Fatalf("terminal use-only state = %v, want unknown", items[0].displayState)
	}
	if items[1].displayState != toolDisplayUnknownResult || items[2].displayState != toolDisplayError {
		t.Fatalf("result-only states = %v, %v, want unknown/error", items[1].displayState, items[2].displayState)
	}
}

func TestBuildTranscriptItemsPreservesIndependentEmptyIDBlocks(t *testing.T) {
	entries := []transcript.Entry{{Seq: 9, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
		{Type: transcript.BlockToolUse, Name: "Read"},
		{Type: transcript.BlockToolResult, Content: "orphan"},
		{Type: transcript.BlockType("future"), Text: "kept"},
	}}}
	items := buildTranscriptItems(entries, false)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if items[0].kind != transcriptItemToolExchange || items[0].toolResult != nil ||
		items[1].kind != transcriptItemToolResult || items[2].kind != transcriptItemUnsupported {
		t.Fatalf("empty-ID/unsupported items = %+v", items)
	}
}

func TestNewInitializesTranscriptItemState(t *testing.T) {
	m := New("run")
	if m.chatItemExpand == nil || m.chatItemRendered == nil || m.chatItemLineRanges == nil {
		t.Fatalf("new monitor did not initialize transcript item state: %+v", m)
	}
}

func TestVisibleExecutionBoundariesUseVisibleItemCoordinates(t *testing.T) {
	items := []transcriptItem{
		{coord: toolCorrelationKey{generation: 1}, primary: transcriptBlockRef{key: blockKey{seq: 5}}},
		{coord: toolCorrelationKey{generation: 1}, primary: transcriptBlockRef{key: blockKey{seq: 6}}},
		{coord: toolCorrelationKey{generation: 1, iteration: 1}, primary: transcriptBlockRef{key: blockKey{seq: 8}}},
	}
	boundaries := visibleExecutionBoundaries(items)
	if len(boundaries) != 2 || boundaries[0].before != 0 || boundaries[1].before != 2 {
		t.Fatalf("boundaries = %+v, want initial rerun and visible iteration transition", boundaries)
	}
}

func TestTranscriptItemMembersKeepPairedExchangeAtomic(t *testing.T) {
	use := transcriptBlockRef{key: blockKey{seq: 1, block: 0}}
	result := transcriptBlockRef{key: blockKey{seq: 2, block: 0}}
	members := itemMembers(transcriptItem{primary: use, toolUse: &use, toolResult: &result})
	if len(members) != 2 || members[0] != use || members[1] != result {
		t.Fatalf("members = %+v, want paired use/result", members)
	}
}

func TestTranscriptItemSpacingIsStructural(t *testing.T) {
	text := transcriptItem{kind: transcriptItemText, role: transcript.RoleAssistant}
	if got := itemSpacingBefore(text, text); got != 0 {
		t.Fatalf("same-role prose spacing = %d, want 0", got)
	}
	tool := transcriptItem{kind: transcriptItemToolExchange, role: transcript.RoleAssistant}
	if got := itemSpacingBefore(text, tool); got != 1 {
		t.Fatalf("prose/tool spacing = %d, want 1", got)
	}
	nextAttempt := tool
	nextAttempt.coord.attempt = 1
	if got := itemSpacingBefore(tool, nextAttempt); got != 2 {
		t.Fatalf("execution-boundary spacing = %d, want 2", got)
	}
}

func TestTranscriptReloadBuildsAndPrunesPageLocalItems(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, ToolUseID: "one", Name: "Read"}}},
		{Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, ToolUseID: "one", Content: "ok"}}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolExchange {
		t.Fatalf("page items = %+v, want one paired exchange", m.chatItems)
	}
	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	m.chatItemCursor = 0
	m.loadChatTail()
	if len(m.chatVisibleItems) != 1 || m.chatVisibleItems[m.chatItemCursor].key != key {
		t.Fatalf("same-step reload did not restore item key: %+v", m.chatVisibleItems)
	}
	m.setChatPage(transcript.Page{})
	if len(m.chatItems) != 0 || len(m.chatItemExpand) != 0 {
		t.Fatalf("empty page retained item state: items=%+v expand=%+v", m.chatItems, m.chatItemExpand)
	}
}

func TestTurnTimestampsSelectsFirstAndLast(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn 0 begin"}}},
		{Seq: 2, Ts: "2026-09-14T10:00:04Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn 0 end"}}},
		{Seq: 3, Ts: "2026-09-14T10:00:07Z", Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "turn 1"}}},
	}
	start, end, ok := turnTimestamps(entries, toolCorrelationKey{})
	if !ok {
		t.Fatalf("turnTimestamps ok=false, want true")
	}
	wantStart, _ := time.Parse(time.RFC3339, "2026-09-14T10:00:00Z")
	wantEnd, _ := time.Parse(time.RFC3339, "2026-09-14T10:00:04Z")
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("turn 0 endpoints = (%v,%v), want (%v,%v)", start, end, wantStart, wantEnd)
	}
	// The toolCorrelationKey.toolUseID field is deliberately ignored so a
	// tool exchange and its result fold into one turn.
	start, end, ok = turnTimestamps(entries, toolCorrelationKey{toolUseID: "unused"})
	if !ok || !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("toolUseID must be ignored, got (start=%v,end=%v,ok=%v)", start, end, ok)
	}
}

func TestTurnTimestampsSingleEntryEqualEndpoints(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "only entry"}}},
	}
	start, end, ok := turnTimestamps(entries, toolCorrelationKey{})
	if !ok || !start.Equal(end) {
		t.Fatalf("single entry: got (start=%v,end=%v,ok=%v), want equal endpoints ok=true", start, end, ok)
	}
}

func TestTurnTimestampsUnparseableTsIgnored(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Ts: "", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "no ts"}}},
		{Seq: 2, Ts: "not-a-timestamp", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "bad ts"}}},
	}
	if _, _, ok := turnTimestamps(entries, toolCorrelationKey{}); ok {
		t.Fatalf("turnTimestamps ok=true, want false when no entry has a parseable Ts")
	}
}

func TestTurnTimestampsCoordinateAbsentReturnsFalse(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "wrong coord"}}},
	}
	if _, _, ok := turnTimestamps(entries, toolCorrelationKey{iteration: 5}); ok {
		t.Fatalf("turnTimestamps ok=true, want false when coord has no entries")
	}
}

func TestTranscriptPersistenceOffBuildsNoItems(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.loadChatTail()
	if len(m.chatItems) != 0 || len(m.chatEntries) != 0 {
		t.Fatalf("persistence-off transcript state = items:%d entries:%d", len(m.chatItems), len(m.chatEntries))
	}
	if got := m.itemTranscriptBody(); got != "" || len(m.chatItemRendered) != 0 {
		t.Fatalf("persistence-off render entered card path: body=%q cache=%d", got, len(m.chatItemRendered))
	}
}
