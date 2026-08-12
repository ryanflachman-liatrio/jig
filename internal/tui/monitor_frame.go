package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
)

// monitorFrameInterval is the repaint cadence of the frame loop. It is fast
// enough that streaming text and the elapsed-time clock feel live (~10 fps) yet
// coarse enough that a burst of per-chunk StepOutput deltas collapses to a
// handful of renders instead of one full re-render per delta.
const monitorFrameInterval = 100 * time.Millisecond

// monitorTickMsg drives the frame loop: the live elapsed-time clock and the
// coalesced panel repaint. It carries the tick time only for provenance; the
// duration column reads wall-clock time when it renders.
type monitorTickMsg time.Time

// monitorTickCmd schedules the next frame. The loop re-arms itself from the
// monitorTickMsg handler while a step runs or a panel is dirty, then falls silent.
func monitorTickCmd() tea.Cmd {
	return tea.Tick(monitorFrameInterval, func(t time.Time) tea.Msg {
		return monitorTickMsg(t)
	})
}

// flushDirty repaints whichever panels are marked dirty and clears their flags.
// It no-ops before the first resize (no viewport yet — resize repaints itself),
// so the dirty flags survive until there is something to render into.
func (m *monitorModel) flushDirty() {
	if !m.ready {
		return
	}
	if m.dirtyList {
		m.vp.SetContent(m.listBody())
		m.dirtyList = false
	}
	if m.dirtyChat {
		m.chatVP.SetContent(m.chatBody())
		if m.chatAutoScroll {
			m.chatVP.GotoBottom()
		}
		m.dirtyChat = false
	}
}

// ensureFrame schedules a frame if one is not already in flight and there is
// work to do (a dirty panel or a running clock). It is the single entry point
// that arms the loop, so concurrent events never stack duplicate tickers.
func (m *monitorModel) ensureFrame() tea.Cmd {
	if m.ticking {
		return nil
	}
	if m.dirtyList || m.dirtyChat || m.anyRunning() {
		m.ticking = true
		return monitorTickCmd()
	}
	return nil
}

// eventAffectsChat reports whether an engine event changes what the Transcript
// panel renders for the currently-visible step. High-frequency step-scoped
// events (streaming output, tool calls, liveness, status) matter only when they
// belong to chatStep, so a parallel step's stream does not trigger the expensive
// glamour re-render. Everything else is low-frequency and conservatively repaints.
func (m monitorModel) eventAffectsChat(e engine.Event) bool {
	switch ev := e.(type) {
	case engine.StepStatus:
		return ev.StepID == m.chatStep
	case engine.StepOutput:
		return ev.StepID == m.chatStep
	case engine.StepToolCall:
		return ev.StepID == m.chatStep
	case engine.StepMessage:
		return ev.StepID == m.chatStep
	default:
		return true
	}
}

// anyRunning reports whether at least one step is currently executing — the
// condition under which the frame loop keeps advancing the live clock.
func (m monitorModel) anyRunning() bool {
	for _, s := range m.steps {
		if s.status == step.StatusRunning {
			return true
		}
	}
	return false
}
