package review

import (
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"jig/internal/review"
	"jig/internal/tui/shared"
)

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.resize()
		return m, nil
	}
	if m.CapturesText() {
		return m.updateEditor(msg)
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	m.error = ""
	if keybind.Matches(k, m.keys.Cancel) {
		if m.mode == ModeBrowse {
			return m, nil
		}
		m.mode = ModeBrowse
		m.rebuildComposer()
		return m, draftCmd(m)
	}
	switch m.mode {
	case ModeSelectRange:
		return m.updateSelection(k)
	case ModeSummary:
		return m.updateSummary(k)
	}
	if keybind.Matches(k, m.keys.Up) {
		m.move(-1)
	} else if keybind.Matches(k, m.keys.Down) {
		m.move(1)
	} else if m.diffNavigationAvailable() && keybind.Matches(k, m.keys.PrevHunk) {
		m.navigateHunk(false)
	} else if m.diffNavigationAvailable() && keybind.Matches(k, m.keys.NextHunk) {
		m.navigateHunk(true)
	} else if m.diffNavigationAvailable() && keybind.Matches(k, m.keys.FoldHunk) {
		m.toggleActiveHunk()
	} else if keybind.Matches(k, m.keys.First) {
		if m.activeDocumentMode() == DocumentPreview {
			m.previewBlock = 0
			m.move(0)
		} else {
			m.cursor = m.firstVisibleSourceLine()
		}
	} else if keybind.Matches(k, m.keys.Last) {
		if m.activeDocumentMode() == DocumentPreview {
			m.previewBlock = len(m.previews[m.active].blocks) - 1
			m.move(0)
		} else {
			m.cursor = m.lastVisibleSourceLine()
		}
	} else if m.handleSourcePan(k) {
	} else if keybind.Matches(k, m.keys.PrevDoc) {
		m.changeDoc(-1)
	} else if keybind.Matches(k, m.keys.NextDoc) {
		m.changeDoc(1)
	} else if keybind.Matches(k, m.keys.NextOpen) {
		m.nextUnreviewed()
	} else if keybind.Matches(k, m.keys.Reviewed) {
		id := m.ActiveDocument().ID
		m.reviewed[id] = !m.reviewed[id]
		return m, draftCmd(m)
	} else if keybind.Matches(k, m.keys.Select) {
		if m.activeDocumentMode() == DocumentPreview {
			m.error = "preview comments use the active block; press c to comment"
			return m, nil
		}
		m.unfoldHunkForPatchLine(m.cursor)
		m.rangeEnd = m.cursor
		m.mode = ModeSelectRange
	} else if keybind.Matches(k, m.keys.Comment) {
		m.openComposer(false)
	} else if keybind.Matches(k, m.keys.Confirm) {
		if !m.openSelectedComment() {
			m.error = "no comment on the active selection"
		}
	} else if keybind.Matches(k, m.keys.Summary) {
		m.mode = ModeSummary
		m.summary.Focus()
	} else if keybind.Matches(k, m.keys.ToggleMode) {
		m.toggleDocumentMode()
	} else if keybind.Matches(k, m.keys.Edit) {
		if m.activeComment != "" {
			m.openComposer(true)
		}
	} else if keybind.Matches(k, m.keys.Delete) {
		m.deleteActive()
		return m, draftCmd(m)
	} else if keybind.Matches(k, m.keys.NextComment) {
		m.nextComment(string(k.Text) == "N")
	}
	return m, nil
}

