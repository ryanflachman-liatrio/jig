package datastore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRunDir_CreatesLayout(t *testing.T) {
	root := t.TempDir()
	runID := "20260730-120000-abcdef01"

	got, err := RunDir(root, runID)
	if err != nil {
		t.Fatalf("RunDir: %v", err)
	}

	// Returned path must be <root>/runs/<runID>.
	want := filepath.Join(root, "runs", runID)
	if got != want {
		t.Errorf("RunDir path: want %q, got %q", want, got)
	}

	// steps/ and artifacts/ subdirectories must exist.
	for _, sub := range []string{"steps", "artifacts"} {
		p := filepath.Join(got, sub)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected subdirectory %q to exist: %v", p, err)
		}
	}
}

func TestRunDir_EmptyRootReturnsError(t *testing.T) {
	_, err := RunDir("", "any-id")
	if err == nil {
		t.Error("expected error for empty root, got nil")
	}
}

func TestRunDir_Idempotent(t *testing.T) {
	root := t.TempDir()
	runID := "idempotent-run"
	// Calling twice should not error.
	if _, err := RunDir(root, runID); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := RunDir(root, runID); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestListRunIDs(t *testing.T) {
	root := t.TempDir()
	// Create run dirs out of order; ListRunIDs must return them sorted ascending
	// (chronological, since run IDs are timestamp-prefixed).
	for _, id := range []string{"20260731-121054-c", "20260731-114852-a", "20260731-121049-b"} {
		if _, err := RunDir(root, id); err != nil {
			t.Fatalf("RunDir %q: %v", id, err)
		}
	}
	// A stray file in runs/ must be ignored — only directories are runs.
	if err := os.WriteFile(filepath.Join(root, "runs", "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListRunIDs(root)
	if err != nil {
		t.Fatalf("ListRunIDs: %v", err)
	}
	want := []string{"20260731-114852-a", "20260731-121049-b", "20260731-121054-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListRunIDs: want %v, got %v", want, got)
	}
}

func TestListRunIDs_MissingAndEmptyRoot(t *testing.T) {
	// No runs/ directory yet (fresh repo): nil, no error.
	got, err := ListRunIDs(t.TempDir())
	if err != nil {
		t.Fatalf("ListRunIDs missing runs dir: %v", err)
	}
	if got != nil {
		t.Errorf("missing runs dir: want nil, got %v", got)
	}
	// Persistence off (empty root): nil, no error — same as "no runs".
	got, err = ListRunIDs("")
	if err != nil {
		t.Fatalf("ListRunIDs empty root: %v", err)
	}
	if got != nil {
		t.Errorf("empty root: want nil, got %v", got)
	}
}

func TestStepDir(t *testing.T) {
	root := t.TempDir()
	runDir, err := RunDir(root, "run1")
	if err != nil {
		t.Fatal(err)
	}

	stepDir, err := StepDir(runDir, "my-step")
	if err != nil {
		t.Fatalf("StepDir: %v", err)
	}

	want := filepath.Join(runDir, "steps", "my-step")
	if stepDir != want {
		t.Errorf("StepDir path: want %q, got %q", want, stepDir)
	}
	if _, err := os.Stat(stepDir); err != nil {
		t.Errorf("step dir does not exist: %v", err)
	}
}

func TestJournalPath(t *testing.T) {
	p := JournalPath("/some/run/dir")
	want := filepath.Join("/some/run/dir", "journal.jsonl")
	if p != want {
		t.Errorf("JournalPath: want %q, got %q", want, p)
	}
}

func TestResultPath(t *testing.T) {
	p := ResultPath("/some/run/dir", "my-step")
	want := filepath.Join("/some/run/dir", "steps", "my-step", "result.json")
	if p != want {
		t.Errorf("ResultPath: want %q, got %q", want, p)
	}
}

func TestTranscriptPath(t *testing.T) {
	p := TranscriptPath("/some/run/dir", "my-step")
	want := filepath.Join("/some/run/dir", "steps", "my-step", "transcript.jsonl")
	if p != want {
		t.Errorf("TranscriptPath: want %q, got %q", want, p)
	}
}
