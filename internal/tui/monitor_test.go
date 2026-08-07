package tui

import (
	"encoding/json"
	"fmt"
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
// diff in the Transcript panel. Verdict choices live in the gate strip, not here
// (Decision 2 / ADR 0005).
func TestMonitorChatReviewFallback(t *testing.T) {
	m := newMonitorWithSteps(t) // no runDir: review steps have no transcript

	// A review arrives; Decision 6 means no auto-focus.
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
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

	m := newMonitorModel("r1")
	m.runDir = runDir
	m = m.withJournal([]engine.Event{
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

// TestMonitorReviewQueued verifies a review gate arriving is enqueued without
// stealing focus (Decision 6), renders the verdict picker in the gate strip, and
// that digit keys select a verdict once the user tabs to the gate.
func TestMonitorReviewQueued(t *testing.T) {
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
	vm, ok := cmd().(reviewVerdictMsg)
	if !ok {
		t.Fatalf("expected reviewVerdictMsg, got %T", cmd())
	}
	if vm.verdict != "approve" {
		t.Fatalf("expected verdict approve, got %q", vm.verdict)
	}
}

// TestMonitorRecoveryGate verifies that a RecoveryRequest surfaces a recovery
// gate whose r/a actions emit recoverResponseMsg, and that guidance ([g]) is
// hidden when the failed step has no resumable session.
func TestMonitorRecoveryGate(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.RecoveryRequest{
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
	rr, ok := cmd().(recoverResponseMsg)
	if !ok {
		t.Fatalf("expected recoverResponseMsg, got %T", cmd())
	}
	if rr.action != engine.RecoverRetry || rr.stepID != "a" {
		t.Fatalf("got action=%q stepID=%q; want retry/a", rr.action, rr.stepID)
	}

	// Abort routes to RecoverAbort.
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(engineEventMsg{event: engine.RecoveryRequest{RunID: "run-1", StepID: "a", Err: "boom"}})
	m2.focus = focusGate
	_, cmd = m2.Update(key("a"))
	if cmd == nil {
		t.Fatal("a produced no command")
	}
	if rr, ok := cmd().(recoverResponseMsg); !ok || rr.action != engine.RecoverAbort {
		t.Fatalf("expected RecoverAbort, got %+v (%T)", cmd(), cmd())
	}
}

// TestMonitorRecoveryGuidance verifies the [g] guidance path: it opens a compose
// box and submitting emits a RecoverResume with the typed text, but only when the
// failed step is resumable.
func TestMonitorRecoveryGuidance(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.RecoveryRequest{
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
	rr, ok := cmd().(recoverResponseMsg)
	if !ok {
		t.Fatalf("expected recoverResponseMsg, got %T", cmd())
	}
	if rr.action != engine.RecoverResume || rr.stepID != "a" {
		t.Fatalf("got action=%q stepID=%q; want resume/a", rr.action, rr.stepID)
	}
	if rr.text != "hi" {
		t.Fatalf("guidance text = %q, want %q", rr.text, "hi")
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
	for _, want := range []string{"(question)", "Which format should we use?", "[Format]", "[1] JSON", "[2] Text", "structured output"} {
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

	// Decision 6: no auto-focus — manually focus the gate to answer.
	m.focus = focusGate
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
	if len(m.inputQueue) > 0 {
		t.Fatal("question entry should be removed from queue after answer is submitted")
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

	// Decision 6: no auto-focus — manually focus the gate.
	m.focus = focusGate

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

// TestMonitorAgentQuestionClearsOnResume verifies that a queued question entry is
// removed when the step transitions away from StatusNeedsInput.
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
	if len(m.inputQueue) == 0 {
		t.Fatal("question entry should be queued after AgentQuestion event")
	}

	// Simulate the step resuming after the answer is delivered.
	m, _ = m.Update(engineEventMsg{event: engine.StepStatus{
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

// TestGateSubmitRouting verifies that submitting an InputRequest entry emits
// agentInputMsg for that entry's stepID and shrinks the queue by one, and that
// submitting the next entry routes to the second stepID.
func TestGateSubmitRouting(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Enqueue two InputRequest entries from distinct steps.
	m, _ = m.Update(engineEventMsg{event: engine.InputRequest{RunID: "run-1", StepID: "a"}})
	m, _ = m.Update(engineEventMsg{event: engine.InputRequest{RunID: "run-1", StepID: "b"}})
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
	msg, ok := cmd().(agentInputMsg)
	if !ok {
		t.Fatalf("expected agentInputMsg, got %T", cmd())
	}
	if msg.stepID != "a" {
		t.Fatalf("expected stepID a, got %q", msg.stepID)
	}
	if msg.text != "hello" {
		t.Fatalf("expected text hello, got %q", msg.text)
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
	msg2, ok := cmd2().(agentInputMsg)
	if !ok {
		t.Fatalf("expected agentInputMsg for second submit, got %T", cmd2())
	}
	if msg2.stepID != "b" {
		t.Fatalf("expected stepID b, got %q", msg2.stepID)
	}
}

// TestQuestionCancel verifies that pressing q on an inputKindQuestion entry
// emits agentQuestionResponseMsg with answer=="cancelled", removes the entry,
// and does not emit showRunsMsg.
func TestQuestionCancel(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:  "run-1",
		StepID: "a",
		Questions: []engine.AgentQuestionItem{
			{Question: "Pick one", Options: []engine.AgentQuestionOption{
				{Label: "Alpha"}, {Label: "Beta"},
			}},
		},
	}})

	if len(m.inputQueue) == 0 || m.inputQueue[0].kind != inputKindQuestion {
		t.Fatal("expected inputKindQuestion in queue")
	}

	m.focus = focusGate
	m, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q produced no command")
	}
	// Must not emit showRunsMsg.
	if _, isRuns := cmd().(showRunsMsg); isRuns {
		t.Fatal("q must not emit showRunsMsg — user stays in monitor")
	}
	// Rerun cmd() to get the actual message (cmd() may only be called once — use a copy).
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:  "run-1",
		StepID: "a",
		Questions: []engine.AgentQuestionItem{
			{Question: "Pick one", Options: []engine.AgentQuestionOption{
				{Label: "Alpha"}, {Label: "Beta"},
			}},
		},
	}})
	m2.focus = focusGate
	_, cmd2 := m2.Update(key("q"))
	resp, ok := cmd2().(agentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg, got %T", cmd2())
	}
	if resp.answer != "cancelled" {
		t.Fatalf("expected answer cancelled, got %q", resp.answer)
	}
	if resp.stepID != "a" {
		t.Fatalf("expected stepID a, got %q", resp.stepID)
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
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
		RunID:        "run-1",
		StepID:       "a",
		Choices:      []string{"approve", "reject"},
		AllowMessage: true,
	}})
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
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
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
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
	var opts []engine.AgentQuestionOption
	for i := 1; i <= 10; i++ {
		opts = append(opts, engine.AgentQuestionOption{Label: fmt.Sprintf("Option%d", i)})
	}
	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:  "run-1",
		StepID: "a",
		Questions: []engine.AgentQuestionItem{
			{Question: "Pick one", Options: opts},
		},
	}})
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

	// Press ↓ (j) to scroll down.
	m, _ = m.Update(key("j"))
	if m.inputQueue[0].scrollOffset != 1 {
		t.Fatalf("expected scrollOffset 1 after j, got %d", m.inputQueue[0].scrollOffset)
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

	// Press ↑ (k) to scroll back up.
	m, _ = m.Update(key("k"))
	if m.inputQueue[0].scrollOffset != 0 {
		t.Fatalf("expected scrollOffset 0 after k, got %d", m.inputQueue[0].scrollOffset)
	}
	if stripH() != h0 {
		t.Fatalf("gate strip height changed after scroll up: was %d, now %d", h0, stripH())
	}

	// Scroll down past the first visible window so Option1 is off-screen.
	for i := 0; i < 3; i++ {
		m, _ = m.Update(key("j"))
	}
	// Digit "1" must still select Option1 (absolute index, not visible-relative).
	m2, cmd := m.Update(key("1"))
	if cmd == nil {
		t.Fatal("digit 1 produced no command while scrolled")
	}
	resp, ok := cmd().(agentQuestionResponseMsg)
	if !ok {
		t.Fatalf("expected agentQuestionResponseMsg from digit 1, got %T", cmd())
	}
	if !strings.Contains(resp.answer, "Option1") {
		t.Fatalf("digit 1 selected wrong option: %q", resp.answer)
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

	var opts []engine.AgentQuestionOption
	for i := 1; i <= 10; i++ {
		opts = append(opts, engine.AgentQuestionOption{Label: fmt.Sprintf("Option%d", i)})
	}
	m, _ = m.Update(engineEventMsg{event: engine.AgentQuestion{
		RunID:  "run-1",
		StepID: "a",
		Questions: []engine.AgentQuestionItem{
			{Question: "Pick one", Options: opts},
		},
	}})
	m.focus = focusGate

	// Scroll down so both ▲ and ▼ are visible.
	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("j"))

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
	m, _ = m.Update(engineEventMsg{event: engine.ReviewRequest{
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
	m, _ = m.Update(engineEventMsg{event: engine.InputRequest{RunID: "run-1", StepID: "a"}})
	m, _ = m.Update(engineEventMsg{event: engine.InputRequest{RunID: "run-1", StepID: "b"}})
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

// TestMonitorIntegrationConflictGate verifies that an IntegrationConflictRequest
// surfaces an integration gate that names the conflicted paths and whose r/a
// actions emit resolveIntegrationResponseMsg (resolve / abort).
func TestMonitorIntegrationConflictGate(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.IntegrationConflictRequest{
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

	// Resolve routes to resolveIntegrationResponseMsg{abort:false}.
	m.focus = focusGate
	_, cmd := m.Update(key("r"))
	if cmd == nil {
		t.Fatal("r produced no command")
	}
	rr, ok := cmd().(resolveIntegrationResponseMsg)
	if !ok {
		t.Fatalf("expected resolveIntegrationResponseMsg, got %T", cmd())
	}
	if rr.abort || rr.stepID != "a" {
		t.Fatalf("got abort=%v stepID=%q; want resolve/a", rr.abort, rr.stepID)
	}

	// Abort routes to resolveIntegrationResponseMsg{abort:true}.
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(engineEventMsg{event: engine.IntegrationConflictRequest{RunID: "run-1", StepID: "a", Paths: []string{"x"}}})
	m2.focus = focusGate
	_, cmd = m2.Update(key("a"))
	if cmd == nil {
		t.Fatal("a produced no command")
	}
	if rr, ok := cmd().(resolveIntegrationResponseMsg); !ok || !rr.abort {
		t.Fatalf("expected abort resolveIntegrationResponseMsg, got %+v (%T)", cmd(), cmd())
	}
}

// TestMonitorFinalMergeGate verifies that a FinalMergeRequest surfaces a
// final-merge gate offering merge/discard, whose y/d actions emit
// finalMergeResponseMsg (approve / discard).
func TestMonitorFinalMergeGate(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(engineEventMsg{event: engine.FinalMergeRequest{
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

	// Approve routes to finalMergeResponseMsg{approve:true}.
	m.focus = focusGate
	_, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y produced no command")
	}
	fr, ok := cmd().(finalMergeResponseMsg)
	if !ok {
		t.Fatalf("expected finalMergeResponseMsg, got %T", cmd())
	}
	if !fr.approve || fr.runID != "run-1" {
		t.Fatalf("got approve=%v runID=%q; want approve/run-1", fr.approve, fr.runID)
	}

	// Discard routes to finalMergeResponseMsg{approve:false}.
	m2 := newMonitorWithSteps(t)
	m2, _ = m2.Update(engineEventMsg{event: engine.FinalMergeRequest{RunID: "run-1", RunBranch: "jig/wf/run-1", Base: "main"}})
	m2.focus = focusGate
	_, cmd = m2.Update(key("d"))
	if cmd == nil {
		t.Fatal("d produced no command")
	}
	if fr, ok := cmd().(finalMergeResponseMsg); !ok || fr.approve {
		t.Fatalf("expected discard finalMergeResponseMsg, got %+v (%T)", cmd(), cmd())
	}
}
