package monitor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

func readGroupInteractionPage() transcript.Page {
	reads := []struct {
		id, path, status string
		offset, limit    int
	}{
		{"one", "synthetic/one.go", "completed", 1, 2},
		{"two", "synthetic/two.go", "completed", 8, 1},
	}
	entries := readGroupFixture(reads...)
	for i := range entries {
		entries[i].Seq += 1
	}
	entries = append([]transcript.Entry{{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "before"}}}}, entries...)
	entries = append(entries, transcript.Entry{Seq: 6, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "after"}}})
	return transcript.Page{Entries: entries}
}

func interactionMonitor(t *testing.T) Model {
	t.Helper()
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(readGroupInteractionPage())
	if len(m.chatVisibleItems) != 3 || m.chatVisibleItems[1].kind != transcriptItemReadGroup {
		t.Fatalf("visible items = %+v, want text/group/text", m.chatVisibleItems)
	}
	return m
}

func TestReadGroupNavigation(t *testing.T) {
	m := interactionMonitor(t)
	m, _ = m.Update(key("n"))
	keyBefore := m.chatVisibleItems[m.chatItemCursor].key
	if m.chatItemCursor != 1 || keyBefore.kind != transcriptItemReadGroup {
		t.Fatalf("first n cursor=%d key=%+v, want group", m.chatItemCursor, keyBefore)
	}
	m, _ = m.Update(key("enter"))
	if len(m.chatVisibleItems) != 3 || m.chatVisibleItems[m.chatItemCursor].key != keyBefore {
		t.Fatalf("expansion changed cursor identity or visible-item count")
	}
	m, _ = m.Update(key("n"))
	if m.chatItemCursor != 2 {
		t.Fatalf("second n cursor=%d, want item after group", m.chatItemCursor)
	}
	m, _ = m.Update(key("N"))
	if m.chatItemCursor != 1 {
		t.Fatalf("N cursor=%d, want group", m.chatItemCursor)
	}

	m.transcriptInnerW = 38
	m.setChatPage(readGroupInteractionPage())
	if m.chatVisibleItems[m.chatItemCursor].key != keyBefore {
		t.Fatalf("resize/rebuild changed group cursor identity")
	}
}

func TestReadGroupToggle(t *testing.T) {
	m := interactionMonitor(t)
	m.chatItemCursor = 1
	groupKey := m.chatVisibleItems[1].key
	collapsed := ansi.Strip(m.itemTranscriptBody())
	m, _ = m.Update(key("enter"))
	expanded := ansi.Strip(m.chatBody())
	if !m.chatItemExpand[groupKey] || expanded == collapsed || !strings.Contains(expanded, "synthetic member output") {
		t.Fatalf("local toggle did not reveal member detail:\n%s", expanded)
	}
	m, _ = m.Update(key("enter"))
	if m.chatItemExpand[groupKey] {
		t.Fatalf("repeated toggle did not collapse group")
	}
	if got := ansi.Strip(m.chatBody()); strings.Contains(got, "synthetic member output") {
		t.Fatalf("collapsed group retained member detail:\n%s", got)
	}
}

func TestReadGroupExpandAll(t *testing.T) {
	m := interactionMonitor(t)
	m.chatItemCursor = 1
	groupKey := m.chatVisibleItems[1].key
	m.chatItemExpand[groupKey] = false
	before := map[transcriptItemKey]bool{groupKey: false}
	m, _ = m.Update(key("o"))
	if !m.chatItemExpandAll || !reflect.DeepEqual(m.chatItemExpand, before) {
		t.Fatalf("expand-all rewrote per-item map: all=%v map=%+v", m.chatItemExpandAll, m.chatItemExpand)
	}
	if !strings.Contains(ansi.Strip(m.chatBody()), "synthetic member output") {
		t.Fatalf("expand-all did not reveal group details")
	}
	m, _ = m.Update(key("o"))
	if m.chatItemExpandAll {
		t.Fatalf("second expand-all toggle did not collapse")
	}
	if m.chatItemExpand[groupKey] {
		t.Fatalf("expand-all changed local group state")
	}

	for _, item := range m.chatItems {
		if item.kind == transcriptItemReadGroup && itemHasStructuredDiff(m.chatEntries, item) {
			t.Fatalf("read group entered structured-edit auto-expansion path")
		}
	}
}

func TestReadGroupCopy(t *testing.T) {
	m := interactionMonitor(t)
	m.chatItemCursor = 1
	requestFor := func(model Model) shared.ClipboardRequest {
		msg := model.copyTranscriptItemCmd()()
		req, ok := msg.(shared.ClipboardRequest)
		if !ok {
			t.Fatalf("copy command = %T, want ClipboardRequest", msg)
		}
		return req
	}
	collapsed := requestFor(m).Loader()
	clean, err := shared.PrepareClipboardPayload(collapsed.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clean, "Read (2)") || !strings.Contains(clean, "one.go") || !strings.Contains(clean, "two.go") {
		t.Fatalf("collapsed copy missing visible group tree:\n%s", clean)
	}
	if strings.Contains(clean, "synthetic member output") {
		t.Fatalf("collapsed copy leaked hidden detail:\n%s", clean)
	}

	groupKey := m.chatVisibleItems[1].key
	m.chatItemExpand[groupKey] = true
	expandedRequest := requestFor(m)
	expanded := expandedRequest.Loader()
	clean, err = shared.PrepareClipboardPayload(expanded.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clean, "synthetic member output") {
		t.Fatalf("expanded copy missing member evidence:\n%s", clean)
	}
	// The request captured its payload before a later page replacement.
	m.setChatPage(transcript.Page{})
	if again := expandedRequest.Loader().Payload; again != expanded.Payload {
		t.Fatalf("captured group copy changed after page replacement")
	}
}
