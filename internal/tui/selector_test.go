package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// TestDiscoverWorkflows verifies the startup scan: it walks the tree, keeps only
// files with a [workflow] name, sorts by name, and returns an empty (non-error)
// result when the directory is absent.
func TestDiscoverWorkflows(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Two real workflows (one nested), one non-workflow .toml, one non-toml.
	write("zebra.toml", "[workflow]\nname = \"zebra\"\ndescription = \"z\"\n")
	write("nested/alpha.toml", "[workflow]\nname = \"alpha\"\n")
	write("notes.txt", "[workflow]\nname = \"ignored\"\n")
	write("config.toml", "[defaults]\nmodel = \"claude\"\n")

	msg, ok := discoverWorkflowsCmd(dir)().(workflowsLoadedMsg)
	if !ok {
		t.Fatalf("expected workflowsLoadedMsg")
	}
	if msg.err != nil {
		t.Fatalf("unexpected err: %v", msg.err)
	}
	if len(msg.items) != 2 {
		t.Fatalf("got %d items, want 2", len(msg.items))
	}
	// Sorted by name: alpha before zebra.
	if got := msg.items[0].(workflowItem).name; got != "alpha" {
		t.Fatalf("items[0] = %q, want alpha", got)
	}
	if got := msg.items[1].(workflowItem).name; got != "zebra" {
		t.Fatalf("items[1] = %q, want zebra", got)
	}

	// A missing directory is an empty result, not an error.
	msg, _ = discoverWorkflowsCmd(filepath.Join(dir, "does-not-exist"))().(workflowsLoadedMsg)
	if msg.err != nil || len(msg.items) != 0 {
		t.Fatalf("missing dir: got err=%v items=%d, want nil/0", msg.err, len(msg.items))
	}
}

// TestSelector verifies the selector renders inside a "Workflows" panel with the
// bubbles-list internal title/help chrome stripped (the panel border and the
// external footer now supply those).
func TestSelector(t *testing.T) {
	m := newSelectorModel()
	m, _ = m.Update(workflowsLoadedMsg{items: []list.Item{
		workflowItem{name: "alpha", desc: "first", path: "a.toml"},
		workflowItem{name: "beta", desc: "second", path: "b.toml"},
	}})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})

	// Chrome stripped: the list supplies neither its own title nor help line.
	if m.list.ShowTitle() {
		t.Error("selector list title chrome should be disabled")
	}
	if m.list.ShowHelp() {
		t.Error("selector list help chrome should be disabled")
	}

	firstLine := strings.SplitN(m.View(), "\n", 2)[0]
	if !strings.Contains(firstLine, "Workflows") {
		t.Errorf("top edge %q missing panel title \"Workflows\"", firstLine)
	}
	if !strings.Contains(firstLine, "╭") {
		t.Errorf("top edge %q missing rounded corner", firstLine)
	}
}
