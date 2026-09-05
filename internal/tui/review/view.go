package review

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	domain "jig/internal/review"
	"jig/internal/tui/diffview"
	"jig/internal/tui/shared"
)

type sourceViewRow struct {
	patchLine int
	folded    *diffHunk
	separator bool
}

func (m *Model) View() string {
	return m.view(true, true)
}

// EmbeddedView omits the workspace header and footer when the Monitor already
// owns chrome (panel title + CompactHint footer).
func (m *Model) EmbeddedView() string {
	return m.view(false, false)
}

// TitleSegments are the identity crumbs after the [REVIEW] badge: active doc,
// progress, and comment count (joined with · by the Monitor panel title).
func (m Model) TitleSegments() []string {
	parts := make([]string, 0, 3)
	if doc := m.ActiveDocument(); doc.Label != "" {
		parts = append(parts, doc.Label)
	} else if doc.ID != "" {
		parts = append(parts, doc.ID)
	}
	if n := len(m.docs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", m.reviewedDocumentCount(), n))
	}
	if c := len(m.comments); c > 0 {
		parts = append(parts, fmt.Sprintf("%d comments", c))
	}
	if m.error != "" {
		parts = append(parts, m.error)
	}
	return parts
}

func (m *Model) view(withHeader, withFooter bool) string {
	if len(m.docs) == 0 {
		return "No documents to review"
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
	var body string
	if m.width > 0 && m.width < 90 {
		body = strings.Join([]string{left, right}, "\n")
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
	}
	base := body + footer
	if withHeader {
		header := fmt.Sprintf("Review: %s · %d / %d reviewed · %d comments", m.session.StepID, m.reviewedDocumentCount(), len(m.docs), len(m.comments))
		if m.error != "" {
			header += " · " + m.error
		}
		base = header + "\n" + base
	}
	if m.mode == ModeComposeComment || m.mode == ModeEditComment {
		return m.commentOverlay(base)
	}
	return base
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
		rows := make([]string, 0, len(preview.blocks)*2)
		activeStart, activeEnd := 0, 0
		for i, block := range preview.blocks {
			rendered, err := preview.render(i, d.meta.Content, m.previewContentWidth())
			if err != nil {
				m.documentModes[m.active] = DocumentSource
				m.error = "preview unavailable: " + err.Error()
				return m.documentView()
			}
			var blockView strings.Builder
			m.writePreviewBlock(&blockView, i, block, rendered)
			blockRows := strings.Split(strings.TrimSuffix(blockView.String(), "\n"), "\n")
			if i == m.previewBlock {
				activeStart = len(rows)
				activeEnd = activeStart + len(blockRows) - 1
			}
			rows = append(rows, blockRows...)
		}
		rows = previewWindow(rows, activeStart, activeEnd, m.previewRowBudget())
		b.WriteString(strings.Join(rows, "\n"))
		b.WriteByte('\n')
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
	if m.sources[m.active].err != nil {
		b.WriteString(shared.Theme.Error.Render("syntax highlighting unavailable: " + m.sources[m.active].err.Error()))
		b.WriteByte('\n')
	}
	if diff := m.sources[m.active].diff; diff != nil && diff.parseErr != nil {
		b.WriteString(shared.Theme.Error.Render("diff navigation unavailable: " + diff.parseErr.Error()))
		b.WriteByte('\n')
	}
	if m.sourceOverflow() {
		b.WriteString(shared.Theme.Review.HorizontalHint.Render(fmt.Sprintf("← col %d →", m.sourceXOffsets[m.active]+1)))
		b.WriteByte('\n')
	}
	for _, row := range m.sourceWindowRows(max(1, m.height-10)) {
		if row.separator {
			b.WriteByte('\n')
		} else if row.folded != nil {
			m.writeFoldedSourceRow(&b, row.folded)
		} else {
			m.writeSourceRow(&b, row.patchLine)
		}
	}
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
		b.WriteString("     ")
		b.WriteString(shared.Theme.Review.Language.Render(m.sourceLanguage()))
		return b.String()
	}
	b.WriteString(shared.Theme.Review.ModeActive.Render("[ SOURCE ]"))
	b.WriteString("     ")
	b.WriteString(shared.Theme.Review.Language.Render(m.sourceLanguage()))
	if status := m.diffHeaderStatus(); status != "" {
		b.WriteString(" · ")
		b.WriteString(shared.Theme.Review.HunkStatus.Render(status))
	}
	return b.String()
}

