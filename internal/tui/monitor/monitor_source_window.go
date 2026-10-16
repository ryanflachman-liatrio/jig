package monitor

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Offsets refer to immutable recorded bytes; display offsets include safe
// normalization and wrapping. No ANSI or highlighted text is stored in the index.
type sourceWindow struct {
	start, end, displayStart, displayEnd int
	firstLine, lastLine, rows            int
	continued, continues                 bool
}
type sourceWindowIndex struct {
	width   int
	display string
	windows []sourceWindow
}

// Index once per loaded section and width. Decode escape sequences with state
// across byte boundaries, so splitting a long OSC cannot expose its payload.
func indexSourceWindows(raw string, width int) *sourceWindowIndex {
	idx := &sourceWindowIndex{width: width}
	if width <= 0 || raw == "" {
		return idx
	}
	var display strings.Builder
	line, column, rows := 1, 0, 0
	w := sourceWindow{firstLine: 1}
	var parserState byte
	finish := func(end int) {
		w.end, w.displayEnd, w.rows = end, display.Len(), rows
		w.continues = end < len(raw) && column > 0
		idx.windows = append(idx.windows, w)
		w = sourceWindow{start: end, displayStart: display.Len(), firstLine: line, continued: column > 0}
		rows, column = 0, 0
	}
	for offset := 0; offset < len(raw); {
		// Bound parser input too, including pathological combining sequences.
		end := min(len(raw), offset+transcriptDetailBytes)
		for end < len(raw) && end > offset && !utf8.RuneStart(raw[end]) {
			end--
		}
		seq, cells, n, nextState := ansi.DecodeSequence(raw[offset:end], parserState, nil)
		if n <= 0 {
			seq, cells, n, nextState = "�", 1, 1, 0
		}
		shown := seq
		newline := false
		if parserState != ansi.NormalState || nextState != ansi.NormalState || strings.HasPrefix(seq, "\x1b") {
			shown, cells = "", 0
		} else if seq == "\r" && offset+n < len(raw) && raw[offset+n] == '\n' {
			shown, cells = "", 0
		} else if seq == "\n" {
			shown, cells, newline = "", 0, true
		} else if seq == "\t" {
			shown, cells = strings.Repeat(" ", min(4, width)), min(4, width)
		} else if r, _ := utf8.DecodeRuneInString(seq); unicode.IsControl(r) {
			shown, cells = "�", 1
		}
		if cells > width {
			shown, cells = "�", 1
		}
		wrap := !newline && cells > 0 && column+cells > width
		extraRow := rows == 0 || wrap
		extraBytes := len(shown)
		if newline && offset+n < len(raw) && rows < transcriptDetailRows {
			extraBytes++
		}
		if extraRow && rows > 0 {
			extraBytes++
		}
		if offset > w.start && (offset+n-w.start > transcriptDetailBytes || display.Len()-w.displayStart+extraBytes > transcriptDetailBytes || extraRow && rows == transcriptDetailRows) {
			finish(offset)
			continue
		}
		if rows == 0 || wrap {
			if rows > 0 {
				display.WriteByte('\n')
			}
			rows++
			column = 0
		}
		display.WriteString(shown)
		column += cells
		w.lastLine = line
		offset += n
		parserState = nextState
		if newline {
			line++
			column = 0
			// Terminating LF belongs to this raw span, without a phantom final row.
			if offset < len(raw) {
				if rows == transcriptDetailRows {
					finish(offset)
				} else {
					display.WriteByte('\n')
					rows++
				}
			}
		}
	}
	if w.start < len(raw) {
		finish(len(raw))
	}
	idx.display = display.String()
	return idx
}

func (idx *sourceWindowIndex) at(anchor int) int {
	if len(idx.windows) == 0 {
		return 0
	}
	return min(sort.Search(len(idx.windows), func(i int) bool { return idx.windows[i].end > anchor }), len(idx.windows)-1)
}
func (idx *sourceWindowIndex) text(window int) string {
	if len(idx.windows) == 0 {
		return ""
	}
	w := idx.windows[window]
	return idx.display[w.displayStart:w.displayEnd]
}
