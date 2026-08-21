package monitor

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/step"
	"jig/internal/transcript"
)

// TestInputQueueIngest verifies that three InputRequest events for distinct steps
// produce len(inputQueue)==3 in arrival order, activeInputIdx==0, and m.focus
// equal to its pre-arrival value (no focus steal — Decision 6).
func TestInputQueueIngest(t *testing.T) {
	m := newMonitorWithSteps(t)
	preFocus := m.focus

	for _, stepID := range []string{"a", "b", "c"} {
		m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{
			RunID:  "run-1",
			StepID: stepID,
		}})
	}

	if got := len(m.inputQueue); got != 3 {
		t.Fatalf("expected len(inputQueue)==3, got %d", got)
	}
	// Arrival order preserved.
	for i, want := range []string{"a", "b", "c"} {
		if got := m.inputQueue[i].stepID; got != want {
			t.Fatalf("inputQueue[%d].stepID = %q, want %q", i, got, want)
		}
		if m.inputQueue[i].kind != inputKindRequest {
			t.Fatalf("inputQueue[%d].kind = %v, want inputKindRequest", i, m.inputQueue[i].kind)
		}
	}
	if m.activeInputIdx != 0 {
		t.Fatalf("activeInputIdx = %d, want 0", m.activeInputIdx)
	}
	// No focus steal on arrival.
	if m.focus != preFocus {
		t.Fatalf("focus changed from %v to %v on InputRequest arrival", preFocus, m.focus)
	}
}

// TestInputQueueMixedKinds verifies that a ReviewRequest and an AgentQuestion for
// two distinct steps coexist as two entries with the correct kinds.
func TestInputQueueMixedKinds(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Choices: []string{"approve"},
	}})
	m, _ = m.Update(EngineEventMsg{Event: questionEvent(
		"run-1", "b", "tu1",
		selectQuestion("q", "", "Q?", false, interaction.QuestionOption{Value: "A", Label: "A"}),
	)})

	if got := len(m.inputQueue); got != 2 {
		t.Fatalf("expected 2 entries, got %d", got)
	}
	if m.inputQueue[0].kind != inputKindReview {
		t.Fatalf("inputQueue[0].kind = %v, want inputKindReview", m.inputQueue[0].kind)
	}
	if m.inputQueue[0].stepID != "a" {
		t.Fatalf("inputQueue[0].stepID = %q, want \"a\"", m.inputQueue[0].stepID)
	}
	if m.inputQueue[1].kind != inputKindQuestion {
		t.Fatalf("inputQueue[1].kind = %v, want inputKindQuestion", m.inputQueue[1].kind)
	}
	if m.inputQueue[1].stepID != "b" {
		t.Fatalf("inputQueue[1].stepID = %q, want \"b\"", m.inputQueue[1].stepID)
	}
}

func TestQuestionResolutionRemovesOnlyMatchingEntry(t *testing.T) {
	m := newMonitorWithSteps(t)
	for _, id := range []string{"q1", "q2"} {
		m, _ = m.Update(EngineEventMsg{Event: questionEvent(
			"run-1", "a", id,
			selectQuestion("answer", "", "Choose", false, interaction.QuestionOption{Value: "A", Label: "A"}),
		)})
	}
	m, _ = m.Update(EngineEventMsg{Event: engine.AgentQuestionResolved{
		RunID: "run-1", StepID: "a", RequestID: "q1", Action: interaction.ActionCancel,
	}})
	if len(m.inputQueue) != 1 || m.inputQueue[0].question.Request().ID != "q2" {
		t.Fatalf("queue after resolution = %+v", m.inputQueue)
	}
}

