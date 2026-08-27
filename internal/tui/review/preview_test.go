package review

import (
	"strings"
	"testing"

	domain "jig/internal/review"
)

func TestPreviewMapsTopLevelMarkdownBlocksToSourceLines(t *testing.T) {
	content := "# Title\n\nA paragraph with café.\n\n- one\n- two\n\n```go\nfmt.Println(1)\n```\n"
	p := buildPreview(content)
	if len(p.blocks) != 4 {
		t.Fatalf("blocks = %d, want 4: %#v", len(p.blocks), p.blocks)
	}
	want := [][2]int{{1, 1}, {3, 3}, {5, 6}, {8, 10}}
	for i, block := range p.blocks {
		if got := [2]int{block.startLine, block.endLine}; got != want[i] {
			t.Fatalf("block %d range = %v, want %v", i, got, want[i])
		}
	}
	d := domain.Document{ID: "doc", SHA256: domain.Digest(content), Content: content}
	a := p.anchor(2, d)
	if a.StartLine != 5 || a.EndLine != 6 || a.Quote != "- one\n- two" {
		t.Fatalf("anchor = %#v", a)
	}
}

func TestPreviewRenderCacheInvalidatesOnlyOnWidthChange(t *testing.T) {
	content := "# Title\n\nbody"
	p := buildPreview(content)
	first, err := p.render(0, content, 40)
	if err != nil || strings.TrimSpace(first) == "" {
		t.Fatalf("first render = %q, %v", first, err)
	}
	if len(p.cache) != 1 {
		t.Fatalf("cache size = %d, want 1", len(p.cache))
	}
	renderer := p.renderer
	if _, err := p.render(1, content, 40); err != nil {
		t.Fatal(err)
	}
	if p.renderer != renderer {
		t.Fatal("renderer was rebuilt without a width change")
	}
	if len(p.cache) != 2 {
		t.Fatalf("cache size = %d, want 2", len(p.cache))
	}
	if _, err := p.render(0, content, 60); err != nil {
		t.Fatal(err)
	}
	if len(p.cache) != 1 {
		t.Fatalf("width change did not invalidate cache: %d", len(p.cache))
	}
	if p.renderer == renderer {
		t.Fatal("width change did not rebuild renderer")
	}
}

func TestPreviewUsesSharedFormatterForFencedCode(t *testing.T) {
	content := "```go\nfunc café() string { return \"世界\" }\n```\n"
	p := buildPreview(content)
	rendered, err := p.render(0, content, 60)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "café") || !strings.Contains(rendered, "世界") || !strings.Contains(rendered, "╭") || !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("fenced code did not use themed formatter:\n%q", rendered)
	}
}

func TestEmptyMarkdownHasNoSourceAddressablePreview(t *testing.T) {
	p := buildPreview("\n")
	if p.parseErr == nil || len(p.blocks) != 0 {
		t.Fatalf("empty preview = %#v, want unavailable", p)
	}
	content := "\n"
	m, err := New(domain.Session{StepID: "review", Documents: []domain.Document{{
		ID: "empty", Label: "Empty", Source: "empty.md", Format: "markdown", Content: content, SHA256: domain.Digest(content),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if m.activeDocumentMode() != DocumentSource {
		t.Fatal("empty Markdown did not fall back to source")
	}
	m = update(m, "s")
	if !strings.Contains(m.error, "preview unavailable") {
		t.Fatalf("empty Markdown preview error = %q", m.error)
	}
}

func TestPreviewConstructionIsMarkdownOnly(t *testing.T) {
	content := "# looks like Markdown"
	session := domain.Session{StepID: "review", Documents: []domain.Document{{
		ID: "text", Label: "Text", Source: "file.txt", Format: "text", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.previews[0].blocks) != 0 || m.previewAvailable() {
		t.Fatalf("non-Markdown preview was constructed: %#v", m.previews[0])
	}
}

func TestPreviewToggleAnchorsCommentToWholeBlock(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m.active = 1
	m.documentModes[m.active] = DocumentPreview
	m.previewBlock = 1
	m.move(0)
	m.openComposer(false)
	if m.cursor != 3 || m.rangeEnd != 3 {
		t.Fatalf("preview selection = %d-%d, want 3-3", m.cursor, m.rangeEnd)
	}
	m.composer.SetValue("Comment on the complete block")
	m = update(m, "enter")
	if len(m.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(m.comments))
	}
	comment := m.comments[0]
	if comment.Anchor.DocumentID != "02-notes" || comment.Anchor.SHA256 != m.docs[1].meta.SHA256 ||
		comment.Anchor.StartLine != 3 || comment.Anchor.EndLine != 3 || comment.Anchor.Quote != "body" || comment.Anchor.Prefix != "" {
		t.Fatalf("preview comment changed source anchor: %#v", comment.Anchor)
	}
}
