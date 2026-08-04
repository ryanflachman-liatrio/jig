package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
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

// enterChatStep drives the Steps cursor to the given step id (which eagerly
// reloads that step's transcript) and focuses the Transcript panel. The two-panel
// monitor always shows the cursor's step, so this is a cursor move plus a focus
// switch rather than a mode toggle.
func enterChatStep(t *testing.T, m monitorModel, id string) monitorModel {
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

	body := m.chatBody()
	for _, want := range []string{IconThinking + " reasoning", IconToolCall + " Read", IconToolResult + " result", "Reading the file"} {
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

	collapsed := m.chatBody()
	if strings.Contains(collapsed, "MARKER") {
		t.Fatalf("collapsed body leaked content past 80 chars:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "[96 chars]") {
		t.Fatalf("collapsed body missing char-count hint:\n%s", collapsed)
	}

	m, _ = m.Update(key("o")) // expand all
	expanded := m.chatBody()
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

	if strings.Contains(m.chatBody(), "END") {
		t.Fatalf("block should start collapsed")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // toggle block under cursor
	if !strings.Contains(m.chatBody(), "END") {
		t.Fatalf("enter did not expand the cursored block:\n%s", m.chatBody())
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // toggle back
	if strings.Contains(m.chatBody(), "END") {
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

	if !strings.Contains(m.chatBody(), "iteration 2") {
		t.Fatalf("missing iteration separator:\n%s", m.chatBody())
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
	m.runDir = runDir
	m = enterChatStep(t, m, "a")

	if !strings.Contains(m.chatBody(), "build output here") {
		t.Fatalf("command output not rendered in chat:\n%s", m.chatBody())
	}
}

// TestMonitorChatReviewFallback checks drilling into a review step shows its
// retained diff and choices rather than a dead end (Phase 6).
func TestMonitorChatReviewFallback(t *testing.T) {
	m := newMonitorWithSteps(t) // no runDir: review steps have no transcript

	// A review arrives (auto-focuses the gate) then is resolved, retaining the request.
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Diff:    "@@ -1 +1 @@\n-old line\n+new line",
		Choices: []string{"approve", "reject"},
	}})
	m, _ = m.Update(key("1")) // verdict clears pendingReview

	m = enterChatStep(t, m, "a")
	body := m.chatBody()
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

func key(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
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
	if !strings.Contains(m.body(), CursorBar) {
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
	if _, ok := cmd().(showRunsMsg); !ok {
		t.Fatalf("esc with Steps focused did not emit showRunsMsg, got %T", cmd())
	}
}

// TestMonitorChatScrolls confirms j/k with the Transcript focused are not
// intercepted as cursor moves (they fall through to the viewport).
func TestMonitorChatScrolls(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("j")) // cursor → 1
	m, _ = m.Update(key("enter"))
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript, got %v", m.focus)
	}
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor != before {
		t.Fatalf("j with Transcript focused moved the list cursor from %d to %d", before, m.cursor)
	}
}

// TestMonitorReviewAutoFocusesGate verifies a review gate arriving auto-focuses
// the Gate region, renders the verdict picker in the gate strip, and that digit
// keys select a verdict.
func TestMonitorReviewAutoFocusesGate(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(key("enter")) // focus the Transcript panel for step "a"
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript, got %v", m.focus)
	}

	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
	if m.focus != focusGate {
		t.Fatalf("review gate did not auto-focus the Gate, got %v", m.focus)
	}
	if !strings.Contains(m.gateStrip(), "Review") {
		t.Fatalf("review gate strip not shown:\n%s", m.gateStrip())
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

// TestMonitorGateConsumesKeys confirms that with the Gate focused, a review
// verdict picker consumes j/k (they are not a review action) so the Steps cursor
// does not move — but focus can still be switched away (see
// TestMonitorGateNonBlocking).
func TestMonitorGateConsumesKeys(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve", "reject"},
	}})
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

// TestMonitorAgentQuestionShowsPanel verifies that an AgentQuestion event shows
// the question overlay in modeList and updates the footer status.
func TestMonitorAgentQuestionShowsPanel(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:     "run-1",
		StepID:    "a",
		ToolUseID: "tu1",
		Questions: []engine.AgentQuestionItem{
			{
				Header:   "Format",
				Question: "Which format should we use?",
				Options: []engine.AgentQuestionOption{
					{Label: "JSON", Description: "structured output"},
					{Label: "Text", Description: "plain output"},
				},
			},
		},
	}})

	if m.pendingQuestion == nil {
		t.Fatal("pendingQuestion not set after AgentQuestion event")
	}
	if m.focus != focusGate {
		t.Fatalf("expected focusGate after AgentQuestion, got %v", m.focus)
	}

	body := m.gateStrip()
	for _, want := range []string{"Agent question", "Which format should we use?", "[Format]", "[1] JSON", "[2] Text", "structured output"} {
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

	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:     "run-1",
		StepID:    "a",
		ToolUseID: "tu1",
		Questions: []engine.AgentQuestionItem{
			{
				Question: "Pick one",
				Options: []engine.AgentQuestionOption{
					{Label: "Alpha"},
					{Label: "Beta"},
				},
			},
		},
	}})

	m, cmd := m.Update(key("2"))
	if cmd == nil {
		t.Fatal("digit key produced no command")
	}
	msg, ok := cmd().(agentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd())
	}
	if msg.toolUseID != "tu1" {
		t.Fatalf("expected toolUseID tu1, got %q", msg.toolUseID)
	}
	if !strings.Contains(msg.answer, "Beta") {
		t.Fatalf("expected answer to contain 'Beta', got %q", msg.answer)
	}
	if m.pendingQuestion != nil {
		t.Fatal("pendingQuestion should be cleared after answer is submitted")
	}
}

