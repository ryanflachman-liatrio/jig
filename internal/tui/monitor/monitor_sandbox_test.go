package monitor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
)

func TestD04ReopenPolicyGate(t *testing.T) {
	m := New("r")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 35})
	m, _ = m.handleEngineEvent(engine.RecoveryRequest{RunID: "r", StepID: "work", Err: "original", CanResume: true})
	m.inputQueue[0].draft = "keep this guidance"
	m, _ = m.handleEngineEvent(engine.SandboxPolicyBlocked{RunID: "r", Reason: "sandbox_policy_denied", Epoch: 3})
	m = m.FocusPendingInput()
	if !m.hasGate() || m.focus != focusGate || !strings.Contains(m.gateOverlay(), "recheck policy") || !strings.Contains(m.statusLabel(), "sandbox policy blocked") {
		t.Fatal("missing policy controls")
	}
	for _, size := range [][2]int{{30, 10}, {120, 40}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if !strings.Contains(m.gateOverlay(), "sandbox policy") {
			t.Fatal("resize lost policy gate")
		}
	}
	m, cmd := m.updateGate(tea.KeyPressMsg{Code: 'r'})
	if cmd == nil {
		t.Fatal("missing recheck")
	}
	action, ok := cmd().(SandboxPolicyActionMsg)
	if !ok || action.Epoch != 3 || action.Abort {
		t.Fatalf("action %+v", action)
	}
	if _, cmd := m.updateGate(tea.KeyPressMsg{Code: 'r'}); cmd != nil {
		t.Fatal("duplicate pending recheck")
	}
	m, _ = m.handleEngineEvent(engine.SandboxPolicyCleared{RunID: "other", Epoch: 3})
	if m.policyBlock == nil {
		t.Fatal("foreign grant")
	}
	m, _ = m.handleEngineEvent(engine.SandboxPolicyCleared{RunID: "r", Epoch: 2})
	if m.policyBlock == nil {
		t.Fatal("stale grant")
	}
	m, _ = m.handleEngineEvent(engine.SandboxPolicyCleared{RunID: "r", Epoch: 3})
	if m.policyBlock != nil || len(m.inputQueue) != 1 || m.inputQueue[0].draft != "keep this guidance" {
		t.Fatal("grant discarded underlying park/draft")
	}
	m, _ = m.handleEngineEvent(engine.SandboxPolicyBlocked{RunID: "r", Reason: "sandbox_policy_denied", Epoch: 4})
	_, cmd = m.updateGate(tea.KeyPressMsg{Code: 'a'})
	if cmd == nil {
		t.Fatal("missing abort")
	}
	if !cmd().(SandboxPolicyActionMsg).Abort {
		t.Fatal("wrong abort")
	}
	m, _ = m.handleEngineEvent(engine.RunFinished{RunID: "r", Failed: true})
	if _, cmd := m.updateGate(tea.KeyPressMsg{Code: 'r'}); cmd != nil {
		t.Fatal("settled run offered recheck")
	}
	t.Log("policy gate precedes recovery; recheck/abort carry run+epoch; stale/foreign grants ignored; resize and drafts preserved")
}
