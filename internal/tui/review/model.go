package review

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	domain "jig/internal/review"
	"jig/internal/tui/shared"
)

type Mode uint8

const (
	ModeBrowse Mode = iota
	ModeSelectRange
	ModeComposeComment
	ModeEditComment
	ModeSummary
)

type DocumentMode uint8

const (
	DocumentSource DocumentMode = iota
	DocumentPreview
)

type DraftChangedMsg struct{ Draft domain.Draft }
type SubmissionMsg struct{ Submission domain.Submission }

type Model struct {
	session                        domain.Session
	docs                           []document
	active, cursor, rangeEnd       int
	mode                           Mode
	documentModes                  []DocumentMode
	reviewed                       map[string]bool
	comments                       []domain.Comment
	nextID                         int
	activeComment                  string
	commentKind                    domain.Kind
	composer, replacement, summary textarea.Model
	editing                        string
	verdict                        string
	choices                        []string
	width, height                  int
	keys                           keyMap
	error                          string
	previews                       []previewState
	sources                        []sourcePresentation
	sourceXOffsets                 []int
	previewBlock                   int
	// discardConfirm arms a one-shot y/n prompt when Esc would abandon dirty
	// compose text (Phase 0.4 / A6).
	discardConfirm bool
}

func New(session domain.Session) (Model, error) { return NewWithDraft(session, domain.Draft{}) }

func NewWithDraft(session domain.Session, draft domain.Draft) (Model, error) {
	docs, err := loadDocuments(session)
	if err != nil {
		return Model{}, err
	}
	m := Model{session: session, docs: docs, reviewed: map[string]bool{}, mode: ModeBrowse, commentKind: domain.KindNote, keys: defaultKeyMap(), nextID: 1}
	m.previews = make([]previewState, len(docs))
	m.sources = make([]sourcePresentation, len(docs))
	m.sourceXOffsets = make([]int, len(docs))
	m.documentModes = make([]DocumentMode, len(docs))
	for i, d := range docs {
		m.sources[i] = buildSourcePresentation(d)
		if d.meta.Format != "markdown" {
			continue
		}
		m.previews[i] = buildPreview(d.meta.Content)
		if m.previews[i].parseErr == nil && len(m.previews[i].blocks) > 0 {
			m.documentModes[i] = DocumentPreview
		}
	}
	for _, id := range draft.Reviewed {
		m.reviewed[id] = true
	}
	m.comments = append([]domain.Comment(nil), draft.Comments...)
	m.nextID += len(m.comments)
	m.active = indexOfDoc(docs, draft.View.ActiveDocumentID)
	if m.active < 0 {
		m.active = 0
	}
	m.cursor = draft.View.CursorLine
	if m.cursor < 1 {
		m.cursor = 1
	}
	m.clampCursor()
	m.rangeEnd = draft.View.RangeEndLine
	if m.activeDocumentMode() == DocumentPreview {
		m.previewBlock = m.blockForLine(m.cursor)
		block := m.previews[m.active].blocks[m.previewBlock]
		m.cursor, m.rangeEnd = block.startLine, block.endLine
	}
	m.activeComment = draft.View.ActiveCommentID
	m.commentKind = draft.View.CommentKind
	if m.commentKind == "" {
		m.commentKind = domain.KindNote
	}
	m.summary = shared.NewInputTextarea("Optional review summary", 0, 3)
	m.summary.SetValue(draft.Summary)
	m.summary.Blur()
	m.rebuildComposer()
	return m, nil
}

func indexOfDoc(docs []document, id string) int {
	for i, d := range docs {
		if d.meta.ID == id {
			return i
		}
	}
	return -1
}
func (m *Model) clampCursor() {
	if len(m.docs) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 1 {
		m.cursor = 1
	}
	if m.cursor > len(m.docs[m.active].lines) {
		m.cursor = len(m.docs[m.active].lines)
	}
	m.ensureVisibleSourceCursor()
}
func (m *Model) rebuildComposer() {
	m.composer = shared.NewInputTextarea("Comment", 0, 4)
	m.replacement = shared.NewInputTextarea("Replacement (suggestions only)", 0, 3)
	m.composer.Blur()
	m.replacement.Blur()
	m.resizeCommentEditors()
}

