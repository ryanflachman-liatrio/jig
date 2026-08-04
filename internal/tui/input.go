package tui

import (
	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
)

// newInputTextarea builds the shared prompt/review/agent-input editor: a
// bordered single-purpose textarea where enter submits (the caller handles that)
// and newlines are inserted with alt/shift+enter. It is returned focused. This
// replaces four byte-identical setup blocks (chat compose + the monitor's
// review/input/prompt gates). A width of 0 defers sizing to a later resize.
func newInputTextarea(placeholder string, width, height int) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.KeyMap.InsertNewline = keybind.NewBinding(
		keybind.WithKeys("alt+enter", "shift+enter"),
		keybind.WithHelp("alt+enter", "insert newline"),
	)
	ta.ShowLineNumbers = false
	ta.SetHeight(height)
	if width > 0 {
		ta.SetWidth(width)
	}
	// v2 textarea: styles are one struct applied via SetStyles; the border color
	// reflects focus state (theme.Textarea.Focused/BlurredBorder). true selects
	// the dark default styles (the theme is dark-only).
	styles := textarea.DefaultStyles(true)
	styles.Focused.Base = theme.Textarea.Base.BorderForeground(theme.Textarea.FocusedBorder)
	styles.Blurred.Base = theme.Textarea.Base.BorderForeground(theme.Textarea.BlurredBorder)
	ta.SetStyles(styles)
	ta.Focus()
	return ta
}
