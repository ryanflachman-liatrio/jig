package monitor

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func longRecordedSource() string {
	return "FIRST\n\n" + strings.Repeat("unchanged context\n", 30) + "MIDDLE\n" + strings.Repeat("雪é👩‍💻", 900) + "\n\nFINAL"
}
func altSource(r rune) tea.KeyPressMsg {
	if r == 'G' {
		return tea.KeyPressMsg{Code: 'g', Mod: tea.ModAlt | tea.ModShift, Text: "G"}
	}
	return tea.KeyPressMsg{Code: r, Mod: tea.ModAlt, Text: string(r)}
}

func TestTranscriptSourceWindowCoverage(t *testing.T) {
	for _, width := range []int{0, 1, 2, 13, 80, 5000} {
		for name, raw := range map[string]string{
			"empty": "", "blank": "\n\n\n", "final": "a\nlast", "boundary": strings.Repeat("x", 4096) + "\nend",
			"rows": strings.Repeat("a\n", 12) + "last", "unicode": longRecordedSource(),
			"combining": "a" + strings.Repeat("́", 5000) + "end",
			"controls":  "start\x1b]52;c;" + strings.Repeat("secret", 1000) + "\a\x1b[31mred\x1b[0m\r\n\t雪\x00end",
		} {
			t.Run(fmt.Sprintf("%s/%d", name, width), func(t *testing.T) {
				idx := indexSourceWindows(raw, width)
				if width == 0 || raw == "" {
					if len(idx.windows) != 0 {
						t.Fatal("zero-width/empty scanned")
					}
					return
				}
				end := 0
				for i, w := range idx.windows {
					if w.start != end || w.end <= w.start || w.end-w.start > 4096 || !utf8.ValidString(raw[w.start:w.end]) {
						t.Fatalf("invalid raw span %d: %+v", i, w)
					}
					text := idx.text(i)
					if len(text) > 4096 || strings.Count(text, "\n")+1 > 12 || strings.Count(text, "\n")+1 != w.rows {
						t.Fatalf("unbounded rows/bytes %d: %+v, bytes=%d rows=%d", i, w, len(text), strings.Count(text, "\n")+1)
					}
					for _, row := range strings.Split(text, "\n") {
						if ansi.StringWidth(row) > width {
							t.Fatalf("row width %d > %d", ansi.StringWidth(row), width)
						}
					}
					if strings.ContainsAny(text, "\x1b\x00\r\t") || name == "controls" && strings.Contains(text, "secret") {
						t.Fatalf("unsafe display %q", text)
					}
					if idx.at(w.start) != i || idx.at(w.end-1) != i {
						t.Fatal("anchor doesn't select containing span")
					}
					end = w.end
				}
				if end != len(raw) {
					t.Fatal("source tail unreachable")
				}
				if width >= 13 && name == "unicode" && (!strings.Contains(idx.display, "FIRST") || !strings.Contains(idx.display, "MIDDLE") || !strings.Contains(idx.display, "FINAL")) {
					t.Fatal("marker lost")
				}
				if name == "blank" && idx.text(0) != "\n\n" {
					t.Fatalf("blank rows lost: %q", idx.text(0))
				}
			})
		}
	}
}

func TestTranscriptSourceWindowNavigationAndWork(t *testing.T) {
	raw := longRecordedSource()
	m := newTranscriptClickModel(t, diffExchangeEntries("a", "a.go", nil, raw))
	m, _ = m.Update(key("v"))
	item, _ := m.selectedTranscriptItem()
	owner := m.sourceOwner(item)
	cache := m.sourceWindows[sourceWindowKey{owner, 0}]
	idx := cache.index
	calls := 0
	m.sourceHighlight = func(path, code string) (string, error) {
		calls++
		if len(code) > 4096 || strings.Count(code, "\n")+1 > 12 {
			t.Fatal("unbounded highlighter input")
		}
		return highlightRecordedSource(path, code)
	}
	cache.renderedWindow = -1
	m.setChatContent()
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	for i, w := range idx.windows {
		if i > 0 {
			before := calls
			var cmd tea.Cmd
			m, cmd = m.Update(altSource('j'))
			if cmd != nil || calls-before != 1 {
				t.Fatalf("effects or highlighter work: cmd=%v calls=%d", cmd != nil, calls-before)
			}
		}
		if m.sourceStates[owner].offset != w.start || m.chatAutoScroll {
			t.Fatal("navigation offset or follow incorrect")
		}
		if m.sourceWindows[sourceWindowKey{owner, 0}].index != idx {
			t.Fatal("index rebuilt on navigation")
		}
		before := calls
		m.setChatContent()
		if calls != before {
			t.Fatal("unchanged window highlighted again")
		}
	}
	for i := len(idx.windows) - 1; i >= 0; i-- {
		if m.sourceStates[owner].offset != idx.windows[i].start {
			t.Fatal("previous differs from next")
		}
		m, _ = m.Update(altSource('k'))
	}
	m, _ = m.Update(altSource('G'))
	if m.sourceStates[owner].offset != idx.windows[len(idx.windows)-1].start || m.chatAutoScroll {
		t.Fatal("alt+G triggered follow or missed last")
	}
	m, _ = m.Update(altSource('g'))
	if m.sourceStates[owner].offset != 0 {
		t.Fatal("first failed")
	}
	// Same index/highlight is retained at a boundary; no full-source work.
	before := calls
	m.chatAutoScroll = true
	m, _ = m.Update(altSource('k'))
	if calls != before || m.chatAutoScroll {
		t.Fatal("boundary work/follow")
	}
	m, _ = m.Update(key("G"))
	if !m.chatAutoScroll {
		t.Fatal("ordinary follow lost")
	}

}

