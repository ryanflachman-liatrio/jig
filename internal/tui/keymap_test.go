package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/tui/monitor"
	"github.com/ryanflachman-liatrio/jig/internal/tui/palette"
	"github.com/ryanflachman-liatrio/jig/internal/tui/runs"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// withKeymap installs overrides for one test and restores the built-in
// keymap, including the global chords ConfigureKeymap rewrites in place.
func withKeymap(t *testing.T, overrides map[string][]string) {
	t.Helper()
	saved := []keybind.Binding{shared.KeyQuit, shared.KeyHelp, shared.KeyHelpTyping, shared.KeyPalette, shared.KeyNotificationDiagnostics}
	t.Cleanup(func() {
		shared.KeyQuit, shared.KeyHelp, shared.KeyHelpTyping, shared.KeyPalette, shared.KeyNotificationDiagnostics =
			saved[0], saved[1], saved[2], saved[3], saved[4]
		shared.SetKeyOverrides(nil)
	})
	if err := ConfigureKeymap(overrides); err != nil {
		t.Fatalf("ConfigureKeymap: %v", err)
	}
}

func TestBuiltInKeymapHasNoConflicts(t *testing.T) {
	if err := shared.ValidateKeymap(Actions(), nil); err != nil {
		t.Fatalf("built-in keymap: %v", err)
	}
}

func TestActionIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Actions() {
		if seen[a.ID] {
			t.Fatalf("duplicate action ID %q", a.ID)
		}
		seen[a.ID] = true
	}
}

