package tui

import "jig/internal/tui/shared"

// help.go renders the "?" modal overlay. Like the footer (see hintString), it
// renders straight from the same keybind.Binding structs the handlers match on,
// skipping disabled bindings — so the overlay can never advertise a key the
// screen does not accept, nor omit one it does. The overlay is focus-aware.

// helpSection is one titled group of bindings in the overlay — aliased from
// shared so existing code continues to use the unexported name.
type helpSection = shared.HelpSection

// helpProvider is implemented by every screen model that contributes a help
// overlay. capturesText reports whether the screen is currently capturing free
// text (a list filter, a gate textarea), in which case "?" is a literal
// character and must not open the overlay.
type helpProvider interface {
	helpSections() []helpSection
	capturesText() bool
}

// renderHelpOverlay delegates to the shared implementation.
func renderHelpOverlay(base string, width, height int, sections []helpSection) string {
	return shared.RenderHelpOverlay(base, width, height, sections)
}
