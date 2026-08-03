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
	idx := buildProfileIndex(profiles)
	if p, ok := idx["@careful"]; !ok {
		t.Error("expected @careful profile")
	} else {
		if p.Model != "claude-opus-4-8" {
			t.Errorf("@careful Model = %q, want claude-opus-4-8", p.Model)
		}
		if p.MaxTurns != 5 {
			t.Errorf("@careful MaxTurns = %d, want 5", p.MaxTurns)
		}
	}
	if p, ok := idx["@fast"]; !ok {
		t.Error("expected @fast profile")
	} else {
		if p.Effort != EffortLow {
			t.Errorf("@fast Effort = %q, want low", p.Effort)
		}
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

func TestLoadProfilesShadowsBuiltin(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, ".agents", "jig", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "bad.toml"), []byte(`
[[agent]]
id = "@interactive"
model = "claude-haiku-4-5-20251001"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadProfiles(dir)
	if err == nil {
		t.Fatal("expected error for shadowing built-in, got nil")
	}
	if !strings.Contains(err.Error(), "shadows a built-in") {
		t.Fatalf("error = %q, want 'shadows a built-in'", err.Error())
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

func TestBuiltinProfilesAlwaysAvailable(t *testing.T) {
	// Structural-only mode (baseDir="") still has built-in profiles available.
	const src = `
[workflow]
name = "x"
version = "1"

[[step]]
id = "a"
type = "agent"
skill = "s"
profile = "@interactive"
`
	// We can't resolve skill dirs in structural mode, so just check no error
	// is returned for the profile reference itself.
	_, err := Decode(src, "")
	// The only errors should be about the skill dir not being found (file checks
	// are skipped in structural mode). Profile lookup must NOT error.
	// In structural mode, checkAgent skips the skill dir check, so this should pass.
	if err != nil {
		// Accept errors about skill/agent_file only — profile must not be the cause.
		if strings.Contains(err.Error(), "unknown profile") {
			t.Fatalf("built-in @interactive unavailable in structural mode: %v", err)
		}
	}
}
