package review

import (
	"errors"
	"strings"
	"testing"

	domain "jig/internal/review"
	"jig/internal/tui/shared"
)

func extract(payload shared.ClipboardPayload) (string, error) {
	return payload.Payload, payload.Err
}

func TestExtractSourceLineRangePreservesTerminators(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		start, end int
		total      int
		want       string
		wantErr    error
	}{
		{"single LF line", "alpha\nbeta\ngamma\n", 2, 2, 3, "beta\n", nil},
		{"first CRLF line", "alpha\r\nbeta\r\n", 1, 1, 2, "alpha\r\n", nil},
		{"CRLF range", "alpha\r\nbeta\r\ngamma\r\n", 1, 2, 3, "alpha\r\nbeta\r\n", nil},
		{"no final newline last line", "alpha\nbeta", 2, 2, 2, "beta", nil},
		{"whole document with trailing newline", "alpha\nbeta\n", 1, 2, 2, "alpha\nbeta\n", nil},
		{"whole document no trailing newline", "alpha\nbeta", 1, 2, 2, "alpha\nbeta", nil},
		{"empty content", "", 1, 1, 0, "", shared.ErrClipboardEmpty},
		{"below range", "alpha\nbeta\n", 0, 1, 2, "", shared.ErrClipboardUnavailable},
		{"above range", "alpha\nbeta\n", 3, 3, 2, "", shared.ErrClipboardUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractSourceLineRange(tt.content, tt.start, tt.end, tt.total)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("payload = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractSourceLineRangeReversedStartAndEndIsNormalized(t *testing.T) {
	got, err := extractSourceLineRange("alpha\nbeta\ngamma\n", 3, 1, 3)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "alpha\nbeta\ngamma\n" {
		t.Fatalf("reversed range = %q, want full slice", got)
	}
}

func plainReviewModel(t *testing.T, content string) Model {
	t.Helper()
	session := domain.Session{StepID: "check", Documents: []domain.Document{{
		ID: "doc", Label: "Doc", Source: "doc.txt", Format: "text", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCopyItemRequestCopiesCurrentSourceLine(t *testing.T) {
	m := plainReviewModel(t, "alpha\r\nbeta\r\ngamma")
	m.cursor = 2
	req, ok := m.CopyItemRequest()
	if !ok || req.Target.Surface != shared.ClipboardSurfaceReviewLine || req.Target.Label != "L2 in Doc" {
		t.Fatalf("target = %#v ok=%v", req.Target, ok)
	}
	got, err := extract(req.Loader())
	if err != nil {
		t.Fatalf("loader err: %v", err)
	}
	if got != "beta\r\n" {
		t.Fatalf("payload = %q, want %q", got, "beta\r\n")
	}
}

func TestCopyItemRequestCopiesRangeAcrossReversedSelection(t *testing.T) {
	m := plainReviewModel(t, "one\ntwo\nthree\nfour\n")
	m.mode = ModeSelectRange
	m.cursor = 3
	m.rangeEnd = 1
	req, ok := m.CopyItemRequest()
	if !ok || req.Target.Surface != shared.ClipboardSurfaceReviewRange {
		t.Fatalf("target = %#v ok=%v", req.Target, ok)
	}
	got, err := extract(req.Loader())
	if err != nil {
		t.Fatalf("loader err: %v", err)
	}
	if got != "one\ntwo\nthree\n" {
		t.Fatalf("payload = %q", got)
	}
	// The label should show the normalized span, not the raw cursor pair.
	if req.Target.Label != "L1–L3 in Doc" {
		t.Fatalf("label = %q", req.Target.Label)
	}
}

func TestCopyAllRequestReturnsUntouchedDocumentContent(t *testing.T) {
	original := "line1\r\nline2\r\nline3"
	m := plainReviewModel(t, original)
	req, ok := m.CopyAllRequest()
	if !ok || req.Target.Surface != shared.ClipboardSurfaceReviewDocument {
		t.Fatalf("target = %#v ok=%v", req.Target, ok)
	}
	got, err := extract(req.Loader())
	if err != nil {
		t.Fatalf("loader err: %v", err)
	}
	if got != original {
		t.Fatalf("payload = %q, want %q", got, original)
	}
	// Mutating the model's document content after the request is built must
	// not change the loaded payload — the loader captured the source string.
	m.docs[m.active].meta.Content = "mutated"
	got2, err := extract(req.Loader())
	if err != nil {
		t.Fatalf("second loader err: %v", err)
	}
	if got2 != original {
		t.Fatalf("second payload changed after mutation: %q", got2)
	}
}

func TestCopyAllRequestRejectsEmptyDocument(t *testing.T) {
	m := plainReviewModel(t, "")
	req, ok := m.CopyAllRequest()
	if !ok {
		t.Fatal("empty document should still produce a request; loader rejects at run time")
	}
	got, err := extract(req.Loader())
	if err == nil {
		t.Fatalf("empty document loader = %q, want error", got)
	}
	if !errors.Is(err, shared.ErrClipboardEmpty) {
		t.Fatalf("empty error = %v, want ErrClipboardEmpty", err)
	}
}

func TestCopyItemRequestOnHunkCopiesHunkText(t *testing.T) {
	content := strings.Join([]string{
		"diff --git a/one.txt b/one.txt",
		"--- a/one.txt",
		"+++ b/one.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/two.txt b/two.txt",
		"--- a/two.txt",
		"+++ b/two.txt",
		"@@ -1 +1 @@",
		"-two",
		"+TWO",
	}, "\n")
	session := domain.Session{StepID: "diff", Documents: []domain.Document{{
		ID: "diff", Label: "Diff", Source: "change.diff", Format: "diff", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	// Cursor on hunk 1's header (line 4).
	m.cursor = 4
	req, ok := m.CopyItemRequest()
	if !ok || req.Target.Surface != shared.ClipboardSurfaceReviewHunk {
		t.Fatalf("target = %#v ok=%v", req.Target, ok)
	}
	got, err := extract(req.Loader())
	if err != nil {
		t.Fatalf("loader err: %v", err)
	}
	want := "@@ -1 +1 @@\n-old\n+new\n"
	if got != want {
		t.Fatalf("hunk payload = %q, want %q", got, want)
	}
}

func TestCopyAllRequestOnDiffCopiesCurrentFileSection(t *testing.T) {
	content := strings.Join([]string{
		"diff --git a/one.txt b/one.txt",
		"--- a/one.txt",
		"+++ b/one.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/two.txt b/two.txt",
		"--- a/two.txt",
		"+++ b/two.txt",
		"@@ -1 +1 @@",
		"-two",
		"+TWO",
	}, "\n")
	session := domain.Session{StepID: "diff", Documents: []domain.Document{{
		ID: "diff", Label: "Diff", Source: "change.diff", Format: "diff", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	// Cursor inside file two's hunk (line 12).
	m.cursor = 12
	req, ok := m.CopyAllRequest()
	if !ok || req.Target.Surface != shared.ClipboardSurfaceReviewFileDiff {
		t.Fatalf("target = %#v ok=%v", req.Target, ok)
	}
	got, err := extract(req.Loader())
	if err != nil {
		t.Fatalf("loader err: %v", err)
	}
	want := strings.Join([]string{
		"diff --git a/two.txt b/two.txt",
		"--- a/two.txt",
		"+++ b/two.txt",
		"@@ -1 +1 @@",
		"-two",
		"+TWO",
	}, "\n")
	if got != want {
		t.Fatalf("file-diff payload = %q, want %q", got, want)
	}
	if !strings.Contains(req.Target.Label, "two.txt") {
		t.Fatalf("label = %q, want file name", req.Target.Label)
	}
}

func TestCopyItemRequestPreviewBlockUsesMarkdownSource(t *testing.T) {
	content := "# Heading\n\nParagraph text.\n\n- list one\n- list two\n\n```go\nfmt.Println(1)\n```\n"
	session := domain.Session{StepID: "md", Documents: []domain.Document{{
		ID: "md", Label: "MD", Source: "notes.md", Format: "markdown", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	// Preview mode is the default for Markdown; move to the list block.
	m.previewBlock = 2
	req, ok := m.CopyItemRequest()
	if !ok || req.Target.Surface != shared.ClipboardSurfaceReviewBlock {
		t.Fatalf("target = %#v ok=%v", req.Target, ok)
	}
	got, err := extract(req.Loader())
	if err != nil {
		t.Fatalf("loader err: %v", err)
	}
	want := "- list one\n- list two\n"
	if got != want {
		t.Fatalf("list block payload = %q, want %q", got, want)
	}
	// The fenced block preserves its opening/closing fences and language.
	m.previewBlock = 3
	req, _ = m.CopyItemRequest()
	got, err = extract(req.Loader())
	if err != nil {
		t.Fatalf("fence loader err: %v", err)
	}
	if !strings.HasPrefix(got, "```go\n") || !strings.HasSuffix(got, "```\n") {
		t.Fatalf("fence payload = %q, want raw fences", got)
	}
}

func TestCopyItemRequestPreviewFallsThroughWhenNoBlockAvailable(t *testing.T) {
	content := "# Heading\n\nText\n"
	session := domain.Session{StepID: "md", Documents: []domain.Document{{
		ID: "md", Label: "MD", Source: "notes.md", Format: "markdown", Content: content, SHA256: domain.Digest(content),
	}}}
	m, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	m.previewBlock = 999
	req, ok := m.CopyItemRequest()
	if !ok {
		t.Fatal("preview block copy should still build a request whose loader rejects")
	}
	if _, err := extract(req.Loader()); !errors.Is(err, shared.ErrClipboardUnavailable) {
		t.Fatalf("out-of-range block err = %v, want ErrClipboardUnavailable", err)
	}
}

func TestCopyRequestsAreUnavailableWhenNoDocuments(t *testing.T) {
	m := Model{}
	if _, ok := m.CopyItemRequest(); ok {
		t.Fatal("empty model reported a copy item is available")
	}
	if _, ok := m.CopyAllRequest(); ok {
		t.Fatal("empty model reported copy all is available")
	}
}

func TestMatchesCopyKeys(t *testing.T) {
	m := Model{}
	if !m.MatchesCopyItem("y") || m.MatchesCopyItem("Y") {
		t.Fatal("MatchesCopyItem should accept y and reject Y")
	}
	if !m.MatchesCopyAll("Y") || m.MatchesCopyAll("y") {
		t.Fatal("MatchesCopyAll should accept Y and reject y")
	}
}