// TestInputQueuePruneOnStatus verifies that a StepStatus moving a queued step to
// StatusRunning removes exactly that step's entry and leaves activeInputIdx within
// [0, len(inputQueue)); pruning on an empty queue does not panic.
func TestInputQueuePruneOnStatus(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Queue entries for all three steps.
	for _, stepID := range []string{"a", "b", "c"} {
		m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{
			RunID:  "run-1",
			StepID: stepID,
		}})
	}
	if got := len(m.inputQueue); got != 3 {
		t.Fatalf("setup: expected 3 entries, got %d", got)
	}

	// Prune step "b" (index 1).
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID:  "run-1",
		StepID: "b",
		From:   step.StatusNeedsInput,
		To:     step.StatusRunning,
	}})

	if got := len(m.inputQueue); got != 2 {
		t.Fatalf("expected 2 entries after prune, got %d", got)
	}
	// Remaining entries are "a" and "c".
	if m.inputQueue[0].stepID != "a" {
		t.Fatalf("inputQueue[0].stepID = %q, want \"a\"", m.inputQueue[0].stepID)
	}
	if m.inputQueue[1].stepID != "c" {
		t.Fatalf("inputQueue[1].stepID = %q, want \"c\"", m.inputQueue[1].stepID)
	}
	// activeInputIdx is within range.
	if m.activeInputIdx < 0 || m.activeInputIdx >= len(m.inputQueue) {
		t.Fatalf("activeInputIdx %d out of range [0, %d)", m.activeInputIdx, len(m.inputQueue))
	}

	// Prune the remaining two.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", From: step.StatusNeedsInput, To: step.StatusRunning,
	}})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "c", From: step.StatusNeedsInput, To: step.StatusRunning,
	}})

	if got := len(m.inputQueue); got != 0 {
		t.Fatalf("expected empty queue after all prunes, got %d", got)
	}

	// Pruning on an already-empty queue must not panic.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", From: step.StatusRunning, To: step.StatusSucceeded,
	}})
	// No panic = pass.
}

func TestEmptyInputBarIsCompactAndPanelHeightIsStable(t *testing.T) {
	m := newMonitorWithSteps(t)

	barHBefore := lipgloss.Height(m.inputBarView())
	panelHBefore := m.verticalLayout().panelH
	if barHBefore < 1 || barHBefore > 2 {
		t.Fatalf("empty input bar height = %d, want 1–2 rows", barHBefore)
	}

	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{
		RunID:  "run-1",
		StepID: "a",
	}})

	if got := lipgloss.Height(m.inputBarView()); got != barHBefore {
		t.Fatalf("input bar height changed on request arrival: %d → %d", barHBefore, got)
	}
	if panelHAfter := m.verticalLayout().panelH; panelHAfter != panelHBefore {
		t.Fatalf("panel height changed on InputRequest arrival: %d → %d", panelHBefore, panelHAfter)
	}
}

func TestGateOverlayPreservesTranscriptPositionAndDraft(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{{
		Role: transcript.RoleSystem,
		Blocks: []transcript.Block{{
			Type: transcript.BlockText,
			Text: strings.Repeat("transcript line\n", 80),
		}},
	}})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m.chatStep = ""
	m.reloadTranscript()
	m.focus = focusTranscript
	m.chatVP.SetYOffset(8)
	offset := m.chatVP.YOffset()
	panelH := m.verticalLayout().panelH

	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "a"}})
	if m.focus != focusTranscript {
		t.Fatalf("request arrival stole focus: got %v", m.focus)
	}

	m, _ = m.Update(key("tab"))
	if m.focus != focusGate {
		t.Fatalf("tab from transcript focused %v, want gate", m.focus)
	}
	if got := m.verticalLayout().panelH; got != panelH {
		t.Fatalf("opening overlay changed panel height: %d → %d", panelH, got)
	}
	if got := m.chatVP.YOffset(); got != offset {
		t.Fatalf("opening overlay changed transcript offset: %d → %d", offset, got)
	}
	m.promptTextarea.SetValue("partial answer")

	m, _ = m.Update(key("esc"))
	if m.focus != focusSteps {
		t.Fatalf("esc from gate focused %v, want steps", m.focus)
	}
	if got := m.chatVP.YOffset(); got != offset {
		t.Fatalf("closing overlay changed transcript offset: %d → %d", offset, got)
	}
	if got := m.inputQueue[0].draft; got != "partial answer" {
		t.Fatalf("draft after closing overlay = %q", got)
	}

	m, _ = m.Update(key("tab"))
	m, _ = m.Update(key("tab"))
	if m.focus != focusGate {
		t.Fatalf("reopening gate focused %v", m.focus)
	}
	if got := m.promptTextarea.Value(); got != "partial answer" {
		t.Fatalf("draft after reopening overlay = %q", got)
	}
}

