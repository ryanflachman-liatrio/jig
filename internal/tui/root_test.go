package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/runner"
	"jig/internal/step"
	"jig/internal/tui/detail"
	"jig/internal/tui/monitor"
	runspane "jig/internal/tui/runs"
	"jig/internal/tui/selector"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

func TestHydrateRunsMarksOrphansTerminal(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          step.Status
		corruptSnapshot bool
	}{
		{name: "missing snapshot running", status: step.StatusRunning},
		{name: "missing snapshot review", status: step.StatusAwaitingReview},
		{name: "missing snapshot pending", status: step.StatusPending},
		{name: "corrupt snapshot review", status: step.StatusAwaitingReview, corruptSnapshot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jigRoot := t.TempDir()
			runID := "20260908-120000-orphan"
			runDir := filepath.Join(jigRoot, "runs", runID)
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.corruptSnapshot {
				if err := os.WriteFile(datastore.WorkflowSnapshotPath(runDir), []byte(`{"broken":`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			events := []engine.Event{engine.RunStarted{RunID: runID, Workflow: "wf", Steps: []string{"a"}}}
			if tc.status != step.StatusPending {
				events = append(events, engine.StepStatus{RunID: runID, StepID: "a", From: step.StatusPending, To: tc.status})
			}
			var journal []byte
			for i, event := range events {
				line, err := engine.MarshalEnvelope(i+1, event)
				if err != nil {
					t.Fatal(err)
				}
				journal = append(journal, line...)
				journal = append(journal, '\n')
			}
			if err := os.WriteFile(datastore.JournalPath(runDir), journal, 0o644); err != nil {
				t.Fatal(err)
			}

			exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
			msg, ok := hydrateRunsCmd(engine.NewManager(exec, jigRoot))().(runsHydratedMsg)
			if !ok || len(msg.runs) != 1 {
				t.Fatalf("hydration message = %#v, want one run", msg)
			}
			if got := engine.ClassifyUnfinished(msg.runs[0]); got != engine.UnfinishedNone {
				t.Fatalf("orphan classification = %v, want terminal", got)
			}
			if _, ok := msg.runs[0][len(msg.runs[0])-1].(engine.RunFinished); !ok {
				t.Fatalf("last hydrated event = %T, want RunFinished", msg.runs[0][len(msg.runs[0])-1])
			}
		})
	}
}

func TestHydrateRunsKeepsMissingAndCorruptJournalRows(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		name := "missing"
		if corrupt {
			name = "corrupt first record"
		}
		t.Run(name, func(t *testing.T) {
			jigRoot := t.TempDir()
			runID := "20260908-130000-journal-orphan"
			runDir := filepath.Join(jigRoot, "runs", runID)
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				if err := os.WriteFile(datastore.JournalPath(runDir), []byte("{ bad\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
			msg := hydrateRunsCmd(engine.NewManager(exec, jigRoot))().(runsHydratedMsg)
			if len(msg.runs) != 1 {
				t.Fatalf("hydrated groups = %d, want orphan row", len(msg.runs))
			}
			if finished, ok := msg.runs[0][len(msg.runs[0])-1].(engine.RunFinished); !ok || !finished.Failed {
				t.Fatalf("orphan finish = %#v", msg.runs[0][len(msg.runs[0])-1])
			}
			pane := runspane.NewModel().Hydrate(msg.runs).WithWorkflowContext("selected-workflow", nil)
			body := pane.PaneBody(80, 12)
			if !strings.Contains(body, "orphan") || !strings.Contains(body, "failed") {
				t.Fatalf("filtered Runs pane hid terminal orphan:\n%s", body)
			}
		})
	}
}

// TestHomeColdStartAutoSelectsFirstWorkflow drives Home without a terminal:
// discover workflows, assert the first is selected and its runs pane is labeled.
func TestHomeColdStartAutoSelectsFirstWorkflow(t *testing.T) {
	dir := t.TempDir()
	writeWF := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeWF("alpha", `
[workflow]
name = "alpha"
version = "1"
description = "first"
[[step]]
id = "hello"
type = "command"
run = "echo hi"
`)
	writeWF("beta", `
[workflow]
name = "beta"
version = "1"
description = "second"
[[step]]
id = "hello"
type = "command"
run = "echo hi"
`)

	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, "")
	var m tea.Model = New(context.Background(), mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m, cmd := m.Update(selector.DiscoverCmd(dir)())
	if cmd != nil {
		m, _ = m.Update(cmd())
	}

	root := m.(rootModel)
	path, ok := root.selector.SelectedPath()
	if !ok || !strings.Contains(path, "alpha") {
		t.Fatalf("expected first workflow alpha selected, path=%q ok=%v", path, ok)
	}
	view := m.View().Content
	for _, want := range []string{"Workflows", "Runs", "alpha", "No runs yet"} {
		if !strings.Contains(view, want) {
			t.Fatalf("home view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Press r in a workflow detail") {
		t.Fatalf("empty CTA still points at Detail:\n%s", view)
	}
}

// TestHomeEnterFocusesRunsAndDOpensDetailOverlay covers G2/A2.
func TestHomeEnterFocusesRunsAndDOpensDetailOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mini.toml")
	os.WriteFile(path, []byte(`
[workflow]
name = "mini"
version = "1"
description = "a tiny workflow"
[[step]]
id = "hello"
type = "command"
run = "echo hi"
`), 0o644)

	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, "")
	var m tea.Model = New(context.Background(), mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m, cmd := m.Update(selector.DiscoverCmd(dir)())
	if cmd != nil {
		m, _ = m.Update(cmd())
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	root := m.(rootModel)
	if root.homeFocus != homeRuns {
		t.Fatalf("enter on workflow should focus Runs, got %v", root.homeFocus)
	}
	if root.showDetailOverlay {
		t.Fatal("enter must not open Detail overlay")
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // back to workflows
	m, cmd = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	root = m.(rootModel)
	if !root.showDetailOverlay {
		t.Fatal("d on Workflows should open Detail overlay")
	}
	// Deliver the async workflow load into the overlay's detail model.
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m, _ = m.Update(msg)
		}
	}
	m, _ = m.Update(detail.New(path).Init()())
	view := m.View().Content
	if !strings.Contains(view, "mini") || !strings.Contains(view, "hello") {
		t.Fatalf("detail overlay missing content:\n%s", view)
	}

	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		m, _ = m.Update(cmd())
	}
	root = m.(rootModel)
	if root.showDetailOverlay {
		t.Fatal("esc should close Detail overlay only")
	}
	if root.active != screenHome {
		t.Fatalf("still on Home after closing overlay, active=%v", root.active)
	}
}

func TestHomeRunsFilterBySelectedWorkflow(t *testing.T) {
	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, "")
	var m tea.Model = New(context.Background(), mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	for _, started := range []engine.RunStarted{
		{RunID: "20260101-000000-alpha001", Workflow: "alpha", Steps: []string{"step"}},
		{RunID: "20260101-000000-beta0001", Workflow: "beta", Steps: []string{"step"}},
	} {
		m, _ = m.Update(monitor.EngineEventMsg{Event: started})
	}

	wf := &workflow.Workflow{Meta: workflow.Meta{Name: "alpha"}}
	m, _ = m.Update(detail.ShowRunsMsg{Workflow: "alpha", Wf: wf})
	view := m.View().Content
	if !strings.Contains(view, "alpha001") {
		t.Fatalf("home runs missing selected workflow run:\n%s", view)
	}
	if strings.Contains(view, "beta0001") {
		t.Fatalf("home runs included another workflow's run:\n%s", view)
	}
}

// TestHelpOverlayGlobal drives the real root model: "?" opens a modal on Home,
// it renders Home sections plus Global, an unrelated key is swallowed, and
// "?"/esc dismiss it.
func TestHelpOverlayGlobal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mini.toml"), []byte(`
[workflow]
name = "mini"
version = "1"
description = "a tiny workflow"

[[step]]
id = "hello"
type = "command"
run = "echo hi"
`), 0o644)

	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, "")
	var m tea.Model = New(context.Background(), mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, cmd := m.Update(selector.DiscoverCmd(dir)())
	if cmd != nil {
		m, _ = m.Update(cmd())
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	view := m.View().Content
	for _, want := range []string{"jig · help", "Workflows", "Global", "?/F1/esc close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "mini") {
		t.Fatalf("expected Home to show through beneath the modal:\n%s", view)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if !strings.Contains(m.View().Content, "jig · help") {
		t.Fatal("expected overlay to stay open on an unrelated key")
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if v := m.View().Content; strings.Contains(v, "jig · help") || !strings.Contains(v, "mini") {
		t.Fatalf("expected ? to close overlay and restore Home:\n%s", v)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: '/', Mod: tea.ModShift})
	if !strings.Contains(m.View().Content, "jig · help") {
		t.Fatal("expected shift+/ to open the overlay")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if strings.Contains(m.View().Content, "jig · help") {
		t.Fatal("expected esc to close the overlay")
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if strings.Contains(m.View().Content, "jig · help") {
		t.Fatal("question mark should remain literal while filtering")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	if view := m.View().Content; !strings.Contains(view, "jig · help") || !strings.Contains(view, "F1") {
		t.Fatalf("F1 did not open typing help:\n%s", view)
	}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	top := m.View().Content
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	bottom := m.View().Content
	if top == bottom || !strings.Contains(bottom, "ctrl+c") {
		t.Fatalf("end did not scroll the short help overlay:\n%s", bottom)
	}
}

// TestHelpOverlayCompositesOverBase verifies renderHelpOverlay layers the modal
// on top of the base screen rather than replacing it.
func TestHelpOverlayCompositesOverBase(t *testing.T) {
	const w, h = 60, 20
	row := strings.Repeat("X", w)
	rows := make([]string, h)
	for i := range rows {
		rows[i] = row
	}
	base := strings.Join(rows, "\n")

	sections := []shared.HelpSection{{Title: "Global", Bindings: []keybind.Binding{shared.KeyHelp, shared.KeyQuit}}}
	out := shared.RenderHelpOverlay(base, w, h, sections, 0)

	if !strings.Contains(out, "jig · help") {
		t.Fatalf("modal content missing:\n%s", out)
	}
	if !strings.Contains(out, "X") {
		t.Fatalf("base did not show through the composite:\n%s", out)
	}
	if first := strings.SplitN(out, "\n", 2)[0]; !strings.HasPrefix(first, "X") {
		t.Fatalf("expected base at top-left corner, got line: %q", first)
	}
}
