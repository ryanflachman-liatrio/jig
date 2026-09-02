package review

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	if m.ActiveDocument().ID != "02-notes" || m.activeDocumentMode() != DocumentPreview || !m.Reviewed("01-plan") {
		t.Fatalf("draft was not restored: %#v", m)
	}
	m = update(m, "{")
	if m.ActiveDocument().ID != "01-plan" || m.activeDocumentMode() != DocumentPreview {
		t.Fatalf("previous document: %s", m.ActiveDocument().ID)
	}
	m = update(m, "s")
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
	m = update(m, "s")
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

func TestDocumentModesDefaultByFormatAndRestorePerDocument(t *testing.T) {
	markdown := "# Title\n\nBody"
	plain := "plain\ntext"
	diff := "--- a/file\n+++ b/file"
	session := domain.Session{StepID: "review", Documents: []domain.Document{
		{ID: "md", Label: "Markdown", Source: "doc.md", Format: "markdown", Content: markdown, SHA256: domain.Digest(markdown)},
		{ID: "text", Label: "Text", Source: "doc.txt", Format: "text", Content: plain, SHA256: domain.Digest(plain)},
		{ID: "diff", Label: "Diff", Source: "change.diff", Format: "diff", Content: diff, SHA256: domain.Digest(diff)},
	}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.activeDocumentMode(); got != DocumentPreview {
		t.Fatalf("Markdown mode = %v, want preview", got)
	}
	m = update(m, "s")
	if got := m.activeDocumentMode(); got != DocumentSource {
		t.Fatalf("toggled Markdown mode = %v, want source", got)
	}
	m = update(m, "}")
	if got := m.activeDocumentMode(); got != DocumentSource {
		t.Fatalf("text mode = %v, want source", got)
	}
	m = update(m, "s")
	if got := m.error; got != "preview is available for Markdown" {
		t.Fatalf("text preview error = %q", got)
	}
	m = update(m, "}")
	if got := m.activeDocumentMode(); got != DocumentSource {
		t.Fatalf("diff mode = %v, want source", got)
	}
	m = update(m, "}")
	if got := m.activeDocumentMode(); got != DocumentSource {
		t.Fatalf("Markdown mode was not restored: %v", got)
	}
}

func TestPreviewToggleMapsCursorAndBlockRanges(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m = update(m, "}")
	if m.activeDocumentMode() != DocumentPreview {
		t.Fatal("Markdown did not open in preview")
	}
	m = update(m, "j")
	if m.CursorLine() != 3 || m.rangeEnd != 3 {
		t.Fatalf("preview block range = %d-%d, want 3-3", m.CursorLine(), m.rangeEnd)
	}
	m = update(m, "s")
	if m.activeDocumentMode() != DocumentSource || m.CursorLine() != 3 || m.rangeEnd != 3 {
		t.Fatalf("source mapping = mode %v, range %d-%d", m.activeDocumentMode(), m.CursorLine(), m.rangeEnd)
	}
	m.cursor = 1
	m = update(m, "s")
	if m.activeDocumentMode() != DocumentPreview || m.PreviewBlock() != 0 {
		t.Fatalf("preview mapping = mode %v, block %d", m.activeDocumentMode(), m.PreviewBlock())
	}
}

func TestPreviewRangeSelectionIsNotAdvertisedOrEntered(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m = update(m, "v")
	if m.Mode() != ModeBrowse || !strings.Contains(m.error, "press c") {
		t.Fatalf("preview range selection changed state: mode=%v error=%q", m.Mode(), m.error)
	}
	for _, help := range m.Help() {
		if help.Key == "v" {
			t.Fatal("preview help advertises source range selection")
		}
	}
}

func TestPreviewDecoratesOverlappingAndActiveComments(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m.active = 1
	m.previewBlock = 1
	m.cursor, m.rangeEnd = 3, 3
	m.comments = []domain.Comment{
		{ID: "C001", Anchor: domain.Anchor{DocumentID: "02-notes", StartLine: 2, EndLine: 3}, Body: "overlaps"},
		{ID: "C002", Anchor: domain.Anchor{DocumentID: "02-notes", StartLine: 3, EndLine: 3}, Body: "active"},
	}
	m.activeComment = "C002"
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := lipgloss.NewStyle().Render(m.documentView())
	plain := ansi.Strip(view)
	for _, want := range []string{"L3", "C002 active"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("preview missing %q:\n%s", want, plain)
		}
	}
	for _, hidden := range []string{"Comments", "C001", "overlaps"} {
		if strings.Contains(plain, hidden) {
			t.Fatalf("preview retained persistent comment detail %q:\n%s", hidden, plain)
		}
	}
	if got := lipgloss.Width(m.documentView()); got > 80 {
		t.Fatalf("narrow preview width = %d, want <= 80", got)
	}
	m.activeComment = ""
	if plain := ansi.Strip(m.documentView()); !strings.Contains(plain, "● 2 comments") {
		t.Fatalf("preview missing overlapping comment count:\n%s", plain)
	}
}

