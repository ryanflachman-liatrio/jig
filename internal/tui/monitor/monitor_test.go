package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/helpchat"
	"jig/internal/interaction"
	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// writeTranscript writes entries to step's transcript.jsonl under a fresh
// per-test runDir and returns that runDir, so a monitor can render from disk.
func writeTranscript(t *testing.T, stepID string, entries []transcript.Entry) string {
	t.Helper()
	runDir := t.TempDir()
	path := datastore.TranscriptPath(runDir, stepID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	w, err := transcript.Create(path)
	if err != nil {
		t.Fatalf("create transcript: %v", err)
	}
	for _, e := range entries {
		if _, err := w.Append(e); err != nil {
			t.Fatalf("append entry: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
	return runDir
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return b
}

// enterChatStep drives the Steps cursor to the given step id (which eagerly
// reloads that step's transcript) and focuses the Transcript panel. The two-panel
// monitor always shows the cursor's step, so this is a cursor move plus a focus
// switch rather than a mode toggle.
func enterChatStep(t *testing.T, m Model, id string) Model {
	t.Helper()
	for m.steps[m.cursor].id != id {
		before := m.cursor
		m, _ = m.Update(key("j"))
		if m.cursor == before {
			t.Fatalf("step %q not found while navigating", id)
		}
	}
	// Force a fresh load: the runDir is typically set by the test after the model
	// is built (so the initial eager load found nothing), and the target may
	// already be under the cursor. Reset chatStep so reloadTranscript re-reads.
	m.chatStep = ""
	m.reloadTranscript()
	m.focus = focusTranscript
	m.refreshPanels()
	return m
}

// TestMonitorChatRendersBlocks renders a transcript with every block kind and
// asserts each surfaces correctly in modeChat. Tool calls are grouped: the
// collapsed group header shows only the count; individual block labels are only
// visible inside an expanded group.
func TestMonitorChatRendersBlocks(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockThinking, Text: "let me look"},
			{Type: transcript.BlockText, Text: "Reading the file now."},
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1",
				Input: rawJSON(t, map[string]string{"file_path": "/tmp/x"})},
		}},
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t1", Content: "file contents"},
		}},
	})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Collapsed view: thinking label, text content, and tool count are present;
	// individual tool block labels are hidden inside the collapsed group.
	body := m.chatBody()
	for _, want := range []string{shared.IconThinking + " reasoning", "Reading the file", "1 tool call"} {
		if !strings.Contains(body, want) {
			t.Fatalf("chat body missing %q:\n%s", want, body)
		}
	}

	// Expanded view (o): individual block labels become visible. The tool_use label
	// now uses a category icon (◈ for file-ops) instead of the generic IconToolCall.
	m, _ = m.Update(key("o"))
	expanded := m.chatBody()
	for _, want := range []string{"◈ Read", shared.IconToolResult + " result", "file contents"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded chat body missing %q:\n%s", want, expanded)
		}
	}
}

func TestMonitorFoldedReadShowsFilenameAndExpandedReadShowsFullInput(t *testing.T) {
	const fullPath = "/workspace/internal/tui/monitor/monitor_transcript.go"
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1",
				Input: rawJSON(t, map[string]string{"file_path": fullPath})},
		}},
	})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = enterChatStep(t, m, "a")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	folded := ansiStrip(m.chatBody())
	if !strings.Contains(folded, "◈ Read · monitor_transcript.go") {
		t.Fatalf("folded Read missing compact filename summary:\n%s", folded)
	}
	if strings.Contains(folded, fullPath) {
		t.Fatalf("folded Read leaked full path:\n%s", folded)
	}

	m, _ = m.Update(key("j"))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	expanded := ansiStrip(m.chatBody())
	if !strings.Contains(expanded, fullPath) {
		t.Fatalf("expanded Read missing complete input:\n%s", expanded)
	}
}

// TestMonitorChatCollapseExpand checks a long tool_result is hidden behind a
// collapsed group by default and reveals its full content once expanded (via o).
func TestMonitorChatCollapseExpand(t *testing.T) {
	// 90 'a's then a marker past the 80-char collapse boundary.
	content := strings.Repeat("a", 90) + "MARKER"
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t1", Content: content},
		}},
	})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Default view: group is collapsed — content not visible, group header shown.
	collapsed := m.chatBody()
	if strings.Contains(collapsed, "MARKER") {
		t.Fatalf("collapsed body leaked content past group boundary:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, shared.CollapsedMarker) {
		t.Fatalf("collapsed body missing collapsed group marker:\n%s", collapsed)
	}

	m, _ = m.Update(key("o")) // expand all — group and inner block both expand
	expanded := m.chatBody()
	if !strings.Contains(expanded, "MARKER") {
		t.Fatalf("expanded body did not reveal full content:\n%s", expanded)
	}
}

// TestMonitorChatBlockCursorToggle checks that enter expands a group, n moves
// the cursor into the expanded group, and enter on the inner block reveals its
// full content; a second enter collapses the inner block again.
func TestMonitorChatBlockCursorToggle(t *testing.T) {
	long := strings.Repeat("z", 100) + "END"
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t1", Content: long},
		}},
	})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Initially: one group header in chatBlocks; content not visible.
	if len(m.chatBlocks) != 1 || !m.chatBlocks[0].isGroup {
		t.Fatalf("expected single group header, got chatBlocks: %v", m.chatBlocks)
	}
	if strings.Contains(m.chatBody(), "END") {
		t.Fatalf("content should be hidden behind collapsed group")
	}

	// Enter on group header → group expands; inner block is still collapsed.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatBlocks) != 2 {
		t.Fatalf("after group expand expected 2 chatBlocks, got %d", len(m.chatBlocks))
	}
	if strings.Contains(m.chatBody(), "END") {
		t.Fatalf("inner block should still be collapsed after group expand")
	}

	// j → cursor moves to inner block; enter → inner block expands.
	m, _ = m.Update(key("j"))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.chatBody(), "END") {
		t.Fatalf("enter on inner block did not reveal full content:\n%s", m.chatBody())
	}

	// Enter again → inner block collapses.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(m.chatBody(), "END") {
		t.Fatalf("second enter did not collapse the inner block")
	}
}

// TestGroupToggle checks that enter on a group header expands it (chatBlocks
// grows) and enter again collapses it (chatBlocks shrinks back).
func TestGroupToggle(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1",
				Input: rawJSON(t, map[string]string{"file_path": "main.go"})},
			{Type: transcript.BlockToolUse, Name: "Edit", ToolUseID: "t2",
				Input: rawJSON(t, map[string]string{"file_path": "monitor.go"})},
			{Type: transcript.BlockToolUse, Name: "Bash", ToolUseID: "t3",
				Input: rawJSON(t, map[string]string{"command": "go test ./..."})},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Start: one group header (3 tool calls).
	if len(m.chatBlocks) != 1 || !m.chatBlocks[0].isGroup {
		t.Fatalf("expected 1 group header, got chatBlocks len=%d", len(m.chatBlocks))
	}
	if !strings.Contains(m.chatBody(), "3 tool calls") {
		t.Fatalf("collapsed body missing tool call count:\n%s", m.chatBody())
	}

	// Enter → group expands: header + 3 individual blocks.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatBlocks) != 4 {
		t.Fatalf("after expand expected 4 chatBlocks, got %d", len(m.chatBlocks))
	}
	if !strings.Contains(m.chatBody(), shared.ExpandedMarker) {
		t.Fatalf("expanded body missing expanded marker:\n%s", m.chatBody())
	}

	// Enter again on group header → group collapses.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatBlocks) != 1 {
		t.Fatalf("after collapse expected 1 chatBlock, got %d", len(m.chatBlocks))
	}
	if !strings.Contains(m.chatBody(), shared.CollapsedMarker) {
		t.Fatalf("collapsed body missing collapsed marker:\n%s", m.chatBody())
	}
}

// TestGroupCursorStability checks that when a group is collapsed while the
// cursor is on the group header, the cursor stays at 0 (group header index).
func TestGroupCursorStability(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1"},
			{Type: transcript.BlockToolUse, Name: "Edit", ToolUseID: "t2"},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Expand group → chatBlocks = [header, b0, b1]; cursor stays on header.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.chatBlockCursor != 0 {
		t.Fatalf("after expand cursor should be 0, got %d", m.chatBlockCursor)
	}

	// Navigate into the group to inner block b1 (index 2).
	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("j"))
	if m.chatBlockCursor != 2 {
		t.Fatalf("expected cursor=2 (second inner block), got %d", m.chatBlockCursor)
	}

	// Navigate back to group header with k.
	m, _ = m.Update(key("k"))
	m, _ = m.Update(key("k"))
	if m.chatBlockCursor != 0 {
		t.Fatalf("expected cursor=0 (group header), got %d", m.chatBlockCursor)
	}

	// Collapse group from header; cursor remains at 0.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatBlocks) != 1 {
		t.Fatalf("after collapse expected 1 chatBlock, got %d", len(m.chatBlocks))
	}
	if m.chatBlockCursor != 0 {
		t.Fatalf("after collapse cursor should be 0, got %d", m.chatBlockCursor)
	}
}

// TestGroupNavigation checks that j/k traverse group headers and inner blocks in
// the correct order, and that after the last block in an expanded group j moves
// to the next outer item naturally.
func TestGroupNavigation(t *testing.T) {
	// Transcript: 2 tool_use → group, then a thinking block.
	// chatBlocks when group expanded: [header(0), b0(1), b1(2), thinking(3)].
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1"},
			{Type: transcript.BlockToolUse, Name: "Edit", ToolUseID: "t2"},
			{Type: transcript.BlockThinking, Text: "reasoning"},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Expand group.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatBlocks) != 4 {
		t.Fatalf("expected 4 chatBlocks after expand, got %d: %v", len(m.chatBlocks), m.chatBlocks)
	}

	// j from header (0) → b0 (1) → b1 (2) → thinking (3) → wraps to header (0).
	positions := []int{}
	for i := 0; i < 4; i++ {
		m, _ = m.Update(key("j"))
		positions = append(positions, m.chatBlockCursor)
	}
	want := []int{1, 2, 3, 0}
	for i, got := range positions {
		if got != want[i] {
			t.Fatalf("n press %d: want cursor=%d, got %d", i+1, want[i], got)
		}
	}
}

