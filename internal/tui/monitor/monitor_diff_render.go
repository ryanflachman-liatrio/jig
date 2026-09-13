package monitor

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/tui/diffview"
	"jig/internal/tui/shared"
)

// diffTabWidth is the visible width of a leading tab (matching omp's
// DEFAULT_TAB_WIDTH = 3). Non-leading tabs render as plain spaces.
const diffTabWidth = 3

// diffCollapsedHunks bounds the number of hunks displayed when a diff is
// collapsed (`expanded == false`). Excess hunks are summarized in a
// slice-06 hint footer row.
const diffCollapsedHunks = 8

// diffCollapsedLines bounds the number of visible rows (context + add +
// delete) displayed when a diff is collapsed. Excess rows are summarized
// in the same hint footer.
const diffCollapsedLines = 40

// sgrRowTerminator closes SGR 7 (reverse video) and the foreground color
// so the frame padding of any surrounding row cannot inherit inverse or
// a stray foreground. It is appended to every rendered row.
const sgrRowTerminator = "\x1b[27m\x1b[39m"

// diffVisibleRow packages a visible diffview row alongside its
// reconstructed text and the intra-line spans the renderer computed
// for a 1↔1 replacement pass. content holds the pre-visualization
// string (marker stripped) so the intra-line pass can compare them
// without having to strip ANSI escapes.
type diffVisibleRow struct {
	presentationIdx int
	kind            diffview.RowKind
	hunkIndex       int
	oldLine         int
	newLine         int
	hasOld          bool
	hasNew          bool
	content         string
	// intraOld/intraNew record the [start, end) byte range within
	// content that should be highlighted with reverse video. A zero
	// pair means "no intra-line highlight" (multi-line change block
	// or leading-whitespace-only diff).
	intraOld [2]int
	intraNew [2]int
}

// renderDiffRows projects a *diffProjection into a slice of styled,
// width-bounded string rows suitable for insertion below the exchange
// card. The producer is intentionally free of Monitor state so a future
// card-section re-host (slice 05) can call it with a different width
// and no other change.
//
// width is the total visible cells available per row (the diff
// section's content column, e.g. `m.transcriptInnerW - 4` for today's
// four-space writer). expandKey is the live per-item Toggle binding's
// help text. insetRenderer is used to batch-highlight consecutive
// context rows; when nil, context rows fall back to Diff.Context
// foreground styling.
func renderDiffRows(
	proj *diffProjection,
	path string,
	width int,
	expanded bool,
	insetRenderer *glamour.TermRenderer,
	expandKey string,
) []string {
	if proj == nil || proj.Presentation == nil || len(proj.Presentation.Hunks) == 0 {
		return nil
	}
	if width < 4 {
		width = 4
	}
	pres := proj.Presentation

	gw := gutterWidth(pres)
	contentWidth := width - gw - 1 // one cell for `│`
	if contentWidth < 1 {
		contentWidth = 1
	}

	visible := collectVisibleRows(proj)
	visible, hiddenHunks, hiddenLines := applyCollapse(visible, expanded, pres.Hunks)
	applyIntraLineDiff(visible)

	contextByIdx := highlightContext(visible, path, contentWidth, insetRenderer)

	rows := make([]string, 0, len(visible)+1)
	prevLineToken := ""
	prevHunk := -1
	for i, vr := range visible {
		if vr.hunkIndex != prevHunk {
			prevLineToken = "" // suppression never leaks across hunks
			prevHunk = vr.hunkIndex
		}
		marker := rowMarker(vr.kind)
		lineToken := formatLineToken(vr)
		showNumber := lineToken != prevLineToken
		gutter := formatDiffGutter(marker, lineToken, showNumber, gw)
		if lineToken != "" {
			prevLineToken = lineToken
		}

		body := visualizeIndent(vr.content)
		switch vr.kind {
		case diffview.RowContext:
			if styled, ok := contextByIdx[i]; ok {
				body = styled
			} else {
				body = shared.Theme.Diff.Context.Render(body)
			}
		case diffview.RowAdd:
			body = applyIntraline(body, vr.content, vr.intraNew, shared.Theme.Diff.Add)
		case diffview.RowDelete:
			body = applyIntraline(body, vr.content, vr.intraOld, shared.Theme.Diff.Remove)
		}

		rendered := shared.Theme.Diff.Gutter.Render(gutter) + body
		rows = append(rows, wrapDiffRow(rendered, gw, contentWidth)...)
	}

	if hiddenHunks > 0 || hiddenLines > 0 {
		rows = append(rows, buildCollapseFooter(hiddenHunks, hiddenLines, expanded, expandKey))
	}

	return rows
}

