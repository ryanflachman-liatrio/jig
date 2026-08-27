package review

import (
	"fmt"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	domain "jig/internal/review"
	"jig/internal/tui/shared"
)

type previewBlock struct {
	startLine int
	endLine   int
	rendered  string
}

type previewState struct {
	blocks    []previewBlock
	cache     map[int]string
	renderer  *glamour.TermRenderer
	width     int
	parseErr  error
	renderErr error
}

func buildPreview(content string) previewState {
	state := previewState{cache: make(map[int]string)}
	source := []byte(content)
	doc := goldmark.New().Parser().Parse(text.NewReader(source))
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		start, end, ok := nodeLines(child, source)
		if ok {
			state.blocks = append(state.blocks, previewBlock{startLine: start, endLine: end})
		}
	}
	if len(state.blocks) == 0 {
		state.parseErr = fmt.Errorf("markdown contains no source-addressable blocks")
	}
	return state
}

func nodeLines(node ast.Node, source []byte) (int, int, bool) {
	start, end := len(source), 0
	var visit func(ast.Node)
	visit = func(n ast.Node) {
		if n.Type() == ast.TypeBlock || n.Type() == ast.TypeDocument {
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				if seg.Start < start {
					start = seg.Start
				}
				if seg.Stop > end {
					end = seg.Stop
				}
			}
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(node)
	if start >= end || start < 0 || end > len(source) {
		return 0, 0, false
	}
	startLine, endLine := lineAt(source, start), lineAt(source, end-1)
	if node.Kind() == ast.KindFencedCodeBlock {
		lines := strings.Split(string(source), "\n")
		for startLine > 1 && strings.TrimSpace(lines[startLine-2]) == "" {
			startLine--
		}
		if startLine > 1 && strings.HasPrefix(strings.TrimSpace(lines[startLine-2]), "```") {
			startLine--
		}
		for endLine < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[endLine]), "```") {
			endLine++
		}
		if endLine < len(lines) {
			endLine++
		}
	}
	return startLine, endLine, true
}

func lineAt(source []byte, offset int) int {
	if offset >= len(source) {
		offset = len(source) - 1
	}
	if offset < 0 {
		return 1
	}
	return 1 + strings.Count(string(source[:offset]), "\n")
}

func (p *previewState) render(index int, content string, width int) (string, error) {
	if index < 0 || index >= len(p.blocks) {
		return "", fmt.Errorf("preview block %d is out of range", index)
	}
	width = max(width, 20)
	if p.width != width {
		p.width = width
		p.cache = make(map[int]string)
		p.renderer = nil
		p.renderErr = nil
	}
	if rendered, ok := p.cache[index]; ok {
		return rendered, nil
	}
	block := p.blocks[index]
	lines := strings.Split(content, "\n")
	if block.startLine < 1 || block.endLine > len(lines) {
		return "", fmt.Errorf("preview block source range is invalid")
	}
	raw := strings.Join(lines[block.startLine-1:block.endLine], "\n")
	if p.renderer == nil {
		renderer, err := newPreviewRenderer(width)
		if err != nil {
			p.renderErr = err
			return "", err
		}
		p.renderer = renderer
	}
	rendered, err := p.renderer.Render(raw)
	if err != nil {
		p.renderErr = err
		return "", err
	}
	rendered = strings.TrimSpace(rendered)
	p.renderErr = nil
	p.cache[index] = rendered
	return rendered, nil
}

func newPreviewRenderer(width int) (*glamour.TermRenderer, error) {
	style := shared.Theme.Markdown
	return glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithChromaFormatter(shared.CodeBlockFormatter(previewCodeWidth(style, width))),
	)
}

func previewCodeWidth(style ansi.StyleConfig, width int) int {
	if style.Document.Indent != nil {
		width -= int(*style.Document.Indent)
	}
	if style.Document.Margin != nil {
		width -= 2 * int(*style.Document.Margin)
	}
	if style.CodeBlock.Indent != nil {
		width -= int(*style.CodeBlock.Indent)
	}
	if style.CodeBlock.Margin != nil {
		width -= int(*style.CodeBlock.Margin)
	}
	return width
}

func (p previewState) anchor(index int, d domain.Document) domain.Anchor {
	block := p.blocks[index]
	lines := strings.Split(d.Content, "\n")
	quote := strings.Join(lines[block.startLine-1:block.endLine], "\n")
	return domain.Anchor{DocumentID: d.ID, SHA256: d.SHA256, StartLine: block.startLine, EndLine: block.endLine, Quote: quote}
}
