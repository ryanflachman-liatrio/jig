package monitor

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"jig/internal/tui/shared"
)

// collapseSummaryLabel derives an oversized user-role text block's summary
// label from the text of its first markdown heading, when the first
// non-blank line is an ATX heading (`# ...`). It falls back to the generic
// label "User input" otherwise, since jig collapses by size rather than
// provenance and the content may well be operator-pasted (spec open
// question 3).
func collapseSummaryLabel(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "#"); ok {
			heading := strings.TrimSpace(strings.TrimLeft(rest, "#"))
			if heading != "" {
				return heading
			}
		}
		break
	}
	return "User input"
}

// humanizeByteSize renders n bytes as a binary-unit (KiB/MiB/...) size, e.g.
// "4.1 KiB". Any unambiguous human-readable form is acceptable (spec open
// question 1); this is the standard IEC-unit formatting.
func humanizeByteSize(n int) string {
	const unit = 1024
	if n < unit {
		return strconv.Itoa(n) + " B"
	}
	div, exp := int64(unit), 0
	for m := int64(n) / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "iB"
}

// buildCollapseSummary composes the dim summary row for an oversized
// user-role text block ("<label> · <size> · <n> line(s)") and ANSI-aware
// truncates it to width with a trailing ellipsis (FR-09.4/FR-09.7/FR-09.8).
// Line count is the raw newline count, not a rendered line count, matching
// the raw-bytes collapse decision this budget exists to support.
func buildCollapseSummary(text string, width int) string {
	label := collapseSummaryLabel(text)
	size := humanizeByteSize(len(text))
	lines := strings.Count(text, "\n") + 1
	unit := "line"
	if lines != 1 {
		unit = "lines"
	}
	summary := label + " · " + size + " · " + strconv.Itoa(lines) + " " + unit
	if width < 1 {
		width = 1
	}
	return ansi.Truncate(summary, width, shared.EllipsisGlyph)
}
