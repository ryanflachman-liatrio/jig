package monitor

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// newMonitorWithFamily builds a live monitor with steps a (ordinary), b
// (foreach family), c (ordinary) and expands b into n children via a live
// FanOutExpanded event.
func newMonitorWithFamily(t *testing.T, n int) Model {
	t.Helper()
	m := newMonitorWithSteps(t)
	instances := make([]engine.FanOutInstanceDescriptor, n)
	for i := 0; i < n; i++ {
		instances[i] = engine.FanOutInstanceDescriptor{
			InstanceID: fanOutChildID("b", i),
			Index:      i,
			ItemSHA256: "deadbeef",
		}
	}
	m, _ = m.Update(EngineEventMsg{Event: engine.FanOutExpanded{
		SchemaVersion: engine.FanOutExpandedVersion,
		RunID:         "run-1",
		FamilyID:      "b",
		Generation:    0,
		Iteration:     0,
		Instances:     instances,
	}})
	return m
}

func fanOutChildID(family string, i int) string {
	return fmt.Sprintf("%s%sg000.r000.i%04d", family, workflow.ForEachIDMarker, i)
}

// TestMonitorFanOutLiveExpansion proves a live FanOutExpanded event registers
// every child, does not disturb ordinary steps, and renders a collapsed family
// row with an aggregate "×N" badge until the operator expands it.
func TestMonitorFanOutLiveExpansion(t *testing.T) {
	m := newMonitorWithFamily(t, 3)

	if got := len(m.familyChildren["b"]); got != 3 {
		t.Fatalf("expected 3 registered children, got %d", got)
	}
	for _, id := range m.familyChildren["b"] {
		if _, ok := m.index[id]; !ok {
			t.Fatalf("child %q not indexed", id)
		}
	}
	// Ordinary steps a/c are untouched and still appear as top-level rows.
	rows := m.visibleRows()
	if len(rows) != 3 {
		t.Fatalf("expected 3 top-level rows (a, b, c) while family collapsed, got %d:\n%v", len(rows), rows)
	}
	body := m.body()
	if !strings.Contains(body, "×3") {
		t.Fatalf("expected family badge ×3 in body:\n%s", body)
	}
	// No color-only state: the badge is text, and the aggregate status word is
	// also rendered as plain text next to the step id.
	if !strings.Contains(body, "0/3 done") {
		t.Fatalf("expected textual completed/total progress in body:\n%s", body)
	}
}

// TestMonitorFanOutReplayedExpansion proves a RunSnapshot fold (the replay /
// resume path) reconstructs family membership identically to the live path.
func TestMonitorFanOutReplayedExpansion(t *testing.T) {
	snap := engine.RunSnapshot{
		Workflow: "demo",
		Steps: []step.State{
			{ID: "a", Status: step.StatusSucceeded},
			{ID: "b", Status: step.StatusRunning},
			{ID: "b__fanout__g000.r000.i0000", Status: step.StatusSucceeded, ParentID: "b", FanOutIndex: 0, FanOutTotal: 2},
			{ID: "b__fanout__g000.r000.i0001", Status: step.StatusRunning, ParentID: "b", FanOutIndex: 1, FanOutTotal: 2},
			{ID: "c", Status: step.StatusPending},
		},
	}
	m := New("run-1").WithSnapshot(snap)

	if got := len(m.familyChildren["b"]); got != 2 {
		t.Fatalf("expected 2 replayed children, got %d", got)
	}
	rows := m.visibleRows()
	if len(rows) != 3 {
		t.Fatalf("expected 3 collapsed top-level rows, got %d", len(rows))
	}
	body := m.body()
	if !strings.Contains(body, "×2") || !strings.Contains(body, "1/2 done") {
		t.Fatalf("expected family badge/progress in replayed body:\n%s", body)
	}
}

// TestMonitorFanOutExpandCollapse proves expanding a family row reveals its
// children in source order beneath it, collapsing hides them again, and the
// cursor (parked on the family row throughout) never moves.
func TestMonitorFanOutExpandCollapse(t *testing.T) {
	m := newMonitorWithFamily(t, 2)
	m, _ = m.Update(key("j")) // cursor: a -> b (the family)
	if id := m.cursorStepID(); id != "b" {
		t.Fatalf("expected cursor on family b, got %q", id)
	}

	m, _ = m.Update(key(" ")) // expand
	if !m.expanded["b"] {
		t.Fatal("expected family b expanded")
	}
	if got := m.cursorStepID(); got != "b" {
		t.Fatalf("cursor moved off family row on expand: %q", got)
	}
	rows := m.visibleRows()
	wantChildren := m.familyChildren["b"]
	if len(wantChildren) != 2 {
		t.Fatalf("expected 2 children, got %d", len(wantChildren))
	}
	// Rows after expand: a, b, child0, child1, c.
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows after expand, got %d:\n%v", len(rows), rows)
	}
	if rows[2].stepID != wantChildren[0] || !rows[2].isChildRow() {
		t.Fatalf("expected child row 0 at index 2, got %+v", rows[2])
	}
	if rows[3].stepID != wantChildren[1] || !rows[3].isChildRow() {
		t.Fatalf("expected child row 1 at index 3, got %+v", rows[3])
	}

	m, _ = m.Update(key(" ")) // collapse
	if m.expanded["b"] {
		t.Fatal("expected family b collapsed")
	}
	if got := m.cursorStepID(); got != "b" {
		t.Fatalf("cursor moved off family row on collapse: %q", got)
	}
	if got := len(m.visibleRows()); got != 3 {
		t.Fatalf("expected 3 rows after collapse, got %d", got)
	}
}

