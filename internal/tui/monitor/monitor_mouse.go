package monitor

import (
	tea "charm.land/bubbletea/v2"

	"jig/internal/tui/shared"
)

const mouseScrollRows = 3

type mouseRect struct{ x, y, width, height int }

func (r mouseRect) contains(x, y int) bool {
	return r.width > 0 && r.height > 0 && x >= r.x && y >= r.y && x < r.x+r.width && y < r.y+r.height
}

type monitorMousePanels struct {
	steps, transcript               mouseRect
	stepsVisible, transcriptVisible bool
}

// mousePanels derives hit rectangles from the same split, vertical budget,
// and shared panel frame used by resize and View.
func (m Model) mousePanels() monitorMousePanels {
	if !m.ready || m.reviewOpen || m.width <= 0 || m.height <= 0 {
		return monitorMousePanels{}
	}
	layout := m.verticalLayout()
	hFrame, vFrame := shared.PanelFrame()
	ox, oy := shared.PanelContentOrigin()
	innerH := layout.panelH - vFrame
	if innerH <= 0 {
		return monitorMousePanels{}
	}
	stepsW, transcriptW, narrow := panelSplit(m.width)
	if narrow {
		r := mouseRect{x: ox, y: oy, width: m.width - hFrame, height: innerH}
		if m.focus == focusTranscript {
			return monitorMousePanels{transcript: r, transcriptVisible: r.width > 0}
		}
		return monitorMousePanels{steps: r, stepsVisible: r.width > 0}
	}
	steps := mouseRect{x: ox, y: oy, width: stepsW - hFrame, height: innerH}
	transcript := mouseRect{x: stepsW + ox, y: oy, width: transcriptW - hFrame, height: innerH}
	return monitorMousePanels{steps: steps, transcript: transcript, stepsVisible: steps.width > 0, transcriptVisible: transcript.width > 0}
}

func (m Model) mouseExcluded() bool {
	return m.helpOpen || m.showDiagnostics || m.reviewOpen || m.searchOpen || m.textareaActive() || (m.focus == focusGate && m.hasGate())
}

func (m *Model) selectVisibleRow(idx int) bool {
	rows := m.visibleRows()
	if idx < 0 || idx >= len(rows) {
		return false
	}
	m.cursor = idx
	m.ensureCursorVisible()
	m.reloadTranscript()
	m.refreshPanels()
	return true
}

func (m Model) rowAtViewportLine(line int) (int, bool) {
	for i, rng := range m.stepRowRanges() {
		if line >= rng.start && line < rng.end {
			return i, true
		}
	}
	return 0, false
}

func (m Model) updateMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.mouseExcluded() {
		return m, nil
	}
	mouse := msg.Mouse()
	if mouse.Mod != 0 || mouse.X < 0 || mouse.Y < 0 || mouse.X >= m.width || mouse.Y >= m.height {
		return m, nil
	}
	panels := m.mousePanels()
	switch event := msg.(type) {
	case tea.MouseClickMsg:
		if event.Button != tea.MouseLeft {
			return m, nil
		}
		if panels.stepsVisible && panels.steps.contains(mouse.X, mouse.Y) {
			line := m.vp.YOffset() + mouse.Y - panels.steps.y
			if idx, ok := m.rowAtViewportLine(line); ok && m.selectVisibleRow(idx) {
				m.focus = focusSteps
				m.refreshPanels()
			}
		} else if panels.transcriptVisible && panels.transcript.contains(mouse.X, mouse.Y) {
			m.focus = focusTranscript
		}
	case tea.MouseWheelMsg:
		delta := 0
		switch event.Button {
		case tea.MouseWheelUp:
			delta = -mouseScrollRows
		case tea.MouseWheelDown:
			delta = mouseScrollRows
		default:
			return m, nil
		}
		if panels.stepsVisible && panels.steps.contains(mouse.X, mouse.Y) {
			rows := m.visibleRows()
			if len(rows) == 0 {
				return m, nil
			}
			idx := min(max(m.cursor+delta, 0), len(rows)-1)
			if idx != m.cursor {
				m.selectVisibleRow(idx)
			}
		} else if panels.transcriptVisible && panels.transcript.contains(mouse.X, mouse.Y) {
			m.scrollTranscript(delta)
		}
	}
	return m, nil
}
