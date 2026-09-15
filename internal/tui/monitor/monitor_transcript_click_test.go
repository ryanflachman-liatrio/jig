package monitor

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/transcript"
)

// clickTranscriptLine builds a MouseLeft click on the transcript panel that
// lands on the given body-line index. The Y math mirrors updateMouse:
// screen y = panels.transcript.y + line - chatVP.YOffset(). The x is anchored
// to the panel's leftmost content column so tests never accidentally hit the
// panel border.
func clickTranscriptLine(m Model, line int) tea.MouseClickMsg {
	panels := m.mousePanels()
	return tea.MouseClickMsg{
		X:      panels.transcript.x + 1,
		Y:      panels.transcript.y + line - m.chatVP.YOffset(),
		Button: tea.MouseLeft,
	}
}

// clickTranscriptItem builds a click that hits the header line of the given
// transcript item, using its recorded lineRange. This is the canonical way
// tests target a specific item without hard-coding its screen y.
func clickTranscriptItem(t *testing.T, m Model, key transcriptItemKey) tea.MouseClickMsg {
	t.Helper()
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: key}]
	if !ok {
		t.Fatalf("no line range recorded for item %+v; ranges=%+v", key, m.chatItemLineRanges)
	}
	return clickTranscriptLine(m, rng.start)
}

// clickTranscriptItemBody clicks on the last line of the item's range instead
// of the first. For a collapsed item its range often only spans the header;
// for an expanded item this hits the detail body — the "reader trap" line
// that must select without collapsing.
func clickTranscriptItemBody(t *testing.T, m Model, key transcriptItemKey) tea.MouseClickMsg {
	t.Helper()
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: key}]
	if !ok {
		t.Fatalf("no line range recorded for item %+v", key)
	}
	if rng.end <= rng.start {
		t.Fatalf("item %+v has no body line: range=%+v", key, rng)
	}
	return clickTranscriptLine(m, rng.end)
}

func newTranscriptClickModel(t *testing.T, entries []transcript.Entry) Model {
	t.Helper()
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()
	return m
}

func TestTranscriptClickToggleStandaloneExchange(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
	)
	m := newTranscriptClickModel(t, entries)
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolExchange {
		t.Fatalf("chat items=%+v, want one standalone exchange", m.chatItems)
	}
	item := m.chatItems[0]
	if m.chatItemExpand[item.key] {
		t.Fatal("standalone exchange started expanded; test premise wrong")
	}

	m.chatAutoScroll = true
	before := m.cursor
	m, cmd := m.Update(clickTranscriptItem(t, m, item.key))
	if cmd != nil {
		t.Fatalf("click cmd = %v, want nil", cmd)
	}
	if m.focus != focusTranscript {
		t.Fatalf("focus=%v, want focusTranscript", m.focus)
	}
	if m.cursor != before {
		t.Fatalf("transcript click moved Steps cursor from %d to %d", before, m.cursor)
	}
	if !m.chatItemExpand[item.key] {
		t.Fatal("click did not expand standalone exchange")
	}
	if m.chatAutoScroll {
		t.Fatal("click did not pause auto-scroll follow")
	}
	sel, ok := m.selectedTranscriptItem()
	if !ok || sel.key != item.key {
		t.Fatalf("selected item=%+v, want the clicked exchange", sel)
	}

	// Click the header of the expanded item — it should collapse.
	m, _ = m.Update(clickTranscriptItem(t, m, item.key))
	if m.chatItemExpand[item.key] {
		t.Fatal("second click on header did not collapse the exchange")
	}
}

func TestTranscriptClickExpandsCompactToolGroup(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "go vet ./..."}, 0, 0, 0, false),
	)
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.focus = focusTranscript
	m.chatStep = "a"
	m.compactToolGroups = true
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()

	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
		t.Fatalf("chat items=%+v, want one compact tool group", m.chatItems)
	}
	group := m.chatItems[0]
	if m.chatItemExpand[group.key] {
		t.Fatal("group started expanded; test premise wrong")
	}

	m, _ = m.Update(clickTranscriptItem(t, m, group.key))
	if !m.chatItemExpand[group.key] {
		t.Fatal("click on group header did not expand the group")
	}
	// After expansion, the group's cursor targets should include its members.
	if len(m.chatCursorTargets) != 1+len(group.groupMembers) {
		t.Fatalf("cursor targets=%d, want %d (group + %d children)", len(m.chatCursorTargets), 1+len(group.groupMembers), len(group.groupMembers))
	}
}

