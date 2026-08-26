package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type Kind string

const (
	KindNote       Kind = "note"
	KindQuestion   Kind = "question"
	KindConcern    Kind = "concern"
	KindBlocker    Kind = "blocker"
	KindSuggestion Kind = "suggestion"
	KindPraise     Kind = "praise"
)

type Document struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Source       string `json:"source"`
	Format       string `json:"format"`
	SnapshotPath string `json:"snapshot_path,omitempty"`
	SHA256       string `json:"sha256"`
	LineCount    int    `json:"line_count"`
	Content      string `json:"-"`
}
type DocumentRecord struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Source    string `json:"source"`
	Format    string `json:"format"`
	SHA256    string `json:"sha256"`
	LineCount int    `json:"line_count"`
}
type Anchor struct {
	DocumentID string `json:"document_id"`
	SHA256     string `json:"sha256"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Quote      string `json:"quote"`
	Prefix     string `json:"prefix,omitempty"`
	Suffix     string `json:"suffix,omitempty"`
}
type Comment struct {
	ID          string `json:"id"`
	Kind        Kind   `json:"kind"`
	Anchor      Anchor `json:"anchor"`
	Body        string `json:"body"`
	Replacement string `json:"replacement,omitempty"`
}
type ViewState struct {
	ActiveDocumentID string `json:"active_document_id,omitempty"`
	Mode             string `json:"mode,omitempty"`
	CursorLine       int    `json:"cursor_line,omitempty"`
	RangeEndLine     int    `json:"range_end_line,omitempty"`
	Viewport         int    `json:"viewport,omitempty"`
	ActiveCommentID  string `json:"active_comment_id,omitempty"`
	CommentKind      Kind   `json:"comment_kind,omitempty"`
}
type Draft struct {
	SchemaVersion int        `json:"schema_version"`
	StepID        string     `json:"step_id"`
	RoundID       string     `json:"round_id"`
	Documents     []Document `json:"documents"`
	Reviewed      []string   `json:"reviewed_documents"`
	Comments      []Comment  `json:"comments"`
	Summary       string     `json:"summary,omitempty"`
	View          ViewState  `json:"view"`
}
type Submission struct {
	SchemaVersion int              `json:"schema_version"`
	StepID        string           `json:"step_id"`
	RoundID       string           `json:"round_id"`
	Verdict       string           `json:"verdict"`
	Documents     []DocumentRecord `json:"documents"`
	Reviewed      []string         `json:"reviewed_documents"`
	Comments      []Comment        `json:"comments"`
	Summary       string           `json:"summary,omitempty"`
}
type Session struct {
	StepID    string
	RoundID   string
	Documents []Document
}

func Digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
func lines(content string) []string { return strings.Split(content, "\n") }
func (d Document) Record() DocumentRecord {
	return DocumentRecord{d.ID, d.Label, d.Source, d.Format, d.SHA256, d.LineCount}
}

func ValidateSubmission(session Session, sub Submission, choices []string) error {
	if sub.StepID != session.StepID || sub.RoundID != session.RoundID {
		return fmt.Errorf("submission does not match review session")
	}
	okChoice := false
	for _, choice := range choices {
		if sub.Verdict == choice {
			okChoice = true
			break
		}
	}
	if !okChoice {
		return fmt.Errorf("invalid verdict %q", sub.Verdict)
	}
	docs := make(map[string]Document, len(session.Documents))
	for _, d := range session.Documents {
		docs[d.ID] = d
	}
	docSeen := map[string]bool{}
	if len(sub.Documents) != len(session.Documents) {
		return fmt.Errorf("submission must include every review document")
	}
	for _, record := range sub.Documents {
		d, ok := docs[record.ID]
		if !ok || record.SHA256 != d.SHA256 || docSeen[record.ID] {
			return fmt.Errorf("invalid or duplicate review document %q", record.ID)
		}
		docSeen[record.ID] = true
	}
	if len(sub.Reviewed) != len(session.Documents) {
		return fmt.Errorf("all review documents must be acknowledged")
	}
	reviewedSeen := map[string]bool{}
	for _, id := range sub.Reviewed {
		if reviewedSeen[id] {
			return fmt.Errorf("duplicate reviewed document %q", id)
		}
		if _, ok := docs[id]; !ok {
			return fmt.Errorf("unknown reviewed document %q", id)
		}
		reviewedSeen[id] = true
	}
	if len(reviewedSeen) != len(session.Documents) {
		return fmt.Errorf("all review documents must be acknowledged")
	}
	commentIDs := map[string]bool{}
	for _, c := range sub.Comments {
		if c.ID == "" || commentIDs[c.ID] {
			return fmt.Errorf("comment id is empty or duplicated")
		}
		commentIDs[c.ID] = true
		if strings.TrimSpace(c.Body) == "" {
			return fmt.Errorf("comment %q has blank body", c.ID)
		}
		if !validKind(c.Kind) {
			return fmt.Errorf("comment %q has invalid kind %q", c.ID, c.Kind)
		}
		if c.Kind == KindSuggestion && strings.TrimSpace(c.Replacement) == "" {
			return fmt.Errorf("suggestion %q requires replacement text", c.ID)
		}
		if c.Kind != KindSuggestion && c.Replacement != "" {
			return fmt.Errorf("comment %q has replacement text but is not a suggestion", c.ID)
		}
		d, ok := docs[c.Anchor.DocumentID]
		if !ok {
			return fmt.Errorf("comment %q references unknown document", c.ID)
		}
		if c.Anchor.SHA256 != d.SHA256 {
			return fmt.Errorf("comment %q has mismatched document digest", c.ID)
		}
		if c.Anchor.StartLine < 1 || c.Anchor.EndLine < c.Anchor.StartLine || c.Anchor.EndLine > d.LineCount {
			return fmt.Errorf("comment %q has invalid line range", c.ID)
		}
		got := strings.Join(lines(d.Content)[c.Anchor.StartLine-1:c.Anchor.EndLine], "\n")
		if c.Anchor.Quote != got {
			return fmt.Errorf("comment %q quote does not match snapshot", c.ID)
		}
	}
	return nil
}
func validKind(k Kind) bool {
	switch k {
	case KindNote, KindQuestion, KindConcern, KindBlocker, KindSuggestion, KindPraise:
		return true
	}
	return false
}
