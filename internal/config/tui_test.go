package config

import (
	"path/filepath"
	"testing"
)

func TestTUIDefaultsMatchPrefsDefault(t *testing.T) {
	// Matches internal/tui/prefs.Default()'s retired asymmetric pair
	// exactly: simple_mode = true, compact_tool_groups = false.
	var zero TUIConfig
	if !zero.SimpleModeOrDefault() {
		t.Fatal("SimpleModeOrDefault() with unset [tui] = false, want true")
	}
	if zero.CompactToolGroupsOrDefault() {
		t.Fatal("CompactToolGroupsOrDefault() with unset [tui] = true, want false")
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
	userPath := writeConfigFile(t, dir, "user.toml", "[tui]\ncompact_tool_groups = true\n")

	cfg, err := Load(userPath, filepath.Join(dir, "project", ".jig"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if !cfg.TUI.CompactToolGroupsOrDefault() {
		t.Fatal("user config's compact_tool_groups=true did not override the built-in false default")
	}
	if !cfg.TUI.SimpleModeOrDefault() {
		t.Fatal("simple_mode unset anywhere should still resolve to the built-in true default")
	}
}