func TestTranscriptClickTogglesCompactToolGroupChild(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "go vet ./..."}, 0, 0, 0, false),
	)
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.focus = focusTranscript
	m.chatStep = "a"
	m.compactToolGroups = true
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()

	group := m.chatItems[0]
	if group.kind != transcriptItemToolGroup {
		t.Fatalf("want a compact tool group, got %+v", group)
	}
	// Expand the group first via the same click gesture.
	m, _ = m.Update(clickTranscriptItem(t, m, group.key))
	if !m.chatItemExpand[group.key] {
		t.Fatal("group did not expand on first click")
	}

	child := group.groupMembers[1]
	if m.chatItemExpand[child.key] {
		t.Fatal("child started expanded inside compact group; slice-15 default violated")
	}
	m, _ = m.Update(clickTranscriptItem(t, m, child.key))
	if !m.chatItemExpand[child.key] {
		t.Fatal("click on child did not toggle its detail")
	}
	if !m.chatItemExpand[group.key] {
		t.Fatal("clicking a child accidentally collapsed the enclosing group")
	}
	sel, ok := m.selectedTranscriptItem()
	if !ok || sel.key != child.key {
		t.Fatalf("selected item=%+v, want the clicked child", sel)
	}
}

func TestTranscriptClickBodyOfExpandedExchangeDoesNotCollapse(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
	)
	m := newTranscriptClickModel(t, entries)
	item := m.chatItems[0]

	// Expand first.
	m, _ = m.Update(clickTranscriptItem(t, m, item.key))
	if !m.chatItemExpand[item.key] {
		t.Fatal("setup: expand click did not open the exchange")
	}
	m.refreshPanels()

	// Click on a body line (not the header) of the now-expanded item.
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}]
	if !ok {
		t.Fatalf("no line range for expanded exchange: ranges=%+v", m.chatItemLineRanges)
	}
	if rng.end == rng.start {
		t.Fatalf("expanded exchange range covers only one line: %+v", rng)
	}
	m, _ = m.Update(clickTranscriptLine(m, rng.end))
	if !m.chatItemExpand[item.key] {
		t.Fatal("body click on expanded exchange collapsed it — reader trap regression")
	}
	sel, ok := m.selectedTranscriptItem()
	if !ok || sel.key != item.key {
		t.Fatalf("selected item=%+v, want the enclosing exchange", sel)
	}
}

func TestTranscriptClickReadGroupHeaderToggles(t *testing.T) {
	entries := append(
		renumberEntries(readExchange("one", "internal/alpha.go", 0, 0, 0), 1),
		renumberEntries(readExchange("two", "internal/beta.go", 0, 0, 0), 3)...,
	)
	m := newTranscriptClickModel(t, entries)
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemReadGroup {
		t.Fatalf("chat items=%+v, want one read group", m.chatItems)
	}
	group := m.chatItems[0]
	if m.chatItemExpand[group.key] {
		t.Fatal("read group started expanded; test premise wrong")
	}

	m, _ = m.Update(clickTranscriptItem(t, m, group.key))
	if !m.chatItemExpand[group.key] {
		t.Fatal("click on read group header did not expand")
	}
	m.refreshPanels()

	// Click on line 0 of the now-expanded group — expect collapse.
	m, _ = m.Update(clickTranscriptItem(t, m, group.key))
	if m.chatItemExpand[group.key] {
		t.Fatal("second header click did not collapse the read group")
	}
}

func TestTranscriptClickReadGroupBodyDoesNotCollapse(t *testing.T) {
	entries := append(
		renumberEntries(readExchange("one", "internal/alpha.go", 0, 0, 0), 1),
		renumberEntries(readExchange("two", "internal/beta.go", 0, 0, 0), 3)...,
	)
	m := newTranscriptClickModel(t, entries)
	group := m.chatItems[0]

	m, _ = m.Update(clickTranscriptItem(t, m, group.key))
	if !m.chatItemExpand[group.key] {
		t.Fatal("setup: read group did not expand on first click")
	}
	m.refreshPanels()

	// A body-line click should select-but-not-collapse.
	m, _ = m.Update(clickTranscriptItemBody(t, m, group.key))
	if !m.chatItemExpand[group.key] {
		t.Fatal("body click collapsed expanded read group — reader trap regression")
	}
}

