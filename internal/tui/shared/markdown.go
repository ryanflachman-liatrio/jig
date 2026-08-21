package shared

import (
	"io"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
)

var (
	codeBlockFormattersMu sync.Mutex
	codeBlockFormatters   = make(map[int]string)
)

// CodeBlockFormatter registers a width-specific Chroma formatter because
// Glamour accepts formatter names rather than formatter implementations.
func CodeBlockFormatter(width int) string {
	minWidth := Theme.Chat.CodeBlock.GetHorizontalFrameSize() + 1
	if width < minWidth {
		width = minWidth
	}

	codeBlockFormattersMu.Lock()
	defer codeBlockFormattersMu.Unlock()
	if name, ok := codeBlockFormatters[width]; ok {
		return name
	}

	name := "jig-code-block-" + strconv.Itoa(width)
	formatters.Register(name, codeBlockFormatter(width))
	codeBlockFormatters[width] = name
	return name
}

func codeBlockFormatter(width int) chroma.Formatter {
	return chroma.FormatterFunc(func(w io.Writer, syntax *chroma.Style, iterator chroma.Iterator) error {
		var content strings.Builder
		for token := iterator(); token != chroma.EOF; token = iterator() {
			tokenStyle := Theme.Chat.CodeText
			entry := syntax.Get(token.Type)
			if entry.Bold == chroma.Yes {
				tokenStyle = tokenStyle.Bold(true)
			}
			if entry.Italic == chroma.Yes {
				tokenStyle = tokenStyle.Italic(true)
			}
			if entry.Underline == chroma.Yes {
				tokenStyle = tokenStyle.Underline(true)
			}
			if entry.Colour.IsSet() {
				tokenStyle = tokenStyle.Foreground(lipgloss.Color(entry.Colour.String()))
			}
			if entry.Background.IsSet() {
				tokenStyle = tokenStyle.Background(lipgloss.Color(entry.Background.String()))
			}
			content.WriteString(tokenStyle.Render(token.Value))
		}

		code := strings.TrimRight(content.String(), "\r\n")
		boxWidth := width
		if minWidth := Theme.Chat.CodeBlock.GetHorizontalFrameSize() + 1; boxWidth < minWidth {
			boxWidth = minWidth
		}
		box := Theme.Chat.CodeBlock.Width(boxWidth).Render(code)
		if _, err := io.WriteString(w, box); err != nil {
			return err
		}
		_, err := io.WriteString(w, "\n")
		return err
	})
}
