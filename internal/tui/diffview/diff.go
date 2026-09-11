// Package diffview provides the parsed, displayable form of a unified diff.
package diffview

import (
	"fmt"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"

	"jig/internal/tui/shared"
)

type RowKind uint8

const (
	RowMetadata RowKind = iota
	RowHunkHeader
	RowContext
	RowDelete
	RowAdd
	RowNoNewline
)

type Row struct {
	PatchLine            int
	FileIndex, HunkIndex int
	Kind                 RowKind
	OldLine, NewLine     int
	HasOld, HasNew       bool
}

type Hunk struct {
	FileIndex                    int
	Ordinal                      int
	StartPatchLine, EndPatchLine int
	FileName                     string
	Header                       string
}

// File tracks one file's full span within the original patch content. The span
// includes every metadata row (diff --git, index, mode, ---/+++) that
// introduces the file as well as every hunk row parsed for it, so a copy of
// the whole file's diff round-trips byte-for-byte against the source content.
type File struct {
	FileIndex                    int
	Name                         string
	StartPatchLine, EndPatchLine int
}

type Presentation struct {
	Rows     []Row
	Hunks    []Hunk
	Files    []File
	ParseErr error
}

type DisplayRow struct {
	PatchLine int
	Separator bool
	Folded    *Hunk
}

type parsedHunk struct {
	fileIndex int
	hunkIndex int
	fileName  string
	fragment  *gitdiff.TextFragment
}

// Parse projects parsed git fragments onto the original patch lines. A hunk-only
// snippet is accepted with a neutral filename because review requests may omit
// file headers even though their code rows are still useful to display.
func Parse(content string) *Presentation {
	lines := strings.Split(content, "\n")
	presentation := &Presentation{Rows: newRows(len(lines))}
	files, _, err := gitdiff.Parse(strings.NewReader(content))
	if err != nil || len(files) == 0 {
		files, _, err = gitdiff.Parse(strings.NewReader("--- a/file\n+++ b/file\n" + content))
	}
	if err != nil {
		presentation.ParseErr = fmt.Errorf("parse diff: %w", err)
		return presentation
	}
	if len(files) == 0 {
		presentation.ParseErr = fmt.Errorf("parse diff: no file changes found")
		return presentation
	}

	hunks := make([]parsedHunk, 0)
	for fileIndex, file := range files {
		for hunkIndex, fragment := range file.TextFragments {
			hunks = append(hunks, parsedHunk{fileIndex: fileIndex, hunkIndex: hunkIndex, fileName: FileName(file), fragment: fragment})
		}
	}
	if err := projectHunks(presentation, lines, hunks); err != nil {
		presentation.ParseErr = fmt.Errorf("project diff: %w", err)
		return presentation
	}
	presentation.Files = computeFileSpans(lines, files)
	assignMetadataToFiles(presentation.Rows, presentation.Files)
	return presentation
}

func newRows(count int) []Row {
	rows := make([]Row, count)
	for i := range rows {
		rows[i] = Row{PatchLine: i + 1, FileIndex: -1, HunkIndex: -1, Kind: RowMetadata}
	}
	return rows
}

