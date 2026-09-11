// Package review: clipboard.go extracts the exact source text for a Review
// copy request without re-reading disk or lifting from the rendered viewport.
// Every slice comes from the immutable `document.meta.Content` captured when
// the review was loaded, so a subsequent working-tree change never leaks into
// what the operator copied and CRLF/LF line terminators and final-newline
// presence survive the round trip byte-for-byte.
//
// Requests flow through the shared root-owned copy admission (spec 23):
//   - y  copies the current selectable unit (line, range, block, hunk).
//   - Y  copies the whole current source (document, or one file in a diff).
//
// This file only builds `shared.ClipboardRequest` values; it never touches the
// clipboard directly and never dispatches an OSC52 command. Availability
// checks fail fast with the standard rejection reasons rather than dispatching
// an empty or malformed payload.
package review

import (
	"fmt"
	"strings"

	"jig/internal/tui/shared"
)

// CopyItemRequest builds a request for the current selectable unit. The
// mapping depends on the active mode: source-mode current line, an explicit
// range in ModeSelectRange, the current hunk when a parsed diff has one, or
// the current preview block in DocumentPreview. The request captures every
// piece of data it needs before it is returned so the loader is safe to run
// off the synchronous key handler.
func (m Model) CopyItemRequest() (shared.ClipboardRequest, bool) {
	if len(m.docs) == 0 || m.active < 0 || m.active >= len(m.docs) {
		return shared.ClipboardRequest{}, false
	}
	doc := m.docs[m.active]
	if m.activeDocumentMode() == DocumentPreview {
		return m.copyPreviewBlockRequest(doc), true
	}
	if diff := m.parsedDiffPresentation(); diff != nil && m.mode != ModeSelectRange {
		if hunk := m.hunkForPatchLine(m.cursor); hunk != nil {
			return m.copyHunkRequest(doc, hunk), true
		}
	}
	if m.mode == ModeSelectRange {
		start, end := m.cursor, m.rangeEnd
		if start > end {
			start, end = end, start
		}
		return m.copySourceRangeRequest(doc, start, end), true
	}
	return m.copySourceLineRequest(doc, m.cursor), true
}

// CopyAllRequest builds a request for the whole current source: the active
// document for source/preview modes, or the current file's full diff section
// when the source is a parsed multi-file patch and the cursor is inside a
// tracked file span.
func (m Model) CopyAllRequest() (shared.ClipboardRequest, bool) {
	if len(m.docs) == 0 || m.active < 0 || m.active >= len(m.docs) {
		return shared.ClipboardRequest{}, false
	}
	doc := m.docs[m.active]
	if diff := m.parsedDiffPresentation(); diff != nil && m.mode != ModeSelectRange {
		if file := m.fileForPatchLine(m.cursor); file != nil {
			return m.copyFileDiffRequest(doc, file.StartPatchLine, file.EndPatchLine, file.Name), true
		}
	}
	return m.copyDocumentRequest(doc), true
}

// MatchesCopyItem/MatchesCopyAll expose keybind matching so the Monitor gate
// can dispatch copy requests without duplicating binding data.
func (m Model) MatchesCopyItem(key string) bool { return key == "y" }
func (m Model) MatchesCopyAll(key string) bool  { return key == "Y" }

// copySourceLineRequest copies exactly one source line, preserving the
// original CR/LF terminator when the source ends the line that way and
// including a trailing newline when the source has one on that line.
func (m Model) copySourceLineRequest(doc document, line int) shared.ClipboardRequest {
	label := fmt.Sprintf("L%d in %s", line, doc.meta.Label)
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewLine, Label: label}
	content := doc.meta.Content
	total := len(doc.lines)
	captured := line
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			payload, err := extractSourceLineRange(content, captured, captured, total)
			return shared.ClipboardPayload{Payload: payload, Err: err}
		},
	}
}

// copySourceRangeRequest copies an inclusive source line range. Start and end
// are normalized by the caller; the loader validates against captured line
// counts so a subsequent working-file change cannot slice out of bounds.
func (m Model) copySourceRangeRequest(doc document, start, end int) shared.ClipboardRequest {
	label := fmt.Sprintf("L%d–L%d in %s", start, end, doc.meta.Label)
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewRange, Label: label}
	content := doc.meta.Content
	total := len(doc.lines)
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			payload, err := extractSourceLineRange(content, start, end, total)
			return shared.ClipboardPayload{Payload: payload, Err: err}
		},
	}
}

// copyDocumentRequest copies the whole immutable document content exactly as
// the round was loaded — no BOM, no trailing normalization. When the source
// is empty the loader rejects rather than emitting a whitespace payload.
func (m Model) copyDocumentRequest(doc document) shared.ClipboardRequest {
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewDocument, Label: doc.meta.Label}
	content := doc.meta.Content
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			if content == "" {
				return shared.ClipboardPayload{Err: shared.ErrClipboardEmpty}
			}
			return shared.ClipboardPayload{Payload: content}
		},
	}
}

