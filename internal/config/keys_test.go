package config

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestKeysAcceptsStringOrList(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user.toml", `[keys]
"monitor.copy_item" = "ctrl+y"
"global.palette" = ["ctrl+k", "ctrl+p"]
`)

	cfg, err := Load(userPath, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Keys["monitor.copy_item"]; !slices.Equal(got, KeyChords{"ctrl+y"}) {
		t.Fatalf("monitor.copy_item = %v, want [ctrl+y]", got)
	}
	if got := cfg.Keys["global.palette"]; !slices.Equal(got, KeyChords{"ctrl+k", "ctrl+p"}) {
		t.Fatalf("global.palette = %v, want [ctrl+k ctrl+p]", got)
	}
}

func TestKeysProjectMergesPerAction(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user.toml", "[keys]\n\"monitor.copy_item\" = \"ctrl+y\"\n\"runs.delete\" = \"D\"\n")
	projectRoot := filepath.Join(dir, "project", ".jig")
	writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), "[keys]\n\"runs.delete\" = \"X\"\n")

	cfg, err := Load(userPath, projectRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string][]string{"monitor.copy_item": {"ctrl+y"}, "runs.delete": {"X"}}
	got := KeyOverrides(cfg.Keys)
	if len(got) != len(want) {
		t.Fatalf("overrides = %v, want %v", got, want)
	}
	for id, chords := range want {
		if !slices.Equal(got[id], chords) {
			t.Fatalf("%s = %v, want %v", id, got[id], chords)
		}
	}
}

func TestKeysRejectsMalformedValues(t *testing.T) {
	for name, body := range map[string]string{
		"empty list":   `"runs.delete" = []`,
		"empty chord":  `"runs.delete" = ""`,
		"non-string":   `"runs.delete" = [1]`,
		"number value": `"runs.delete" = 3`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfigFile(t, t.TempDir(), "user.toml", "[keys]\n"+body+"\n")
			_, err := Load(path, "")
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("Load error = %v, want ErrConfigInvalid", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q does not name the file", err)
			}
		})
	}
}
