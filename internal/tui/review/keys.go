package review

import keybind "charm.land/bubbles/v2/key"

type keyMap struct {
	Up, Down, First, Last        keybind.Binding
	PrevDoc, NextDoc, NextOpen   keybind.Binding
	Select, Comment, Reviewed    keybind.Binding
	ToggleMode, NextComment      keybind.Binding
	PanLeft, PanRight, PanHome   keybind.Binding
	PrevHunk, NextHunk, FoldHunk keybind.Binding
	Edit, Delete, Summary        keybind.Binding
	Confirm, Cancel, NextKind    keybind.Binding
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
		ToggleMode: b([]string{"s"}, "toggle source/preview"), NextComment: b([]string{"n", "N"}, "next comment"),
		PanLeft: b([]string{"left", "h"}, "pan left"), PanRight: b([]string{"right", "l"}, "pan right"),
		PanHome:  b([]string{"0"}, "first column"),
		PrevHunk: b([]string{"["}, "previous hunk"), NextHunk: b([]string{"]"}, "next hunk"),
		FoldHunk: b([]string{"z"}, "fold hunk"),
		Edit:     b([]string{"e"}, "edit comment"), Delete: b([]string{"x"}, "delete comment"),
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
	move := "move line"
	if m.activeDocumentMode() == DocumentPreview {
		move = "move block"
	}
	help := []KeyHelp{
		{Key: "S", Description: "finish review"},
		{Key: "j/k", Description: move}, {Key: "{/}", Description: "change document"},
	}
	if m.activeDocumentMode() == DocumentSource {
		help = append(help, KeyHelp{Key: "v", Description: "select range"})
		help = append(help, KeyHelp{Key: "h/l", Description: "pan source"}, KeyHelp{Key: "0", Description: "first column"})
		if m.diffNavigationAvailable() {
			help = append(help, KeyHelp{Key: "[", Description: "previous hunk"}, KeyHelp{Key: "]", Description: "next hunk"}, KeyHelp{Key: "z", Description: "fold hunk"})
		}
	}
	help = append(help,
		KeyHelp{Key: "c", Description: "new comment"},
		KeyHelp{Key: "enter", Description: "open comment"},
		KeyHelp{Key: "r", Description: "mark reviewed"},
	)
	if m.docs[m.active].meta.Format == "markdown" {
		help = append(help, KeyHelp{Key: "s", Description: "view"})
	}
	return append(help, KeyHelp{Key: "esc", Description: "close/cancel"})
}

type KeyHelp struct{ Key, Description string }