// stripANSI removes ANSI escape codes from s for plain-text artifact storage.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // consume 'm'
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestCycleFocusSkipsEmptyGate verifies that with an empty queue, cycling focus
// from focusTranscript wraps back to focusSteps without landing on focusGate
// (Unit 2: the empty gate is inert, not focusable via tab).
func TestCycleFocusSkipsEmptyGate(t *testing.T) {
	m := newMonitorWithSteps(t)

	if m.hasGate() {
		t.Fatal("expected empty gate at start")
	}

	// Start at focusTranscript and cycle forward (+1).
	m.focus = focusTranscript
	next := m.cycleFocus(+1)
	if next == focusGate {
		t.Fatalf("cycleFocus(+1) landed on focusGate with empty queue")
	}
	if next != focusSteps {
		t.Fatalf("cycleFocus(+1) from focusTranscript with empty queue: got %v, want focusSteps", next)
	}
}

// TestGateDraftPreservation verifies that per-entry drafts survive tab/shift+tab
// navigation: typing into entry 2, tabbing to entry 1, and tabbing back restores
// the text. Task 3.8 adds an arrow-exit variant to prove syncActiveTextarea is
// also called on left/right panel exit.
func TestGateDraftPreservation(t *testing.T) {
	m := newMonitorWithSteps(t)

	// Enqueue two InputRequest entries for distinct steps.
	for _, id := range []string{"a", "b"} {
		m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: id}})
	}
	if len(m.inputQueue) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m.inputQueue))
	}

	// Focus the gate so key events route there.
	m.focus = focusGate
	m.loadActiveTextarea() // sync textarea to active entry (idx 0)

	// Tab to entry 2 (idx 1). Entry 1 draft should remain "".
	m, _ = m.Update(key("tab"))
	if m.activeInputIdx != 1 {
		t.Fatalf("after tab: activeInputIdx = %d, want 1", m.activeInputIdx)
	}

	// Type "hello" into entry 2's textarea via the promptTextarea path.
	m.promptTextarea.SetValue("hello")

	// Tab back to entry 1. Entry 2's draft must be saved.
	m, _ = m.Update(key("tab"))
	if m.activeInputIdx != 0 {
		t.Fatalf("after second tab: activeInputIdx = %d, want 0", m.activeInputIdx)
	}
	if got := m.inputQueue[1].draft; got != "hello" {
		t.Fatalf("entry 2 draft after tab-away = %q, want %q", got, "hello")
	}

	// Tab back to entry 2 and confirm textarea is restored.
	m, _ = m.Update(key("tab"))
	if m.activeInputIdx != 1 {
		t.Fatalf("after third tab: activeInputIdx = %d, want 1", m.activeInputIdx)
	}
	if got := m.promptTextarea.Value(); got != "hello" {
		t.Fatalf("textarea value after returning to entry 2 = %q, want %q", got, "hello")
	}

	// 3.8 arrow-exit variant: type into entry 2, exit with right arrow, re-enter
	// via tab, and confirm the draft is restored.
	m.promptTextarea.SetValue("world")
	m, _ = m.Update(key("right"))
	if m.focus == focusGate {
		t.Fatalf("right arrow did not exit the gate")
	}
	// Draft must be saved on panel exit.
	if got := m.inputQueue[1].draft; got != "world" {
		t.Fatalf("entry 2 draft after right-arrow exit = %q, want %q", got, "world")
	}

	// Tab back into gate (focuses gate region via cycleFocus since queue is non-empty).
	m, _ = m.Update(key("tab"))
	// Tab cycles region here (from focusSteps → focusTranscript → focusGate) until
	// we land on the gate. Keep tabbing until focusGate.
	for m.focus != focusGate {
		m, _ = m.Update(key("tab"))
	}
	// Now tab within the gate to return to entry 2 (if not already there).
	for m.activeInputIdx != 1 {
		m, _ = m.Update(key("tab"))
	}
	if got := m.promptTextarea.Value(); got != "world" {
		t.Fatalf("textarea value after re-entering gate entry 2 = %q, want %q", got, "world")
	}
}