// copyPreviewBlockRequest slices out the source lines that back the currently
// selected Markdown preview block, so a paste round-trips the raw source
// (fences, headings, list markers) rather than rendered ANSI.
func (m Model) copyPreviewBlockRequest(doc document) shared.ClipboardRequest {
	blocks := m.previews[m.active].blocks
	if m.previewBlock < 0 || m.previewBlock >= len(blocks) {
		target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewBlock, Label: doc.meta.Label}
		return shared.ClipboardRequest{
			Target: target,
			Loader: func() shared.ClipboardPayload {
				return shared.ClipboardPayload{Err: shared.ErrClipboardUnavailable}
			},
		}
	}
	block := blocks[m.previewBlock]
	label := fmt.Sprintf("L%d–L%d in %s", block.startLine, block.endLine, doc.meta.Label)
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewBlock, Label: label}
	content := doc.meta.Content
	total := len(doc.lines)
	start, end := block.startLine, block.endLine
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			payload, err := extractSourceLineRange(content, start, end, total)
			return shared.ClipboardPayload{Payload: payload, Err: err}
		},
	}
}

// copyHunkRequest copies one hunk's exact patch text, including the hunk
// header and any no-newline marker. It reuses source-offset slicing so
// terminators survive.
func (m Model) copyHunkRequest(doc document, hunk *diffHunk) shared.ClipboardRequest {
	label := fmt.Sprintf("hunk %d in %s", hunk.ordinal, hunk.fileName)
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewHunk, Label: label}
	content := doc.meta.Content
	total := len(doc.lines)
	start, end := hunk.startPatchLine, hunk.endPatchLine
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			payload, err := extractSourceLineRange(content, start, end, total)
			return shared.ClipboardPayload{Payload: payload, Err: err}
		},
	}
}

// copyFileDiffRequest copies one file's full section of a multi-file patch,
// including its introducing metadata and every hunk. The span was computed by
// the diffview parser so no header inference happens here.
func (m Model) copyFileDiffRequest(doc document, start, end int, name string) shared.ClipboardRequest {
	label := fmt.Sprintf("%s in %s", name, doc.meta.Label)
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceReviewFileDiff, Label: label}
	content := doc.meta.Content
	total := len(doc.lines)
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			payload, err := extractSourceLineRange(content, start, end, total)
			return shared.ClipboardPayload{Payload: payload, Err: err}
		},
	}
}

// fileForPatchLine returns the diffview.File whose span contains the cursor
// or nil when the cursor is outside every file (parse error, empty diff, or
// cursor placed beyond the last file's rows).
func (m *Model) fileForPatchLine(line int) *fileSpan {
	diff := m.parsedDiffPresentation()
	if diff == nil || diff.display == nil {
		return nil
	}
	for i := range diff.display.Files {
		f := diff.display.Files[i]
		if line >= f.StartPatchLine && line <= f.EndPatchLine {
			return &fileSpan{
				StartPatchLine: f.StartPatchLine,
				EndPatchLine:   f.EndPatchLine,
				Name:           f.Name,
			}
		}
	}
	return nil
}

// fileSpan mirrors the diffview File span but lets this file own the small
// value type it returns so callers cannot mutate the parser's slice.
type fileSpan struct {
	StartPatchLine, EndPatchLine int
	Name                         string
}

// extractSourceLineRange slices `content` by 1-indexed inclusive line numbers
// while preserving the original line terminators. It walks the raw bytes and
// counts real \n boundaries so a CRLF source keeps its \r; a source without a
// final newline keeps that absence too.
func extractSourceLineRange(content string, start, end, totalLines int) (string, error) {
	if content == "" {
		return "", shared.ErrClipboardEmpty
	}
	if start < 1 || end < 1 {
		return "", shared.ErrClipboardUnavailable
	}
	if start > totalLines || end > totalLines {
		return "", shared.ErrClipboardUnavailable
	}
	if start > end {
		start, end = end, start
	}
	startOffset := lineOffset(content, start)
	// End offset: one byte past the terminator of `end` (or end of content when
	// `end` is the last line and there is no trailing newline).
	endOffset := lineEndOffset(content, end)
	if startOffset < 0 || endOffset < 0 || startOffset > endOffset {
		return "", shared.ErrClipboardUnavailable
	}
	return content[startOffset:endOffset], nil
}

// lineOffset returns the byte offset of the first character of line n
// (1-indexed) or -1 if the line does not exist. Lines are split on '\n'; any
// preceding '\r' belongs to the previous line so CRLF terminators are
// preserved as-is when the slice starts on the next line.
func lineOffset(content string, n int) int {
	if n <= 0 {
		return -1
	}
	if n == 1 {
		return 0
	}
	count := 1
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			count++
			if count == n {
				return i + 1
			}
		}
	}
	return -1
}

// lineEndOffset returns the offset one byte past the '\n' that terminates
// line n; when line n has no terminator (final line without trailing
// newline), returns len(content).
func lineEndOffset(content string, n int) int {
	if n <= 0 {
		return -1
	}
	start := lineOffset(content, n)
	if start < 0 {
		return -1
	}
	idx := strings.IndexByte(content[start:], '\n')
	if idx < 0 {
		return len(content)
	}
	return start + idx + 1
}
