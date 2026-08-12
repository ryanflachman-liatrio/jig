package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/runner"
	"jig/internal/tui/shared"
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

	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, "")
	var m tea.Model = New(context.Background(), mgr)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(discoverWorkflowsCmd(dir)())

	if view := m.View().Content; !strings.Contains(view, "mini") {
		t.Fatalf("selector view missing workflow name:\n%s", view)
	}

	// Enter emits a showDetailMsg via its command; run it and deliver it.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if _, ok := cmd().(showDetailMsg); !ok {
		t.Fatalf("enter did not produce showDetailMsg, got %T", cmd())
	}
	m, _ = m.Update(showDetailMsg{path: path})

	// The detail screen loads asynchronously; deliver the load result directly.
	m, _ = m.Update(loadWorkflowCmd(path)())
	view := m.View().Content
	for _, want := range []string{"mini", "valid", "hello", "command"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view missing %q:\n%s", want, view)
		}
	}

	// esc returns to the picker.
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(backToSelectorMsg); !ok {
		t.Fatalf("esc did not produce backToSelectorMsg, got %T", cmd())
	}
	m, _ = m.Update(backToSelectorMsg{})
	if view := m.View().Content; !strings.Contains(view, "mini") {
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

// TestHelpOverlayGlobal drives the real root model: "?" opens a modal on the
// selector, it renders the selector's sections plus Global, an unrelated key is
// swallowed (no navigation behind it), and "?"/esc dismiss it.
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
	m, _ = m.Update(discoverWorkflowsCmd(dir)())

	// "?" opens the overlay over the selector.
	m, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	view := m.View().Content
	for _, want := range []string{"jig · help", "Workflows", "Global", "? or esc to close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, view)
		}
	}
	// The modal is composited over the live screen (not replacing it): the
	// selector beneath must still show through around the centered box.
	if !strings.Contains(view, "mini") {
		t.Fatalf("expected the selector to show through beneath the modal:\n%s", view)
	}

	// An unrelated key is swallowed; the overlay stays up.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if !strings.Contains(m.View().Content, "jig · help") {
		t.Fatal("expected overlay to stay open on an unrelated key")
	}

	// "?" closes it and the selector returns.
	m, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if v := m.View().Content; strings.Contains(v, "jig · help") || !strings.Contains(v, "mini") {
		t.Fatalf("expected ? to close overlay and restore selector:\n%s", v)
	}

	// The shift+/ fallback also opens it, and esc closes it.
	m, _ = m.Update(tea.KeyPressMsg{Code: '/', Mod: tea.ModShift})
	if !strings.Contains(m.View().Content, "jig · help") {
		t.Fatal("expected shift+/ to open the overlay")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if strings.Contains(m.View().Content, "jig · help") {
		t.Fatal("expected esc to close the overlay")
	}
}

// TestHelpOverlayCompositesOverBase verifies renderHelpOverlay layers the modal
// on top of the base screen rather than replacing it: base cells the box does not
// cover survive in the output (lipgloss Canvas show-through).
func TestHelpOverlayCompositesOverBase(t *testing.T) {
	const w, h = 60, 20
	// A base filled edge-to-edge with a distinctive rune; whatever the centered
	// box does not cover must remain.
	row := strings.Repeat("X", w)
	rows := make([]string, h)
	for i := range rows {
		rows[i] = row
	}
	base := strings.Join(rows, "\n")

	sections := []shared.HelpSection{{Title: "Global", Bindings: []keybind.Binding{shared.KeyHelp, shared.KeyQuit}}}
	out := shared.RenderHelpOverlay(base, w, h, sections)

	if !strings.Contains(out, "jig · help") {
		t.Fatalf("modal content missing:\n%s", out)
	}
	if !strings.Contains(out, "X") {
		t.Fatalf("base did not show through the composite:\n%s", out)
	}
	// Top-left corner is outside the centered box, so it must still be a base cell.
	if first := strings.SplitN(out, "\n", 2)[0]; !strings.HasPrefix(first, "X") {
		t.Fatalf("expected base at top-left corner, got line: %q", first)
	}
}
