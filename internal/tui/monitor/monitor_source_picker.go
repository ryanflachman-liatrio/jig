package monitor

import (
	"fmt"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

type sourcePickerState struct {
	owner    shared.OpenLocationOwner
	picker   selectionPicker
	members  []transcriptItem
	item     transcriptItem
	sections bool
}

func (m Model) sourceEligible() bool {
	item, ok := m.selectedTranscriptItem()
	if !ok || m.selKind == "file" {
		return false
	}
	if len(m.sourceSections(item)) > 0 {
		return true
	}
	for _, member := range item.groupMembers {
		if len(m.sourceSections(member)) > 0 {
			return true
		}
	}
	return false
}

func (m Model) toggleSource() Model {
	item, ok := m.selectedTranscriptItem()
	if !ok || m.selKind == "file" {
		return m
	}
	if len(item.groupMembers) > 0 {
		state := &sourcePickerState{owner: m.OpenLocationOwner(), picker: selectionPicker{title: "Choose recorded edit", noun: "Edit", prompt: "Choose one exchange to change its presentation"}}
		for _, member := range item.groupMembers {
			sections := m.sourceSections(member)
			if len(sections) == 0 {
				continue
			}
			state.members = append(state.members, member)
			state.picker.rows = append(state.picker.rows, selectionRow{label: fmt.Sprintf("%d · %s", len(state.members), sections[0].Path)})
		}
		if len(state.members) > 0 {
			m.sourcePicker = state
		}
		return m
	}
	return m.chooseSourceSection(item, m.OpenLocationOwner())
}

func (m Model) chooseSourceSection(item transcriptItem, owner shared.OpenLocationOwner) Model {
	sections := m.sourceSections(item)
	if len(sections) == 0 {
		return m
	}
	if state := m.sourceStates[m.sourceOwner(item)]; state.source {
		return m.applySourceMode(item, false, state.section)
	}
	if len(sections) == 1 {
		return m.applySourceMode(item, true, 0)
	}
	state := &sourcePickerState{owner: owner, item: item, sections: true, picker: selectionPicker{title: "Choose recorded source", noun: "File", prompt: "Show this section's recorded new source"}}
	for i, d := range sections {
		state.picker.rows = append(state.picker.rows, selectionRow{label: fmt.Sprintf("%d · %s", i+1, d.Path)})
	}
	m.sourcePicker = state
	return m
}

func (m Model) applySourceMode(item transcriptItem, source bool, section int) Model {
	if m.sourceStates == nil {
		m.sourceStates = make(map[sourceOwner]sourcePresentation)
	}
	state := m.sourceStates[m.sourceOwner(item)]
	if state.section != section {
		state.offset = 0
	}
	state.source, state.section, state.version = source, section, m.sourceVersion(item)
	m.sourceStates[m.sourceOwner(item)] = state
	m.sourcePicker = nil
	m.chatItemExpand[item.key] = true
	for _, group := range m.chatItems {
		for _, member := range group.groupMembers {
			if member.key == item.key {
				m.chatItemExpand[group.key] = true
			}
		}
	}
	m.chatAutoScroll = false
	m.rebuildTranscriptItemState(item.key)
	m.setChatContent()
	m.ensureTranscriptItemCursorVisible()
	return m
}

func (m Model) sourcePickerKeys() monitorKeys {
	keys := m.keys
	keys.PickerConfirm = shared.Relabel(keys.PickerConfirm, "select")
	return keys
}

func (m Model) sourcePickerHelp() []shared.HelpSection {
	keys := m.sourcePickerKeys()
	return []shared.HelpSection{{Title: m.sourcePicker.picker.title, Bindings: []keybind.Binding{keys.Down, keys.Up, keys.PickerConfirm, keys.PickerCancel}}}
}

func (m Model) updateSourcePicker(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	state := *m.sourcePicker
	switch {
	case keybind.Matches(msg, m.keys.PickerCancel):
		m.sourcePicker = nil
	case keybind.Matches(msg, m.keys.PickerConfirm):
		if state.sections {
			return m.applySourceMode(state.item, true, state.picker.cursor), nil
		}
		return m.chooseSourceSection(state.members[state.picker.cursor], state.owner), nil
	default:
		state.picker = state.picker.move(msg, m.keys)
		m.sourcePicker = &state
	}
	return m, nil
}
