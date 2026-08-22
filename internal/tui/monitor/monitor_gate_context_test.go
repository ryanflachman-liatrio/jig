package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/transcript"
)

func ctrlO() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
}

func addTranscript(t *testing.T, runDir, stepID string, entries []transcript.Entry) {
	t.Helper()
	path := datastore.TranscriptPath(runDir, stepID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	writer, err := transcript.Create(path)
	if err != nil {
		t.Fatalf("create transcript: %v", err)
	}
	for _, entry := range entries {
		if _, err := writer.Append(entry); err != nil {
			t.Fatalf("append transcript: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
}

func TestGatePresentations(t *testing.T) {
	tests := []struct {
		name         string
		entry        pendingInputEntry
		title        string
		subjectLabel string
		subject      string
		contextStep  string
	}{
		{name: "agent input", entry: pendingInputEntry{kind: inputKindRequest, stepID: "build"}, title: "Agent input required", subjectLabel: "Step", subject: "build", contextStep: "build"},
		{name: "question", entry: pendingInputEntry{kind: inputKindQuestion, stepID: "research"}, title: "Answer required", subjectLabel: "Step", subject: "research", contextStep: "research"},
		{name: "prompt", entry: pendingInputEntry{kind: inputKindPrompt, stepID: "plan"}, title: "User input required", subjectLabel: "Step", subject: "plan", contextStep: "plan"},
		{name: "review", entry: pendingInputEntry{kind: inputKindReview, stepID: "review"}, title: "Review required", subjectLabel: "Step", subject: "review", contextStep: "review"},
		{name: "recovery", entry: pendingInputEntry{kind: inputKindRecovery, stepID: "implement"}, title: "Recovery action", subjectLabel: "Step", subject: "implement", contextStep: "implement"},
		{name: "integration", entry: pendingInputEntry{kind: inputKindIntegrationConflict, stepID: "merge"}, title: "Conflict resolution", subjectLabel: "Step", subject: "merge", contextStep: "merge"},
		{
			name: "final merge",
			entry: pendingInputEntry{
				kind:       inputKindFinalMerge,
				stepID:     "jig/run",
				finalMerge: &engine.FinalMergeRequest{RunBranch: "jig/run"},
			},
			title:        "Merge approval",
			subjectLabel: "Run branch",
			subject:      "jig/run",
		},
		{name: "reset", entry: pendingInputEntry{kind: inputKindResetConfirm, stepID: "test"}, title: "Reset confirmation", subjectLabel: "Step", subject: "test", contextStep: "test"},
		{name: "help merge", entry: pendingInputEntry{kind: inputKindHelpFinalMerge}, title: "Merge approval", subjectLabel: "Scope", subject: "Run-level action"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := presentationForGate(&tt.entry)
			if got.title != tt.title || got.subjectLabel != tt.subjectLabel ||
				got.subject != tt.subject || got.contextStep != tt.contextStep {
				t.Fatalf("presentation = %+v, want title=%q subject=%q:%q context=%q",
					got, tt.title, tt.subjectLabel, tt.subject, tt.contextStep)
			}
			if got.action == "" {
				t.Fatal("required action is empty")
			}
		})
	}
}

func TestGateQueueSwitchUpdatesContextWithoutFollowing(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.cursor = 2
	m.chatStep = ""
	m.reloadTranscript()
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "run-1", StepID: "a", Choices: []string{"approve"},
	}})
	m, _ = m.Update(EngineEventMsg{Event: engine.RecoveryRequest{
		RunID: "run-1", StepID: "b", Err: "failed",
	}})
	m.focus = focusGate

	first := ansiStrip(m.gateOverlay())
	for _, want := range []string{"Review required", "Step: a", "view diff"} {
		if !strings.Contains(first, want) {
			t.Fatalf("first gate missing %q:\n%s", want, first)
		}
	}
	if bar := ansiStrip(m.inputBarView()); !strings.Contains(bar, "Review required") || !strings.Contains(bar, "Step: a") {
		t.Fatalf("first input bar is not contextual:\n%s", bar)
	}

	m, _ = m.Update(key("]"))
	if m.cursor != 2 || m.chatStep != "c" {
		t.Fatalf("queue switch followed context: cursor=%d chatStep=%q", m.cursor, m.chatStep)
	}
	second := ansiStrip(m.gateOverlay())
	for _, want := range []string{"Recovery action", "Step: b", "view transcript"} {
		if !strings.Contains(second, want) {
			t.Fatalf("second gate missing %q:\n%s", want, second)
		}
	}
	if bar := ansiStrip(m.inputBarView()); !strings.Contains(bar, "Recovery action") || !strings.Contains(bar, "Step: b") {
		t.Fatalf("second input bar is not contextual:\n%s", bar)
	}
}