// gutterWidth returns the fused marker+line-number width, floored at 3.
// The marker occupies one cell and the number is right-aligned into the
// remaining cells so a 5-line file renders as `+42` (2 digits + marker,
// padded to 3) and a 1000-line file renders as `+1000` (padded to 5).
func gutterWidth(pres *diffview.Presentation) int {
	maxLine := 0
	for _, row := range pres.Rows {
		if row.OldLine > maxLine {
			maxLine = row.OldLine
		}
		if row.NewLine > maxLine {
			maxLine = row.NewLine
		}
	}
	digits := 1
	for maxLine >= 10 {
		digits++
		maxLine /= 10
	}
	w := digits + 1 // marker character
	if w < 3 {
		return 3
	}
	return w
}

// formatDiffGutter returns `<pad><marker><line-no>│` with the marker
// and number concatenated and right-aligned as one token. Blank-line-
// number rows (`hasLineNo == false`) render as `<pad><marker>│`.
func formatDiffGutter(marker byte, lineNo string, hasLineNo bool, width int) string {
	if width < 3 {
		width = 3
	}
	token := string(marker)
	if hasLineNo {
		token = string(marker) + lineNo
	}
	if pad := width - len(token); pad > 0 {
		token = strings.Repeat(" ", pad) + token
	}
	return token + "│"
}

// formatLineToken returns the row's preferred line-number text. Add
// rows use the new line number, delete rows use the old, and context
// rows use the new (which is the value operators think of when
// navigating). Rows without a number return "".
func formatLineToken(vr diffVisibleRow) string {
	switch vr.kind {
	case diffview.RowAdd:
		if vr.hasNew {
			return strconv.Itoa(vr.newLine)
		}
	case diffview.RowDelete:
		if vr.hasOld {
			return strconv.Itoa(vr.oldLine)
		}
	case diffview.RowContext:
		if vr.hasNew {
			return strconv.Itoa(vr.newLine)
		}
		if vr.hasOld {
			return strconv.Itoa(vr.oldLine)
		}
	}
	return ""
}

func rowMarker(kind diffview.RowKind) byte {
	switch kind {
	case diffview.RowAdd:
		return '+'
	case diffview.RowDelete:
		return '-'
	default:
		return ' '
	}
}

// collectVisibleRows walks the presentation, keeps only rows the diff
// card should paint (context, add, delete), and reconstructs each
// row's textual content from the projection's retained patch source.
// Hunk headers are dropped: the current slice renders visible code
// rows without the `@@` gap.
func collectVisibleRows(proj *diffProjection) []diffVisibleRow {
	pres := proj.Presentation
	rows := make([]diffVisibleRow, 0, len(pres.Rows))
	for i, r := range pres.Rows {
		switch r.Kind {
		case diffview.RowContext, diffview.RowAdd, diffview.RowDelete:
		default:
			continue
		}
		rows = append(rows, diffVisibleRow{
			presentationIdx: i,
			kind:            r.Kind,
			hunkIndex:       r.HunkIndex,
			oldLine:         r.OldLine,
			newLine:         r.NewLine,
			hasOld:          r.HasOld,
			hasNew:          r.HasNew,
			content:         patchRowContent(proj, r.PatchLine),
		})
	}
	return rows
}