func (m *Model) diffHeaderStatus() string {
	if m.active < 0 || m.active >= len(m.sources) || m.sources[m.active].diff == nil {
		return ""
	}
	diff := m.sources[m.active].diff
	if diff.parseErr != nil {
		return "Raw diff"
	}
	if hunk := m.hunkForPatchLine(m.cursor); hunk != nil {
		return fmt.Sprintf("Hunk %d/%d", hunk.ordinal, len(diff.hunks))
	}
	return fmt.Sprintf("Hunks %d", len(diff.hunks))
}

func (m *Model) sourceWindowRows(limit int) []sourceViewRow {
	rows := m.visibleSourceRows()
	if len(rows) <= limit {
		return rows
	}
	cursor := 0
	for i, row := range rows {
		if row.patchLine == m.cursor {
			cursor = i
			break
		}
	}
	start := max(0, cursor-8)
	if start+limit > len(rows) {
		start = len(rows) - limit
	}
	return rows[start : start+limit]
}

func (m *Model) visibleSourceRows() []sourceViewRow {
	rows := make([]sourceViewRow, 0, len(m.docs[m.active].lines))
	if diff := m.parsedDiffPresentation(); diff != nil {
		for _, display := range diff.display.DisplayRows(func(hunk diffview.Hunk) bool { return diff.folded[hunk.Ordinal] }) {
			if display.Separator {
				rows = append(rows, sourceViewRow{separator: true})
				continue
			}
			if display.Folded != nil {
				rows = append(rows, sourceViewRow{patchLine: display.PatchLine, folded: m.hunkStartingAtPatchLine(display.PatchLine)})
				continue
			}
			rows = append(rows, sourceViewRow{patchLine: display.PatchLine})
		}
		return rows
	}
	for line := range m.docs[m.active].lines {
		rows = append(rows, sourceViewRow{patchLine: line + 1})
	}
	return rows
}

func (m *Model) sourceLanguage() string {
	if m.activeDocumentMode() == DocumentPreview {
		return "Markdown"
	}
	if m.active >= 0 && m.active < len(m.sources) && m.sources[m.active].language != "" {
		if m.docs[m.active].meta.Format == "markdown" {
			return "Markdown source"
		}
		return m.sources[m.active].language
	}
	return "Plain text"
}

func (m *Model) writeSourceRow(b *strings.Builder, line int) {
	selected := m.mode == ModeSelectRange && line >= min(m.cursor, m.rangeEnd) && line <= max(m.cursor, m.rangeEnd)
	rail, railStyle := " ", shared.Theme.Review.Gutter
	gutterStyle := shared.Theme.Review.Gutter
	if selected {
		rail, railStyle = "▌", shared.Theme.Review.RangeRail
		gutterStyle = shared.Theme.Review.GutterRange
	}
	if line == m.cursor {
		rail, railStyle = "▌", shared.Theme.Review.CursorRail
		gutterStyle = shared.Theme.Review.GutterCursor
	}

	marker, markerStyle := m.sourceCommentMarker(line)
	contentWidth := m.sourceContentWidth()
	offset := m.sourceXOffsets[m.active]
	if diff := m.parsedDiffPresentation(); diff != nil && diff.rows[line-1].kind == diffRowHunkHeader {
		offset = 0
	}
	content := ansi.Cut(m.sources[m.active].lines[line-1], offset, offset+contentWidth)
	if diff := m.parsedDiffPresentation(); diff != nil && diff.rows[line-1].kind == diffRowHunkHeader {
		content = ansi.Cut(m.hunkHeaderContent(line), 0, contentWidth)
	}
	b.WriteString(railStyle.Render(rail))
	b.WriteByte(' ')
	b.WriteString(markerStyle.Render(fmt.Sprintf("%-2s", marker)))
	b.WriteByte(' ')
	if diff := m.parsedDiffPresentation(); diff != nil {
		m.writeDiffGutters(b, diff.rows[line-1], selected, line == m.cursor)
	} else {
		digits := len(fmt.Sprint(len(m.docs[m.active].lines)))
		b.WriteString(gutterStyle.Render(fmt.Sprintf("%*d", digits, line)))
	}
	b.WriteString(shared.Theme.Review.Gutter.Render(" │ "))
	b.WriteString(content)
	b.WriteByte('\n')
}

