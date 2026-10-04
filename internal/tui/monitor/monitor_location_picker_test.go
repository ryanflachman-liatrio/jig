package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func locationPickerModel(t *testing.T, kind string, count int) Model {
	t.Helper()
	dir := t.TempDir()
	var exchanges [][]transcript.Entry
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("file %02d.go", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		exchanges = append(exchanges, originExchange(fmt.Sprint(i), kind, name, dir))
	}
	m := newTranscriptClickModel(t, toolGroupEntries(exchanges...))
	if kind != "read" {
		m.toggleCompactToolGroups()
		m.refreshPanels()
	}
	return m
}

func readyLocationPicker(t *testing.T, m Model) Model {
	t.Helper()
	updated, cmd := m.Update(key("e"))
	if updated.locationPicker == nil || cmd == nil {
		t.Fatal("multi-target selection did not open picker")
	}
	updated, _ = updated.Update(cmd())
	if updated.locationPicker == nil || updated.locationPicker.loading {
		t.Fatal("picker did not load")
	}
	return updated
}

func TestOpenLocationPickerSelectionAndCancel(t *testing.T) {
	m := locationPickerModel(t, "read", 3)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	owner, offset := m.OpenLocationOwner(), m.chatVP.YOffset()
	before := m.View()
	m = readyLocationPicker(t, m)
	if m.locationPicker.picker.rows[0].disabled != "" {
		t.Fatal(m.locationPicker.picker.rows[0].disabled)
	}
	if !strings.Contains(ansi.Strip(m.View()), "enter open target") {
		t.Fatal("narrow picker hides confirm hint")
	}
	if !strings.Contains(ansi.Strip(m.View()), "Target 1 of 3") {
		t.Fatal("picker missing target count")
	}
	m, _ = m.Update(key("j"))
	m, cmd := m.Update(key("esc"))
	if cmd != nil || m.locationPicker != nil || m.OpenLocationOwner() != owner || m.chatVP.YOffset() != offset || m.focus != focusTranscript || m.View() != before {
		t.Fatal("cancel changed presentation")
	}
	m = readyLocationPicker(t, m)
	want := m.locationPicker.targets[1].target.Path
	m, _ = m.Update(key("j"))
	m, cmd = m.Update(key("enter"))
	if cmd == nil || m.locationPicker != nil {
		t.Fatal("did not activate selected target")
	}
	req := cmd().(shared.OpenLocationRequest)
	if req.Target.Path != want || req.Owner != owner || m.chatVP.YOffset() != offset {
		t.Fatalf("selection=%+v", req)
	}
}

func TestOpenLocationPickerGroupChild(t *testing.T) {
	m := locationPickerModel(t, "edit", 3)
	if item, _ := m.selectedTranscriptItem(); item.kind != transcriptItemToolGroup {
		t.Fatalf("not grouped: %+v", item)
	}
	headerOwner := m.OpenLocationOwner()
	if len(m.selectedLocationTargets()) != 3 {
		t.Fatal("header lacks loaded members")
	}
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("n"))
	childOwner := m.OpenLocationOwner()
	if childOwner == headerOwner {
		t.Fatal("header and first child identities collide")
	}
	if len(m.selectedLocationTargets()) != 1 {
		t.Fatal("child contains siblings")
	}
	m, cmd := m.Update(key("e"))
	if m.locationPicker != nil || cmd == nil {
		t.Fatal("single child should open directly")
	}
	req := cmd().(shared.OpenLocationRequest)
	if req.Owner != childOwner || !strings.HasSuffix(req.Target.Path, "file 00.go") {
		t.Fatalf("child target=%+v", req)
	}
}

