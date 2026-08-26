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
		Summary: b([]string{"S"}, "summary"), Confirm: b([]string{"enter"}, "confirm"),
		Cancel: b([]string{"esc"}, "cancel"), NextKind: b([]string{"tab"}, "next kind"),
	}
}

// Help returns the workspace-local shortcuts. It is intentionally generated
// from the bindings so the help surface cannot drift from behavior.
func (m Model) Help() []KeyHelp {
	return []KeyHelp{
		{Key: "j/k", Description: "move line"}, {Key: "{/}", Description: "change document"},
		{Key: "v", Description: "select range"}, {Key: "c", Description: "comment"},
		{Key: "r", Description: "mark reviewed"}, {Key: "s", Description: "source/preview"},
		{Key: "S", Description: "summary"}, {Key: "esc", Description: "close/cancel"},
	}
}

type KeyHelp struct{ Key, Description string }
