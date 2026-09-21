package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeConfigFile writes TOML content to name inside dir and returns the
// full path, creating parent directories as needed.
func writeConfigFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaultsOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-empty"))

	cfg, err := Load("", filepath.Join(dir, "project", ".jig"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("Load with no files present: got %+v, want Default()", cfg)
	}
}

func TestLoadMissingFilesAreNotErrors(t *testing.T) {
	dir := t.TempDir()
	// Neither the user path nor the project path exists on disk.
	_, err := Load(filepath.Join(dir, "user", "config.toml"), filepath.Join(dir, "project", ".jig"))
	if err != nil {
		t.Fatalf("Load with absent user/project files: unexpected error %v", err)
	}
}

func TestLoadPersistenceOffRootIsNotError(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user-config.toml", "[ui]\n")
	if _, err := Load(userPath, ""); err != nil {
		t.Fatalf("Load with root==\"\" (persistence-off): unexpected error %v", err)
	}
}

func TestLoadInvalidUserConfigIsSanitizedAndIdentifiesPath(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user-config.toml", "not [ valid")

	_, err := Load(userPath, filepath.Join(dir, "project", ".jig"))
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Load with invalid user config: err = %v, want errors.Is(err, ErrConfigInvalid)", err)
	}
}

func TestLoadInvalidProjectConfigIsSanitizedAndIdentifiesPath(t *testing.T) {
	dir := t.TempDir()
	projectRoot := filepath.Join(dir, "project", ".jig")
	writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), "not [ valid")

	_, err := Load("", projectRoot)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Load with invalid project config: err = %v, want errors.Is(err, ErrConfigInvalid)", err)
	}
}
