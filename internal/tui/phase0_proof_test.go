package tui_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/engine"
	"jig/internal/runner"
	"jig/internal/tui"
	"jig/internal/tui/monitor"
	"jig/internal/tui/selector"
)

// TestPhase0HomeProofArtifact writes a stripped Home frame for walkthrough
// evidence (workflows + runs dual-pane, local empty CTA).
func TestPhase0HomeProofArtifact(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"feature", "hotfix"} {
		body := "[workflow]\nname = \"" + name + "\"\nversion = \"1\"\ndescription = \"" + name + " demo\"\n\n[[step]]\nid = \"hello\"\ntype = \"command\"\nrun = \"echo hi\"\n"
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, "")
	var m tea.Model = tui.New(context.Background(), mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, cmd := m.Update(selector.DiscoverCmd(dir)())
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m, _ = m.Update(msg)
		}
	}
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "20260905-203000-ab12cd34", Workflow: "feature", Steps: []string{"a", "b", "c"},
	}})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.StepStatus{
		RunID: "20260905-203000-ab12cd34", StepID: "a", To: "succeeded",
	}})

	plain := ansi.Strip(m.View().Content)
	outDir := filepath.Join(t.TempDir(), "artifacts")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(outDir, "phase0_home_frame.txt")
	if err := os.WriteFile(path, []byte(plain+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Workflows", "Runs · feature", "ab12cd34", "feature"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("proof frame missing %q:\n%s", want, plain)
		}
	}
	t.Logf("wrote %s", path)
}
