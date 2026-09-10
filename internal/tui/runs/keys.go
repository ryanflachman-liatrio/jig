package runs

import keybind "charm.land/bubbles/v2/key"

type runsKeys struct {
	Up      keybind.Binding // matched
	Down    keybind.Binding // matched
	Open    keybind.Binding // matched
	NewRun  keybind.Binding // matched
	Resume  keybind.Binding // matched
	Delete  keybind.Binding // matched
	CopyID  keybind.Binding // matched (y: copy selected run ID)
	Back    keybind.Binding // matched
}

func defaultKeys() runsKeys {
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
