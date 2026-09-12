package monitor

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

const (
	transcriptDetailBytes    = 4096
	transcriptDetailRows     = 12
	transcriptDetailTailRows = 3
)

// detailAnchor selects how boundTranscriptDetail crops content that would
// otherwise exceed transcriptDetailRows. Head anchoring (the settled-content
// default) keeps the first N rows plus a small tail so status and error
// text remain inspectable. Tail anchoring (running content) keeps only the
// newest rows so a live-streaming buffer pins its trailing edge; slice 06
// consumes this to make a running tool exchange's body follow the stream.
type detailAnchor int

const (
	detailAnchorHead detailAnchor = iota
	detailAnchorTail
)

// boundTranscriptDetail applies the transcript detail contract without any TUI
// state. Byte limiting happens first; row limiting then keeps rows according
// to the anchor argument so status and error information remain inspectable
// for settled content while a running buffer keeps its newest rows visible.
func boundTranscriptDetail(content string, width int, anchor detailAnchor) (shown string, hiddenRows int) {
	if len(content) > transcriptDetailBytes {
		content = truncateUTF8Bytes(content, transcriptDetailBytes)
	}
	if width < 1 {
		width = 1
	}
	var rows []string
	for _, line := range strings.Split(content, "\n") {
		for lipgloss.Width(line) > width {
			row, rest := splitDetailRow(line, width)
			rows = append(rows, row)
			line = rest
		}
		rows = append(rows, line)
	}
	if len(rows) <= transcriptDetailRows {
		return strings.Join(rows, "\n"), 0
	}
	hiddenRows = len(rows) - transcriptDetailRows
	switch anchor {
	case detailAnchorTail:
		kept := rows[len(rows)-transcriptDetailRows:]
		return strings.Join(kept, "\n"), hiddenRows
	default:
		headRows := transcriptDetailRows - transcriptDetailTailRows
		kept := append([]string{}, rows[:headRows]...)
		kept = append(kept, rows[len(rows)-transcriptDetailTailRows:]...)
		return strings.Join(kept, "\n"), hiddenRows
	}
}

func truncateUTF8Bytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	end := limit
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end]
}

func splitDetailRow(s string, width int) (string, string) {
	var cells, end int
	for i, r := range s {
		w := lipgloss.Width(string(r))
		if cells+w > width && end > 0 {
			return s[:end], s[end:]
		}
		cells += w
		end = i + utf8.RuneLen(r)
	}
	return s, ""
}
