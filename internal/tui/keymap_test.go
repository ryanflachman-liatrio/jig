package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"

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
