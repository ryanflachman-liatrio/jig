package selector

import keybind "charm.land/bubbles/v2/key"

// selectorKeys covers the workflow picker. Navigation and filtering are owned
// by the embedded bubbles list; Nav/Filter/Apply/Clear are display-only so the
// footer stays honest about what the list accepts, while Open is the one
// binding the selector itself matches.
type selectorKeys struct {
	Nav    keybind.Binding // display-only (list-owned)
	Filter keybind.Binding // display-only (list-owned)
	Open   keybind.Binding // matched
	Apply  keybind.Binding // display-only (list-owned, filtering)
	Clear  keybind.Binding // display-only (list-owned, filtering)
}

func defaultKeys() selectorKeys {
	return selectorKeys{
		Nav:    keybind.NewBinding(keybind.WithKeys("up", "down", "k", "j"), keybind.WithHelp("↑/↓", "navigate")),
		Filter: keybind.NewBinding(keybind.WithKeys("/"), keybind.WithHelp("/", "filter")),
		Open:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "open")),
		Apply:  keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "apply")),
		Clear:  keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "clear filter")),
	}
}
