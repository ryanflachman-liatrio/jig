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
	"jig/internal/tui/detail"
	"jig/internal/tui/selector"
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
	m, _ = m.Update(selector.DiscoverCmd(dir)())

	if view := m.View().Content; !strings.Contains(view, "mini") {
		t.Fatalf("selector view missing workflow name:\n%s", view)
	}

	// Enter emits a showDetailMsg via its command; run it and deliver it.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if _, ok := cmd().(selector.ShowDetailMsg); !ok {
		t.Fatalf("enter did not produce selector.ShowDetailMsg, got %T", cmd())
	}
	m, _ = m.Update(selector.ShowDetailMsg{Path: path})

	// The detail screen loads asynchronously; deliver the load result directly.
	m, _ = m.Update(detail.New(path).Init()())
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
	if _, ok := cmd().(detail.BackMsg); !ok {
		t.Fatalf("esc did not produce detail.BackMsg, got %T", cmd())
	}
	m, _ = m.Update(detail.BackMsg{})
	if view := m.View().Content; !strings.Contains(view, "mini") {
		t.Fatalf("did not return to selector:\n%s", view)
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
	m, _ = m.Update(selector.DiscoverCmd(dir)())

	// "?" opens the overlay over the selector.
	m, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	view := m.View().Content
	for _, want := range []string{"jig · help", "Workflows", "Global", "?/F1/esc close"} {
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

	// While filtering, "?" remains text and F1 owns the global help chord.
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
	out := shared.RenderHelpOverlay(base, w, h, sections, 0)

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
