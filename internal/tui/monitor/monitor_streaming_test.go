package monitor

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
)

func TestMonitorStreamAppearsOnlyInTranscript(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		name := "persistence off"
		if persisted {
			name = "existing transcript"
		}
		t.Run(name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			if persisted {
				m.RunDir = writeTranscript(t, "a", []transcript.Entry{{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Previous response"}}}})
			}
			m = enterChatStep(t, m, "a")
			m.steps[m.index["a"]].status = step.StatusRunning
			m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			for _, delta := range []string{"Live response", " continues"} {
				m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: delta}})
				m, _ = m.Update(TickMsg(time.Now()))
				if got := stripANSI(m.vp.View()); strings.Contains(got, "Live response") {
					t.Errorf("response leaked into Steps: %s", got)
				}
				if got := stripANSI(m.chatVP.View()); !strings.Contains(got, "Live response") {
					t.Errorf("response missing from Transcript: %s", got)
				}
			}
			if got := stripANSI(m.chatVP.View()); !strings.Contains(got, "continues") {
				t.Errorf("next delta missing: %s", got)
			}
			if persisted {
				appendTranscriptEntry(t, m.RunDir, "a", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Live response continues"}}})
				m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{RunID: "run-1", StepID: "a", Seq: 2}})
				m, _ = m.Update(TickMsg(time.Now()))
				if got := stripANSI(m.chatVP.View()); strings.Count(got, "Live response") != 1 {
					t.Errorf("finalized response must replace preview: %s", got)
				}
			}
		})
	}
}

func TestMonitorStreamRespectsSelectedContent(t *testing.T) {
	m := newMonitorWithSteps(t)
	m = enterChatStep(t, m, "a")
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "b", Delta: "Other step response"}})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "stale-run", StepID: "a", Delta: "Stale response"}})
	if got := stripANSI(m.chatBody()); strings.Contains(got, "response") {
		t.Fatalf("unrelated output in selected transcript: %s", got)
	}
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: "Selected response"}})
	m.chatPage.HasLater = true
	if got := stripANSI(m.chatBody()); strings.Contains(got, "Selected response") {
		t.Fatalf("live output appended to historical page: %s", got)
	}
	m.chatPage.HasLater = false
	m.selKind, m.selFile = "file", "missing-file"
	if got := stripANSI(m.chatBody()); strings.Contains(got, "Selected response") {
		t.Fatalf("live output replaced file view: %s", got)
	}
}

func TestMonitorStreamSurvivesResizeAndPersistenceOffFinalization(t *testing.T) {
	m := newMonitorWithSteps(t)
	m = enterChatStep(t, m, "a")
	for _, width := range []int{120, 60, 20} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: "Hello"}})
		m, _ = m.Update(TickMsg(time.Now()))
		if got := stripANSI(m.chatVP.View()); !strings.Contains(got, "Hello") {
			t.Fatalf("live output missing at width %d: %s", width, got)
		}
		m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{RunID: "run-1", StepID: "a", Seq: 1}})
		if got := m.chatBody(); strings.Contains(got, "Hello") {
			t.Fatalf("finalization retained preview: %s", got)
		}
	}
}
