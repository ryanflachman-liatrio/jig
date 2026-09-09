package ops

import (
	"testing"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

// TestFoldStatusReportsChildProvenanceAndOrdering proves status folds a
// FanOutExpanded event into per-child parent_id/index/total, orders children
// directly beneath their family, and folds every generation across a reset
// re-expansion (A8 tasks 30/31).
func TestFoldStatusReportsChildProvenanceAndOrdering(t *testing.T) {
	report := FoldStatus("r1", []engine.JournalRecord{
		{Seq: 1, Event: engine.RunStarted{RunID: "r1", Workflow: "wf", Steps: []string{"discover", "analyze"}}},
		{Seq: 2, Event: engine.StepStatus{RunID: "r1", StepID: "discover", To: step.StatusSucceeded}},
		{Seq: 3, Event: engine.FanOutExpanded{
			SchemaVersion: engine.FanOutExpandedVersion,
			RunID:         "r1", FamilyID: "analyze", Generation: 0,
			Instances: []engine.FanOutInstanceDescriptor{
				{InstanceID: "analyze.g0.i0", Index: 0},
				{InstanceID: "analyze.g0.i1", Index: 1},
			},
		}},
		{Seq: 4, Event: engine.StepStatus{RunID: "r1", StepID: "analyze.g0.i0", To: step.StatusFailed, Err: "boom"}},
		{Seq: 5, Event: engine.StepStatus{RunID: "r1", StepID: "analyze.g0.i1", To: step.StatusSucceeded}},
		// Operator resets the family; the scheduler re-expands to generation 1.
		{Seq: 6, Event: engine.FanOutExpanded{
			SchemaVersion: engine.FanOutExpandedVersion,
			RunID:         "r1", FamilyID: "analyze", Generation: 1,
			Instances: []engine.FanOutInstanceDescriptor{
				{InstanceID: "analyze.g1.i0", Index: 0},
			},
		}},
		{Seq: 7, Event: engine.StepStatus{RunID: "r1", StepID: "analyze.g1.i0", To: step.StatusSucceeded}},
		{Seq: 8, Event: engine.StepStatus{RunID: "r1", StepID: "analyze", To: step.StatusSucceeded}},
	}, false)

	wantOrder := []string{"discover", "analyze", "analyze.g0.i0", "analyze.g0.i1", "analyze.g1.i0"}
	if len(report.Steps) != len(wantOrder) {
		t.Fatalf("steps = %#v, want ids %v", report.Steps, wantOrder)
	}
	for i, id := range wantOrder {
		if report.Steps[i].ID != id {
			t.Fatalf("steps[%d].ID = %q, want %q (full: %#v)", i, report.Steps[i].ID, id, report.Steps)
		}
	}

	byID := map[string]StepReport{}
	for _, s := range report.Steps {
		byID[s.ID] = s
	}
	if p := byID["analyze"]; p.ParentID != "" {
		t.Fatalf("family analyze must not carry a parent_id, got %q", p.ParentID)
	}
	c := byID["analyze.g0.i0"]
	if c.ParentID != "analyze" || c.FanOutIndex != 0 || c.FanOutTotal != 2 {
		t.Fatalf("gen0 child 0 provenance = %+v", c)
	}
	if c.Status != step.StatusFailed || c.Error != "boom" {
		t.Fatalf("gen0 child 0 status/error = %+v", c)
	}
	c1 := byID["analyze.g1.i0"]
	if c1.ParentID != "analyze" || c1.FanOutIndex != 0 || c1.FanOutTotal != 1 {
		t.Fatalf("gen1 child provenance = %+v", c1)
	}
}

// TestReadLogsAllDiscoversFanOutChildren proves `logs --all` discovers every
// runtime instance transcript recorded by FanOutExpanded, and that `--step`
// accepts a full runtime instance id.
func TestReadLogsAllDiscoversFanOutChildren(t *testing.T) {
	root := t.TempDir()
	runID := "20260908-130000-fanoutlogs"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	writeOpsJournal(t, runDir,
		engine.RunStarted{RunID: runID, Workflow: "wf", Steps: []string{"discover", "analyze"}},
		engine.StepStatus{RunID: runID, StepID: "discover", To: step.StatusSucceeded},
		engine.FanOutExpanded{
			SchemaVersion: engine.FanOutExpandedVersion,
			RunID:         runID, FamilyID: "analyze",
			Instances: []engine.FanOutInstanceDescriptor{
				{InstanceID: "analyze.__fanout__.g000.r000.i0000", Index: 0},
				{InstanceID: "analyze.__fanout__.g000.r000.i0001", Index: 1},
			},
		},
	)
	writeTranscript(t, runDir, "discover", []transcript.Entry{
		{Seq: 1, Ts: "2026-09-08T13:00:00Z", Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "d1"}}},
	})
	writeTranscript(t, runDir, "analyze.__fanout__.g000.r000.i0000", []transcript.Entry{
		{Seq: 1, Ts: "2026-09-08T13:00:01Z", Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "c0"}}},
	})
	writeTranscript(t, runDir, "analyze.__fanout__.g000.r000.i0001", []transcript.Entry{
		{Seq: 1, Ts: "2026-09-08T13:00:02Z", Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "c1"}}},
	})

	batch, err := ReadLogs(root, runID, LogOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range batch.Entries {
		seen[e.StepID] = true
	}
	for _, want := range []string{"discover", "analyze.__fanout__.g000.r000.i0000", "analyze.__fanout__.g000.r000.i0001"} {
		if !seen[want] {
			t.Errorf("logs --all missing step %q; got entries %#v", want, batch.Entries)
		}
	}

	// --step accepts a full runtime instance id.
	single, err := ReadLogs(root, runID, LogOptions{StepID: "analyze.__fanout__.g000.r000.i0001"})
	if err != nil {
		t.Fatalf("--step with full runtime id: %v", err)
	}
	if len(single.Entries) != 1 || single.Entries[0].StepID != "analyze.__fanout__.g000.r000.i0001" {
		t.Fatalf("single-step batch = %#v", single.Entries)
	}

	// The default tail (neither --all nor --step) stays scoped to the static
	// declared graph and does not error just because children exist.
	if _, err := ReadLogs(root, runID, LogOptions{}); err != nil {
		t.Fatalf("default tail with fan-out children present: %v", err)
	}

	if _, err := ReadLogs(root, runID, LogOptions{StepID: "no-such-step"}); err == nil {
		t.Error("unknown step id (including among discovered children) should fail")
	}
}

