package runs

import (
	keybind "charm.land/bubbles/v2/key"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

type runsKeys struct {
	Up     keybind.Binding // matched
	Down   keybind.Binding // matched
	Open   keybind.Binding // matched
	NewRun keybind.Binding // matched
	Resume keybind.Binding // matched
	Delete keybind.Binding // matched
	CopyID keybind.Binding // matched (y: copy selected run ID)
	Back   keybind.Binding // matched
}

func defaultKeys() runsKeys {
	k := baseKeys()
	shared.ApplyKeymap(k.actions())
	return k
}

// Actions lists the Runs pane's named actions over the built-in bindings.
func Actions() []shared.Action {
	k := baseKeys()
	return k.actions()
}

func baseKeys() runsKeys {
	return runsKeys{
		Up:     keybind.NewBinding(keybind.WithKeys("up", "k"), keybind.WithHelp("↑/k", "up")),
		Down:   keybind.NewBinding(keybind.WithKeys("down", "j"), keybind.WithHelp("↓/j", "down")),
		Open:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "monitor")),
		NewRun: keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "new run")),
		Resume: keybind.NewBinding(keybind.WithKeys("R"), keybind.WithHelp("R", "resume paused/interrupted")),
		Delete: keybind.NewBinding(keybind.WithKeys("d"), keybind.WithHelp("d", "delete")),
		CopyID: keybind.NewBinding(keybind.WithKeys("y"), keybind.WithHelp("y", "copy run id")),
		Back:   keybind.NewBinding(keybind.WithKeys("esc", "q", "backspace", "h", "left"), keybind.WithHelp("esc", "back")),
	}
}

// actions names the Runs pane bindings. NewRun is also live while the
// Workflows pane is focused, which starts a run of the selected workflow.
func (k *runsKeys) actions() []shared.Action {
	const ctx = "home.runs"
	return []shared.Action{
		{ID: "runs.up", Contexts: []string{ctx}, Binding: &k.Up},
		{ID: "runs.down", Contexts: []string{ctx}, Binding: &k.Down},
		{ID: "runs.open", Contexts: []string{ctx}, Binding: &k.Open},
		{ID: "runs.new_run", Contexts: []string{ctx, "home.workflows"}, Binding: &k.NewRun},
		{ID: "runs.resume", Contexts: []string{ctx}, Binding: &k.Resume},
		{ID: "runs.delete", Contexts: []string{ctx}, Binding: &k.Delete},
		{ID: "runs.copy_id", Contexts: []string{ctx}, Binding: &k.CopyID},
		{ID: "runs.back", Contexts: []string{ctx}, Binding: &k.Back},
	}
}