// TestGroupExpandAll checks that o expands all groups AND all inner blocks
// (full content rendered), and o again collapses everything.
func TestGroupExpandAll(t *testing.T) {
	readInput := rawJSON(t, map[string]string{"file_path": "UNIQUE_READ_PATH"})
	editInput := rawJSON(t, map[string]string{"file_path": "UNIQUE_EDIT_PATH"})
	// Thinking content is padded past the 80-rune collapse boundary so
	// "THINKING_CONTENT" is only visible when the block is expanded.
	thinkingText := strings.Repeat("x", 90) + "THINKING_CONTENT"
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1", Input: readInput},
			{Type: transcript.BlockToolUse, Name: "Edit", ToolUseID: "t2", Input: editInput},
			{Type: transcript.BlockThinking, Text: thinkingText},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Default: collapsed group header and collapsed thinking.
	body := m.chatBody()
	if !strings.Contains(body, shared.CollapsedMarker) {
		t.Fatalf("default body should show collapsed markers:\n%s", body)
	}

	// Press o → chatExpandAll=true: group expands, thinking expands, inner blocks expand.
	m, _ = m.Update(key("o"))
	expanded := m.chatBody()

	// Group header should show ▾.
	if !strings.Contains(expanded, shared.ExpandedMarker) {
		t.Fatalf("expanded body missing expanded marker for group:\n%s", expanded)
	}
	// Inner block content should be visible (proves blocks inside group are expanded).
	if !strings.Contains(expanded, "UNIQUE_READ_PATH") {
		t.Fatalf("expanded body missing inner block content UNIQUE_READ_PATH:\n%s", expanded)
	}
	if !strings.Contains(expanded, "UNIQUE_EDIT_PATH") {
		t.Fatalf("expanded body missing inner block content UNIQUE_EDIT_PATH:\n%s", expanded)
	}
	// Thinking block content should be visible.
	if !strings.Contains(expanded, "THINKING_CONTENT") {
		t.Fatalf("expanded body missing thinking content:\n%s", expanded)
	}

	// Press o again → chatExpandAll=false: all collapse.
	m, _ = m.Update(key("o"))
	collapsed := m.chatBody()
	if strings.Contains(collapsed, "UNIQUE_READ_PATH") {
		t.Fatalf("collapsed body should not show inner block content:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "THINKING_CONTENT") {
		t.Fatalf("collapsed body should not show thinking content:\n%s", collapsed)
	}
}

// TestMonitorChatIterationSeparators checks a loop iteration boundary renders a
// separator between entries.
func TestMonitorChatIterationSeparators(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Iteration: 0, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "first pass"},
		}},
		{Role: transcript.RoleAssistant, Iteration: 1, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "second pass"},
		}},
	})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	if !strings.Contains(m.chatBody(), "iteration 2") {
		t.Fatalf("missing iteration separator:\n%s", m.chatBody())
	}
}

func TestMonitorToolGroupLayout(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1",
				Input: rawJSON(t, map[string]string{"file_path": "config.toml"})},
		}},
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t1", Content: "configuration loaded"},
		}},
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Bash", ToolUseID: "t2",
				Input: rawJSON(t, map[string]string{"command": "go test ./internal/tui/monitor"})},
		}},
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t2", Content: "PASS"},
		}},
	})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	collapsedView := m.View()
	collapsed := ansiStrip(m.chatBody())
	var groupLine string
	for _, line := range strings.Split(collapsed, "\n") {
		if strings.Contains(line, "tool calls") {
			groupLine = strings.TrimSpace(line)
			break
		}
	}
	if groupLine != shared.BarThick+" "+shared.CollapsedMarker+" 2 tool calls" {
		t.Fatalf("collapsed group = %q, want count only:\n%s", groupLine, collapsed)
	}
	for _, hidden := range []string{"Read", "Bash", "configuration loaded", "PASS"} {
		if strings.Contains(collapsed, hidden) {
			t.Fatalf("collapsed group exposed %q:\n%s", hidden, collapsed)
		}
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	expandedView := m.View()
	expanded := ansiStrip(m.chatBody())
	lines := strings.Split(expanded, "\n")
	var blockLines []int
	for i, line := range lines {
		if strings.Contains(line, "◈ Read") ||
			strings.Contains(line, shared.IconToolResult+" result") ||
			strings.Contains(line, "$ Run") {
			blockLines = append(blockLines, i)
		}
	}
	if len(blockLines) != 4 {
		t.Fatalf("expanded group has %d tool/result messages, want 4:\n%s", len(blockLines), expanded)
	}
	for i := 1; i < len(blockLines); i++ {
		separated := false
		for _, line := range lines[blockLines[i-1]+1 : blockLines[i]] {
			if strings.TrimSpace(line) == "" {
				separated = true
				break
			}
		}
		if !separated {
			t.Fatalf("messages %d and %d have no blank line between them:\n%s", i, i+1, expanded)
		}
	}

	if dir := os.Getenv("JIG_UI_SNAPSHOT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create snapshot dir: %v", err)
		}
		for name, view := range map[string]string{
			"tool-group-folded.ansi":   collapsedView,
			"tool-group-unfolded.ansi": expandedView,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(view), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}
}

// TestLoadChatGroupDetection verifies the single-pass accumulator produces the
// correct chatGroupHeaders for various block sequences and edge cases.
func TestLoadChatGroupDetection(t *testing.T) {
	mkUse := func(name, id string) transcript.Block {
		return transcript.Block{Type: transcript.BlockToolUse, Name: name, ToolUseID: id}
	}
	mkResult := func(id string) transcript.Block {
		return transcript.Block{Type: transcript.BlockToolResult, ToolUseID: id}
	}
	mkThink := func() transcript.Block {
		return transcript.Block{Type: transcript.BlockThinking, Text: "t"}
	}
	mkText := func() transcript.Block {
		return transcript.Block{Type: transcript.BlockText, Text: "hello"}
	}

	cases := []struct {
		name       string
		entries    []transcript.Entry
		wantGroups int // expected len(chatGroupHeaders)
		wantItems  int // expected len(chatBlocks) (collapsed)
	}{
		{
			name: "consecutive tool_use+result → one group",
			entries: []transcript.Entry{
				{Role: transcript.RoleAssistant, Blocks: []transcript.Block{mkUse("Read", "t1")}},
				{Role: transcript.RoleUser, Blocks: []transcript.Block{mkResult("t1")}},
			},
			wantGroups: 1, wantItems: 1,
		},
		{
			name: "thinking between tool blocks → two groups",
			entries: []transcript.Entry{
				{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
					mkUse("Read", "t1"), mkThink(), mkUse("Edit", "t2"),
				}},
			},
			wantGroups: 3, wantItems: 3, // group(Read) + thinking + group(Edit)
		},
		{
			name: "text between tool blocks → two groups",
			entries: []transcript.Entry{
				{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
					mkUse("Read", "t1"), mkText(), mkUse("Edit", "t2"),
				}},
			},
			wantGroups: 2, wantItems: 2, // group(Read) + group(Edit); text is not a nav item
		},
		{
			name: "cross-entry group (tool_use in entry 1, result in entry 2)",
			entries: []transcript.Entry{
				{Role: transcript.RoleAssistant, Blocks: []transcript.Block{mkUse("Read", "t1")}},
				{Role: transcript.RoleUser, Blocks: []transcript.Block{mkResult("t1")}},
			},
			wantGroups: 1, wantItems: 1,
		},
		{
			name: "orphaned tool_use (no result) → included in group, no panic",
			entries: []transcript.Entry{
				{Role: transcript.RoleAssistant, Blocks: []transcript.Block{mkUse("Read", "t1")}},
			},
			wantGroups: 1, wantItems: 1,
		},
		{
			name: "orphaned tool_result (no preceding use) → included in group, no panic",
			entries: []transcript.Entry{
				{Role: transcript.RoleUser, Blocks: []transcript.Block{mkResult("t1")}},
			},
			wantGroups: 1, wantItems: 1,
		},
		{
			name:       "empty transcript → zero groups, zero blocks, cursor=0",
			entries:    nil,
			wantGroups: 0, wantItems: 0,
		},
		{
			name: "text-only transcript → zero nav items",
			entries: []transcript.Entry{
				{Role: transcript.RoleAssistant, Blocks: []transcript.Block{mkText()}},
			},
			wantGroups: 0, wantItems: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var runDir string
			if tc.entries != nil {
				runDir = writeTranscript(t, "a", tc.entries)
			}
			m := newMonitorWithSteps(t)
			m.RunDir = runDir
			m = enterChatStep(t, m, "a")

			if len(m.chatGroupHeaders) != tc.wantGroups {
				t.Fatalf("chatGroupHeaders len: got %d, want %d", len(m.chatGroupHeaders), tc.wantGroups)
			}
			if len(m.chatBlocks) != tc.wantItems {
				t.Fatalf("chatBlocks len: got %d, want %d", len(m.chatBlocks), tc.wantItems)
			}
			// Cursor must always be a valid index (or 0 when empty).
			if m.chatBlockCursor != 0 {
				t.Fatalf("cursor should be 0 for collapsed view, got %d", m.chatBlockCursor)
			}
			// Must not panic when calling chatBody.
			_ = m.chatBody()
		})
	}
}

// TestGroupExpandReset verifies that chatGroupExpand is cleared when the user
// navigates to a different step (reloadTranscript).
func TestGroupExpandReset(t *testing.T) {
	runDirA := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1"},
		}},
	})
	runDirB := writeTranscript(t, "b", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Edit", ToolUseID: "t2"},
		}},
	})
	// Use the first runDir (a) since both steps live under the same run.
	// Write step b's transcript to runDirA so the monitor can find it.
	if err := os.MkdirAll(datastore.TranscriptPath(runDirA, "b")[:len(datastore.TranscriptPath(runDirA, "b"))-len("transcript.jsonl")], 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Re-use writeTranscript for step b, but under runDirA.
	srcPath := datastore.TranscriptPath(runDirB, "b")
	dstPath := datastore.TranscriptPath(runDirA, "b")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read transcript b: %v", err)
	}
	if err := os.WriteFile(dstPath, data, 0o644); err != nil {
		t.Fatalf("write transcript b to runDirA: %v", err)
	}

	m := newMonitorWithSteps(t)
	m.RunDir = runDirA
	m = enterChatStep(t, m, "a")

	// Expand the group in step a.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatGroupExpand) == 0 {
		t.Fatalf("expected chatGroupExpand to be non-empty after expand")
	}

	// Navigate to step b → reloadTranscript clears chatGroupExpand.
	m, _ = m.Update(key("j")) // move to step b in steps panel
	// Force reload by resetting chatStep so reloadTranscript triggers.
	m.chatStep = ""
	m.reloadTranscript()

	if len(m.chatGroupExpand) != 0 {
		t.Fatalf("chatGroupExpand should be empty after step change, got %v", m.chatGroupExpand)
	}
}

// TestGroupExpandPreservedOnResize verifies that expanding a group and then
// resizing the terminal leaves the group expanded (chatGroupExpand unchanged).
func TestGroupExpandPreservedOnResize(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Name: "Read", ToolUseID: "t1"},
		}},
	})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	// Expand the group; record the group key.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatGroupExpand) == 0 {
		t.Fatalf("group should be expanded after Enter")
	}

	// Send a WindowSizeMsg (resize).
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Group expansion state must be preserved.
	if len(m.chatGroupExpand) == 0 {
		t.Fatalf("chatGroupExpand was cleared by WindowSizeMsg; must be preserved")
	}
	if !strings.Contains(m.chatBody(), shared.ExpandedMarker) {
		t.Fatalf("group should still be expanded after resize:\n%s", m.chatBody())
	}
}

// TestMonitorChatNoTranscript shows a graceful placeholder when persistence is
// off (no runDir) rather than a dead end.
func TestMonitorChatNoTranscript(t *testing.T) {
	m := newMonitorWithSteps(t) // runDir stays ""
	m = enterChatStep(t, m, "a")
	if !strings.Contains(m.chatBody(), "persistence off") {
		t.Fatalf("expected persistence-off placeholder:\n%s", m.chatBody())
	}
}

// TestMonitorChatCommandOutput checks a command step's captured system/text
// entry renders in modeChat, so enter is never a dead end for command steps
// (Phase 6).
func TestMonitorChatCommandOutput(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleSystem, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "build output here"},
		}},
	})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	if !strings.Contains(m.chatBody(), "build output here") {
		t.Fatalf("command output not rendered in chat:\n%s", m.chatBody())
	}
}

