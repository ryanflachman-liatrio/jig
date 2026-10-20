package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func d05Input(t *testing.T) engine.ResolvedInput {
	t.Helper()
	data := []byte{0, 255, 128, 0, 1}
	p := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(p, data, 0700); err != nil {
		t.Fatal(err)
	}
	return engine.ResolvedInput{Ref: workflow.Input{Ref: "build", As: "binary"}, Value: p, SnapshotPath: p, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), File: step.FileArtifact{SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Size: int64(len(data)), Mode: 0555}}
}
func TestD05ContainerInputRoundTrip(t *testing.T) {
	_, req := d05Source(t)
	in := d05Input(t)
	if err := os.Chmod(in.Value, 0600); err != nil {
		t.Fatal(err)
	} // Delivery mode comes from accepted metadata.
	req.ArtifactDir = filepath.Dir(in.Value)
	req.Inputs = []engine.ResolvedInput{in}
	req.Step.Inputs = []workflow.Input{in.Ref}
	w, err := prepareContainerWorkspace(context.Background(), req, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Remove()
	if w.InputEnv["JIG_INPUT_BINARY"] != "/inputs/001" {
		t.Fatal(w.InputEnv)
	}
	if err := os.Remove(in.SnapshotPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.Inputs, "001"))
	if err != nil || !bytes.Equal(data, []byte{0, 255, 128, 0, 1}) {
		t.Fatalf("binary copy: %x %v", data, err)
	}
	info, err := os.Stat(filepath.Join(w.Inputs, "001"))
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("mode: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(w.Workspace, "inputs")); !os.IsNotExist(err) {
		t.Fatal("input exposed in writable workspace")
	}
}
func TestD05ContainerInputRejects(t *testing.T) {
	for _, name := range []string{"missing", "symlink", "directory", "digest", "no_digest", "no_snapshot", "inline", "undeclared", "alias_collision", "oversized", "corrupt_bytes", "size_changed", "parent_escape", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			in := d05Input(t)
			req := engine.StepRequest{ArtifactDir: filepath.Dir(in.Value), Step: &workflow.Step{Inputs: []workflow.Input{in.Ref}}, Inputs: []engine.ResolvedInput{in}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "missing":
				os.Remove(in.SnapshotPath)
			case "symlink":
				os.Rename(in.SnapshotPath, in.SnapshotPath+"-real")
				os.Symlink(in.SnapshotPath+"-real", in.SnapshotPath)
			case "directory":
				os.Remove(in.SnapshotPath)
				os.Mkdir(in.SnapshotPath, 0700)
			case "digest":
				req.Inputs[0].SHA256 = fmt.Sprintf("%064d", 0)
			case "no_digest":
				req.Inputs[0].SHA256 = ""
			case "no_snapshot":
				req.Inputs[0].SnapshotPath = ""
			case "inline":
				req.Inputs[0].Ref.Inline = true
			case "undeclared":
				req.Step.Inputs = nil
			case "alias_collision":
				req.Inputs = append(req.Inputs, in)
				req.Step.Inputs = append(req.Step.Inputs, in.Ref)
			case "oversized":
				if err := os.Truncate(in.SnapshotPath, step.ContainerInputFileBytes+1); err != nil {
					t.Fatal(err)
				}
			case "corrupt_bytes":
				if err := os.WriteFile(in.Value, []byte{1, 2, 3, 4, 5}, 0600); err != nil {
					t.Fatal(err)
				}
			case "size_changed":
				req.Inputs[0].File.Size++
			case "parent_escape":
				root := t.TempDir()
				if err := os.Symlink(filepath.Dir(in.Value), filepath.Join(root, "escape")); err != nil {
					t.Fatal(err)
				}
				req.ArtifactDir = root
				req.Inputs[0].Value = filepath.Join(root, "escape", filepath.Base(in.Value))
				req.Inputs[0].SnapshotPath = req.Inputs[0].Value
			case "cancelled":
				cancel()
			}
			w := &containerWorkspace{Inputs: t.TempDir()}
			if err := w.stageInputs(ctx, req); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}
func TestD05ContainerInputAggregateBound(t *testing.T) {
	in := d05Input(t)
	if _, err := copyContainerInput(context.Background(), filepath.Dir(in.Value), in, filepath.Join(t.TempDir(), "copy"), 4); err == nil {
		t.Fatal("aggregate bound ignored")
	}
}
func TestD05ContainerInputFailureCleansStaging(t *testing.T) {
	_, req := d05Source(t)
	in := d05Input(t)
	in.SHA256 = ""
	req.ArtifactDir = filepath.Dir(in.Value)
	req.Inputs = []engine.ResolvedInput{in}
	req.Step.Inputs = []workflow.Input{in.Ref}
	parent := t.TempDir()
	if _, err := prepareContainerWorkspace(context.Background(), req, parent); err == nil {
		t.Fatal("accepted missing digest")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging leaked: %v %v", entries, err)
	}
}
