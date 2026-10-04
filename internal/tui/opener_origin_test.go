package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/runner"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/monitor"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type originReporter struct{}

func (originReporter) Output(string)                  {}
func (originReporter) ToolCall(string, string)        {}
func (originReporter) Message(int, int)               {}
func (originReporter) Finding(engine.SecurityFinding) {}
func (originReporter) Question(_ context.Context, r interaction.QuestionRequest) interaction.QuestionResponse {
	return interaction.QuestionResponse{RequestID: r.ID, Action: interaction.ActionCancel}
}

func TestOpenLocationOriginRunnerToLauncher(t *testing.T) {
	runDir := t.TempDir()
	path := datastore.TranscriptPath(runDir, "edit")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	oldDir, newDir := t.TempDir(), t.TempDir()
	t.Chdir(newDir) // Deliberate same-name decoy in jig's current checkout.
	for attempt, dir := range []string{oldDir, newDir} {
		if err := os.WriteFile(filepath.Join(dir, "same.go"), []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		events := []harness.Event{
			{Type: harness.EventToolUse, Tool: &toolcall.Activity{ID: "reused", Kind: "edit", Input: json.RawMessage(`{"path":"same.go","execution_dir":"/synthetic/untrusted"}`)}},
			{Type: harness.EventAssistantEnd},
			{Type: harness.EventToolResult, Tool: &toolcall.Activity{ID: "reused", Kind: "edit", Status: "completed"}},
			{Type: harness.EventUserEnd}, {Type: harness.EventResult},
		}
		h := &harness.FakeHarness{Caps: harness.NewCapabilitySet(harness.CapPermissionCallback), Sess: harness.NewFakeSession(events)}
		result, err := runner.NewAgentExecutorFixed(h).Execute(t.Context(), engine.StepRequest{RunID: "synthetic-run", Step: &workflow.Step{ID: "edit"}, ExecutionDir: dir, TranscriptPath: path, Generation: 1, Iteration: 2, Attempt: attempt}, originReporter{})
		if err != nil || result.Status != step.StatusSucceeded || h.OpenSpec.Cwd != dir {
			t.Fatalf("runner=%+v err=%v cwd=%q", result, err, h.OpenSpec.Cwd)
		}
	}
	reader, err := transcript.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reader.Tail(10)
	if err != nil || len(entries) != 4 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	if entries[0].ExecutionDir != oldDir || entries[2].ExecutionDir != newDir {
		t.Fatal("runner did not preserve attempt-local origins")
	}
	m, _ := openerRoot(t)
	output := m.tuiConfig.Opener.Argv[5]
	m.monitor = monitor.New("synthetic-run")
	m.monitor.RunDir = runDir
	m.monitor = m.monitor.WithSnapshot(engine.RunSnapshot{ID: "synthetic-run", Done: true, Steps: []step.State{{ID: "edit", Status: step.StatusSucceeded}}})
	m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.monitor, _ = m.monitor.Update(monitor.EngineEventMsg{Event: engine.StepMessage{RunID: "synthetic-run", StepID: "edit", Seq: 4}})
	m.monitor, _ = m.monitor.Update(makeKey("enter"))
	_, cmd := m.monitor.Update(makeKey("e"))
	req := cmd().(shared.OpenLocationRequest)
	if req.Target.Path != filepath.Join(oldDir, "same.go") || req.Owner.Generation != 1 || req.Owner.Iteration != 2 || req.Owner.Attempt != 0 {
		t.Fatalf("old selection=%+v", req)
	}
	m, prepare := m.admitOpenLocation(req)
	m, launch := m.preparedOpenLocation(prepare().(openerPreparedMsg))
	if launch == nil {
		t.Fatal(m.opener.notice)
	}
	m, _ = m.completeOpenLocation(launch().(shared.OpenLocationResult))
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []string{filepath.Join(oldDir, "same.go")}) {
		t.Fatalf("old opener argv=%q", args)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(oldDir); err != nil {
		t.Fatal(err)
	}
	m, prepare = m.admitOpenLocation(req)
	m, launch = m.preparedOpenLocation(prepare().(openerPreparedMsg))
	if launch != nil || !strings.Contains(m.opener.notice, "unavailable") {
		t.Fatal("removed view did not fail closed")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("launched same-name decoy")
	}
	m.monitor, _ = m.monitor.Update(makeKey("n"))
	_, cmd = m.monitor.Update(makeKey("e"))
	req = cmd().(shared.OpenLocationRequest)
	if req.Target.Path != filepath.Join(newDir, "same.go") || req.Owner.Attempt != 1 {
		t.Fatalf("new selection=%+v", req)
	}
}
