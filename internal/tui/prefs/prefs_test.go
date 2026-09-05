package prefs_test

import (
	"os"
	"path/filepath"
	"testing"

	"jig/internal/tui/prefs"
)

func TestDefaultSimpleModeOn(t *testing.T) {
	if !prefs.Default().SimpleMode {
		t.Fatal("default simple_mode should be true (C5)")
	}
}

func TestLoadMissingFileDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	p := prefs.Load(dir)
	if !p.SimpleMode {
		t.Fatalf("missing file: SimpleMode=%v, want true", p.SimpleMode)
	}
}

func TestLoadEmptyRootDefaultsOn(t *testing.T) {
	p := prefs.Load("")
	if !p.SimpleMode {
		t.Fatal("empty jig root should default simple mode on")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := prefs.Save(dir, prefs.Prefs{SimpleMode: false}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, prefs.FileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s: %v", path, err)
	}
	got := prefs.Load(dir)
	if got.SimpleMode {
		t.Fatalf("round-trip SimpleMode=%v, want false", got.SimpleMode)
	}
}

func TestSaveEmptyRootNoop(t *testing.T) {
	if err := prefs.Save("", prefs.Prefs{SimpleMode: false}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, prefs.FileName), []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := prefs.Load(dir)
	if !p.SimpleMode {
		t.Fatal("corrupt file should fall back to default simple mode on")
	}
}
