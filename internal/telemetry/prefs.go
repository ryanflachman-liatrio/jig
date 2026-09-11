package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PrefsFileName is the on-disk name of the operator-scoped preferences file,
// stored under the same .jig/ root as internal/tui/prefs' tui.json.
const PrefsFileName = "telemetry.json"

// PrefsPath joins jigRoot with telemetry.json. Empty jigRoot yields "" so
// callers naturally hit the persistence-off no-op path.
func PrefsPath(jigRoot string) string {
	if jigRoot == "" {
		return ""
	}
	return filepath.Join(jigRoot, PrefsFileName)
}

// LoadPrefs reads .jig/telemetry.json. Missing file returns the zero-value
// Prefs (mode == ""). Corrupt or unreadable file also returns zero-value
// Prefs so a bad write never bricks the TUI or `jig run`, matching the
// existing tui.json precedent (internal/tui/prefs/prefs.go).
func LoadPrefs(jigRoot string) Prefs {
	path := PrefsPath(jigRoot)
	if path == "" {
		return Prefs{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Prefs{}
	}
	var p Prefs
	if err := json.Unmarshal(data, &p); err != nil {
		return Prefs{}
	}
	return p
}

// SavePrefs writes prefs atomically when jigRoot is set. Empty root is a
// no-op (persistence-off), matching internal/tui/prefs.
func SavePrefs(jigRoot string, p Prefs) error {
	path := PrefsPath(jigRoot)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