func projectHunks(presentation *Presentation, lines []string, hunks []parsedHunk) error {
	nextLine := 0
	for ordinal, parsed := range hunks {
		headerLine, err := findNextHunkHeader(lines, nextLine)
		if err != nil {
			return err
		}
		if err := validateHunkHeader(lines[headerLine], parsed.fragment); err != nil {
			return fmt.Errorf("patch line %d: %w", headerLine+1, err)
		}

		row := &presentation.Rows[headerLine]
		row.FileIndex, row.HunkIndex, row.Kind = parsed.fileIndex, parsed.hunkIndex, RowHunkHeader
		presentation.Hunks = append(presentation.Hunks, Hunk{
			FileIndex: parsed.fileIndex, Ordinal: ordinal + 1, StartPatchLine: headerLine + 1,
			FileName: parsed.fileName, Header: strings.TrimSuffix(lines[headerLine], "\r"),
		})

		nextLine = headerLine + 1
		oldLine, newLine := parsed.fragment.OldPosition, parsed.fragment.NewPosition
		for _, expected := range parsed.fragment.Lines {
			for nextLine < len(lines) && isNoNewlineMarker(lines[nextLine]) {
				markNoNewlineRow(&presentation.Rows[nextLine], parsed.fileIndex, parsed.hunkIndex)
				nextLine++
			}
			if nextLine >= len(lines) {
				return fmt.Errorf("hunk ending at patch line %d is missing a body row", headerLine+1)
			}
			if err := projectLine(&presentation.Rows[nextLine], lines[nextLine], expected, parsed.fileIndex, parsed.hunkIndex, oldLine, newLine); err != nil {
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
			markNoNewlineRow(&presentation.Rows[nextLine], parsed.fileIndex, parsed.hunkIndex)
			nextLine++
		}
		presentation.Hunks[len(presentation.Hunks)-1].EndPatchLine = nextLine
	}

	for line := nextLine; line < len(lines); line++ {
		if strings.HasPrefix(strings.TrimSuffix(lines[line], "\r"), "@@") {
			return fmt.Errorf("patch line %d is an unparsed hunk header", line+1)
		}
	}
	return nil
}

// computeFileSpans locates each parsed file's introducing header row so that a
// caller can slice the original source content for the whole file's diff. The
// spans are 1-indexed inclusive over the raw patch lines. Header detection
// prefers `diff --git` for git-style patches, falls back to `--- ` file
// headers for plain unified patches, and finally uses the first hunk header
// when neither is present (the hunk-only fallback path in Parse).
func computeFileSpans(lines []string, files []*gitdiff.File) []File {
	if len(files) == 0 {
		return nil
	}
	starts := make([]int, len(files))
	cursor := 0
	for i, f := range files {
		start := findFileHeader(lines, cursor, f)
		if start < 0 {
			return nil
		}
		starts[i] = start
		cursor = start + 1
	}
	spans := make([]File, len(files))
	for i, f := range files {
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		spans[i] = File{
			FileIndex:      i,
			Name:           FileName(f),
			StartPatchLine: starts[i] + 1,
			EndPatchLine:   end,
		}
	}
	return spans
}

// findFileHeader locates the introducing line for a parsed file, searching
// from cursor onward. Returns the 0-indexed line number or -1 if no plausible
// header is found (the caller then leaves file spans unreported so review
// keeps its raw-fallback path). Order of preference: git-style `diff --git`,
// plain `--- a/<name>` (or `--- /dev/null` for deletes), and finally the
// first hunk header. Every check tolerates a trailing CR.
func findFileHeader(lines []string, cursor int, file *gitdiff.File) int {
	for i := cursor; i < len(lines); i++ {
		if isGitFileHeader(strings.TrimSuffix(lines[i], "\r"), file) {
			return i
		}
	}
	for i := cursor; i < len(lines); i++ {
		if isPlainFileHeader(strings.TrimSuffix(lines[i], "\r"), file) {
			return i
		}
	}
	for i := cursor; i < len(lines); i++ {
		if isHunkHeader(lines[i]) {
			return i
		}
	}
	return -1
}

func isGitFileHeader(line string, file *gitdiff.File) bool {
	if !strings.HasPrefix(line, "diff --git ") {
		return false
	}
	rest := strings.TrimPrefix(line, "diff --git ")
	names := extractPathPair(rest)
	if names == nil {
		return true
	}
	return matchesName(names[0], file.OldName) || matchesName(names[1], file.NewName)
}

func isPlainFileHeader(line string, file *gitdiff.File) bool {
	if !strings.HasPrefix(line, "--- ") {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "--- "))
	if rest == "/dev/null" {
		return file.OldName == "" || file.IsNew
	}
	if matchesName(rest, file.OldName) || matchesName(rest, file.NewName) {
		return true
	}
	return false
}

// extractPathPair pulls the "a/x b/y" pair out of a `diff --git` line body.
// Returns nil for unparseable shapes so the caller falls back to filename
// matching rather than rejecting the header outright.
func extractPathPair(rest string) []string {
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return nil
	}
	return []string{fields[0], fields[1]}
}

func matchesName(candidate, name string) bool {
	return normalizeDiffName(candidate) != "" && normalizeDiffName(candidate) == normalizeDiffName(name)
}

// normalizeDiffName strips the a/ or b/ diff prefix and trailing CR/space so
// the comparison tolerates CRLF-terminated headers and gitdiff variants that
// keep the prefix in File.OldName/NewName for prefix-less patches.
func normalizeDiffName(name string) string {
	name = strings.TrimSpace(strings.TrimSuffix(name, "\r"))
	name = strings.TrimPrefix(name, "a/")
	name = strings.TrimPrefix(name, "b/")
	return name
}

// assignMetadataToFiles fills the FileIndex on RowMetadata rows so callers
// (e.g. review's clipboard code) can find the file a cursor is currently in
// regardless of whether the cursor sits on a hunk row or on metadata.
func assignMetadataToFiles(rows []Row, files []File) {
	if len(files) == 0 {
		return
	}
	for i := range rows {
		if rows[i].FileIndex >= 0 {
			continue
		}
		for _, f := range files {
			if rows[i].PatchLine >= f.StartPatchLine && rows[i].PatchLine <= f.EndPatchLine {
				rows[i].FileIndex = f.FileIndex
				break
			}
		}
	}
}

