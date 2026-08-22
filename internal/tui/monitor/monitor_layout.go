package monitor

import (
	"charm.land/bubbles/v2/viewport"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

type verticalLayout struct {
	panelH    int
	securityH int
	inputH    int
	footerH   int
}

// verticalLayout gives the compact input bar and footer priority, then lets a bounded
// security summary consume space without collapsing the main panels entirely.
func (m Model) verticalLayout() verticalLayout {
	height := max(m.height, 0)
	footerH := min(lipgloss.Height(m.footerView()), height)
	remaining := height - footerH

	inputH := min(lipgloss.Height(m.inputBarView()), remaining)
	remaining -= inputH

	// Preserve a titled panel with one content row whenever the terminal has
	// enough room; security findings remain available in findings.jsonl when the
	// inline summary has to be shortened.
	_, vFrame := shared.PanelFrame()
	minPanelH := min(vFrame+1, remaining)
	securityH := m.securityViewHeight(remaining - minPanelH)

	return verticalLayout{
		panelH:    remaining - securityH,
		securityH: securityH,
		inputH:    inputH,
		footerH:   footerH,
	}
}

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

// resize fits both panels to the current vertical budget and width split. In the
// narrow fallback (Decision 14) each panel is sized full-width, since only the
// focused one renders.
func (m *Model) resize() {
	hFrame, vFrame := shared.PanelFrame()
	layout := m.verticalLayout()
	innerH := max(layout.panelH-vFrame, 0)

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
	if m.searchOpen {
		m.searchInput.SetWidth(max(1, m.transcriptInnerW-6))
	}
	for i := range m.inputQueue {
		if m.inputQueue[i].kind == inputKindQuestion {
			m.inputQueue[i].question = m.inputQueue[i].question.Resize(
				m.gateInnerWidth(),
				m.gateBodyHeight()-gateHeaderRows,
			)
		}
	}
	m.rebuildRenderer()
	m.vp.SetContent(m.listBody())
	m.chatVP.SetContent(m.chatBody())
}

// gateInnerWidth is the content width available to a gate strip's textarea. The
// gate textarea is borderless (the contextual gate panel owns the frame, so a
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

// gateBodyHeight returns the fixed body height inside the focused gate overlay.
// It is the maximum of the two bounded per-kind natural body heights:
//
//   - textarea kinds (inputKindRequest/inputKindPrompt): gateHeaderRows + label
//     row + gateTextareaRows content rows. The textarea is borderless (the panel
//     owns the frame), so it adds no vertical frame of its own.
//   - review kind (inputKindReview, non-composing): gateHeaderRows + spacer row
//   - maxReviewChoices verdict lines + [m] affordance + diff-location hint.
//
// inputKindQuestion is the only unbounded kind; its option list scrolls within
// this height (Unit 6) and is therefore excluded from the max.
func (m Model) gateBodyHeight() int {
	taVFrame := shared.Theme.Textarea.Borderless.GetVerticalFrameSize()
	// Textarea case: contextual header + label + textarea content rows.
	textareaCaseH := gateHeaderRows + 1 + gateTextareaRows + taVFrame
	// Review case: contextual header + spacer + bounded choices + [m] + hint.
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

	zero := uint(0)
	chatStyle := shared.Theme.Markdown
	// The Lip Gloss box owns code-block spacing, so Glamour must not add its
	// default code margin outside the rounded border.
	chatStyle.CodeBlock.StyleBlock.Margin = &zero
	m.renderer = newMarkdownRenderer(chatStyle, wordWrap)

	// fileRenderer strips all glamour document framing so output file content sits
	// flush in the panel without extra blank lines or indentation:
	//   - Document.Margin/Indent/BlockPrefix/BlockSuffix: removes the 2-column left
	//     margin and the leading/trailing \n the default Document style adds.
	//   - Document.Color: removes the styled-space background fill that pads every
	//     line to word-wrap width (only visible when the terminal background
	//     contrasts with color #252); syntax highlighting via Chroma is unaffected.
	//   - CodeBlock.Margin: removes the 2-column code-block-level left indent
	//     (separate from the document margin) so code starts at column 0.
	// The leading blank line from glamour's code block top padding is stripped in
	// fileBody() via stripBlankEdges.
	fileStyle := chatStyle
	fileStyle.Document.Margin = &zero
	fileStyle.Document.Indent = &zero
	fileStyle.Document.BlockPrefix = ""
	fileStyle.Document.BlockSuffix = ""
	fileStyle.Document.Color = nil
	fileStyle.CodeBlock.StyleBlock.Margin = &zero
	m.fileRenderer = newMarkdownRenderer(fileStyle, wordWrap)

	insetWidth := wordWrap - 4 // "  ▌ " prefix added by withBar
	if insetWidth < 1 {
		insetWidth = 1
	}
	m.insetRenderer = newMarkdownRenderer(fileStyle, insetWidth)

	if m.lastTranscriptW != m.transcriptInnerW {
		m.chatRendered = make(map[blockKey]string)
		m.lastTranscriptW = m.transcriptInnerW
	}
}

func newMarkdownRenderer(style ansi.StyleConfig, wordWrap int) *glamour.TermRenderer {
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(wordWrap),
		glamour.WithChromaFormatter(shared.CodeBlockFormatter(markdownCodeWidth(style, wordWrap))),
	)
	return renderer
}

