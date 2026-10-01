package shared

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	keybind "charm.land/bubbles/v2/key"
)

// Action is one named command in a screen's keymap. ID is the stable name
// operators use in config.toml's [keys] table ("monitor.copy_item"); Binding
// points at the field in the screen's key struct that handlers match against,
// so remapping rewrites the same value the handler, footer, help overlay, and
// command palette all read.
//
// Context is the dot-separated region where the action's keys are live
// ("" = everywhere, "monitor" = every Monitor region, "monitor.steps" = the
// Steps panel only). Two actions conflict when one context equals or encloses
// the other and they share a key. An action matched in several regions lists
// each one.
//
// Fixed actions keep their default keys: they are listed so a remap cannot
// silently shadow them, but they either distinguish direction by the pressed
// key (j/k, n/N, [/]) or belong to a chord or text editor, so they cannot be
// rebound independently.
type Action struct {
	ID       string
	Contexts []string
	Binding  *keybind.Binding
	Fixed    bool
}

// keyOverrides is the process-wide validated [keys] table. It is written once
// by ConfigureKeymap before the program starts, and read by every screen's
// key constructor, so a screen built later in the session sees the same keys.
var keyOverrides map[string][]string

// SetKeyOverrides installs a validated override table. Callers must validate
// it with ValidateKeymap first; tui.ConfigureKeymap does both.
func SetKeyOverrides(overrides map[string][]string) {
	keyOverrides = make(map[string][]string, len(overrides))
	for id, keys := range overrides {
		keyOverrides[id] = slices.Clone(keys)
	}
}

// ApplyKeymap rewrites each overridden action's binding in place: its keys
// become the configured chords and its help label lists them, keeping the
// default description. Unlisted actions keep their defaults.
func ApplyKeymap(actions []Action) {
	for _, a := range actions {
		keys, ok := keyOverrides[a.ID]
		if !ok || a.Fixed {
			continue
		}
		rebind(a.Binding, keys)
	}
}

func rebind(b *keybind.Binding, keys []string) {
	desc := b.Help().Desc
	enabled := b.Enabled()
	b.SetKeys(keys...)
	b.SetHelp(strings.Join(keys, "/"), desc)
	b.SetEnabled(enabled)
}

// Relabel returns b with a new help description and its current key label,
// for contextual descriptions ("copy file", "bell: on"). Use it instead of
// SetHelp with a literal key so a remapped key never shows its default.
func Relabel(b keybind.Binding, desc string) keybind.Binding {
	b.SetHelp(b.Help().Key, desc)
	return b
}

// ValidateKeymap checks overrides against the registered actions: every ID
// must name a non-fixed action, every chord list must be non-empty, and no
// two actions whose contexts overlap may share a key after the overrides
// apply. Errors name action IDs and keys only.
func ValidateKeymap(actions []Action, overrides map[string][]string) error {
	byID := make(map[string]Action, len(actions))
	for _, a := range actions {
		byID[a.ID] = a
	}
	ids := make([]string, 0, len(overrides))
	for id := range overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a, ok := byID[id]
		if !ok {
			return fmt.Errorf("keys: unknown action %q", id)
		}
		if a.Fixed {
			return fmt.Errorf("keys: action %q cannot be remapped", id)
		}
		keys := overrides[id]
		if len(keys) == 0 {
			return fmt.Errorf("keys: action %q needs at least one key", id)
		}
		for _, k := range keys {
			if k == "" || strings.ContainsAny(k, " \t\n") {
				return fmt.Errorf("keys: action %q has an invalid key %q", id, k)
			}
		}
	}

	effective := func(a Action) []string {
		if keys, ok := overrides[a.ID]; ok && !a.Fixed {
			return keys
		}
		return a.Binding.Keys()
	}
	for i, a := range actions {
		for _, b := range actions[i+1:] {
			if a.Binding == b.Binding || !contextsOverlap(a.Contexts, b.Contexts) {
				continue
			}
			for _, k := range effective(a) {
				if slices.Contains(effective(b), k) {
					return fmt.Errorf("keys: %q is bound to both %q and %q", k, a.ID, b.ID)
				}
			}
		}
	}
	return nil
}

func contextsOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if encloses(x, y) || encloses(y, x) {
				return true
			}
		}
	}
	return false
}

// encloses reports whether outer is inner or one of its dot-separated
// ancestors; "" encloses every context.
func encloses(outer, inner string) bool {
	return outer == "" || outer == inner || strings.HasPrefix(inner, outer+".")
}

// GlobalActions lists the app-wide chords. They are package variables, so
// remapping them rewrites the values every screen reads.
func GlobalActions() []Action {
	return []Action{
		{ID: "global.quit", Contexts: []string{""}, Binding: &KeyQuit},
		{ID: "global.help", Contexts: []string{""}, Binding: &KeyHelp},
		{ID: "global.help_typing", Contexts: []string{""}, Binding: &KeyHelpTyping},
		{ID: "global.palette", Contexts: []string{""}, Binding: &KeyPalette},
		{ID: "global.notification_diagnostics", Contexts: []string{""}, Binding: &KeyNotificationDiagnostics},
	}
}
