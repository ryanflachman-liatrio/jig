package shared

import (
	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
)

// TextareaOption tweaks the shared textarea's setup. Used so the paneled chat
// can suppress the textarea's own border (its Message panel owns the frame,
// avoiding a double box) without duplicating the setup block.
type TextareaOption func(*textareaConfig)

type textareaConfig struct {
	borderless bool
}

// WithoutBorder suppresses the textarea's own rounded border. The chat's Message
// panel draws the frame, so a bordered textarea inside it would double-box.
func WithoutBorder() TextareaOption {
	return func(c *textareaConfig) { c.borderless = true }
}

// NewInputTextarea builds the shared prompt/review/agent-input editor: a
// bordered single-purpose textarea where enter submits (the caller handles that)
// and newlines are inserted with alt/shift+enter. It is returned focused. This
// replaces four byte-identical setup blocks (chat compose + the monitor's
// review/input/prompt gates). A width of 0 defers sizing to a later resize.
func NewInputTextarea(placeholder string, width, height int, opts ...TextareaOption) textarea.Model {
	var cfg textareaConfig
	for _, o := range opts {
		o(&cfg)
	}

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
	// reflects focus state (Theme.Textarea.Focused/BlurredBorder). true selects
	// the dark default styles (the theme is dark-only).
	styles := textarea.DefaultStyles(true)
	base := Theme.Textarea.Base
	if cfg.borderless {
		// The Message panel owns the frame; drop the textarea's border+padding so
		// its content aligns flush against the panel's inner area (no double box).
		base = Theme.Textarea.Borderless
	}
	styles.Focused.Base = base.BorderForeground(Theme.Textarea.FocusedBorder)
	styles.Blurred.Base = base.BorderForeground(Theme.Textarea.BlurredBorder)
	ta.SetStyles(styles)
	ta.Focus()
	return ta
}
