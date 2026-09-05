package palette_test

import (
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/tui/palette"
)

func TestPaletteFilterAndEsc(t *testing.T) {
	m := palette.New().Show([]palette.Command{
		{ID: "1", Title: "reset step", Binding: "r", Key: "r", Enabled: true},
		{ID: "2", Title: "resume step", Binding: "ctrl+r", Key: "ctrl+r", Enabled: true},
		{ID: "3", Title: "switch run", Binding: "", Key: "q", Enabled: true},
		{ID: "x", Title: "hidden", Binding: "x", Key: "x", Enabled: false},
	})
	if !m.Open() {
		t.Fatal("expected open")
	}
	view := m.View("base", 80, 24)
	if !strings.Contains(view, "Commands") || !strings.Contains(view, "reset step") {
		t.Fatalf("palette view missing commands:\n%s", view)
	}
	if strings.Contains(view, "hidden") {
		t.Fatal("disabled command should be omitted")
	}

	m, _, _ = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m, _, _ = m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	m, _, _ = m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	sel, ok := m.Selected()
	if !ok || sel.Title != "reset step" {
		t.Fatalf("filter selection = %+v ok=%v", sel, ok)
	}

	m, cmd, consumed := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !consumed || cmd != nil || m.Open() {
		t.Fatalf("esc should close without side effects: open=%v cmd=%v", m.Open(), cmd)
	}
}

func TestPaletteEnterDispatchesKey(t *testing.T) {
	m := palette.New().Show([]palette.Command{
		{ID: "1", Title: "reset step", Binding: "r", Key: "r", Enabled: true},
	})
	m, cmd, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Open() {
		t.Fatal("enter should close palette")
	}
	if cmd == nil {
		t.Fatal("enter should dispatch key cmd")
	}
	msg := cmd()
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || k.String() != "r" {
		t.Fatalf("dispatched msg = %#v", msg)
	}
}

func TestFromBindingsSkipsDisabled(t *testing.T) {
	enabled := keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "reset"))
	disabled := keybind.NewBinding(keybind.WithKeys("s"), keybind.WithHelp("s", "stop"))
	disabled.SetEnabled(false)
	cmds := palette.FromBindings("Steps", []keybind.Binding{enabled, disabled})
	if len(cmds) != 1 || cmds[0].Title != "reset" || cmds[0].Key != "r" {
		t.Fatalf("FromBindings = %+v", cmds)
	}
}

func TestParseKey(t *testing.T) {
	got := palette.ParseKey("ctrl+r")
	if got.Code != 'r' || !got.Mod.Contains(tea.ModCtrl) {
		t.Fatalf("ctrl+r = %#v", got)
	}
	if got := palette.ParseKey("esc"); got.Code != tea.KeyEsc {
		t.Fatalf("esc = %#v", got)
	}
}
