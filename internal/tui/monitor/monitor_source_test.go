package monitor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func TestTranscriptSourceModeIdentity(t *testing.T) {
	old := "before\n"
	entries := toolGroupEntries(diffExchangeEntries("reused", "same.go", &old, "first\n"), diffExchangeEntries("reused", "same.go", &old, "second\n"))
	entries[2].Attempt = 1
	entries[3].Attempt = 1
	m := newTranscriptClickModel(t, entries)
	m.sourceStates = make(map[sourceOwner]sourcePresentation)
	for _, item := range m.chatItems {
		m.sourceStates[m.sourceOwner(item)] = sourcePresentation{source: true, version: m.sourceVersion(item)}
	}
	if len(m.sourceStates) != 2 {
		t.Fatal("same path or tool ID merged distinct executions")
	}
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.sourceStates) != 2 {
		t.Fatal("unchanged loaded state lost")
	}
	entries[3].Blocks[0].Tool.Content[0].Diff.NewText = "replacement\n"
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.sourceStates) != 1 {
		t.Fatal("payload change retained stale selection")
	}
	m.setChatPage(transcript.Page{})
	if len(m.sourceStates) != 0 {
		t.Fatal("evicted state retained")
	}
}

func TestTranscriptSourceSelection(t *testing.T) {
	old := "before\n"
	first := diffExchangeEntries("first", "same.go", &old, "first\n")
	first[1].Blocks[0].Tool.Content = append(first[1].Blocks[0].Tool.Content, toolcall.Content{Diff: &toolcall.Diff{Path: "second.go", NewText: "second section\n"}})
	entries := toolGroupEntries(first, diffExchangeEntries("second", "same.go", &old, "other exchange\n"))
	m := newTranscriptClickModel(t, entries)
	m.toggleCompactToolGroups()
	m.compactToolNotice = ""
	m.refreshPanels()
	before, owner := m.View(), m.OpenLocationOwner()
	m, cmd := m.Update(key("v"))
	if cmd != nil || m.sourcePicker == nil || len(m.sourceStates) != 0 {
		t.Fatal("group bypassed selection")
	}
	m, _ = m.Update(key("enter"))
	if m.sourcePicker == nil || !m.sourcePicker.sections {
		t.Fatal("multi-file section picker missing")
	}
	m, _ = m.Update(key("esc"))
	if m.View() != before || m.OpenLocationOwner() != owner || len(m.sourceStates) != 0 {
		t.Fatal("cancel changed presentation")
	}
	m, _ = m.Update(key("v"))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("j"))
	m, cmd = m.Update(key("enter"))
	if cmd != nil || m.sourcePicker != nil || len(m.sourceStates) != 1 || m.chatAutoScroll {
		t.Fatal("selection effects invalid")
	}
	item, _ := m.selectedTranscriptItem()
	state := m.sourceStates[m.sourceOwner(item)]
	if !state.source || state.section != 1 || !m.chatItemExpand[item.key] {
		t.Fatalf("state=%+v", state)
	}
	m, _ = m.Update(key("v"))
	if m.sourceStates[m.sourceOwner(item)].source {
		t.Fatal("did not toggle back")
	}
	m, _ = m.Update(key("n"))
	m, _ = m.Update(key("v"))
	if len(m.sourceStates) != 2 {
		t.Fatal("group child affected sibling")
	}
}