// TestMonitorChatReviewFallback checks drilling into a review step shows its
// diff in the Transcript panel. Verdict choices live in the gate strip, not here
// (Decision 2 / ADR 0005).
func TestMonitorChatReviewFallback(t *testing.T) {
	m := newMonitorWithSteps(t) // no runDir: review steps have no transcript

	// A review arrives; Decision 6 means no auto-focus.
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Diff:    "@@ -1 +1 @@\n-old line\n+new line",
		Choices: []string{"approve", "reject"},
	}})

	m = enterChatStep(t, m, "a")
	body := m.chatBody()
	// Diff markers must appear in the Transcript body.
	for _, want := range []string{"new line", "old line", "proposed changes"} {
		if !strings.Contains(body, want) {
			t.Fatalf("review Transcript missing %q:\n%s", want, body)
		}
	}
	// Verdict choices must NOT appear in the Transcript body (they live in the gate).
	for _, notWant := range []string{"[1] approve", "[2] reject"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("verdict choices must not appear in Transcript body, found %q:\n%s", notWant, body)
		}
	}
}

// newMonitorWithSteps returns a sized monitor primed with a three-step run so
// tests can exercise list navigation without the engine.
// TestMonitorWithJournal rebuilds a monitor from a replayed journal — the
// recovery path for a run from a previous session with no in-memory handle. It
// must reconstruct the step list, terminal statuses, and run state, and point the
// Transcript panel at the first step's content read from disk.
func TestMonitorWithJournal(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "recovered from disk"},
		}},
	})

	m := New("r1")
	m.RunDir = runDir
	m = m.WithJournal([]engine.Event{
		engine.RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"a", "b"}},
		engine.StepStatus{RunID: "r1", StepID: "a", To: step.StatusSucceeded},
		engine.StepStatus{RunID: "r1", StepID: "b", To: step.StatusFailed, Err: "boom"},
		engine.RunFinished{RunID: "r1", Failed: true},
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	if len(m.steps) != 2 {
		t.Fatalf("steps: want 2, got %d", len(m.steps))
	}
	if m.workflow != "feature" {
		t.Errorf("workflow: want %q, got %q", "feature", m.workflow)
	}
	if !m.done || !m.failed {
		t.Errorf("run state: want done && failed, got done=%v failed=%v", m.done, m.failed)
	}
	if m.steps[1].status != step.StatusFailed || m.steps[1].err != "boom" {
		t.Errorf("step b: want failed/%q, got %v/%q", "boom", m.steps[1].status, m.steps[1].err)
	}
	// The Transcript panel points at the first step and shows its recovered text.
	if m.chatStep != "a" {
		t.Errorf("chatStep: want %q, got %q", "a", m.chatStep)
	}
	if body := ansiStrip(m.chatBody()); !strings.Contains(body, "recovered from disk") {
		t.Errorf("transcript body missing recovered text:\n%s", body)
	}
}

// TestMonitorCostTokens verifies that StepStatus events drive the per-step
// token/cost metadata line and the run-total row, that both survive the narrow
// (80-col) Steps panel without being clipped, and that a re-run accumulates
// (cumulative, never refunded).
func TestMonitorCostTokens(t *testing.T) {
	m := New("rc")
	// Deliberately narrow so a regression that pushes metrics off the panel edge
	// (the two-line-row fix) is caught: assert against the rendered View, not the
	// raw body, since the raw body always contains the substrings.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m, _ = m.Update(EngineEventMsg{Event: engine.RunStarted{
		RunID: "rc", Workflow: "feature", Steps: []string{"a", "b"},
	}})

	costA, costB := 0.0012, 0.0034
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "rc", StepID: "a", To: step.StatusSucceeded, Cost: &costA, Tokens: 1500,
	}})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "rc", StepID: "b", To: step.StatusSucceeded, Cost: &costB, Tokens: 2000,
	}})

	if m.totalTokens != 3500 {
		t.Errorf("totalTokens = %d, want 3500", m.totalTokens)
	}
	if want := costA + costB; m.totalCost != want {
		t.Errorf("totalCost = %v, want %v", m.totalCost, want)
	}

	// The distinct per-step token values (1.5k, 2.0k) come only from the per-step
	// metadata lines; the total row shows 3.5k. Finding all three in the rendered
	// View proves the per-step metrics are visible (not clipped) and the total row
	// renders too.
	view := ansiStrip(m.View())
	for _, want := range []string{"1.5k tok", "$0.0012", "2.0k tok", "$0.0034", "Total", "3.5k tok", "$0.0046"} {
		if !strings.Contains(view, want) {
			t.Errorf("rendered view missing %q:\n%s", want, view)
		}
	}

	// Re-running step a and completing again: the engine folds the cumulative
	// figure onto the event (earlier attempt + new attempt), so the monitor
	// reflects the higher total — a reset/retry is not refunded.
	// The engine attaches the step's cumulative figure to every transition,
	// including →Running, so the display never blanks mid re-run.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "rc", StepID: "a", From: step.StatusSucceeded, To: step.StatusRunning,
		Cost: &costA, Tokens: 1500,
	}})
	// While re-running, the event still carries the prior cumulative (engine
	// accrues at completion), so the total does not blank.
	if m.totalTokens != 3500 {
		t.Errorf("during re-run totalTokens = %d, want 3500", m.totalTokens)
	}
	costA2 := costA * 2 // cumulative: attempt 1 + attempt 2
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "rc", StepID: "a", From: step.StatusRunning, To: step.StatusSucceeded,
		Cost: &costA2, Tokens: 3000,
	}})
	if m.totalTokens != 3000+2000 {
		t.Errorf("after re-run totalTokens = %d, want 5000", m.totalTokens)
	}
	if want := costA2 + costB; m.totalCost != want {
		t.Errorf("after re-run totalCost = %v, want %v", m.totalCost, want)
	}
}

func newMonitorWithSteps(t *testing.T) Model {
	t.Helper()
	m := New("run-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(EngineEventMsg{Event: engine.RunStarted{
		RunID:    "run-1",
		Workflow: "demo",
		Steps:    []string{"a", "b", "c"},
	}})
	return m
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

// TestMonitorListNavigation checks that j/k move the selection cursor with the
// Steps panel focused (without scrolling) and clamp at the list bounds.
func TestMonitorListNavigation(t *testing.T) {
	m := newMonitorWithSteps(t)

	if m.focus != focusSteps {
		t.Fatalf("expected focusSteps by default, got %v", m.focus)
	}
	if m.cursor != 0 {
		t.Fatalf("expected cursor 0, got %d", m.cursor)
	}

	m, _ = m.Update(key("k")) // already at top, stays
	if m.cursor != 0 {
		t.Fatalf("k at top moved cursor to %d", m.cursor)
	}

	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("j"))
	if m.cursor != 2 {
		t.Fatalf("expected cursor 2 after two j, got %d", m.cursor)
	}

	m, _ = m.Update(key("j")) // at bottom, clamps
	if m.cursor != 2 {
		t.Fatalf("j at bottom moved cursor to %d", m.cursor)
	}

	m, _ = m.Update(key("k"))
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1 after k, got %d", m.cursor)
	}

	// Selected row is marked in the list body with the cursor bar.
	if !strings.Contains(m.body(), shared.CursorBar) {
		t.Fatalf("list body missing cursor marker:\n%s", m.body())
	}
}

// TestMonitorEnterAndBack checks enter crosses focus into the Transcript panel,
// esc returns focus to Steps, and a second esc emits showRunsMsg. The Transcript
// always shows the cursor's step (eager reload), so moving the cursor to "b"
// points the transcript at "b".
func TestMonitorEnterAndBack(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("j")) // cursor → step "b"; eager reload points transcript at "b"

	if m.chatStep != "b" {
		t.Fatalf("expected chatStep b after cursor move, got %q", m.chatStep)
	}

	m, _ = m.Update(key("enter"))
	if m.focus != focusTranscript {
		t.Fatalf("enter did not focus the Transcript panel, got %v", m.focus)
	}

	m, _ = m.Update(key("esc"))
	if m.focus != focusSteps {
		t.Fatalf("esc did not return focus to Steps, got %v", m.focus)
	}

	_, cmd := m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("esc with Steps focused produced no command")
	}
	if _, ok := cmd().(ShowRunsMsg); !ok {
		t.Fatalf("esc with Steps focused did not emit showRunsMsg, got %T", cmd())
	}
}

// TestMonitorChatScrolls confirms j with the Transcript focused moves the block
// cursor (not the step list cursor), and J scrolls the viewport without moving
// the list cursor.
func TestMonitorChatScrolls(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("j")) // step list: cursor → 1
	m, _ = m.Update(key("enter"))
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript, got %v", m.focus)
	}
	before := m.cursor
	// j in Transcript focus moves the block cursor, not the step list cursor.
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j with Transcript focused moved the list cursor from %d to %d", before, m.cursor)
	}
	// J scrolls the viewport — also must not move the list cursor.
	m, _ = m.Update(key("J"))
	if m.cursor != before {
		t.Fatalf("J with Transcript focused moved the list cursor from %d to %d", before, m.cursor)
	}
}

// TestMonitorReviewQueued verifies a review gate arriving is enqueued without
// stealing focus (Decision 6), renders the verdict picker in the gate strip, and
// that digit keys select a verdict once the user tabs to the gate.
func TestMonitorReviewQueued(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("enter")) // focus the Transcript panel for step "a"
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript, got %v", m.focus)
	}

	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
	// Decision 6: arrivals do not steal focus.
	if m.focus != focusTranscript {
		t.Fatalf("review arrival must not steal focus, got %v", m.focus)
	}
	if len(m.inputQueue) == 0 {
		t.Fatal("review event not added to input queue")
	}
	if !strings.Contains(m.gateStrip(), "(review)") {
		t.Fatalf("review gate strip not shown:\n%s", m.gateStrip())
	}

	// Tab from Transcript → Gate, then send verdict.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != focusGate {
		t.Fatalf("expected focusGate after tab, got %v", m.focus)
	}
	_, cmd := m.Update(key("1"))
	if cmd == nil {
		t.Fatal("digit key produced no verdict command")
	}
	vm, ok := cmd().(ReviewVerdictMsg)
	if !ok {
		t.Fatalf("expected reviewVerdictMsg, got %T", cmd())
	}
	if vm.Verdict != "approve" {
		t.Fatalf("expected verdict approve, got %q", vm.Verdict)
	}
}

// TestMonitorRecoveryGate verifies that a RecoveryRequest surfaces a recovery
// gate whose r/a actions emit recoverResponseMsg, and that guidance ([g]) is
// hidden when the failed step has no resumable session.
func TestMonitorRecoveryGate(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.RecoveryRequest{
		RunID:     "run-1",
		StepID:    "a",
		Err:       "git worktree add: fatal: a branch named 'jig/x/a' already exists",
		CanResume: false,
	}})
	if len(m.inputQueue) == 0 {
		t.Fatal("recovery event not added to input queue")
	}
	strip := m.gateStrip()
	if !strings.Contains(strip, "(recovery)") {
		t.Fatalf("recovery gate strip not shown:\n%s", strip)
	}
	if !strings.Contains(strip, "[r] retry") || !strings.Contains(strip, "[a] abort") {
		t.Fatalf("recovery actions not rendered:\n%s", strip)
	}
	// CanResume=false ⇒ no guidance affordance.
	if strings.Contains(strip, "[g] retry with guidance") {
		t.Fatalf("guidance shown despite CanResume=false:\n%s", strip)
	}

	m.focus = focusGate
	_, cmd := m.Update(key("r"))
	if cmd == nil {
		t.Fatal("r produced no command")
	}
	rr, ok := cmd().(RecoverResponseMsg)
	if !ok {
		t.Fatalf("expected recoverResponseMsg, got %T", cmd())
	}
	if rr.Action != engine.RecoverRetry || rr.StepID != "a" {
		t.Fatalf("got action=%q stepID=%q; want retry/a", rr.Action, rr.StepID)
	}

	// Abort routes to RecoverAbort.
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(EngineEventMsg{Event: engine.RecoveryRequest{RunID: "run-1", StepID: "a", Err: "boom"}})
	m2.focus = focusGate
	_, cmd = m2.Update(key("a"))
	if cmd == nil {
		t.Fatal("a produced no command")
	}
	if rr, ok := cmd().(RecoverResponseMsg); !ok || rr.Action != engine.RecoverAbort {
		t.Fatalf("expected RecoverAbort, got %+v (%T)", cmd(), cmd())
	}
}