func FileName(file *gitdiff.File) string {
	name := normalizeDiffName(file.NewName)
	if name == "" || name == "/dev/null" {
		name = normalizeDiffName(file.OldName)
	}
	if name == "" || name == "/dev/null" {
		return "file"
	}
	return name
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

func isHunkHeader(line string) bool { return strings.HasPrefix(strings.TrimSuffix(line, "\r"), "@@ -") }

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
	oldPosition, oldLines, err := parseRange(ranges[0])
	if err != nil {
		return fmt.Errorf("invalid old range: %w", err)
	}
	newPosition, newLines, err := parseRange(ranges[1])
	if err != nil {
		return fmt.Errorf("invalid new range: %w", err)
	}
	if oldPosition != fragment.OldPosition || oldLines != fragment.OldLines || newPosition != fragment.NewPosition || newLines != fragment.NewLines {
		return fmt.Errorf("hunk range does not match parsed fragment")
	}
	return nil
}

func parseRange(value string) (int64, int64, error) {
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

func projectLine(row *Row, raw string, expected gitdiff.Line, fileIndex, hunkIndex int, oldLine, newLine int64) error {
	if len(raw) == 0 {
		if expected.Op != gitdiff.OpContext || lineText(expected.Line) != "" {
			return fmt.Errorf("expected %q row", expected.Op.String())
		}
	} else {
		if raw[0] != expected.Op.String()[0] {
			return fmt.Errorf("expected %q row", expected.Op.String())
		}
		if strings.TrimSuffix(raw[1:], "\r") != lineText(expected.Line) {
			return fmt.Errorf("row content does not match parsed diff")
		}
	}

	row.FileIndex, row.HunkIndex = fileIndex, hunkIndex
	switch expected.Op {
	case gitdiff.OpContext:
		row.Kind, row.OldLine, row.NewLine, row.HasOld, row.HasNew = RowContext, int(oldLine), int(newLine), true, true
	case gitdiff.OpDelete:
		row.Kind, row.OldLine, row.HasOld = RowDelete, int(oldLine), true
	case gitdiff.OpAdd:
		row.Kind, row.NewLine, row.HasNew = RowAdd, int(newLine), true
	default:
		return fmt.Errorf("unsupported diff operation %q", expected.Op.String())
	}
	return nil
}

func lineText(line string) string {
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

func isNoNewlineMarker(line string) bool {
	return strings.HasPrefix(strings.TrimSuffix(line, "\r"), "\\ ")
}

func markNoNewlineRow(row *Row, fileIndex, hunkIndex int) {
	row.FileIndex, row.HunkIndex, row.Kind = fileIndex, hunkIndex, RowNoNewline
}

func IsVisible(row Row) bool {
	switch row.Kind {
	case RowHunkHeader, RowContext, RowDelete, RowAdd:
		return true
	default:
		return false
	}
}

// DisplayRows is the shared display order: only hunk headings and code rows,
// with two blank rows separating each hunk. Folding is an optional consumer
// concern because a transcript has no interaction state.
func (p *Presentation) DisplayRows(folded func(Hunk) bool) []DisplayRow {
	rows := make([]DisplayRow, 0, len(p.Rows))
	for i := range p.Hunks {
		hunk := &p.Hunks[i]
		if hunk.Ordinal > 1 {
			rows = append(rows, DisplayRow{Separator: true}, DisplayRow{Separator: true})
		}
		if folded != nil && folded(*hunk) {
			rows = append(rows, DisplayRow{PatchLine: hunk.StartPatchLine, Folded: hunk})
			continue
		}
		for line := hunk.StartPatchLine; line <= hunk.EndPatchLine; line++ {
			if IsVisible(p.Rows[line-1]) {
				rows = append(rows, DisplayRow{PatchLine: line})
			}
		}
	}
	return rows
}

func SectionTitle(header string) string {
	header = strings.TrimSuffix(header, "\r")
	if !strings.HasPrefix(header, "@@") {
		return ""
	}
	end := strings.Index(header[2:], "@@")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(header[end+4:])
}

// RenderHunkHeader is intentionally shared so static transcript diffs and the
// interactive review source present the same hunk title.
func RenderHunkHeader(hunk Hunk) string {
	label := shared.Theme.Review.HunkTitle.Render(fmt.Sprintf(" Hunk %d ", hunk.Ordinal))
	content := label + " " + shared.Theme.Review.HunkStatus.Render(hunk.FileName)
	if title := SectionTitle(hunk.Header); title != "" {
		content += shared.Theme.Review.Gutter.Render(" · ") + shared.Theme.Diff.Hunk.Render(title)
	}
	return content
}

func RenderRawLine(line string) string {
	style := shared.Theme.Review.SyntaxBase
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), isMetadata(line):
		style = shared.Theme.Review.Gutter
	case strings.HasPrefix(line, "@@"):
		style = shared.Theme.Diff.Hunk
	case strings.HasPrefix(line, "+"):
		style = shared.Theme.Diff.Add
	case strings.HasPrefix(line, "-"):
		style = shared.Theme.Diff.Remove
	}
	return style.Render(line)
}

func isMetadata(line string) bool {
	prefixes := []string{"diff ", "index ", "rename ", "similarity ", "old mode ", "new mode ", "deleted file mode ", "new file mode ", "Binary files ", "\\ No newline"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
