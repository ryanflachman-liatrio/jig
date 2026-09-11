package review

import (
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

func TestBuildDiffPresentationProjectsSourceCoordinates(t *testing.T) {
	content := strings.Join([]string{
		"diff --git a/example.go b/example.go",
		"index 1111111..2222222 100644",
		"--- a/example.go",
		"+++ b/example.go",
		"@@ -10,3 +10,3 @@ func example()",
		" context",
		"-old()",
		"+new()",
		" contextAgain",
	}, "\n")
	presentation := buildDiffPresentation(content, strings.Split(content, "\n"))
	if presentation.parseErr != nil {
		t.Fatal(presentation.parseErr)
	}
	if len(presentation.hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(presentation.hunks))
	}
	hunk := presentation.hunks[0]
	if hunk.ordinal != 1 || hunk.fileIndex != 0 || hunk.fileName != "example.go" || hunk.startPatchLine != 5 || hunk.endPatchLine != 9 || hunk.header != "@@ -10,3 +10,3 @@ func example()" {
		t.Fatalf("hunk = %#v", hunk)
	}
	want := []struct {
		line           int
		kind           diffRowKind
		old, new       int
		hasOld, hasNew bool
	}{
		{5, diffRowHunkHeader, 0, 0, false, false},
		{6, diffRowContext, 10, 10, true, true},
		{7, diffRowDelete, 11, 0, true, false},
		{8, diffRowAdd, 0, 11, false, true},
		{9, diffRowContext, 12, 12, true, true},
	}
	for _, tt := range want {
		row := presentation.rows[tt.line-1]
		if row.patchLine != tt.line || row.kind != tt.kind || row.oldLine != tt.old || row.newLine != tt.new || row.hasOld != tt.hasOld || row.hasNew != tt.hasNew {
			t.Errorf("patch line %d = %#v", tt.line, row)
		}
	}
	assertPatchLines(t, presentation)
}

