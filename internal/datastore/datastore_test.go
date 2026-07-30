package datastore

import (
	"os"
	"path/filepath"
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