// TestGateEscBlurs verifies that esc while focusGate sets m.focus == focusSteps,
// leaves the queue unchanged, and emits no showRunsMsg (Decision 6 / ADR 0005).
func TestGateEscBlurs(t *testing.T) {
	m := newMonitorWithSteps(t)

	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "a"}})
	if len(m.inputQueue) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m.inputQueue))
	}
	m.focus = focusGate

	var runsNavigated bool
	m2, cmd := m.Update(key("esc"))
	if cmd != nil {
		// Execute the command and check it does not produce a showRunsMsg.
		result := cmd()
		if _, ok := result.(ShowRunsMsg); ok {
			runsNavigated = true
		}
	}

	if m2.focus != focusSteps {
		t.Fatalf("after esc: focus = %v, want focusSteps", m2.focus)
	}
	if got := len(m2.inputQueue); got != 1 {
		t.Fatalf("after esc: inputQueue len = %d, want 1 (queue must not be cleared)", got)
	}
	if runsNavigated {
		t.Fatal("esc emitted showRunsMsg — gate blur must not navigate away")
	}
}

// captureUnit3Nav captures View() frames for the unit3-nav.txt proof artifact:
// a two-entry queue showing [1/2], after tab → [2/2], after shift+tab → [1/2]
// wrapping back from [1/2] to [2/2].
func init() {
	// Captured by TestGateDraftPreservation's setup above; we capture separately
	// via TestGateNavFrames so it doesn't slow the draft test.
	_ = captureUnit3NavFrames // called by TestGateNavFrames
}

func captureUnit3NavFrames(m Model) {
	frames := make([]string, 0, 3)

	// Frame 1: [1 / 2] active.
	frames = append(frames, stripANSI(m.View()))

	// Frame 2: after tab → [2 / 2].
	m2, _ := m.Update(key("tab"))
	frames = append(frames, stripANSI(m2.View()))

	// Frame 3: from [1/2] after shift+tab wraps to [2/2].
	m3, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	frames = append(frames, stripANSI(m3.View()))

	combined := strings.Join(frames, "\n\n--- frame ---\n\n")
	_ = os.MkdirAll("../../docs/specs/02-spec-tui-persistent-agent-input/artifacts", 0o755)
	_ = os.WriteFile(
		"../../docs/specs/02-spec-tui-persistent-agent-input/artifacts/unit3-nav.txt",
		[]byte(combined),
		0o644,
	)
}

func TestGateNavFrames(t *testing.T) {
	m := newMonitorWithSteps(t)

	for _, id := range []string{"a", "b"} {
		m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: id}})
	}
	m.focus = focusGate
	m.loadActiveTextarea()

	// Verify [1 / 2] header is visible.
	if !strings.Contains(m.View(), "[1 / 2]") {
		t.Fatalf("expected [1 / 2] header in view:\n%s", stripANSI(m.View()))
	}

	// Tab → [2 / 2].
	m2, _ := m.Update(key("tab"))
	if m2.activeInputIdx != 1 {
		t.Fatalf("after tab: activeInputIdx = %d, want 1", m2.activeInputIdx)
	}
	if !strings.Contains(m2.View(), "[2 / 2]") {
		t.Fatalf("expected [2 / 2] header after tab:\n%s", stripANSI(m2.View()))
	}

	// shift+tab from [1/2] wraps to [2/2].
	m3, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m3.activeInputIdx != 1 {
		t.Fatalf("shift+tab from idx 0: activeInputIdx = %d, want 1 (wrap)", m3.activeInputIdx)
	}
	if !strings.Contains(m3.View(), "[2 / 2]") {
		t.Fatalf("expected [2 / 2] after shift+tab wrap:\n%s", stripANSI(m3.View()))
	}

	captureUnit3NavFrames(m)
}
