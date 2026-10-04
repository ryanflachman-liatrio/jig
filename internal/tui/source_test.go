package tui

import (
	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"
)

func TestTranscriptSourceSelectionRootAndPalette(t *testing.T) {
	for _, overlay := range []string{"help", "delete", "leave", "palette"} {
		t.Run(overlay, func(t *testing.T) {
			m, _ := newOpenerFixture(t, t.Context(), "source")
			before := m.monitor.View()
			switch overlay {
			case "help":
				m.showHelp = true
			case "delete":
				m.confirmDelete = true
			case "leave":
				m.leaveConfirm = true
			case "palette":
				m.palette = m.palette.Show(m.paletteCommands())
			}
			next, _ := m.Update(makeKey("v"))
			m = next.(rootModel)
			if m.monitor.View() != before {
				t.Fatal("root overlay leaked source key")
			}
		})
	}
	m, _ := newOpenerFixture(t, t.Context(), "source")
	m.palette = m.palette.Show(m.paletteCommands())
	next, _ := m.Update(tea.KeyPressMsg{Code: 'd', Text: "diff / recorded source"})
	m = next.(rootModel)
	selected, ok := m.palette.Selected()
	if !ok || selected.Key != "v" {
		t.Fatalf("palette selected=%+v", selected)
	}
	next, cmd := m.Update(makeKey("enter"))
	m = next.(rootModel)
	if cmd == nil {
		t.Fatal("palette did not dispatch source action")
	}
	next, _ = m.Update(cmd())
	m = next.(rootModel)
	if !strings.Contains(m.monitor.View(), "Choose recorded edit") {
		t.Fatal("source picker did not open")
	}
}
