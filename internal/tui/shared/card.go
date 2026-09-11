package shared

import (
	"regexp"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var sgrSequence = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// CardState controls the card's semantic border and optional recessed tint.
type CardState int

const (
	CardPending CardState = iota
	CardRunning
	CardSuccess
	CardWarning
	CardError
)

// CardSection is an optional labeled or ruled body section.
type CardSection struct {
	Label string
	Lines []string
	Rule  bool
}

// Card is a pure-presentation transcript card. Nil padding means omitted;
// explicit zero means flush content on that side.
type Card struct {
	Header      string
	HeaderMeta  string
	Sections    []CardSection
	State       CardState
	Width       int
	PadLeft     *int
	PadRight    *int
	Tint        bool
	BorderMuted bool
}

func cardPadding(width int, left, right *int) (int, int) {
	l, r := 1, 1
	if left != nil {
		l = max(*left, 0)
	}
	if right != nil {
		r = max(*right, 0)
	} else {
		r = l
	}
	// Keep one content cell whenever a framed row can have one.
	for width >= 3 && width-2-l-r < 1 {
		if r > 0 {
			r--
			continue
		}
		if l > 0 {
			l--
			continue
		}
		break
	}
	return l, r
}

// CardContentWidth returns the body content width after presence-aware padding.
func CardContentWidth(width int, left, right *int) int {
	if width <= 0 {
		return 0
	}
	l, r := cardPadding(width, left, right)
	return max(1, width-2-l-r)
}

func (c Card) borderStyle() lipgloss.Style {
	if c.BorderMuted {
		return Theme.Card.BorderMuted
	}
	switch c.State {
	case CardRunning:
		return Theme.Card.BorderRunning
	case CardSuccess:
		return Theme.Card.BorderSuccess
	case CardWarning:
		return Theme.Card.BorderWarning
	case CardError:
		return Theme.Card.BorderError
	default:
		return Theme.Card.BorderPending
	}
}

func cardLabel(s string) string {
	// Preserve caller SGR styling; only literal presentation whitespace is
	// normalized here. ANSI-aware truncation below keeps escape sequences valid.
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c Card) finishRow(row string) string {
	if !c.Tint {
		return row
	}
	background := "\x1b[48;2;26;25;31m"
	if c.State == CardError {
		background = "\x1b[48;2;42;26;30m"
	}
	// Content can contain full/background SGR resets (notably Glamour output).
	// Reapply the card background after those resets, while leaving intentional
	// foreground and non-reset attributes intact. A parameter is a reset only
	// when it is exactly 0 or 49; zeros in RGB parameters are not resets.
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

// RenderCard returns newline-separated rows without a trailing newline.
func RenderCard(c Card) string {
	if c.Width <= 0 {
		return ""
	}
	if c.Width < 3 {
		return strings.Repeat(" ", c.Width)
	}
	border := c.borderStyle()
	label := c.Header
	if c.HeaderMeta != "" {
		if label != "" {
			label += " · "
		}
		label += c.HeaderMeta
	}
	rows := []string{c.finishRow(composeBorderBar(c.Width, "╭", "╮", 3, cardLabel(label), border))}
	pl, pr := cardPadding(c.Width, c.PadLeft, c.PadRight)
	contentWidth := CardContentWidth(c.Width, c.PadLeft, c.PadRight)
	for i, section := range c.Sections {
		if section.Label != "" || (section.Rule && i > 0) {
			rows = append(rows, c.finishRow(composeBorderBar(c.Width, "├", "┤", 3, cardLabel(section.Label), border)))
		}
		for _, logical := range section.Lines {
			for _, line := range strings.Split(logical, "\n") {
				line = strings.TrimRightFunc(line, unicode.IsSpace)
				wrapped := ansi.Hardwrap(line, contentWidth, true)
				for _, body := range strings.Split(wrapped, "\n") {
					fill := max(contentWidth-lipgloss.Width(body), 0)
					row := border.Render("│") + strings.Repeat(" ", pl) + body + strings.Repeat(" ", fill+pr) + border.Render("│")
					rows = append(rows, c.finishRow(row))
				}
			}
		}
	}
	rows = append(rows, c.finishRow(composeBorderBar(c.Width, "╰", "╯", 3, "", border)))
	return strings.Join(rows, "\n")
}
