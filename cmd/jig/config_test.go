package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetGlobalConfigFlagPath restores the pre-dispatch --config flag state
// between tests, mirroring resetPreset's role for --ascii.
func resetGlobalConfigFlagPath() {
	globalConfigFlagPath = ""
}

func TestConfigShowDefaults(t *testing.T) {
	t.Cleanup(resetGlobalConfigFlagPath)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-empty"))

	var stdout, stderr bytes.Buffer
	code := configShow([]string{"--root", filepath.Join(dir, "project", ".jig")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("configShow: exit code %d, stderr %q", code, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatal("configShow: expected non-empty TOML output for built-in defaults")
	}
}

func TestConfigShowGlobalConfigFlagSubstitutesUserPath(t *testing.T) {
	t.Cleanup(resetGlobalConfigFlagPath)
	dir := t.TempDir()
	userPath := filepath.Join(dir, "alt.toml")
	if err := os.WriteFile(userPath, []byte("[ui]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	globalConfigFlagPath = userPath

	var stdout, stderr bytes.Buffer
	code := configShow([]string{"--root", filepath.Join(dir, "project", ".jig")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("configShow: exit code %d, stderr %q", code, stderr.String())
	}
}

func TestConfigShowInvalidTOMLIsSanitizedNonZeroExit(t *testing.T) {
	t.Cleanup(resetGlobalConfigFlagPath)
	dir := t.TempDir()
	userPath := filepath.Join(dir, "alt.toml")
	if err := os.WriteFile(userPath, []byte("not [ valid toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	globalConfigFlagPath = userPath

	var stdout, stderr bytes.Buffer
	code := configShow([]string{"--root", filepath.Join(dir, "project", ".jig")}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("configShow with invalid TOML: expected non-zero exit code")
	}
	if stderr.Len() == 0 {
		t.Fatal("configShow with invalid TOML: expected an error message on stderr")
	}
	if !strings.Contains(stderr.String(), userPath) {
		t.Fatalf("configShow with invalid TOML: stderr %q does not name the offending path", stderr.String())
	}
	if strings.Contains(stderr.String(), "not [ valid toml") {
		t.Fatalf("configShow with invalid TOML: stderr %q leaks file contents", stderr.String())
	}
}
