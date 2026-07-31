package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/transcript"
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

// enterChatStep drives the monitor into modeChat for the given step id.
func enterChatStep(t *testing.T, m monitorModel, id string) monitorModel {
	t.Helper()
	for m.steps[m.cursor].id != id {
		before := m.cursor
		m, _ = m.Update(key("j"))
		if m.cursor == before {
			t.Fatalf("step %q not found while navigating", id)
		}
	}
	m, _ = m.Update(key("enter"))
	if m.mode != modeChat {
		t.Fatalf("did not enter modeChat for %q", id)
	}
	return m
}

// TestMonitorChatRendersBlocks renders a transcript with every block kind and
// asserts each surfaces with its label in modeChat.
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
	m.runDir = runDir
	m = enterChatStep(t, m, "a")

	body := m.body()
	for _, want := range []string{"🧠 reasoning", "⚙ Read", "↳ result", "Reading the file"} {
		if !strings.Contains(body, want) {
			t.Fatalf("chat body missing %q:\n%s", want, body)
		}
	}
}

// TestMonitorChatCollapseExpand checks a long tool_result collapses to 80 chars
// with a hint and reveals its full content once expanded (via o).
func TestMonitorChatCollapseExpand(t *testing.T) {
	// 90 'a's then a marker past the 80-char collapse boundary.
	content := strings.Repeat("a", 90) + "MARKER"
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t1", Content: content},
		}},
	})

	m := newMonitorWithSteps(t)
	m.runDir = runDir
	m = enterChatStep(t, m, "a")

	collapsed := m.body()
	if strings.Contains(collapsed, "MARKER") {
		t.Fatalf("collapsed body leaked content past 80 chars:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "[96 chars]") {
		t.Fatalf("collapsed body missing char-count hint:\n%s", collapsed)
	}

	m, _ = m.Update(key("o")) // expand all
	expanded := m.body()
	if !strings.Contains(expanded, "MARKER") {
		t.Fatalf("expanded body did not reveal full content:\n%s", expanded)
	}
}

// TestMonitorChatBlockCursorToggle checks tab moves the block cursor and enter
// toggles just the selected block.
func TestMonitorChatBlockCursorToggle(t *testing.T) {
	long := strings.Repeat("z", 100) + "END"
	runDir := writeTranscript(t, "a", []transcript.Entry{
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, ToolUseID: "t1", Content: long},
		}},
	})

	m := newMonitorWithSteps(t)
	m.runDir = runDir
	m = enterChatStep(t, m, "a")

	if strings.Contains(m.body(), "END") {
		t.Fatalf("block should start collapsed")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // toggle block under cursor
	if !strings.Contains(m.body(), "END") {
		t.Fatalf("enter did not expand the cursored block:\n%s", m.body())
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // toggle back
	if strings.Contains(m.body(), "END") {
		t.Fatalf("second enter did not collapse the block")
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
	m.runDir = runDir
	m = enterChatStep(t, m, "a")

	if !strings.Contains(m.body(), "iteration 2") {
		t.Fatalf("missing iteration separator:\n%s", m.body())
	}
}

// TestMonitorChatNoTranscript shows a graceful placeholder when persistence is
// off (no runDir) rather than a dead end.
func TestMonitorChatNoTranscript(t *testing.T) {
	m := newMonitorWithSteps(t) // runDir stays ""
	m = enterChatStep(t, m, "a")
	if !strings.Contains(m.body(), "persistence off") {
		t.Fatalf("expected persistence-off placeholder:\n%s", m.body())
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
	m.runDir = runDir
	m = enterChatStep(t, m, "a")

	if !strings.Contains(m.body(), "build output here") {
		t.Fatalf("command output not rendered in chat:\n%s", m.body())
	}
}

// TestMonitorChatReviewFallback checks drilling into a review step shows its
// retained diff and choices rather than a dead end (Phase 6).
func TestMonitorChatReviewFallback(t *testing.T) {
	m := newMonitorWithSteps(t) // no runDir: review steps have no transcript

	// A review arrives (forces modeList) then is resolved, retaining the request.
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Diff:    "@@ -1 +1 @@\n-old line\n+new line",
		Choices: []string{"approve", "reject"},
	}})
	m, _ = m.Update(key("1")) // verdict clears pendingReview

	m = enterChatStep(t, m, "a")
	body := m.body()
	for _, want := range []string{"new line", "old line", "[1] approve", "[2] reject"} {
		if !strings.Contains(body, want) {
			t.Fatalf("review drill-in missing %q:\n%s", want, body)
		}
	}
}

