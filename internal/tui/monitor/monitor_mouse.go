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

// transcriptHit is the resolved click target for a body line in the
// Transcript panel. cursorIdx is the index into m.chatCursorTargets that
// the click resolves to; headerLine is true when the click landed on the
// first rendered line of the target's line-range (the affordance row that
// carries the ▸/▾ marker for expandable items).
type transcriptHit struct {
	cursorIdx  int
	headerLine bool
}

// transcriptItemAtLine walks m.chatCursorTargets in reverse — children of a
// compact tool group are appended after their group header, so reverse order
// returns the most specific match first — and returns the first target whose
// recorded line range contains line. Blank spacers between items, the
// page-edge banner, and file view all have no matching range and produce
// (transcriptHit{}, false).
func (m Model) transcriptItemAtLine(line int) (transcriptHit, bool) {
	for i := len(m.chatCursorTargets) - 1; i >= 0; i-- {
		target := m.chatCursorTargets[i]
		rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: target.key}]
		if !ok {
			continue
		}
		if line < rng.start || line > rng.end {
			continue
		}
		return transcriptHit{cursorIdx: i, headerLine: line == rng.start}, true
	}
	return transcriptHit{}, false
}

// itemIsExpandable reports whether a click on the item's header line should
// toggle its expansion state. It mirrors the predicate itemHasDetail already
// uses to decide whether writeTranscriptItem draws the ▸/▾ marker: a group
// header (read or compact non-read) always toggles, a tool exchange always
// toggles, and a text or reasoning row toggles only when it is oversized.
func itemIsExpandable(item transcriptItem) bool {
	switch item.kind {
	case transcriptItemToolGroup, transcriptItemReadGroup:
		return true
	}
	return itemHasDetail(item)
}

// clickTranscript is the transcript-panel branch of a primary MouseClick. It
// mirrors selectVisibleRow for the Steps panel but works against the item-
// level line-range map that itemTranscriptBody records. On a hit it moves
// the block cursor to the resolved item, pauses auto-scroll follow, and
// toggles the item's expansion state only when the click landed on the
// first line of an expandable item that is currently collapsed, or on the
// header line of an expanded item. Clicks below the header of an expanded
// item select without collapsing so a reader can click into a long expanded
// exchange without losing their place ("reader trap" rule in
// docs/plans/clickable-transcript.md).
//
// panelY is the mouse Y in panel-content coordinates
// (mouse.Y - panels.transcript.y). Blank spacer lines between items, the
// page-edge banner, and file view all resolve to no hit and leave the
// model untouched.
func (m *Model) clickTranscript(panelY int) {
	if panelY < 0 {
		return
	}
	line := m.chatVP.YOffset() + panelY
	hit, ok := m.transcriptItemAtLine(line)
	if !ok {
		// Blank spacer, page-edge banner, or file view — focus was
		// already updated by the caller. Do not repaint: an empty
		// transcript may not own its viewport content (tests set
		// synthetic content directly on chatVP), and refresh would
		// clobber that offset.
		return
	}
	m.chatItemCursor = hit.cursorIdx
	m.chatAutoScroll = false

	target := m.chatCursorTargets[hit.cursorIdx]
	item, hasItem := m.itemForCursorTarget(target)
	if !hasItem || !itemIsExpandable(item) {
		m.refreshPanels()
		return
	}
	expanded := m.chatItemExpandAll || m.chatItemExpand[item.key]
	if !expanded || hit.headerLine {
		m.chatItemExpand[item.key] = !m.chatItemExpand[item.key]
		m.rebuildTranscriptItemState(item.key)
	}
	m.refreshPanels()
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
			m.clickTranscript(mouse.Y - panels.transcript.y)
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
