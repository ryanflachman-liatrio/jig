package monitor

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// Highlight only a prebounded, sanitized window. Splitting tokens at newlines
// keeps styling from trimming blank source rows or adding layout padding.
func highlightRecordedSource(path, code string) (rendered string, err error) {
	defer func() {
		if recover() != nil {
			rendered, err = "", fmt.Errorf("source highlighting failed")
		}
	}()
	lexer := lexers.Get(codeLanguage(path))
	if lexer == nil {
		return code, nil
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(&chroma.TokeniseOptions{State: "root"}, code)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for token := iterator(); token != chroma.EOF; token = iterator() {
		for i, part := range strings.Split(token.Value, "\n") {
			if i > 0 {
				b.WriteByte('\n')
			}
			if part != "" {
				b.WriteString(shared.SourceTokenStyle(token.Type).Render(part))
			}
		}
	}
	return b.String(), nil
}
