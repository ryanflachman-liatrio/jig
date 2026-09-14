package shared

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

var sgrSequence = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// StyleBackground returns the raw SGR sequence that opens style's background,
// suitable as the background argument to TintRow. It renders style against
// empty content and strips the trailing reset, so a caller building a tinted
// multi-row surface can source its background from one themed style instead
// of a duplicated hex literal.
func StyleBackground(style lipgloss.Style) string {
	return strings.TrimSuffix(style.Render(""), "\x1b[m")
}

// TintRow wraps row in background and reapplies it after any SGR sequence
// that resets the background (parameters `0`, empty, or `49`). Content can
// contain full/background SGR resets (notably Glamour output); reapplying the
// background after those resets keeps intentional foreground and non-reset
// attributes intact so a tinted surface (card, bubble) is not punctured by
// nested rendering. Extracted from the card renderer (epic CC-9) so every
// tinted surface shares one background-reset rule instead of duplicating it.
func TintRow(row, background string) string {
	row = sgrSequence.ReplaceAllStringFunc(row, func(sequence string) string {
		match := sgrSequence.FindStringSubmatch(sequence)
		if sgrResetsBackground(match[1]) {
			return sequence + background
		}
		return sequence
	})
	return background + row + "\x1b[49m"
}

func sgrResetsBackground(params string) bool {
	if params == "" {
		return true
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "0", "49":
			return true
		case "38", "48", "58":
			// Extended foreground/background/underline colors consume their mode
			// plus either one palette index or three RGB components. Those color
			// components may legitimately be zero and are not SGR reset codes.
			if i+1 >= len(fields) {
				continue
			}
			switch fields[i+1] {
			case "2":
				i += min(4, len(fields)-i-1)
			case "5":
				i += min(2, len(fields)-i-1)
			}
		}
	}
	return false
}
