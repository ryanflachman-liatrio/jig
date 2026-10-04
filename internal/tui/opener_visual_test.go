package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/monitor"
)

type openerSmokeModel struct{ rootModel }

func (m openerSmokeModel) Init() tea.Cmd { return nil }

// Opt-in real terminal fixture; no workflow, backend login, or real run data.
// Build with go test -c ./internal/tui -o /tmp/jig-opener-smoke, then run
// JIG_SDD26_SMOKE=terminal /tmp/jig-opener-smoke -test.run '^TestOpenLocationSmoke$'
// Modes: terminal, gui, missing, grouped, source, windows. Press e or ctrl+k, "open file", Enter; ctrl+c exits.
func TestOpenLocationSmoke(t *testing.T) {
	mode := os.Getenv("JIG_SDD26_SMOKE")
	if mode == "" {
		t.Skip("set JIG_SDD26_SMOKE=terminal, gui, missing, grouped, source, or windows in a terminal")
	}
	if mode != "terminal" && mode != "gui" && mode != "missing" && mode != "grouped" && mode != "source" && mode != "windows" {
		t.Fatal("unknown smoke mode")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m, root := newOpenerFixture(t, ctx, mode)
	if _, err := tea.NewProgram(openerSmokeModel{m}, tea.WithContext(ctx)).Run(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "received.json")); err == nil {
		fmt.Printf("\nReceived argv: %s\n", data)
	}
}

func newOpenerFixture(t *testing.T, ctx context.Context, mode string) (rootModel, string) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "雪 sample:1.go")
	if err := os.WriteFile(target, []byte("package synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "run")
	path := datastore.TranscriptPath(runDir, "edit")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writer, err := transcript.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	line := 3
	entries := []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "synthetic-edit", Kind: "edit", Title: "Synthetic edit", Locations: []toolcall.Location{{Path: target, Line: &line}}}}}},
		{Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "synthetic-edit", Kind: "edit", Status: "completed", Content: []toolcall.Content{{Diff: &toolcall.Diff{Path: target, NewText: "package synthetic\n"}}}}}}},
	}
	if mode == "grouped" {
		entries = groupedOpenerEntries(t, root)
	}
	if mode == "source" {
		entries = sourceDemoEntries()
	}
	if mode == "windows" {
		old := "package synthetic\nvar value = 1\n"
		text := "FIRST recorded source\n\n" + strings.Repeat("// unchanged synthetic context\n", 25) + "MIDDLE recorded source\n" + strings.Repeat("雪é👩‍💻", 900) + "\n\nFINAL without newline"
		entries[1].Blocks[0].Tool.Content[0].Diff.OldText = &old
		entries[1].Blocks[0].Tool.Content[0].Diff.NewText = text
	}
	for _, entry := range entries {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	terminal := mode == "terminal" || mode == "windows"
	argv := append(openerHelperArgv(t, "success", filepath.Join(root, "received.json")), "{path}", "{line}:{column}")
	if terminal {
		argv = append(argv, "interactive")
	}
	if mode == "missing" {
		argv[0] = filepath.Join(root, "missing-editor")
	}
	mgr := engine.NewManager(nil, root)
	m := New(ctx, mgr, WithTUIPrefs(config.TUIConfig{Opener: config.OpenerConfig{Argv: argv, Terminal: &terminal}})).(rootModel)
	m.active = screenMonitor
	m.monitor = monitor.New("synthetic-run")
	m.monitor.RunDir = runDir
	m.monitor = m.monitor.WithSnapshot(engine.RunSnapshot{ID: "synthetic-run", Workflow: "Opener smoke", Done: true, Steps: []step.State{{ID: "edit", Status: step.StatusSucceeded}}})
	m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.monitor, _ = m.monitor.Update(monitor.EngineEventMsg{Event: engine.StepMessage{RunID: "synthetic-run", StepID: "edit", Seq: len(entries)}})
	m.monitor, _ = m.monitor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if mode == "grouped" || mode == "source" {
		m.monitor, _ = m.monitor.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	}
	return m, root
}

func openerInteractive(args []string) {
	if len(args) > 0 && args[len(args)-1] == "interactive" {
		fmt.Printf("\nSynthetic terminal editor received argv: %q\nPress Enter to return to Monitor.\n", args)
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

// The grouped scene mixes loaded reads, compact edits, a removed file and an
// old relative target with no origin. It never touches an operator checkout.
func groupedOpenerEntries(t *testing.T, root string) []transcript.Entry {
	t.Helper()
	var entries []transcript.Entry
	for i, spec := range []struct {
		kind, name, origin string
		exists             bool
	}{
		{"read", "first.go", root, true},
		{"read", "removed.go", root, false},
		{"read", "historical.go", "", false},
		{"edit", "first.go", root, true},
		{"edit", "last.go", root, true},
	} {
		if spec.exists {
			if err := os.WriteFile(filepath.Join(root, spec.name), []byte("synthetic"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		input, _ := json.Marshal(map[string]string{"path": spec.name})
		line := i + 3
		use := &toolcall.Activity{ID: fmt.Sprintf("group-%d", i), Kind: spec.kind, Title: spec.kind, Input: input, Locations: []toolcall.Location{{Path: spec.name, Line: &line}}}
		result := &toolcall.Activity{ID: use.ID, Kind: spec.kind, Status: "completed"}
		entries = append(entries,
			transcript.Entry{ExecutionDir: spec.origin, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: use}}},
			transcript.Entry{ExecutionDir: spec.origin, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: result}}},
		)
	}
	return entries
}

func sourceDemoEntries() []transcript.Entry {
	old := "package synthetic\n\nvar value = 1\n// unchanged context\n"
	first := &toolcall.Activity{ID: "multi-edit", Kind: "edit", Status: "completed", Content: []toolcall.Content{
		{Diff: &toolcall.Diff{Path: "first.go", OldText: &old, NewText: "package synthetic\n\nvar value = 2\n// unchanged context\n"}},
		{Diff: &toolcall.Diff{Path: "context.go", OldText: &old, NewText: "package synthetic\n\nvar value = 3\n// recorded unchanged context\n"}},
	}}
	empty := &toolcall.Activity{ID: "empty-edit", Kind: "edit", Status: "completed", Content: []toolcall.Content{{Diff: &toolcall.Diff{Path: "empty.go", NewText: ""}}}}
	var entries []transcript.Entry
	for i, a := range []*toolcall.Activity{first, empty} {
		entries = append(entries,
			transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: a.ID, Kind: "edit", Title: "Synthetic recorded edit"}}}},
			transcript.Entry{Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: a, Truncated: i == 1}}},
		)
	}
	return entries
}
