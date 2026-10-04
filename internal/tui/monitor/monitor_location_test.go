package monitor

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func locationModel(t *testing.T) Model {
	t.Helper()
	line := 8
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "edit", Kind: "edit", Locations: []toolcall.Location{{Path: "/synthetic/雪 file.go", Line: &line}}}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "edit", Kind: "edit", Status: "completed", Content: []toolcall.Content{{Diff: &toolcall.Diff{Path: "/synthetic/雪 file.go", NewText: "recorded"}}}}}}},
	}
	return newTranscriptClickModel(t, entries)
}

func TestOpenLocationSingleSelection(t *testing.T) {
	m := locationModel(t)
	before := m.OpenLocationOwner()
	offset := m.chatVP.YOffset()
	expand := m.chatItemExpandAll
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmd == nil {
		t.Fatal("e did not dispatch")
	}
	req, ok := cmd().(shared.OpenLocationRequest)
	if !ok || req.Unavailable != "" || req.Target.Path != "/synthetic/雪 file.go" || req.Target.Line != 8 || req.Owner != before {
		t.Fatalf("request=%+v", req)
	}
	if updated.OpenLocationOwner() != before || updated.chatVP.YOffset() != offset || updated.chatItemExpandAll != expand {
		t.Fatal("open changed presentation")
	}
	// Messages carry values: mutating the old recorded location cannot redirect it.
	*m.chatEntries[0].Blocks[0].Tool.Locations[0].Line = 99
	if req.Target.Line != 8 {
		t.Fatal("request aliases source")
	}
	for _, sections := range [][]shared.HelpSection{updated.HelpSections(), updated.PaletteSections()} {
		found := false
		for _, sec := range sections {
			for _, binding := range sec.Bindings {
				if binding.Help().Key == "e" && strings.Contains(binding.Help().Desc, "open file") && binding.Enabled() {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("open action missing from help/palette")
		}
	}
	if monitor := New(m.RunID); monitor.OpenLocationOwner().Instance == before.Instance {
		t.Fatal("instance identity reused")
	}
}

func TestOpenLocationSingleMissingAndMultiple(t *testing.T) {
	m := locationModel(t)
	m.chatEntries[0].Blocks[0].Tool.Locations = nil
	m.chatEntries[1].Blocks[0].Tool.Content = nil
	_, cmd := m.openLocation()
	req := cmd().(shared.OpenLocationRequest)
	if !strings.Contains(req.Unavailable, "no recorded") {
		t.Fatalf("%+v", req)
	}
	m.chatEntries[0].Blocks[0].Tool.Locations = []toolcall.Location{{Path: "/one"}, {Path: "/two"}}
	updated, cmd := m.openLocation()
	if updated.locationPicker == nil || cmd == nil {
		t.Fatal("ambiguous selection silently launched")
	}
}

func TestOpenLocationInputOwnership(t *testing.T) {
	for _, mode := range []string{"search", "filter", "help", "diagnostics", "gate", "review", "file", "steps"} {
		t.Run(mode, func(t *testing.T) {
			m := locationModel(t)
			switch mode {
			case "search":
				m.RunDir = "/synthetic"
				m, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
			case "filter":
				m.filterOpen = true
			case "help":
				m.helpOpen = true
			case "diagnostics":
				m.showDiagnostics = true
			case "gate":
				m.focus = focusGate
			case "review":
				m.focus = focusGate
				m.reviewOpen = true
			case "file":
				m.selKind = "file"
			case "steps":
				m.focus = focusSteps
			}
			m, cmd := m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
			if cmd != nil {
				if _, ok := cmd().(shared.OpenLocationRequest); ok {
					t.Fatal("stole input")
				}
			}
			if mode == "search" && m.searchInput.Value() != "e" {
				t.Fatalf("search text=%q", m.searchInput.Value())
			}
		})
	}
	m := locationModel(t)
	before := m.OpenLocationOwner()
	m, cmd := m.Update(clickTranscriptItem(t, m, m.chatItems[0].key))
	if cmd != nil {
		t.Fatal("mouse launched editor")
	}
	if !reflect.DeepEqual(before, m.OpenLocationOwner()) {
		t.Fatal("click lost selection")
	}
}
