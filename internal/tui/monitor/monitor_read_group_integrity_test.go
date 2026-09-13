package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

func threeMemberReadPage() transcript.Page {
	reads := []struct {
		id, path, status string
		offset, limit    int
	}{
		{"one", "synthetic/one.go", "completed", 1, 2},
		{"two", "synthetic/two.go", "completed", 8, 1},
		{"three", "synthetic/three.go", "failed", 21, 3},
	}
	entries := readGroupFixture(reads...)
	thirdUse := entries[4].Blocks[0].Tool
	thirdUse.Input = json.RawMessage(`{"file_path":"synthetic/three.go","offset":21,"limit":3,"input_token":"third-input-only"}`)
	line := 21
	thirdUse.Locations = []toolcall.Location{{Path: "synthetic/location-only.go", Line: &line}}
	thirdResult := entries[5].Blocks[0].Tool
	thirdResult.Output = json.RawMessage(`{"text":"third-output-only"}`)
	thirdResult.Content = []toolcall.Content{{Type: "text", Text: "third-content-only"}}
	for i := range entries {
		entries[i].Attempt = 1
	}
	return transcript.Page{Entries: entries}
}

func readGroupIntegrityMonitor(t *testing.T) Model {
	t.Helper()
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 64
	m.setChatPage(threeMemberReadPage())
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemReadGroup {
		t.Fatalf("items = %+v, want one read group", m.chatItems)
	}
	return m
}

func TestReadGroupSearchKeepsWholeGroup(t *testing.T) {
	for _, query := range []string{"third-input-only", "third-output-only", "location-only.go", "third-content-only"} {
		t.Run(query, func(t *testing.T) {
			m := readGroupIntegrityMonitor(t)
			m.searchQuery = query
			m.rerunSearch()
			if len(m.chatVisibleItems) != 1 || m.chatVisibleItems[0].kind != transcriptItemReadGroup || len(m.searchHits) != 1 {
				t.Fatalf("query %q split or discarded group: visible=%+v hits=%+v", query, m.chatVisibleItems, m.searchHits)
			}
			m.applyCurrentSearchHit()
			if !m.chatItemExpand[m.chatVisibleItems[0].key] {
				t.Fatalf("query %q did not expand matching group", query)
			}
		})
	}
}

func TestReadGroupFilterKeepsWholeGroup(t *testing.T) {
	tests := []struct {
		name    string
		filters transcriptFilters
	}{
		{"error on third result", transcriptFilters{errors: true}},
		{"user-role result", transcriptFilters{user: true}},
		{"assistant-role use", transcriptFilters{assistant: true}},
		{"retry coordinate", transcriptFilters{retries: true}},
		{"tool kind", transcriptFilters{tools: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := readGroupIntegrityMonitor(t)
			m.filters = tt.filters
			m.rebuildTranscriptItemState(transcriptItemKey{})
			if len(m.chatVisibleItems) != 1 || len(m.chatVisibleItems[0].groupMembers) != 3 {
				t.Fatalf("filter split or discarded group: %+v", m.chatVisibleItems)
			}
			plain := ansi.Strip(m.itemTranscriptBody())
			for _, target := range []string{"one.go", "two.go", "three.go"} {
				if !strings.Contains(plain, target) {
					t.Fatalf("filter lost target %q:\n%s", target, plain)
				}
			}
		})
	}
}

func assertReadGroupRange(t *testing.T, m *Model, wantExpanded bool) lineRange {
	t.Helper()
	body := ansi.Strip(m.itemTranscriptBody())
	key := m.chatVisibleItems[m.chatItemCursor].key
	rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: key}]
	if !ok {
		t.Fatalf("group range missing: body=%q", body)
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if rng.start < 0 || rng.end >= len(lines) || rng.start > rng.end {
		t.Fatalf("range %+v outside %d rendered rows", rng, len(lines))
	}
	segment := strings.Join(lines[rng.start:rng.end+1], "\n")
	for _, want := range []string{"Read (3)", "one.go", "two.go", "three.go"} {
		if !strings.Contains(segment, want) {
			t.Fatalf("range %+v misses %q:\n%s", rng, want, segment)
		}
	}
	hasDetails := strings.Contains(segment, "third-content-only")
	if hasDetails != wantExpanded {
		t.Fatalf("expanded=%v, detail-visible=%v:\n%s", wantExpanded, hasDetails, segment)
	}
	return rng
}

func TestReadGroupLineRanges(t *testing.T) {
	m := readGroupIntegrityMonitor(t)
	collapsed := assertReadGroupRange(t, &m, false)
	if got := collapsed.end - collapsed.start + 1; got != 4 {
		t.Fatalf("collapsed height=%d, want header + 3 targets", got)
	}
	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	expanded := assertReadGroupRange(t, &m, true)
	if expanded.end <= collapsed.end {
		t.Fatalf("expanded range %+v did not grow from %+v", expanded, collapsed)
	}

	// Selection, search/filter activation, global toggle, same-page reload, and
	// width replacement all continue to make the group key own the exact range.
	m.searchQuery = "third-output-only"
	m.rerunSearch()
	assertReadGroupRange(t, &m, true)
	m.filters = transcriptFilters{errors: true}
	m.rebuildTranscriptItemState(key)
	assertReadGroupRange(t, &m, true)
	m.chatItemExpand[key] = false
	m.chatItemExpandAll = true
	assertReadGroupRange(t, &m, true)
	m.setChatPage(threeMemberReadPage())
	assertReadGroupRange(t, &m, true)
	m.transcriptInnerW = 28
	m.rebuildRenderer()
	assertReadGroupRange(t, &m, true)
}

