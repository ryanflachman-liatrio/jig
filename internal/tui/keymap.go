package tui

import (
	"github.com/ryanflachman-liatrio/jig/internal/tui/detail"
	"github.com/ryanflachman-liatrio/jig/internal/tui/monitor"
	"github.com/ryanflachman-liatrio/jig/internal/tui/runs"
	"github.com/ryanflachman-liatrio/jig/internal/tui/selector"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// Actions lists every named action across the TUI over its built-in
// bindings: the app-wide chords plus each screen's keymap.
func Actions() []shared.Action {
	home := baseHomeKeys()
	var out []shared.Action
	out = append(out, shared.GlobalActions()...)
	out = append(out, home.actions()...)
	out = append(out, selector.Actions()...)
	out = append(out, runs.Actions()...)
	out = append(out, detail.Actions()...)
	out = append(out, monitor.Actions()...)
	return out
}

// ConfigureKeymap validates the [keys] overrides against every registered
// action and installs them process-wide. Call it once, before the program
// starts: screens read the installed overrides when they build their keys,
// and the global chords are rewritten here.
func ConfigureKeymap(overrides map[string][]string) error {
	if err := shared.ValidateKeymap(Actions(), overrides); err != nil {
		return err
	}
	shared.SetKeyOverrides(overrides)
	shared.ApplyKeymap(shared.GlobalActions())
	return nil
}
