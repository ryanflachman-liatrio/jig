package selector

import (
	keybind "charm.land/bubbles/v2/key"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

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
	k := baseKeys()
	shared.ApplyKeymap(k.actions())
	return k
}

// Actions lists the workflow picker's named actions over the built-in
// bindings.
func Actions() []shared.Action {
	k := baseKeys()
	return k.actions()
}

func baseKeys() selectorKeys {
	return selectorKeys{
		Nav:    keybind.NewBinding(keybind.WithKeys("up", "down", "k", "j"), keybind.WithHelp("↑/↓", "navigate")),
		Filter: keybind.NewBinding(keybind.WithKeys("/"), keybind.WithHelp("/", "filter")),
		Open:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "focus runs")),
		Apply:  keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "apply")),
		Clear:  keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "clear filter")),
	}
}

// actions names Open, the one binding the selector matches. Navigation and
// filtering belong to the embedded list, so they are listed as fixed to keep
// a remap from shadowing them.
func (k *selectorKeys) actions() []shared.Action {
	ctx := []string{"home.workflows"}
	return []shared.Action{
		{ID: "selector.open", Contexts: ctx, Binding: &k.Open},
		{ID: "selector.nav", Contexts: ctx, Binding: &k.Nav, Fixed: true},
		{ID: "selector.filter", Contexts: ctx, Binding: &k.Filter, Fixed: true},
	}
}