// newMonitorWithSteps returns a sized monitor primed with a three-step run so
// tests can exercise list navigation without the engine.
func newMonitorWithSteps(t *testing.T) monitorModel {
	t.Helper()
	m := newMonitorModel("run-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(engineEventMsg{event: engine.RunStarted{
		RunID:    "run-1",
		Workflow: "demo",
		Steps:    []string{"a", "b", "c"},
	}})
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// TestMonitorListNavigation checks that j/k move the selection cursor in
// modeList (without scrolling) and clamp at the list bounds.
func TestMonitorListNavigation(t *testing.T) {
	m := newMonitorWithSteps(t)

	if m.mode != modeList {
		t.Fatalf("expected modeList by default, got %v", m.mode)
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

	// Selected row is marked in the list body.
	if !strings.Contains(m.body(), "> ") {
		t.Fatalf("list body missing cursor marker:\n%s", m.body())
	}
}

// TestMonitorEnterAndBack checks enter drills into a step's chat, esc returns
// to the list, and a second esc emits showRunsMsg.
func TestMonitorEnterAndBack(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("j")) // cursor → step "b"

	m, _ = m.Update(key("enter"))
	if m.mode != modeChat {
		t.Fatalf("enter did not enter modeChat, got %v", m.mode)
	}
	if m.chatStep != "b" {
		t.Fatalf("expected chatStep b, got %q", m.chatStep)
	}
	if !strings.Contains(m.body(), "chat") {
		t.Fatalf("chat body missing header:\n%s", m.body())
	}

	m, _ = m.Update(key("esc"))
	if m.mode != modeList {
		t.Fatalf("esc did not return to modeList, got %v", m.mode)
	}
	if m.chatStep != "" {
		t.Fatalf("expected chatStep cleared, got %q", m.chatStep)
	}

	_, cmd := m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("esc in modeList produced no command")
	}
	if _, ok := cmd().(showRunsMsg); !ok {
		t.Fatalf("esc in modeList did not emit showRunsMsg, got %T", cmd())
	}
}

// TestMonitorChatScrolls confirms j/k in modeChat are not intercepted as
// cursor moves (they fall through to the viewport).
func TestMonitorChatScrolls(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("j")) // cursor → 1
	m, _ = m.Update(key("enter"))
	if m.mode != modeChat {
		t.Fatalf("expected modeChat, got %v", m.mode)
	}
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j in modeChat moved the list cursor from %d to %d", before, m.cursor)
	}
}

// TestMonitorReviewForcesList verifies a review gate arriving while in modeChat
// pops back to the list (where the overlay renders) and that digit keys select
// a verdict.
func TestMonitorReviewForcesList(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("enter")) // into chat for step "a"
	if m.mode != modeChat {
		t.Fatalf("expected modeChat, got %v", m.mode)
	}

	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
	if m.mode != modeList {
		t.Fatalf("review gate did not force modeList, got %v", m.mode)
	}
	if !strings.Contains(m.body(), "Review required") {
		t.Fatalf("review overlay not shown:\n%s", m.body())
	}

	_, cmd := m.Update(key("1"))
	if cmd == nil {
		t.Fatal("digit key produced no verdict command")
	}
	vm, ok := cmd().(reviewVerdictMsg)
	if !ok {
		t.Fatalf("expected reviewVerdictMsg, got %T", cmd())
	}
	if vm.verdict != "approve" {
		t.Fatalf("expected verdict approve, got %q", vm.verdict)
	}
}

// TestMonitorReviewFreezesNavigation confirms j/k are swallowed (do not move
// the cursor) while a review overlay is up.
func TestMonitorReviewFreezesNavigation(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j moved cursor during review: %d → %d", before, m.cursor)
	}
}

// TestMonitorMessageCount shows StepMessage liveness events surface as a
// per-step message count in the list.
func TestMonitorMessageCount(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: 3,
	}})
	if got := m.msgCount["a"]; got != 3 {
		t.Fatalf("expected msgCount 3, got %d", got)
	}
	if !strings.Contains(m.body(), "3 msg") {
		t.Fatalf("list body missing message count:\n%s", m.body())
	}
	// Stale (lower) seq must not lower the count.
	m, _ = m.Update(engineEventMsg{event: engine.StepMessage{
		RunID: "run-1", StepID: "a", Seq: 2,
	}})
	if got := m.msgCount["a"]; got != 3 {
		t.Fatalf("stale seq lowered count to %d", got)
	}
}
