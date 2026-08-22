package shared

import (
	"fmt"
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

func helpTestSection(title string, count int) HelpSection {
	bindings := make([]keybind.Binding, count)
	for i := range count {
		key := fmt.Sprintf("%d", i+1)
		bindings[i] = keybind.NewBinding(
			keybind.WithKeys(key),
			keybind.WithHelp(key, fmt.Sprintf("%s action %d", strings.ToLower(title), i+1)),
		)
	}
	return HelpSection{Title: title, Bindings: bindings}
}

func TestHelpOverlayUsesMultipleColumnsWhenTheyFit(t *testing.T) {
	layout := buildHelpOverlay(80, 24, []HelpSection{
		helpTestSection("Steps", 3),
		helpTestSection("Focus", 2),
		helpTestSection("Global", 2),
	}, 0)

	foundSharedRow := false
	for _, line := range strings.Split(layout.box, "\n") {
		if strings.Contains(line, "Steps") && strings.Contains(line, "Focus") {
			foundSharedRow = true
			break
		}
	}
	if !foundSharedRow {
		t.Fatalf("expected sections to share a column row:\n%s", layout.box)
	}
	if lipgloss.Width(layout.box) > 76 || lipgloss.Height(layout.box) > 22 {
		t.Fatalf("overlay exceeds bounded modal size: %dx%d", lipgloss.Width(layout.box), lipgloss.Height(layout.box))
	}
}

func TestHelpOverlayScrollsCompleteSectionGrid(t *testing.T) {
	sections := []HelpSection{
		helpTestSection("Transcript", 12),
		helpTestSection("Focus", 5),
		helpTestSection("Global", 4),
	}
	top := buildHelpOverlay(42, 12, sections, 0)
	if top.maxOffset == 0 {
		t.Fatal("expected short terminal to require scrolling")
	}
	if !strings.Contains(top.box, "Transcript") || strings.Contains(top.box, "global action 4") {
		t.Fatalf("unexpected first page:\n%s", top.box)
	}

	bottom := buildHelpOverlay(42, 12, sections, top.maxOffset)
	for _, want := range []string{"jig · help", "global action 4", "?/F1/esc close"} {
		if !strings.Contains(bottom.box, want) {
			t.Fatalf("last page missing %q:\n%s", want, bottom.box)
		}
	}
}
