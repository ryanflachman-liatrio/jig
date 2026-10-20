package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func d05Output(t *testing.T) (*containerWorkspace, engine.StepRequest, []byte) {
	t.Helper()
	root := t.TempDir()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	w := &containerWorkspace{Root: root, Workspace: filepath.Join(root, "workspace"), identity: info}
	data := []byte{0, 255, 128, 1, 0, 13, 10}
	d05Write(t, w.Workspace, "out/binary", data, 0755)
	req := engine.StepRequest{Step: &workflow.Step{ID: "build", Type: workflow.StepCommand, Output: "out/binary"}, ArtifactDir: t.TempDir(), ExecutionNonce: strings.Repeat("a", 32)}
	return w, req, data
}
func TestD05ContainerOutputRoundTrip(t *testing.T) {
	w, req, _ := d05Output(t)
	// Model a validator's last edit: collection must read the final bytes.
	data := []byte{9, 0, 255, 8, 7}
	if err := os.WriteFile(filepath.Join(w.Workspace, req.Step.Output), data, 0755); err != nil {
		t.Fatal(err)
	}
	path, meta, err := publishContainerOutput(context.Background(), req, w)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) || meta.Size != int64(len(data)) || meta.Mode != 0555 {
		t.Fatal(meta)
	}
	if err := w.Remove(); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("host artifact: %x %v", stored, err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved step.FileArtifact
	if err := json.Unmarshal(raw, &saved); err != nil || saved != meta {
		t.Fatalf("metadata: %v %v", saved, err)
	}
	input := workflow.Input{Ref: "build", As: "binary"}
	consumer := &containerWorkspace{Inputs: t.TempDir()}
	if err := consumer.stageInputs(context.Background(), engine.StepRequest{Step: &workflow.Step{Inputs: []workflow.Input{input}}, ArtifactDir: req.ArtifactDir, Inputs: []engine.ResolvedInput{{Ref: input, Value: path, SnapshotPath: path, SHA256: meta.SHA256, File: meta}}}); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(consumer.Inputs, "001"))
	if err != nil || !bytes.Equal(copied, data) {
		t.Fatalf("consumer artifact: %x %v", copied, err)
	}
	if consumer.InputEnv["JIG_INPUT_BINARY"] != "/inputs/001" {
		t.Fatal(consumer.InputEnv)
	}
}
func TestD05ContainerOutputRejects(t *testing.T) {
	for _, name := range []string{"missing", "directory", "symlink", "parent_symlink", "hardlink", "oversize", "escape", "private", "no_root", "no_nonce", "cancelled", "changed_root"} {
		t.Run(name, func(t *testing.T) {
			w, req, _ := d05Output(t)
			p := filepath.Join(w.Workspace, req.Step.Output)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "missing":
				must(os.Remove(p))
			case "directory":
				must(os.Remove(p))
				must(os.Mkdir(p, 0700))
			case "symlink":
				must(os.Rename(p, p+"-real"))
				must(os.Symlink(p+"-real", p))
			case "parent_symlink":
				must(os.Rename(filepath.Dir(p), filepath.Dir(p)+"-real"))
				must(os.Symlink("out-real", filepath.Dir(p)))
			case "hardlink":
				must(os.Link(p, p+"-alias"))
			case "oversize":
				must(os.Truncate(p, step.ContainerInputFileBytes+1))
			case "escape":
				req.Step.Output = "../outside"
			case "private":
				req.Step.Output = ".git/config"
			case "no_root":
				req.ArtifactDir = ""
			case "no_nonce":
				req.ExecutionNonce = ""
			case "cancelled":
				cancel()
			case "changed_root":
				w.identity = nil
			}
			path, meta, err := publishContainerOutput(ctx, req, w)
			if err == nil || path != "" || meta != (step.FileArtifact{}) {
				t.Fatalf("accepted: %q %+v %v", path, meta, err)
			}
			if req.ArtifactDir != "" {
				entries, err := os.ReadDir(req.ArtifactDir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("partial publication: %v %v", entries, err)
				}
			}
		})
	}
}
func TestD05ContainerOutputPublicationFailures(t *testing.T) {
	for _, phase := range []string{"copy", "file_sync", "metadata", "rename", "directory_sync"} {
		t.Run(phase, func(t *testing.T) {
			w, req, _ := d05Output(t)
			path, meta, err := collectContainerOutput(context.Background(), req, w, func(at string) error {
				if at == phase {
					return errors.New("injected")
				}
				return nil
			})
			if err == nil || path != "" || meta != (step.FileArtifact{}) {
				t.Fatalf("published failed copy: %s %+v %v", path, meta, err)
			}
			entries, err := os.ReadDir(req.ArtifactDir)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "directory_sync" {
				if len(entries) != 1 || entries[0].Name() != "container-"+req.ExecutionNonce {
					t.Fatal(entries)
				}
			} else if len(entries) != 0 {
				t.Fatal(entries)
			}
		})
	}
}
func TestD05ContainerOutputImmutableAttempts(t *testing.T) {
	w, req, data := d05Output(t)
	first, _, err := publishContainerOutput(context.Background(), req, w)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Workspace, req.Step.Output), []byte("replacement"), 0644); err != nil {
		t.Fatal(err)
	}
	if path, _, err := publishContainerOutput(context.Background(), req, w); err == nil || path != "" {
		t.Fatal("replaced accepted artifact")
	}
	req.ExecutionNonce = strings.Repeat("b", 32)
	second, _, err := publishContainerOutput(context.Background(), req, w)
	if err != nil || first == second {
		t.Fatalf("fresh attempt: %s %v", second, err)
	}
	original, err := os.ReadFile(first)
	if err != nil || !bytes.Equal(original, data) {
		t.Fatal("original artifact changed")
	}
}
func TestD05ContainerOutputCancellationDuringCopy(t *testing.T) {
	w, req, _ := d05Output(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err := collectContainerOutput(ctx, req, w, func(at string) error {
		if at == "copy" {
			cancel()
		}
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(req.ArtifactDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial publication: %v %v", entries, err)
	}
}

func TestD05ContainerOutputNonExecutableAndEmpty(t *testing.T) {
	w, req, _ := d05Output(t)
	p := filepath.Join(w.Workspace, req.Step.Output)
	if err := os.WriteFile(p, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	path, meta, err := publishContainerOutput(context.Background(), req, w)
	if err != nil || meta.Mode != 0444 || meta.Size != 0 {
		t.Fatalf("%+v %v", meta, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0444 {
		t.Fatalf("%v %v", info, err)
	}
}
func TestD05ContainerOutputPathGrammar(t *testing.T) {
	for _, path := range []string{"out/x00", "out/jig", "out/tool0"} {
		if !validContainerOutputPath(path) {
			t.Fatalf("rejected %q", path)
		}
	}
	for _, path := range []string{"", "out/../file", "out\\file", "out\x00file", "/file", ".jig/private"} {
		if validContainerOutputPath(path) {
			t.Fatalf("accepted %q", path)
		}
	}
}