func TestTranscriptClickSelectsShortTextWithoutToggling(t *testing.T) {
	entries := []transcript.Entry{{
		Seq:  1,
		Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{
			Type: transcript.BlockText,
			Text: "hello world",
		}},
	}}
	m := newTranscriptClickModel(t, entries)
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemText {
		t.Fatalf("chat items=%+v, want one short text", m.chatItems)
	}
	item := m.chatItems[0]
	if item.oversized {
		t.Fatal("short text should not be oversized")
	}
	beforeExpand := m.chatItemExpand[item.key]

	m.chatAutoScroll = true
	m, _ = m.Update(clickTranscriptItem(t, m, item.key))
	if m.chatItemExpand[item.key] != beforeExpand {
		t.Fatal("click on non-expandable text mutated expansion state")
	}
	sel, ok := m.selectedTranscriptItem()
	if !ok || sel.key != item.key {
		t.Fatalf("selected item=%+v, want the clicked text", sel)
	}
	if m.chatAutoScroll {
		t.Fatal("click on selectable non-expandable row did not pause auto-scroll")
	}
}

func TestTranscriptClickOnBlankSpacerIsFocusOnly(t *testing.T) {
	// Two exchanges separated by a coordinate boundary produce blank spacer
	// rows and per-turn metadata between them (see itemTranscriptBody).
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "one"}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "two"}, 0, 1, 0, false),
	)
	m := newTranscriptClickModel(t, entries)
	if len(m.chatItems) < 2 {
		t.Fatalf("chat items=%+v, want two exchanges", m.chatItems)
	}
	first := m.chatItems[0]
	firstRange, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: first.key}]
	if !ok {
		t.Fatalf("no range for first item; ranges=%+v", m.chatItemLineRanges)
	}
	// The itemTranscriptBody loop folds the metadata row's lines into the
	// preceding item's range end, then adds one blank spacer line before the
	// next item. Aim one past the folded end.
	blankLine := firstRange.end + 1

	m.focus = focusSteps
	m.chatAutoScroll = false
	prevScroll := m.chatVP.YOffset()
	prevExpand := make(map[transcriptItemKey]bool, len(m.chatItemExpand))
	for k, v := range m.chatItemExpand {
		prevExpand[k] = v
	}

	m, _ = m.Update(clickTranscriptLine(m, blankLine))
	if m.focus != focusTranscript {
		t.Fatalf("focus=%v, want focusTranscript after transcript click", m.focus)
	}
	// State that must not change on a no-hit click.
	if m.chatVP.YOffset() != prevScroll {
		t.Fatalf("no-hit click scrolled viewport: %d → %d", prevScroll, m.chatVP.YOffset())
	}
	for k, want := range prevExpand {
		if got := m.chatItemExpand[k]; got != want {
			t.Fatalf("no-hit click flipped chatItemExpand[%v]: %v → %v", k, want, got)
		}
	}
}

func TestTranscriptClickHonoursViewportOffset(t *testing.T) {
	// Enough tool exchanges to push the second item below the transcript
	// viewport's top when scrolled. Each exchange renders as a small card;
	// twenty is plenty.
	var exchanges [][]transcript.Entry
	for i := 0; i < 20; i++ {
		exchanges = append(exchanges, toolExchange(string(rune('a'+i)), "bash", map[string]any{"command": "cmd-" + string(rune('a'+i))}, 0, 0, 0, false))
	}
	entries := toolGroupEntries(exchanges...)
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	m.refreshPanels()

	// Scroll a few lines down and click. The click math should account for
	// the offset and still resolve to the correct item.
	m.chatVP.ScrollDown(6)
	m.chatAutoScroll = false

	target := m.chatItems[10] // middle-ish
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: target.key}]
	if !ok {
		t.Fatalf("no range for middle item; ranges=%+v", m.chatItemLineRanges)
	}
	// Skip if the middle item is above the visible viewport at this offset.
	panelY := rng.start - m.chatVP.YOffset()
	if panelY < 0 || panelY >= m.chatVP.Height() {
		t.Skipf("target line %d not visible at offset %d (panel height %d)", rng.start, m.chatVP.YOffset(), m.chatVP.Height())
	}

	m, _ = m.Update(clickTranscriptLine(m, rng.start))
	sel, ok := m.selectedTranscriptItem()
	if !ok || sel.key != target.key {
		t.Fatalf("nonzero-offset click resolved to %+v, want target item at line %d", sel, rng.start)
	}
	if !m.chatItemExpand[target.key] {
		t.Fatal("nonzero-offset click did not toggle expansion")
	}
}

