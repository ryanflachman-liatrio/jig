package detail

import (
	keybind "charm.land/bubbles/v2/key"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// detailKeys covers the read-only workflow view. Run is disabled until a valid
// workflow loads, so it both stops matching and drops out of the footer.
type detailKeys struct {
	Run    keybind.Binding // matched (SetEnabled on wf presence)
	Runs   keybind.Binding // matched
	Toggle keybind.Binding // matched (v: list⇆chart; SetEnabled on wf presence)
	Back   keybind.Binding // matched
}

func defaultKeys() detailKeys {
	k := baseKeys()
	shared.ApplyKeymap(k.actions())
	return k
}

// Actions lists the workflow detail view's named actions over the built-in
// bindings.
func Actions() []shared.Action {
	k := baseKeys()
	return k.actions()
}

func baseKeys() detailKeys {
	return detailKeys{
		Run:    keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "run")),
		Runs:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "runs")),
		Toggle: keybind.NewBinding(keybind.WithKeys("v"), keybind.WithHelp("v", "chart")),
		Back:   keybind.NewBinding(keybind.WithKeys("esc", "q", "backspace", "h", "left"), keybind.WithHelp("esc", "back")),
	}
}

func (k *detailKeys) actions() []shared.Action {
	ctx := []string{"detail"}
	return []shared.Action{
		{ID: "detail.run", Contexts: ctx, Binding: &k.Run},
		{ID: "detail.runs", Contexts: ctx, Binding: &k.Runs},
		{ID: "detail.toggle_chart", Contexts: ctx, Binding: &k.Toggle},
		// Back sheds h/left in chart mode (applyViewMode), so its keys stay fixed.
		{ID: "detail.back", Contexts: ctx, Binding: &k.Back, Fixed: true},
	}
}