func (m *Model) hunkHeaderContent(line int) string {
	hunk := m.hunkStartingAtPatchLine(line)
	if hunk == nil {
		return ""
	}
	return diffview.RenderHunkHeader(diffview.Hunk{Ordinal: hunk.ordinal, FileName: hunk.fileName, Header: hunk.header})
}

func (m *Model) writeFoldedSourceRow(b *strings.Builder, hunk *diffHunk) {
	if hunk == nil {
		return
	}
	b.WriteString(shared.Theme.Review.Gutter.Render(" "))
	b.WriteByte(' ')
	b.WriteString(shared.Theme.Review.Gutter.Render("  "))
	b.WriteByte(' ')
	m.writeDiffGutters(b, diffRow{}, false, false)
	b.WriteString(shared.Theme.Review.Gutter.Render(" │ "))
	bodyRows := hunk.endPatchLine - hunk.startPatchLine
	placeholder := fmt.Sprintf("… %d patch rows folded; press z to expand", bodyRows)
	content := shared.Theme.Review.FoldedPlaceholder.Render(placeholder)
	b.WriteString(ansi.Cut(content, 0, m.sourceContentWidth()))
	b.WriteByte('\n')
}

func (m *Model) parsedDiffPresentation() *diffPresentation {
	if m.active < 0 || m.active >= len(m.sources) {
		return nil
	}
	diff := m.sources[m.active].diff
	if diff == nil || diff.parseErr != nil || len(diff.rows) != len(m.docs[m.active].lines) {
		return nil
	}
	return diff
}

func (m *Model) writeDiffGutters(b *strings.Builder, row diffRow, selected, cursor bool) {
	digits := m.diffGutterDigits()
	b.WriteString(m.diffGutterStyle(row, selected, cursor, row.hasOld).Render(formatDiffCoordinate(row.oldLine, row.hasOld, digits)))
	b.WriteByte(' ')
	b.WriteString(m.diffGutterStyle(row, selected, cursor, row.hasNew).Render(formatDiffCoordinate(row.newLine, row.hasNew, digits)))
}

func (m *Model) diffGutterDigits() int {
	digits := 1
	if diff := m.parsedDiffPresentation(); diff != nil {
		for _, row := range diff.rows {
			if row.hasOld {
				digits = max(digits, len(fmt.Sprint(row.oldLine)))
			}
			if row.hasNew {
				digits = max(digits, len(fmt.Sprint(row.newLine)))
			}
		}
	}
	return digits
}

func formatDiffCoordinate(line int, present bool, digits int) string {
	if !present {
		return strings.Repeat(" ", digits)
	}
	return fmt.Sprintf("%*d", digits, line)
}

func (m *Model) diffGutterStyle(row diffRow, selected, cursor, present bool) lipgloss.Style {
	if cursor {
		return shared.Theme.Review.DiffGutterCursor
	}
	if selected {
		return shared.Theme.Review.DiffGutterRange
	}
	if !present {
		return shared.Theme.Review.DiffGutterAbsent
	}
	switch row.kind {
	case diffRowAdd:
		return shared.Theme.Review.DiffGutterAdd
	case diffRowDelete:
		return shared.Theme.Review.DiffGutterRemove
	default:
		return shared.Theme.Review.DiffGutterContext
	}
}

func (m *Model) sourceCommentMarker(line int) (string, lipgloss.Style) {
	comments := make([]domain.Comment, 0)
	for _, comment := range m.comments {
		if comment.Anchor.DocumentID == m.docs[m.active].meta.ID && comment.Anchor.StartLine == line {
			comments = append(comments, comment)
		}
	}
	if len(comments) == 0 {
		return "", shared.Theme.Review.CommentMarker
	}
	marker := "●"
	if len(comments) > 1 {
		marker = fmt.Sprintf("●%d", len(comments))
		if lipgloss.Width(marker) > 2 {
			marker = "●+"
		}
	}
	for _, comment := range comments {
		if comment.ID == m.activeComment {
			return marker, shared.Theme.Review.ActiveCommentMarker
		}
	}
	return marker, shared.Theme.Review.CommentMarker
}

