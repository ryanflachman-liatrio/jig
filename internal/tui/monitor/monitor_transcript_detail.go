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

// boundTranscriptDetail applies the transcript detail contract without any TUI
// state. Byte limiting happens first; row limiting then keeps the final rows so
// status and error information remain inspectable.
func boundTranscriptDetail(content string, width int) (shown string, hiddenRows int) {
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
	headRows := transcriptDetailRows - transcriptDetailTailRows
	hiddenRows = len(rows) - transcriptDetailRows
	kept := append([]string{}, rows[:headRows]...)
	kept = append(kept, rows[len(rows)-transcriptDetailTailRows:]...)
	return strings.Join(kept, "\n"), hiddenRows
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
