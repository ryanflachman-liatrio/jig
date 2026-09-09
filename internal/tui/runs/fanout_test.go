package runs

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/tui/monitor"
)

// TestRunsFanOutTotalIncreasesOnExpansion proves a live FanOutExpanded event
// discovers its children and increases total/done deterministically, while
// preserving the static pre-expansion count until the event arrives.
func TestRunsFanOutTotalIncreasesOnExpansion(t *testing.T) {
	m := NewModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "run-1", Workflow: "wf", Steps: []string{"a", "analyze", "c"},
	}})

	row := m.rows[m.index["run-1"]]
	if row.total != 3 {
		t.Fatalf("pre-expansion total: want 3, got %d", row.total)
	}
	if got := runRowProgress(row); got != "0/3 steps" {
		t.Fatalf("pre-expansion progress: want %q, got %q", "0/3 steps", got)
	}

	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusSucceeded,
	}})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.FanOutExpanded{
		RunID:    "run-1",
		FamilyID: "analyze",
		Instances: []engine.FanOutInstanceDescriptor{
			{InstanceID: "analyze.i0", Index: 0},
			{InstanceID: "analyze.i1", Index: 1},
			{InstanceID: "analyze.i2", Index: 2},
			{InstanceID: "analyze.i3", Index: 3},
		},
	}})

	row = m.rows[m.index["run-1"]]
	// 3 static steps (a, analyze, c) + 4 discovered children = 7. Expansion
	// increases the count deterministically; it never shrinks or replaces the
	// pre-expansion total.
	if row.total != 7 {
		t.Fatalf("post-expansion total: want 7, got %d", row.total)
	}
	if got := runRowProgress(row); got != "1/7 steps" {
		t.Fatalf("post-expansion progress: want %q, got %q", "1/7 steps", got)
	}

	// An unrecognized child StepStatus is folded, not treated as corrupt.
	for _, id := range []string{"analyze.i0", "analyze.i1", "analyze.i2", "analyze.i3"} {
		m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
			RunID: "run-1", StepID: id, To: step.StatusSucceeded,
		}})
	}
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "analyze", To: step.StatusSucceeded,
	}})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "c", To: step.StatusSucceeded,
	}})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunFinished{RunID: "run-1"}})

	row = m.rows[m.index["run-1"]]
	if !row.done {
		t.Fatal("expected run marked done after all discovered children settle")
	}
	if got := runRowProgress(row); got != "7/7 steps" {
		t.Fatalf("final progress: want %q, got %q", "7/7 steps", got)
	}
}

// TestRunsFanOutReplay proves Hydrate folds a historical FanOutExpanded event
// exactly like the live path, from a single journal-shaped event slice.
func TestRunsFanOutReplay(t *testing.T) {
	m := NewModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	past := [][]engine.Event{
		{
			engine.RunStarted{RunID: "past-1", Workflow: "wf", Steps: []string{"discover", "analyze"}},
			engine.StepStatus{RunID: "past-1", StepID: "discover", To: step.StatusSucceeded},
			engine.FanOutExpanded{
				RunID:    "past-1",
				FamilyID: "analyze",
				Instances: []engine.FanOutInstanceDescriptor{
					{InstanceID: "analyze.i0", Index: 0},
					{InstanceID: "analyze.i1", Index: 1},
				},
			},
			engine.StepStatus{RunID: "past-1", StepID: "analyze.i0", To: step.StatusSucceeded},
			engine.StepStatus{RunID: "past-1", StepID: "analyze.i1", To: step.StatusSucceeded},
			engine.StepStatus{RunID: "past-1", StepID: "analyze", To: step.StatusSucceeded},
			engine.RunFinished{RunID: "past-1"},
		},
	}
	m = m.Hydrate(past)

	row := m.rows[m.index["past-1"]]
	if row.total != 4 {
		t.Fatalf("replayed total: want 4 (discover, analyze, i0, i1), got %d", row.total)
	}
	if got := runRowProgress(row); got != "4/4 steps" {
		t.Fatalf("replayed progress: want %q, got %q", "4/4 steps", got)
	}
	if !row.done || row.failed {
		t.Fatalf("replayed run: want done && !failed, got done=%v failed=%v", row.done, row.failed)
	}
}

// TestRunsFanOutGenerationReplacementBumpsCount proves a reset that
// re-expands a family to a new generation adds its new instance ids on top of
// the running total rather than shrinking it back down — the prior
// generation's children remain counted as historical residue, matching the
// scheduler's own bookkeeping (A8 "Reset, routes, worktrees, and budgets").
func TestRunsFanOutGenerationReplacementBumpsCount(t *testing.T) {
	m := NewModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "run-1", Workflow: "wf", Steps: []string{"analyze"},
	}})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.FanOutExpanded{
		RunID: "run-1", FamilyID: "analyze", Generation: 0,
		Instances: []engine.FanOutInstanceDescriptor{
			{InstanceID: "analyze.g0.i0", Index: 0},
			{InstanceID: "analyze.g0.i1", Index: 1},
		},
	}})
	row := m.rows[m.index["run-1"]]
	if row.total != 3 {
		t.Fatalf("gen-0 total: want 3, got %d", row.total)
	}

	// Operator resets the family; the scheduler re-expands to a new generation
	// with different instance ids (a fresh producer read can also change the
	// item count — here it grows from 2 to 3).
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.FanOutExpanded{
		RunID: "run-1", FamilyID: "analyze", Generation: 1,
		Instances: []engine.FanOutInstanceDescriptor{
			{InstanceID: "analyze.g1.i0", Index: 0},
			{InstanceID: "analyze.g1.i1", Index: 1},
			{InstanceID: "analyze.g1.i2", Index: 2},
		},
	}})
	row = m.rows[m.index["run-1"]]
	if row.total != 6 {
		t.Fatalf("post-reset total: want 6 (1 family + 2 gen-0 residue + 3 gen-1), got %d", row.total)
	}
	if got := len(row.familyChildren["analyze"]); got != 3 {
		t.Fatalf("current generation children: want 3, got %d", got)
	}
}
