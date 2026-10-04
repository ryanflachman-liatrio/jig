package config

import (
	"errors"
	"reflect"
	"testing"
)

func TestOpenerConfigLayers(t *testing.T) {
	clearTelemetryEnv(t)
	dir := t.TempDir()
	user := writeConfigFile(t, dir, "user.toml", "[tui.opener]\nargv = ['editor', '+{line}', '{path}']\nterminal = false\n")
	project := t.TempDir()
	for _, tc := range []struct {
		name, overlay string
		argv          []string
		terminal      bool
	}{
		{"user", "", []string{"editor", "+{line}", "{path}"}, false},
		{"terminal only", "terminal = true", []string{"editor", "+{line}", "{path}"}, true},
		{"replacement", "argv = ['gui', '{path}:{line}:{column}']", []string{"gui", "{path}:{line}:{column}"}, false},
		{"empty fallback", "argv = []", []string{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeConfigFile(t, project, FileName, "[tui.opener]\n"+tc.overlay)
			got, err := Load(user, project)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.TUI.Opener.Argv, tc.argv) || got.TUI.Opener.TerminalOrDefault() != tc.terminal {
				t.Fatalf("unexpected merged opener: %+v", got.TUI.Opener)
			}
		})
	}
	got, err := Load(dir+"/missing", "")
	if err != nil || got.TUI.Opener.Argv != nil || !got.TUI.Opener.TerminalOrDefault() {
		t.Fatalf("defaults: %+v, %v", got.TUI.Opener, err)
	}
	base := Config{TUI: TUIConfig{Opener: OpenerConfig{Argv: []string{"editor", "{path}"}}}}
	out := Merge(base, Config{})
	out.TUI.Opener.Argv[0] = "changed"
	if base.TUI.Opener.Argv[0] != "editor" {
		t.Fatal("merge aliases argv")
	}
}

func TestOpenerConfigValidation(t *testing.T) {
	for _, body := range []string{
		`argv = 'PRIVATE'`, `argv = [4]`, `terminal = 'PRIVATE'`,
		`argv = ['', '{path}']`, `argv = ['   ', '{path}']`,
		`argv = ['editor', '{PRIVATE}']`, `argv = ['editor', '{line}']`,
		`argv = ['{path}', '{path}']`, `argv = ['editor{line}', '{path}']`,
		`argv = ['editor', '{path', 'PRIVATE']`, `argv = ["editor", "{path}\u0000PRIVATE"]`,
	} {
		t.Run(body, func(t *testing.T) {
			path := writeConfigFile(t, t.TempDir(), FileName, "[tui.opener]\n"+body)
			_, err := loadFile(path)
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("want invalid, got %v", err)
			}
			if err.Error() != ErrConfigInvalid.Error()+": "+path {
				t.Fatal("error discloses contents")
			}
		})
	}
}
