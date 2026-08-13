package chat

import keybind "charm.land/bubbles/v2/key"

// chatKeys covers the standalone streaming chat. ToOutput (esc) and FocusInput
// (i) are matched separately because they move focus in opposite directions;
// SwitchFocus is a display-only binding that labels the pair in the footer.
// Newline is display-only (the textarea owns alt+enter).
type chatKeys struct {
	Send        keybind.Binding // matched
	Newline     keybind.Binding // display-only (textarea-owned)
	SwitchFocus keybind.Binding // display-only (esc/i pair)
	ToOutput    keybind.Binding // matched (esc)
	FocusInput  keybind.Binding // matched (i)
	PrevTurn    keybind.Binding // matched (left)
	NextTurn    keybind.Binding // matched (right)
	Quit        keybind.Binding // matched (ctrl+c: disconnect then quit)
}

func defaultChatKeys() chatKeys {
	return chatKeys{
		Send:        keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "send")),
		Newline:     keybind.NewBinding(keybind.WithKeys("alt+enter", "shift+enter"), keybind.WithHelp("alt+enter", "newline")),
		SwitchFocus: keybind.NewBinding(keybind.WithKeys("esc", "i"), keybind.WithHelp("esc/i", "switch focus")),
		ToOutput:    keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "output")),
		FocusInput:  keybind.NewBinding(keybind.WithKeys("i"), keybind.WithHelp("i", "input")),
		PrevTurn:    keybind.NewBinding(keybind.WithKeys("left"), keybind.WithHelp("←", "prev turn")),
		NextTurn:    keybind.NewBinding(keybind.WithKeys("right"), keybind.WithHelp("→", "next turn")),
		Quit:        keybind.NewBinding(keybind.WithKeys("ctrl+c"), keybind.WithHelp("ctrl+c", "quit")),
	}
}
