// Package prefs loads and saves lightweight TUI operator preferences under
// .jig/tui.json (E6 / C5). Persistence-off (empty jig root) keeps in-memory
// defaults and makes Save a no-op.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FileName is the preference file under the .jig/ root.
const FileName = "tui.json"

// Prefs is the on-disk shape of .jig/tui.json.
type Prefs struct {
	// SimpleMode hides advanced transcript affordances from footer/help until
	// toggled. Default true for new installs when the file is missing (C5).
	SimpleMode bool `json:"simple_mode"`
}

// Default returns preferences for a missing config file.
func Default() Prefs {
	return Prefs{SimpleMode: true}
}

// Path joins jigRoot with tui.json. Empty jigRoot yields "".
func Path(jigRoot string) string {
	if jigRoot == "" {
		return ""
	}
	return filepath.Join(jigRoot, FileName)
}

// Load reads .jig/tui.json. Missing file → Default(). Corrupt/unreadable file
// also falls back to Default so a bad write never bricks the TUI.
func Load(jigRoot string) Prefs {
	path := Path(jigRoot)
	if path == "" {
		return Default()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Default()
	}
	var p Prefs
	if err := json.Unmarshal(data, &p); err != nil {
		return Default()
	}
	return p
}

// Save writes prefs atomically when jigRoot is set. Empty root is a no-op.
func Save(jigRoot string, p Prefs) error {
	path := Path(jigRoot)
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