// TestMonitorRecoveryGuidance verifies the [g] guidance path: it opens a compose
// box and submitting emits a RecoverResume with the typed text, but only when the
// failed step is resumable.
func TestMonitorRecoveryGuidance(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.RecoveryRequest{
		RunID:     "run-1",
		StepID:    "a",
		Err:       "agent gave up",
		CanResume: true,
	}})
	if !strings.Contains(m.gateStrip(), "[g] retry with guidance") {
		t.Fatalf("guidance affordance missing when CanResume=true:\n%s", m.gateStrip())
	}

	m.focus = focusGate
	m, _ = m.Update(key("g")) // enter compose
	if entry, ok := m.activeEntry(); !ok || !entry.composing {
		t.Fatal("g did not enter guidance compose mode")
	}
	// Type guidance, then submit.
	m, _ = m.Update(key("h"))
	m, _ = m.Update(key("i"))
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no command while composing guidance")
	}
	rr, ok := cmd().(RecoverResponseMsg)
	if !ok {
		t.Fatalf("expected recoverResponseMsg, got %T", cmd())
	}
	if rr.Action != engine.RecoverResume || rr.StepID != "a" {
		t.Fatalf("got action=%q stepID=%q; want resume/a", rr.Action, rr.StepID)
	}
	if rr.Text != "hi" {
		t.Fatalf("guidance text = %q, want %q", rr.Text, "hi")
	}
}

// TestMonitorGateConsumesKeys confirms that with the Gate focused, a review
// verdict picker consumes j/k (they are not a review action) so the Steps cursor
// does not move — but focus can still be switched away (see
// TestMonitorGateNonBlocking).
func TestMonitorGateConsumesKeys(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
	// Decision 6: no auto-focus — manually focus the gate to test key consumption.
	m.focus = focusGate
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j moved cursor while Gate focused: %d → %d", before, m.cursor)
	}
}

// TestMonitorMessageCount shows StepMessage liveness events surface as a
// per-step message count in the list.
func TestMonitorMessageCount(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: 3,
	}})
	if got := m.msgCount["a"]; got != 3 {
		t.Fatalf("expected msgCount 3, got %d", got)
	}
	if !strings.Contains(m.body(), "3 msg") {
		t.Fatalf("list body missing message count:\n%s", m.body())
	}
	// Stale (lower) seq must not lower the count.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: 2,
	}})
	if got := m.msgCount["a"]; got != 3 {
		t.Fatalf("stale seq lowered count to %d", got)
	}
}

// TestMonitorAgentQuestionShowsPanel verifies that an AgentQuestion event shows
// the question overlay in modeList and updates the footer status.
func TestMonitorAgentQuestionShowsPanel(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "tu1",
		selectQuestion("format", "Format", "Which format should we use?", false,
			interaction.QuestionOption{Value: "JSON", Label: "JSON", Description: "structured output"},
			interaction.QuestionOption{Value: "Text", Label: "Text", Description: "plain output"},
		),
	)})

	if len(m.inputQueue) == 0 {
		t.Fatal("AgentQuestion not added to input queue")
	}
	if m.inputQueue[0].kind != inputKindQuestion {
		t.Fatalf("expected inputKindQuestion, got %v", m.inputQueue[0].kind)
	}
	// Decision 6: arrivals do not steal focus.
	if m.focus != focusSteps {
		t.Fatalf("question arrival must not steal focus, got %v", m.focus)
	}

	body := m.gateStrip()
	for _, want := range []string{"(question)", "Which format should we use?", "[Format]", "JSON", "Text", "structured output"} {
		if !strings.Contains(body, want) {
			t.Fatalf("question body missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(m.footerView(), "awaiting answer") {
		t.Fatalf("footer missing 'awaiting answer':\n%s", m.footerView())
	}
}

// TestMonitorAgentQuestionSelectEmits verifies that pressing a digit key for a
// single-select question emits agentQuestionResponseMsg with the correct answer.
func TestMonitorAgentQuestionSelectEmits(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "tu1",
		selectQuestion("pick", "", "Pick one", false,
			interaction.QuestionOption{Value: "Alpha", Label: "Alpha"},
			interaction.QuestionOption{Value: "Beta", Label: "Beta"},
		),
	)})

	// Decision 6: no auto-focus — manually focus the gate to answer.
	m.focus = focusGate
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("enter"))
	m, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("review submit produced no command")
	}
	msg, ok := cmd().(AgentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd())
	}
	if msg.Response.RequestID != "tu1" {
		t.Fatalf("expected request id tu1, got %q", msg.Response.RequestID)
	}
	if got := msg.Response.Answers["pick"].Values; len(got) != 1 || got[0] != "Beta" {
		t.Fatalf("expected Beta answer, got %v", got)
	}
	if len(m.inputQueue) > 0 {
		t.Fatal("question entry should be removed from queue after answer is submitted")
	}
}

// TestMonitorAgentQuestionMultiSelect verifies that multiSelect questions require
// toggling and enter to confirm, and accumulate multiple selections.
func TestMonitorAgentQuestionMultiSelect(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "tu2",
		selectQuestion("features", "", "Select features", true,
			interaction.QuestionOption{Value: "Cache", Label: "Cache"},
			interaction.QuestionOption{Value: "Retry", Label: "Retry"},
			interaction.QuestionOption{Value: "Logging", Label: "Logging"},
		),
	)})

	// Decision 6: no auto-focus — manually focus the gate.
	m.focus = focusGate

	// Toggle options 1 and 3.
	m, _ = m.Update(key("space"))
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("space"))

	body := m.gateStrip()
	if !strings.Contains(body, "[x]") {
		t.Fatalf("toggled options not shown:\n%s", body)
	}

	// Confirm the field, then submit the review.
	m, _ = m.Update(key("enter"))
	m, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	resp, ok := cmd().(AgentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd())
	}
	values := resp.Response.Answers["features"].Values
	if !slices.Contains(values, "Logging") {
		t.Fatalf("expected Logging in answer, got %v", values)
	}
	if !slices.Contains(values, "Cache") {
		t.Fatalf("expected Cache in answer, got %v", values)
	}
}

// TestMonitorAgentQuestionConsumesKeys confirms j/k are consumed by the focused
// question gate (they are not a selection key), so the Steps cursor does not move
// while the Gate is focused.
func TestMonitorAgentQuestionConsumesKeys(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "tu1",
		selectQuestion("q", "", "Q?", false, interaction.QuestionOption{Value: "A", Label: "A"}),
	)})
	// Decision 6: no auto-focus — manually focus the gate to test key consumption.
	m.focus = focusGate

	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j moved cursor during question overlay: %d → %d", before, m.cursor)
	}
}

// TestMonitorStepSubtypeBadge verifies that a failed step with a policy-limit
// subtype (error_max_turns, error_max_budget_usd) shows its annotation in the
// list body, and that ordinary failures show no annotation.
func TestMonitorStepSubtypeBadge(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Step "a" fails with error_max_turns.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID:   "run-1",
		StepID:  "a",
		From:    step.StatusRunning,
		To:      step.StatusFailed,
		Err:     "agent reached the maximum turn limit",
		Subtype: "error_max_turns",
	}})
	// Step "b" fails with error_max_budget_usd.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID:   "run-1",
		StepID:  "b",
		From:    step.StatusRunning,
		To:      step.StatusFailed,
		Err:     "agent exceeded the maximum USD budget",
		Subtype: "error_max_budget_usd",
	}})
	// Step "c" fails with a plain API error (no subtype annotation expected).
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID:   "run-1",
		StepID:  "c",
		From:    step.StatusRunning,
		To:      step.StatusFailed,
		Err:     "unknown agent error",
		Subtype: "error_during_execution",
	}})

	body := m.body()
	if !strings.Contains(body, "max turns") {
		t.Fatalf("list body missing max turns annotation:\n%s", body)
	}
	if !strings.Contains(body, "budget") {
		t.Fatalf("list body missing budget annotation:\n%s", body)
	}
	// Verify step "c" (error_during_execution) shows no special annotation.
	// We can't easily assert a negative per-line, so verify subtypeBadgeLabel directly.
	if subtypeBadgeLabel("error_during_execution") != "" {
		t.Error("error_during_execution should have no badge label")
	}
	if subtypeBadgeLabel("error_max_turns") != "max turns" {
		t.Error("error_max_turns badge label mismatch")
	}
	if subtypeBadgeLabel("error_max_budget_usd") != "budget" {
		t.Error("error_max_budget_usd badge label mismatch")
	}
}

// TestMonitorAgentQuestionClearsOnResume verifies that a queued question entry is
// removed when the step transitions away from StatusNeedsInput.
func TestMonitorAgentQuestionClearsOnResume(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "tu1",
		selectQuestion("q", "", "Q?", false, interaction.QuestionOption{Value: "A", Label: "A"}),
	)})
	if len(m.inputQueue) == 0 {
		t.Fatal("question entry should be queued after AgentQuestion event")
	}

	// Simulate the step resuming after the answer is delivered.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID:  "run-1",
		StepID: "a",
		From:   step.StatusNeedsInput,
		To:     step.StatusRunning,
	}})
	if len(m.inputQueue) > 0 {
		t.Fatal("question entry should be removed from queue when step leaves StatusNeedsInput")
	}
}

// primaryBorderSeq is the SGR truecolor foreground for the Charple primary token
// (#6B50FF → 107;80;255), used to detect which panel's border is focused.
const primaryBorderSeq = "\x1b[38;2;107;80;255m"

// titleLineFor returns the top-edge (title) line of the panel whose title
// contains want, from a rendered two-panel monitor View. Panels are joined
// horizontally, so each rendered row concatenates both panels' cells; the first
// row carries both top edges. It returns that first row for inspection.
func firstRow(view string) string {
	return strings.SplitN(view, "\n", 2)[0]
}