func (m *Model) sourceGutterWidth() int {
	if m.parsedDiffPresentation() != nil {
		digits := m.diffGutterDigits()
		return lipgloss.Width("  " + "  " + " " + strings.Repeat("0", digits) + " " + strings.Repeat("0", digits) + " │ ")
	}
	digits := len(fmt.Sprint(len(m.docs[m.active].lines)))
	return lipgloss.Width("  " + "  " + " " + strings.Repeat("0", digits) + " │ ")
}

func (m *Model) sourceContentWidth() int {
	hFrame, _ := shared.PanelFrame()
	return max(1, documentPanelWidth(m.width)-hFrame-m.sourceGutterWidth())
}

func (m *Model) sourceMaxWidth() int {
	if m.active < 0 || m.active >= len(m.sources) {
		return 0
	}
	width := 0
	if diff := m.parsedDiffPresentation(); diff != nil {
		for _, row := range diff.rows {
			if isVisibleDiffRow(row) && row.kind != diffRowHunkHeader {
				width = max(width, lipgloss.Width(m.sources[m.active].lines[row.patchLine-1]))
			}
		}
		return width
	}
	for _, line := range m.sources[m.active].lines {
		width = max(width, lipgloss.Width(line))
	}
	return width
}

func (m *Model) sourceOverflow() bool {
	return m.sourceXOffsets[m.active] > 0 || m.sourceMaxWidth() > m.sourceContentWidth()
}

func (m *Model) clampSourceOffset() {
	if m.active < 0 || m.active >= len(m.sourceXOffsets) {
		return
	}
	maxOffset := max(0, m.sourceMaxWidth()-m.sourceContentWidth())
	m.sourceXOffsets[m.active] = min(max(m.sourceXOffsets[m.active], 0), maxOffset)
}

func (m *Model) previewContentWidth() int {
	hFrame, _ := shared.PanelFrame()
	return max(documentPanelWidth(m.width)-hFrame-2, 20)
}

func (m *Model) previewRowBudget() int {
	if m.height <= 0 {
		return 0
	}
	return max(1, m.height-10)
}

func previewWindow(rows []string, activeStart, activeEnd, limit int) []string {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}

	start := max(0, activeStart-3)
	activeHeight := activeEnd - activeStart + 1
	if activeHeight > limit {
		start = activeStart
	} else if activeEnd >= start+limit {
		start = activeEnd - limit + 1
	}
	start = min(start, len(rows)-limit)
	return rows[start : start+limit]
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

func commentModalWidth(width int) int {
	return min(72, max(width-4, 1))
}

func (m *Model) commentOverlay(base string) string {
	width, height := m.width, m.height
	if width <= 0 {
		width = max(lipgloss.Width(base), 1)
	}
	if height <= 0 {
		height = max(lipgloss.Height(base), 1)
	}

	title := "New comment"
	metadata := formatLineRange(min(m.cursor, m.rangeEnd), max(m.cursor, m.rangeEnd))
	if m.mode == ModeEditComment {
		title = "Edit comment"
		metadata = m.editing + " · " + metadata
	}
	var content strings.Builder
	content.WriteString(shared.Theme.Review.CommentModalTitle.Render(title + " · " + string(m.commentKind)))
	content.WriteByte('\n')
	content.WriteString(shared.Theme.Review.CommentModalMeta.Render(metadata))
	if m.error != "" {
		content.WriteByte('\n')
		content.WriteString(shared.Theme.Error.Render(m.error))
	}
	content.WriteByte('\n')
	content.WriteString(m.composer.View())
	if m.commentKind == domain.KindSuggestion {
		content.WriteByte('\n')
		content.WriteString(shared.Theme.Review.CommentModalMeta.Render("Replacement"))
		content.WriteByte('\n')
		content.WriteString(m.replacement.View())
	}
	content.WriteByte('\n')
	content.WriteString(shared.Theme.Review.CommentModalHint.Render("enter save · tab change kind · esc close"))

	box := shared.Theme.Review.CommentModal.Width(commentModalWidth(width)).Render(content.String())
	x := max((width-lipgloss.Width(box))/2, 0)
	y := max((height-lipgloss.Height(box))/2, 0)
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(width, height).Compose(comp).Render()
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
	pan := ""
	if m.activeDocumentMode() == DocumentSource {
		pan = " · h/l pan · 0 first col"
		if m.diffNavigationAvailable() {
			pan += " · [/] hunk · z fold"
		}
	}
	return "S finish review · j/k move · {/} document · c new comment · enter open comment · r reviewed" + view + pan + " · esc close"
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
