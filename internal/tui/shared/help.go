package shared

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// HelpSection is one titled group of bindings in the help overlay.
type HelpSection struct {
	Title    string
	Bindings []keybind.Binding
}

// HelpProvider is implemented by every screen model that contributes a help
// overlay. CapturesText reports whether the screen is currently capturing free
// text (a list filter, a gate textarea), in which case "?" is a literal
// character and must not open the overlay.
type HelpProvider interface {
	HelpSections() []HelpSection
	CapturesText() bool
}

// RenderConfirmOverlay composites a centered confirmation box over base using
// the same Compositor technique as RenderHelpOverlay.  title is rendered in
// the danger color; body lines follow; a fixed hint closes the box.
func RenderConfirmOverlay(base, title, body string, width, height int) string {
	var content strings.Builder
	content.WriteString(Theme.Error.Render(title))
	content.WriteString("\n\n")
	content.WriteString(Theme.Question.Render(body))
	content.WriteString("\n\n")
	content.WriteString(Theme.Help.Desc.Render("y to confirm · n or esc to cancel"))

	box := Theme.Help.Box.Render(content.String())

	x := (width - lipgloss.Width(box)) / 2
	y := (height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(width, height).Compose(comp).Render()
}

// RenderHelpOverlay composites the modal box centered over base — the live
// screen — using a lipgloss v2 Canvas so the underlying screen shows through
// around the box (the box layer draws on top; cells it does not cover keep the
// base). It renders straight from keybind.Binding structs and skips disabled
// ones, so the overlay can never advertise a key the screen does not accept.
func RenderHelpOverlay(base string, width, height int, sections []HelpSection) string {
	// Compute the key-column width across all enabled bindings so the two columns
	// align regardless of which section a row belongs to.
	keyW := 0
	for _, sec := range sections {
		for _, b := range sec.Bindings {
			if !b.Enabled() {
				continue
			}
			if k := lipgloss.Width(b.Help().Key); k > keyW {
				keyW = k
			}
		}
	}

	var body strings.Builder
	body.WriteString(Theme.Help.Title.Render("jig · help"))
	body.WriteString("\n")
	for _, sec := range sections {
		rows := make([]string, 0, len(sec.Bindings))
		for _, b := range sec.Bindings {
			if !b.Enabled() {
				continue
			}
			h := b.Help()
			key := Theme.Help.Key.Render(PadRight(h.Key, keyW))
			rows = append(rows, "  "+key+"  "+Theme.Help.Desc.Render(h.Desc))
		}
		if len(rows) == 0 {
			continue
		}
		body.WriteString("\n")
		body.WriteString(Theme.Help.Section.Render(sec.Title))
		body.WriteString("\n")
		body.WriteString(strings.Join(rows, "\n"))
		body.WriteString("\n")
	}
	body.WriteString("\n")
	body.WriteString(Theme.Help.Desc.Render("? or esc to close"))

	box := Theme.Help.Box.Render(body.String())

	// Center the box over the base screen. The base is layer z=0 (drawn first);
	// the box is placed on top at the centered offset, so everything the box does
	// not cover keeps showing the live screen beneath it.
	x := (width - lipgloss.Width(box)) / 2
	y := (height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	// A Compositor (not Canvas.Compose directly) is what honors per-layer x/y/z:
	// the base sits at the origin as z=0, the box is offset and drawn on top as
	// z=1. Draw onto a fixed width×height canvas so the result matches the screen.
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(width, height).Compose(comp).Render()
}
