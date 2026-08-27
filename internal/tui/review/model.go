package review

import (
	"fmt"
	"sort"

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
	previewBlock                   int
}

func New(session domain.Session) (Model, error) { return NewWithDraft(session, domain.Draft{}) }

func NewWithDraft(session domain.Session, draft domain.Draft) (Model, error) {
	docs, err := loadDocuments(session)
	if err != nil {
		return Model{}, err
	}
	m := Model{session: session, docs: docs, reviewed: map[string]bool{}, mode: ModeBrowse, commentKind: domain.KindNote, keys: defaultKeyMap(), nextID: 1}
	m.previews = make([]previewState, len(docs))
	m.documentModes = make([]DocumentMode, len(docs))
	for i, d := range docs {
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
}
func (m *Model) rebuildComposer() {
	m.composer = shared.NewInputTextarea("Comment", 0, 4)
	m.replacement = shared.NewInputTextarea("Replacement (suggestions only)", 0, 3)
	m.composer.Blur()
	m.replacement.Blur()
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

// SetVerdict is used by a parent surface when verdict choices are rendered as
// buttons or a compact selector rather than raw digit key presses.
func (m *Model) SetVerdict(verdict string) { m.verdict = verdict }

func (m *Model) SetChoices(choices []string) { m.choices = append([]string(nil), choices...) }

func (m Model) Verdict() string { return m.verdict }

func (m Model) Reviewed(documentID string) bool { return m.reviewed[documentID] }

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
