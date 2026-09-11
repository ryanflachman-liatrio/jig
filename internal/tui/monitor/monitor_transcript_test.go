package monitor

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/transcript"
)

// denseTranscriptFixture deliberately keeps each migration edge case in one
// place. Item-normalization and rendering tests share it without needing a
// transcript writer to accept malformed raw JSON.
func denseTranscriptFixture() []transcript.Entry {
	return []transcript.Entry{
		{
			Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{
				{Type: transcript.BlockText, Text: "I will inspect the file."},
				{Type: transcript.BlockThinking, Text: "Need the current implementation."},
				{Type: transcript.BlockToolUse, ToolUseID: "read-1", Name: "Read", Input: json.RawMessage(`{"file_path":"internal/tui/monitor/monitor.go"}`)},
			},
		},
		{
			Seq: 2, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{
				// The result intentionally precedes the later use in file order.
				{Type: transcript.BlockToolResult, ToolUseID: "edit-1", Content: "applied"},
				{Type: transcript.BlockToolResult, ToolUseID: "read-1", Content: "package monitor"},
			},
		},
		{
			Seq: 3, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{
				{Type: transcript.BlockToolUse, ToolUseID: "edit-1", Name: "Edit", Input: json.RawMessage(`{"file_path":"monitor.go"}`)},
				{Type: transcript.BlockToolUse, ToolUseID: "pending", Name: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`)},
				{Type: transcript.BlockToolUse, Name: "Unknown", Input: json.RawMessage(`{`)},
				{Type: transcript.BlockType("future_block"), Text: "future payload"},
			},
		},
		{
			Seq: 4, Generation: 0, Iteration: 1, Attempt: 1, Role: transcript.RoleResult,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "retry summary"}},
		},
	}
}

func TestDenseTranscriptFixtureCharacterizesNormalizationCases(t *testing.T) {
	entries := denseTranscriptFixture()
	if len(entries) != 4 {
		t.Fatalf("fixture entries = %d, want 4", len(entries))
	}

	var uses, results, thinking, text, unsupported, malformedInput int
	for _, entry := range entries {
		for _, block := range entry.Blocks {
			switch block.Type {
			case transcript.BlockToolUse:
				uses++
				if !json.Valid(block.Input) {
					malformedInput++
				}
			case transcript.BlockToolResult:
				results++
			case transcript.BlockThinking:
				thinking++
			case transcript.BlockText:
				text++
			default:
				unsupported++
			}
		}
	}

	if uses != 4 || results != 2 || thinking != 1 || text != 2 || unsupported != 1 || malformedInput != 1 {
		t.Fatalf("fixture coverage uses=%d results=%d thinking=%d text=%d unsupported=%d malformed=%d", uses, results, thinking, text, unsupported, malformedInput)
	}
	if entries[1].Blocks[0].ToolUseID != "edit-1" || entries[2].Blocks[0].ToolUseID != "edit-1" {
		t.Fatal("fixture no longer characterizes an out-of-order tool result")
	}
	if entries[2].Blocks[1].ToolUseID != "pending" {
		t.Fatal("fixture no longer characterizes a use-only tool exchange")
	}
	if entries[3].Generation != 0 || entries[3].Iteration != 1 || entries[3].Attempt != 1 {
		t.Fatal("fixture no longer characterizes an execution boundary")
	}
}

func TestTranscriptScrollHotkeysUseConfiguredRowCounts(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	m.chatVP.SetContent(strings.Repeat("line\n", 100))
	m.chatVP.GotoTop()
	m.chatAutoScroll = false

	for _, tc := range []struct {
		key  string
		want int
	}{
		{key: "j", want: 2},
		{key: "J", want: 12},
		{key: "k", want: 10},
		{key: "K", want: 0},
	} {
		m, _ = m.Update(key(tc.key))
		if got := m.chatVP.YOffset(); got != tc.want {
			t.Fatalf("after %q offset = %d, want %d", tc.key, got, tc.want)
		}
	}
}

func TestTranscriptMouseWheelPreservesAndRestoresFollowOnAppend(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	m.focus = focusTranscript
	panels := m.mousePanels()
	base := strings.Repeat("finalized line\n", 40)
	m.chatVP.SetContent(base)
	m.chatVP.GotoBottom()
	m.chatAutoScroll = true

	m, _ = m.Update(tea.MouseWheelMsg{X: panels.transcript.x, Y: panels.transcript.y, Button: tea.MouseWheelUp})
	away := m.chatVP.YOffset()
	if m.chatAutoScroll || m.chatVP.AtBottom() {
		t.Fatal("wheel above bottom did not disable transcript follow")
	}
	m.chatVP.SetContent(base + strings.Repeat("new finalized line\n", 5))
	if m.chatVP.YOffset() != away {
		t.Fatalf("append moved reader from %d to %d while follow was disabled", away, m.chatVP.YOffset())
	}

	m.chatVP.GotoBottom()
	m.updateTranscriptFollow(m.chatVP.AtBottom())
	if !m.chatAutoScroll {
		t.Fatal("reaching bottom did not restore transcript follow")
	}
	m.chatVP.SetContent(base + strings.Repeat("new finalized line\n", 10))
	if m.chatAutoScroll {
		m.chatVP.GotoBottom()
	}
	if !m.chatVP.AtBottom() {
		t.Fatal("followed transcript did not remain at bottom after append")
	}
}

func TestFilePreviewMouseWheelPreservesSelection(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	m.focus = focusTranscript
	m.selKind = "file"
	m.selFile = "/synthetic/output.txt"
	m.cursor = 1
	m.chatVP.SetContent(strings.Repeat("file line\n", 50))
	m.chatVP.GotoTop()
	panels := m.mousePanels()
	m, _ = m.Update(tea.MouseWheelMsg{X: panels.transcript.x, Y: panels.transcript.y, Button: tea.MouseWheelDown})
	if m.chatVP.YOffset() != 3 || m.selKind != "file" || m.selFile != "/synthetic/output.txt" || m.cursor != 1 {
		t.Fatalf("file wheel offset/kind/file/cursor = %d/%q/%q/%d", m.chatVP.YOffset(), m.selKind, m.selFile, m.cursor)
	}
}

func TestBuildTranscriptItemsPairsFixtureToolsByScopedFIFO(t *testing.T) {
	items := buildTranscriptItems(denseTranscriptFixture(), true)
	if len(items) != 8 {
		t.Fatalf("items = %d, want 8", len(items))
	}

	var read, edit, pending, resultOnly *transcriptItem
	for i := range items {
		item := &items[i]
		switch {
		case item.kind == transcriptItemToolExchange && item.coord.toolUseID == "read-1":
			read = item
		case item.kind == transcriptItemToolExchange && item.coord.toolUseID == "edit-1":
			edit = item
		case item.kind == transcriptItemToolExchange && item.coord.toolUseID == "pending":
			pending = item
		case item.kind == transcriptItemToolResult:
			resultOnly = item
		}
	}
	if read == nil || read.toolUse == nil || read.toolResult == nil || read.displayState != toolDisplaySuccess {
		t.Fatalf("read item = %+v, want successful paired exchange", read)
	}
	if edit == nil || edit.toolUse == nil || edit.toolResult == nil || edit.primary.key.seq != 3 {
		t.Fatalf("edit item = %+v, want use-anchored paired exchange", edit)
	}
	if pending == nil || pending.toolResult != nil || pending.displayState != toolDisplayRunning {
		t.Fatalf("pending item = %+v, want running use-only exchange", pending)
	}
	if resultOnly != nil {
		t.Fatalf("unexpected result-only item after matching out-of-order result: %+v", resultOnly)
	}
}
