package review

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	domain "jig/internal/review"
)

func testSession() domain.Session {
	first := "one\ntwo\nthree\nfour"
	second := "# Notes\n\nbody"
	return domain.Session{StepID: "check", RoundID: "g000-i000", Documents: []domain.Document{
		{ID: "01-plan", Label: "Plan", Source: "plan.md", Format: "markdown", Content: first, SHA256: domain.Digest(first), LineCount: 4},
		{ID: "02-notes", Label: "Notes", Source: "notes.md", Format: "markdown", Content: second, SHA256: domain.Digest(second), LineCount: 3},
	}}
}

func press(text string) tea.KeyPressMsg {
	switch text {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	return tea.KeyPressMsg{Text: text}
}

func update(m Model, text string) Model {
	next, _ := m.Update(press(text))
	return next
}

func TestModelNavigationAndRestoration(t *testing.T) {
	m, err := NewWithDraft(testSession(), domain.Draft{Reviewed: []string{"01-plan"}, View: domain.ViewState{
		ActiveDocumentID: "02-notes", CursorLine: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if m.ActiveDocument().ID != "02-notes" || m.CursorLine() != 2 || !m.Reviewed("01-plan") {
		t.Fatalf("draft was not restored: %#v", m)
	}
	m = update(m, "{")
	if m.ActiveDocument().ID != "01-plan" {
		t.Fatalf("previous document: %s", m.ActiveDocument().ID)
	}
	m = update(m, "j")
	if m.CursorLine() != 2 {
		t.Fatalf("cursor line = %d, want 2", m.CursorLine())
	}
	m = update(m, "r")
	if m.Reviewed("01-plan") {
		t.Fatal("review acknowledgement toggle did not take effect")
	}
}

func TestModelRangeAnchorAndDraftLifecycle(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m = update(m, "j")
	m = update(m, "v")
	m = update(m, "j")
	m = update(m, "c")
	if m.Mode() != ModeComposeComment {
		t.Fatalf("mode = %v, want compose", m.Mode())
	}
	// The textarea receives printable key events in the same way it does in the
	// real Bubble Tea loop; use its public value setter to keep this test focused
	// on workspace state rather than textarea internals.
	m.composer.SetValue("Please clarify this range")
	m = update(m, "enter")
	if len(m.Comments()) != 1 {
		t.Fatalf("comments = %d, want 1", len(m.Comments()))
	}
	c := m.Comments()[0]
	if c.ID != "C001" || c.Anchor.StartLine != 2 || c.Anchor.EndLine != 3 || c.Anchor.Quote != "two\nthree" {
		t.Fatalf("unexpected anchor: %#v", c.Anchor)
	}
	draft := m.Draft()
	if len(draft.Comments) != 1 || draft.View.CursorLine != 3 {
		t.Fatalf("draft did not retain workspace state: %#v", draft)
	}
}

func TestSuggestionRequiresReplacementAndDeletedIDsAreNotReused(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m.commentKind = domain.KindSuggestion
	m.openComposer(false)
	m.composer.SetValue("Replace this")
	m = update(m, "enter")
	if len(m.comments) != 0 || m.error == "" {
		t.Fatal("suggestion without replacement was accepted")
	}
	m.replacement.SetValue("new text")
	m = update(m, "enter")
	if len(m.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(m.comments))
	}
	m.activeComment = m.comments[0].ID
	m.deleteActive()
	m.commentKind = domain.KindNote
	m.openComposer(false)
	m.composer.SetValue("another")
	m = update(m, "enter")
	if m.comments[0].ID != "C002" {
		t.Fatalf("comment ID reused: %s", m.comments[0].ID)
	}
}

func TestSummaryProducesStructuredSubmission(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range m.docs {
		m.reviewed[d.meta.ID] = true
	}
	m = update(m, "S")
	m.SetVerdict("revise")
	m.summary.SetValue("Please address the concern.")
	next, cmd := m.Update(press("enter"))
	if next.Mode() != ModeBrowse || cmd == nil {
		t.Fatalf("summary did not submit: mode=%v cmd=%v", next.Mode(), cmd)
	}
	msg := cmd()
	sub, ok := msg.(SubmissionMsg)
	if !ok || sub.Submission.Verdict != "revise" || sub.Submission.Summary == "" || len(sub.Submission.Documents) != 2 {
		t.Fatalf("bad submission: %#v", msg)
	}
}
