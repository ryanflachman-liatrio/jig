package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestProjectConfigPath(t *testing.T) {
	if got := ProjectConfigPath(""); got != "" {
		t.Fatalf("ProjectConfigPath(\"\") = %q, want empty (persistence-off must not join)", got)
	}
	got := ProjectConfigPath("/tmp/proj/.jig")
	want := filepath.Join("/tmp/proj/.jig", "config.toml")
	if got != want {
		t.Fatalf("ProjectConfigPath = %q, want %q", got, want)
	}
}

func TestLoadFileMissingIsNotError(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadFile(filepath.Join(dir, "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("missing file: unexpected error %v", err)
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Fatalf("missing file: got %+v, want zero value", cfg)
	}
}

func TestLoadFileEmptyPathIsNotError(t *testing.T) {
	cfg, err := loadFile("")
	if err != nil {
		t.Fatalf("empty path: unexpected error %v", err)
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Fatalf("empty path: got %+v, want zero value", cfg)
	}
}

func TestLoadFileInvalidTOMLIsSanitized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not [ valid toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadFile(path)
	if err == nil {
		t.Fatal("invalid TOML: expected error, got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("invalid TOML: err = %v, want errors.Is(err, ErrConfigInvalid)", err)
	}
	if got := err.Error(); got == ErrConfigInvalid.Error() {
		t.Fatalf("invalid TOML: error %q does not name the offending path", got)
	}
	// Sanitized: the raw file content must never appear in the error text.
	if want := "this is not"; strings.Contains(err.Error(), want) {
		t.Fatalf("invalid TOML: error %q leaks file contents", err.Error())
	}
}

// TestGlyphPresetCaseSensitivity is the schema validation FR for Unit 3:
// [ui] glyph_preset accepts only the exact lowercase strings "ascii" and
// "unicode"; any other casing (including an otherwise-correct value with the
// wrong case) is ErrConfigInvalid, with no case-folding/normalization.
func TestGlyphPresetCaseSensitivity(t *testing.T) {
	for _, tc := range []struct {
		value string
		bad   bool
	}{
		{"ascii", false},
		{"unicode", false},
		{"ASCII", true},
		{"Unicode", true},
		{"nerd-font", true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			content := "[ui]\nglyph_preset = " + strconv.Quote(tc.value) + "\n"
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := loadFile(path)
			if tc.bad {
				if !errors.Is(err, ErrConfigInvalid) {
					t.Fatalf("glyph_preset=%q: err = %v, want errors.Is(err, ErrConfigInvalid)", tc.value, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("glyph_preset=%q: unexpected error %v", tc.value, err)
			}
		})
	}
}

func TestLoadFileValidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[ui]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFile(path)
	if err != nil {
		t.Fatalf("valid TOML: unexpected error %v", err)
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Fatalf("valid TOML with no keys: got %+v, want zero value", cfg)
	}
}