// TestMonitorFanOutNestedFileExpansion proves expanding a child reveals its own
// ordinary output files (not the family's children again), and selecting a
// file row identifies the child as its owning step.
func TestMonitorFanOutNestedFileExpansion(t *testing.T) {
	runDir := t.TempDir()
	childID := "b__fanout__g000.r000.i0000"
	if _, err := datastore.StepDir(runDir, childID); err != nil {
		t.Fatalf("mkdir step dir: %v", err)
	}
	if err := os.WriteFile(datastore.OutputPath(runDir, childID), []byte("# child output\n"), 0o644); err != nil {
		t.Fatalf("write child output: %v", err)
	}

	snap := engine.RunSnapshot{
		Workflow: "demo",
		Steps: []step.State{
			{ID: "a", Status: step.StatusSucceeded},
			{ID: "b", Status: step.StatusSucceeded},
			{ID: childID, Status: step.StatusSucceeded, ParentID: "b", FanOutIndex: 0, FanOutTotal: 1},
			{ID: "c", Status: step.StatusPending},
		},
	}
	m := New("run-1")
	m.RunDir = runDir
	m = m.WithSnapshot(snap)

	m, _ = m.Update(key("j")) // a -> b
	m, _ = m.Update(key(" ")) // expand family: reveals the child, not files
	rows := m.visibleRows()
	if len(rows) != 4 || !rows[2].isChildRow() {
		t.Fatalf("expected child row after family expand, got:\n%v", rows)
	}

	m, _ = m.Update(key("j")) // b -> child
	if got := m.cursorStepID(); got != childID {
		t.Fatalf("expected cursor on child, got %q", got)
	}
	m, _ = m.Update(key(" ")) // expand child: reveals its own files
	rows = m.visibleRows()
	found := false
	for _, r := range rows {
		if r.isFileRow() && r.stepID == childID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a file row under the expanded child:\n%v", rows)
	}
}

// TestMonitorFanOutGateFocusesChild proves a gate event for a runtime child
// (e.g. a RecoveryRequest carrying the child's full instance id) auto-expands
// its family and focuses the exact child row via ctrl+o's showGateContext path.
func TestMonitorFanOutGateFocusesChild(t *testing.T) {
	m := newMonitorWithFamily(t, 2)
	childID := m.familyChildren["b"][1]

	m, _ = m.Update(EngineEventMsg{Event: engine.RecoveryRequest{
		RunID:  "run-1",
		StepID: childID,
		Err:    "child failed",
	}})
	if len(m.inputQueue) == 0 {
		t.Fatal("recovery event not queued")
	}

	m.focus = focusGate
	m.toggleGateContext()

	if m.expanded["b"] != true {
		t.Fatal("expected family auto-expanded to focus its child")
	}
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor].stepID != childID {
		t.Fatalf("expected cursor on child %q, got row %+v (cursor=%d)", childID, rows, m.cursor)
	}
}

// TestMonitorFanOutResetOnlyOnFamily proves reset is offered on the collapsed
// family row but never on one of its runtime children, per the plan
// ("reset is offered only on the family").
func TestMonitorFanOutResetOnlyOnFamily(t *testing.T) {
	snap := engine.RunSnapshot{
		Workflow: "demo",
		Done:     false,
		Steps: []step.State{
			{ID: "a", Status: step.StatusSucceeded},
			{ID: "b", Status: step.StatusSucceeded},
			{ID: "b__fanout__g000.r000.i0000", Status: step.StatusSucceeded, ParentID: "b", FanOutIndex: 0, FanOutTotal: 1},
			{ID: "c", Status: step.StatusPending},
		},
	}
	m := New("run-1").WithSnapshot(snap)
	// selectedLifecycleActions requires a non-nil run handle and !m.done.
	m.run = &engine.Run{}

	m, _ = m.Update(key("j")) // a -> b
	actions := m.selectedLifecycleActions()
	if !actions.canReset {
		t.Fatal("expected reset offered on the family row")
	}

	m, _ = m.Update(key(" ")) // expand family
	m, _ = m.Update(key("j")) // b -> child
	if id := m.cursorStepID(); id != "b__fanout__g000.r000.i0000" {
		t.Fatalf("expected cursor on child, got %q", id)
	}
	actions = m.selectedLifecycleActions()
	if actions.canReset {
		t.Fatal("expected reset NOT offered on a fan-out child")
	}
}

// TestMonitorFanOutNarrowWidth proves the family/child rows render without
// panicking or losing status text at a narrow terminal width, and that status
// is conveyed by text (not color alone) at every width.
func TestMonitorFanOutNarrowWidth(t *testing.T) {
	m := newMonitorWithFamily(t, 2)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m, _ = m.Update(key("j"))
	m, _ = m.Update(key(" "))

	body := m.body()
	if !strings.Contains(body, string(step.StatusPending)) {
		t.Fatalf("expected textual child status at narrow width:\n%s", body)
	}
}
