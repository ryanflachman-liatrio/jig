package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		root string
		path string
		want string
		fail bool
	}{
		{name: "persistence off", path: "report.md", want: "report.md"},
		{name: "relative path", root: root, path: "nested/report.md", want: filepath.Join(root, "nested/report.md")},
		{name: "absolute", root: root, path: filepath.Join(root, "report.md"), fail: true},
		{name: "parent traversal", root: root, path: "nested/../report.md", fail: true},
		{name: "symlink escape", root: root, path: "escape/report.md", fail: true},
		{name: "dangling symlink", root: root, path: "dangling", fail: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExecutionPath(tt.root, tt.path)
			if tt.fail {
				if err == nil {
					t.Fatalf("ExecutionPath(%q, %q) = %q, want error", tt.root, tt.path, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ExecutionPath(%q, %q) = %q, %v; want %q, nil", tt.root, tt.path, got, err, tt.want)
			}
		})
	}
}