func TestOpenLocationPickerUnavailableAndStale(t *testing.T) {
	m := locationPickerModel(t, "read", 2)
	old := m.selectedLocationTargets()[0].target.Path
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	m, check := m.Update(key("e"))
	m, cmd := m.Update(key("enter"))
	if cmd != nil {
		t.Fatal("launched before validation")
	}
	delayed := check()
	m, _ = m.Update(delayed)
	if !strings.Contains(m.locationPicker.picker.rows[0].disabled, "unavailable") {
		t.Fatal("missing file enabled")
	}
	m, cmd = m.Update(key("enter"))
	if cmd != nil || m.locationPicker == nil {
		t.Fatal("invalid row silently selected valid sibling")
	}
	if m.pickerHelp()[0].Bindings[2].Enabled() {
		t.Fatal("disabled row advertises confirmation")
	}
	if !strings.Contains(ansi.Strip(m.View()), "file unavailable") {
		t.Fatal("missing disabled reason")
	}
	// Cancel/reopen the same owner: an older stat completion must not overwrite it.
	m, _ = m.Update(key("esc"))
	m, check = m.Update(key("e"))
	id := m.locationPicker.id
	m, _ = m.Update(delayed)
	if m.locationPicker.id != id || !m.locationPicker.loading {
		t.Fatal("stale completion applied")
	}
	m, _ = m.Update(check())
	m, _ = m.Update(key("j"))
	m, cmd = m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("valid sibling not explicitly selectable")
	}
	if req := cmd().(shared.OpenLocationRequest); strings.HasSuffix(req.Target.Path, "file 00.go") {
		t.Fatal("opened wrong sibling")
	}
	// Old page/step selection invalidates both picker and delayed results.
	m = locationPickerModel(t, "read", 2)
	m, check = m.Update(key("e"))
	m.setChatPage(transcript.Page{})
	m, _ = m.Update(check())
	if m.locationPicker != nil {
		t.Fatal("picker survived evicted selection")
	}
}

func TestOpenLocationPickerBoundsAndInput(t *testing.T) {
	m := readyLocationPicker(t, locationPickerModel(t, "read", 30))
	owner := m.OpenLocationOwner()
	for _, msg := range []tea.Msg{key("tab"), key("right"), key("e"), tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft}, tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown}} {
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		if cmd != nil || m.focus != focusTranscript || m.OpenLocationOwner() != owner || m.locationPicker.picker.cursor != 0 {
			t.Fatal("picker leaked input")
		}
	}
	for i := 0; i < 40; i++ {
		m, _ = m.Update(key("j"))
	}
	if m.locationPicker.picker.cursor != 29 {
		t.Fatal("cursor unbounded")
	}
	for _, size := range [][2]int{{120, 40}, {60, 20}, {15, 8}, {1, 1}, {0, 0}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View()
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("width %v: line width %d", size, ansi.StringWidth(line))
			}
		}
		if size[1] > 0 && len(strings.Split(view, "\n")) > size[1] {
			t.Fatalf("height overflow %v", size)
		}
		if m.locationPicker.picker.cursor != 29 {
			t.Fatal("resize lost cursor")
		}
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if !strings.Contains(ansi.Strip(m.View()), "Target 30 of 30") {
		t.Fatal("last option unreachable")
	}
	for i := 0; i < 40; i++ {
		m, _ = m.Update(key("k"))
	}
	if m.locationPicker.picker.cursor != 0 {
		t.Fatal("first option unreachable")
	}
}

func TestOpenLocationPickerAllUnavailable(t *testing.T) {
	m := locationPickerModel(t, "read", 2)
	for _, candidate := range m.selectedLocationTargets() {
		if err := os.Remove(candidate.target.Path); err != nil {
			t.Fatal(err)
		}
	}
	m = readyLocationPicker(t, m)
	for range 2 {
		var cmd tea.Cmd
		m, cmd = m.Update(key("enter"))
		if cmd != nil || m.locationPicker == nil {
			t.Fatal("all-invalid group launched")
		}
		m, _ = m.Update(key("j"))
	}
	m, _ = m.Update(key("esc"))
	if m.focus != focusTranscript {
		t.Fatal("cancel moved focus")
	}
}