func (m Model) Session() domain.Session { return m.session }
func (m Model) Mode() Mode              { return m.mode }
func (m Model) ActiveDocument() domain.Document {
	if len(m.docs) == 0 {
		return domain.Document{}
	}
	return m.docs[m.active].meta
}
func (m Model) CursorLine() int { return m.cursor }
func (m Model) Documents() []domain.Document {
	out := make([]domain.Document, len(m.docs))
	for i, d := range m.docs {
		out[i] = d.meta
	}
	return out
}
func (m Model) Comments() []domain.Comment { return append([]domain.Comment(nil), m.comments...) }
func (m Model) CapturesText() bool {
	return m.mode == ModeComposeComment || m.mode == ModeEditComment || m.mode == ModeSummary
}

// HasDirtyCompose reports unsaved text in the comment or summary editors.
func (m Model) HasDirtyCompose() bool {
	switch m.mode {
	case ModeComposeComment, ModeEditComment:
		return strings.TrimSpace(m.composer.Value()) != "" ||
			(m.commentKind == domain.KindSuggestion && strings.TrimSpace(m.replacement.Value()) != "")
	case ModeSummary:
		return strings.TrimSpace(m.summary.Value()) != ""
	default:
		return false
	}
}

// DiscardCompose clears compose/edit state and returns to browse without saving.
func (m Model) DiscardCompose() Model {
	m.mode = ModeBrowse
	m.rebuildComposer()
	m.discardConfirm = false
	return m
}

func (m Model) PreviewBlock() int { return m.previewBlock }

func (m Model) activeDocumentMode() DocumentMode {
	if m.active < 0 || m.active >= len(m.documentModes) {
		return DocumentSource
	}
	return m.documentModes[m.active]
}

func (m Model) previewAvailable() bool {
	return m.active >= 0 && m.active < len(m.docs) &&
		m.docs[m.active].meta.Format == "markdown" &&
		m.previews[m.active].parseErr == nil && len(m.previews[m.active].blocks) > 0
}

func (m *Model) diffNavigationAvailable() bool {
	diff := m.parsedDiffPresentation()
	return m.activeDocumentMode() == DocumentSource && diff != nil && len(diff.hunks) > 0
}

func (m *Model) hunkForPatchLine(line int) *diffHunk {
	diff := m.parsedDiffPresentation()
	if diff == nil {
		return nil
	}
	for i := range diff.hunks {
		hunk := &diff.hunks[i]
		if line >= hunk.startPatchLine && line <= hunk.endPatchLine {
			return hunk
		}
	}
	return nil
}

func (m *Model) hunkStartingAtPatchLine(line int) *diffHunk {
	diff := m.parsedDiffPresentation()
	if diff == nil {
		return nil
	}
	for i := range diff.hunks {
		if diff.hunks[i].startPatchLine == line {
			return &diff.hunks[i]
		}
	}
	return nil
}

func (m *Model) sourceLineHidden(line int) bool {
	diff := m.parsedDiffPresentation()
	if diff == nil {
		return false
	}
	hunk := m.hunkForPatchLine(line)
	if hunk == nil || !isVisibleDiffRow(diff.rows[line-1]) {
		return true
	}
	return hunk.startPatchLine != line && diff.folded[hunk.ordinal]
}

func (m *Model) ensureVisibleSourceCursor() {
	if m.activeDocumentMode() != DocumentSource || !m.sourceLineHidden(m.cursor) {
		return
	}
	for line := m.cursor + 1; line <= len(m.docs[m.active].lines); line++ {
		if !m.sourceLineHidden(line) {
			m.cursor = line
			return
		}
	}
	for line := m.cursor - 1; line >= 1; line-- {
		if !m.sourceLineHidden(line) {
			m.cursor = line
			return
		}
	}
}

func (m *Model) firstVisibleSourceLine() int {
	for line := 1; line <= len(m.docs[m.active].lines); line++ {
		if !m.sourceLineHidden(line) {
			return line
		}
	}
	return 1
}

func (m *Model) lastVisibleSourceLine() int {
	for line := len(m.docs[m.active].lines); line >= 1; line-- {
		if !m.sourceLineHidden(line) {
			return line
		}
	}
	return len(m.docs[m.active].lines)
}

