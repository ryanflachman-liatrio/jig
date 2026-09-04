package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"jig/internal/transcript"
)

func TestWriteDiffUsesSharedHunkPresentation(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/internal/service.go b/internal/service.go",
		"index 1234567..7654321 100644",
		"--- a/internal/service.go",
		"+++ b/internal/service.go",
		"@@ -10 +10 @@ func Start()",
		"-return oldService()",
		"+return newService()",
		"@@ -24 +24 @@ func Stop()",
		"-return oldShutdown()",
		"+return gracefulShutdown()",
	}, "\n")

	var rendered strings.Builder
	writeDiff(&rendered, diff)
	got := ansiStrip(rendered.String())
	for _, want := range []string{"Hunk 1", "internal/service.go", "func Start()", "return newService()", "Hunk 2", "func Stop()", "return gracefulShutdown()"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered diff missing %q:\n%s", want, got)
		}
	}
	for _, hidden := range []string{"diff --git", "index 1234567", "--- a/internal", "+++ b/internal"} {
		if strings.Contains(got, hidden) {
			t.Errorf("rendered diff retained metadata %q:\n%s", hidden, got)
		}
	}
	if !strings.Contains(got, fmt.Sprintf("%s\n\n\n", "+return newService()")) {
		t.Errorf("hunks are not separated by two blank lines:\n%s", got)
	}
}

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