// TestMonitorTwoPanel asserts the monitor renders both titled panels side by side
// and that tab toggles which region's border uses the primary (Charple) style.
func TestMonitorTwoPanel(t *testing.T) {
	m := newMonitorWithSteps(t)
	view := m.View()

	if !strings.Contains(view, "Steps") {
		t.Fatalf("view missing Steps panel title:\n%s", view)
	}
	// The right panel title is the selected step id ("a") — the cursor's step.
	top := firstRow(view)
	if !strings.Contains(top, "a") {
		t.Fatalf("top edge missing selected-step transcript title:\n%s", top)
	}

	// Default focus is Steps: the Steps (left) title should carry the primary
	// color and the Transcript (right) title should not. The left title precedes
	// the right on the first row, so split on the "Transcript"/step boundary is
	// fiddly; instead assert the whole first row contains exactly one primary
	// border run and that it moves after tab.
	if m.focus != focusSteps {
		t.Fatalf("expected default focusSteps, got %v", m.focus)
	}
	beforeCount := strings.Count(top, primaryBorderSeq)
	if beforeCount == 0 {
		t.Fatalf("focused Steps panel should use the primary border color:\n%q", top)
	}

	// Tab moves focus to Transcript; the primary border must move to the right
	// panel. We detect the move by checking the primary sequence now appears
	// after the "Steps" label position rather than before it.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != focusTranscript {
		t.Fatalf("tab did not move focus to Transcript, got %v", m.focus)
	}
	top2 := firstRow(m.View())

	stepsIdx := strings.Index(ansiStrip(top2), "Steps")
	// Find the byte offset in the raw string of the primary sequence and compare
	// to where the Steps title sits: with Transcript focused, the primary run
	// should start to the right of the left panel (i.e. after the Steps region).
	primIdx := strings.Index(top2, primaryBorderSeq)
	if primIdx < 0 {
		t.Fatalf("no primary border after tab:\n%q", top2)
	}
	// The left panel (Steps) is ~stepsW cells; the primary run for a focused
	// Transcript must appear later in the row than the un-focused Steps title.
	if primIdx <= stepsIdx {
		t.Fatalf("primary border did not move to the right panel after tab (primIdx=%d, stepsIdx=%d):\n%q", primIdx, stepsIdx, top2)
	}
}

// ansiStrip removes SGR sequences so index math over visible text is meaningful.
func ansiStrip(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if c == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// TestMonitorGateNonBlocking asserts that a pending gate does not freeze
// navigation (ADR 0002): a focus-switch key still moves focus, gate keys resolve
// the gate with the correct response, and esc/q cancellation delivers the
// cancellation response so no reporter goroutine hangs.
func TestMonitorGateNonBlocking(t *testing.T) {
	// A gate that owes a tool_result: AskUserQuestion.
	makeGate := func() Model {
		m := newMonitorWithSteps(t)
		m, _ = m.Update(EngineEventMsg{Event: questionEvent(
			"run-1", "a", "tu1",
			selectQuestion("pick", "", "Pick one", false,
				interaction.QuestionOption{Value: "Alpha", Label: "Alpha"},
				interaction.QuestionOption{Value: "Beta", Label: "Beta"},
			),
		)})
		return m
	}

	// 1) Navigation is not frozen: with a gate pending but not focused, j/k
	// navigate the Steps panel as normal (Decision 6: no auto-focus).
	m := makeGate()
	if m.focus != focusSteps {
		t.Fatalf("question arrival must not steal focus (Decision 6), got %v", m.focus)
	}
	if len(m.inputQueue) == 0 {
		t.Fatal("question entry should be queued after AgentQuestion event")
	}
	// j/k navigate Steps even while a gate entry is pending.
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor == before {
		t.Fatal("j did not move the Steps cursor while a gate was pending (navigation frozen)")
	}
	// Tab cycles regions as normal; a second tab reaches the Gate.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != focusGate {
		t.Fatalf("expected focusGate after two tabs, got %v", m.focus)
	}
	// The entry is still pending (moving focus did not resolve it).
	if len(m.inputQueue) == 0 {
		t.Fatal("moving focus off the gate should not resolve the entry")
	}

	// 2) Returning focus to the gate and answering resolves it with the response.
	m.focus = focusGate
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("enter"))
	m, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("gate review submit produced no command")
	}
	resp, ok := cmd().(AgentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd())
	}
	if got := resp.Response.Answers["pick"].Values; len(got) != 1 || got[0] != "Beta" {
		t.Fatalf("expected answer Beta, got %v", got)
	}

	// 3) esc while gate-focused blurs to Steps (ADR 0005 §esc-blurs). The entry
	// stays queued; cancellation delivery is task 4.5 (q key → "cancelled").
	m = makeGate()
	m.focus = focusGate
	m, _ = m.Update(key("esc"))
	if m.focus != focusSteps {
		t.Fatalf("esc did not blur gate to Steps, focus=%v", m.focus)
	}
	if len(m.inputQueue) == 0 {
		t.Fatal("esc must not clear the queue — entry must remain for later answer")
	}
}

// TestMonitorEagerReload asserts moving the Steps cursor eagerly reloads the
// Transcript panel to the newly-selected step (Resolved Decision 10).
func TestMonitorEagerReload(t *testing.T) {
	runDirA := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "ALPHA-BODY"},
		}},
	})
	// Write step "b" into the SAME run dir so both transcripts resolve.
	pathB := datastore.TranscriptPath(runDirA, "b")
	if err := os.MkdirAll(filepath.Dir(pathB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	w, err := transcript.Create(pathB)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := w.Append(transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
		{Type: transcript.BlockText, Text: "BETA-BODY"},
	}}); err != nil {
		t.Fatalf("append b: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close b: %v", err)
	}

	m := newMonitorWithSteps(t)
	m.RunDir = runDirA
	// Force the initial eager load now that runDir is set.
	m.chatStep = ""
	m.reloadTranscript()
	m.refreshPanels()

	if got := m.chatBody(); !strings.Contains(got, "ALPHA-BODY") {
		t.Fatalf("transcript for step a not shown before cursor move:\n%s", got)
	}

	// Move the cursor to step "b": the transcript body must switch to b's content.
	m, _ = m.Update(key("j"))
	if m.chatStep != "b" {
		t.Fatalf("cursor move did not re-point transcript, chatStep=%q", m.chatStep)
	}
	body := m.chatBody()
	if !strings.Contains(body, "BETA-BODY") {
		t.Fatalf("transcript did not eagerly reload to step b:\n%s", body)
	}
	if strings.Contains(body, "ALPHA-BODY") {
		t.Fatalf("transcript still shows step a after moving to b:\n%s", body)
	}
}

// TestGateSubmitRouting verifies that submitting an InputRequest entry emits
// agentInputMsg for that entry's stepID and shrinks the queue by one, and that
// submitting the next entry routes to the second stepID.
func TestGateSubmitRouting(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Enqueue two InputRequest entries from distinct steps.
	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "a"}})
	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "b"}})
	if len(m.inputQueue) != 2 {
		t.Fatalf("expected 2 queue entries, got %d", len(m.inputQueue))
	}

	// Focus gate and pre-load text so submit is non-empty.
	m.focus = focusGate
	m.inputQueue[0].draft = "hello"
	m.loadActiveTextarea()

	// Submit active entry → should emit agentInputMsg for step "a".
	m2, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("submit produced no command")
	}
	msg, ok := cmd().(AgentInputMsg)
	if !ok {
		t.Fatalf("expected agentInputMsg, got %T", cmd())
	}
	if msg.StepID != "a" {
		t.Fatalf("expected stepID a, got %q", msg.StepID)
	}
	if msg.Text != "hello" {
		t.Fatalf("expected text hello, got %q", msg.Text)
	}
	if len(m2.inputQueue) != 1 {
		t.Fatalf("expected queue length 1 after submit, got %d", len(m2.inputQueue))
	}

	// Submit the now-active entry → should route to step "b".
	m2.focus = focusGate
	m2.inputQueue[0].draft = "world"
	m2.loadActiveTextarea()
	_, cmd2 := m2.Update(key("enter"))
	if cmd2 == nil {
		t.Fatal("second submit produced no command")
	}
	msg2, ok := cmd2().(AgentInputMsg)
	if !ok {
		t.Fatalf("expected agentInputMsg for second submit, got %T", cmd2())
	}
	if msg2.StepID != "b" {
		t.Fatalf("expected stepID b, got %q", msg2.StepID)
	}
}

// TestQuestionCancel verifies that pressing q on an inputKindQuestion entry
// emits agentQuestionResponseMsg with answer=="cancelled", removes the entry,
// and does not emit showRunsMsg.
func TestQuestionCancel(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "q1",
		selectQuestion("pick", "", "Pick one", false,
			interaction.QuestionOption{Value: "Alpha", Label: "Alpha"},
			interaction.QuestionOption{Value: "Beta", Label: "Beta"},
		),
	)})

	if len(m.inputQueue) == 0 || m.inputQueue[0].kind != inputKindQuestion {
		t.Fatal("expected inputKindQuestion in queue")
	}

	m.focus = focusGate
	m, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q produced no command")
	}
	// Must not emit showRunsMsg.
	if _, isRuns := cmd().(ShowRunsMsg); isRuns {
		t.Fatal("q must not emit showRunsMsg — user stays in monitor")
	}
	// Rerun cmd() to get the actual message (cmd() may only be called once — use a copy).
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "q1",
		selectQuestion("pick", "", "Pick one", false,
			interaction.QuestionOption{Value: "Alpha", Label: "Alpha"},
			interaction.QuestionOption{Value: "Beta", Label: "Beta"},
		),
	)})
	m2.focus = focusGate
	_, cmd2 := m2.Update(key("q"))
	resp, ok := cmd2().(AgentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd2())
	}
	if resp.Response.Action != interaction.ActionCancel {
		t.Fatalf("expected cancel action, got %q", resp.Response.Action)
	}
	if resp.StepID != "a" {
		t.Fatalf("expected stepID a, got %q", resp.StepID)
	}
	// Queue should be empty after cancel.
	if len(m.inputQueue) != 0 {
		t.Fatalf("expected empty queue after cancel, got %d entries", len(m.inputQueue))
	}
}

// TestReviewComposeIsolation verifies that composing a message on one review
// entry does not affect another entry's composing state or draft.
func TestReviewComposeIsolation(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Enqueue two ReviewRequests from distinct steps.
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:        "run-1",
		StepID:       "a",
		Choices:      []string{"approve", "reject"},
		AllowMessage: true,
	}})
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:        "run-1",
		StepID:       "b",
		Choices:      []string{"approve", "reject"},
		AllowMessage: true,
	}})

	if len(m.inputQueue) != 2 {
		t.Fatalf("expected 2 queue entries, got %d", len(m.inputQueue))
	}

	// Focus gate on entry 0 (step "a") and start composing.
	m.focus = focusGate
	m, _ = m.Update(key("m")) // press [m] to start compose
	if !m.inputQueue[0].composing {
		t.Fatal("expected composing=true on entry 0 after [m]")
	}

	// Tab to entry 1 (step "b").
	m, _ = m.Update(key("tab"))
	if m.activeInputIdx != 1 {
		t.Fatalf("expected activeInputIdx 1, got %d", m.activeInputIdx)
	}
	// Entry 1 must not be composing.
	if m.inputQueue[1].composing {
		t.Fatal("tab to entry 1 must not carry over composing state")
	}
	// Entry 1's draft must be empty.
	if m.inputQueue[1].draft != "" {
		t.Fatalf("entry 1 draft should be empty, got %q", m.inputQueue[1].draft)
	}
}

