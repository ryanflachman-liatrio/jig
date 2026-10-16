package monitor

import (
	"fmt"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// SandboxPolicyActionMsg carries the engine's block epoch so delayed commands
// cannot answer a later block. The engine owns recheck and cancellation.
type SandboxPolicyActionMsg struct {
	RunID string
	Epoch uint64
	Abort bool
}

func (m Model) updateSandboxGate(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if keybind.Matches(msg, m.keys.GateBlur) {
		m.focus = focusSteps
		return m, nil
	}
	if m.historical || m.done || m.policyBlock == nil {
		return m, nil
	}
	abort := keybind.Matches(msg, m.keys.RecoverAbort)
	recheck := keybind.Matches(msg, m.keys.RecoverRetry)
	if !abort && (!recheck || m.policyChecking) {
		return m, nil
	}
	if recheck {
		m.policyChecking = true
	}
	action := SandboxPolicyActionMsg{RunID: m.RunID, Epoch: m.policyBlock.Epoch, Abort: abort}
	return m, func() tea.Msg { return action }
}
func (m Model) sandboxGateView() string {
	_, frame := shared.PanelFrame()
	text := "  " + m.policyBlock.Reason + "\n\n  All execution is blocked. Existing human decisions remain pending.\n"
	if m.done {
		text += "\n  This run is settled."
	} else if m.historical {
		text += "\n  Return to Runs and reopen this run."
	} else {
		text += fmt.Sprintf("\n  [%s] recheck policy\n  [%s] abort run", m.keys.RecoverRetry.Help().Key, m.keys.RecoverAbort.Help().Key)
		if m.policyChecking {
			text += "\n  Checking current policy" + shared.EllipsisGlyph
		}
	}
	return shared.Panel("GATE · sandbox policy", text, m.width, m.gateBodyHeight()+frame, m.focus == focusGate)
}
