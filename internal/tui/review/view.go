package review

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

func (m Model) View() string {
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
	if m.width > 0 && m.width < 90 {
		return strings.Join([]string{header, left, right, m.footer()}, "\n")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right) + "\n" + m.footer()
}

func (m Model) documentList() string {
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

func (m Model) documentView() string {
	d := m.docs[m.active]
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s · %s", d.meta.Label, strings.ToUpper(formatMode(m.documentMode))))
	if m.mode == ModeSelectRange {
		b.WriteString(fmt.Sprintf(" · L%d–%d", min(m.cursor, m.rangeEnd), max(m.cursor, m.rangeEnd)))
	}
	b.WriteByte('\n')
	start := m.cursor - 8
	if start < 1 {
		start = 1
	}
	end := start + max(8, m.height-10)
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
	if len(m.comments) > 0 {
		b.WriteString("\n")
		b.WriteString(shared.Theme.StatusLine.Render("Comments"))
		b.WriteByte('\n')
		for _, c := range m.comments {
			if c.Anchor.DocumentID == d.meta.ID {
				b.WriteString(fmt.Sprintf("%s · %s · lines %d–%d\n", c.ID, c.Kind, c.Anchor.StartLine, c.Anchor.EndLine))
				b.WriteString("  " + strings.ReplaceAll(c.Body, "\n", " ") + "\n")
			}
		}
	}
	return shared.Panel(d.meta.Label, b.String(), max(42, m.width-listWidth(m.width)-4), max(12, m.height-2), m.mode != ModeSummary)
}

func (m Model) summaryView() string {
	var b strings.Builder
	b.WriteString(shared.Theme.Title.Render("Review summary"))
	b.WriteString("\n\n")
	for _, d := range m.docs {
		mark := "unreviewed"
		if m.reviewed[d.meta.ID] {
			mark = "reviewed"
		}
		b.WriteString(fmt.Sprintf("%s — %s\n", d.meta.Label, mark))
	}
	b.WriteString(fmt.Sprintf("\nVerdict: %s\n\n%s", m.verdict, m.summary.View()))
	return shared.Panel("Summary", b.String(), max(42, m.width-2), max(12, m.height-2), true)
}
func (m Model) footer() string {
	if m.CapturesText() {
		return "enter save · esc cancel · tab change kind"
	}
	return "j/k move · {/} document · c comment · r reviewed · S summary · esc close"
}
func formatMode(mode DocumentMode) string {
	if mode == DocumentPreview {
		return "preview"
	}
	return "source"
}
func listWidth(width int) int {
	if width > 0 && width < 90 {
		return width
	}
	return 30
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