func markdownCodeWidth(style ansi.StyleConfig, wordWrap int) int {
	width := wordWrap
	if style.Document.Indent != nil {
		width -= int(*style.Document.Indent)
	}
	if style.Document.Margin != nil {
		width -= 2 * int(*style.Document.Margin)
	}
	if style.CodeBlock.Indent != nil {
		width -= int(*style.CodeBlock.Indent)
	}
	if style.CodeBlock.Margin != nil {
		width -= int(*style.CodeBlock.Margin)
	}
	return width
}

// ensureCursorVisible nudges the viewport so the selected step stays on screen
// as the cursor moves, keeping a small margin from the top and bottom edges.
// Step rows occupy stepRowLines (2) physical lines; file rows occupy 1. We sum
// variable row heights up to the cursor rather than multiplying by stepRowLines
// so that expanded file trees don't skew the scroll offset.
func (m *Model) ensureCursorVisible() {
	if !m.ready {
		return
	}
	row := listBodyHeaderLines
	for i, r := range m.visibleRows() {
		if i == m.cursor {
			break
		}
		if r.isFileRow() {
			row++
		} else {
			row += stepRowLines
		}
	}
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

func (m *Model) ensureChatCursorVisible() {
	if len(m.chatBlocks) == 0 || m.chatBlockCursor < 0 || m.chatBlockCursor >= len(m.chatBlocks) {
		return
	}
	rng, ok := m.chatLineRanges[m.chatBlocks[m.chatBlockCursor].lineKey()]
	if !ok {
		return
	}
	m.ensureTranscriptRangeVisible(rng)
}

func (m *Model) ensureTranscriptRangeVisible(rng lineRange) {
	if !m.ready || m.chatVP.Height() <= 0 {
		return
	}

	height := m.chatVP.Height()
	margin := min(2, max((height-1)/2, 0))
	top := m.chatVP.YOffset()
	bottom := top + height - 1

	// An expanded block can be taller than the viewport. Its highlighted header
	// is the actionable cursor target, so anchor that line instead of aligning
	// the block's bottom and pushing the target off-screen.
	if rng.end-rng.start+1+2*margin > height {
		if rng.start-margin < top || rng.start+margin > bottom {
			m.chatVP.SetYOffset(max(0, rng.start-margin))
		}
		return
	}

	switch {
	case rng.start-margin < top:
		m.chatVP.SetYOffset(max(0, rng.start-margin))
	case rng.end+margin > bottom:
		m.chatVP.SetYOffset(max(0, rng.end+margin-height+1))
	}
}