func TestReadGroupReloadPreservesAndPrunesState(t *testing.T) {
	m := readGroupIntegrityMonitor(t)
	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) == 0 {
		t.Fatal("group render cache was not populated")
	}
	m.setChatPage(threeMemberReadPage())
	if !m.chatItemExpand[key] || m.chatItems[0].key != key {
		t.Fatal("same-page reload lost group state or identity")
	}
	m.setChatPage(transcript.Page{})
	if len(m.chatItemExpand) != 0 || len(m.chatItemRendered) != 0 || len(m.chatItemLineRanges) != 0 {
		t.Fatalf("removed group retained state: expand=%d render=%d ranges=%d", len(m.chatItemExpand), len(m.chatItemRendered), len(m.chatItemLineRanges))
	}
}

func TestReadGroupResizePreservesExpansionInvalidatesRender(t *testing.T) {
	m := readGroupIntegrityMonitor(t)
	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	_ = m.itemTranscriptBody()
	m.lastTranscriptW = m.transcriptInnerW
	m.transcriptInnerW = 31
	m.rebuildRenderer()
	if !m.chatItemExpand[key] {
		t.Fatal("resize discarded local expansion")
	}
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("resize retained %d width-dependent renders", len(m.chatItemRendered))
	}
	assertReadGroupRange(t, &m, true)
}

func TestReadGroupReloadStepChangeClearsState(t *testing.T) {
	m := readGroupIntegrityMonitor(t)
	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	_ = m.itemTranscriptBody()
	m.cursor = 1 // step b
	m.reloadTranscript()
	if m.chatStep != "b" || len(m.chatItemExpand) != 0 || len(m.chatItemRendered) != 0 || len(m.chatItemLineRanges) != 0 {
		t.Fatalf("step change retained group state: step=%q expand=%d render=%d ranges=%d", m.chatStep, len(m.chatItemExpand), len(m.chatItemRendered), len(m.chatItemLineRanges))
	}
}

func TestReadGroupPersistenceOff(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunDir = ""
	m.chatStep = "a"
	m.loadChatTail()
	if len(m.chatItems) != 0 || len(m.chatEntries) != 0 || m.itemTranscriptBody() != "" {
		t.Fatalf("persistence-off allocated or rendered grouped reads")
	}
}

func TestReadGroupTerminalSmoke(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the read-group terminal smoke")
	}
	entries := append([]transcript.Entry{{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "before grouped reads"}}}}, threeMemberReadPage().Entries...)
	entries = append(entries, transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "after grouped reads"}}})
	runDir := writeTranscript(t, "a", entries)
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	var proof strings.Builder
	fmt.Fprintln(&proof, "# Synthetic persisted-transcript Monitor smoke")
	fmt.Fprintln(&proof)
	fmt.Fprintf(&proof, "- Terminal: %dx%d; initial transcript inner width: %d\n", m.width, m.height, m.transcriptInnerW)
	fmt.Fprintln(&proof, "- Fixture: temporary persisted transcript containing fabricated paths and output only.")
	fmt.Fprintln(&proof, "- Sequence: `n`, `enter`, `enter`, `o`, `o`, `/third-content-only`, `F` + errors, resize narrow/wide, `N`/`n`.")

	m, _ = m.Update(key("n"))
	fmt.Fprintf(&proof, "\n## After `n` (group selected)\n\n```text\n%s```\n", proofPlainText(m.chatBody()))
	m, _ = m.Update(key("enter"))
	fmt.Fprintf(&proof, "\n## After `enter` (local expansion)\n\n```text\n%s```\n", proofPlainText(m.chatBody()))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("o"))
	fmt.Fprintf(&proof, "\n## After collapse then `o` (global expansion)\n\n```text\n%s```\n", proofPlainText(m.chatBody()))
	m, _ = m.Update(key("o"))

	m, _ = m.Update(key("/"))
	for _, r := range "third-content-only" {
		m, _ = m.Update(key(string(r)))
	}
	m, _ = m.Update(key("enter"))
	fmt.Fprintf(&proof, "\n## Search for third-member-only token\n\nVisible items: %d; selected kind: %d; expanded: %v\n", len(m.chatVisibleItems), m.chatVisibleItems[m.chatItemCursor].kind, m.chatItemExpand[m.chatVisibleItems[m.chatItemCursor].key])
	m, _ = m.Update(key("c"))
	m, _ = m.Update(key("F"))
	m, _ = m.Update(key(" "))
	m, _ = m.Update(key("enter"))
	fmt.Fprintf(&proof, "\n## Error filter\n\n```text\n%s```\n", proofPlainText(m.chatBody()))

	m, _ = m.Update(tea.WindowSizeMsg{Width: 56, Height: 20})
	fmt.Fprintf(&proof, "\n## Narrow resize\n\nTerminal: %dx%d; transcript inner width: %d\n\n```text\n%s```\n", m.width, m.height, m.transcriptInnerW, proofPlainText(m.chatBody()))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(key("N"))
	m, _ = m.Update(key("n"))
	fmt.Fprintf(&proof, "\n## Wide resize and `N`/`n` navigation\n\nTerminal: %dx%d; transcript inner width: %d; selected key: %+v\n", m.width, m.height, m.transcriptInnerW, m.chatVisibleItems[m.chatItemCursor].key)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "25-task-04-monitor-smoke.md")
	if err := os.WriteFile(path, []byte(proof.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