func (m *Model) unfoldHunkForPatchLine(line int) {
	hunk := m.hunkForPatchLine(line)
	if hunk == nil {
		return
	}
	delete(m.parsedDiffPresentation().folded, hunk.ordinal)
}

func (m *Model) navigateHunk(next bool) {
	diff := m.parsedDiffPresentation()
	if diff == nil || len(diff.hunks) == 0 {
		return
	}
	current := -1
	for i, hunk := range diff.hunks {
		if m.cursor >= hunk.startPatchLine && m.cursor <= hunk.endPatchLine {
			current = i
			break
		}
	}
	target := 0
	if current >= 0 {
		if next {
			target = (current + 1) % len(diff.hunks)
		} else {
			target = (current - 1 + len(diff.hunks)) % len(diff.hunks)
		}
	} else if next {
		for i, hunk := range diff.hunks {
			if hunk.startPatchLine > m.cursor {
				target = i
				break
			}
		}
	} else {
		target = len(diff.hunks) - 1
		for i := len(diff.hunks) - 1; i >= 0; i-- {
			if diff.hunks[i].startPatchLine < m.cursor {
				target = i
				break
			}
		}
	}
	m.cursor = diff.hunks[target].startPatchLine
	m.rangeEnd = m.cursor
}

func (m *Model) toggleActiveHunk() {
	hunk := m.hunkForPatchLine(m.cursor)
	if hunk == nil {
		return
	}
	diff := m.parsedDiffPresentation()
	if diff.folded[hunk.ordinal] {
		delete(diff.folded, hunk.ordinal)
	} else {
		diff.folded[hunk.ordinal] = true
	}
	m.cursor, m.rangeEnd = hunk.startPatchLine, hunk.startPatchLine
	if m.mode == ModeSelectRange {
		m.mode = ModeBrowse
	}
}

// SetVerdict is used by a parent surface when verdict choices are rendered as
// buttons or a compact selector rather than raw digit key presses.
func (m *Model) SetVerdict(verdict string) { m.verdict = verdict }

func (m *Model) SetChoices(choices []string) { m.choices = append([]string(nil), choices...) }

func (m Model) Verdict() string { return m.verdict }

func (m Model) Reviewed(documentID string) bool { return m.reviewed[documentID] }

func (m Model) reviewedDocumentCount() int {
	count := 0
	for _, d := range m.docs {
		if m.reviewed[d.meta.ID] {
			count++
		}
	}
	return count
}

func (m Model) Draft() domain.Draft {
	docs := m.Documents()
	reviewed := make([]string, 0, len(m.reviewed))
	for _, d := range docs {
		if m.reviewed[d.ID] {
			reviewed = append(reviewed, d.ID)
		}
	}
	sort.Strings(reviewed)
	return domain.Draft{SchemaVersion: 1, StepID: m.session.StepID, RoundID: m.session.RoundID, Documents: docs, Reviewed: reviewed, Comments: append([]domain.Comment(nil), m.comments...), Summary: m.summary.Value(), View: domain.ViewState{ActiveDocumentID: m.ActiveDocument().ID, Mode: fmt.Sprint(m.mode), CursorLine: m.cursor, RangeEndLine: m.rangeEnd, ActiveCommentID: m.activeComment, CommentKind: m.commentKind}}
}

func (m Model) Submission(verdict string) domain.Submission {
	docs := m.Documents()
	records := make([]domain.DocumentRecord, len(docs))
	for i, d := range docs {
		records[i] = d.Record()
	}
	reviewed := make([]string, 0, len(m.reviewed))
	for _, d := range docs {
		if m.reviewed[d.ID] {
			reviewed = append(reviewed, d.ID)
		}
	}
	return domain.Submission{SchemaVersion: 1, StepID: m.session.StepID, RoundID: m.session.RoundID, Verdict: verdict, Documents: records, Reviewed: reviewed, Comments: m.Comments(), Summary: m.summary.Value()}
}

func draftCmd(m Model) tea.Cmd {
	d := m.Draft()
	return func() tea.Msg { return DraftChangedMsg{Draft: d} }
}
func submitCmd(m Model) tea.Cmd {
	return func() tea.Msg { return SubmissionMsg{Submission: m.Submission(m.verdict)} }
}
