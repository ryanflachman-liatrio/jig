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

func TestPaletteEnterInvokesRunWhenSet(t *testing.T) {
	var ran bool
	m := palette.New().Show([]palette.Command{
		{ID: "1", Title: "go to home", Key: "should-not-fire", Enabled: true, Run: func() tea.Cmd {
			return func() tea.Msg {
				ran = true
				return "ran"
			}
		}},
	})
	m, cmd, consumed := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !consumed || m.Open() {
		t.Fatalf("enter should close palette: consumed=%v open=%v", consumed, m.Open())
	}
	if cmd == nil {
		t.Fatal("enter should return the command's Run cmd")
	}
	msg := cmd()
	if msg != "ran" || !ran {
		t.Fatalf("Run was not invoked: msg=%v ran=%v", msg, ran)
	}
}

func TestPaletteEnterFallsBackToDispatchKeyWhenRunNil(t *testing.T) {
	m := palette.New().Show([]palette.Command{
		{ID: "1", Title: "reset step", Binding: "r", Key: "r", Enabled: true},
	})
	_, cmd, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected DispatchKey fallback cmd")
	}
	msg := cmd()
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || k.String() != "r" {
		t.Fatalf("dispatched msg = %#v, want key-redispatch of 'r'", msg)
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

func TestRefilterRanksByFuzzyScore(t *testing.T) {
	// "swr" is a subsequence of "switch run" but not the catalog's first entry;
	// fuzzy ranking must surface it as the best (first) match, not catalog order.
	m := palette.New().Show([]palette.Command{
		{ID: "1", Title: "reset step", Binding: "r", Key: "r", Enabled: true},
		{ID: "2", Title: "resume step", Binding: "ctrl+r", Key: "ctrl+r", Enabled: true},
		{ID: "3", Title: "switch run", Binding: "", Key: "q", Enabled: true},
	})
	for _, r := range "swr" {
		m, _, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	sel, ok := m.Selected()
	if !ok || sel.Title != "switch run" {
		t.Fatalf("top fuzzy match = %+v ok=%v, want %q first", sel, ok, "switch run")
	}
}

func TestRefilterMatchesCategory(t *testing.T) {
	cmds := []palette.Command{
		{ID: "1", Title: "open transcript", Binding: "enter", Key: "enter", Category: "Steps", Enabled: true},
		{ID: "2", Title: "leave", Binding: "esc", Key: "esc", Category: "Global", Enabled: true},
	}
	m := palette.New().Show(cmds)
	for _, r := range "Steps" {
		m, _, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	sel, ok := m.Selected()
	if !ok || sel.ID != "1" {
		t.Fatalf("category-only query selected = %+v ok=%v, want command 1 (category Steps)", sel, ok)
	}
}

func TestRefilterEmptyFallsBackToCatalogOrder(t *testing.T) {
	cmds := []palette.Command{
		{ID: "1", Title: "zzz first in catalog", Binding: "", Key: "a", Enabled: true},
		{ID: "2", Title: "aaa second in catalog", Binding: "", Key: "b", Enabled: true},
	}
	m := palette.New().Show(cmds)
	view := m.View("base", 80, 24)
	zi := strings.Index(view, "zzz first")
	ai := strings.Index(view, "aaa second")
	if zi < 0 || ai < 0 || zi > ai {
		t.Fatalf("empty filter should preserve catalog order, view:\n%s", view)
	}
}

func TestRefilterExcludesDisabledRegardlessOfScore(t *testing.T) {
	cmds := []palette.Command{
		{ID: "1", Title: "reset step", Binding: "r", Key: "r", Enabled: true},
		{ID: "2", Title: "reset all", Binding: "R", Key: "R", Enabled: false},
	}
	m := palette.New().Show(cmds)
	for _, r := range "reset" {
		m, _, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	view := m.View("base", 80, 24)
	if strings.Contains(view, "reset all") {
		t.Fatalf("disabled command should stay excluded even with a high-scoring query:\n%s", view)
	}
}

func TestHighlightMatchesWrapsGivenIndexes(t *testing.T) {
	out := palette.ExportHighlightMatches("reset step", []int{0, 1})
	if ansiStrip(out) != "reset step" {
		t.Fatalf("highlightMatches changed the rendered text, got %q", ansiStrip(out))
	}
	if out == "reset step" {
		t.Fatal("expected ANSI styling to be applied to matched indexes")
	}
}

// ansiStrip removes SGR escape sequences for a simple suffix sanity check.
func ansiStrip(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