func (m Model) updateEditor(msg tea.Msg) (Model, tea.Cmd) {
	k, isKey := msg.(tea.KeyPressMsg)
	if m.mode == ModeSummary && isKey {
		if len(k.Text) == 1 && k.Text[0] >= '0' && k.Text[0] <= '9' {
			return m.updateSummary(k)
		}
	}
	if isKey && keybind.Matches(k, m.keys.Cancel) {
		m.mode = ModeBrowse
		m.rebuildComposer()
		return m, draftCmd(m)
	}
	if isKey && keybind.Matches(k, m.keys.NextKind) && (m.mode == ModeComposeComment || m.mode == ModeEditComment) {
		m.commentKind = nextKind(m.commentKind)
		return m, nil
	}
	if isKey && keybind.Matches(k, m.keys.Confirm) {
		if m.mode == ModeSummary {
			return m.submitReview()
		}
		body := strings.TrimSpace(m.composer.Value())
		if body == "" {
			m.error = "comment body is required"
			return m, nil
		}
		if m.commentKind == review.KindSuggestion && strings.TrimSpace(m.replacement.Value()) == "" {
			m.error = "suggestions require replacement text"
			return m, nil
		}
		start, end := m.cursor, m.rangeEnd
		if start > end {
			start, end = end, start
		}
		id := m.editing
		if id == "" {
			id = fmt.Sprintf("C%03d", m.nextID)
			m.nextID++
		}
		c := review.Comment{ID: id, Kind: m.commentKind, Anchor: m.docs[m.active].anchor(m.docs[m.active].meta.SHA256, start, end), Body: body, Replacement: m.replacement.Value()}
		replaced := false
		for i := range m.comments {
			if m.comments[i].ID == id {
				m.comments[i] = c
				replaced = true
			}
		}
		if !replaced {
			m.comments = append(m.comments, c)
		}
		m.activeComment = id
		m.mode = ModeBrowse
		m.rebuildComposer()
		return m, draftCmd(m)
	}
	var cmd tea.Cmd
	switch m.mode {
	case ModeSummary:
		m.summary, cmd = m.summary.Update(msg)
	case ModeComposeComment, ModeEditComment:
		m.composer, cmd = m.composer.Update(msg)
		if m.commentKind == review.KindSuggestion {
			var c2 tea.Cmd
			m.replacement, c2 = m.replacement.Update(msg)
			cmd = tea.Batch(cmd, c2)
		}
	}
	return m, cmd
}
func (m Model) updateSelection(k tea.KeyPressMsg) (Model, tea.Cmd) {
	if keybind.Matches(k, m.keys.Cancel) {
		m.mode = ModeBrowse
		return m, nil
	}
	if m.diffNavigationAvailable() && keybind.Matches(k, m.keys.FoldHunk) {
		m.toggleActiveHunk()
	} else if keybind.Matches(k, m.keys.Up) {
		m.move(-1)
	} else if keybind.Matches(k, m.keys.Down) {
		m.move(1)
	} else if m.handleSourcePan(k) {
	} else if keybind.Matches(k, m.keys.Comment) {
		m.openComposer(false)
	} else if keybind.Matches(k, m.keys.Confirm) {
		if !m.openSelectedComment() {
			m.error = "no comment on the active selection"
		}
	}
	return m, nil
}

func (m *Model) handleSourcePan(k tea.KeyPressMsg) bool {
	if m.activeDocumentMode() != DocumentSource || m.active < 0 || m.active >= len(m.sourceXOffsets) {
		return false
	}
	switch {
	case keybind.Matches(k, m.keys.PanLeft):
		m.sourceXOffsets[m.active] = max(0, m.sourceXOffsets[m.active]-4)
	case keybind.Matches(k, m.keys.PanRight):
		m.sourceXOffsets[m.active] += 4
		m.clampSourceOffset()
	case keybind.Matches(k, m.keys.PanHome):
		m.sourceXOffsets[m.active] = 0
	default:
		return false
	}
	return true
}

func (m Model) updateSummary(k tea.KeyPressMsg) (Model, tea.Cmd) {
	if keybind.Matches(k, m.keys.Cancel) {
		m.mode = ModeBrowse
		m.summary.Blur()
		return m, nil
	}
	if keybind.Matches(k, m.keys.Confirm) {
		return m.submitReview()
	}
	if len(k.Text) == 1 && k.Text[0] >= '0' && k.Text[0] <= '9' {
		idx := int(k.Text[0] - '1')
		if idx >= 0 && idx < len(m.choices) {
			m.verdict = m.choices[idx]
		} else {
			m.verdict = k.Text
		}
	}
	return m, nil
}

