package review

import (
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"jig/internal/review"
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
	} else if keybind.Matches(k, m.keys.First) {
		m.cursor = 1
	} else if keybind.Matches(k, m.keys.Last) {
		m.cursor = len(m.docs[m.active].lines)
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
		m.rangeEnd = m.cursor
		m.mode = ModeSelectRange
	} else if keybind.Matches(k, m.keys.Comment) {
		m.openComposer(false)
	} else if keybind.Matches(k, m.keys.Summary) {
		m.mode = ModeSummary
		m.summary.Focus()
	} else if keybind.Matches(k, m.keys.ToggleMode) {
		m.documentMode = (m.documentMode + 1) % 2
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
			if strings.TrimSpace(m.verdict) == "" {
				m.error = "choose a verdict"
				return m, nil
			}
			m.mode = ModeBrowse
			return m, submitCmd(m)
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
	if keybind.Matches(k, m.keys.Up) {
		m.move(-1)
	} else if keybind.Matches(k, m.keys.Down) {
		m.move(1)
	} else if keybind.Matches(k, m.keys.Comment) || keybind.Matches(k, m.keys.Confirm) {
		m.openComposer(false)
	}
	return m, nil
}

func (m Model) updateSummary(k tea.KeyPressMsg) (Model, tea.Cmd) {
	if keybind.Matches(k, m.keys.Cancel) {
		m.mode = ModeBrowse
		m.summary.Blur()
		return m, nil
	}
	if keybind.Matches(k, m.keys.Confirm) {
		if strings.TrimSpace(m.verdict) == "" {
			m.error = "choose a verdict"
			return m, nil
		}
		m.mode = ModeBrowse
		m.summary.Blur()
		return m, submitCmd(m)
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
func (m *Model) move(delta int) { m.cursor += delta; m.clampCursor() }
func (m *Model) changeDoc(delta int) {
	if len(m.docs) == 0 {
		return
	}
	m.active = (m.active + delta + len(m.docs)) % len(m.docs)
	m.cursor = 1
	m.rangeEnd = 0
	m.activeComment = ""
}
func (m *Model) nextUnreviewed() {
	for i := 1; i <= len(m.docs); i++ {
		idx := (m.active + i) % len(m.docs)
		if !m.reviewed[m.docs[idx].meta.ID] {
			m.active = idx
			m.cursor = 1
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
	m.composer.Focus()
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
	if len(m.comments) == 0 {
		return
	}
	for _, c := range m.comments {
		if c.Anchor.DocumentID == m.ActiveDocument().ID && ((reverse && c.Anchor.StartLine < m.cursor) || (!reverse && c.Anchor.StartLine > m.cursor)) {
			m.activeComment = c.ID
			m.cursor = c.Anchor.StartLine
			return
		}
	}
}
func (m *Model) resize() {
	if m.width > 0 {
		m.summary.SetWidth(m.width)
		m.composer.SetWidth(m.width)
		m.replacement.SetWidth(m.width)
	}
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
