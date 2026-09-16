package monitor

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/prefs"
	"jig/internal/tui/shared"
)

func toolExchange(id, kind string, args map[string]any, generation, iteration, attempt int, failed bool) []transcript.Entry {
	input, _ := json.Marshal(args)
	use := &toolcall.Activity{ID: id, Title: kind, Kind: kind, Input: input}
	result := use.Clone()
	result.Input = nil
	result.Status = "completed"
	result.Output, _ = json.Marshal(map[string]any{"text": "synthetic " + id + " output"})
	block := transcript.Block{Type: transcript.BlockToolResult, Tool: result}
	if failed {
		result.Status = "failed"
		block.IsError = true
	}
	return []transcript.Entry{
		{Generation: generation, Iteration: iteration, Attempt: attempt, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: use}}},
		{Generation: generation, Iteration: iteration, Attempt: attempt, Role: transcript.RoleUser, Blocks: []transcript.Block{block}},
	}
}

func toolGroupEntries(exchanges ...[]transcript.Entry) []transcript.Entry {
	var entries []transcript.Entry
	seq := 1
	for _, exchange := range exchanges {
		for _, entry := range exchange {
			entry.Seq = seq
			seq++
			entries = append(entries, entry)
		}
	}
	return entries
}

func TestCompactToolGroupingPolicy(t *testing.T) {
	tests := []struct {
		name  string
		left  []transcript.Entry
		right []transcript.Entry
		want  bool
	}{
		{"same edits", toolExchange("a", "edit", map[string]any{"file_path": "a.go"}, 0, 0, 0, false), toolExchange("b", "edit", map[string]any{"file_path": "b.go"}, 0, 0, 0, false), true},
		{"same bash", toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false), toolExchange("b", "bash", map[string]any{"command": "go vet ./..."}, 0, 0, 0, false), true},
		{"historical ACP execute", toolExchange("a", "execute", map[string]any{"command": "go test ./..."}, 0, 0, 0, false), toolExchange("b", "execute", map[string]any{"command": "go vet ./..."}, 0, 0, 0, false), true},
		{"different kinds", toolExchange("a", "edit", map[string]any{"file_path": "a.go"}, 0, 0, 0, false), toolExchange("b", "write", map[string]any{"file_path": "b.go"}, 0, 0, 0, false), false},
		{"coordinate boundary", toolExchange("a", "bash", map[string]any{"command": "one"}, 0, 0, 0, false), toolExchange("b", "bash", map[string]any{"command": "two"}, 0, 1, 0, false), false},
		{"failure boundary", toolExchange("a", "bash", map[string]any{"command": "one"}, 0, 0, 0, false), toolExchange("b", "bash", map[string]any{"command": "two"}, 0, 0, 0, true), false},
		{"targetless", toolExchange("a", "bash", map[string]any{}, 0, 0, 0, false), toolExchange("b", "bash", map[string]any{"command": "two"}, 0, 0, 0, false), false},
		{"todo reads excluded", toolExchange("a", "todoread", map[string]any{}, 0, 0, 0, false), toolExchange("b", "todoread", map[string]any{}, 0, 0, 0, false), false},
		{"questions excluded", toolExchange("a", "askuserquestion", map[string]any{"questions": []any{map[string]any{"question": "Proceed?"}}}, 0, 0, 0, false), toolExchange("b", "askuserquestion", map[string]any{"questions": []any{map[string]any{"question": "Continue?"}}}, 0, 0, 0, false), false},
		{"unknown excluded", toolExchange("a", "futuretool", map[string]any{"target": "one"}, 0, 0, 0, false), toolExchange("b", "futuretool", map[string]any{"target": "two"}, 0, 0, 0, false), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := toolGroupEntries(tt.left, tt.right)
			items := groupCompactToolTranscriptItems(buildTranscriptItems(entries, false), entries)
			got := len(items) == 1 && items[0].kind == transcriptItemToolGroup && len(items[0].groupMembers) == 2
			if got != tt.want {
				t.Fatalf("grouped=%v items=%+v, want grouped=%v", got, items, tt.want)
			}
		})
	}
}

