package datastore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionPath(t *testing.T) {
	if (Run{}).Step("step").Session() != "" {
		t.Fatal("empty runDir must yield empty path")
	}
	got := RunAt("/run").Step("agent").Session()
	want := filepath.Join("/run", "steps", "agent", "session.json")
	if got != want {
		t.Fatalf("SessionPath = %q, want %q", got, want)
	}
}

func TestWriteReadClearSession(t *testing.T) {
	runDir, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	info := SessionInfo{
		SessionID:  "sess-1",
		Backend:    "claude",
		Attempt:    2,
		Iteration:  1,
		Generation: 0,
	}
	if err := runDir.Step("agent").WriteSession(info); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	got, err := runDir.Step("agent").ReadSession()
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if got.SessionID != "sess-1" || got.Backend != "claude" {
		t.Fatalf("ReadSession = %+v", got)
	}
	if got.Attempt != 2 || got.Iteration != 1 || got.UpdatedAt == "" {
		t.Fatalf("ReadSession metadata = %+v", got)
	}
	if err := runDir.Step("agent").ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	got, err = runDir.Step("agent").ReadSession()
	if err != nil {
		t.Fatalf("ReadSession after clear: %v", err)
	}
	if got.SessionID != "" {
		t.Fatalf("after clear SessionID = %q", got.SessionID)
	}
}

func TestWriteSession_PersistenceOffAndEmptyID(t *testing.T) {
	if err := (Run{}).Step("agent").WriteSession(SessionInfo{SessionID: "x"}); err != nil {
		t.Fatalf("persistence-off WriteSession: %v", err)
	}
	runDir, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	if err := runDir.Step("agent").WriteSession(SessionInfo{}); err == nil {
		t.Fatal("empty session_id must error")
	}
}

func TestReadSession_CorruptReported(t *testing.T) {
	for name, contents := range map[string]string{
		"undecodable":      "{not-json",
		"empty session_id": `{"session_id":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			run, err := Open(t.TempDir()).Create("run1")
			if err != nil {
				t.Fatal(err)
			}
			st := run.Step("agent")
			if err := st.Ensure(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(st.Session(), []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := st.ReadSession()
			if !errors.Is(err, ErrSessionCorrupt) {
				t.Fatalf("ReadSession error = %v, want ErrSessionCorrupt", err)
			}
			if got != (SessionInfo{}) {
				t.Fatalf("corrupt file must yield a zero SessionInfo, got %+v", got)
			}
		})
	}
}

func TestClearStepOutputsRemovesSession(t *testing.T) {
	runDir, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	if err := runDir.Step("agent").WriteSession(SessionInfo{SessionID: "sess"}); err != nil {
		t.Fatal(err)
	}
	if err := runDir.Step("agent").ClearOutputs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runDir.Step("agent").Session()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("session.json still present: %v", err)
	}
}
