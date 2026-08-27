package review

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	domain "jig/internal/review"
	"jig/internal/tui/shared"
)

func (m *Model) View() string {
	return m.view(true)
}

// EmbeddedView omits the workspace footer when the Monitor already renders
// the same contextual bindings in its global footer.
func (m *Model) EmbeddedView() string {
	return m.view(false)
}

func (m *Model) view(withFooter bool) string {
	if len(m.docs) == 0 {
		return "No documents to review"
	}
	header := fmt.Sprintf("Review: %s · %d / %d reviewed · %d comments", m.session.StepID, len(m.reviewed), len(m.docs), len(m.comments))
	if m.error != "" {
		header += " · " + m.error
	}
	left := m.documentList()
	right := m.documentView()
	if m.mode == ModeSummary {
		right = m.summaryView()
	}
	footer := ""
	if withFooter {
		footer = "\n" + m.footer()
	}
	if m.width > 0 && m.width < 90 {
		return strings.Join([]string{header, left, right}, "\n") + footer
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right) + footer
}

func (m *Model) documentList() string {
	var b strings.Builder
	b.WriteString(shared.Theme.Title.Render("Documents"))
	b.WriteByte('\n')
	for i, d := range m.docs {
		mark := "○"
		if m.reviewed[d.meta.ID] {
			mark = "✓"
		}
		cursor := " "
		if i == m.active {
			cursor = "▌"
		}
		count := 0
		for _, c := range m.comments {
			if c.Anchor.DocumentID == d.meta.ID {
				count++
			}
		}
		b.WriteString(fmt.Sprintf("%s %s %s", cursor, mark, d.meta.Label))
		if count > 0 {
			b.WriteString(fmt.Sprintf("  %d", count))
		}
		b.WriteByte('\n')
	}
	return shared.Panel("Documents", b.String(), listWidth(m.width), max(len(m.docs)+4, 5), m.mode != ModeSummary)
}

func (m *Model) documentView() string {
	d := m.docs[m.active]
	var b strings.Builder
	b.WriteString(m.documentHeader(d))
	if m.activeDocumentMode() == DocumentPreview {
		b.WriteByte('\n')
		preview := &m.previews[m.active]
		if preview.parseErr != nil {
			m.documentModes[m.active] = DocumentSource
			m.error = "preview unavailable: " + preview.parseErr.Error()
			return m.documentView()
		}
		for i, block := range preview.blocks {
			rendered, err := preview.render(i, d.meta.Content, m.previewContentWidth())
			if err != nil {
				m.documentModes[m.active] = DocumentSource
				m.error = "preview unavailable: " + err.Error()
				return m.documentView()
			}
			m.writePreviewBlock(&b, i, block, rendered)
		}
		m.writeCommentsAndComposer(&b, d)
		return shared.Panel(d.meta.Label, b.String(), documentPanelWidth(m.width), max(12, m.height-2), m.mode != ModeSummary)
	}
	if m.mode == ModeSelectRange {
		b.WriteString(fmt.Sprintf(" · L%d–%d", min(m.cursor, m.rangeEnd), max(m.cursor, m.rangeEnd)))
	}
	b.WriteByte('\n')
	if strings.HasPrefix(m.error, "preview unavailable:") {
		b.WriteString(shared.Theme.Error.Render(m.error))
		b.WriteByte('\n')
	}
	start := m.cursor - 8
	if start < 1 {
		start = 1
	}
	sourceRows := max(1, m.height-10)
	if m.mode == ModeComposeComment || m.mode == ModeEditComment {
		sourceRows = max(1, sourceRows-7)
	}
	end := start + sourceRows - 1
	if end > len(d.lines) {
		end = len(d.lines)
	}
	for i := start; i <= end; i++ {
		prefix := "  "
		if i == m.cursor {
			prefix = "▌ "
		}
		if m.mode == ModeSelectRange && i >= min(m.cursor, m.rangeEnd) && i <= max(m.cursor, m.rangeEnd) {
			prefix = "▌▌"
		}
		b.WriteString(fmt.Sprintf("%s%4d %s\n", prefix, i, d.lines[i-1]))
	}
	m.writeCommentsAndComposer(&b, d)
	return shared.Panel(d.meta.Label, b.String(), documentPanelWidth(m.width), max(12, m.height-2), m.mode != ModeSummary)
}

func (m *Model) documentHeader(d document) string {
	var b strings.Builder
	b.WriteString(d.meta.Label)
	b.WriteString("    ")
	if d.meta.Format == "markdown" {
		preview := shared.Theme.Review.ModeInactive.Render(" PREVIEW ")
		source := shared.Theme.Review.ModeInactive.Render(" SOURCE ")
		if m.activeDocumentMode() == DocumentPreview {
			preview = shared.Theme.Review.ModeActive.Render("[ PREVIEW ]")
		} else {
			source = shared.Theme.Review.ModeActive.Render("[ SOURCE ]")
		}
		b.WriteString(preview)
		b.WriteString("  ")
		b.WriteString(source)
		b.WriteString("     Markdown")
		return b.String()
	}
	b.WriteString(shared.Theme.Review.ModeActive.Render("[ SOURCE ]"))
	if d.meta.Format == "diff" {
		b.WriteString("     Diff")
	} else {
		b.WriteString("     Plain text")
	}
	return b.String()
}

func (m *Model) previewContentWidth() int {
	hFrame, _ := shared.PanelFrame()
	return max(documentPanelWidth(m.width)-hFrame-2, 20)
}

