package diffview

import (
	"os"
	"strings"
	"testing"
)

func TestParseHunkOnlySnippetUsesNeutralFilename(t *testing.T) {
	presentation := Parse("@@ -1 +1 @@ replace handler\n-old\n+new")
	if presentation.ParseErr != nil {
		t.Fatal(presentation.ParseErr)
	}
	if len(presentation.Hunks) != 1 || presentation.Hunks[0].FileName != "file" {
		t.Fatalf("hunks = %#v, want one neutral-file hunk", presentation.Hunks)
	}
	if got := RenderHunkHeader(presentation.Hunks[0]); !strings.Contains(got, "Hunk 1") || !strings.Contains(got, "file") || !strings.Contains(got, "replace handler") {
		t.Fatalf("hunk heading = %q", got)
	}
}

func TestParseTracksFileSpansAndOwnsMetadata(t *testing.T) {
	content := strings.Join([]string{
		"diff --git a/first.txt b/first.txt",
		"--- a/first.txt",
		"+++ b/first.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/second.txt b/second.txt",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/second.txt",
		"@@ -0,0 +1 @@",
		"+added",
	}, "\n")
	p := Parse(content)
	if p.ParseErr != nil {
		t.Fatal(p.ParseErr)
	}
	if len(p.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(p.Files))
	}
	want := []File{
		{FileIndex: 0, Name: "first.txt", StartPatchLine: 1, EndPatchLine: 6},
		{FileIndex: 1, Name: "second.txt", StartPatchLine: 7, EndPatchLine: 12},
	}
	for i, got := range p.Files {
		if got != want[i] {
			t.Errorf("file %d = %#v, want %#v", i, got, want[i])
		}
	}
	for _, row := range p.Rows {
		if row.FileIndex < 0 {
			t.Errorf("row %d still unassigned: %#v", row.PatchLine, row)
		}
	}
}

func TestParseTracksFileSpansForPlainUnifiedPatch(t *testing.T) {
	content := strings.Join([]string{
		"--- a/first.txt",
		"+++ b/first.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"--- a/second.txt",
		"+++ b/second.txt",
		"@@ -1 +1 @@",
		"-two",
		"+TWO",
	}, "\n")
	p := Parse(content)
	if p.ParseErr != nil {
		t.Fatal(p.ParseErr)
	}
	if len(p.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(p.Files))
	}
	want := []File{
		{FileIndex: 0, Name: "first.txt", StartPatchLine: 1, EndPatchLine: 5},
		{FileIndex: 1, Name: "second.txt", StartPatchLine: 6, EndPatchLine: 10},
	}
	for i, got := range p.Files {
		if got != want[i] {
			t.Errorf("file %d = %#v, want %#v", i, got, want[i])
		}
	}
}

func TestParseTracksFileSpansForCRLFHeaders(t *testing.T) {
	content := "diff --git a/one.txt b/one.txt\r\n--- a/one.txt\r\n+++ b/one.txt\r\n@@ -1 +1 @@\r\n-old\r\n+new\r\n"
	p := Parse(content)
	if p.ParseErr != nil {
		t.Fatal(p.ParseErr)
	}
	if len(p.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(p.Files))
	}
	f := p.Files[0]
	if f.StartPatchLine != 1 || f.EndPatchLine < 6 || f.Name != "one.txt" {
		t.Fatalf("file = %#v", f)
	}
}

func TestReviewUIDemoFixtureParses(t *testing.T) {
	content, err := os.ReadFile("../../../.agents/jig/fixtures/review-ui-demo.diff")
	if err != nil {
		t.Fatal(err)
	}
	presentation := Parse(string(content))
	if presentation.ParseErr != nil {
		t.Fatal(presentation.ParseErr)
	}
	if len(presentation.Hunks) < 5 {
		t.Fatalf("hunks = %d, want a production-sized fixture", len(presentation.Hunks))
	}
	rows := presentation.DisplayRows(nil)
	if len(rows) == 0 || !rows[0].Separator && rows[0].PatchLine == 0 {
		t.Fatalf("display rows = %#v", rows)
	}
}