func TestTranscriptClickInFileViewIsFocusOnly(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.focus = focusSteps
	// Enter file view via the Steps click path.
	m.stepFiles["a"] = []outputFile{{name: "proof.txt", path: "/synthetic/proof.txt", kind: kindOther}}
	m.expanded["a"] = true
	m.refreshPanels()
	panels := m.mousePanels()
	ranges := m.stepRowRanges()
	m, _ = m.Update(monitorClick(panels.steps.x, panels.steps.y+ranges[1].start))
	if m.selKind != "file" {
		t.Fatalf("setup: could not enter file view; selKind=%q", m.selKind)
	}
	prevFile := m.selFile
	prevOffset := m.chatVP.YOffset()

	// Click somewhere inside the transcript panel body.
	m, _ = m.Update(monitorClick(panels.transcript.x+2, panels.transcript.y+3))
	if m.focus != focusTranscript {
		t.Fatalf("focus=%v, want focusTranscript", m.focus)
	}
	if m.selKind != "file" || m.selFile != prevFile {
		t.Fatalf("click flipped file view: kind=%q file=%q", m.selKind, m.selFile)
	}
	if m.chatVP.YOffset() != prevOffset {
		t.Fatalf("click changed viewport offset in file view: %d → %d", prevOffset, m.chatVP.YOffset())
	}
}

func TestTranscriptClickIgnoresModifiedAndRightClicks(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
	)
	m := newTranscriptClickModel(t, entries)
	item := m.chatItems[0]
	prevExpand := m.chatItemExpand[item.key]

	// Right-click on the header must be ignored.
	rng := m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}]
	base := clickTranscriptLine(m, rng.start)
	right := tea.MouseClickMsg{X: base.X, Y: base.Y, Button: tea.MouseRight}
	m, _ = m.Update(right)
	if m.chatItemExpand[item.key] != prevExpand {
		t.Fatal("right click toggled expansion; only MouseLeft should act")
	}

	// Shift+left click on the header must be ignored (mod != 0).
	shifted := tea.MouseClickMsg{X: base.X, Y: base.Y, Button: tea.MouseLeft, Mod: tea.ModShift}
	m, _ = m.Update(shifted)
	if m.chatItemExpand[item.key] != prevExpand {
		t.Fatal("shift+left click toggled expansion; modified clicks should be ignored")
	}
}

func TestTranscriptClickExcludedWhileSearchOpen(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
	)
	m := newTranscriptClickModel(t, entries)
	item := m.chatItems[0]
	prevExpand := m.chatItemExpand[item.key]

	m.searchOpen = true
	msg := clickTranscriptItem(t, m, item.key)
	m, _ = m.Update(msg)
	if m.chatItemExpand[item.key] != prevExpand {
		t.Fatal("click reached transcript while search was open; mouseExcluded broken")
	}
}

// TestTranscriptClickBannerLineTargetsPreviousItem checks that boundary
// banner and per-turn metadata rows between two execution coordinates fold
// into the previous item's range end (see itemTranscriptBody), so a click
// there resolves to the previous item. When that item is already expanded,
// the click follows the reader-trap rule and does not re-collapse it.
func TestTranscriptClickBannerLineTargetsPreviousItem(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "one"}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "two"}, 0, 1, 0, false),
	)
	m := newTranscriptClickModel(t, entries)
	if len(m.chatItems) < 2 {
		t.Fatalf("chat items=%+v, want two exchanges", m.chatItems)
	}
	first := m.chatItems[0]

	// Expand the first exchange so the reader-trap rule applies to
	// subsequent body/banner clicks. This puts us in the interesting
	// state: any click on a folded-in banner row should select the
	// previous item without collapsing it.
	m, _ = m.Update(clickTranscriptItem(t, m, first.key))
	if !m.chatItemExpand[first.key] {
		t.Fatal("setup: header click did not expand the first exchange")
	}
	m.refreshPanels()

	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: first.key}]
	if !ok {
		t.Fatalf("no range for first item; ranges=%+v", m.chatItemLineRanges)
	}
	if rng.end <= rng.start+1 {
		t.Skip("boundary metadata did not fold extra rows on this build")
	}
	m, _ = m.Update(clickTranscriptLine(m, rng.end))
	sel, ok := m.selectedTranscriptItem()
	if !ok || sel.key != first.key {
		t.Fatalf("selected item=%+v, want first exchange after banner click", sel)
	}
	if !m.chatItemExpand[first.key] {
		t.Fatal("banner-line click collapsed the expanded previous item")
	}
}