func TestPreviewHelpUsesCompactAndFullModeLabels(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	if got := m.keys.ToggleMode.Help().Desc; got != "toggle source/preview" {
		t.Fatalf("full mode help = %q", got)
	}
	found := false
	for _, item := range m.Help() {
		if item.Key == "s" && item.Description == "view" {
			found = true
		}
	}
	if !found {
		t.Fatal("compact preview help is missing s view")
	}
}

func TestPreviewCommentNavigationSelectsContainingBlock(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m.active = 1
	m.comments = []domain.Comment{{
		ID: "C001", Anchor: domain.Anchor{DocumentID: "02-notes", StartLine: 3, EndLine: 3}, Body: "body",
	}}
	m.nextComment(false)
	if m.activeComment != "C001" || m.previewBlock != 1 || m.cursor != 3 || m.rangeEnd != 3 {
		t.Fatalf("comment navigation = id %q block %d range %d-%d", m.activeComment, m.previewBlock, m.cursor, m.rangeEnd)
	}
}

func TestPreviewFailureFallsBackToSourceWithVisibleError(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m.previews[0].parseErr = fmt.Errorf("renderer failed")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := ansi.Strip(m.documentView())
	if m.activeDocumentMode() != DocumentSource || !strings.Contains(m.error, "renderer failed") || !strings.Contains(view, "[ SOURCE ]") || !strings.Contains(view, "preview unavailable: renderer failed") {
		t.Fatalf("fallback = mode %v error %q view:\n%s", m.activeDocumentMode(), m.error, view)
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

func TestCommentModalIsCenteredAndUnaffectedByCommentCount(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 120, Height: 40}, {Width: 180, Height: 38}} {
		m, err := New(testSession())
		if err != nil {
			t.Fatal(err)
		}
		m, _ = m.Update(size)
		m = update(m, "c")
		view := ansi.Strip(m.View())
		modalRow := rowContaining(view, "New comment")
		if modalRow < size.Height/4 || modalRow > 3*size.Height/4 {
			t.Fatalf("%dx%d modal row = %d, want centered:\n%s", size.Width, size.Height, modalRow, view)
		}
		if width, height := lipgloss.Width(m.View()), lipgloss.Height(m.View()); width > size.Width || height > size.Height {
			t.Fatalf("%dx%d modal view = %dx%d", size.Width, size.Height, width, height)
		}

		for i := range 20 {
			m.comments = append(m.comments, domain.Comment{
				ID: fmt.Sprintf("C%03d", i+1), Anchor: domain.Anchor{DocumentID: m.ActiveDocument().ID, StartLine: 1, EndLine: 1}, Body: "hidden detail",
			})
		}
		crowded := ansi.Strip(m.View())
		if got := rowContaining(crowded, "New comment"); got != modalRow {
			t.Fatalf("%dx%d comment count moved modal from row %d to %d", size.Width, size.Height, modalRow, got)
		}
		if strings.Contains(crowded, "hidden detail") {
			t.Fatalf("%dx%d persistent comment body remained visible:\n%s", size.Width, size.Height, crowded)
		}
	}
}

func TestEnterOpensCommentOnActiveLineOrRange(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m = update(m, "s")
	m.comments = []domain.Comment{{
		ID: "C001", Kind: domain.KindConcern,
		Anchor: domain.Anchor{DocumentID: "01-plan", StartLine: 2, EndLine: 3},
		Body:   "Please revise these lines",
	}}
	m.cursor, m.rangeEnd = 2, 2
	m = update(m, "enter")
	if m.mode != ModeEditComment || m.editing != "C001" || m.composer.Value() != "Please revise these lines" {
		t.Fatalf("line did not open comment modal: mode=%v editing=%q body=%q", m.mode, m.editing, m.composer.Value())
	}
	m = update(m, "esc")
	if m.mode != ModeBrowse || strings.Contains(ansi.Strip(m.View()), "Please revise these lines") {
		t.Fatalf("closing modal left comment detail visible:\n%s", ansi.Strip(m.View()))
	}

	m.cursor, m.rangeEnd = 2, 2
	m = update(m, "v")
	m = update(m, "j")
	m = update(m, "enter")
	if m.mode != ModeEditComment || m.editing != "C001" {
		t.Fatalf("selected range did not reopen comment: mode=%v editing=%q", m.mode, m.editing)
	}
}