func TestConfigureKeymapRejectsBadOverrides(t *testing.T) {
	for name, tc := range map[string]struct {
		overrides map[string][]string
		want      string
	}{
		"unknown action":  {map[string][]string{"monitor.nope": {"x"}}, `unknown action "monitor.nope"`},
		"fixed action":    {map[string][]string{"monitor.scroll": {"x"}}, `"monitor.scroll" cannot be remapped`},
		"empty list":      {map[string][]string{"runs.delete": {}}, "needs at least one key"},
		"whitespace":      {map[string][]string{"runs.delete": {"ctrl x"}}, "invalid key"},
		"same region":     {map[string][]string{"monitor.copy_item": {"j"}}, `"j" is bound to both`},
		"parent region":   {map[string][]string{"monitor.toggle_simple": {"y"}}, `"y" is bound to both`},
		"global shadow":   {map[string][]string{"runs.delete": {"ctrl+c"}}, `"ctrl+c" is bound to both`},
		"both remapped":   {map[string][]string{"runs.delete": {"Z"}, "runs.copy_id": {"Z"}}, `"Z" is bound to both`},
		"fixed list keys": {map[string][]string{"selector.open": {"/"}}, `"/" is bound to both`},
		"opener key":      {map[string][]string{"monitor.copy_item": {"e"}}, `"e" is bound to both`},
		"source key":      {map[string][]string{"monitor.copy_item": {"v"}}, `"v" is bound to both`},
		"source window":   {map[string][]string{"monitor.copy_item": {"alt+j"}}, `"alt+j" is bound to both`},
		"picker confirm":  {map[string][]string{"monitor.down": {"enter"}}, `"enter" is bound to both`},
	} {
		t.Run(name, func(t *testing.T) {
			err := ConfigureKeymap(tc.overrides)
			t.Cleanup(func() { shared.SetKeyOverrides(nil) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ConfigureKeymap error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestConfigureKeymapAllowsKeysInSeparateRegions(t *testing.T) {
	// y copies in the Transcript and approves in the final-merge gate; a
	// Steps-panel action may take it too because the regions never overlap.
	withKeymap(t, map[string][]string{"monitor.stop_step": {"y"}})
}

func TestRemapReachesHandlerFooterAndPalette(t *testing.T) {
	withKeymap(t, map[string][]string{"runs.delete": {"X"}, "global.palette": {"ctrl+p"}})

	keys := runs.NewModel().Keys()
	if !keybind.Matches(palette.ParseKey("X"), keys.Delete) {
		t.Fatal("remapped runs.delete does not match X")
	}
	if keybind.Matches(palette.ParseKey("d"), keys.Delete) {
		t.Fatal("remapped runs.delete still matches its default d")
	}
	if got := keys.Delete.Help(); got.Key != "X" || got.Desc != "delete" {
		t.Fatalf("runs.delete help = %+v, want X delete", got)
	}
	cmds := palette.FromBindings("Runs", []keybind.Binding{keys.Delete})
	if len(cmds) != 1 || cmds[0].Key != "X" || cmds[0].Binding != "X" {
		t.Fatalf("palette command = %+v, want key and label X", cmds)
	}
	if !keybind.Matches(palette.ParseKey("ctrl+p"), shared.KeyPalette) {
		t.Fatal("remapped global.palette does not match ctrl+p")
	}
}

func TestRemapKeepsContextualLabels(t *testing.T) {
	withKeymap(t, map[string][]string{"monitor.toggle_bell": {"ctrl+b"}})

	hint := ""
	for _, sec := range monitor.New("run-1").PaletteSections() {
		hint += shared.HintString(sec.Bindings...)
	}
	if !strings.Contains(hint, "ctrl+b bell: off") {
		t.Fatalf("monitor sections %q do not show the remapped bell toggle", hint)
	}
	if strings.Contains(hint, "B bell") {
		t.Fatalf("monitor sections %q still show the default bell key", hint)
	}
}

func TestRemapRecordedEvidenceActions(t *testing.T) {
	withKeymap(t, map[string][]string{
		"monitor.open_location": {"E"},
		"monitor.source_mode":   {"V"},
		"monitor.source_last":   {"alt+e"},
	})
	m, _ := newOpenerFixture(t, t.Context(), "windows")
	before := m.monitor.View()
	m.monitor, _ = m.monitor.Update(palette.ParseKey("v"))
	if m.monitor.View() != before {
		t.Fatal("default source binding remained active after remap")
	}
	m.monitor, _ = m.monitor.Update(palette.ParseKey("V"))
	if !strings.Contains(ansi.Strip(m.monitor.View()), "FIRST recorded source") {
		t.Fatal("remapped mode action did not show recorded source")
	}
	foundOpener := false
	for _, command := range m.paletteCommands() {
		switch command.Title {
		case "open file in editor":
			foundOpener = true
			if command.Key != "E" {
				t.Fatalf("opener palette key = %q", command.Key)
			}
		}
	}
	if !foundOpener {
		t.Fatal("remapped opener missing from palette")
	}
	m.palette = m.palette.Show(m.paletteCommands())
	next, _ := m.Update(tea.KeyPressMsg{Code: 'l', Text: "last recorded source window"})
	m = next.(rootModel)
	selected, ok := m.palette.Selected()
	if !ok || selected.Key != "alt+e" {
		t.Fatalf("remapped palette action = %+v", selected)
	}
	next, cmd := m.Update(palette.ParseKey("enter"))
	if cmd == nil {
		t.Fatal("palette did not dispatch remapped source action")
	}
	next, _ = next.Update(cmd())
	m = next.(rootModel)
	if !strings.Contains(ansi.Strip(m.monitor.View()), "FINAL without newline") {
		t.Fatal("remapped palette action did not reach final source window")
	}
	_, request := m.monitor.Update(palette.ParseKey("E"))
	if request == nil {
		t.Fatal("remapped opener did not emit a request")
	}
	if req, ok := request().(shared.OpenLocationRequest); !ok || req.Target.Path == "" || req.Unavailable != "" {
		t.Fatalf("remapped opener request = %+v", req)
	}
}

// TestKeymapDocumentationListsRemappableActions keeps docs/TUI.md's action
// table in step with the registry operators remap against.
func TestKeymapDocumentationListsRemappableActions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "TUI.md"))
	if err != nil {
		t.Fatalf("read docs/TUI.md: %v", err)
	}
	doc := string(data)
	for _, a := range Actions() {
		listed := strings.Contains(doc, "`"+a.ID+"`")
		if !a.Fixed && !listed {
			t.Errorf("docs/TUI.md does not list remappable action %q", a.ID)
		}
		if a.Fixed && listed {
			t.Errorf("docs/TUI.md lists fixed action %q as remappable", a.ID)
		}
	}
}
