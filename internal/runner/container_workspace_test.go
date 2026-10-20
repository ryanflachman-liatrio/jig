package runner

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func d05Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@invalid"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func d05Write(t *testing.T, root, name string, data []byte, mode os.FileMode) {
	t.Helper()
	target := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		t.Fatal(err)
	}
}
func d05Source(t *testing.T) (string, engine.StepRequest) {
	t.Helper()
	root := t.TempDir()
	d05Git(t, root, "init", "--quiet", "--template=")
	d05Write(t, root, "src/main.go", []byte("package main\n"), 0644)
	d05Git(t, root, "add", ".")
	d05Git(t, root, "commit", "--quiet", "-m", "source")
	return root, engine.StepRequest{ExecutionDir: root, SourceCommit: d05Git(t, root, "rev-parse", "HEAD"), Step: &workflow.Step{ID: "build", Type: workflow.StepCommand}}
}
func TestD05ContainerWorkspaceSource(t *testing.T) {
	source, req := d05Source(t)
	binary := []byte{0, 255, 1, 2, 0, 3}
	d05Write(t, source, "bin/tool", binary, 0755)
	d05Write(t, source, ".env", []byte("SYNTHETIC_PRIVATE"), 0600)
	d05Write(t, source, "nested/.aws/config", []byte("SYNTHETIC_PRIVATE"), 0600)
	d05Write(t, source, ".gitignore", []byte("ignored.txt\n"), 0644)
	d05Write(t, source, ".gitattributes", []byte("src/main.go text eol=crlf\n"), 0644)
	if err := os.Symlink("src/main.go", filepath.Join(source, "source-link")); err != nil {
		t.Fatal(err)
	}
	d05Git(t, source, "add", "--all", "--force")
	d05Git(t, source, "commit", "--quiet", "-m", "selected")
	req.SourceCommit = d05Git(t, source, "rev-parse", "HEAD")
	privateObject := d05Git(t, source, "rev-parse", "HEAD:.env")
	d05Write(t, source, "src/main.go", []byte("NEW HEAD"), 0644)
	d05Git(t, source, "add", ".")
	d05Git(t, source, "commit", "--quiet", "-m", "new head")
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(sentinel, []byte("HOST_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "src/main.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(source, "src/main.go")); err != nil {
		t.Fatal(err)
	}
	d05Write(t, source, "ignored.txt", []byte("IGNORED_PRIVATE"), 0600)
	d05Write(t, source, "untracked.txt", []byte("UNTRACKED_PRIVATE"), 0600)
	t.Setenv("GIT_DIR", "/missing/host/git")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", "/missing/host/hooks")
	parent := t.TempDir()
	w, err := prepareContainerWorkspace(context.Background(), req, parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Remove(); err != nil {
			t.Error(err)
		}
	})
	got, err := os.ReadFile(filepath.Join(w.Workspace, "source-link"))
	if err != nil || string(got) != "package main\n" {
		t.Fatalf("selected source changed: %q %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(w.Workspace, "bin/tool"))
	if err != nil || string(got) != string(binary) {
		t.Fatalf("binary changed: %v", err)
	}
	info, err := os.Stat(filepath.Join(w.Workspace, "bin/tool"))
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("executable mode lost", err)
	}
	for _, name := range []string{".env", "nested/.aws", "ignored.txt", "untracked.txt", ".git/objects/info/alternates"} {
		if _, err := os.Lstat(filepath.Join(w.Workspace, name)); !os.IsNotExist(err) {
			t.Fatalf("private source included: %s", name)
		}
	}
	if got := d05Git(t, w.Workspace, "rev-list", "--all", "--count"); got != "1" {
		t.Fatalf("source history exposed: %s", got)
	}
	if got := d05Git(t, w.Workspace, "remote"); got != "" {
		t.Fatal("source remotes exposed")
	}
	if got := d05Git(t, w.Workspace, "show", "HEAD:src/main.go"); got != "package main" {
		t.Fatal("synthetic Git transformed source")
	}
	cmd := exec.Command("git", "-C", w.Workspace, "cat-file", "-e", privateObject)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if err := cmd.Run(); err == nil {
		t.Fatal("excluded Git object exposed")
	}
	var manifest containerSourceManifest
	raw, err := os.ReadFile(filepath.Join(w.Metadata, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Commit != req.SourceCommit || len(manifest.Excluded) != 2 {
		t.Fatalf("bad source identity/exclusions: %+v", manifest)
	}
	got, err = os.ReadFile(sentinel)
	if err != nil || string(got) != "HOST_SENTINEL" {
		t.Fatal("host source modified")
	}
	if err := os.WriteFile(filepath.Join(w.Workspace, "bin/tool"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(source, "bin/tool"))
	if err != nil || string(got) != string(binary) {
		t.Fatal("source and staging share writable files")
	}
}
func TestD05ContainerWorkspaceRejectsUnsafeSource(t *testing.T) {
	for _, mode := range []string{"outside link", "dangling link", "cycle", "excluded link", "LFS", "submodule", "requirements", "missing identity", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			source, req := d05Source(t)
			symlink := func(target, name string) {
				t.Helper()
				if err := os.Symlink(target, filepath.Join(source, name)); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "outside link":
				symlink("../outside", "link")
			case "dangling link":
				symlink("missing", "link")
			case "cycle":
				symlink("b", "a")
				symlink("a", "b")
			case "excluded link":
				d05Write(t, source, ".env", []byte("synthetic"), 0600)
				symlink(".env", "link")
			case "LFS":
				d05Write(t, source, "file", []byte("version https://git-lfs.github.com/spec/v1\noid sha256:synthetic\n"), 0644)
			case "submodule":
				d05Git(t, source, "update-index", "--add", "--cacheinfo", "160000,"+req.SourceCommit+",submodule")
			case "requirements":
				req.Step.WorkspaceRequirements = []string{"source_history"}
			}
			if mode != "submodule" {
				d05Git(t, source, "add", "--all", "--force")
			}
			d05Git(t, source, "commit", "--quiet", "--allow-empty", "-m", "case")
			req.SourceCommit = d05Git(t, source, "rev-parse", "HEAD")
			if mode == "missing identity" {
				req.SourceCommit = ""
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			parent := t.TempDir()
			w, err := prepareContainerWorkspace(ctx, req, parent)
			if err == nil || w != nil {
				t.Fatalf("unsafe source accepted/retained: %v %+v", err, w)
			}
			files, err := os.ReadDir(parent)
			if err != nil || len(files) != 0 {
				t.Fatalf("failed preparation leaked files: %v %v", files, err)
			}
		})
	}
}
func TestD05ContainerTreeBounds(t *testing.T) {
	oid := strings.Repeat("a", 40)
	record := func(mode, name string, size int64) string {
		return fmt.Sprintf("%s blob %s %d\t%s\x00", mode, oid, size, name)
	}
	for _, raw := range []string{
		record("100644", "../escape", 1), record("100644", "/escape", 1), record("100644", "a\\b", 1),
		record("100644", "big", containerSourceFileBytes+1), record("100644", "a", -1),
		record("100644", "A/file", 1) + record("100644", "a/other", 1), record("100644", "same", 1) + record("100644", "same", 1),
		record("100644", "café/file", 1) + record("100644", "cafe\u0301/other", 1),
		record("100644", strings.Repeat("d/", 64)+"file", 1), record("100644", "bad\tname", 1),
		record("120000", "link", 4097), record("100600", "special", 1),
		strings.Repeat(record("100644", "file", 1), containerSourceFiles+1),
	} {
		if _, _, err := parseContainerTree([]byte(raw), oid); err == nil {
			t.Fatal("unsafe tree accepted")
		}
	}
}

func TestD05ContainerWorkspaceRejectsCorruptBlob(t *testing.T) {
	source, req := d05Source(t)
	oid := d05Git(t, source, "rev-parse", "HEAD:src/main.go")
	var corrupt bytes.Buffer
	zw := zlib.NewWriter(&corrupt)
	if _, err := zw.Write([]byte("blob 13\x00XXXXXXXXXXXXX")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(source, ".git", "objects", oid[:2], oid[2:])
	if err := os.Chmod(object, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(object, corrupt.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	if _, err := prepareContainerWorkspace(context.Background(), req, parent); err == nil {
		t.Fatal("corrupt blob staged")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("corruption leaked staging", err)
	}
}
func TestD05ContainerWorkspaceCleanupIdentity(t *testing.T) {
	_, req := d05Source(t)
	w, err := prepareContainerWorkspace(context.Background(), req, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := w.Root + "-original"
	if err := os.Rename(w.Root, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(w.Root, 0700); err != nil {
		t.Fatal(err)
	}
	d05Write(t, w.Root, "sentinel", []byte("unowned"), 0600)
	if err := w.Remove(); err == nil {
		t.Fatal("replacement root deleted")
	}
	if data, err := os.ReadFile(filepath.Join(w.Root, "sentinel")); err != nil || string(data) != "unowned" {
		t.Fatal("unowned root changed")
	}
}

func TestD05ContainerWorkspaceFrozenCaptures(t *testing.T) {
	source, req := d05Source(t)
	d05Write(t, source, "scripts with spaces/build.sh", []byte("printf ORIGINAL"), 0600)
	d05Write(t, source, "schema.json", []byte(`{"type":"object"}`), 0600)
	wf, err := workflow.Decode("[workflow]\nname='capture'\nversion='1'\n[[step]]\nid='build'\ntype='command'\nscript='scripts with spaces/build.sh'\noutput='result.json'\n[step.validate]\noutput_schema='schema.json'\n", source)
	if err != nil {
		t.Fatal(err)
	}
	owned, assets, err := workflow.PrepareAuthoring(wf, source)
	if err != nil {
		t.Fatal(err)
	}
	req.Step, req.Assets, req.AssetOwner = &owned.Steps[0], assets, "build"
	d05Write(t, source, "scripts with spaces/build.sh", []byte("CHANGED"), 0600)
	if err := os.Remove(filepath.Join(source, "schema.json")); err != nil {
		t.Fatal(err)
	}
	w, err := prepareContainerWorkspace(context.Background(), req, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Remove(); err != nil {
			t.Error(err)
		}
	})
	for name, want := range map[string]string{"command.sh": "printf ORIGINAL", "validation-schema.json": `{"type":"object"}`} {
		data, err := os.ReadFile(filepath.Join(w.Captures, name))
		if err != nil || string(data) != want {
			t.Fatalf("capture %s changed: %q %v", name, data, err)
		}
		info, err := os.Stat(filepath.Join(w.Captures, name))
		if err != nil || info.Mode().Perm() != 0400 {
			t.Fatal("capture is writable", err)
		}
	}
	req.Assets = nil
	parent := t.TempDir()
	if _, err := prepareContainerWorkspace(context.Background(), req, parent); err == nil {
		t.Fatal("missing capture accepted")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("capture failure leaked staging", err)
	}
}

func TestD05ContainerSyntheticGitDoesNotRunSourceConversions(t *testing.T) {
	source, req := d05Source(t)
	// Set an attribute that would reject this ordinary ASCII file during add.
	// Insert it directly into the source index to leave the source blob intact.
	d05Write(t, source, ".gitattributes", []byte("src/main.go working-tree-encoding=UTF-16LE\n"), 0644)
	attributes := d05Git(t, source, "hash-object", "-w", ".gitattributes")
	d05Git(t, source, "update-index", "--add", "--cacheinfo", "100644,"+attributes+",.gitattributes")
	d05Git(t, source, "commit", "--quiet", "-m", "source attributes")
	req.SourceCommit = d05Git(t, source, "rev-parse", "HEAD")
	originalBlob := d05Git(t, source, "rev-parse", "HEAD:src/main.go")
	w, err := prepareContainerWorkspace(context.Background(), req, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Remove(); err != nil {
			t.Error(err)
		}
	})
	if got := d05Git(t, w.Workspace, "rev-parse", "HEAD:src/main.go"); got != originalBlob {
		t.Fatal("source conversion changed baseline bytes")
	}
}

func TestD05ContainerTreeAggregateLimits(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, mode := range []string{"bytes", "paths"} {
		t.Run(mode, func(t *testing.T) {
			var tree strings.Builder
			count, size := 9, int64(containerSourceFileBytes)
			if mode == "paths" {
				count = 14000
				size = 0
			}
			for i := 0; i < count; i++ {
				fmt.Fprintf(&tree, "100644 blob %s %d\td%d/sub%d/file\x00", oid, size, i, i)
			}
			if _, _, err := parseContainerTree([]byte(tree.String()), oid); err == nil {
				t.Fatal("aggregate source limit not enforced")
			}
		})
	}
}

func TestD05ContainerStagingRejectsRelativeTemporaryRoot(t *testing.T) {
	_, req := d05Source(t)
	t.Setenv("TMPDIR", "relative-staging")
	if _, err := prepareContainerWorkspace(context.Background(), req, ""); err == nil {
		t.Fatal("implicit relative staging accepted")
	}
}
