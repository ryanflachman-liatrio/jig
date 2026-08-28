package review

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"jig/internal/tui/shared"
)

type sourcePresentation struct {
	lines    []string
	language string
	err      error
}

func buildSourcePresentation(d document) sourcePresentation {
	if isDiffDocument(d) {
		return sourcePresentation{lines: renderDiffLines(d.lines), language: "Diff"}
	}

	var lexer chroma.Lexer
	if d.meta.Format == "markdown" {
		lexer = lexers.Get("markdown")
	} else if filepath.Base(d.meta.Source) != "go.mod" {
		lexer = lexers.Match(d.meta.Source)
	}
	// Chroma classifies *.mod as AMPL; that match is not useful for Go's module
	// manifest, so the demo and real go.mod reviews remain honest plain text.
	if lexer == nil || strings.EqualFold(lexer.Config().Name, "fallback") {
		return plainSourcePresentation(d, nil)
	}

	presentation, err := highlightSource(d, lexer)
	if err != nil {
		return plainSourcePresentation(d, err)
	}
	presentation.language = lexer.Config().Name
	if d.meta.Format == "markdown" {
		presentation.language = "Markdown"
	}
	return presentation
}

func isDiffDocument(d document) bool {
	ext := strings.ToLower(filepath.Ext(d.meta.Source))
	return d.meta.Format == "diff" || ext == ".diff" || ext == ".patch"
}

func plainSourcePresentation(d document, err error) sourcePresentation {
	return sourcePresentation{lines: append([]string(nil), d.lines...), language: "Plain text", err: err}
}

func highlightSource(d document, lexer chroma.Lexer) (presentation sourcePresentation, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			presentation = sourcePresentation{}
			err = fmt.Errorf("highlight source: %v", recovered)
		}
	}()

	iterator, err := chroma.Coalesce(lexer).Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: true}, d.meta.Content)
	if err != nil {
		return sourcePresentation{}, fmt.Errorf("highlight source: %w", err)
	}

	lines := []strings.Builder{{}}
	for token := iterator(); token != chroma.EOF; token = iterator() {
		parts := strings.Split(token.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				lines = append(lines, strings.Builder{})
			}
			if part != "" {
				lines[len(lines)-1].WriteString(renderSourceToken(token.Type, part))
			}
		}
	}
	if len(lines) != len(d.lines) {
		return sourcePresentation{}, fmt.Errorf("highlight source: rendered %d logical lines, want %d", len(lines), len(d.lines))
	}

	rendered := make([]string, len(lines))
	for i := range lines {
		rendered[i] = lines[i].String()
	}
	return sourcePresentation{lines: rendered}, nil
}

func renderSourceToken(tokenType chroma.TokenType, value string) string {
	return shared.SourceTokenStyle(tokenType).Render(value)
}

func renderDiffLines(lines []string) []string {
	rendered := make([]string, len(lines))
	for i, line := range lines {
		style := shared.Theme.Review.SyntaxBase
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), isDiffMetadata(line):
			style = shared.Theme.Review.Gutter
		case strings.HasPrefix(line, "@@"):
			style = shared.Theme.Diff.Hunk
		case strings.HasPrefix(line, "+"):
			style = shared.Theme.Diff.Add
		case strings.HasPrefix(line, "-"):
			style = shared.Theme.Diff.Remove
		}
		rendered[i] = style.Render(line)
	}
	return rendered
}

func isDiffMetadata(line string) bool {
	prefixes := []string{"diff ", "index ", "rename ", "similarity ", "old mode ", "new mode ", "deleted file mode ", "new file mode ", "Binary files ", "\\ No newline"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
