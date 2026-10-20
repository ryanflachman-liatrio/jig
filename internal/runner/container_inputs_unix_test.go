//go:build unix

package runner

import (
	"context"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestD05ContainerInputRejectsFIFO(t *testing.T) {
	in := d05Input(t)
	if err := os.Remove(in.SnapshotPath); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(in.SnapshotPath, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := copyContainerInput(context.Background(), filepath.Dir(in.Value), in, filepath.Join(t.TempDir(), "copy"), step.ContainerInputTotalBytes); err == nil {
		t.Fatal("accepted FIFO")
	}
}

func TestD05ContainerOutputRejectsFIFO(t *testing.T) {
	w, req, _ := d05Output(t)
	path := filepath.Join(w.Workspace, req.Step.Output)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := publishContainerOutput(context.Background(), req, w); err == nil {
		t.Fatal("accepted FIFO")
	}
}