func rowContaining(view, needle string) int {
	for i, row := range strings.Split(view, "\n") {
		if strings.Contains(row, needle) {
			return i
		}
	}
	return -1
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

func TestSummaryRequiresEveryDocumentAcknowledged(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m = update(m, "S")
	m.SetVerdict("approve")
	next, cmd := m.Update(press("enter"))
	if cmd != nil {
		t.Fatal("incomplete acknowledgement submitted a review")
	}
	if next.Mode() != ModeSummary {
		t.Fatalf("mode = %v, want summary", next.Mode())
	}
	if got := next.error; got != "acknowledge 2 remaining document(s) before submitting" {
		t.Fatalf("submission error = %q", got)
	}
}

func TestSummaryDoesNotCountUnknownDraftAcknowledgements(t *testing.T) {
	m, err := NewWithDraft(testSession(), domain.Draft{Reviewed: []string{"unknown", "01-plan"}})
	if err != nil {
		t.Fatal(err)
	}
	m = update(m, "S")
	m.SetVerdict("approve")
	next, cmd := m.Update(press("enter"))
	if cmd != nil {
		t.Fatal("unknown acknowledgement allowed a review submission")
	}
	if got := next.error; got != "acknowledge 1 remaining document(s) before submitting" {
		t.Fatalf("submission error = %q", got)
	}
}

func TestSummaryDisplaysNumberedVerdictChoices(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m.SetChoices([]string{"approve", "revise"})
	m = update(m, "S")

	view := m.View()
	for _, want := range []string{"Decision", "[1] approve", "[2] revise", "1-9 choose decision", "enter submit review"} {
		if !strings.Contains(view, want) {
			t.Fatalf("summary view missing %q:\n%s", want, view)
		}
	}
}

func TestSourceViewPansStyledContentWithoutMovingGutter(t *testing.T) {
	content := "package main\nvar long = \"" + strings.Repeat("0123456789", 12) + "世界\""
	session := domain.Session{StepID: "source", Documents: []domain.Document{{
		ID: "go", Label: "Go source", Source: "main.go", Format: "text", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.cursor = 2
	m.comments = []domain.Comment{
		{ID: "C001", Anchor: domain.Anchor{DocumentID: "go", StartLine: 2, EndLine: 2}},
		{ID: "C002", Anchor: domain.Anchor{DocumentID: "go", StartLine: 2, EndLine: 2}},
	}
	m.activeComment = "C002"

	var before strings.Builder
	m.writeSourceRow(&before, 2)
	m = update(m, "l")
	if m.sourceXOffsets[0] != 4 {
		t.Fatalf("offset = %d, want 4", m.sourceXOffsets[0])
	}
	var after strings.Builder
	m.writeSourceRow(&after, 2)
	beforePlain, afterPlain := ansi.Strip(before.String()), ansi.Strip(after.String())
	if !strings.Contains(beforePlain, "▌ ●2 2 │ ") || !strings.Contains(afterPlain, "▌ ●2 2 │ ") {
		t.Fatalf("panning moved or changed gutter:\nbefore %q\nafter  %q", beforePlain, afterPlain)
	}
	expected := ansi.Strip(ansi.Cut(m.sources[0].lines[1], 4, 4+m.sourceContentWidth()))
	if !strings.Contains(afterPlain, expected) {
		t.Fatalf("panned row does not contain expected ANSI-safe slice %q: %q", expected, afterPlain)
	}
	if view := ansi.Strip(m.documentView()); !strings.Contains(view, "← col 5 →") || !strings.Contains(view, "Go") {
		t.Fatalf("source view missing pan hint or language:\n%s", view)
	}
	m = update(m, "0")
	if m.sourceXOffsets[0] != 0 {
		t.Fatalf("home offset = %d", m.sourceXOffsets[0])
	}
}

func TestMarkdownPreviewFollowsActiveBlock(t *testing.T) {
	content := strings.Join([]string{
		"# Section 1", "", "Body 1", "",
		"# Section 2", "", "Body 2", "",
		"# Section 3", "", "Body 3", "",
		"# Section 4", "", "Body 4", "",
		"# Section 5", "", "Body 5",
	}, "\n")
	session := domain.Session{StepID: "preview-scroll", Documents: []domain.Document{{
		ID: "md", Label: "Markdown", Source: "sections.md", Format: "markdown", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})

	for m.previewBlock < len(m.previews[0].blocks)-1 {
		m = update(m, "j")
		plain := ansi.Strip(m.documentView())
		activeRange := formatLineRange(m.cursor, m.rangeEnd)
		if !strings.Contains(plain, "▌ "+activeRange) {
			t.Fatalf("active block %d (%s) scrolled out of view:\n%s", m.previewBlock, activeRange, plain)
		}
	}
	bottom := ansi.Strip(m.documentView())
	if !strings.Contains(bottom, "Section 5") || strings.Contains(bottom, "Section 1") {
		t.Fatalf("preview did not follow navigation to the final section:\n%s", bottom)
	}

	for m.previewBlock > 0 {
		m = update(m, "k")
	}
	top := ansi.Strip(m.documentView())
	if !strings.Contains(top, "Section 1") {
		t.Fatalf("preview did not follow navigation back to the first section:\n%s", top)
	}
}

func TestSourceOffsetsArePerDocumentAndEditorsCapturePanKeys(t *testing.T) {
	long := strings.Repeat("abcdefghij", 12)
	session := domain.Session{StepID: "source", Documents: []domain.Document{
		{ID: "one", Label: "One", Source: "one.go", Format: "text", Content: long, SHA256: domain.Digest(long)},
		{ID: "two", Label: "Two", Source: "two.go", Format: "text", Content: long, SHA256: domain.Digest(long)},
	}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, "l")
	m = update(m, "}")
	m = update(m, "l")
	m = update(m, "l")
	m = update(m, "{")
	if got := m.sourceXOffsets; got[0] != 4 || got[1] != 8 {
		t.Fatalf("per-document offsets = %v, want [4 8]", got)
	}
	m = update(m, "c")
	m = update(m, "h")
	m = update(m, "l")
	m = update(m, "0")
	if m.sourceXOffsets[0] != 4 || !strings.Contains(m.composer.Value(), "hl0") {
		t.Fatalf("editor did not capture pan keys: offset=%d value=%q", m.sourceXOffsets[0], m.composer.Value())
	}
}

func TestHighlightedRenderingDoesNotChangeAnchorsOrRebuildOnResize(t *testing.T) {
	content := "package main\n\nfunc main() { println(\"世界\") }\n"
	session := domain.Session{StepID: "source", Documents: []domain.Document{{
		ID: "go", Label: "Go", Source: "main.go", Format: "text", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	want := m.docs[0].anchor(m.docs[0].meta.SHA256, 2, 3)
	styled := m.sources[0].lines[2]
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, "l")
	_ = m.documentView()
	got := m.docs[0].anchor(m.docs[0].meta.SHA256, 2, 3)
	if got != want || strings.Contains(got.Quote, "\x1b[") {
		t.Fatalf("styled rendering changed anchor:\ngot  %#v\nwant %#v", got, want)
	}
	if m.sources[0].lines[2] != styled {
		t.Fatal("resize or cursor movement rebuilt the cached source presentation")
	}
}

func TestSourceRowsFitTargetWidthsAndClampOffsetOnResize(t *testing.T) {
	content := "const wide = \"" + strings.Repeat("界", 80) + "\""
	session := domain.Session{StepID: "source", Documents: []domain.Document{{
		ID: "go", Label: "Go", Source: "wide.go", Format: "text", Content: content, SHA256: domain.Digest(content),
	}}}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 120, Height: 40}} {
		m, err := New(session)
		if err != nil {
			t.Fatal(err)
		}
		m, _ = m.Update(size)
		for range 100 {
			m = update(m, "l")
		}
		m, _ = m.Update(tea.WindowSizeMsg{Width: size.Width + 40, Height: size.Height})
		if m.sourceXOffsets[0] > max(0, m.sourceMaxWidth()-m.sourceContentWidth()) {
			t.Fatalf("%dx%d offset was not clamped: %d", size.Width, size.Height, m.sourceXOffsets[0])
		}
		for _, row := range strings.Split(m.documentView(), "\n") {
			if width := lipgloss.Width(row); width > documentPanelWidth(m.width) {
				t.Fatalf("%dx%d source row width = %d, panel = %d", size.Width, size.Height, width, documentPanelWidth(m.width))
			}
		}
	}
}
