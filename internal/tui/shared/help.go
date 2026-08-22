package shared

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

type helpOverlayLayout struct {
	box       string
	maxOffset int
	pageSize  int
}

func renderHelpSection(sec HelpSection, maxWidth int) string {
	keyW := 0
	for _, binding := range sec.Bindings {
		if !binding.Enabled() {
			continue
		}
		if width := lipgloss.Width(binding.Help().Key); width > keyW {
			keyW = width
		}
	}
	rows := make([]string, 0, len(sec.Bindings)+1)
	rows = append(rows, Theme.Help.Section.Render(sec.Title))
	for _, binding := range sec.Bindings {
		if !binding.Enabled() {
			continue
		}
		help := binding.Help()
		key := Theme.Help.Key.Render(PadRight(help.Key, keyW))
		row := "  " + key + "  " + Theme.Help.Desc.Render(help.Desc)
		if maxWidth > 0 {
			row = ansi.Truncate(row, maxWidth, "")
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func helpSectionRows(sections []HelpSection, maxWidth int) []string {
	const columnGap = 3
	var rows []string
	var current string
	for _, sec := range sections {
		enabled := false
		for _, binding := range sec.Bindings {
			if binding.Enabled() {
				enabled = true
				break
			}
		}
		if !enabled {
			continue
		}
		block := renderHelpSection(sec, maxWidth)
		if current == "" {
			current = block
			continue
		}
		if lipgloss.Width(current)+columnGap+lipgloss.Width(block) <= maxWidth {
			current = lipgloss.JoinHorizontal(
				lipgloss.Top,
				current,
				strings.Repeat(" ", columnGap),
				block,
			)
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, strings.Split(current, "\n")...)
		current = block
	}
	if current != "" {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, strings.Split(current, "\n")...)
	}
	return rows
}

func buildHelpOverlay(width, height int, sections []HelpSection, offset int) helpOverlayLayout {
	boxFrameW := Theme.Help.Box.GetHorizontalFrameSize()
	boxFrameH := Theme.Help.Box.GetVerticalFrameSize()
	maxInnerW := max(width-4-boxFrameW, 1)
	maxInnerH := max(height-2-boxFrameH, 1)
	bodyRows := helpSectionRows(sections, maxInnerW)

	// Title, spacing, and controls remain fixed while only the section grid
	// scrolls, so users never lose the modal's identity or escape route.
	pageSize := max(maxInnerH-4, 1)
	maxOffset := max(len(bodyRows)-pageSize, 0)
	offset = min(max(offset, 0), maxOffset)
	end := min(offset+pageSize, len(bodyRows))
	visible := bodyRows[offset:end]

	control := "?/F1/esc close"
	if maxOffset > 0 {
		control += " · ↑/↓ scroll · pgup/pgdn · home/end"
	}
	title := ansi.Truncate(Theme.Help.Title.Render("jig · help"), maxInnerW, "")
	control = ansi.Truncate(Theme.Help.Desc.Render(control), maxInnerW, "")
	content := []string{title, ""}
	content = append(content, visible...)
	content = append(content, "", control)

	return helpOverlayLayout{
		box:       Theme.Help.Box.Render(strings.Join(content, "\n")),
		maxOffset: maxOffset,
		pageSize:  pageSize,
	}
}

func HelpOverlayMaxOffset(width, height int, sections []HelpSection) int {
	return buildHelpOverlay(width, height, sections, 0).maxOffset
}

func HelpOverlayPageSize(width, height int, sections []HelpSection) int {
	return buildHelpOverlay(width, height, sections, 0).pageSize
}

// RenderHelpOverlay keeps complete bindings in a responsive column grid and
// scrolls that grid inside a fixed-height modal when the terminal is short.
func RenderHelpOverlay(base string, width, height int, sections []HelpSection, offset int) string {
	box := buildHelpOverlay(width, height, sections, offset).box

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