func TestTranscriptSourceSelectionInput(t *testing.T) {
	old := "old"
	m := newTranscriptClickModel(t, diffExchangeEntries("e", "a.go", &old, "new"))
	for _, mode := range []string{"search", "filter", "file", "steps", "help", "diagnostics", "gate", "review"} {
		t.Run(mode, func(t *testing.T) {
			n := m
			switch mode {
			case "search":
				n.searchOpen = true
				n.searchInput = shared.NewInputTextarea("search", 20, 1, shared.WithoutBorder())
			case "filter":
				n.filterOpen = true
			case "file":
				n.selKind = "file"
			case "steps":
				n.focus = focusSteps
			case "help":
				n.helpOpen = true
			case "diagnostics":
				n.showDiagnostics = true
			case "gate":
				n.focus = focusGate
			case "review":
				n.focus = focusGate
				n.reviewOpen = true
			}
			n, _ = n.Update(key("v"))
			if len(n.sourceStates) > 0 || n.sourcePicker != nil {
				t.Fatal("source key escaped input owner")
			}
		})
	}
	m = newTranscriptClickModel(t, toolExchange("read", "read", nil, 0, 0, 0, false))
	if m.sourceEligible() {
		t.Fatal("non-edit eligible")
	}
	m, cmd := m.Update(key("v"))
	if cmd != nil || len(m.sourceStates) > 0 {
		t.Fatal("non-edit toggled")
	}
}

func TestTranscriptSourceModeRendering(t *testing.T) {
	old := "before\nunchanged context\n"
	same := "identical\n"
	big := strings.Repeat("x", diffComputeMaxBytes+1)
	for _, tc := range []struct {
		name  string
		old   *string
		new   string
		label string
		hint  string
	}{
		{"changed", &old, "after\nunchanged context\n", "Diff · a.go", ""},
		{"creation", nil, "created\n", "Recorded new source · a.go", ""},
		{"identical", &same, same, "Recorded new source · a.go", ""},
		{"oversize", &big, "new\n", "Recorded new source · a.go", "diff unavailable"},
		{"empty", nil, "", "Recorded new source · a.go", "Empty recorded result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTranscriptClickModel(t, diffExchangeEntries("edit", "a.go", tc.old, tc.new))
			before := stripANSI(m.itemTranscriptBody())
			if !strings.Contains(before, tc.label) || !strings.Contains(before, tc.hint) {
				t.Fatalf("default: %s", before)
			}
			item, _ := m.selectedTranscriptItem()
			m.chatItemExpand[item.key] = false
			m, cmd := m.Update(key("v"))
			after := stripANSI(m.itemTranscriptBody())
			if cmd != nil || !m.chatItemExpand[item.key] || !strings.Contains(after, "Recorded new source · a.go") || strings.Contains(after, "Diff · a.go") {
				t.Fatalf("source: %s", after)
			}
			for _, line := range strings.Split(strings.TrimSuffix(tc.new, "\n"), "\n") {
				if !strings.Contains(after, line) {
					t.Fatalf("missing recorded source line %q: %s", line, after)
				}
			}
			m, _ = m.Update(key("v"))
			if got := stripANSI(m.itemTranscriptBody()); got != before {
				t.Fatalf("toggle changed default\nbefore:%s\nafter:%s", before, got)
			}
		})
	}
	// A computation failure, separately from oversize, retains its explanation.
	m := newMonitorWithSteps(t)
	var b strings.Builder
	writeRecordedEditSection(&m, &b, transcriptItem{}, 0, &toolcall.Diff{Path: "a.go", NewText: "recorded"}, nil, computeFailed, false, false, detailAnchorHead)
	if !strings.Contains(stripANSI(b.String()), "diff unavailable") || !strings.Contains(stripANSI(b.String()), "recorded") {
		t.Fatal(b.String())
	}
}

func TestTranscriptSourceModeUseContent(t *testing.T) {
	entries := diffExchangeEntries("e", "request.go", nil, "request evidence")
	entries[0].Blocks[0].Tool.Content = entries[1].Blocks[0].Tool.Content
	entries[1].Blocks[0].Tool.Content = nil
	m := newTranscriptClickModel(t, entries)
	m, _ = m.Update(key("v"))
	if !strings.Contains(stripANSI(m.itemTranscriptBody()), "request evidence") {
		t.Fatal("result without content hid structured request")
	}
}

