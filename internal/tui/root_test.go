package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSelectToDetailFlow drives the root model without a terminal: discover a
// workflow, render the picker, press Enter, and render the detail screen —
// asserting the visible content at each step.
func TestSelectToDetailFlow(t *testing.T) {
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

	var m tea.Model = New(context.Background(), true)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(discoverWorkflowsCmd(dir)())

	if view := m.View(); !strings.Contains(view, "mini") {
		t.Fatalf("selector view missing workflow name:\n%s", view)
	}

	// Enter emits a showDetailMsg via its command; run it and deliver it.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if _, ok := cmd().(showDetailMsg); !ok {
		t.Fatalf("enter did not produce showDetailMsg, got %T", cmd())
	}
	m, _ = m.Update(showDetailMsg{path: path})

	// The detail screen loads asynchronously; deliver the load result directly.
	m, _ = m.Update(loadWorkflowCmd(path)())
	view := m.View()
	for _, want := range []string{"mini", "valid", "hello", "command"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view missing %q:\n%s", want, view)
		}
	}

	// esc returns to the picker.
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(backToSelectorMsg); !ok {
		t.Fatalf("esc did not produce backToSelectorMsg, got %T", cmd())
	}
	m, _ = m.Update(backToSelectorMsg{})
	if view := m.View(); !strings.Contains(view, "mini") {
		t.Fatalf("did not return to selector:\n%s", view)
	}
}

// TestDetailShowsValidationError confirms an invalid workflow still lists and
// its detail view surfaces the validation failure rather than crashing.
func TestDetailShowsValidationError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.toml")
	os.WriteFile(path, []byte(`
[workflow]
name = "broken"

[[step]]
id = "oops"
type = "command"
`), 0o644)

	m := newDetailModel(path)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(loadWorkflowCmd(path)())

	view := m.View()
	if !strings.Contains(view, "broken") || !strings.Contains(view, "invalid") {
		t.Fatalf("expected invalid-workflow detail view:\n%s", view)
	}
}