// TestPreviewResetStaysFamilyLevel proves a reset preview targeting a foreach
// family reports only the family itself — never its runtime children — even
// after it has expanded and one child has failed. Applied reset (not covered
// here) is the scheduler's job of expanding the closure; ops only previews.
func TestPreviewResetStaysFamilyLevel(t *testing.T) {
	root := t.TempDir()
	runID := "20260908-140000-fanoutreset"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	const source = `
[workflow]
name = "fanout-reset"
version = "1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
skill = "skills/analyze"
depends_on = ["discover"]

  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 4
`
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	writeCapturedWorkflow(t, runDir, source, wf)
	writeOpsJournal(t, runDir,
		engine.RunStarted{RunID: runID, Workflow: wf.Meta.Name, Steps: []string{"discover", "analyze"}},
		engine.StepStatus{RunID: runID, StepID: "discover", To: step.StatusSucceeded},
		engine.FanOutExpanded{
			SchemaVersion: engine.FanOutExpandedVersion,
			RunID:         runID, FamilyID: "analyze",
			Instances: []engine.FanOutInstanceDescriptor{
				{InstanceID: "analyze.__fanout__.g000.r000.i0000", Index: 0},
				{InstanceID: "analyze.__fanout__.g000.r000.i0001", Index: 1},
			},
		},
		engine.StepStatus{RunID: runID, StepID: "analyze.__fanout__.g000.r000.i0000", To: step.StatusFailed, Err: "boom"},
		engine.StepStatus{RunID: runID, StepID: "analyze", To: step.StatusAwaitingRecovery},
	)

	preview, err := PreviewReset(root, runID, "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Steps) != 1 || preview.Steps[0].ID != "analyze" {
		t.Fatalf("family-level reset preview = %#v, want exactly [analyze]", preview.Steps)
	}
}
