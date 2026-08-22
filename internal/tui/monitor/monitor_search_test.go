package monitor

import (
	"fmt"
	"strings"
	"testing"

	"jig/internal/engine"
	"jig/internal/transcript"
)

func manyTranscriptEntries(n int) []transcript.Entry {
	entries := make([]transcript.Entry, 0, n)
	for i := 1; i <= n; i++ {
		entries = append(entries, transcript.Entry{
			Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{
				Type: transcript.BlockText,
				Text: fmt.Sprintf("message %d", i),
			}},
		})
	}
	return entries
}

func TestTranscriptPagingStaysBoundedAndNavigatesBothDirections(t *testing.T) {
	runDir := writeTranscript(t, "a", manyTranscriptEntries(650))
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	if len(m.chatEntries) != chatWindowMax || m.chatEntries[0].Seq != 351 || !m.chatPage.HasEarlier {
		t.Fatalf("tail page = len:%d first:%d earlier:%v", len(m.chatEntries), m.chatEntries[0].Seq, m.chatPage.HasEarlier)
	}

	m, _ = m.Update(key("["))
	if len(m.chatEntries) != chatWindowMax || m.chatEntries[0].Seq != 51 || len(m.chatNewerEnds) != 1 {
		t.Fatalf("older page = len:%d first:%d newer:%d", len(m.chatEntries), m.chatEntries[0].Seq, len(m.chatNewerEnds))
	}
	if m.chatAutoScroll {
		t.Fatal("loading an older page did not pause follow")
	}

	m, _ = m.Update(key("["))
	if len(m.chatEntries) != 50 || m.chatEntries[0].Seq != 1 || m.chatPage.HasEarlier {
		t.Fatalf("oldest page = len:%d first:%d earlier:%v", len(m.chatEntries), m.chatEntries[0].Seq, m.chatPage.HasEarlier)
	}

	m, _ = m.Update(key("]"))
	if len(m.chatEntries) != chatWindowMax || m.chatEntries[0].Seq != 51 {
		t.Fatalf("newer page = len:%d first:%d", len(m.chatEntries), m.chatEntries[0].Seq)
	}
}

func TestPausedPagedTranscriptSurvivesStepMessage(t *testing.T) {
	runDir := writeTranscript(t, "a", manyTranscriptEntries(350))
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")
	m, _ = m.Update(key("["))
	first := m.chatEntries[0].Seq

	seq := appendTranscriptEntry(t, runDir, "a", transcript.Entry{
		Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{
			Type: transcript.BlockText,
			Text: "new tail",
		}},
	})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: seq,
	}})

	if m.chatEntries[0].Seq != first {
		t.Fatalf("paused page moved from seq %d to %d", first, m.chatEntries[0].Seq)
	}
	if got := m.unseenChatEntries(); got != 1 {
		t.Fatalf("unseen entries = %d, want 1", got)
	}

	m, _ = m.Update(key("f"))
	if m.chatEntries[len(m.chatEntries)-1].Seq != seq || !m.chatAutoScroll {
		t.Fatalf("follow did not return to latest seq %d", seq)
	}
}

func TestTranscriptSearchFindsFilteredLoadedBlocks(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "Alpha prose"},
			{Type: transcript.BlockThinking, Text: "alpha reasoning"},
			{Type: transcript.BlockToolUse, Name: "Read", Input: []byte(`{"file_path":"alpha.go"}`)},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	m.searchQuery = "ALPHA"
	m.rerunSearch()
	if len(m.searchHits) != 3 {
		t.Fatalf("search hits = %d, want 3", len(m.searchHits))
	}

	m.filters.reasoning = true
	m.rebuildLoadedChat(chatItem{})
	m.rerunSearch()
	if len(m.searchHits) != 1 || m.searchHits[0].key.block != 1 {
		t.Fatalf("reasoning-filtered hits = %+v", m.searchHits)
	}
}

func TestErrorFilterKeepsAtomicToolContext(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "ordinary prose"},
			{Type: transcript.BlockToolUse, ToolUseID: "t1", Name: "Read", Input: []byte(`{"file_path":"broken.go"}`)},
		}},
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t1", Content: "permission denied", IsError: true},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")
	m.filters.errors = true
	m.rebuildLoadedChat(chatItem{})

	body := ansiStrip(m.chatBody())
	if strings.Contains(body, "ordinary prose") || !strings.Contains(body, "1 tool call") {
		t.Fatalf("error-filtered body lost tool context or kept prose:\n%s", body)
	}
	m.chatGroupExpand[m.chatGroupHeaders[0].key] = true
	m.rebuildActiveState(m.chatGroupHeaders[0])
	body = ansiStrip(m.chatBody())
	for _, want := range []string{"Read", "permission denied"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expanded error context missing %q:\n%s", want, body)
		}
	}
}

func TestSearchInputAndContextualNavigation(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockThinking, Text: "needle one"},
			{Type: transcript.BlockThinking, Text: "needle two"},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	m, _ = m.Update(key("/"))
	if !m.searchOpen || !m.CapturesText() {
		t.Fatal("/ did not open a text-capturing search")
	}
	for _, r := range "needle" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("enter"))
	if m.searchOpen || m.searchQuery != "needle" || len(m.searchHits) != 2 {
		t.Fatalf("submitted search = open:%v query:%q hits:%d", m.searchOpen, m.searchQuery, len(m.searchHits))
	}
	m, _ = m.Update(key("n"))
	if m.searchHitCursor != 1 {
		t.Fatalf("n selected hit %d, want 1", m.searchHitCursor)
	}
	m, _ = m.Update(key("N"))
	if m.searchHitCursor != 0 {
		t.Fatalf("N selected hit %d, want 0", m.searchHitCursor)
	}
	m, _ = m.Update(key("c"))
	if m.searchQuery != "" || m.filters.active() {
		t.Fatal("c did not clear transcript view state")
	}
}
