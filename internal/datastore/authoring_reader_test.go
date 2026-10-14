//go:build darwin || linux

package datastore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthoringReaderPinsRunDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "run")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.json"), []byte("ORIGINAL"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenAuthoringReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := os.Rename(dir, filepath.Join(parent, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.json"), []byte("SUBSTITUTE"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := reader.Read("workflow.json", 1024)
	if err != nil || string(got) != "ORIGINAL" {
		t.Fatalf("pinned=%q %v", got, err)
	}
}
