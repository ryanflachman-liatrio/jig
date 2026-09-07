package datastore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionPath(t *testing.T) {
	if SessionPath("", "step") != "" {
		t.Fatal("empty runDir must yield empty path")
	}
	got := SessionPath("/run", "agent")
	want := filepath.Join("/run", "steps", "agent", "session.json")
	if got != want {
		t.Fatalf("SessionPath = %q, want %q", got, want)
	}
}

func TestWriteReadClearSession(t *testing.T) {
	runDir, err := RunDir(t.TempDir(), "run1")
	if err != nil {
		t.Fatal(err)
	}
	info := SessionInfo{
		SessionID:  "sess-1",
		Backend:    "claude",
		Transport:  "sdk",
		Attempt:    2,
		Iteration:  1,
		Generation: 0,
	}
	if err := WriteSession(runDir, "agent", info); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	got, err := ReadSession(runDir, "agent")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if got.SessionID != "sess-1" || got.Backend != "claude" || got.Transport != "sdk" {
		t.Fatalf("ReadSession = %+v", got)
	}
	if got.Attempt != 2 || got.Iteration != 1 || got.UpdatedAt == "" {
		t.Fatalf("ReadSession metadata = %+v", got)
	}
	if err := ClearSession(runDir, "agent"); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	got, err = ReadSession(runDir, "agent")
	if err != nil {
		t.Fatalf("ReadSession after clear: %v", err)
	}
	if got.SessionID != "" {
		t.Fatalf("after clear SessionID = %q", got.SessionID)
	}
}

func TestWriteSession_PersistenceOffAndEmptyID(t *testing.T) {
	if err := WriteSession("", "agent", SessionInfo{SessionID: "x"}); err != nil {
		t.Fatalf("persistence-off WriteSession: %v", err)
	}
	runDir, err := RunDir(t.TempDir(), "run1")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSession(runDir, "agent", SessionInfo{}); err == nil {
		t.Fatal("empty session_id must error")
	}
}

func TestReadSession_CorruptIgnored(t *testing.T) {
	runDir, err := RunDir(t.TempDir(), "run1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StepDir(runDir, "agent"); err != nil {
		t.Fatal(err)
	}
	path := SessionPath(runDir, "agent")
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSession(runDir, "agent")
	if err != nil {
		t.Fatalf("corrupt ReadSession: %v", err)
	}
	if got.SessionID != "" {
		t.Fatalf("corrupt file must yield empty SessionID, got %q", got.SessionID)
	}
}

func TestClearStepOutputsRemovesSession(t *testing.T) {
	runDir, err := RunDir(t.TempDir(), "run1")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSession(runDir, "agent", SessionInfo{SessionID: "sess"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearStepOutputs(runDir, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SessionPath(runDir, "agent")); !os.IsNotExist(err) {
		t.Fatalf("session.json still present: %v", err)
	}
}