func TestCompactToolPolicyRegistry(t *testing.T) {
	tests := []struct {
		kind string
		args map[string]any
	}{
		{"edit", map[string]any{"file_path": "a.go"}},
		{"write", map[string]any{"file_path": "a.go"}},
		{"notebookedit", map[string]any{"notebook_path": "a.ipynb"}},
		{"glob", map[string]any{"pattern": "*.go"}},
		{"grep", map[string]any{"pattern": "needle"}},
		{"bash", map[string]any{"command": "go test ./..."}},
		{"websearch", map[string]any{"query": "jig"}},
		{"webfetch", map[string]any{"url": "https://example.test/docs"}},
		{"task", map[string]any{"description": "inspect monitor"}},
		{"skill", map[string]any{"skill": "qa"}},
		{"todowrite", map[string]any{"todos": []any{"one"}}},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			entries := toolGroupEntries(
				toolExchange("a", tt.kind, tt.args, 0, 0, 0, false),
				toolExchange("b", tt.kind, tt.args, 0, 0, 0, false),
			)
			items := groupCompactToolTranscriptItems(buildTranscriptItems(entries, false), entries)
			if len(items) != 1 || items[0].kind != transcriptItemToolGroup {
				t.Fatalf("items=%+v, want one %s group", items, tt.kind)
			}
		})
	}
}

func TestCompactToolGroupsCodexWebSearchActions(t *testing.T) {
	first := toolExchange("a", "search", map[string]any{
		"action": map[string]any{"type": "search", "queries": []any{"Cratera sandbox", "Cratera isolation"}},
	}, 0, 0, 0, false)
	second := toolExchange("b", "search", map[string]any{
		"action": map[string]any{"type": "openPage", "url": "https://example.test/cratera"},
	}, 0, 0, 0, false)
	for i := range first {
		first[i].Blocks[0].Tool.Title = "Web search: Cratera sandbox, Cratera isolation"
	}
	for i := range second {
		second[i].Blocks[0].Tool.Title = "Open page: https://example.test/cratera"
	}

	entries := toolGroupEntries(first, second)
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	m, _ = m.Update(key("c"))
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
		t.Fatalf("toggle-on items=%+v, want one Codex web-search group", m.chatItems)
	}
	rows := compactToolGroupRows(m.chatItems[0], entries)
	if len(rows) != 2 || rows[0].text != "Cratera sandbox, Cratera isolation" || rows[1].text != "https://example.test/cratera" {
		t.Fatalf("Codex web-search rows = %+v", rows)
	}
}

func TestCompactToolGroupsAcrossHiddenReasoning(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "websearch", map[string]any{"query": "first query"}, 0, 0, 0, false),
		[]transcript.Entry{{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "synthetic reasoning"}}}},
		toolExchange("b", "websearch", map[string]any{"query": "second query"}, 0, 0, 0, false),
	)
	m := newMonitorWithSteps(t)
	m.RunDir = t.TempDir()
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	m, _ = m.Update(key("c"))
	assertGroup := func() transcriptItem {
		t.Helper()
		if len(m.chatVisibleItems) != 1 || m.chatVisibleItems[0].kind != transcriptItemToolGroup || len(m.chatVisibleItems[0].groupMembers) != 2 {
			t.Fatalf("visible items=%+v, want two searches in one group across hidden reasoning", m.chatVisibleItems)
		}
		body := ansi.Strip(m.itemTranscriptBody())
		if !strings.Contains(body, "first query") || !strings.Contains(body, "second query") || strings.Contains(body, "synthetic reasoning") {
			t.Fatalf("compact render lost queries or exposed hidden reasoning: %s", body)
		}
		return m.chatVisibleItems[0]
	}
	group := assertGroup()
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("n"))
	m, _ = m.Update(key("n"))
	selected, ok := m.selectedTranscriptItem()
	if !ok || selected.key != group.groupMembers[1].key {
		t.Fatal("expanded group did not expose its second child for navigation")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 35})
	m.setChatPage(transcript.Page{Entries: entries})
	selected, ok = m.selectedTranscriptItem()
	if !ok || selected.key != group.groupMembers[1].key {
		t.Fatal("resize/reload lost selected child")
	}
	m.searchQuery = "second query"
	m.rerunSearch()
	if len(m.searchHits) != 1 || len(m.chatVisibleItems) != 1 || len(m.chatVisibleItems[0].groupMembers) != 2 {
		t.Fatal("search split the compact group")
	}
	m, _ = m.Update(key("x"))
	payload := m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader()
	if payload.Err != nil || !strings.Contains(payload.Payload, "first query") || !strings.Contains(payload.Payload, "second query") || strings.Contains(payload.Payload, "synthetic reasoning") {
		t.Fatalf("group copy lost members or exposed hidden reasoning: error=%v", payload.Err)
	}

	// The tools + reasoning filters reveal the original ordered conversation.
	m, _ = m.Update(key("F"))
	m, _ = m.Update(key("j"))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m, _ = m.Update(key("j"))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m, _ = m.Update(key("enter"))
	if len(m.chatVisibleItems) != 3 || m.chatVisibleItems[1].kind != transcriptItemThinking {
		t.Fatalf("reasoning filter did not restore the tool/reasoning/tool sequence: %+v", m.chatVisibleItems)
	}
	if !strings.Contains(ansi.Strip(m.itemTranscriptBody()), "synthetic reasoning") {
		t.Fatal("reasoning text is no longer inspectable")
	}
	// Hiding reasoning again through the filter menu also rebuilds groups.
	m, _ = m.Update(key("F"))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m, _ = m.Update(key("enter"))
	assertGroup()
	m, _ = m.Update(key("F"))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("x"))
	assertGroup()
	m, _ = m.Update(key("c"))
	if len(m.chatVisibleItems) != 2 || m.chatVisibleItems[0].kind != transcriptItemToolExchange || m.chatVisibleItems[1].kind != transcriptItemToolExchange {
		t.Fatal("toggle off did not restore standalone tools")
	}
}

