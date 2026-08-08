package engine

import (
	"os"
	"testing"

	"jig/internal/datastore"
	"jig/internal/step"
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

func TestReplayJournal_SkipsUndecodableLines(t *testing.T) {
	runDir := t.TempDir()
	good, _ := MarshalEnvelope(1, RunStarted{RunID: "r1", Workflow: "wf", Steps: []string{"a"}})
	last, _ := MarshalEnvelope(4, RunFinished{RunID: "r1", Failed: true})

	// A garbage line and an unknown-kind line between two good events must be
	// skipped, not abort the replay. The final line intentionally has no trailing
	// newline — the real-world tail after a crash mid-write. Unknown-kind lines
	// (forward-compat) return (env, nil, nil) and are skipped by the nil-event
	// guard in ReplayJournal.
	var buf []byte
	buf = append(buf, good...)
	buf = append(buf, '\n')
	buf = append(buf, "{ not json\n"...)
	buf = append(buf, `{"seq":3,"ts":"2026-01-01T00:00:00Z","kind":"not_a_kind","data":{}}`...)
	buf = append(buf, '\n')
	buf = append(buf, last...)
	if err := os.WriteFile(datastore.JournalPath(runDir), buf, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReplayJournal(runDir)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 decodable events, got %d", len(got))
	}
	if _, ok := got[0].(RunStarted); !ok {
		t.Errorf("event 0: want RunStarted, got %T", got[0])
	}
	if _, ok := got[1].(RunFinished); !ok {
		t.Errorf("event 1 (no trailing newline): want RunFinished, got %T", got[1])
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