func TestTranscriptSourceEvidence(t *testing.T) {
	for _, flags := range [][2]bool{{true, false}, {false, true}, {true, true}, {false, false}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			old := "old"
			entries := diffExchangeEntries("reused", "a.go", &old, "captured")
			entries[0].Blocks[0].Truncated = flags[0]
			entries[1].Blocks[0].Truncated = flags[1]
			other := diffExchangeEntries("reused", "a.go", &old, "another attempt")
			other[0].Attempt = 1
			other[1].Attempt = 1
			other[0].Blocks[0].Truncated = true
			m := newTranscriptClickModel(t, toolGroupEntries(entries, other))
			m, _ = m.Update(key("v"))
			item, _ := m.selectedTranscriptItem()
			var b strings.Builder
			m.writeTranscriptItem(&b, item, true)
			text := stripANSI(b.String())
			warned := strings.Contains(text, "Recorded content truncated")
			if warned != (flags[0] || flags[1]) {
				t.Fatalf("wrong coordinate warning: %s", text)
			}
			if warned && strings.Index(text, "Recorded content truncated") > strings.Index(text, "captured") {
				t.Fatal("warning below evidence")
			}
		})
	}
}

func TestTranscriptSourceEvidenceSafeHighlightFallback(t *testing.T) {
	raw := "start\x1b]52;c;payload\a\x1b[31mred\x1b[0m\r\n\t雪\x00end"
	m := newTranscriptClickModel(t, diffExchangeEntries("e", "a.go", nil, raw))
	m, _ = m.Update(key("v"))
	text := m.itemTranscriptBody()
	if strings.Contains(text, "payload") || strings.Contains(text, "\x00") || strings.Contains(text, "\x1b]52") {
		t.Fatalf("unsafe output: %q", text)
	}
	item, _ := m.selectedTranscriptItem()
	if m.sourceSections(item)[0].NewText != raw {
		t.Fatal("mutated raw evidence")
	}
	cache := &sourceWindowCache{index: indexSourceWindows(raw, 60), renderedWindow: -1}
	called := false
	rendered := cache.render(0, "a.go", func(_ string, input string) (string, error) {
		called = true
		if strings.Contains(input, "\x1b") || strings.Contains(input, "\x00") {
			t.Fatal("unsafe highlighter input")
		}
		return "", errors.New("synthetic highlighter failure")
	})
	if !called || !strings.Contains(stripANSI(rendered), "startred") || !strings.Contains(stripANSI(rendered), "雪�end") {
		t.Fatalf("fallback lost safe text: %q", rendered)
	}
}

func TestTranscriptSourceModeViewState(t *testing.T) {
	old := "before\n"
	entries := toolGroupEntries(diffExchangeEntries("a", "same.go", &old, "first"), diffExchangeEntries("b", "same.go", &old, "second"))
	m := newTranscriptClickModel(t, entries)
	m, _ = m.Update(key("v"))
	item, _ := m.selectedTranscriptItem()
	owner := m.sourceOwner(item)
	want := m.sourceStates[owner]
	for _, size := range [][2]int{{60, 20}, {120, 40}, {15, 8}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if m.sourceStates[owner] != want {
			t.Fatal("resize lost source mode")
		}
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("enter"))
	m.searchQuery = "no match"
	m.rebuildTranscriptItemState(item.key)
	m.setChatContent()
	if m.sourceStates[owner] != want {
		t.Fatal("filter lost loaded state")
	}
	m.searchQuery = ""
	m.rebuildTranscriptItemState(item.key)
	m.setChatContent()
	m.toggleCompactToolGroups()
	m.refreshPanels()
	if m.sourceStates[owner] != want {
		t.Fatal("grouping lost state")
	}
	m.toggleCompactToolGroups()
	m.refreshPanels()
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()
	if m.sourceStates[owner] != want {
		t.Fatal("reload lost unchanged state")
	}
	m, _ = m.Update(clickTranscriptItem(t, m, item.key))
	selected, _ := m.selectedTranscriptItem()
	if selected.key != item.key {
		t.Fatal("source representation broke hit testing")
	}
	// Source mode/section/version are represented in affected cache keys.
	for key := range m.chatItemRendered {
		if key.itemKey == item.key && key.source != want {
			t.Fatal("stale presentation cache")
		}
	}
	m.chatStep = "replacement-step"
	m.pruneSourceState()
	if len(m.sourceStates) != 0 {
		t.Fatal("replacement step reused state")
	}
}