func TestCompactToolGroupsKeepNonReasoningBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name    string
		between []transcript.Entry
	}{
		{"prose hidden by tools filter", []transcript.Entry{{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "synthetic narration"}}}}},
		{"reasoning at another coordinate", []transcript.Entry{{Generation: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "synthetic reasoning"}}}}},
		{"targetless web call", toolExchange("boundary", "websearch", map[string]any{}, 0, 0, 0, false)},
		{"failed web call", toolExchange("boundary", "websearch", map[string]any{"query": "failed query"}, 0, 0, 0, true)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries := toolGroupEntries(
				toolExchange("a", "websearch", map[string]any{"query": "first query"}, 0, 0, 0, false),
				tt.between,
				toolExchange("b", "websearch", map[string]any{"query": "second query"}, 0, 0, 0, false),
			)
			m := newMonitorWithSteps(t)
			m.chatStep = "a"
			m.compactToolGroups = true
			m.filters.tools = true
			m.setChatPage(transcript.Page{Entries: entries})
			for _, item := range m.chatVisibleItems {
				if item.kind == transcriptItemToolGroup && len(item.groupMembers) > 1 {
					t.Fatalf("compacting erased a real group boundary: %+v", item)
				}
			}
		})
	}
}

func TestCompactToolGroupsWebBurstWithHiddenReasoning(t *testing.T) {
	var exchanges [][]transcript.Entry
	for i, query := range []string{"first query", "second query", "https://example.test/one", "", "third query", "https://example.test/two", ""} {
		exchanges = append(exchanges, []transcript.Entry{{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "synthetic reasoning"}}}})
		exchange := toolExchange(string(rune('a'+i)), "search", nil, 0, 0, 0, false)
		use, result := exchange[0].Blocks[0].Tool, exchange[1].Blocks[0].Tool
		use.Title, result.Title = "Web search", "Web search"
		use.Input = json.RawMessage(`{"query":"","action":null}`)
		action := "search"
		if query == "" {
			action = "other"
		}
		result.Input, _ = json.Marshal(map[string]any{"query": query, "action": map[string]any{"type": action}})
		exchanges = append(exchanges, exchange)
	}
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: toolGroupEntries(exchanges...)})
	m, _ = m.Update(key("c"))
	if len(m.chatVisibleItems) != 4 {
		t.Fatalf("visible items=%d, want two groups and two targetless calls", len(m.chatVisibleItems))
	}
	for i, count := range []int{3, 0, 2, 0} {
		if got := len(m.chatVisibleItems[i].groupMembers); got != count {
			t.Fatalf("item %d has %d members, want %d", i, got, count)
		}
	}
	if body := ansi.Strip(m.itemTranscriptBody()); strings.Count(body, "Search web") != 4 || strings.Contains(body, "synthetic reasoning") {
		t.Fatalf("web burst did not compact in the rendered transcript: %s", body)
	}
}

func TestCompactToolGroupPreviewBound(t *testing.T) {
	var exchanges [][]transcript.Entry
	for i, command := range []string{"one", "two", "three", "four", "five", "six"} {
		exchanges = append(exchanges, toolExchange(string(rune('a'+i)), "bash", map[string]any{"command": command}, 0, 0, 0, false))
	}
	entries := toolGroupEntries(exchanges...)
	group := groupCompactToolTranscriptItems(buildTranscriptItems(entries, false), entries)[0]
	rows := compactToolGroupRows(group, entries)
	if len(rows) != 5 || rows[0].text != "one" || rows[2].text != "three" || rows[4].text != "six" || rows[3].text != "… 2 more calls" {
		t.Fatalf("preview rows = %+v", rows)
	}
}

