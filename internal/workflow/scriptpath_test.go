package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptPath(t *testing.T) {
	root := filepath.FromSlash("/proj")
	cases := []struct {
		name         string
		root, script string
		want         string
	}{
		{"relative joins onto root", root, "scripts/test.sh", filepath.Join(root, "scripts/test.sh")},
		{"absolute is unchanged", root, filepath.FromSlash("/usr/bin/x.sh"), filepath.FromSlash("/usr/bin/x.sh")},
		{"empty root leaves script relative", "", "scripts/test.sh", "scripts/test.sh"},
		{"empty script stays empty", root, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ScriptPath(c.root, c.script); got != c.want {
				t.Errorf("ScriptPath(%q, %q) = %q, want %q", c.root, c.script, got, c.want)
			}
		})
	}
}

func TestRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// From a nested dir, RepoRoot walks up to the dir containing .git. Resolve
	// symlinks on both sides: t.TempDir may return /var/... which is a symlink to
	// /private/var/... on macOS, while filepath.Abs inside RepoRoot does not.
	got, err := filepath.EvalSymlinks(RepoRoot(sub))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Errorf("RepoRoot(%q) = %q, want %q", sub, got, want)
	}

	// Outside any repo → "".
	if r := RepoRoot(t.TempDir()); r != "" {
		t.Errorf("RepoRoot with no .git = %q, want empty", r)
	}
}

// TestValidateScriptAnchoredAtRepoRoot exercises the load-time existence check:
// a command step's `script` is resolved from the project (git repo) root, so a
// script at <root>/scripts/ok.sh validates when referenced repo-root-relative
// and fails when the (baseDir-relative) path does not exist under the root.
func TestValidateScriptAnchoredAtRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "ok.sh"), []byte("echo ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The workflow lives in a subdir; script paths are NOT relative to it.
	baseDir := filepath.Join(root, "workflows")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	const okTOML = `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
script = "scripts/ok.sh"`
	if _, err := Decode(okTOML, baseDir); err != nil {
		t.Errorf("repo-root-relative script should validate; got %v", err)
	}

	// Same path spelled relative to the workflow dir must fail — proving the
	// anchor is the repo root, not baseDir.
	const badTOML = `
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
script = "missing/ok.sh"`
	_, err := Decode(badTOML, baseDir)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("script missing at repo root should fail with 'not found'; got %v", err)
	}
}
