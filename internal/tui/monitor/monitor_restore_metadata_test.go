package monitor

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
)

func TestMonitorLiveSnapshotAndJournalHaveSameMetadata(t *testing.T) {
	for _, status := range []step.Status{step.StatusSucceeded, step.StatusFailed, step.StatusStopped, step.StatusNeedsInput, step.StatusAwaitingRecovery} {
		t.Run(string(status), func(t *testing.T) {
			runDir := writeTranscript(t, "a", liveEntries(chatWindowMax+5))
			base := time.Date(2026, 10, 1, 10, 0, 0, 123456789, time.UTC)
			events := []engine.Event{
				engine.RunStarted{RunID: "r", Workflow: "fixture", Steps: []string{"a", "b"}},
				engine.StepStatus{RunID: "r", StepID: "a", At: base, To: step.StatusRunning},
				engine.StepStatus{RunID: "r", StepID: "a", At: base.Add(12500 * time.Millisecond), To: status, Err: "synthetic error"},
			}
			live := New("r")
			live.RunDir = runDir
			for _, event := range events {
				live = sendEvent(live, event)
			}
			live, _ = live.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			// Deliberately send no StepMessage: finalized files must suffice.
			snapshot := New("r")
			snapshot.RunDir = runDir
			snapshot = snapshot.WithSnapshot(engine.RunSnapshot{Workflow: "fixture", Steps: []step.State{
				{ID: "a", Status: status, Timing: live.steps[0].timing, Result: &step.Result{Err: "synthetic error"}},
				{ID: "b", Status: step.StatusPending},
			}})
			// Exercise real serialization/replay, not just WithJournal's event loop.
			var journal []byte
			for i, event := range events {
				line, err := engine.MarshalEnvelope(i+1, event)
				if err != nil {
					t.Fatal(err)
				}
				journal = append(journal, append(line, '\n')...)
			}
			if err := os.WriteFile(datastore.JournalPath(runDir), journal, 0o600); err != nil {
				t.Fatal(err)
			}
			replayed, err := engine.ReplayJournalRaw(runDir)
			if err != nil {
				t.Fatal(err)
			}
			reopened := New("r")
			reopened.RunDir = runDir
			reopened = reopened.WithJournal(replayed)
			for name, model := range map[string]Model{"live": live, "snapshot": snapshot, "journal": reopened} {
				if got := stepDuration(model.steps[0]); got != "12.5s" {
					t.Fatalf("%s duration=%q", name, got)
				}
				if got := model.msgCount["a"]; got != chatWindowMax+5 {
					t.Fatalf("%s count=%d", name, got)
				}
				if model.msgCount["b"] != 0 {
					t.Fatalf("%s missing transcript has messages", name)
				}
				if !reflect.DeepEqual(model.chatEntries, live.chatEntries) {
					t.Fatalf("%s transcript differs from live", name)
				}
				if !strings.Contains(model.listBody(), "305 msg") {
					t.Fatalf("%s list omits restored count", name)
				}
			}
		})
	}
}

func TestMonitorTerminalRefreshRecoversDroppedMessage(t *testing.T) {
	m, runDir := newDiskLiveModel(t, liveEntries(1))
	appendTranscriptEntry(t, runDir, "a", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "final saved reply"}}})
	m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "a", At: time.Now(), To: step.StatusSucceeded})
	if m.msgCount["a"] != 2 || !strings.Contains(strippedBody(m), "final saved reply") {
		t.Fatalf("final status did not repair dropped notification: count=%d body=%s", m.msgCount["a"], strippedBody(m))
	}
}

func TestMonitorLiveRefreshKeepsPausedPage(t *testing.T) {
	m, runDir := newDiskLiveModel(t, liveEntries(1))
	m.chatAutoScroll = false
	before := m.chatPage
	seq := appendTranscriptEntry(t, runDir, "a", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "unseen saved reply"}}})
	// An old notification still refreshes from the file's current sequence.
	m = sendEvent(m, engine.StepMessage{RunID: "run-1", StepID: "a", Seq: 1})
	m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "a", At: time.Now(), To: step.StatusStopped})
	if m.msgCount["a"] != seq || !reflect.DeepEqual(m.chatPage, before) || m.chatAutoScroll {
		t.Fatal("refresh failed to update count while preserving paused page")
	}
}

