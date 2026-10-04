package monitor

import (
	"fmt"
	"slices"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// selectionPicker is a bounded, keyboard-only selection surface. Its row
// indexes belong to the caller's immutable options, never transcript rows.
// File/source selection can share its movement, help and rendering contract.
type selectionPicker struct {
	title  string
	noun   string
	prompt string
	rows   []selectionRow
	cursor int
}
type selectionRow struct{ label, disabled string }

type locationPickerState struct {
	id      uint64
	owner   shared.OpenLocationOwner
	picker  selectionPicker
	targets []locationCandidate
	loading bool
}
type locationTargetsCheckedMsg struct {
	id      uint64
	owner   shared.OpenLocationOwner
	targets []locationCandidate
}

func (m Model) startLocationPicker(targets []locationCandidate) (Model, tea.Cmd) {
	m.locationPickerID++
	owner, id := m.OpenLocationOwner(), m.locationPickerID
	m.locationPicker = &locationPickerState{id: id, owner: owner, targets: targets, loading: true, picker: locationSelection(targets, true)}
	// Stat only already-loaded candidates, off the update/render loop. No reader
	// is opened and no current snapshot or working directory is consulted.
	return m, func() tea.Msg {
		checked := slices.Clone(targets)
		for i := range checked {
			if checked[i].unavailable == "" {
				if err := shared.ValidateOpenLocationPath(checked[i].target.Path); err != nil {
					checked[i].unavailable = err.Error()
				}
			}
		}
		return locationTargetsCheckedMsg{id: id, owner: owner, targets: checked}
	}
}

func locationSelection(targets []locationCandidate, loading bool) selectionPicker {
	picker := selectionPicker{title: "Open current local file"}
	for _, target := range targets {
		label := target.target.Path
		if label == "" {
			label = "(empty path)"
		}
		if target.target.Line > 0 {
			label += fmt.Sprintf(" · line %d", target.target.Line)
		}
		if target.target.Column > 0 {
			label += fmt.Sprintf(" · column %d", target.target.Column)
		}
		reason := target.unavailable
		if loading && reason == "" {
			reason = "checking file"
		}
		picker.rows = append(picker.rows, selectionRow{label: label, disabled: reason})
	}
	return picker
}

func (m Model) checkedLocationTargets(msg locationTargetsCheckedMsg) (Model, tea.Cmd) {
	if m.locationPicker == nil || m.locationPicker.id != msg.id || m.locationPicker.owner != msg.owner || m.OpenLocationOwner() != msg.owner {
		return m, nil
	}
	state := *m.locationPicker
	cursor := state.picker.cursor
	state.targets = msg.targets
	state.loading = false
	state.picker = locationSelection(msg.targets, false)
	state.picker.cursor = min(cursor, max(0, len(state.picker.rows)-1))
	m.locationPicker = &state
	return m, nil
}

func (m Model) updateLocationPicker(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	state := *m.locationPicker
	switch {
	case keybind.Matches(msg, m.keys.PickerCancel):
		m.locationPicker = nil
		return m, nil
	case keybind.Matches(msg, m.keys.PickerConfirm):
		if state.loading || state.picker.cursor < 0 || state.picker.cursor >= len(state.targets) {
			return m, nil
		}
		target := state.targets[state.picker.cursor]
		if target.unavailable != "" {
			return m, nil
		}
		m.locationPicker = nil
		return m, locationRequestCmd(state.owner, target)
	default:
		state.picker = state.picker.move(msg, m.keys)
	}
	m.locationPicker = &state
	return m, nil
}

func (p selectionPicker) move(msg tea.KeyPressMsg, keys monitorKeys) selectionPicker {
	switch {
	case keybind.Matches(msg, keys.Down):
		p.cursor = min(p.cursor+1, len(p.rows)-1)
	case keybind.Matches(msg, keys.Up):
		p.cursor = max(p.cursor-1, 0)
	}
	return p
}

func (m Model) pickerHelp() []shared.HelpSection {
	confirm := m.keys.PickerConfirm
	state := m.locationPicker
	confirm.SetEnabled(!state.loading && state.picker.cursor >= 0 && state.picker.cursor < len(state.picker.rows) && state.picker.rows[state.picker.cursor].disabled == "")
	return []shared.HelpSection{{Title: "Choose recorded target", Bindings: []keybind.Binding{m.keys.Down, m.keys.Up, confirm, m.keys.PickerCancel}}}
}

func (p selectionPicker) overlay(base string, width, height int, keys monitorKeys) string {
	if width <= 0 || height <= 0 {
		return base
	}
	w := min(width, 100)
	hframe, vframe := shared.PanelFrame()
	innerW := max(0, w-hframe)
	h := min(height, min(8, len(p.rows))+5+vframe)
	innerH := max(0, h-vframe)
	// Chrome is bounded too; at very small heights the canvas clips it safely.
	rows := min(len(p.rows), max(0, innerH-5))
	start := max(0, p.cursor-rows+1)
	var lines []string
	noun := p.noun
	if noun == "" {
		noun = "Target"
	}
	lines = append(lines, fmt.Sprintf("%s %d of %d", noun, p.cursor+1, len(p.rows)))
	for i := start; i < min(len(p.rows), start+rows); i++ {
		prefix := "  "
		if i == p.cursor {
			prefix = "> "
		}
		if p.rows[i].disabled == "checking file" {
			prefix += "[checking] "
		} else if p.rows[i].disabled != "" {
			prefix += "[unavailable] "
		}
		label := sanitizeToolSummary(p.rows[i].label)
		budget := max(0, innerW-ansi.StringWidth(prefix))
		if ansi.StringWidth(label) > budget {
			label = ansi.TruncateLeft(label, ansi.StringWidth(label)-max(0, budget-ansi.StringWidth(shared.EllipsisGlyph)), shared.EllipsisGlyph)
		}
		line := ansi.Truncate(prefix+label, innerW, shared.EllipsisGlyph)
		if i == p.cursor {
			line = shared.Theme.Marker.Render(line)
		}
		lines = append(lines, line)
	}
	if p.cursor >= 0 && p.cursor < len(p.rows) {
		// Keep the reason separate from the path so long paths cannot hide it.
		reason := p.rows[p.cursor].disabled
		if reason == "" {
			reason = p.prompt
			if reason == "" {
				reason = "Open the current file at this recorded target"
			}
		}
		lines = append(lines, "", ansi.Truncate(reason, innerW, shared.EllipsisGlyph))
	}
	confirm := keys.PickerConfirm
	confirm.SetEnabled(p.cursor >= 0 && p.cursor < len(p.rows) && p.rows[p.cursor].disabled == "")
	hints := shared.CompactHint(innerW, keys.PickerCancel, keys.Down, keys.Up, confirm)
	lines = append(lines, hints)
	body := fitBlock(strings.Join(lines, "\n"), innerW, innerH)
	box := shared.Panel(p.title, body, w, h, true)
	comp := lipgloss.NewCompositor(lipgloss.NewLayer(base), lipgloss.NewLayer(box).X(max(0, (width-w)/2)).Y(max(0, (height-h)/2)).Z(1))
	return lipgloss.NewCanvas(width, height).Compose(comp).Render()
}