func TestGateContextJumpAndReturnPreserveTranscriptState(t *testing.T) {
	runDir := t.TempDir()
	addTranscript(t, runDir, "b", []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{
			{Type: transcript.BlockThinking, Text: strings.Repeat("thinking ", 50)},
			{Type: transcript.BlockText, Text: strings.Repeat("prior transcript line\n", 80)},
		},
	}})

	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "b")
	m.chatExpand[blockKey{seq: 1, block: 0}] = true
	m.rebuildActiveState(m.chatBlocks[0])
	m.refreshPanels()
	m.chatVP.SetYOffset(5)
	m.chatAutoScroll = false
	priorOffset := m.chatVP.YOffset()

	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		Diff:    "@@ -1 +1 @@\n-old\n+new",
		Choices: []string{"approve", "reject"},
	}})
	m.focus = focusGate

	jumped, cmd := m.Update(ctrlO())
	if cmd != nil {
		t.Fatal("context navigation emitted a command")
	}
	if jumped.focus != focusTranscript || jumped.cursorStepID() != "a" || jumped.chatStep != "a" {
		t.Fatalf("jump state: focus=%v cursorStep=%q chatStep=%q", jumped.focus, jumped.cursorStepID(), jumped.chatStep)
	}
	if jumped.gateContext == nil || jumped.gateContext.targetStep != "a" {
		t.Fatal("jump did not save the prior context")
	}
	if hint := ansiStrip(jumped.hintLabel()); !strings.Contains(hint, "ctrl+o return") {
		t.Fatalf("context return is not discoverable in transcript hint: %q", hint)
	}
	if len(jumped.inputQueue) != 1 || jumped.activeInputIdx != 0 {
		t.Fatal("context navigation changed the gate queue")
	}
	for _, want := range []string{"proposed changes", "old", "new"} {
		if !strings.Contains(jumped.chatBody(), want) {
			t.Fatalf("review context missing %q:\n%s", want, jumped.chatBody())
		}
	}

	restored, cmd := jumped.Update(ctrlO())
	if cmd != nil {
		t.Fatal("context return emitted a command")
	}
	if restored.focus != focusGate || restored.cursorStepID() != "b" || restored.chatStep != "b" {
		t.Fatalf("restore state: focus=%v cursorStep=%q chatStep=%q", restored.focus, restored.cursorStepID(), restored.chatStep)
	}
	if restored.gateContext != nil {
		t.Fatal("context return snapshot was not cleared")
	}
	if restored.chatVP.YOffset() != priorOffset || restored.chatAutoScroll {
		t.Fatalf("transcript position not restored: offset=%d autoScroll=%v, want %d/false",
			restored.chatVP.YOffset(), restored.chatAutoScroll, priorOffset)
	}
	if !restored.chatExpand[blockKey{seq: 1, block: 0}] {
		t.Fatal("transcript expansion state was not restored")
	}
	if len(restored.inputQueue) != 1 {
		t.Fatal("context return changed the gate queue")
	}
}

func TestGateContextWorksWhileTextareaCapturesInput(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "b"}})
	m.focus = focusGate
	m.promptTextarea.SetValue("draft")

	got, cmd := m.Update(ctrlO())
	if cmd != nil {
		t.Fatal("context navigation emitted a command")
	}
	if got.focus != focusTranscript || got.cursorStepID() != "b" {
		t.Fatalf("ctrl+o did not open textarea gate context: focus=%v step=%q", got.focus, got.cursorStepID())
	}
	if got.inputQueue[0].draft != "draft" {
		t.Fatalf("context navigation saved draft %q, want %q", got.inputQueue[0].draft, "draft")
	}
}

func TestRunLevelGateHasNoStepContextAction(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: engine.FinalMergeRequest{
		RunID: "run-1", RunBranch: "jig/run", Base: "main",
	}})
	m.focus = focusGate

	view := ansiStrip(m.gateOverlay())
	for _, want := range []string{"Merge approval", "Run branch: jig/run", "Required: Merge or discard"} {
		if !strings.Contains(view, want) {
			t.Fatalf("run-level gate missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "ctrl+o") {
		t.Fatalf("run-level gate advertised unavailable step context:\n%s", view)
	}

	got, cmd := m.Update(ctrlO())
	if cmd != nil || got.focus != focusGate || got.gateContext != nil {
		t.Fatalf("run-level context action was not inert: cmd=%v focus=%v snapshot=%v", cmd, got.focus, got.gateContext)
	}
}

func TestGateResolutionRestoresSavedContext(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.cursor = 2
	m.chatStep = ""
	m.reloadTranscript()
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "run-1", StepID: "a", Choices: []string{"approve"},
	}})
	m.focus = focusGate
	m, _ = m.Update(ctrlO())
	m, _ = m.Update(key("tab"))
	if m.focus != focusGate {
		t.Fatalf("tab from context focused %v, want gate", m.focus)
	}

	resolved, cmd := m.Update(key("1"))
	if cmd == nil {
		t.Fatal("review verdict emitted no command")
	}
	if resolved.cursorStepID() != "c" || resolved.chatStep != "c" {
		t.Fatalf("resolution did not restore prior selection: cursorStep=%q chatStep=%q",
			resolved.cursorStepID(), resolved.chatStep)
	}
	if resolved.gateContext != nil || len(resolved.inputQueue) != 0 {
		t.Fatal("resolution left stale gate context state")
	}
}
