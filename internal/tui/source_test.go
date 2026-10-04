package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
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

func TestTranscriptSourceIntegrationEditorReturnAndPalette(t *testing.T) {
	m, _ := newOpenerFixture(t, t.Context(), "windows")
	// Exercise the same launcher effect without interactive terminal ownership;
	// the opt-in PTY fixture exercises ExecProcess handoff separately.
	noTerminal := false
	m.tuiConfig.Opener.Terminal = &noTerminal
	m.tuiConfig.Opener.Argv = m.tuiConfig.Opener.Argv[:len(m.tuiConfig.Opener.Argv)-1]
	m.monitor, _ = m.monitor.Update(makeKey("v"))
	m.monitor, _ = m.monitor.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	before := m.monitor.View()
	owner := m.monitor.OpenLocationOwner()
	nextRaw, _ := m.Update(tea.Raw("synthetic clipboard emission")())
	m = nextRaw.(rootModel)
	if m.monitor.View() != before {
		t.Fatal("clipboard emission changed source view/follow")
	}
	_, request := m.monitor.Update(makeKey("e"))
	if request == nil {
		t.Fatal("no opener request")
	}
	m, prepare := m.admitOpenLocation(request().(shared.OpenLocationRequest))
	m, launch := m.preparedOpenLocation(prepare().(openerPreparedMsg))
	if launch == nil {
		t.Fatal("no launch")
	}
	m, _ = m.completeOpenLocation(launch().(shared.OpenLocationResult))
	if m.monitor.View() != before || m.monitor.OpenLocationOwner() != owner {
		t.Fatal("editor return moved source window")
	}
	for _, overlay := range []string{"help", "delete", "leave", "palette"} {
		for _, r := range "jkgG" {
			t.Run(overlay+string(r), func(t *testing.T) {
				n := m
				switch overlay {
				case "help":
					n.showHelp = true
				case "delete":
					n.confirmDelete = true
				case "leave":
					n.leaveConfirm = true
				case "palette":
					n.palette = n.palette.Show(n.paletteCommands())
				}
				next, _ := n.Update(tea.KeyPressMsg{Code: r, Mod: tea.ModAlt})
				if next.(rootModel).monitor.View() != before {
					t.Fatal("root overlay leaked source navigation")
				}
			})
		}
	}

	m.palette = m.palette.Show(m.paletteCommands())
	next, _ := m.Update(tea.KeyPressMsg{Code: 'l', Text: "last recorded source window"})
	m = next.(rootModel)
	selected, ok := m.palette.Selected()
	if !ok || selected.Key != "alt+G" {
		t.Fatalf("palette action=%+v", selected)
	}
	next, cmd := m.Update(makeKey("enter"))
	m = next.(rootModel)
	if cmd == nil {
		t.Fatal("no palette dispatch")
	}
	next, _ = m.Update(cmd())
	m = next.(rootModel)
	if !strings.Contains(ansi.Strip(m.monitor.View()), "FINAL without newline") {
		t.Fatalf("palette didn't reach final recorded source:\n%s", ansi.Strip(m.monitor.View()))
	}
}
