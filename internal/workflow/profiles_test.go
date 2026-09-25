package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProfilesAbsent(t *testing.T) {
	dir := t.TempDir()
	profiles, err := loadProfiles(dir)
	if err != nil {
		t.Fatalf("loadProfiles with no .agents dir: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected no profiles, got %d", len(profiles))
	}
}

func TestLoadProfilesStructuralMode(t *testing.T) {
	profiles, err := loadProfiles("")
	if err != nil {
		t.Fatalf("loadProfiles(\"\") should be a no-op: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected no profiles in structural mode, got %d", len(profiles))
	}
}

func TestLoadProfilesValid(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, ".agents", "jig", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "custom.toml"), []byte(`
[[agent]]
id = "@careful"
model = "claude-opus-4-8"
max_turns = 5

[[agent]]
id = "@fast"
effort = "low"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := loadProfiles(dir)
	if err != nil {
		t.Fatalf("loadProfiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	careful, ok := profiles["@careful"]
	if !ok {
		t.Fatal("expected @careful profile")
	}
	if deref(careful.spec.Model) != "claude-opus-4-8" || deref(careful.spec.MaxTurns) != 5 {
		t.Errorf("@careful = model %q max_turns %d", deref(careful.spec.Model), deref(careful.spec.MaxTurns))
	}
	fast, ok := profiles["@fast"]
	if !ok {
		t.Fatal("expected @fast profile")
	}
	if deref(fast.spec.Effort) != "low" {
		t.Errorf("@fast effort = %q, want low", deref(fast.spec.Effort))
	}
}

func TestLoadProfilesDuplicateID(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, ".agents", "jig", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two files each declaring the same id — the second file should conflict.
	if err := os.WriteFile(filepath.Join(profDir, "a.toml"), []byte(`
[[agent]]
id = "@myprofile"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "b.toml"), []byte(`
[[agent]]
id = "@myprofile"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadProfiles(dir)
	if err == nil {
		t.Fatal("expected error for duplicate id, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate agent id") {
		t.Fatalf("error = %q, want 'duplicate agent id'", err.Error())
	}
}

func TestLoadProfilesMissingAtSign(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, ".agents", "jig", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "bad.toml"), []byte(`
[[agent]]
id = "noatsign"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadProfiles(dir)
	if err == nil {
		t.Fatal("expected error for missing '@', got nil")
	}
	if !strings.Contains(err.Error(), "must start with '@'") {
		t.Fatalf("error = %q, want 'must start with @'", err.Error())
	}
}

func TestLoadProfilesMissingID(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, ".agents", "jig", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "bad.toml"), []byte(`
[[agent]]
model = "claude-haiku-4-5-20251001"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadProfiles(dir)
	if err == nil {
		t.Fatal("expected error for missing id, got nil")
	}
	if !strings.Contains(err.Error(), "missing `id`") {
		t.Fatalf("error = %q, want 'missing id'", err.Error())
	}
}

func TestLoadProfilesUnknownKey(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, ".agents", "jig", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "bad.toml"), []byte(`
[[agent]]
id = "@myprofile"
typo_field = "oops"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadProfiles(dir)
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("error = %q, want 'unknown key'", err.Error())
	}
}
