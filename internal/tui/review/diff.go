package review

import (
	"github.com/bluekeyes/go-gitdiff/gitdiff"

	"jig/internal/tui/diffview"
)

type diffRowKind = diffview.RowKind

const (
	diffRowMetadata   = diffview.RowMetadata
	diffRowHunkHeader = diffview.RowHunkHeader
	diffRowContext    = diffview.RowContext
	diffRowDelete     = diffview.RowDelete
	diffRowAdd        = diffview.RowAdd
	diffRowNoNewline  = diffview.RowNoNewline
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
	fileName                     string
	header                       string
}

type diffPresentation struct {
	rows     []diffRow
	hunks    []diffHunk
	folded   map[int]bool
	parseErr error
	display  *diffview.Presentation
}

func buildDiffPresentation(content string, _ []string) *diffPresentation {
	parsed := diffview.Parse(content)
	presentation := &diffPresentation{
		rows:     make([]diffRow, len(parsed.Rows)),
		hunks:    make([]diffHunk, len(parsed.Hunks)),
		folded:   make(map[int]bool),
		parseErr: parsed.ParseErr,
		display:  parsed,
	}
	for i, row := range parsed.Rows {
		presentation.rows[i] = diffRow{
			patchLine: row.PatchLine, fileIndex: row.FileIndex, hunkIndex: row.HunkIndex,
			kind: row.Kind, oldLine: row.OldLine, newLine: row.NewLine, hasOld: row.HasOld, hasNew: row.HasNew,
		}
	}
	for i, hunk := range parsed.Hunks {
		presentation.hunks[i] = diffHunk{
			fileIndex: hunk.FileIndex, ordinal: hunk.Ordinal, startPatchLine: hunk.StartPatchLine,
			endPatchLine: hunk.EndPatchLine, fileName: hunk.FileName, header: hunk.Header,
		}
	}
	return presentation
}

func hunkSectionTitle(header string) string { return diffview.SectionTitle(header) }

func diffFileName(file *gitdiff.File) string { return diffview.FileName(file) }

func isVisibleDiffRow(row diffRow) bool {
	return diffview.IsVisible(diffview.Row{Kind: row.kind})
}
