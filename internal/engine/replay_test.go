package engine

import (
	"os"
	"path/filepath"
	"testing"

	"jig/internal/datastore"
	"jig/internal/review"
	"jig/internal/step"
	"jig/internal/workflow"
)

// writeJournal marshals evs (seq starting at 1) into runDir's journal.jsonl,
// one line each, mirroring what manifest.Writer produces at run time.
func writeJournal(t *testing.T, runDir string, evs []Event) {
	t.Helper()
	f, err := os.Create(datastore.JournalPath(runDir))
	if err != nil {
		t.Fatalf("create journal: %v", err)
	}
	defer f.Close()
	for i, ev := range evs {
		line, err := MarshalEnvelope(i+1, ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

// eventData round-trips an event through the envelope encoding and returns its
// kind and data JSON, so two events can be compared structurally regardless of
// the transient timestamp MarshalEnvelope stamps.
func eventData(t *testing.T, seq int, e Event) (string, string) {
	t.Helper()
	line, err := MarshalEnvelope(seq, e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	env, _, err := UnmarshalEnvelope(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return env.Kind, string(env.Data)
}

func TestReplayJournal_RoundTrip(t *testing.T) {
	runDir := t.TempDir()
	want := []Event{
		RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"a", "b"}},
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusRunning, To: step.StatusSucceeded},
		RunFinished{RunID: "r1", Failed: false},
	}
	writeJournal(t, runDir, want)

	got, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("event count: want %d, got %d", len(want), len(got))
	}
	for i := range want {
		wKind, wData := eventData(t, i+1, want[i])
		gKind, gData := eventData(t, i+1, got[i])
		if wKind != gKind || wData != gData {
			t.Errorf("event %d mismatch:\n want %s %s\n  got %s %s", i, wKind, wData, gKind, gData)
		}
	}
}

func TestReplayJournal_MissingJournal(t *testing.T) {
	// A run dir with no journal.jsonl (persistence-off run, or one that never got
	// past creation) is not an error — it simply has no events to fold.
	got, err := ReplayJournal(t.TempDir())
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if got != nil {
		t.Errorf("want nil events for missing journal, got %v", got)
	}
}

func TestReplayJournal_RecoversRunningStepWithoutTerminalEvent(t *testing.T) {
	// Orphan path: no workflow.json → virtual fail for display (Spec 20 D7).
	runDir := t.TempDir()
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"synthesize"}},
		StepStatus{RunID: "r1", StepID: "synthesize", From: step.StatusPending, To: step.StatusRunning},
	})

	got, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("event count: want 4 (including recovered terminal events), got %d", len(got))
	}
	failed, ok := got[2].(StepStatus)
	if !ok {
		t.Fatalf("event[2]: want StepStatus, got %T", got[2])
	}
	if failed.From != step.StatusRunning || failed.To != step.StatusFailed {
		t.Errorf("recovered status = %q -> %q, want running -> failed", failed.From, failed.To)
	}
	if failed.Err != "agent SDK session terminated abruptly before reporting a terminal result" {
		t.Errorf("recovered error = %q", failed.Err)
	}
	finished, ok := got[3].(RunFinished)
	if !ok || !finished.Failed {
		t.Errorf("event[3] = %#v, want failed RunFinished", got[3])
	}
}