// TestReviewDiffInTranscript verifies that selecting a review step shows its diff
// in the Transcript panel (chatBody) while activeInputIdx is unaffected.
func TestReviewDiffInTranscript(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Enqueue a review with a diff.
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Diff:    "@@ -1 +1 @@\n-removed\n+added",
		Choices: []string{"approve", "reject"},
	}})

	idxBefore := m.activeInputIdx

	// Select the review step in Steps — this triggers reloadTranscript.
	m = enterChatStep(t, m, "a")

	// activeInputIdx must be unchanged (Decision 2: navigations are independent).
	if m.activeInputIdx != idxBefore {
		t.Fatalf("reloadTranscript must not touch activeInputIdx: was %d, now %d", idxBefore, m.activeInputIdx)
	}

	body := m.chatBody()
	for _, want := range []string{"removed", "added", "proposed changes"} {
		if !strings.Contains(body, want) {
			t.Fatalf("chatBody missing %q:\n%s", want, body)
		}
	}
	// Choices must not appear in Transcript.
	for _, notWant := range []string{"[1] approve", "[2] reject"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("verdict choice %q must not appear in Transcript:\n%s", notWant, body)
		}
	}
}

// TestQuestionScroll verifies that ↓/↑ (j/k) scroll the option list within the
// fixed strip height, that the height is constant regardless of scroll position,
// and that digit keys select the correct absolute option index even when scrolled.
func TestQuestionScroll(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Build a question with enough options to overflow the fixed gate height.
	var opts []interaction.QuestionOption
	for i := 1; i <= 10; i++ {
		label := fmt.Sprintf("Option%d", i)
		opts = append(opts, interaction.QuestionOption{Value: label, Label: label})
	}
	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "q1", selectQuestion("pick", "", "Pick one", false, opts...),
	)})
	m.focus = focusGate

	// Verify scroll is actually needed (budget < len(options)).
	budget := m.gateBodyHeight() - gateHeaderRows - 2 // gateHeaderRows + question + blank
	if len(opts) <= budget {
		t.Skipf("options (%d) don't exceed budget (%d) — increase option count", len(opts), budget)
	}

	stripH := func() int { return lipgloss.Height(m.gateStrip()) }
	h0 := stripH()

	// Initial state: Option1 visible, Option10 not.
	strip0 := ansiStrip(m.gateStrip())
	if !strings.Contains(strip0, "Option1") {
		t.Fatalf("Option1 not visible at scrollOffset=0:\n%s", strip0)
	}

	// Move the cursor far enough to scroll down.
	for i := 0; i < 8; i++ {
		m, _ = m.Update(key("j"))
	}
	// Height must be unchanged.
	if stripH() != h0 {
		t.Fatalf("gate strip height changed after scroll: was %d, now %d", h0, stripH())
	}
	strip1 := ansiStrip(m.gateStrip())
	// Option1 should now be scrolled off; a higher-indexed option should appear.
	if strings.Contains(strip1, "[1] Option1") {
		t.Fatalf("Option1 still in view after scrolling down:\n%s", strip1)
	}
	// ▲ more indicator must be present.
	if !strings.Contains(strip1, "▲ more") {
		t.Fatalf("▲ more indicator missing after scroll:\n%s", strip1)
	}

	// Move back to the first option.
	for i := 0; i < 8; i++ {
		m, _ = m.Update(key("k"))
	}
	if stripH() != h0 {
		t.Fatalf("gate strip height changed after scroll up: was %d, now %d", h0, stripH())
	}

	// Scroll down and select the ninth option by cursor, then submit review.
	for i := 0; i < 8; i++ {
		m, _ = m.Update(key("j"))
	}
	m, _ = m.Update(key("enter"))
	m2, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("review submit produced no command while scrolled")
	}
	resp, ok := cmd().(AgentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg from digit 1, got %T", cmd())
	}
	if got := resp.Response.Answers["pick"].Values; len(got) != 1 || got[0] != "Option9" {
		t.Fatalf("cursor selected wrong option: %v", got)
	}
	if len(m2.inputQueue) != 0 {
		t.Fatalf("queue should be empty after answering, got %d", len(m2.inputQueue))
	}
}

// TestCaptureUnit6Scroll captures View() of a scrollable AgentQuestion,
// showing the windowed option list with ▲/▼ indicators.
func TestCaptureUnit6Scroll(t *testing.T) {
	const artifactPath = "../../docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit6-scroll.txt"
	m := newMonitorWithSteps(t)

	var opts []interaction.QuestionOption
	for i := 1; i <= 10; i++ {
		label := fmt.Sprintf("Option%d", i)
		opts = append(opts, interaction.QuestionOption{Value: label, Label: label})
	}
	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "a", "q1", selectQuestion("pick", "", "Pick one", false, opts...),
	)})
	m.focus = focusGate

	// Scroll down so both ▲ and ▼ are visible.
	for i := 0; i < 8; i++ {
		m, _ = m.Update(key("j"))
	}

	view := ansiStrip(m.View())
	if err := os.WriteFile(artifactPath, []byte(view), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
}

// TestCaptureUnit5ReviewDiff captures View() with a review step selected,
// showing the diff in the Transcript panel and the gate entry with verdict choices.
func TestCaptureUnit5ReviewDiff(t *testing.T) {
	const artifactPath = "../../docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit5-review-diff.txt"
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:        "run-1",
		StepID:       "a",
		Diff:         "@@ -1,3 +1,3 @@\n context\n-old line\n+new line\n context",
		Choices:      []string{"approve", "reject"},
		AllowMessage: true,
	}})
	// Select the review step so Transcript shows the diff.
	m = enterChatStep(t, m, "a")
	m.focus = focusGate

	view := ansiStrip(m.View())
	if err := os.WriteFile(artifactPath, []byte(view), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
}

// TestCaptureUnit4Drain captures View() frames draining a two-entry InputRequest
// queue to a single entry and then to the empty placeholder, writing the result
// to docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit4-drain.txt.
func TestCaptureUnit4Drain(t *testing.T) {
	const artifactPath = "../../docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit4-drain.txt"
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "a"}})
	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "b"}})
	m.focus = focusGate

	var frames []string

	// Frame 1: [1 / 2] — two entries, first active.
	frames = append(frames, ansiStrip(m.View()))

	// Submit entry "a" (pre-load draft so enter fires).
	m.inputQueue[0].draft = "answer-a"
	m.loadActiveTextarea()
	m, _ = m.Update(key("enter"))

	// Frame 2: [1 / 1] — one entry remaining, focus advances.
	m.focus = focusGate
	frames = append(frames, ansiStrip(m.View()))

	// Submit entry "b".
	m.inputQueue[0].draft = "answer-b"
	m.loadActiveTextarea()
	m, _ = m.Update(key("enter"))

	// Frame 3: empty placeholder, focus=focusSteps.
	frames = append(frames, ansiStrip(m.View()))

	out := strings.Join(frames, "\n--- frame ---\n\n")
	if err := os.WriteFile(artifactPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
}

func monitorLayoutFindings(n int) []sentinel.Finding {
	findings := make([]sentinel.Finding, n)
	for i := range findings {
		findings[i] = sentinel.Finding{
			StepID:  "a",
			Monitor: fmt.Sprintf("security-monitor-%d", i+1),
			Severity: []sentinel.Severity{
				sentinel.SeverityCritical,
				sentinel.SeverityHigh,
				sentinel.SeverityMedium,
				sentinel.SeverityLow,
			}[i%4],
			Action: sentinel.ActionBlocked,
			Detail: "a deliberately long security finding detail that must remain inside the terminal width",
		}
	}
	return findings
}

func TestMonitorViewFitsWindow(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		height   int
		findings int
	}{
		{name: "80x24 empty security", width: 80, height: 24},
		{name: "80x24 populated security", width: 80, height: 24, findings: 8},
		{name: "narrow fallback", width: 60, height: 24, findings: 8},
		{name: "short resize", width: 70, height: 20, findings: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m.secFindings = monitorLayoutFindings(tt.findings)
			m, _ = m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})

			view := m.View()
			if width := lipgloss.Width(view); width > tt.width {
				t.Fatalf("width %d exceeds terminal width %d:\n%s", width, tt.width, ansiStrip(view))
			}
			if height := lipgloss.Height(view); height > tt.height {
				t.Fatalf("height %d exceeds terminal height %d:\n%s", height, tt.height, ansiStrip(view))
			}

			plain := ansiStrip(view)
			for _, want := range []string{"Agent input", "running"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("view missing %q:\n%s", want, plain)
				}
			}
			if tt.findings == 0 {
				if strings.Contains(plain, "Security findings") {
					t.Fatalf("empty security section rendered:\n%s", plain)
				}
			} else {
				for _, want := range []string{"Security findings (8)", "… 5 more findings"} {
					if !strings.Contains(plain, want) {
						t.Fatalf("populated security summary missing %q:\n%s", want, plain)
					}
				}
			}
		})
	}
}

// TestMonitorResizeRefits asserts subsequent WindowSizeMsg values re-fit all
// regions without losing the security summary, gate, or footer.
func TestMonitorResizeRefits(t *testing.T) {
	m := newMonitorWithSteps(t) // 80x24
	m.secFindings = monitorLayoutFindings(8)
	m.resize()

	for _, size := range []struct{ w, h int }{{100, 30}, {70, 20}, {120, 40}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		view := m.View()
		if width := lipgloss.Width(view); width > size.w {
			t.Fatalf("at %dx%d, width %d exceeds terminal width:\n%s",
				size.w, size.h, width, ansiStrip(view))
		}
		if height := lipgloss.Height(view); height > size.h {
			t.Fatalf("at %dx%d, height %d exceeds terminal height:\n%s",
				size.w, size.h, height, ansiStrip(view))
		}
		plain := ansiStrip(view)
		for _, want := range []string{"Agent input", "running", "Security findings"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("at %dx%d, view missing %q:\n%s", size.w, size.h, want, plain)
			}
		}
	}
}

// TestMonitorIntegrationConflictGate verifies that an IntegrationConflictRequest
// surfaces an integration gate that names the conflicted paths and whose r/a
// actions emit resolveIntegrationResponseMsg (resolve / abort).
func TestMonitorIntegrationConflictGate(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.IntegrationConflictRequest{
		RunID:  "run-1",
		StepID: "a",
		Paths:  []string{"shared.go"},
	}})
	if len(m.inputQueue) == 0 {
		t.Fatal("integration conflict event not added to input queue")
	}
	strip := m.gateStrip()
	if !strings.Contains(strip, "(integration)") {
		t.Fatalf("integration gate strip not shown:\n%s", strip)
	}
	if !strings.Contains(strip, "shared.go") {
		t.Fatalf("conflicted path not rendered:\n%s", strip)
	}
	if !strings.Contains(strip, "[r] resolve") || !strings.Contains(strip, "[a] abort") {
		t.Fatalf("integration actions not rendered:\n%s", strip)
	}

	// Resolve routes to ResolveIntegrationResponseMsg{abort:false}.
	m.focus = focusGate
	_, cmd := m.Update(key("r"))
	if cmd == nil {
		t.Fatal("r produced no command")
	}
	rr, ok := cmd().(ResolveIntegrationResponseMsg)
	if !ok {
		t.Fatalf("expected resolveIntegrationResponseMsg, got %T", cmd())
	}
	if rr.Abort || rr.StepID != "a" {
		t.Fatalf("got abort=%v stepID=%q; want resolve/a", rr.Abort, rr.StepID)
	}

	// Abort routes to ResolveIntegrationResponseMsg{abort:true}.
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(EngineEventMsg{Event: engine.IntegrationConflictRequest{RunID: "run-1", StepID: "a", Paths: []string{"x"}}})
	m2.focus = focusGate
	_, cmd = m2.Update(key("a"))
	if cmd == nil {
		t.Fatal("a produced no command")
	}
	if rr, ok := cmd().(ResolveIntegrationResponseMsg); !ok || !rr.Abort {
		t.Fatalf("expected abort resolveIntegrationResponseMsg, got %+v (%T)", cmd(), cmd())
	}
}