// patchRowContent returns the visible content of a diffview row,
// stripping the leading marker character (`+`, `-`, ` `) that the
// unified patch preserves. The projection retains the raw patch as
// PatchLines so the renderer has direct string access without a
// wire-format change.
func patchRowContent(proj *diffProjection, patchLine int) string {
	if proj == nil {
		return ""
	}
	idx := patchLine - 1
	if idx < 0 || idx >= len(proj.PatchLines) {
		return ""
	}
	line := proj.PatchLines[idx]
	if line == "" {
		return ""
	}
	// The first byte carries the diff marker (` `, `+`, `-`). Strip it
	// once — any embedded marker later in the string is content.
	return strings.TrimSuffix(line[1:], "\r")
}

// applyCollapse enforces the hunk and line budgets when the diff is
// not expanded. It keeps the first eight hunks and the first forty
// visible rows, returning the count of clipped hunks and lines for
// the footer.
func applyCollapse(rows []diffVisibleRow, expanded bool, hunks []diffview.Hunk) ([]diffVisibleRow, int, int) {
	if expanded {
		return rows, 0, 0
	}
	hiddenHunks := 0
	if len(hunks) > diffCollapsedHunks {
		hiddenHunks = len(hunks) - diffCollapsedHunks
		trimmed := rows[:0]
		for _, row := range rows {
			if row.hunkIndex >= diffCollapsedHunks {
				continue
			}
			trimmed = append(trimmed, row)
		}
		rows = trimmed
	}
	hiddenLines := 0
	if len(rows) > diffCollapsedLines {
		hiddenLines = len(rows) - diffCollapsedLines
		rows = rows[:diffCollapsedLines]
	}
	return rows, hiddenHunks, hiddenLines
}

// applyIntraLineDiff finds 1↔1 delete/add replacement pairs among the
// visible rows and populates their intra-line spans. Multi-line change
// blocks receive no intra-line highlight (slice 07 FR-07.9).
func applyIntraLineDiff(rows []diffVisibleRow) {
	i := 0
	for i < len(rows) {
		if rows[i].kind != diffview.RowDelete {
			i++
			continue
		}
		delStart := i
		for i < len(rows) && rows[i].kind == diffview.RowDelete {
			i++
		}
		delEnd := i
		addStart := i
		for i < len(rows) && rows[i].kind == diffview.RowAdd {
			i++
		}
		addEnd := i
		if delEnd-delStart != 1 || addEnd-addStart != 1 {
			continue
		}
		oldSpan, newSpan := intraLineSpans(rows[delStart].content, rows[addStart].content)
		rows[delStart].intraOld = oldSpan
		rows[addStart].intraNew = newSpan
	}
}

// intraLineSpans returns the differing byte ranges within old and new,
// excluding leading whitespace so indentation is never inverted. When
// the entire diff is confined to leading whitespace (e.g. an indent
// bump) the returned spans are zero pairs so no intra-line highlight
// is applied.
func intraLineSpans(oldText, newText string) ([2]int, [2]int) {
	oldStart, newStart := commonPrefix(oldText, newText)
	oldEnd, newEnd := commonSuffix(oldText[oldStart:], newText[newStart:])
	oldEnd = len(oldText) - oldEnd
	newEnd = len(newText) - newEnd
	// Push the span past any shared leading whitespace so an indent
	// difference does not invert the leading run.
	lead := leadingWhitespaceLen(oldText)
	if lead > oldStart {
		oldStart = lead
	}
	lead = leadingWhitespaceLen(newText)
	if lead > newStart {
		newStart = lead
	}
	if oldStart >= oldEnd && newStart >= newEnd {
		return [2]int{}, [2]int{}
	}
	if oldStart > oldEnd {
		oldStart = oldEnd
	}
	if newStart > newEnd {
		newStart = newEnd
	}
	return [2]int{oldStart, oldEnd}, [2]int{newStart, newEnd}
}

