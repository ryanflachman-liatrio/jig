package monitor

import (
	"time"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// gateBellCooldown bounds how often the gate bell can ring. Fan-out steps can
// open and drain gates in quick succession; without a floor the operator would
// hear a stutter of beeps for what is one "come look" moment.
const gateBellCooldown = 5 * time.Second

// bellClock is the bell's source of "now", swapped in tests to exercise the
// cooldown without sleeping.
var bellClock = time.Now

// bellCmd emits BEL through the Bubble Tea renderer rather than writing to
// stdout directly, so the byte is sequenced with frame output instead of
// racing it and tearing the screen.
func bellCmd() tea.Cmd {
	return tea.Raw("\a")
}

// withGateBell rings the bell after an update when the gate queue went from
// empty to non-empty. Checking once here, around Update, covers every enqueue
// site (engine gates and the monitor's own permission/reset gates) without
// each carrying its own copy of the rule. cmd passes through untouched when
// the bell doesn't ring.
func (m Model) withGateBell(hadGate, wasFocused bool, cmd tea.Cmd) (Model, tea.Cmd) {
	if !m.gateShouldRing(hadGate, wasFocused) {
		return m, cmd
	}
	m.lastBell = bellClock()
	return m, tea.Batch(cmd, bellCmd())
}

// toggleBell flips the gate bell for this session only; [tui] bell in
// config.toml stays the persistent default. The footer's "bell: on/off"
// label is the feedback.
func (m *Model) toggleBell() {
	m.bellEnabled = !m.bellEnabled
	m.refreshPanels()
}

// bellBinding is ToggleBell labeled with the current session state, in the
// same "name: on/off" form as the compact-tools toggle.
func (m Model) bellBinding() keybind.Binding {
	b := m.keys.ToggleBell
	if m.bellEnabled {
		b.SetHelp("B", "bell: on")
	} else {
		b.SetHelp("B", "bell: off")
	}
	return b
}
