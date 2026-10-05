package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTUIBuiltInDefaults(t *testing.T) {
	// Both built-in defaults are on: simple_mode = true and
	// compact_tool_groups = true (Spec 06: burst folding is default-on).
	var zero TUIConfig
	if !zero.SimpleModeOrDefault() {
		t.Fatal("SimpleModeOrDefault() with unset [tui] = false, want true")
	}
	if !zero.CompactToolGroupsOrDefault() {
		t.Fatal("CompactToolGroupsOrDefault() with unset [tui] = false, want true")
	}
}

func TestTUIProjectOverridesUser(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user.toml", "[tui]\ncompact_tool_groups = true\n")
	projectRoot := filepath.Join(dir, "project", ".jig")
	writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), "[tui]\ncompact_tool_groups = false\nsimple_mode = false\n")

	cfg, err := Load(userPath, projectRoot)
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if cfg.TUI.CompactToolGroupsOrDefault() {
		t.Fatal("project's explicit compact_tool_groups=false did not override user's true")
	}
	if cfg.TUI.SimpleModeOrDefault() {
		t.Fatal("project's explicit simple_mode=false did not override the built-in true default")
	}
}

func TestTUIUserOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user.toml", "[tui]\ncompact_tool_groups = false\n")

	cfg, err := Load(userPath, filepath.Join(dir, "project", ".jig"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if cfg.TUI.CompactToolGroupsOrDefault() {
		t.Fatal("user config's compact_tool_groups=false did not override the built-in true default")
	}
	if !cfg.TUI.SimpleModeOrDefault() {
		t.Fatal("simple_mode unset anywhere should still resolve to the built-in true default")
	}
}

func TestTUIBellOrDefault(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name string
		cfg  TUIConfig
		want bool
	}{
		{"unset defaults to off", TUIConfig{}, false},
		{"explicit true", TUIConfig{Bell: &on}, true},
		{"explicit false", TUIConfig{Bell: &off}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.BellOrDefault(); got != tt.want {
				t.Fatalf("BellOrDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTUIBellLoad(t *testing.T) {
	tests := []struct {
		name    string
		user    string
		project string
		want    bool
		wantErr bool
	}{
		{name: "unset anywhere", want: false},
		{name: "user true", user: "[tui]\nbell = true\n", want: true},
		{name: "user true, project unset", user: "[tui]\nbell = true\n", project: "[tui]\nsimple_mode = true\n", want: true},
		{name: "project false overrides user true", user: "[tui]\nbell = true\n", project: "[tui]\nbell = false\n", want: false},
		{name: "non-boolean rejected", user: "[tui]\nbell = \"yes\"\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			userPath := ""
			if tt.user != "" {
				userPath = writeConfigFile(t, dir, "user.toml", tt.user)
			}
			projectRoot := filepath.Join(dir, "project", ".jig")
			if tt.project != "" {
				writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), tt.project)
			}

			cfg, err := Load(userPath, projectRoot)
			if tt.wantErr {
				if !errors.Is(err, ErrConfigInvalid) {
					t.Fatalf("Load: err = %v, want errors.Is(err, ErrConfigInvalid)", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: unexpected error %v", err)
			}
			if got := cfg.TUI.BellOrDefault(); got != tt.want {
				t.Fatalf("BellOrDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}