func TestMonitorRetryClearsPreviousTiming(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	m := newMonitorWithSteps(t)
	for _, ev := range []engine.StepStatus{
		{RunID: "run-1", StepID: "a", At: base, To: step.StatusRunning},
		{RunID: "run-1", StepID: "a", At: base.Add(time.Second), To: step.StatusFailed},
		{RunID: "run-1", StepID: "a", At: base.Add(time.Minute), To: step.StatusPending},
		{RunID: "run-1", StepID: "a", At: base.Add(2 * time.Minute), To: step.StatusRunning, Attempt: 1},
		{RunID: "run-1", StepID: "a", At: base.Add(2*time.Minute + 3*time.Second), To: step.StatusSucceeded, Attempt: 1},
	} {
		m = sendEvent(m, ev)
	}
	if got := stepDuration(m.steps[0]); got != "3s" {
		t.Fatalf("retry duration=%s", got)
	}
}

func TestMonitorFanOutChildRestoresMetadata(t *testing.T) {
	id := fanOutChildID("b", 0)
	runDir := writeTranscript(t, id, liveEntries(2))
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var timing step.Timing
	timing.Apply(step.StatusRunning, base)
	timing.Apply(step.StatusSucceeded, base.Add(4*time.Second))
	events := []engine.Event{
		engine.RunStarted{RunID: "r", Steps: []string{"b"}},
		engine.FanOutExpanded{RunID: "r", FamilyID: "b", SchemaVersion: engine.FanOutExpandedVersion,
			Instances: []engine.FanOutInstanceDescriptor{{InstanceID: id, Index: 0}}},
		engine.StepStatus{RunID: "r", StepID: id, At: base.Add(4 * time.Second), Timing: &timing, To: step.StatusSucceeded},
	}
	m := New("r")
	m.RunDir = runDir
	m = m.WithJournal(events)
	m.expanded["b"] = true
	if got := stepDuration(m.steps[m.index[id]]); got != "4s" || m.msgCount[id] != 2 {
		t.Fatalf("child duration=%s count=%d", got, m.msgCount[id])
	}
	if body := m.listBody(); !strings.Contains(body, "4s") || !strings.Contains(body, "2 msg") {
		t.Fatalf("child metadata missing: %s", body)
	}
}

func TestMonitorRunningSnapshotPreservesClock(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var timing step.Timing
	timing.Apply(step.StatusRunning, base)
	for _, status := range []step.Status{step.StatusRunning, step.StatusValidating} {
		m := New("r").WithSnapshot(engine.RunSnapshot{Steps: []step.State{{ID: "a", Status: status, Timing: timing}}})
		if got := m.steps[0].timing.Duration(base.Add(10 * time.Second)); got != 10*time.Second {
			t.Fatalf("snapshot restarted active timer: %v", got)
		}
		if m.EnsureFrame() == nil {
			t.Fatalf("%s snapshot did not restart clock repaint", status)
		}
	}
}

func TestMonitorInterruptedHistoryFreezesClock(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	m := New("r").WithJournal([]engine.Event{
		engine.RunStarted{RunID: "r", Steps: []string{"a", "b"}},
		engine.StepStatus{RunID: "r", StepID: "a", At: base, To: step.StatusRunning},
		engine.StepStatus{RunID: "r", StepID: "b", At: base.Add(4 * time.Second), To: step.StatusSkipped},
	})
	if got := stepDuration(m.steps[0]); got != "4s" {
		t.Fatalf("history clock includes time since process exit: %s", got)
	}
	if m.EnsureFrame() != nil {
		t.Fatal("historical clock should not keep repainting")
	}
}