// TestMonitorFinalMergeGate verifies that a FinalMergeRequest surfaces a
// final-merge gate offering merge/discard, whose y/d actions emit
// finalMergeResponseMsg (approve / discard).
func TestMonitorFinalMergeGate(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.FinalMergeRequest{
		RunID:     "run-1",
		RunBranch: "jig/wf/run-1",
		Base:      "main",
	}})
	if len(m.inputQueue) == 0 {
		t.Fatal("final merge event not added to input queue")
	}
	strip := m.gateStrip()
	if !strings.Contains(strip, "(final merge)") {
		t.Fatalf("final-merge gate strip not shown:\n%s", strip)
	}
	if !strings.Contains(strip, "main") {
		t.Fatalf("base branch not rendered:\n%s", strip)
	}
	if !strings.Contains(strip, "[y] merge") || !strings.Contains(strip, "[d] discard") {
		t.Fatalf("final-merge actions not rendered:\n%s", strip)
	}

	// Approve routes to FinalMergeResponseMsg{approve:true}.
	m.focus = focusGate
	_, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	fr, ok := cmd().(FinalMergeResponseMsg)
	if !ok {
		t.Fatalf("expected finalMergeResponseMsg, got %T", cmd())
	}
	if !fr.Approve || fr.RunID != "run-1" {
		t.Fatalf("got approve=%v runID=%q; want approve/run-1", fr.Approve, fr.RunID)
	}

	// Discard routes to FinalMergeResponseMsg{approve:false}.
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(EngineEventMsg{Event: engine.FinalMergeRequest{RunID: "run-1", RunBranch: "jig/wf/run-1", Base: "main"}})
	m2.focus = focusGate
	_, cmd = m2.Update(key("d"))
	if cmd == nil {
		t.Fatal("d produced no command")
	}
	if fr, ok := cmd().(FinalMergeResponseMsg); !ok || fr.Approve {
		t.Fatalf("expected discard finalMergeResponseMsg, got %+v (%T)", cmd(), cmd())
	}
}

// buildResetMonitor creates a Model with a fan-out workflow snapshot for
// reset TUI tests. Steps: a (succeeded), b (succeeded, depends a), gate
// (awaitingReview, depends b), d (succeeded, independent). Run is quiescent.
func buildResetMonitor(t *testing.T) Model {
	t.Helper()
	m := New("run-reset")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(EngineEventMsg{Event: engine.RunStarted{
		RunID:    "run-reset",
		Workflow: "fanout",
		Steps:    []string{"a", "d", "b", "gate"},
	}})
	for _, ev := range []engine.Event{
		engine.StepStatus{RunID: "run-reset", StepID: "a", From: step.StatusPending, To: step.StatusSucceeded},
		engine.StepStatus{RunID: "run-reset", StepID: "d", From: step.StatusPending, To: step.StatusSucceeded},
		engine.StepStatus{RunID: "run-reset", StepID: "b", From: step.StatusPending, To: step.StatusSucceeded},
		engine.StepStatus{RunID: "run-reset", StepID: "gate", From: step.StatusPending, To: step.StatusAwaitingReview},
	} {
		m, _ = m.Update(EngineEventMsg{Event: ev})
	}
	return m
}

// TestResetConfirmation verifies that pressing r on a mid-graph step opens the
// reset confirmation gate, y confirms (emits resetStepMsg), and n cancels.
func TestResetConfirmation(t *testing.T) {
	m := buildResetMonitor(t)

	// Cursor starts at step 0 (a). Navigate to step 0 explicitly.
	// Inject showResetConfirmMsg directly (as the root would after resolving closure).
	closure := []string{"a", "b", "gate"}
	m, _ = m.Update(ShowResetConfirmMsg{RunID: "run-reset", StepID: "a", Closure: closure})

	// Confirmation entry must be in the queue.
	if len(m.inputQueue) == 0 {
		t.Fatal("expected reset confirmation in inputQueue, queue is empty")
	}
	entry := m.inputQueue[len(m.inputQueue)-1]
	if entry.kind != inputKindResetConfirm {
		t.Fatalf("queue entry kind = %v; want inputKindResetConfirm", entry.kind)
	}
	if entry.resetConfirm.stepID != "a" {
		t.Errorf("resetConfirm.stepID = %q; want %q", entry.resetConfirm.stepID, "a")
	}
	if len(entry.resetConfirm.closure) != 3 {
		t.Errorf("resetConfirm.closure len = %d; want 3", len(entry.resetConfirm.closure))
	}

	// Tab twice to reach gate focus (Steps → Transcript → Gate).
	m, _ = m.Update(key("tab"))
	m, _ = m.Update(key("tab"))
	if m.focus != focusGate {
		t.Fatalf("after 2×tab: focus = %v; want focusGate", m.focus)
	}

	// y confirms → emits resetStepMsg.
	m2, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("expected a Cmd after y; got nil")
	}
	result := cmd()
	if rsm, ok := result.(ResetStepMsg); !ok {
		t.Fatalf("cmd() returned %T; want resetStepMsg", result)
	} else if rsm.StepID != "a" {
		t.Errorf("resetStepMsg.StepID = %q; want %q", rsm.StepID, "a")
	}
	_ = m2

	// Press n instead: clears the entry, emits nothing.
	m3, cmd3 := m.Update(key("n"))
	if cmd3 != nil && cmd3() != nil {
		t.Errorf("after n: expected nil cmd, got %T", cmd3())
	}
	// Confirmation entry should be gone.
	for _, e := range m3.inputQueue {
		if e.kind == inputKindResetConfirm {
			t.Error("after n: reset confirmation still in queue")
		}
	}
}

// TestResetLinearTipTUI verifies that pressing r on a linear-tip step emits
// requestResetMsg immediately (no confirmation), and r on a settled run or
// non-terminal step is a no-op.
func TestResetLinearTipTUI(t *testing.T) {
	m := buildResetMonitor(t)

	// Navigate to step "a" (index 0 in the monitor step list).
	// Press r: since a is Succeeded, it emits requestResetMsg.
	m2, cmd := m.Update(key("r"))
	if cmd == nil {
		t.Fatal("expected a Cmd after r on a terminal step; got nil")
	}
	result := cmd()
	if rrm, ok := result.(RequestResetMsg); !ok {
		t.Fatalf("cmd() returned %T; want requestResetMsg", result)
	} else {
		if rrm.StepID != "a" {
			t.Errorf("requestResetMsg.StepID = %q; want %q", rrm.StepID, "a")
		}
	}
	_ = m2

	// Press r on a pending step → no cmd (gate is in AwaitingReview, cursor on a).
	// Simulate pressing r on a non-terminal step: navigate to gate (index 3).
	for range 3 {
		m, _ = m.Update(key("j"))
	}
	if m.steps[m.cursor].status != step.StatusAwaitingReview {
		t.Logf("cursor status = %q (expected AwaitingReview for gate)", m.steps[m.cursor].status)
	}
	// gate is AwaitingReview — this IS a resettable status, so r is eligible.
	// Navigate to a pending/running step that is NOT resettable.
	// Create a separate model with a running step.
	m2Running := New("run-running")
	m2Running, _ = m2Running.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2Running, _ = m2Running.Update(EngineEventMsg{Event: engine.RunStarted{
		RunID: "run-running", Workflow: "w", Steps: []string{"x"},
	}})
	m2Running, _ = m2Running.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-running", StepID: "x", From: step.StatusPending, To: step.StatusRunning,
	}})
	// x is Running → r key should emit stopStepMsg (not requestResetMsg),
	// since StopStep binding uses "s" and ResetStep uses "r" — r on running
	// is a no-op for reset (only terminal/stopped trigger reset).
	_, cmdRunning := m2Running.Update(key("r"))
	if cmdRunning != nil {
		if result := cmdRunning(); result != nil {
			if _, isReset := result.(RequestResetMsg); isReset {
				t.Error("r on a running step should not emit requestResetMsg")
			}
		}
	}

	// Settled run: done=true → r must produce no cmd.
	mDone := buildResetMonitor(t)
	mDone, _ = mDone.Update(EngineEventMsg{Event: engine.RunFinished{RunID: "run-reset", Failed: false}})
	_, cmdDone := mDone.Update(key("r"))
	if cmdDone != nil {
		if result := cmdDone(); result != nil {
			t.Errorf("r on settled run: expected nil result, got %T", result)
		}
	}
}

// TestStopKey verifies that s on a running step emits stopStepMsg, and s on a
// non-running step is a no-op.
func TestStopKey(t *testing.T) {
	m := New("run-stop")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(EngineEventMsg{Event: engine.RunStarted{
		RunID: "run-stop", Workflow: "w", Steps: []string{"x", "y"},
	}})
	// x transitions to Running, y stays Pending.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-stop", StepID: "x", From: step.StatusPending, To: step.StatusRunning,
	}})

	// Cursor is on x (Running). s → stopStepMsg.
	_, cmd := m.Update(key("s"))
	if cmd == nil {
		t.Fatal("expected Cmd after s on running step; got nil")
	}
	result := cmd()
	if ssm, ok := result.(StopStepMsg); !ok {
		t.Fatalf("cmd() returned %T; want stopStepMsg", result)
	} else if ssm.StepID != "x" {
		t.Errorf("stopStepMsg.StepID = %q; want %q", ssm.StepID, "x")
	}

	// Navigate to y (Pending). s → no stopStepMsg.
	m, _ = m.Update(key("j"))
	if m.steps[m.cursor].id != "y" {
		t.Fatalf("expected cursor on y, got %q", m.steps[m.cursor].id)
	}
	_, cmd2 := m.Update(key("s"))
	if cmd2 != nil {
		if result := cmd2(); result != nil {
			if _, isStop := result.(StopStepMsg); isStop {
				t.Error("s on a non-running step should not emit stopStepMsg")
			}
		}
	}
}