func TestReplayJournal_ReopenableInterruptedStaysUnfinished(t *testing.T) {
	wf, err := workflow.Decode(`
[workflow]
name = "feature"
version = "1"
[[step]]
id = "synthesize"
type = "command"
run = "true"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"synthesize"}},
		StepStatus{RunID: "r1", StepID: "synthesize", From: step.StatusPending, To: step.StatusRunning},
	})

	got, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("event count = %d, want 2 durable events (no virtual finish)", len(got))
	}
	for _, event := range got {
		if _, ok := event.(RunFinished); ok {
			t.Fatal("reopenable interrupted run must not invent RunFinished")
		}
	}
}

func TestReplayJournal_CorruptSnapshotIsOrphaned(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(datastore.WorkflowSnapshotPath(runDir), []byte(`{"broken":`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"agent"}},
		StepStatus{RunID: "r1", StepID: "agent", From: step.StatusRunning, To: step.StatusValidating},
	})

	events, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("event count = %d, want orphan reconciliation", len(events))
	}
	failed, ok := events[2].(StepStatus)
	if !ok || failed.From != step.StatusValidating || failed.To != step.StatusFailed {
		t.Fatalf("virtual validating failure = %#v", events[2])
	}
}

func TestReplayJournal_OrphanedNonWorkerParksAreTerminalForDisplay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status step.Status
	}{
		{name: "pending", status: step.StatusPending},
		{name: "review", status: step.StatusAwaitingReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runDir := t.TempDir()
			events := []Event{RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"gate"}}}
			if tc.status != step.StatusPending {
				events = append(events, StepStatus{RunID: "r1", StepID: "gate", From: step.StatusPending, To: tc.status})
			}
			writeJournal(t, runDir, events)

			got, err := ReplayJournal(runDir)
			if err != nil {
				t.Fatal(err)
			}
			failed, ok := got[len(got)-2].(StepStatus)
			if !ok || failed.From != tc.status || failed.To != step.StatusFailed || failed.Err != orphanedRunErr {
				t.Fatalf("orphan failure = %#v", got[len(got)-2])
			}
			if finished, ok := got[len(got)-1].(RunFinished); !ok || !finished.Failed {
				t.Fatalf("orphan finish = %#v", got[len(got)-1])
			}
		})
	}
}

func TestReplayJournal_MissingRunStartedIsOrphaned(t *testing.T) {
	runDir := t.TempDir()
	writeJournal(t, runDir, []Event{
		StepStatus{RunID: "r1", StepID: "agent", From: step.StatusPending, To: step.StatusRunning},
	})

	events, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want virtual failure and finish", len(events))
	}
	if finished, ok := events[2].(RunFinished); !ok || !finished.Failed || finished.RunID != "r1" {
		t.Fatalf("virtual finish = %#v", events[2])
	}
}

func TestReplayJournalRawLeavesInterruptedRunUnfinished(t *testing.T) {
	runDir := t.TempDir()
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"agent"}},
		StepStatus{RunID: "r1", StepID: "agent", From: step.StatusPending, To: step.StatusRunning},
	})

	got, err := ReplayJournalRaw(runDir)
	if err != nil {
		t.Fatalf("ReplayJournalRaw: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("raw event count = %d, want 2 durable events", len(got))
	}
	for _, event := range got {
		if _, ok := event.(RunFinished); ok {
			t.Fatal("raw replay invented a terminal event")
		}
	}
}

func TestReplayJournalHydratesReviewDocumentSnapshot(t *testing.T) {
	runDir := t.TempDir()
	docDir := datastore.ReviewDocumentsDir(runDir, "gate", "g000-i000")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "Persisted review content"
	path := filepath.Join(docDir, "01-scope.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "r1", Workflow: "wf", Steps: []string{"gate"}},
		StepStatus{RunID: "r1", StepID: "gate", From: step.StatusPending, To: step.StatusAwaitingReview},
		ReviewRequest{RunID: "r1", StepID: "gate", RoundID: "g000-i000", Documents: []review.Document{{
			ID: "01-scope", Format: "markdown", SnapshotPath: path, SHA256: review.Digest(content), LineCount: 1,
		}}},
	})

	events, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatal(err)
	}
	req, ok := events[2].(ReviewRequest)
	if !ok || len(req.Documents) != 1 || req.Documents[0].Content != content {
		t.Fatalf("hydrated review request = %#v", events[2])
	}
}

func TestReplayJournal_ToleratesUnknownKindsAndTornTail(t *testing.T) {
	runDir := t.TempDir()
	wf, err := workflow.Decode(`
[workflow]
name = "wf"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	good, _ := MarshalEnvelope(1, RunStarted{RunID: "r1", Workflow: "wf", Steps: []string{"a"}})

	// Unknown kinds remain forward-compatible. The final invalid bytes have no
	// trailing newline and model a process dying midway through one append.
	var buf []byte
	buf = append(buf, good...)
	buf = append(buf, '\n')
	buf = append(buf, `{"seq":2,"ts":"2026-01-01T00:00:00Z","kind":"not_a_kind","data":{}}`...)
	buf = append(buf, '\n')
	buf = append(buf, `{"seq":3,"ts":"2026-01-01`...)
	if err := os.WriteFile(datastore.JournalPath(runDir), buf, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 decodable event, got %d", len(got))
	}
	if _, ok := got[0].(RunStarted); !ok {
		t.Errorf("event 0: want RunStarted, got %T", got[0])
	}
}

func TestReplayJournal_CompleteInteriorCorruptionOrphansDisplayAndRejectsRaw(t *testing.T) {
	runDir := t.TempDir()
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "r1", Workflow: "wf", Steps: []string{"a"}},
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusPending, To: step.StatusRunning},
	})
	f, err := os.OpenFile(datastore.JournalPath(runDir), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{ not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := ReplayJournalRaw(runDir); err == nil {
		t.Fatal("ReplayJournalRaw accepted newline-complete corruption")
	}
	got, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatalf("ReplayJournal display reconciliation: %v", err)
	}
	if _, ok := got[len(got)-1].(RunFinished); !ok {
		t.Fatalf("last display event = %T, want virtual RunFinished", got[len(got)-1])
	}
}

