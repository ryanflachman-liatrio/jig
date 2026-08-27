package review

import keybind "charm.land/bubbles/v2/key"

type keyMap struct {
	Up, Down, First, Last      keybind.Binding
	PrevDoc, NextDoc, NextOpen keybind.Binding
	Select, Comment, Reviewed  keybind.Binding
	ToggleMode, NextComment    keybind.Binding
	Edit, Delete, Summary      keybind.Binding
	Confirm, Cancel, NextKind  keybind.Binding
}

func defaultKeyMap() keyMap {
	b := func(keys []string, help string) keybind.Binding {
		return keybind.NewBinding(keybind.WithKeys(keys...), keybind.WithHelp(keys[0], help))
	}
	return keyMap{
		Up: b([]string{"up", "k"}, "up"), Down: b([]string{"down", "j"}, "down"),
		First: b([]string{"g"}, "first"), Last: b([]string{"G"}, "last"),
		PrevDoc: b([]string{"{"}, "previous document"), NextDoc: b([]string{"}"}, "next document"),
		NextOpen: b([]string{"u"}, "next unreviewed"), Select: b([]string{"v"}, "select range"),
		Comment: b([]string{"c"}, "comment"), Reviewed: b([]string{"r"}, "mark reviewed"),
		ToggleMode: b([]string{"s"}, "source/preview"), NextComment: b([]string{"n", "N"}, "next comment"),
		Edit: b([]string{"e"}, "edit comment"), Delete: b([]string{"x"}, "delete comment"),
		Summary: b([]string{"S"}, "finish review"), Confirm: b([]string{"enter"}, "confirm"),
		Cancel: b([]string{"esc"}, "cancel"), NextKind: b([]string{"tab"}, "next kind"),
	}
}

// Help returns the workspace-local shortcuts. It is intentionally generated
// from the bindings so the help surface cannot drift from behavior.
func (m Model) Help() []KeyHelp {
	if m.mode == ModeSummary {
		return []KeyHelp{
			{Key: "1-9", Description: "choose decision"},
			{Key: "enter", Description: "submit review"},
			{Key: "esc", Description: "return to documents"},
		}
	}
	if m.mode == ModeComposeComment || m.mode == ModeEditComment {
		return []KeyHelp{
			{Key: "enter", Description: "save comment"},
			{Key: "tab", Description: "change comment kind"},
			{Key: "esc", Description: "cancel comment"},
		}
	}
	return []KeyHelp{
		{Key: "S", Description: "finish review"},
		{Key: "j/k", Description: "move line"}, {Key: "{/}", Description: "change document"},
		{Key: "v", Description: "select range"}, {Key: "c", Description: "comment"},
		{Key: "r", Description: "mark reviewed"}, {Key: "s", Description: "source/preview"},
		{Key: "esc", Description: "close/cancel"},
	}
}

type KeyHelp struct{ Key, Description string }