func TestBuildDiffPresentationHandlesPatchShapes(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(*testing.T, *diffPresentation)
	}{
		{
			name: "omitted counts and no newline markers",
			content: strings.Join([]string{
				"--- a/file.txt", "+++ b/file.txt", "@@ -1 +1 @@ section", "-old", "\\ No newline at end of file", "+new", "\\ No newline at end of file",
			}, "\n"),
			check: func(t *testing.T, p *diffPresentation) {
				if len(p.hunks) != 1 || p.hunks[0].header != "@@ -1 +1 @@ section" {
					t.Fatalf("hunks = %#v", p.hunks)
				}
				for _, line := range []int{5, 7} {
					if p.rows[line-1].kind != diffRowNoNewline {
						t.Errorf("line %d kind = %v, want no-newline", line, p.rows[line-1].kind)
					}
				}
			},
		},
		{
			name: "empty context row",
			content: strings.Join([]string{
				"--- a/file.txt", "+++ b/file.txt", "@@ -1,3 +1,3 @@", " first", "", "-old", "+new",
			}, "\n"),
			check: func(t *testing.T, p *diffPresentation) {
				if got := p.rows[4]; got.kind != diffRowContext || got.oldLine != 2 || got.newLine != 2 {
					t.Errorf("empty context = %#v", got)
				}
			},
		},
		{
			name: "multiple files",
			content: strings.Join([]string{
				"diff --git a/one.txt b/one.txt", "--- a/one.txt", "+++ b/one.txt", "@@ -1 +1 @@", "-one", "+ONE",
				"diff --git a/two.txt b/two.txt", "--- a/two.txt", "+++ b/two.txt", "@@ -4 +4 @@", "-two", "+TWO",
			}, "\n"),
			check: func(t *testing.T, p *diffPresentation) {
				if len(p.hunks) != 2 || p.hunks[0].fileIndex != 0 || p.hunks[1].fileIndex != 1 || p.hunks[1].ordinal != 2 {
					t.Fatalf("hunks = %#v", p.hunks)
				}
				if got := p.rows[10]; got.fileIndex != 1 || got.oldLine != 4 || !got.hasOld {
					t.Fatalf("second file delete = %#v", got)
				}
			},
		},
		{
			name: "new and deleted files",
			content: strings.Join([]string{
				"diff --git a/new.txt b/new.txt", "new file mode 100644", "--- /dev/null", "+++ b/new.txt", "@@ -0,0 +1,2 @@", "+first", "+second",
				"diff --git a/old.txt b/old.txt", "deleted file mode 100644", "--- a/old.txt", "+++ /dev/null", "@@ -1,2 +0,0 @@", "-first", "-second",
			}, "\n"),
			check: func(t *testing.T, p *diffPresentation) {
				if got := p.rows[5]; got.hasOld || !got.hasNew || got.newLine != 1 {
					t.Errorf("new file first row = %#v", got)
				}
				if got := p.rows[12]; !got.hasOld || got.hasNew || got.oldLine != 1 {
					t.Errorf("deleted file first row = %#v", got)
				}
			},
		},
		{
			name: "rename copy mode and binary metadata",
			content: strings.Join([]string{
				"diff --git a/old.txt b/renamed.txt", "similarity index 100%", "rename from old.txt", "rename to renamed.txt",
				"diff --git a/source.txt b/copy.txt", "similarity index 100%", "copy from source.txt", "copy to copy.txt",
				"diff --git a/script.sh b/script.sh", "old mode 100644", "new mode 100755",
				"diff --git a/image.png b/image.png", "index 1111111..2222222 100644", "Binary files a/image.png and b/image.png differ",
			}, "\n"),
			check: func(t *testing.T, p *diffPresentation) {
				if len(p.hunks) != 0 {
					t.Fatalf("hunks = %#v, want none", p.hunks)
				}
				// Header-only files (rename, copy, mode-only, binary) still
				// carry FileIndex on every metadata row so a whole-file copy
				// can slice the original patch content, but they have no code
				// rows so hasOld/hasNew stay false.
				wantOwners := []int{0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 3, 3, 3}
				if len(p.rows) != len(wantOwners) {
					t.Fatalf("rows = %d, want %d", len(p.rows), len(wantOwners))
				}
				for i, row := range p.rows {
					if row.kind != diffRowMetadata || row.hasOld || row.hasNew {
						t.Errorf("metadata row = %#v", row)
					}
					if row.fileIndex != wantOwners[i] {
						t.Errorf("row %d file = %d, want %d", i+1, row.fileIndex, wantOwners[i])
					}
				}
				if len(p.display.Files) != 4 {
					t.Fatalf("files = %d, want 4", len(p.display.Files))
				}
				wantFiles := []struct {
					name       string
					start, end int
				}{
					{"renamed.txt", 1, 4},
					{"copy.txt", 5, 8},
					{"script.sh", 9, 11},
					{"image.png", 12, 14},
				}
				for i, want := range wantFiles {
					got := p.display.Files[i]
					if got.Name != want.name || got.StartPatchLine != want.start || got.EndPatchLine != want.end {
						t.Errorf("file %d = %#v, want %+v", i, got, want)
					}
				}
			},
		},
		{
			name:    "CRLF and trailing empty row",
			content: "--- a/file.txt\r\n+++ b/file.txt\r\n@@ -1 +1 @@\r\n-old\r\n+new\r\n",
			check: func(t *testing.T, p *diffPresentation) {
				if got := p.rows[3]; got.oldLine != 1 || !got.hasOld {
					t.Errorf("CRLF delete = %#v", got)
				}
				if got := p.rows[len(p.rows)-1]; got.patchLine != len(p.rows) || got.kind != diffRowMetadata {
					t.Errorf("trailing row = %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := buildDiffPresentation(tt.content, strings.Split(tt.content, "\n"))
			if p.parseErr != nil {
				t.Fatal(p.parseErr)
			}
			tt.check(t, p)
			assertPatchLines(t, p)
		})
	}
}

func TestBuildDiffPresentationFallsBackForMalformedPatch(t *testing.T) {
	content := "--- a/file.txt\n+++ b/file.txt\n@@ -1,2 +1 @@\n-old\n+new"
	p := buildDiffPresentation(content, strings.Split(content, "\n"))
	if p.parseErr == nil {
		t.Fatal("parseErr = nil, want malformed patch fallback")
	}
	if len(p.hunks) != 0 {
		t.Fatalf("hunks = %#v, want none", p.hunks)
	}
	assertPatchLines(t, p)
}

func TestHunkSectionTitle(t *testing.T) {
	tests := map[string]string{
		"@@ -10,3 +10,3 @@ func example()": "func example()",
		"@@ -1 +1 @@ section\r":            "section",
		"@@ -1 +1 @@":                      "",
		"not a hunk":                       "",
	}
	for header, want := range tests {
		if got := hunkSectionTitle(header); got != want {
			t.Errorf("hunkSectionTitle(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestDiffFileNamePrefersTheChangedFile(t *testing.T) {
	tests := []struct {
		file gitdiff.File
		want string
	}{
		{file: gitdiff.File{OldName: "a/old.go", NewName: "b/new.go"}, want: "new.go"},
		{file: gitdiff.File{OldName: "a/deleted.go", NewName: "/dev/null"}, want: "deleted.go"},
		{file: gitdiff.File{}, want: "file"},
	}
	for _, tt := range tests {
		if got := diffFileName(&tt.file); got != tt.want {
			t.Errorf("diffFileName(%#v) = %q, want %q", tt.file, got, tt.want)
		}
	}
}

func assertPatchLines(t *testing.T, presentation *diffPresentation) {
	t.Helper()
	for i, row := range presentation.rows {
		if row.patchLine != i+1 {
			t.Errorf("row %d patchLine = %d", i, row.patchLine)
		}
	}
}