// TestReplayPostReset verifies that a journal containing a steps_reset event
// followed by fresh step_status transitions replays all events correctly —
// unknown kinds are skipped (not dropped due to an error) and known kinds are
// preserved in order.
func TestReplayPostReset(t *testing.T) {
	runDir := t.TempDir()

	evs := []Event{
		RunStarted{RunID: "r1", Workflow: "wf", Steps: []string{"a", "b", "c"}},
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusRunning, To: step.StatusSucceeded},
		StepStatus{RunID: "r1", StepID: "b", From: step.StatusPending, To: step.StatusSucceeded},
		// Operator resets to "a" — audit event written before the StepStatus resets.
		StepsReset{RunID: "r1", Target: "a", Closure: []string{"a", "b"}, RewindTo: "deadbeef"},
		// Post-reset StepStatus transitions (pending, then re-running).
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusSucceeded, To: step.StatusPending, Generation: 1},
		StepStatus{RunID: "r1", StepID: "b", From: step.StatusSucceeded, To: step.StatusPending, Generation: 1},
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusPending, To: step.StatusRunning, Generation: 1},
		StepStatus{RunID: "r1", StepID: "a", From: step.StatusRunning, To: step.StatusSucceeded, Generation: 1},
		RunFinished{RunID: "r1", Failed: false},
	}
	writeJournal(t, runDir, evs)

	got, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if len(got) != len(evs) {
		t.Fatalf("event count: want %d, got %d", len(evs), len(got))
	}
	// Spot-check the StepsReset at index 4.
	sr, ok := got[4].(StepsReset)
	if !ok {
		t.Fatalf("event[4]: want StepsReset, got %T", got[4])
	}
	if sr.Target != "a" {
		t.Errorf("StepsReset.Target = %q; want %q", sr.Target, "a")
	}
	if sr.RewindTo != "deadbeef" {
		t.Errorf("StepsReset.RewindTo = %q; want %q", sr.RewindTo, "deadbeef")
	}
	// Spot-check that the post-reset StepStatus carries Generation.
	ss, ok := got[5].(StepStatus)
	if !ok {
		t.Fatalf("event[5]: want StepStatus, got %T", got[5])
	}
	if ss.Generation != 1 {
		t.Errorf("post-reset StepStatus.Generation = %d; want 1", ss.Generation)
	}
}
