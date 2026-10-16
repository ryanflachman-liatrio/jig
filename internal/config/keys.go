package config

import (
	"errors"
	"fmt"
)

// KeyChords is one [keys] entry: the chords bound to a named TUI action.
// TOML accepts either a single chord ("ctrl+y") or a list (["y", "ctrl+y"]).
type KeyChords []string

// UnmarshalTOML accepts a string or an array of strings. Anything else is a
// schema error; loadFile reports it as ErrConfigInvalid.
func (k *KeyChords) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case string:
		*k = KeyChords{v}
		return nil
	case []any:
		out := make(KeyChords, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return errors.New("keys: chords must be strings")
			}
			out = append(out, s)
		}
		*k = out
		return nil
	}
	return errors.New("keys: value must be a chord string or a list of chords")
}

// keysError names only an action ID, never file content, so loadFile can
// surface it alongside the file path.
type keysError struct{ action string }

func (e keysError) Error() string {
	return fmt.Sprintf("keys.%q: needs one or more non-empty chords", e.action)
}

// validateKeys checks the table's shape. Whether an ID names a real action,
// and whether chords collide, depends on the TUI's action registry, so
// tui.ConfigureKeymap checks those when the TUI starts.
func validateKeys(keys map[string]KeyChords) error {
	for id, chords := range keys {
		if len(chords) == 0 {
			return keysError{action: id}
		}
		for _, c := range chords {
			if c == "" {
				return keysError{action: id}
			}
		}
	}
	return nil
}

// KeyOverrides returns the table as plain chord lists for the TUI keymap.
func KeyOverrides(keys map[string]KeyChords) map[string][]string {
	out := make(map[string][]string, len(keys))
	for id, chords := range keys {
		out[id] = append([]string(nil), chords...)
	}
	return out
}
