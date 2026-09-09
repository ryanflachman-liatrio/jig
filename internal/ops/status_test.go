package ops

import (
	"os"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
)

func TestInspectRunOrphanAndCorrupt(t *testing.T) {
	root := t.TempDir()
	orphanID := "20260908-120000-orphan12"
	if _, err := datastore.RunDir(root, orphanID); err != nil {
		t.Fatal(err)
	}
	report, err := InspectRun(root, orphanID)
	if err == nil || report.State != StateOrphaned {
		t.Fatalf("orphan = %#v, %v", report, err)
	}

	corruptID := "20260908-120001-corrupt1"
	runDir, err := datastore.RunDir(root, corruptID)
	if err != nil {
		t.Fatal(err)
	}
	line, err := engine.MarshalEnvelope(1, engine.RunStarted{RunID: corruptID, Workflow: "wf", Steps: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	data := append(append(line, '\n'), []byte("not-json\n")...)
	if err := os.WriteFile(datastore.JournalPath(runDir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = InspectRun(root, corruptID)
	if err == nil || report.State != StateCorrupt || report.Workflow != "wf" || len(report.Steps) != 1 {
		t.Fatalf("corrupt = %#v, %v", report, err)
	}
}

func TestFoldStatusInterruptedAndTotals(t *testing.T) {
	start := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	update := start.Add(time.Minute)
	cost := 0.42
	report := FoldStatus("r1", []engine.JournalRecord{
		{Seq: 1, Timestamp: start, Event: engine.RunStarted{RunID: "r1", Workflow: "feature", Steps: []string{"a", "b"}}},
		{Seq: 2, Timestamp: update, Event: engine.StepStatus{RunID: "r1", StepID: "a", To: step.StatusSucceeded, Cost: &cost, Tokens: 12}},
		{Seq: 3, Timestamp: update, Event: engine.StepStatus{RunID: "r1", StepID: "b", To: step.StatusRunning}},
	}, false)
	if report.State != StateInterrupted || !report.Reopenable {
		t.Fatalf("state=%s reopenable=%t", report.State, report.Reopenable)
	}
	if report.TotalCostUSD != cost || report.TotalTokens != 12 {
		t.Fatalf("totals = %f/%d", report.TotalCostUSD, report.TotalTokens)
	}
	if report.StartedAt == nil || !report.StartedAt.Equal(start) || report.UpdatedAt == nil || !report.UpdatedAt.Equal(update) {
		t.Fatalf("timestamps = %#v %#v", report.StartedAt, report.UpdatedAt)
	}
}

func TestFoldStatusActiveWaitAndFinished(t *testing.T) {
	now := time.Now().UTC()
	records := []engine.JournalRecord{
		{Seq: 1, Timestamp: now, Event: engine.RunStarted{RunID: "r", Steps: []string{"review"}}},
		{Seq: 2, Timestamp: now, Event: engine.StepStatus{StepID: "review", To: step.StatusAwaitingReview}},
		{Seq: 3, Timestamp: now, Event: engine.ReviewRequest{StepID: "review"}},
	}
	report := FoldStatus("r", records, true)
	if report.State != StateActive || len(report.WaitingOn) != 1 || report.WaitingOn[0].Kind != "review" {
		t.Fatalf("report = %#v", report)
	}
	records = append(records, engine.JournalRecord{Seq: 4, Timestamp: now, Event: engine.RunFinished{RunID: "r", Failed: true}})
	report = FoldStatus("r", records, true)
	if report.State != StateFailed || report.Reopenable || report.FinishedAt == nil {
		t.Fatalf("finished report = %#v", report)
	}
}