func TestTranscriptSourceCopy(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "recorded.go")
	old := "before\n"
	entries := diffExchangeEntries("a", file, &old, "recorded unchanged context\nafter\n")
	if err := os.WriteFile(file, []byte("current disk"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript.jsonl")
	w, err := transcript.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	m := newTranscriptClickModel(t, entries)
	copyItem := func() string {
		t.Helper()
		payload := m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader()
		if payload.Err != nil {
			t.Fatal(payload.Err)
		}
		return payload.Payload
	}
	before := copyItem()
	whole, _, err := exportRecordedTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = m.Update(key("v"))
	rendered := m.itemTranscriptBody()
	for _, remove := range []bool{false, true} {
		if remove {
			err = os.Remove(file)
		} else {
			err = os.WriteFile(file, []byte("mutated disk"), 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		if m.itemTranscriptBody() != rendered || copyItem() != before {
			t.Fatal("disk changed recorded view or item copy")
		}
		after, _, err := exportRecordedTranscript(path)
		if err != nil || after != whole {
			t.Fatal("mode/disk changed whole-step copy")
		}
	}
	m, _ = m.Update(key("enter"))
	if copyItem() != before {
		t.Fatal("collapse changed copied evidence")
	}
	m, _ = m.Update(key("v"))
	if copyItem() != before {
		t.Fatal("diff mode changed copied evidence")
	}
}

func TestTranscriptSourceSelectionPickerOwnership(t *testing.T) {
	old := "old"
	entries := toolGroupEntries(diffExchangeEntries("a", "a.go", &old, "a"), diffExchangeEntries("b", "b.go", &old, "b"))
	m := newTranscriptClickModel(t, entries)
	m.toggleCompactToolGroups()
	m.refreshPanels()
	for _, sections := range [][]shared.HelpSection{m.HelpSections(), m.PaletteSections()} {
		found := false
		for _, section := range sections {
			for _, binding := range section.Bindings {
				if binding.Help().Key == "v" && binding.Enabled() {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("source mode missing from help/palette")
		}
	}
	m, _ = m.Update(key("v"))
	owner := m.OpenLocationOwner()
	for _, msg := range []tea.Msg{key("tab"), key("e"), tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft}, tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown}} {
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		if cmd != nil || m.sourcePicker == nil || m.OpenLocationOwner() != owner || m.sourcePicker.picker.cursor != 0 {
			t.Fatal("picker leaked input")
		}
	}
	m.setChatPage(transcript.Page{Entries: entries})
	if m.sourcePicker != nil {
		t.Fatal("picker retained stale page refs")
	}
}

func TestTranscriptSourceModeNestedWidth(t *testing.T) {
	entries := toolGroupEntries(diffExchangeEntries("a", strings.Repeat("long/", 20)+"a.go", nil, "package sample\n\nvar value = 1\n"), diffExchangeEntries("b", "b.go", nil, "second"))
	entries[1].Blocks[0].Truncated = true
	m := newTranscriptClickModel(t, entries)
	m.toggleCompactToolGroups()
	m.refreshPanels()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m, _ = m.Update(key("v"))
	m, _ = m.Update(key("enter"))
	body := m.itemTranscriptBody()
	for _, line := range strings.Split(body, "\n") {
		if ansi.StringWidth(line) > m.transcriptInnerW {
			t.Fatalf("nested source width %d > %d: %q", ansi.StringWidth(line), m.transcriptInnerW, line)
		}
	}
	if !strings.Contains(stripANSI(body), "Recorded content truncated") || !strings.Contains(stripANSI(body), "var value = 1") {
		t.Fatal("bounded chrome hid warning or source")
	}
}