// TestMonitorAgentQuestionMultiSelect verifies that multiSelect questions require
// toggling and enter to confirm, and accumulate multiple selections.
func TestMonitorAgentQuestionMultiSelect(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:     "run-1",
		StepID:    "a",
		ToolUseID: "tu2",
		Questions: []engine.AgentQuestionItem{
			{
				Question:    "Select features",
				MultiSelect: true,
				Options: []engine.AgentQuestionOption{
					{Label: "Cache"},
					{Label: "Retry"},
					{Label: "Logging"},
				},
			},
		},
	}})

	// Toggle options 1 and 3.
	m, _ = m.Update(key("1"))
	m, _ = m.Update(key("3"))

	body := m.gateStrip()
	if !strings.Contains(body, "[x]") {
		t.Fatalf("toggled options not shown:\n%s", body)
	}

	// A digit toggle in multiSelect mode should not emit a response.
	_, cmd := m.Update(key("2"))
	if cmd != nil {
		if result := cmd(); result != nil {
			t.Fatalf("digit toggle in multiSelect emitted unexpected message: %T", result)
		}
	}

	// Toggle option 1 back off; confirm with enter.
	m, _ = m.Update(key("1"))
	m, cmd = m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	resp, ok := cmd().(agentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd())
	}
	if !strings.Contains(resp.answer, "Logging") {
		t.Fatalf("expected Logging in answer, got %q", resp.answer)
	}
	if strings.Contains(resp.answer, "Cache") {
		t.Fatalf("Cache was toggled off but appears in answer: %q", resp.answer)
	}
}

// TestMonitorAgentQuestionConsumesKeys confirms j/k are consumed by the focused
// question gate (they are not a selection key), so the Steps cursor does not move
// while the Gate is focused.
func TestMonitorAgentQuestionConsumesKeys(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:     "run-1",
		StepID:    "a",
		ToolUseID: "tu1",
		Questions: []engine.AgentQuestionItem{
			{Question: "Q?", Options: []engine.AgentQuestionOption{{Label: "A"}}},
		},
	}})

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
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
		RunID:   "run-1",
		StepID:  "a",
		From:    step.StatusRunning,
		To:      step.StatusFailed,
		Err:     "agent reached the maximum turn limit",
		Subtype: "error_max_turns",
	}})
	// Step "b" fails with error_max_budget_usd.
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
		RunID:   "run-1",
		StepID:  "b",
		From:    step.StatusRunning,
		To:      step.StatusFailed,
		Err:     "agent exceeded the maximum USD budget",
		Subtype: "error_max_budget_usd",
	}})
	// Step "c" fails with a plain API error (no subtype annotation expected).
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
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