// TestSecurityPane verifies that SecurityFinding ctrl events populate the
// Security pane and that render output is styled by severity (verbatim path,
// not glamour). It uses the findingsPath mechanism: findings are written to
// disk and the model reads them from there.
func TestSecurityPane(t *testing.T) {
	dir := t.TempDir()
	fPath := datastore.FindingsPath(dir)

	// Write two findings to disk.
	fw, err := sentinel.NewWriter(fPath)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	findings := []sentinel.Finding{
		{
			StepID: "impl", Tier: "guard", Monitor: "secret-in-write",
			Severity: sentinel.SeverityHigh, Action: sentinel.ActionBlocked,
			Detail: "tool input contains aws-key pattern: [aws-key:…MPLE]",
		},
		{
			StepID: "impl", Tier: "guard", Monitor: "denied-shell",
			Severity: sentinel.SeverityCritical, Action: sentinel.ActionEscalated,
			Detail: "Bash command matches denied shell pattern",
		},
	}
	for _, f := range findings {
		if err := fw.Append(f); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Build a minimal Model with runDir set.
	m := New("run1")
	m.RunDir = dir
	m.ready = true
	m.width = 80
	m.height = 24

	// Feed a SecurityFinding event for each finding.
	for _, f := range findings {
		m, _ = m.handleEngineEvent(engine.SecurityFinding{
			RunID:       "run1",
			StepID:      f.StepID,
			Tier:        string(f.Tier),
			Monitor:     f.Monitor,
			Severity:    string(f.Severity),
			Action:      string(f.Action),
			Fingerprint: f.Fingerprint,
		})
	}

	if len(m.secFindings) != 2 {
		t.Fatalf("secFindings len = %d, want 2", len(m.secFindings))
	}

	// Render the security view and verify it contains expected text.
	secView := m.securityView(securityMaxHeight)

	t.Run("high severity row rendered", func(t *testing.T) {
		if !strings.Contains(secView, "HIGH") {
			t.Errorf("security view missing HIGH row:\n%s", secView)
		}
		if !strings.Contains(secView, "secret-in-write") {
			t.Errorf("security view missing monitor name:\n%s", secView)
		}
		// Redacted preview must appear verbatim (not mangled by glamour).
		if !strings.Contains(secView, "[aws-key:…MPLE]") {
			t.Errorf("security view mangled redacted preview:\n%s", secView)
		}
	})

	t.Run("critical severity row rendered", func(t *testing.T) {
		if !strings.Contains(secView, "CRITICAL") {
			t.Errorf("security view missing CRITICAL row:\n%s", secView)
		}
		if !strings.Contains(secView, "denied-shell") {
			t.Errorf("security view missing monitor name:\n%s", secView)
		}
	})

	t.Run("header rendered", func(t *testing.T) {
		if !strings.Contains(secView, "Security findings") {
			t.Errorf("security view missing header:\n%s", secView)
		}
	})

	t.Run("empty when no findings", func(t *testing.T) {
		m2 := New("run2")
		if got := m2.securityView(securityMaxHeight); got != "" {
			t.Errorf("securityView with no findings returned non-empty string: %q", got)
		}
	})
}

// TestMonitorHelpSections checks the monitor contributes focus-appropriate
// sections: the Steps section (default focus) plus the shared Focus and Global
// sections. Root ownership of the overlay itself is covered in root_test.go.
func TestMonitorHelpSections(t *testing.T) {
	m := newMonitorWithSteps(t)
	titles := map[string]bool{}
	for _, sec := range m.HelpSections() {
		titles[sec.Title] = true
	}
	for _, want := range []string{"Steps", "Focus", "Global"} {
		if !titles[want] {
			t.Errorf("monitor help missing section %q; got %v", want, titles)
		}
	}
}

// drainFrames flushes any scheduled frame(s) so the monitor settles to an idle
// state (no frame in flight, no pending repaint) — the fixture starting point
// for asserting on the next event.
func drainFrames(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 8 && m.ticking; i++ {
		m, _ = m.Update(TickMsg(time.Now()))
	}
	if m.ticking {
		t.Fatal("frame loop did not settle after draining")
	}
	return m
}

// TestMonitorLiveClockTicks verifies the frame loop's clock role: a running step
// keeps the loop re-arming so the elapsed column advances, and the loop falls
// silent once the step finishes.
func TestMonitorLiveClockTicks(t *testing.T) {
	m := drainFrames(t, newMonitorWithSteps(t))

	// A step going Running schedules a frame.
	m, cmd := m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusRunning,
	}})
	if !m.ticking {
		t.Fatal("frame not scheduled after step started running")
	}
	if cmd == nil {
		t.Fatal("no frame command returned when step started running")
	}

	// A frame while the step runs re-arms the loop (non-nil command).
	m, cmd = m.Update(TickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("frame loop did not re-arm while a step was running")
	}
	if !m.ticking {
		t.Fatal("ticking flag cleared while a step was still running")
	}

	// Once the step finishes, the next frame flushes any pending repaint, then
	// falls silent (no re-arm) and clears the guard so a future event restarts it.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusSucceeded,
	}})
	m, cmd = m.Update(TickMsg(time.Now()))
	if cmd != nil {
		t.Fatal("frame loop re-armed after all steps finished")
	}
	if m.ticking {
		t.Fatal("ticking flag still set after the loop should have stopped")
	}
}

// TestMonitorFrameLeadingEdge verifies the leading-edge flush: a lone event that
// arrives while the frame loop is idle repaints immediately (no deferred latency)
// rather than waiting for the next frame.
func TestMonitorFrameLeadingEdge(t *testing.T) {
	m := drainFrames(t, newMonitorWithSteps(t)) // idle: no frame in flight

	// A single (non-running) status event flushes on the spot and, with nothing
	// running and nothing left dirty, does not arm a frame at all.
	m, cmd := m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusSkipped,
	}})
	if m.dirtyList || m.dirtyChat {
		t.Fatal("idle event was left pending instead of flushed on the leading edge")
	}
	if cmd != nil || m.ticking {
		t.Fatal("a lone idle event spun up a frame loop with nothing to coalesce")
	}
}

// TestMonitorFrameCoalescesBurst verifies the coalescing contract: while the loop
// is running (a streaming window), a burst of events does not stack extra frames
// and defers the repaint via the dirty flags, which one frame then flushes.
func TestMonitorFrameCoalescesBurst(t *testing.T) {
	m := drainFrames(t, newMonitorWithSteps(t)) // chatStep is the first step, "a"

	// Put the visible step into Running so the frame loop stays armed — this is the
	// streaming window during which deltas must coalesce.
	m, cmd := m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusRunning,
	}})
	if !m.ticking || cmd == nil {
		t.Fatal("running step did not arm the frame loop")
	}

	// The whole burst reuses the in-flight frame: no delta schedules another frame,
	// and the transcript repaint stays pending until a frame services it.
	for i := 0; i < 20; i++ {
		var c tea.Cmd
		m, c = m.Update(EngineEventMsg{Event: engine.StepOutput{
			RunID: "run-1", StepID: "a", Delta: "chunk",
		}})
		if c != nil {
			t.Fatalf("burst delta %d stacked a duplicate frame", i)
		}
	}
	if !m.dirtyChat {
		t.Fatal("repaint was not still pending mid-burst (delta rendered inline?)")
	}

	// A single frame flushes the coalesced repaint for the whole burst.
	m, _ = m.Update(TickMsg(time.Now()))
	if m.dirtyChat {
		t.Fatal("frame did not flush the pending transcript repaint")
	}
}

// TestMonitorFrameStepGating verifies that an event for a non-visible step never
// dirties the (glamour-rendered) Transcript panel — the parallel-steps fix — while
// still refreshing the cheap Steps list.
func TestMonitorFrameStepGating(t *testing.T) {
	m := drainFrames(t, newMonitorWithSteps(t)) // chatStep is "a"

	// Arm the loop (visible step running) so events coalesce into the dirty flags
	// rather than flushing on the leading edge — that is what we want to inspect.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusRunning,
	}})

	// A parallel step "b" streaming must not schedule a transcript repaint.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{
		RunID: "run-1", StepID: "b", Delta: "chunk",
	}})
	if m.dirtyChat {
		t.Fatal("event for a hidden step dirtied the transcript panel")
	}
	if !m.dirtyList {
		t.Fatal("event did not refresh the steps list")
	}

	// An event for the visible step "a" does dirty the transcript panel.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: 1,
	}})
	if !m.dirtyChat {
		t.Fatal("event for the visible step did not dirty the transcript panel")
	}
}

// TestToggleHelp_OpenClose verifies that ctrl+\ opens the help modal, a second
// ctrl+\ closes it, and esc also closes it while open.
func TestToggleHelp_OpenClose(t *testing.T) {
	m := newMonitorWithSteps(t)

	if m.helpOpen {
		t.Fatal("help modal should start closed")
	}

	// First ctrl+\: opens the modal (run == nil → NewUnavailable path, no SDK connect).
	ctrlBackslash := tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}
	m, _ = m.Update(ctrlBackslash)
	if !m.helpOpen {
		t.Fatal("first ctrl+\\ did not open the help modal")
	}
	if !m.helpReady {
		t.Fatal("helpReady should be true after first open (unavailable path)")
	}

	// Second ctrl+\: closes.
	m, _ = m.Update(ctrlBackslash)
	if m.helpOpen {
		t.Fatal("second ctrl+\\ did not close the help modal")
	}

	// Third ctrl+\ to reopen, then esc to close.
	m, _ = m.Update(ctrlBackslash)
	if !m.helpOpen {
		t.Fatal("third ctrl+\\ did not reopen the help modal")
	}
	m, _ = m.Update(key("esc"))
	if m.helpOpen {
		t.Fatal("esc did not close the help modal")
	}
}

// TestHelpDispatch_DispatchedMsg verifies that a DispatchedMsg carrying a
// RecoverAction is converted to a RecoverResponseMsg by dispatchHelpAction and
// the returned cmd produces that message when called.
func TestHelpDispatch_DispatchedMsg(t *testing.T) {
	m := newMonitorWithSteps(t)

	dispatched := helpchat.DispatchedMsg{
		Inner: helpchat.RecoverAction{StepID: "a", Action: "retry", Text: "try again"},
	}

	_, cmd := m.Update(dispatched)
	if cmd == nil {
		t.Fatal("DispatchedMsg returned nil cmd")
	}

	// The batch contains the inner action cmd and the re-armed drain cmd. Execute
	// them until we get a RecoverResponseMsg (or exhaust without finding one).
	msgs := runBatch(cmd)
	var found *RecoverResponseMsg
	for _, msg := range msgs {
		if r, ok := msg.(RecoverResponseMsg); ok {
			found = &r
			break
		}
	}
	if found == nil {
		t.Fatalf("no RecoverResponseMsg in batch; got %T values", msgs)
	}
	if found.StepID != "a" {
		t.Errorf("StepID = %q, want %q", found.StepID, "a")
	}
	if found.Action != "retry" {
		t.Errorf("Action = %q, want %q", found.Action, "retry")
	}
	if found.RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", found.RunID, "run-1")
	}
}

// runBatch executes a tea.Cmd and collects the messages it produces within a
// short timeout. Handles tea.BatchMsg by executing each sub-command in a
// goroutine, collecting only those that return before the deadline (blocking
// channel-drain cmds are skipped automatically).
func runBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	type result struct{ msg tea.Msg }
	run := func(c tea.Cmd) <-chan tea.Msg {
		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		return ch
	}
	collect := func(cmds []tea.Cmd) []tea.Msg {
		chans := make([]<-chan tea.Msg, len(cmds))
		for i, c := range cmds {
			chans[i] = run(c)
		}
		deadline := time.After(50 * time.Millisecond)
		var out []tea.Msg
		for _, ch := range chans {
			select {
			case m := <-ch:
				if m != nil {
					out = append(out, m)
				}
			case <-deadline:
			}
		}
		return out
	}

	topCh := run(cmd)
	select {
	case top := <-topCh:
		if top == nil {
			return nil
		}
		if batch, ok := top.(tea.BatchMsg); ok {
			return collect([]tea.Cmd(batch))
		}
		return []tea.Msg{top}
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// TestMonitorLiveClockNoDuplicateLoops verifies the ticking guard: a second
// step going Running while a frame is already scheduled does not stack a loop.
func TestMonitorLiveClockNoDuplicateLoops(t *testing.T) {
	m := drainFrames(t, newMonitorWithSteps(t))

	m, cmd := m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusRunning,
	}})
	if cmd == nil {
		t.Fatal("first Running event did not schedule a frame")
	}

	// A second concurrent Running step must not spawn another frame command.
	m, cmd = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "b", To: step.StatusRunning,
	}})
	if cmd != nil {
		t.Fatal("second Running event stacked a duplicate frame loop")
	}
	if !m.ticking {
		t.Fatal("ticking flag unexpectedly cleared")
	}
}