func TestCompactToolGroupInteractionAndPersistence(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "go vet ./..."}, 0, 0, 0, false),
	)
	dir := t.TempDir()
	m := newMonitorWithSteps(t).WithPrefs(dir)
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 2 {
		t.Fatalf("default-off items=%d, want 2", len(m.chatItems))
	}
	foundPaletteAction := false
	for _, section := range m.PaletteSections() {
		for _, binding := range section.Bindings {
			help := binding.Help()
			if help.Key == "c" && strings.Contains(help.Desc, "compact tools") {
				foundPaletteAction = true
			}
		}
	}
	if !foundPaletteAction {
		t.Fatal("command palette is missing compact-tools action")
	}
	m, _ = m.Update(key("c"))
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
		t.Fatalf("toggle-on items=%+v, want one group", m.chatItems)
	}
	loaded := prefs.Load(dir)
	if !loaded.CompactToolGroups || !loaded.SimpleMode || !strings.Contains(ansi.Strip(m.chatBody()), "compact tool groups: on") {
		t.Fatal("toggle did not persist or show confirmation")
	}
	m, _ = m.Update(key("enter"))
	if len(m.chatCursorTargets) != 3 {
		t.Fatalf("expanded cursor targets=%d, want header plus two children", len(m.chatCursorTargets))
	}
	body := ansi.Strip(m.chatBody())
	if strings.Count(body, "╭") < 2 || !strings.Contains(body, "Run: go test ./...") || !strings.Contains(body, "Run: go vet ./...") {
		t.Fatalf("expanded group did not show bordered child cards:\n%s", body)
	}
	m, _ = m.Update(key("n"))
	item, ok := m.selectedTranscriptItem()
	if !ok || item.key != m.chatItems[0].groupMembers[0].key {
		t.Fatalf("n did not select first child: %+v", item)
	}
	m, _ = m.Update(key("enter"))
	if !m.chatItemExpand[item.key] || !strings.Contains(ansi.Strip(m.chatBody()), "synthetic a output") {
		t.Fatal("child toggle did not reveal existing detail")
	}
}

func TestCompactToolGroupSearchSelectsMember(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "write", map[string]any{"file_path": "alpha.go"}, 0, 0, 0, false),
		toolExchange("b", "write", map[string]any{"file_path": "needle.go"}, 0, 0, 0, false),
	)
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.compactToolGroups = true
	m.setChatPage(transcript.Page{Entries: entries})
	m.searchQuery = "needle.go"
	m.rerunSearch()
	if len(m.searchHits) != 1 {
		t.Fatalf("search hits=%d, want 1", len(m.searchHits))
	}
	m.applyCurrentSearchHit()
	item, ok := m.selectedTranscriptItem()
	if !ok || item.key != m.chatItems[0].groupMembers[1].key || !m.chatItemExpand[m.chatItems[0].key] {
		t.Fatalf("search did not expand group and select matching child: item=%+v", item)
	}
}

func TestCompactEditChildrenStartCollapsedAndRestoreStandaloneDefault(t *testing.T) {
	old := "before\n"
	first := diffExchangeEntries("a", "a.go", &old, "after a\n")
	second := renumberEntries(diffExchangeEntries("b", "b.go", &old, "after b\n"), 3)
	entries := append(first, second...)
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	for _, item := range m.chatItems {
		if !m.chatItemExpand[item.key] {
			t.Fatalf("standalone edit %v did not receive default expansion", item.key)
		}
	}
	m, _ = m.Update(key("c"))
	group := m.chatItems[0]
	if group.kind != transcriptItemToolGroup {
		t.Fatalf("items=%+v, want edit group", m.chatItems)
	}
	for _, member := range group.groupMembers {
		if m.chatItemExpand[member.key] {
			t.Fatalf("grouped edit child %v started expanded", member.key)
		}
	}
	m, _ = m.Update(key("c"))
	for _, item := range m.chatItems {
		if !m.chatItemExpand[item.key] {
			t.Fatalf("ungrouped edit %v did not restore standalone default", item.key)
		}
	}
}

func TestCompactToolGroupCopyIncludesEveryMember(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "first-command"}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "second-command"}, 0, 0, 0, false),
	)
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.compactToolGroups = true
	m.setChatPage(transcript.Page{Entries: entries})
	msg := m.copyTranscriptItemCmd()()
	req, ok := msg.(shared.ClipboardRequest)
	if !ok {
		t.Fatalf("copy command=%T, want ClipboardRequest", msg)
	}
	payload := req.Loader()
	if payload.Err != nil || !strings.Contains(payload.Payload, "first-command") || !strings.Contains(payload.Payload, "second-command") {
		t.Fatalf("group payload=%q err=%v", payload.Payload, payload.Err)
	}
}