func (m Model) submitReview() (Model, tea.Cmd) {
	if strings.TrimSpace(m.verdict) == "" {
		m.error = "choose a decision"
		return m, nil
	}
	remaining := len(m.docs) - m.reviewedDocumentCount()
	if remaining > 0 {
		m.error = fmt.Sprintf("acknowledge %d remaining document(s) before submitting", remaining)
		return m, nil
	}
	m.mode = ModeBrowse
	m.summary.Blur()
	return m, submitCmd(m)
}
func (m *Model) move(delta int) {
	if m.activeDocumentMode() == DocumentPreview && len(m.previews[m.active].blocks) > 0 {
		m.previewBlock += delta
		if m.previewBlock < 0 {
			m.previewBlock = 0
		}
		if m.previewBlock >= len(m.previews[m.active].blocks) {
			m.previewBlock = len(m.previews[m.active].blocks) - 1
		}
		m.cursor = m.previews[m.active].blocks[m.previewBlock].startLine
		m.rangeEnd = m.previews[m.active].blocks[m.previewBlock].endLine
		return
	}
	if delta == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	steps := delta
	if steps < 0 {
		steps = -steps
	}
	for range steps {
		next := m.cursor + step
		if next < 1 || next > len(m.docs[m.active].lines) {
			return
		}
		m.cursor = next
		for m.sourceLineHidden(m.cursor) {
			next = m.cursor + step
			if next < 1 || next > len(m.docs[m.active].lines) {
				return
			}
			m.cursor = next
		}
	}
}
func (m *Model) changeDoc(delta int) {
	if len(m.docs) == 0 {
		return
	}
	m.activateDocument((m.active + delta + len(m.docs)) % len(m.docs))
}

func (m *Model) activateDocument(index int) {
	m.active = index
	m.cursor = 1
	m.previewBlock = 0
	m.rangeEnd = 0
	m.activeComment = ""
	m.ensureVisibleSourceCursor()
	if m.activeDocumentMode() == DocumentPreview && len(m.previews[m.active].blocks) > 0 {
		block := m.previews[m.active].blocks[0]
		m.cursor, m.rangeEnd = block.startLine, block.endLine
	}
}

func (m *Model) toggleDocumentMode() {
	if m.activeDocumentMode() == DocumentSource {
		if m.docs[m.active].meta.Format != "markdown" {
			m.error = "preview is available for Markdown"
			return
		}
		preview := &m.previews[m.active]
		if preview.parseErr != nil || len(preview.blocks) == 0 {
			if preview.parseErr == nil {
				preview.parseErr = fmt.Errorf("markdown contains no source-addressable blocks")
			}
			m.error = "preview unavailable: " + preview.parseErr.Error()
			return
		}
		m.documentModes[m.active] = DocumentPreview
		m.previewBlock = m.blockForLine(m.cursor)
		block := preview.blocks[m.previewBlock]
		m.cursor, m.rangeEnd = block.startLine, block.endLine
		return
	}
	m.documentModes[m.active] = DocumentSource
	if len(m.previews[m.active].blocks) > 0 {
		m.cursor = m.previews[m.active].blocks[m.previewBlock].startLine
		m.rangeEnd = m.cursor
	}
}

func (m *Model) blockForLine(line int) int {
	for i, block := range m.previews[m.active].blocks {
		if line >= block.startLine && line <= block.endLine {
			return i
		}
		if line < block.startLine {
			return max(i-1, 0)
		}
	}
	return max(len(m.previews[m.active].blocks)-1, 0)
}
func (m *Model) nextUnreviewed() {
	for i := 1; i <= len(m.docs); i++ {
		idx := (m.active + i) % len(m.docs)
		if !m.reviewed[m.docs[idx].meta.ID] {
			m.activateDocument(idx)
			return
		}
	}
}
func (m *Model) openComposer(edit bool) {
	m.mode = ModeComposeComment
	m.editing = ""
	if edit {
		m.mode = ModeEditComment
		for _, c := range m.comments {
			if c.ID == m.activeComment {
				m.editing = c.ID
				m.commentKind = c.Kind
				m.cursor = c.Anchor.StartLine
				m.rangeEnd = c.Anchor.EndLine
				m.composer.SetValue(c.Body)
				m.replacement.SetValue(c.Replacement)
				break
			}
		}
	}
	if m.rangeEnd < 1 {
		m.rangeEnd = m.cursor
	}
	if m.activeDocumentMode() == DocumentPreview && len(m.previews[m.active].blocks) > 0 {
		block := m.previews[m.active].blocks[m.previewBlock]
		m.cursor, m.rangeEnd = block.startLine, block.endLine
	}
	m.composer.Focus()
}

