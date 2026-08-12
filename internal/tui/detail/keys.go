package detail

import keybind "charm.land/bubbles/v2/key"

// detailKeys covers the read-only workflow view. Run is disabled until a valid
// workflow loads, so it both stops matching and drops out of the footer.
type detailKeys struct {
	Run    keybind.Binding // matched (SetEnabled on wf presence)
	Runs   keybind.Binding // matched
	Toggle keybind.Binding // matched (v: list⇆chart; SetEnabled on wf presence)
	Back   keybind.Binding // matched
}

func defaultKeys() detailKeys {
	return detailKeys{
		Run:    keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "run")),
		Runs:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "runs")),
		Toggle: keybind.NewBinding(keybind.WithKeys("v"), keybind.WithHelp("v", "chart")),
		Back:   keybind.NewBinding(keybind.WithKeys("esc", "q", "backspace", "h", "left"), keybind.WithHelp("esc", "back")),
	}
}
