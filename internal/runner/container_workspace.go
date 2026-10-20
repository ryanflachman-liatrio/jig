package runner

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

const (
	containerSourceFiles     = 20000
	containerSourcePaths     = 40000
	containerSourceBytes     = 256 << 20
	containerSourceFileBytes = 32 << 20
	containerTreeBytes       = 8 << 20
	containerSourceSetup     = 60 * time.Second
)

// containerWorkspace owns a private root. Only Workspace and Scratch may be
// writable workload mounts; Captures and Metadata are separate host-owned data.
// Remove requires confirmed termination of every helper/container using it.
type containerWorkspace struct {
	Root, Workspace, Scratch, Captures, Metadata string
	Inputs                                       string
	InputEnv                                     map[string]string
	identity                                     os.FileInfo
}

func (w *containerWorkspace) Remove() error {
	info, err := os.Lstat(w.Root)
	if err != nil || !info.IsDir() || !os.SameFile(info, w.identity) {
		return fmt.Errorf("container staging ownership changed")
	}
	return os.RemoveAll(w.Root)
}

type containerSourceEntry struct {
	name, oid, mode string
	size            int64
}
type containerSourceManifest struct {
	Commit   string   `json:"source_commit"`
	Files    int      `json:"files"`
	Bytes    int64    `json:"bytes"`
	Excluded []string `json:"excluded"`
}

