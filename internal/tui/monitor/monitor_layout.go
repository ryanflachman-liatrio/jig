package monitor

import (
	"charm.land/bubbles/v2/viewport"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// panelSplit computes the two panels' outer widths for the given total width per
// Resolved Decision 11: Steps = max(32, width/3), clamped so the Transcript keeps
// an inner width of at least ~40; the Transcript takes the remainder. narrow
// reports that the terminal is too narrow for both panels to meet their minimums,
// so the caller should fall back to a single full-width focused panel.
func panelSplit(width int) (stepsW, transcriptW int, narrow bool) {
	hFrame, _ := shared.PanelFrame()
	// Both panels need at least their border frame; the Transcript additionally
	// needs transcriptMinInnerWidth inner cells. Below this the split is impossible.
	minTotal := stepsMinWidth + hFrame + transcriptMinInnerWidth
	if width < minTotal {
		return width, width, true
	}
	stepsW = width / 3
	if stepsW < stepsMinWidth {
		stepsW = stepsMinWidth
	}
	// Clamp so the Transcript keeps its minimum inner width.
	if maxSteps := width - (transcriptMinInnerWidth + hFrame); stepsW > maxSteps {
		stepsW = maxSteps
	}
	return stepsW, width - stepsW, false
}

// resize fits both panels to the width split (Resolved Decision 11) minus the
// footer and gate-strip rows. In the narrow fallback (Decision 14) each panel is
// sized full-width, since only the focused one renders.
func (m *Model) resize() {
	hFrame, vFrame := shared.PanelFrame()

	footerH := lipgloss.Height(m.footerView())
	// The gate strip is always rendered at a fixed height (Unit 2 — no layout shift
	// when input arrives or departs). Use the derived constant, not a measurement.
	gateH := m.gateBodyHeight() + vFrame
	panelH := m.height - footerH - gateH
	if panelH < 1 {
		panelH = 1
	}
	innerH := panelH - vFrame
	if innerH < 1 {
		innerH = 1
	}

	stepsOuter, transcriptOuter, narrow := panelSplit(m.width)
	m.narrow = narrow
	if narrow {
		// Only the focused panel renders, full-width; size both to the full width
		// so whichever is shown fits.
		stepsOuter, transcriptOuter = m.width, m.width
	}
	m.stepsInnerW = stepsOuter - hFrame
	if m.stepsInnerW < 1 {
		m.stepsInnerW = 1
	}
	m.transcriptInnerW = transcriptOuter - hFrame
	if m.transcriptInnerW < 1 {
		m.transcriptInnerW = 1
	}

	if !m.ready {
		m.vp = viewport.New(viewport.WithWidth(m.stepsInnerW), viewport.WithHeight(innerH))
		m.chatVP = viewport.New(viewport.WithWidth(m.transcriptInnerW), viewport.WithHeight(innerH))
		// Content is word-wrapped to each panel's inner width; horizontal
		// scrolling only ever cuts left characters off rendered lines.
		m.vp.KeyMap.Left.Unbind()
		m.vp.KeyMap.Right.Unbind()
		m.chatVP.KeyMap.Left.Unbind()
		m.chatVP.KeyMap.Right.Unbind()
		m.ready = true
	} else {
		m.vp.SetWidth(m.stepsInnerW)
		m.vp.SetHeight(innerH)
		m.chatVP.SetWidth(m.transcriptInnerW)
		m.chatVP.SetHeight(innerH)
	}
	if entry, ok := m.activeEntry(); ok &&
		(entry.kind == inputKindRequest || entry.kind == inputKindPrompt ||
			((entry.kind == inputKindReview || entry.kind == inputKindRecovery) && entry.composing)) {
		m.promptTextarea.SetWidth(m.gateInnerWidth())
	}
	m.rebuildRenderer()
	m.vp.SetContent(m.listBody())
	m.chatVP.SetContent(m.chatBody())
}

// gateInnerWidth is the content width available to a gate strip's textarea. The
// gate textarea is borderless (the "Agent input" panel owns the frame, so a
// bordered textarea would double-box), so it fills the panel's full inner width
// — no textarea frame to subtract, only the panel's own hFrame.
func (m Model) gateInnerWidth() int {
	panelHFrame, _ := shared.PanelFrame()
	w := m.width - panelHFrame - shared.Theme.Textarea.Borderless.GetHorizontalFrameSize()
	if w < 1 {
		w = 1
	}
	return w
}

// gateBodyHeight returns the fixed body height (inside the panel border, excluding
// the panel's own vFrame) reserved for the gate strip unconditionally — whether
// the queue is empty or not. It is the maximum of the two bounded per-kind
// natural body heights:
//
//   - textarea kinds (inputKindRequest/inputKindPrompt): gateHeaderRows + label
//     row + gateTextareaRows content rows. The textarea is borderless (the panel
//     owns the frame), so it adds no vertical frame of its own.
//   - review kind (inputKindReview, non-composing): gateHeaderRows + label row
//   - maxReviewChoices verdict lines + [m] affordance + diff-location hint.
//
// inputKindQuestion is the only unbounded kind; its option list scrolls within
// this height (Unit 6) and is therefore excluded from the max.
func (m Model) gateBodyHeight() int {
	taVFrame := shared.Theme.Textarea.Borderless.GetVerticalFrameSize()
	// textarea case: header + label + textarea content rows (borderless: no frame)
	textareaCaseH := gateHeaderRows + 1 + gateTextareaRows + taVFrame
	// review case: header + label + bounded choices + [m] affordance + hint (Unit 5)
	reviewCaseH := gateHeaderRows + 1 + maxReviewChoices + 1 + 1
	if textareaCaseH > reviewCaseH {
		return textareaCaseH
	}
	return reviewCaseH
}

// rebuildRenderer (re)constructs the markdown renderer for the *Transcript
// panel's* inner width — the transcript occupies only part of the terminal, so
// wrapping to the full width would overflow the panel. It invalidates the
// per-block render cache when that inner width changes: glamour bakes its
// word-wrap width in at construction, so a stale cache would wrap to the old
// width (see turn.go / viewer.go for the same house rule). A static style (not
// AutoStyle) avoids the OSC-11 stdin race documented in main.go.
func (m *Model) rebuildRenderer() {
	wordWrap := m.transcriptInnerW
	if wordWrap < 1 {
		wordWrap = 1
	}
	m.renderer, _ = glamour.NewTermRenderer(
		glamour.WithStyles(shared.Theme.Markdown),
		glamour.WithWordWrap(wordWrap),
	)
	if m.lastTranscriptW != m.transcriptInnerW {
		m.chatRendered = make(map[blockKey]string)
		m.lastTranscriptW = m.transcriptInnerW
	}
}

// ensureCursorVisible nudges the viewport so the selected step stays on screen
// as the cursor moves, keeping a small margin from the top and bottom edges. The
// step rows now start at line 0 (the panel border carries the "Steps" title), so
// the cursor index maps directly to a viewport row.
func (m *Model) ensureCursorVisible() {
	if !m.ready {
		return
	}
	row := listBodyHeaderLines + m.cursor*stepRowLines
	const margin = 2
	top := m.vp.YOffset()
	bottom := top + m.vp.Height() - 1
	switch {
	case row-margin < top:
		m.vp.SetYOffset(row - margin)
	case row+margin > bottom:
		m.vp.SetYOffset(row + margin - m.vp.Height() + 1)
	}
}