func (m *Model) openSelectedComment() bool {
	start, end := m.cursor, m.cursor
	if m.mode == ModeSelectRange || m.activeDocumentMode() == DocumentPreview {
		start, end = min(m.cursor, m.rangeEnd), max(m.cursor, m.rangeEnd)
	}

	selected := -1
	for i, c := range m.comments {
		if c.Anchor.DocumentID != m.ActiveDocument().ID || c.Anchor.StartLine > end || c.Anchor.EndLine < start {
			continue
		}
		if c.ID == m.activeComment {
			selected = i
			break
		}
		if selected < 0 || c.Anchor.StartLine == start && c.Anchor.EndLine == end {
			selected = i
		}
	}
	if selected < 0 {
		return false
	}

	m.activeComment = m.comments[selected].ID
	m.openComposer(true)
	return true
}
func (m *Model) deleteActive() {
	for i, c := range m.comments {
		if c.ID == m.activeComment {
			m.comments = append(m.comments[:i], m.comments[i+1:]...)
			m.activeComment = ""
			return
		}
	}
}
func (m *Model) nextComment(reverse bool) {
	documentID := m.ActiveDocument().ID
	indices := make([]int, 0, len(m.comments))
	for i, c := range m.comments {
		if c.Anchor.DocumentID == documentID {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return
	}
	selected := -1
	for pos, index := range indices {
		if m.comments[index].ID == m.activeComment {
			selected = pos
			break
		}
	}
	if reverse {
		if selected < 0 {
			selected = 0
		}
		selected = (selected - 1 + len(indices)) % len(indices)
	} else {
		selected = (selected + 1) % len(indices)
	}
	c := m.comments[indices[selected]]
	m.activeComment = c.ID
	m.unfoldHunkForPatchLine(c.Anchor.StartLine)
	m.cursor, m.rangeEnd = c.Anchor.StartLine, c.Anchor.EndLine
	if m.activeDocumentMode() == DocumentPreview {
		m.previewBlock = m.blockForLine(c.Anchor.StartLine)
		block := m.previews[m.active].blocks[m.previewBlock]
		m.cursor, m.rangeEnd = block.startLine, block.endLine
	}
}
func (m *Model) resize() {
	if m.width > 0 {
		hFrame, _ := shared.PanelFrame()
		summaryWidth := max(1, max(42, m.width-2)-hFrame)
		m.summary.SetWidth(summaryWidth)
		m.resizeCommentEditors()
	}
	m.clampSourceOffset()
}

func (m *Model) resizeCommentEditors() {
	if m.width <= 0 {
		return
	}
	innerWidth := max(1, commentModalWidth(m.width)-shared.Theme.Review.CommentModal.GetHorizontalFrameSize())
	m.composer.SetWidth(innerWidth)
	m.replacement.SetWidth(innerWidth)
}
func nextKind(k review.Kind) review.Kind {
	kinds := []review.Kind{review.KindNote, review.KindQuestion, review.KindConcern, review.KindBlocker, review.KindSuggestion, review.KindPraise}
	for i, v := range kinds {
		if v == k {
			return kinds[(i+1)%len(kinds)]
		}
	}
	return kinds[0]
}
func hasComment(cs []review.Comment, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}

func nextCommentID(cs []review.Comment) string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("C%03d", n)
		if !hasComment(cs, id) {
			return id
		}
	}
}
