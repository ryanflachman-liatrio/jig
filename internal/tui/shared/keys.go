package shared

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
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

// KeyHelpTyping avoids stealing a printable question mark from text editors.
var KeyHelpTyping = keybind.NewBinding(
	keybind.WithKeys("f1"),
	keybind.WithHelp("F1", "help"),
)

// KeyPalette opens the command palette (Phase 1.3 / E4: ctrl+k only, no ":").
// Disabled while CapturesText so free-text editors keep their chords.
var KeyPalette = keybind.NewBinding(
	keybind.WithKeys("ctrl+k"),
	keybind.WithHelp("ctrl+k", "commands"),
)

// KeyNotificationDiagnostics opens the process-wide notification diagnostics
// overlay. The chord is chosen to be discoverable but unusual (Ctrl+G is
// unused elsewhere in the app) so it never clashes with a screen-owned key.
// Disabled while CapturesText — a text editor keeps its chord — and while
// the palette is open (the palette owns its keys). See Spec 23 FR-17.
var KeyNotificationDiagnostics = keybind.NewBinding(
	keybind.WithKeys("ctrl+g"),
	keybind.WithHelp("ctrl+g", "notification diagnostics"),
)

func GlobalHelpBindings(capturesText bool, extra ...keybind.Binding) []keybind.Binding {
	regular := KeyHelp
	regular.SetEnabled(!capturesText)
	typing := KeyHelpTyping
	typing.SetEnabled(capturesText)
	palette := KeyPalette
	palette.SetEnabled(!capturesText)

	bindings := make([]keybind.Binding, 0, len(extra)+4)
	bindings = append(bindings, extra...)
	bindings = append(bindings, palette, regular, typing, KeyQuit)
	return bindings
}

func MoreHelpBinding(capturesText bool) keybind.Binding {
	binding := KeyHelp
	if capturesText {
		binding = KeyHelpTyping
	}
	help := binding.Help()
	binding.SetHelp(help.Key, "more")
	return binding
}

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

// CompactHint reserves the final slot for complete help, then admits contextual
// bindings atomically so terminal clipping can never leave a partial command.
func CompactHint(width int, more keybind.Binding, bindings ...keybind.Binding) string {
	if width < 1 || !more.Enabled() {
		return ""
	}
	moreText := HintString(more)
	if lipgloss.Width(moreText) > width {
		return ""
	}

	const separator = "  •  "
	parts := make([]string, 0, len(bindings)+1)
	used := 0
	for _, binding := range bindings {
		if !binding.Enabled() {
			continue
		}
		text := HintString(binding)
		added := lipgloss.Width(text)
		if len(parts) > 0 {
			added += lipgloss.Width(separator)
		}
		reserved := lipgloss.Width(separator) + lipgloss.Width(moreText)
		if used+added+reserved > width {
			continue
		}
		parts = append(parts, text)
		used += added
	}
	parts = append(parts, moreText)
	return strings.Join(parts, separator)
}
