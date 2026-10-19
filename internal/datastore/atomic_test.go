package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFileAtomic_ReplacesContentsWithPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("contents = %q, %v; want \"new\"", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
	assertOnlyEntries(t, dir, "state.json")
}

func TestWriteFileAtomic_FailureLeavesOriginalAndNoTemp(t *testing.T) {
	dir := t.TempDir()
	// Renaming a file over a non-empty directory fails, after the temp file
	// has been written — the failure path that must clean up after itself.
	path := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("x"), 0o600); err == nil {
		t.Fatal("expected rename over a non-empty directory to fail")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("original target disturbed: %v", err)
	}
	assertOnlyEntries(t, dir, "target")
}

func TestWriteFileAtomic_MissingDirErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "state.json")
	if err := WriteFileAtomic(path, []byte("x"), 0o600); err == nil {
		t.Fatal("expected error when the parent directory is missing")
	}
}

// TestWriteFileAtomic_ConcurrentWritersNeverTear guards the unique temp name:
// with a shared temp path, concurrent writers could rename one another's
// half-written file into place.
func TestWriteFileAtomic_ConcurrentWritersNeverTear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	payloads := make(map[string]bool)
	var wg sync.WaitGroup
	for i := range 16 {
		payload := fmt.Sprintf("writer-%02d-%s", i, string(make([]byte, 4096)))
		payloads[payload] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WriteFileAtomic(path, []byte(payload), 0o600); err != nil {
				t.Errorf("WriteFileAtomic: %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !payloads[string(got)] {
		t.Fatalf("final contents (%d bytes) match no single writer", len(got))
	}
	assertOnlyEntries(t, dir, "state.json")
}

// assertOnlyEntries fails if dir holds anything besides names — in
// particular, a leaked temp file.
func assertOnlyEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	for _, e := range ents {
		if !want[e.Name()] {
			t.Errorf("unexpected entry %q left in %s", e.Name(), dir)
		}
	}
}
