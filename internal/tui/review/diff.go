package review

import (
	"fmt"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type diffRowKind uint8

const (
	diffRowMetadata diffRowKind = iota
	diffRowHunkHeader
	diffRowContext
	diffRowDelete
	diffRowAdd
	diffRowNoNewline
)

type diffRow struct {
	patchLine            int
	fileIndex, hunkIndex int
	kind                 diffRowKind
	oldLine, newLine     int
	hasOld, hasNew       bool
}

type diffHunk struct {
	fileIndex                    int
	ordinal                      int
	startPatchLine, endPatchLine int
	header                       string
}

type diffPresentation struct {
	rows     []diffRow
	hunks    []diffHunk
	folded   map[int]bool
	parseErr error
}

type parsedDiffHunk struct {
	fileIndex int
	hunkIndex int
	fragment  *gitdiff.TextFragment
}

func buildDiffPresentation(content string, lines []string) *diffPresentation {
	presentation := &diffPresentation{rows: newDiffRows(len(lines)), folded: make(map[int]bool)}
	files, _, err := gitdiff.Parse(strings.NewReader(content))
	if err != nil {
		presentation.parseErr = fmt.Errorf("parse diff: %w", err)
		return presentation
	}
	if len(files) == 0 {
		presentation.parseErr = fmt.Errorf("parse diff: no file changes found")
		return presentation
	}

	var hunks []parsedDiffHunk
	for fileIndex, file := range files {
		for hunkIndex, fragment := range file.TextFragments {
			hunks = append(hunks, parsedDiffHunk{fileIndex: fileIndex, hunkIndex: hunkIndex, fragment: fragment})
		}
	}
	if err := projectDiffHunks(presentation, lines, hunks); err != nil {
		presentation.parseErr = fmt.Errorf("project diff: %w", err)
	}
	return presentation
}

func newDiffRows(count int) []diffRow {
	rows := make([]diffRow, count)
	for i := range rows {
		rows[i] = diffRow{patchLine: i + 1, fileIndex: -1, hunkIndex: -1, kind: diffRowMetadata}
	}
	return rows
}

func projectDiffHunks(presentation *diffPresentation, lines []string, hunks []parsedDiffHunk) error {
	nextLine := 0
	for ordinal, parsed := range hunks {
		headerLine, err := findNextHunkHeader(lines, nextLine)
		if err != nil {
			return err
		}
		if err := validateHunkHeader(lines[headerLine], parsed.fragment); err != nil {
			return fmt.Errorf("patch line %d: %w", headerLine+1, err)
		}

		row := &presentation.rows[headerLine]
		row.fileIndex, row.hunkIndex, row.kind = parsed.fileIndex, parsed.hunkIndex, diffRowHunkHeader
		presentation.hunks = append(presentation.hunks, diffHunk{
			fileIndex: parsed.fileIndex, ordinal: ordinal + 1, startPatchLine: headerLine + 1,
			header: strings.TrimSuffix(lines[headerLine], "\r"),
		})

		nextLine = headerLine + 1
		oldLine, newLine := parsed.fragment.OldPosition, parsed.fragment.NewPosition
		for _, expected := range parsed.fragment.Lines {
			for nextLine < len(lines) && isNoNewlineMarker(lines[nextLine]) {
				markNoNewlineRow(&presentation.rows[nextLine], parsed.fileIndex, parsed.hunkIndex)
				nextLine++
			}
			if nextLine >= len(lines) {
				return fmt.Errorf("hunk ending at patch line %d is missing a body row", headerLine+1)
			}
			if err := projectDiffLine(&presentation.rows[nextLine], lines[nextLine], expected, parsed.fileIndex, parsed.hunkIndex, oldLine, newLine); err != nil {
				return fmt.Errorf("patch line %d: %w", nextLine+1, err)
			}
			if expected.Old() {
				oldLine++
			}
			if expected.New() {
				newLine++
			}
			nextLine++
		}
		for nextLine < len(lines) && isNoNewlineMarker(lines[nextLine]) {
			markNoNewlineRow(&presentation.rows[nextLine], parsed.fileIndex, parsed.hunkIndex)
			nextLine++
		}
		presentation.hunks[len(presentation.hunks)-1].endPatchLine = nextLine
	}

	for line := nextLine; line < len(lines); line++ {
		if strings.HasPrefix(strings.TrimSuffix(lines[line], "\r"), "@@") {
			return fmt.Errorf("patch line %d is an unparsed hunk header", line+1)
		}
	}
	return nil
}

func findNextHunkHeader(lines []string, start int) (int, error) {
	for line := start; line < len(lines); line++ {
		if strings.HasPrefix(strings.TrimSuffix(lines[line], "\r"), "@@") && !isHunkHeader(lines[line]) {
			return 0, fmt.Errorf("patch line %d has an invalid hunk header", line+1)
		}
		if isHunkHeader(lines[line]) {
			return line, nil
		}
	}
	return 0, fmt.Errorf("parsed hunk has no matching patch header")
}

func isHunkHeader(line string) bool {
	return strings.HasPrefix(strings.TrimSuffix(line, "\r"), "@@ -")
}

func isNoNewlineMarker(line string) bool {
	return strings.HasPrefix(strings.TrimSuffix(line, "\r"), "\\ ")
}

func validateHunkHeader(line string, fragment *gitdiff.TextFragment) error {
	line = strings.TrimSuffix(line, "\r")
	if !strings.HasPrefix(line, "@@ -") {
		return fmt.Errorf("not a hunk header")
	}
	end := strings.Index(line, " @@")
	if end < 0 {
		return fmt.Errorf("invalid hunk header")
	}
	ranges := strings.Split(strings.TrimPrefix(line[:end], "@@ -"), " +")
	if len(ranges) != 2 {
		return fmt.Errorf("invalid hunk ranges")
	}
	oldPosition, oldLines, err := parseHunkRange(ranges[0])
	if err != nil {
		return fmt.Errorf("invalid old range: %w", err)
	}
	newPosition, newLines, err := parseHunkRange(ranges[1])
	if err != nil {
		return fmt.Errorf("invalid new range: %w", err)
	}
	if oldPosition != fragment.OldPosition || oldLines != fragment.OldLines || newPosition != fragment.NewPosition || newLines != fragment.NewLines {
		return fmt.Errorf("hunk range does not match parsed fragment")
	}
	return nil
}

func parseHunkRange(value string) (int64, int64, error) {
	parts := strings.Split(value, ",")
	if len(parts) > 2 || len(parts) == 0 {
		return 0, 0, fmt.Errorf("invalid range %q", value)
	}
	var position, count int64
	if _, err := fmt.Sscan(parts[0], &position); err != nil || position < 0 {
		return 0, 0, fmt.Errorf("invalid position %q", parts[0])
	}
	count = 1
	if len(parts) == 2 {
		if _, err := fmt.Sscan(parts[1], &count); err != nil || count < 0 {
			return 0, 0, fmt.Errorf("invalid count %q", parts[1])
		}
	}
	return position, count, nil
}

func projectDiffLine(row *diffRow, raw string, expected gitdiff.Line, fileIndex, hunkIndex int, oldLine, newLine int64) error {
	if len(raw) == 0 {
		if expected.Op != gitdiff.OpContext || diffLineText(expected.Line) != "" {
			return fmt.Errorf("expected %q row", expected.Op.String())
		}
	} else {
		if raw[0] != expected.Op.String()[0] {
			return fmt.Errorf("expected %q row", expected.Op.String())
		}
		if strings.TrimSuffix(raw[1:], "\r") != diffLineText(expected.Line) {
			return fmt.Errorf("body text does not match parsed fragment")
		}
	}

	row.fileIndex, row.hunkIndex = fileIndex, hunkIndex
	switch expected.Op {
	case gitdiff.OpContext:
		row.kind, row.oldLine, row.newLine, row.hasOld, row.hasNew = diffRowContext, int(oldLine), int(newLine), true, true
	case gitdiff.OpDelete:
		row.kind, row.oldLine, row.hasOld = diffRowDelete, int(oldLine), true
	case gitdiff.OpAdd:
		row.kind, row.newLine, row.hasNew = diffRowAdd, int(newLine), true
	default:
		return fmt.Errorf("unknown line operation %q", expected.Op)
	}
	return nil
}

func diffLineText(line string) string {
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

func markNoNewlineRow(row *diffRow, fileIndex, hunkIndex int) {
	row.fileIndex, row.hunkIndex, row.kind = fileIndex, hunkIndex, diffRowNoNewline
}