func TestTranscriptSourceWindowLiteralRowsAndWarnings(t *testing.T) {
	raw := "\n\npackage p\n\n\nvar x = 1\n\n"
	entries := diffExchangeEntries("a", "a.go", nil, raw)
	entries[0].Blocks[0].Truncated = true
	m := newTranscriptClickModel(t, entries)
	m, _ = m.Update(key("v"))
	item, _ := m.selectedTranscriptItem()
	cache := m.sourceWindows[sourceWindowKey{m.sourceOwner(item), 0}]
	if got := ansi.Strip(cache.render(0, "a.go", highlightRecordedSource)); got != "\n\npackage p\n\n\nvar x = 1\n" {
		t.Fatalf("blank rows changed: %q", got)
	}
	body := ansi.Strip(m.itemTranscriptBody())
	if strings.Index(body, "Recorded content truncated") > strings.Index(body, "Lines 1") || !strings.Contains(body, "Lines 1–7") {
		t.Fatal(body)
	}
}

func TestTranscriptSourceStateRetention(t *testing.T) {
	entries := toolGroupEntries(diffExchangeEntries("a", "same.go", nil, longRecordedSource()), diffExchangeEntries("b", "same.go", nil, "sibling"))
	m := newTranscriptClickModel(t, entries)
	m, _ = m.Update(key("v"))
	m, _ = m.Update(altSource('j'))
	item, _ := m.selectedTranscriptItem()
	owner := m.sourceOwner(item)
	want := m.sourceStates[owner]
	for _, size := range [][2]int{{60, 20}, {15, 8}, {1, 1}, {120, 40}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if m.sourceStates[owner] != want {
			t.Fatal("resize changed raw anchor")
		}
		if cache := m.sourceWindows[sourceWindowKey{owner, 0}]; len(cache.index.windows) > 0 {
			w := cache.index.windows[cache.index.at(want.offset)]
			if want.offset < w.start || want.offset >= w.end {
				t.Fatal("reflow lost anchor")
			}
		}
	}
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("enter"))
	m.searchQuery = "no-match"
	m.rebuildTranscriptItemState(item.key)
	m.setChatContent()
	if m.sourceStates[owner] != want {
		t.Fatal("hidden loaded state lost")
	}
	m.searchQuery = ""
	m.rebuildTranscriptItemState(item.key)
	m.setChatContent()
	m.toggleCompactToolGroups()
	m.refreshPanels()
	if m.sourceStates[owner] != want {
		t.Fatal("grouping lost anchor")
	}
	m.toggleCompactToolGroups()
	m.refreshPanels()
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()
	if m.sourceStates[owner] != want {
		t.Fatal("page refresh lost anchor")
	}
	m, _ = m.Update(clickTranscriptItem(t, m, item.key))
	selected, _ := m.selectedTranscriptItem()
	if selected.key != item.key {
		t.Fatal("hit testing lost item")
	}
	entries[1].Blocks[0].Tool.Content[0].Diff.NewText = "replacement"
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()
	if _, ok := m.sourceStates[owner]; ok {
		t.Fatal("changed payload retained offset")
	}
	m.setChatPage(transcript.Page{})
	if len(m.sourceWindows) != 0 || len(m.sourceStates) != 0 || len(m.sourceDiffs) != 0 {
		t.Fatal("evicted state/cache retained")
	}
}

