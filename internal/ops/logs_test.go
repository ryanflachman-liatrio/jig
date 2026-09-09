package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/transcript"
)

func TestReadLogsMergesAndBounds(t *testing.T) {
	root := t.TempDir()
	runID := "20260908-120000-logs1234"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	writeOpsJournal(t, runDir, engine.RunStarted{RunID: runID, Workflow: "logs", Steps: []string{"a", "b"}})
	writeTranscript(t, runDir, "a", []transcript.Entry{{Seq: 1, Ts: "2026-09-08T12:00:00Z", Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "a1"}}}, {Seq: 2, Ts: "2026-09-08T12:00:02Z", Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "secret thought"}}}})
	writeTranscript(t, runDir, "b", []transcript.Entry{{Seq: 1, Ts: "2026-09-08T12:00:01Z", Role: transcript.RoleSystem, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "b1"}}}})
	batch, err := ReadLogs(root, runID, LogOptions{Tail: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Entries) != 2 || batch.Omitted != 1 || batch.Entries[0].StepID != "b" || batch.Entries[1].StepID != "a" {
		t.Fatalf("batch = %#v", batch)
	}
	rendered := RenderLogText(batch.Entries[1], false)
	if !strings.Contains(rendered, "[thinking omitted]") || strings.Contains(rendered, "secret thought") {
		t.Fatalf("rendered = %q", rendered)
	}
	if _, err := ReadLogs(root, runID, LogOptions{StepID: "missing"}); err == nil {
		t.Error("unknown step succeeded")
	}
}

func writeOpsJournal(t *testing.T, runDir string, events ...engine.Event) {
	t.Helper()
	f, err := os.Create(datastore.JournalPath(runDir))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i, event := range events {
		line, err := engine.MarshalEnvelope(i+1, event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func writeTranscript(t *testing.T, runDir, stepID string, entries []transcript.Entry) {
	t.Helper()
	dir := filepath.Dir(datastore.TranscriptPath(runDir, stepID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(datastore.TranscriptPath(runDir, stepID))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}
