package engine

// Tests for rebuilding [step.foreach] families/children from durable
// FanOutExpanded manifests on replay/Resume (task 17 of
// docs/plans/a8-dynamic-foreach-fan-out.md, Phase 3 — see resume.go's
// rebuildFanOutFamily and the ForEach special cases in
// restoreUnfinishedCheckpoint/rehydrateParks).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/workflow"
)

// writeFanOutManifestForTest writes a manifest with the given raw JSON items
// (already valid JSON, e.g. `{"name":"api"}`) and returns it alongside a
// matching FanOutExpanded event whose ManifestDigest is correct — callers that
// want to prove fail-closed behavior mutate the returned event afterward.
func writeFanOutManifestForTest(t *testing.T, runDir, familyID string, generation, iteration int, items ...string) (datastore.FanOutManifest, FanOutExpanded) {
	t.Helper()
	m := datastore.FanOutManifest{
		FamilyID:     familyID,
		Generation:   generation,
		Iteration:    iteration,
		SourceRef:    "@discover.targets",
		SourceDigest: "test-source-digest",
	}
	var instances []FanOutInstanceDescriptor
	for i, raw := range items {
		id := fmt.Sprintf("%s%sg%03d.r%03d.i%04d", familyID, workflow.ForEachIDMarker, generation, iteration, i)
		item := json.RawMessage(raw)
		digest := datastore.ItemDigest(item)
		m.Items = append(m.Items, datastore.FanOutItem{InstanceID: id, Index: i, Item: item, ItemSHA256: digest})
		instances = append(instances, FanOutInstanceDescriptor{InstanceID: id, Index: i, ItemSHA256: digest})
	}
	if err := datastore.WriteFanOutManifest(runDir, m); err != nil {
		t.Fatalf("WriteFanOutManifest: %v", err)
	}
	// Re-read so m.SchemaVersion reflects what WriteFanOutManifest defaulted it
	// to, keeping the digest computed here identical to what resume.go recomputes.
	m, err := datastore.ReadFanOutManifest(runDir, familyID, generation, iteration)
	if err != nil {
		t.Fatalf("ReadFanOutManifest: %v", err)
	}
	digest, err := datastore.FanOutManifestDigest(m)
	if err != nil {
		t.Fatalf("FanOutManifestDigest: %v", err)
	}
	ev := FanOutExpanded{
		SchemaVersion:  FanOutExpandedVersion,
		RunID:          "r",
		FamilyID:       familyID,
		Generation:     generation,
		Iteration:      iteration,
		ManifestDigest: digest,
		Instances:      instances,
	}
	return m, ev
}