// TestMonitorAgentQuestionClearsOnResume verifies that pendingQuestion is cleared
// when the step transitions away from StatusNeedsInput.
func TestMonitorAgentQuestionClearsOnResume(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:     "run-1",
		StepID:    "a",
		ToolUseID: "tu1",
		Questions: []engine.AgentQuestionItem{
			{Question: "Q?", Options: []engine.AgentQuestionOption{{Label: "A"}}},
		},
	}})
	if m.pendingQuestion == nil {
		t.Fatal("pendingQuestion should be set")
	}

	// Simulate the step resuming after the answer is delivered.
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
		RunID:  "run-1",
		StepID: "a",
		From:   step.StatusNeedsInput,
		To:     step.StatusRunning,
	}})
	if m.pendingQuestion != nil {
		t.Fatal("pendingQuestion should be cleared when step leaves StatusNeedsInput")
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
	makeGate := func() monitorModel {
		m := newMonitorWithSteps(t)
		m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
			RunID:     "run-1",
			StepID:    "a",
			ToolUseID: "tu1",
			Questions: []engine.AgentQuestionItem{
				{Question: "Pick one", Options: []engine.AgentQuestionOption{{Label: "Alpha"}, {Label: "Beta"}}},
			},
		}})
		return m
	}

	// 1) Navigation is not frozen: tab moves focus off the gate.
	m := makeGate()
	if m.focus != focusGate {
		t.Fatalf("gate should auto-focus, got %v", m.focus)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus == focusGate {
		t.Fatalf("tab did not move focus away from the gate (still %v)", m.focus)
	}
	// The gate is still pending (moving focus did not resolve it).
	if m.pendingQuestion == nil {
		t.Fatal("moving focus off the gate should not resolve it")
	}
	// With focus on a panel, j/k navigate (Steps) rather than answering.
	m.focus = focusSteps
	before := m.cursor
	m, _ = m.Update(key("j"))
	if m.cursor == before {
		t.Fatal("j did not move the Steps cursor while a gate was pending (navigation frozen)")
	}

	// 2) Returning focus to the gate and answering resolves it with the response.
	m.focus = focusGate
	m, cmd := m.Update(key("2"))
	if cmd == nil {
		t.Fatal("gate digit produced no command")
	}
	resp, ok := cmd().(agentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd())
	}
	if !strings.Contains(resp.answer, "Beta") {
		t.Fatalf("expected answer Beta, got %q", resp.answer)
	}

	// 3) Cancellation (esc) delivers the cancellation response so the reporter
	// goroutine unblocks rather than hanging.
	m = makeGate()
	_, cmd = m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	var gotCancel bool
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if r, ok := c().(agentQuestionResponseMsg); ok && r.answer == "cancelled" {
				gotCancel = true
			}
		}
	} else if r, ok := msg.(agentQuestionResponseMsg); ok && r.answer == "cancelled" {
		gotCancel = true
	}
	if !gotCancel {
		t.Fatalf("esc cancellation did not emit agentQuestionResponseMsg{answer:\"cancelled\"}: %T", msg)
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
	m.runDir = runDirA
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

// TestMonitorResizeRefits asserts a second WindowSizeMsg re-fits both panels with
// no line exceeding the new terminal width (no overflow, borders intact).
func TestMonitorResizeRefits(t *testing.T) {
	m := newMonitorWithSteps(t) // 80x24

	for _, size := range []struct{ w, h int }{{100, 30}, {70, 20}, {120, 40}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		view := m.View()
		for i, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > size.w {
				t.Fatalf("at %dx%d, line %d width %d exceeds terminal width:\n%q",
					size.w, size.h, i, w, ansiStrip(line))
			}
		}
	}
}