func TestTranscriptSourceIntegrationLiveCopyAndInput(t *testing.T) {
	raw := longRecordedSource()
	m := newLiveModel(t, diffExchangeEntries("a", "a.go", nil, raw))
	m, _ = m.Update(key("v"))
	m, _ = m.Update(altSource('j'))
	item, _ := m.selectedTranscriptItem()
	owner := m.sourceOwner(item)
	want := m.sourceStates[owner]
	copyItem := func(m Model) string {
		t.Helper()
		result := m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader()
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		return result.Payload
	}
	copied := copyItem(m)
	m, _ = m.Update(tea.Raw("synthetic clipboard emission")())
	if m.chatAutoScroll || m.sourceStates[owner] != want {
		t.Fatal("non-key clipboard emission resumed follow")
	}
	m = sendDelta(m, "a", "live text cannot replace recorded source")
	m = flushFrame(m)
	if m.sourceStates[owner] != want || m.chatAutoScroll || m.sourceSections(item)[0].NewText != raw {
		t.Fatal("live delta replaced history")
	}
	m, _ = m.Update(altSource('G'))
	if copyItem(m) != copied {
		t.Fatal("window changed copy")
	}
	m, _ = m.Update(key("j"))
	if m.sourceStates[owner].offset == 0 {
		t.Fatal("ordinary scroll changed source")
	}
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
			before := n.sourceStates[owner]
			for _, r := range "jkgG" {
				n, _ = n.Update(altSource(r))
				if n.sourceStates[owner] != before {
					t.Fatal("source navigation escaped input owner")
				}
			}
		})
	}
	for _, section := range m.HelpSections() {
		for _, binding := range section.Bindings {
			if strings.HasPrefix(binding.Help().Key, "alt+") && !binding.Enabled() {
				t.Fatal("available source navigation hidden")
			}
		}
	}
	m.chatStep = "another-step"
	m.pruneSourceState()
	if len(m.sourceStates) != 0 || len(m.sourceWindows) != 0 {
		t.Fatal("step replacement kept state")
	}
}

func TestTranscriptSourceStateSectionsAndBoundedCaches(t *testing.T) {
	entries := diffExchangeEntries("same", "same.go", nil, longRecordedSource())
	entries[1].Blocks[0].Tool.Content = append(entries[1].Blocks[0].Tool.Content, toolcall.Content{Diff: &toolcall.Diff{Path: "same.go", NewText: strings.Repeat("other section\n", 20)}})
	m := newTranscriptClickModel(t, entries)
	m, _ = m.Update(key("v"))
	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(altSource('G'))
	item, _ := m.selectedTranscriptItem()
	owner := m.sourceOwner(item)
	want := m.sourceStates[owner]
	if want.section != 1 || want.offset == 0 {
		t.Fatal("same-path sections merged")
	}
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.WindowSizeMsg{Width: 60 + i, Height: 20})
		if len(m.sourceWindows) > 2 || len(m.sourceDiffs) > 2 || len(m.sourceStates) != 1 {
			t.Fatal("cache grew with widths")
		}
	}
	if m.sourceStates[owner] != want {
		t.Fatal("resize changed section anchor")
	}
	// Content ordering is part of the payload version, even with equal paths.
	content := entries[1].Blocks[0].Tool.Content
	content[0], content[1] = content[1], content[0]
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()
	if _, ok := m.sourceStates[owner]; ok {
		t.Fatal("reordered sections reused position")
	}
	m.chatStep = "replacement"
	m.reloadTranscript()
	if len(m.sourceWindows) != 0 || len(m.sourceStates) != 0 || len(m.sourceDiffs) != 0 {
		t.Fatal("reload retained caches")
	}
}

func TestTranscriptSourceCopyEveryWindow(t *testing.T) {
	m := newTranscriptClickModel(t, diffExchangeEntries("a", "a.go", nil, longRecordedSource()))
	m, _ = m.Update(key("v"))
	item, _ := m.selectedTranscriptItem()
	owner := m.sourceOwner(item)
	copied := m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader().Payload
	idx := m.sourceWindows[sourceWindowKey{owner, 0}].index
	for range idx.windows {
		if got := m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader(); got.Err != nil || got.Payload != copied {
			t.Fatal("visible window narrowed copy")
		}
		m, _ = m.Update(altSource('j'))
	}
}

func TestTranscriptSourceStateFallbackCacheVersion(t *testing.T) {
	entries := diffExchangeEntries("same", "same.go", nil, "first\n")
	entries[1].Blocks[0].Tool.Content = append(entries[1].Blocks[0].Tool.Content, toolcall.Content{Diff: &toolcall.Diff{Path: "second.go", NewText: "second\n"}})
	m := newTranscriptClickModel(t, entries)
	m.itemTranscriptBody()
	if len(m.sourceWindows) != 2 || len(m.sourceStates) != 0 {
		t.Fatal("fixture did not cache default fallback sections")
	}
	entries[1].Blocks[0].Tool.Content = entries[1].Blocks[0].Tool.Content[:1]
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()
	if len(m.sourceWindows) != 1 || len(m.sourceDiffs) != 1 {
		t.Fatal("removed fallback section retained cache")
	}
}
