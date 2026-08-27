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
	if _, err := p.render(1, content, 40); err != nil {
		t.Fatal(err)
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
}

func TestPreviewToggleAnchorsCommentToWholeBlock(t *testing.T) {
	m, err := New(testSession())
	if err != nil {
		t.Fatal(err)
	}
	m.active = 1
	m.documentMode = DocumentPreview
	m.previewBlock = 1
	m.move(0)
	m.openComposer(false)
	if m.cursor != 3 || m.rangeEnd != 3 {
		t.Fatalf("preview selection = %d-%d, want 3-3", m.cursor, m.rangeEnd)
	}
}