func commonPrefix(a, b string) (int, int) {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	// Snap back to a valid UTF-8 boundary.
	for i > 0 && !utf8.ValidString(a[:i]) {
		i--
	}
	return i, i
}

func commonSuffix(a, b string) (int, int) {
	i := 0
	for i < len(a) && i < len(b) && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	for i > 0 && !utf8.ValidString(a[len(a)-i:]) {
		i--
	}
	return i, i
}

func leadingWhitespaceLen(s string) int {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// applyIntraline wraps the changed span of body in the intra-line style
// while retaining the surrounding row style. body is the pre-highlight
// content (post-indent visualization), raw is the un-visualized source
// (marker stripped) used for span math. When span is a zero pair the
// row is styled uniformly.
func applyIntraline(body, raw string, span [2]int, rowStyle lipgloss.Style) string {
	if span == [2]int{} || span[0] >= span[1] {
		return rowStyle.Render(body)
	}
	// Translate raw-string byte indices to indices within the
	// indent-visualized body. Indent visualization only expands
	// leading whitespace, so any span that starts past the leading
	// run keeps its offsets after visualization.
	leadRaw := leadingWhitespaceLen(raw)
	leadBody := leadingIndentWidth(body)
	shift := leadBody - leadRaw
	if span[0] < leadRaw {
		span[0] = leadRaw
	}
	start := span[0] + shift
	end := span[1] + shift
	if start < 0 {
		start = 0
	}
	if end > len(body) {
		end = len(body)
	}
	if start >= end {
		return rowStyle.Render(body)
	}
	pre := body[:start]
	mid := body[start:end]
	post := body[end:]
	return rowStyle.Render(pre) + rowStyle.Inherit(shared.Theme.Diff.Intraline).Render(mid) + rowStyle.Render(post)
}

// leadingIndentWidth returns the byte length of the leading visualized
// indent (space middle-dots and tab arrows) at the start of body. The
// visualizer only expands the leading run, so any character past this
// point is at its original position.
func leadingIndentWidth(body string) int {
	i := 0
	for i < len(body) {
		if strings.HasPrefix(body[i:], "\x1b[2m·\x1b[22m") {
			i += len("\x1b[2m·\x1b[22m")
			continue
		}
		if strings.HasPrefix(body[i:], "\x1b[2m→"+strings.Repeat(" ", diffTabWidth-1)+"\x1b[22m") {
			i += len("\x1b[2m→" + strings.Repeat(" ", diffTabWidth-1) + "\x1b[22m")
			continue
		}
		break
	}
	return i
}

// visualizeIndent replaces a leading run of spaces and tabs with dim
// middle-dots and arrows so operators can see indentation. Non-leading
// tabs render as plain spaces so mid-line tabs do not glitch the
// gutter alignment.
func visualizeIndent(content string) string {
	var b strings.Builder
	i := 0
	for i < len(content) {
		switch content[i] {
		case ' ':
			b.WriteString("\x1b[2m·\x1b[22m")
			i++
		case '\t':
			b.WriteString("\x1b[2m→")
			for j := 0; j < diffTabWidth-1; j++ {
				b.WriteByte(' ')
			}
			b.WriteString("\x1b[22m")
			i++
		default:
			// Trailing content: preserve as-is; expand any embedded
			// tab into plain spaces so wrap math stays accurate.
			for j := i; j < len(content); j++ {
				if content[j] == '\t' {
					b.WriteString(content[i:j])
					for k := 0; k < diffTabWidth; k++ {
						b.WriteByte(' ')
					}
					i = j + 1
					j = i - 1
					continue
				}
			}
			b.WriteString(content[i:])
			return b.String()
		}
	}
	return b.String()
}

// highlightContext batches consecutive context runs through the inset
// glamour renderer so multi-line grammars tokenize as one unit. It
// returns a map keyed on the visible-row index whose value is the
// styled row (indent visualization already applied). Rows outside the
// map fall back to Diff.Context foreground styling.
func highlightContext(rows []diffVisibleRow, path string, contentWidth int, insetRenderer *glamour.TermRenderer) map[int]string {
	out := map[int]string{}
	if insetRenderer == nil {
		return out
	}
	i := 0
	for i < len(rows) {
		if rows[i].kind != diffview.RowContext {
			i++
			continue
		}
		start := i
		for i < len(rows) && rows[i].kind == diffview.RowContext {
			i++
		}
		end := i
		lines := make([]string, 0, end-start)
		for _, r := range rows[start:end] {
			lines = append(lines, r.content)
		}
		block := strings.Join(lines, "\n")
		rendered, err := insetRenderer.Render(fencedCode(path, block))
		if err != nil {
			continue
		}
		rendered = stripBlankEdges(rendered)
		styled := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
		if len(styled) != end-start {
			// Glamour wrapped a row; skip highlighting for this run
			// and fall back to plain styling row-by-row.
			continue
		}
		for j, line := range styled {
			out[start+j] = visualizeIndentStyled(line)
		}
	}
	return out
}

// visualizeIndentStyled applies indent visualization to a pre-styled
// context row emitted by the inset renderer. Chroma preserves the
// leading indent as plain space/tab characters before the first token
// escape, so the visualization pass replaces those leading runs.
func visualizeIndentStyled(row string) string {
	i := 0
	for i < len(row) && (row[i] == ' ' || row[i] == '\t') {
		i++
	}
	if i == 0 {
		return row
	}
	return visualizeIndent(row[:i]) + row[i:]
}

// wrapDiffRow hard-wraps a rendered row at the diff section's content
// width, emitting continuation rows with a blank gutter followed by
// the `│` column. Every emitted row is terminated with the SGR closer
// so the surrounding frame padding cannot inherit reverse video or a
// stray foreground color.
func wrapDiffRow(row string, gutterWidth, contentWidth int) []string {
	rowWidth := lipgloss.Width(row)
	frame := gutterWidth + 1
	if rowWidth <= frame+contentWidth {
		return []string{row + sgrRowTerminator}
	}
	wrapped := ansi.Hardwrap(row, frame+contentWidth, true)
	segments := strings.Split(wrapped, "\n")
	out := make([]string, 0, len(segments))
	blankGutter := shared.Theme.Diff.Gutter.Render(strings.Repeat(" ", gutterWidth) + "│")
	for i, seg := range segments {
		if i == 0 {
			out = append(out, seg+sgrRowTerminator)
			continue
		}
		out = append(out, blankGutter+ansi.Strip(seg)+sgrRowTerminator)
	}
	return out
}

// buildCollapseFooter composes the single hint row that summarizes
// clipped hunks and lines through slice-06 vocabulary. The leading
// ellipsis appears once at the start of the composed row.
func buildCollapseFooter(hiddenHunks, hiddenLines int, expanded bool, expandKey string) string {
	var hunkPhrase, linePhrase string
	if hiddenHunks > 0 {
		hunkPhrase = shared.MoreItems(hiddenHunks, "hunk", "hunks")
	}
	if hiddenLines > 0 {
		linePhrase = shared.MoreItems(hiddenLines, "line", "lines")
		if hunkPhrase != "" {
			linePhrase = strings.TrimPrefix(linePhrase, "… ")
		}
	}
	var count string
	switch {
	case hunkPhrase != "" && linePhrase != "":
		count = hunkPhrase + ", " + linePhrase
	case hunkPhrase != "":
		count = hunkPhrase
	default:
		count = linePhrase
	}
	hint := shared.ExpandHint(expanded, hiddenHunks+hiddenLines > 0, expandKey)
	return shared.Theme.Chat.Hint.Render(shared.HintLine(count, hint))
}
