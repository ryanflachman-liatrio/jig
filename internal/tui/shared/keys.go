package shared

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
)

// KeyQuit is the global quit chord. The root model performs the actual quit
// (chatModel disconnects first); screens list it in their footer so the binding
// and its advertised help live in one place.
var KeyQuit = keybind.NewBinding(
	keybind.WithKeys("ctrl+c"),
	keybind.WithHelp("ctrl+c", "quit"),
)

// KeyHelp toggles the help overlay. Global like KeyQuit: the monitor renders the
// modal and lists this binding in its footer so the key and its advertised help
// live in one place. Pressing it again (or esc) dismisses the overlay.
// "shift+/" is listed alongside "?" because some terminals deliver the shifted
// slash as a modified keypress (String() == "shift+/") rather than the literal
// "?" text; matching both keeps the chord working everywhere.
var KeyHelp = keybind.NewBinding(
	keybind.WithKeys("?", "shift+/"),
	keybind.WithHelp("?", "help"),
)

// HintString renders the enabled bindings into the footer's "k desc  •  k desc"
// hint format. Disabled bindings (SetEnabled(false)) are skipped, so a hint can
// never outlive the key it describes.
func HintString(bindings ...keybind.Binding) string {
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		parts = append(parts, h.Key+" "+h.Desc)
	}
	return strings.Join(parts, "  •  ")
}