// TestRestoreUnfinishedCheckpoint_ForEach_CrashBeforeExpansionEventCreatesNoFamily
// proves the "process dies after the manifest rename but before the event"
// boundary: with no FanOutExpanded event in the journal, the family stays
// exactly where it was left (Pending) and no runtime child is created — the
// scheduler's own next dispatch pass re-expands deterministically and may
// overwrite whatever manifest bytes happen to already be on disk.
func TestRestoreUnfinishedCheckpoint_ForEach_CrashBeforeExpansionEventCreatesNoFamily(t *testing.T) {
	const toml = `
[workflow]
name = "foreach-crash-before-expand"
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
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"

[[step]]
id = "solo"
type = "command"
run = "true"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	events := []Event{
		RunStarted{RunID: "r", Workflow: wf.Meta.Name, Steps: []string{"discover", "analyze", "solo"}},
		StepStatus{RunID: "r", StepID: "discover", From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: "r", StepID: "discover", From: step.StatusRunning, To: step.StatusSucceeded},
		StepStatus{RunID: "r", StepID: "solo", From: step.StatusPending, To: step.StatusRunning},
		// Crash here: dispatchForEach for "analyze" never got past resolving the
		// list (or died between the manifest rename and the journal write) — no
		// FanOutExpanded line exists at all.
	}
	checkpoint, err := restoreUnfinishedCheckpoint(runDir, wf, events)
	if err != nil {
		t.Fatalf("restoreUnfinishedCheckpoint: %v", err)
	}
	if len(checkpoint.fanOutFamilies) != 0 {
		t.Fatalf("fanOutFamilies = %v, want none (no FanOutExpanded was journaled)", checkpoint.fanOutFamilies)
	}
	if got := checkpoint.states["analyze"].Status; got != step.StatusPending {
		t.Fatalf("analyze status = %s, want Pending", got)
	}
	if !checkpoint.interrupted["solo"] {
		t.Fatal("solo must be classified as an interrupted worker so Resume has something to reopen")
	}
}

// TestRestoreUnfinishedCheckpoint_ForEach_ManifestDigestMismatchFailsClosed
// proves "if the event is durable, the manifest must already exist and match
// or Resume fails closed": a manifest that differs from what FanOutExpanded
// journaled (rewritten, or never durably renamed) must never be trusted.
func TestRestoreUnfinishedCheckpoint_ForEach_ManifestDigestMismatchFailsClosed(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	_, ev := writeFanOutManifestForTest(t, runDir, "analyze", 0, 0, `{"name":"api","path":"services/api"}`)
	ev.ManifestDigest = strings.Repeat("0", len(ev.ManifestDigest))
	events := []Event{
		RunStarted{RunID: "r", Workflow: wf.Meta.Name, Steps: []string{"discover", "analyze"}},
		StepStatus{RunID: "r", StepID: "discover", From: step.StatusPending, To: step.StatusSucceeded},
		ev,
	}
	_, err = restoreUnfinishedCheckpoint(runDir, wf, events)
	if err == nil {
		t.Fatal("expected a digest-mismatch error")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Fatalf("error = %v, want a digest-mismatch error", err)
	}
}

// TestRestoreUnfinishedCheckpoint_ForEach_MissingManifestFailsClosed proves a
// FanOutExpanded event whose manifest file is simply absent (e.g. a run
// directory copied without its fanout/ subtree) fails closed rather than
// silently creating zero children for a family the journal says has some.
func TestRestoreUnfinishedCheckpoint_ForEach_MissingManifestFailsClosed(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	events := []Event{
		RunStarted{RunID: "r", Workflow: wf.Meta.Name, Steps: []string{"discover", "analyze"}},
		StepStatus{RunID: "r", StepID: "discover", From: step.StatusPending, To: step.StatusSucceeded},
		FanOutExpanded{SchemaVersion: FanOutExpandedVersion, RunID: "r", FamilyID: "analyze", ManifestDigest: "does-not-matter",
			Instances: []FanOutInstanceDescriptor{{InstanceID: "analyze" + workflow.ForEachIDMarker + "g000.r000.i0000", Index: 0}}},
	}
	if _, err := restoreUnfinishedCheckpoint(runDir, wf, events); err == nil {
		t.Fatal("expected a missing-manifest error")
	}
}

// TestRestoreUnfinishedCheckpoint_ForEach_CorruptManifestFailsClosed proves a
// manifest that exists but does not decode (disk corruption, a half-written
// file that somehow survived) also fails closed.
func TestRestoreUnfinishedCheckpoint_ForEach_CorruptManifestFailsClosed(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	path := datastore.FanOutManifestPath(runDir, "analyze", 0, 0)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := []Event{
		RunStarted{RunID: "r", Workflow: wf.Meta.Name, Steps: []string{"discover", "analyze"}},
		StepStatus{RunID: "r", StepID: "discover", From: step.StatusPending, To: step.StatusSucceeded},
		FanOutExpanded{SchemaVersion: FanOutExpandedVersion, RunID: "r", FamilyID: "analyze", ManifestDigest: "does-not-matter"},
	}
	if _, err := restoreUnfinishedCheckpoint(runDir, wf, events); err == nil {
		t.Fatal("expected a corrupt-manifest error")
	}
}

// TestRestoreUnfinishedCheckpoint_ForEach_ExactItemReuseAndMixedParkKinds
// proves the manifest is the sole source of truth for instance identity/order
// (never a fresh producer read) and that children parked in different ways —
// succeeded, mid-flight (interrupted), and needs_input — are folded through
// exactly the same classification a static step would get.
func TestRestoreUnfinishedCheckpoint_ForEach_ExactItemReuseAndMixedParkKinds(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	m, ev := writeFanOutManifestForTest(t, runDir, "analyze", 0, 0,
		`{"name":"a","path":"a"}`, `{"name":"b","path":"b"}`, `{"name":"c","path":"c"}`)
	child0, child1, child2 := m.Items[0].InstanceID, m.Items[1].InstanceID, m.Items[2].InstanceID

	events := []Event{
		RunStarted{RunID: "r", Workflow: wf.Meta.Name, Steps: []string{"discover", "analyze"}},
		StepStatus{RunID: "r", StepID: "discover", From: step.StatusPending, To: step.StatusSucceeded},
		StepStatus{RunID: "r", StepID: "analyze", From: step.StatusPending, To: step.StatusRunning},
		ev,
		StepStatus{RunID: "r", StepID: child0, From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: "r", StepID: child0, From: step.StatusRunning, To: step.StatusSucceeded},
		StepStatus{RunID: "r", StepID: child1, From: step.StatusPending, To: step.StatusRunning},
		// child1 crashes mid-flight: no further StepStatus for it.
		StepStatus{RunID: "r", StepID: child2, From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: "r", StepID: child2, From: step.StatusRunning, To: step.StatusNeedsInput},
	}
	checkpoint, err := restoreUnfinishedCheckpoint(runDir, wf, events)
	if err != nil {
		t.Fatalf("restoreUnfinishedCheckpoint: %v", err)
	}

	fam := checkpoint.fanOutFamilies["analyze"]
	if fam == nil {
		t.Fatal("no family rebuilt for analyze")
	}
	wantOrder := []string{child0, child1, child2}
	if len(fam.Order) != len(wantOrder) {
		t.Fatalf("fam.Order = %v, want %v", fam.Order, wantOrder)
	}
	for i, id := range wantOrder {
		if fam.Order[i] != id {
			t.Fatalf("fam.Order[%d] = %q, want %q (manifest order must be reused exactly)", i, fam.Order[i], id)
		}
	}
	for i, id := range wantOrder {
		item, ok := checkpoint.fanOutItemByChild[id]
		if !ok {
			t.Fatalf("no FanOutItem rebuilt for %q", id)
		}
		if item.Index != i || item.Total != 3 || item.As != "target" {
			t.Fatalf("FanOutItem for %q = %+v, want index %d total 3 as %q", id, item, i, "target")
		}
		if string(item.Item) != string(m.Items[i].Item) {
			t.Fatalf("FanOutItem.Item for %q = %s, want exact manifest bytes %s", id, item.Item, m.Items[i].Item)
		}
	}

	if got := checkpoint.states[child0].Status; got != step.StatusSucceeded {
		t.Errorf("child0 status = %s, want Succeeded", got)
	}
	if !checkpoint.interrupted[child1] {
		t.Error("child1 must be classified as an interrupted worker")
	}
	if got := checkpoint.states[child2].Status; got != step.StatusNeedsInput {
		t.Errorf("child2 status = %s, want NeedsInput", got)
	}
	if _, ok := checkpoint.missingInputPayload[child2]; !ok {
		t.Error("child2 has no durable InputRequest and no pending questions — must be classified as missing input payload")
	}
	// The family's own status is unaffected by its children's parks — it must
	// never itself appear in the interrupted map (it is a barrier, not a worker).
	if checkpoint.interrupted["analyze"] {
		t.Error("the family step itself must never be classified as an interrupted worker")
	}
	if !checkpoint.hasParks() {
		t.Fatal("hasParks() must be true: a child is interrupted and another needs input")
	}
}

// TestResumeForEach_SettlesBarrierForAlreadyTerminalChildren proves the crash
// boundary between a family's last child reaching a terminal status and the
// family's own barrier-settlement transition: on Resume, the family (still
// Running, per the journal) is not treated as a park, but Resume must still
// notice every child is done and close the barrier — writing the aggregate
// and letting the run finish — without any operator action.
func TestResumeForEach_SettlesBarrierForAlreadyTerminalChildren(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".jig")
	runID := "foreach-settle-on-resume"
	runDir, err := datastore.RunDir(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	m, ev := writeFanOutManifestForTest(t, runDir, "analyze", 0, 0,
		`{"name":"a","path":"a"}`, `{"name":"b","path":"b"}`)
	child0, child1 := m.Items[0].InstanceID, m.Items[1].InstanceID

	events := []Event{
		RunStarted{RunID: runID, Workflow: wf.Meta.Name, Steps: []string{"discover", "analyze"}},
		StepStatus{RunID: runID, StepID: "discover", From: step.StatusPending, To: step.StatusSucceeded},
		StepStatus{RunID: runID, StepID: "analyze", From: step.StatusPending, To: step.StatusRunning},
		ev,
		StepStatus{RunID: runID, StepID: child0, From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: runID, StepID: child0, From: step.StatusRunning, To: step.StatusSucceeded},
		StepStatus{RunID: runID, StepID: child1, From: step.StatusPending, To: step.StatusRunning},
		StepStatus{RunID: runID, StepID: child1, From: step.StatusRunning, To: step.StatusSucceeded},
		// Crash here: both children are terminal, but the family's own
		// Running -> Succeeded transition (trySettleFamilyIfChild's emit) never
		// got journaled.
	}
	writeJournal(t, runDir, events)

	mgr := NewManager(&testExec{}, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Resume(runID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := collectEvents(t, ch, 5*time.Second)
	rf, ok := got[len(got)-1].(RunFinished)
	if !ok || rf.Failed {
		t.Fatalf("last event = %+v, want a successful RunFinished", got[len(got)-1])
	}

	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 2 || agg.Succeeded != 2 || !agg.AllSucceeded {
		t.Fatalf("aggregate = %+v, want both children counted as succeeded", agg)
	}
	ids := childIDs(agg)
	if len(ids) != 2 || ids[0] != child0 || ids[1] != child1 {
		t.Fatalf("aggregate instance ids = %v, want exact manifest reuse [%s %s]", ids, child0, child1)
	}
}