// prepareContainerWorkspace stages an immutable engine-selected tree. It never
// enumerates the caller's filesystem or invokes checkout/smudge filters. Missing
// source identity (including an unconfigured persistence-off source) fails closed.
// On uncertain helper termination the returned workspace must be retained.
func prepareContainerWorkspace(ctx context.Context, req engine.StepRequest, parent string) (workspace *containerWorkspace, resultErr error) {
	if req.Step == nil || req.ExecutionDir == "" || !filepath.IsAbs(req.ExecutionDir) || !containerObjectID(req.SourceCommit) || len(req.Step.WorkspaceRequirements) != 0 {
		return nil, fmt.Errorf("container source capability unavailable")
	}
	if req.Step.Type != workflow.StepCommand || req.Step.Isolation == workflow.IsolationWorktree || req.Worktree != "" {
		return nil, fmt.Errorf("container staging requires a non-integrating command")
	}
	if parent == "" {
		parent = os.TempDir()
	}
	if !filepath.IsAbs(parent) {
		return nil, fmt.Errorf("container staging parent must be absolute")
	}
	ctx, cancel := context.WithTimeout(ctx, containerSourceSetup)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("container staging requires Git")
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(parent, "jig-container-")
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	w := &containerWorkspace{Root: root, Workspace: filepath.Join(root, "workspace"), Scratch: filepath.Join(root, "scratch"), Captures: filepath.Join(root, "captures"), Metadata: filepath.Join(root, "metadata"), Inputs: filepath.Join(root, "inputs"), identity: info}
	defer func() {
		if resultErr == nil {
			return
		}
		var uncertain *engine.ExecutionUncertainError
		if errors.As(resultErr, &uncertain) {
			workspace = w
			return
		}
		if err := w.Remove(); err != nil {
			resultErr = errors.Join(resultErr, err)
			workspace = w
		}
	}()
	for _, dir := range []string{w.Workspace, w.Scratch, w.Captures, w.Metadata, w.Inputs, filepath.Join(root, "git-home"), filepath.Join(root, "git-template")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
	}
	if err := w.stageInputs(ctx, req); err != nil {
		return nil, err
	}
	runGit := func(dir string, out io.Writer, args ...string) error {
		return containerGit(ctx, git, dir, filepath.Join(root, "git-home"), out, args...)
	}
	// Require the exact selected commit, not a tree-ish revision expression.
	var kind bytes.Buffer
	if err := runGit(req.ExecutionDir, &stagingWriter{writer: &kind, remaining: 16}, "cat-file", "-t", req.SourceCommit); err != nil {
		return nil, err
	}
	if kind.String() != "commit\n" {
		return nil, fmt.Errorf("container source must name a commit")
	}
	var tree bytes.Buffer
	if err := runGit(req.ExecutionDir, &stagingWriter{writer: &tree, remaining: containerTreeBytes}, "ls-tree", "-rlz", "--full-tree", req.SourceCommit); err != nil {
		return nil, err
	}
	entries, manifest, err := parseContainerTree(tree.Bytes(), req.SourceCommit)
	if err != nil {
		return nil, err
	}
	rootFS, err := os.OpenRoot(w.Workspace)
	if err != nil {
		return nil, err
	}
	defer rootFS.Close()
	links := map[string]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := rootFS.MkdirAll(filepath.Dir(entry.name), 0755); err != nil {
			return nil, err
		}
		if entry.mode == "120000" {
			var target bytes.Buffer
			digest := containerBlobHash(entry)
			if err := runGit(req.ExecutionDir, &stagingWriter{writer: io.MultiWriter(&target, digest), remaining: entry.size}, "cat-file", "blob", entry.oid); err != nil {
				return nil, err
			}
			if fmt.Sprintf("%x", digest.Sum(nil)) != entry.oid {
				return nil, fmt.Errorf("container source blob integrity failure")
			}
			if int64(target.Len()) != entry.size || target.Len() > 4096 || target.Len() == 0 || path.IsAbs(target.String()) || strings.ContainsAny(target.String(), "\\\x00") {
				return nil, fmt.Errorf("unsupported container source symlink")
			}
			links[entry.name] = target.String()
			continue
		}
		f, err := rootFS.OpenFile(entry.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		digest := containerBlobHash(entry)
		bounded := &stagingWriter{writer: io.MultiWriter(f, digest), remaining: entry.size}
		err = runGit(req.ExecutionDir, bounded, "cat-file", "blob", entry.oid)
		if err == nil && bounded.remaining != 0 {
			err = fmt.Errorf("container source blob size changed")
		}
		if err == nil && fmt.Sprintf("%x", digest.Sum(nil)) != entry.oid {
			err = fmt.Errorf("container source blob integrity failure")
		}
		mode := os.FileMode(0644)
		if entry.mode == "100755" {
			mode = 0755
		}
		if err == nil {
			err = f.Chmod(mode)
		}
		closeErr := f.Close()
		if err = errors.Join(err, closeErr); err != nil {
			return nil, err
		}
		// LFS payload delivery is not supported; never silently stage pointer bytes.
		prefix, err := rootFS.Open(entry.name)
		if err != nil {
			return nil, err
		}
		header := make([]byte, 128)
		n, readErr := prefix.Read(header)
		closeErr = prefix.Close()
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if bytes.HasPrefix(header[:n], []byte("version https://git-lfs.github.com/spec/v1")) {
			return nil, fmt.Errorf("container source requires unsupported LFS content")
		}
	}
	// Install links only after regular files. Root.Stat checks chains and escape
	// attempts before synthetic .git exists, so links cannot expose its internals.
	for name, target := range links {
		if err := rootFS.Symlink(target, name); err != nil {
			return nil, err
		}
	}
	for name := range links {
		if _, err := rootFS.Stat(name); err != nil {
			return nil, fmt.Errorf("container source symlink is dangling, cyclic or outside staging")
		}
	}
	for _, asset := range []struct {
		needed bool
		kind   workflow.AssetKind
		name   string
	}{
		{req.Step.Script != "", workflow.AssetScript, "command.sh"},
		{req.Step.Validate != nil && req.Step.Validate.OutputSchema != "", workflow.AssetValidationSchema, "validation-schema.json"},
	} {
		if !asset.needed {
			continue
		}
		if err := req.Assets.Verify(req.AssetOwner); err != nil {
			return nil, err
		}
		data, err := req.Assets.Bytes(req.AssetOwner, asset.kind)
		if err != nil {
			return nil, err
		}
		if len(data) > 1<<20 {
			return nil, fmt.Errorf("container capture limit exceeded")
		}
		if err := os.WriteFile(filepath.Join(w.Captures, asset.name), data, 0400); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(w.Metadata, "source.json"), raw, 0400); err != nil {
		return nil, err
	}
	if err := runGit(w.Workspace, io.Discard, "init", "--quiet", "--template="+filepath.Join(root, "git-template")); err != nil {
		return nil, err
	}
	// The baseline must preserve staged bytes even when source attributes ask
	// Git to clean, re-encode or normalize files. This private override takes
	// precedence over every source .gitattributes file.
	if err := os.MkdirAll(filepath.Join(w.Workspace, ".git", "info"), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(w.Workspace, ".git", "info", "attributes"), []byte("* -filter -text -ident -working-tree-encoding\n"), 0600); err != nil {
		return nil, err
	}
	if err := runGit(w.Workspace, io.Discard, "add", "--all", "--force"); err != nil {
		return nil, err
	}
	if err := runGit(w.Workspace, io.Discard, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "Contained source baseline"); err != nil {
		return nil, err
	}
	return w, nil
}

func containerObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func containerBlobHash(entry containerSourceEntry) hash.Hash {
	var h hash.Hash = sha1.New()
	if len(entry.oid) == 64 {
		h = sha256.New()
	}
	fmt.Fprintf(h, "blob %d\x00", entry.size)
	return h
}
func deniedContainerSource(name string) bool {
	for _, part := range strings.Split(name, "/") {
		lower := cases.Fold().String(norm.NFC.String(part))
		if strings.HasPrefix(lower, ".env") {
			return true
		}
		switch lower {
		case ".git", ".jig", ".aws", ".ssh", ".docker", ".azure", ".gnupg", ".config", ".cache", ".kube", ".netrc", ".npmrc", ".pypirc", "auth.json", "credentials.json", ".gitconfig", ".git-credentials":
			return true
		}
	}
	return false
}
func parseContainerTree(raw []byte, commit string) ([]containerSourceEntry, containerSourceManifest, error) {
	manifest := containerSourceManifest{Commit: commit, Excluded: []string{}}
	var entries []containerSourceEntry
	names := map[string]string{}
	files := map[string]bool{}
	if len(raw) > containerTreeBytes {
		return nil, manifest, fmt.Errorf("container tree byte limit exceeded")
	}
	records := bytes.SplitN(raw, []byte{0}, containerSourceFiles+2)
	if len(records) > containerSourceFiles+1 || len(records[len(records)-1]) != 0 {
		return nil, manifest, fmt.Errorf("container source entry limit or malformed tree")
	}
	for _, record := range records[:len(records)-1] {
		header, name, ok := strings.Cut(string(record), "\t")
		if !ok || len(name) > 4096 || !filepath.IsLocal(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\r\n") {
			return nil, manifest, fmt.Errorf("unsafe container source path")
		}
		parts := strings.Split(name, "/")
		if !utf8.ValidString(name) || len(parts) > 64 {
			return nil, manifest, fmt.Errorf("container source path limit exceeded")
		}
		for _, part := range parts {
			if len(part) > 255 {
				return nil, manifest, fmt.Errorf("container source path limit exceeded")
			}
			for _, r := range part {
				if unicode.IsControl(r) {
					return nil, manifest, fmt.Errorf("unsafe container source path")
				}
			}
		}
		fields := strings.Fields(header)
		if len(fields) != 4 || !containerObjectID(fields[2]) {
			return nil, manifest, fmt.Errorf("invalid container source entry")
		}
		if deniedContainerSource(name) {
			manifest.Excluded = append(manifest.Excluded, name)
			continue
		}
		if files[name] {
			return nil, manifest, fmt.Errorf("duplicate container source path")
		}
		files[name] = true
		if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755" && fields[0] != "120000") {
			return nil, manifest, fmt.Errorf("unsupported container source entry (submodule or mode)")
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 || size > containerSourceFileBytes || manifest.Bytes+size > containerSourceBytes {
			return nil, manifest, fmt.Errorf("container source byte limit exceeded")
		}
		if fields[0] == "120000" && size > 4096 {
			return nil, manifest, fmt.Errorf("container symlink limit exceeded")
		}
		// Detect portable case collisions, including differently cased directories.
		for prefix := name; prefix != "."; prefix = path.Dir(prefix) {
			key := cases.Fold().String(norm.NFC.String(prefix))
			if prior, ok := names[key]; ok && prior != prefix {
				return nil, manifest, fmt.Errorf("container source path collision")
			}
			names[key] = prefix
			if len(names) > containerSourcePaths {
				return nil, manifest, fmt.Errorf("container source traversal limit exceeded")
			}
		}
		entries = append(entries, containerSourceEntry{name: name, oid: fields[2], mode: fields[0], size: size})
		manifest.Files++
		manifest.Bytes += size
	}
	return entries, manifest, nil
}

type stagingWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *stagingWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("container staging output limit exceeded")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func containerGit(ctx context.Context, git, dir, home string, out io.Writer, args ...string) error {
	base := []string{"--no-pager", "--no-replace-objects", "-c", "protocol.allow=never", "-c", "core.hooksPath=/dev/null", "-c", "core.attributesFile=/dev/null", "-c", "core.autocrlf=false"}
	cmd := exec.CommandContext(ctx, git, append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0"}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	configureProcessGroup(cmd)
	err := cmd.Run()
	if cmd.Process != nil {
		if stopErr := finishProcessGroup(cmd); stopErr != nil {
			return &engine.ExecutionUncertainError{}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("container source Git operation failed: %w", err)
	}
	return nil
}
