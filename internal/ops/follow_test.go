package ops

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/transcript"
)

func TestFollowLogsEmitsAppendOnceAndStopsAtTerminal(t *testing.T) {
	root := t.TempDir()
	runID := "20260908-120000-follow12"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	writeOpsJournal(t, runDir, engine.RunStarted{RunID: runID, Workflow: "follow", Steps: []string{"a"}})
	writeTranscript(t, runDir, "a", []transcript.Entry{{Seq: 1, Ts: "2026-09-08T12:00:00Z", Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "first"}}}})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	items := make(chan LogEntry, 4)
	done := make(chan error, 1)
	go func() {
		_, err := FollowLogs(ctx, root, runID, LogOptions{Tail: 10}, 10*time.Millisecond, func(item LogEntry) error {
			items <- item
			return nil
		})
		done <- err
	}()

	select {
	case item := <-items:
		if item.Entry.Seq != 1 {
			t.Fatalf("initial seq = %d", item.Entry.Seq)
		}
	case <-ctx.Done():
		t.Fatal("initial entry was not emitted")
	}
	appendTranscriptEntry(t, runDir, "a", transcript.Entry{Seq: 2, Ts: "2026-09-08T12:00:01Z", Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "second"}}})
	appendJournalEvent(t, runDir, 2, engine.RunFinished{RunID: runID})

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("follow did not stop after RunFinished")
	}
	close(items)
	var seqs []int
	for item := range items {
		seqs = append(seqs, item.Entry.Seq)
	}
	if len(seqs) != 1 || seqs[0] != 2 {
		t.Fatalf("appended seqs = %v", seqs)
	}
}

func appendTranscriptEntry(t *testing.T, runDir, stepID string, entry transcript.Entry) {
	t.Helper()
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(datastore.TranscriptPath(runDir, stepID), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func appendJournalEvent(t *testing.T, runDir string, seq int, event engine.Event) {
	t.Helper()
	line, err := engine.MarshalEnvelope(seq, event)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(datastore.JournalPath(runDir), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