func (m *Model) writePreviewBlock(b *strings.Builder, index int, block previewBlock, rendered string) {
	active := index == m.previewBlock
	rail, railStyle := "│", shared.Theme.Review.BlockRailInactive
	metaStyle := shared.Theme.Review.BlockMetaInactive
	if active {
		rail, railStyle = "▌", shared.Theme.Review.BlockRailActive
		metaStyle = shared.Theme.Review.BlockMetaActive
	}

	b.WriteString(railStyle.Render(rail))
	b.WriteByte(' ')
	b.WriteString(metaStyle.Render(formatLineRange(block.startLine, block.endLine)))
	comments := m.commentsForRange(m.docs[m.active].meta.ID, block.startLine, block.endLine)
	if activeID := m.activeCommentForRange(comments); activeID != "" {
		b.WriteString("  ")
		b.WriteString(shared.Theme.Review.ActiveComment.Render("● " + activeID + " active"))
	} else if len(comments) > 0 {
		label := "comment"
		if len(comments) != 1 {
			label = "comments"
		}
		b.WriteString("  ")
		b.WriteString(shared.Theme.Review.CommentMarker.Render(fmt.Sprintf("● %d %s", len(comments), label)))
	}
	b.WriteByte('\n')
	for _, row := range strings.Split(rendered, "\n") {
		b.WriteString(railStyle.Render(rail))
		b.WriteByte(' ')
		b.WriteString(row)
		b.WriteByte('\n')
	}
}

func (m *Model) commentsForRange(documentID string, start, end int) []domain.Comment {
	comments := make([]domain.Comment, 0)
	for _, c := range m.comments {
		if c.Anchor.DocumentID == documentID && c.Anchor.StartLine <= end && c.Anchor.EndLine >= start {
			comments = append(comments, c)
		}
	}
	return comments
}

func (m *Model) activeCommentForRange(comments []domain.Comment) string {
	for _, c := range comments {
		if c.ID == m.activeComment {
			return c.ID
		}
	}
	return ""
}

func formatLineRange(start, end int) string {
	if start == end {
		return fmt.Sprintf("L%d", start)
	}
	return fmt.Sprintf("L%d–L%d", start, end)
}

func (m *Model) writeCommentsAndComposer(b *strings.Builder, d document) {
	comments := m.commentsForRange(d.meta.ID, 1, len(d.lines))
	if len(comments) > 0 {
		b.WriteString("\n")
		b.WriteString(shared.Theme.StatusLine.Render("Comments"))
		b.WriteByte('\n')
		for _, c := range comments {
			metadata := fmt.Sprintf("%s · %s · lines %d–%d", c.ID, c.Kind, c.Anchor.StartLine, c.Anchor.EndLine)
			if c.ID == m.activeComment {
				metadata = shared.Theme.Review.ActiveComment.Render(metadata + " · active")
			}
			b.WriteString(metadata + "\n")
			b.WriteString("  " + strings.ReplaceAll(c.Body, "\n", " ") + "\n")
		}
	}
	if m.mode == ModeComposeComment || m.mode == ModeEditComment {
		b.WriteString("\n")
		label := "New comment"
		if m.mode == ModeEditComment {
			label = "Edit comment"
		}
		b.WriteString(shared.Theme.StatusLine.Render(fmt.Sprintf("%s · %s", label, m.commentKind)))
		b.WriteString("\n")
		b.WriteString(m.composer.View())
		if m.commentKind == domain.KindSuggestion {
			b.WriteString("\n")
			b.WriteString(m.replacement.View())
		}
	}
}

func (m *Model) summaryView() string {
	var b strings.Builder
	b.WriteString(shared.Theme.Title.Render("Review summary"))
	b.WriteString("\n\n")
	b.WriteString(shared.Theme.StatusLine.Render("Documents"))
	b.WriteByte('\n')
	for _, d := range m.docs {
		mark := "unreviewed"
		if m.reviewed[d.meta.ID] {
			mark = "reviewed"
		}
		b.WriteString(fmt.Sprintf("%s — %s\n", d.meta.Label, mark))
	}
	b.WriteString("\n")
	b.WriteString(shared.Theme.StatusLine.Render("Decision"))
	b.WriteByte('\n')
	for i, choice := range m.choices {
		row := fmt.Sprintf("  [%d] %s", i+1, choice)
		if choice == m.verdict {
			row = shared.Theme.SelectedLine.Render("▌ " + row[2:])
		}
		b.WriteString(row + "\n")
	}
	b.WriteString("\n")
	b.WriteString(shared.Theme.StatusLine.Render("Overall summary (optional)"))
	b.WriteByte('\n')
	b.WriteString(m.summary.View())
	return shared.Panel("Summary", b.String(), max(42, m.width-2), max(12, m.height-2), true)
}
func (m *Model) footer() string {
	if m.mode == ModeSummary {
		return "1-9 choose decision · enter submit review · esc return to documents"
	}
	if m.mode == ModeComposeComment || m.mode == ModeEditComment {
		return "enter save comment · esc cancel comment · tab change comment kind"
	}
	view := ""
	if m.docs[m.active].meta.Format == "markdown" {
		view = " · s view"
	}
	return "S finish review · j/k move · {/} document · c comment · r reviewed" + view + " · esc close"
}
func listWidth(width int) int {
	if width > 0 && width < 90 {
		return width
	}
	return 30
}
func documentPanelWidth(width int) int {
	if width > 0 && width < 90 {
		return width
	}
	return max(42, width-listWidth(width)-4)
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
