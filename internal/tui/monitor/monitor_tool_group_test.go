package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
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
			items := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, true)
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
			items := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, true)
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
	group := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, true)[0]
	rows := compactToolGroupRows(group, entries)
	if len(rows) != 5 || rows[0].text != "one" || rows[2].text != "three" || rows[4].text != "six" || rows[3].text != "… 2 more calls" {
		t.Fatalf("preview rows = %+v", rows)
	}
}

func TestCompactToolGroupsDefaultOnAndSessionToggle(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "go vet ./..."}, 0, 0, 0, false),
	)
	m := newMonitorWithSteps(t).WithTUIConfig(config.TUIConfig{})
	if !m.compactToolGroups {
		t.Fatal("unset [tui] compact_tool_groups should start grouping on")
	}
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
		t.Fatalf("default items=%+v, want one group", m.chatItems)
	}
	m, _ = m.Update(key("c"))
	if m.compactToolGroups || len(m.chatItems) != 2 {
		t.Fatalf("after c: compact=%v items=%d, want off with two standalone calls", m.compactToolGroups, len(m.chatItems))
	}
	// The toggle is session-only: a fresh model from the same config is on.
	if !New("run-1").WithTUIConfig(config.TUIConfig{}).compactToolGroups {
		t.Fatal("a fresh model must not inherit the prior session's toggle")
	}
}

func TestCompactToolGroupInteractionSessionOnly(t *testing.T) {
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "go test ./..."}, 0, 0, 0, false),
		toolExchange("b", "bash", map[string]any{"command": "go vet ./..."}, 0, 0, 0, false),
	)
	off := false
	m := newMonitorWithSteps(t).WithTUIConfig(config.TUIConfig{CompactToolGroups: &off})
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 2 {
		t.Fatalf("configured-off items=%d, want 2", len(m.chatItems))
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
	if !m.compactToolGroups || !m.simpleMode || !strings.Contains(ansi.Strip(strings.Join(m.transcriptChrome(), "\n")), "compact tool groups: on") {
		t.Fatal("toggle did not take effect for this session or show confirmation")
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

// TestSingletonEditGroupOpensByDefault covers the regression from 79498d1:
// wrapping every eligible call in a group, even a run of one, must not hide
// a lone edit's diff behind an extra collapsed group header when compact
// tool groups are already enabled at page load — a singleton group is the
// wrapper around what would otherwise be a standalone exchange, and it must
// open the same way that exchange always has.
func TestSingletonEditGroupOpensByDefault(t *testing.T) {
	old := "before\n"
	entries := diffExchangeEntries("a", "a.go", &old, "after a\n")
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	m.chatStep = "a"
	m.compactToolGroups = true
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
		t.Fatalf("items=%+v, want a single edit group", m.chatItems)
	}
	group := m.chatItems[0]
	if len(group.groupMembers) != 1 {
		t.Fatalf("group members=%+v, want exactly one", group.groupMembers)
	}
	if !m.chatItemExpand[group.key] {
		t.Fatalf("singleton edit group %v did not receive default expansion", group.key)
	}
	if !m.chatItemExpand[group.groupMembers[0].key] {
		t.Fatalf("singleton edit group child %v did not receive default expansion", group.groupMembers[0].key)
	}
	if !strings.Contains(m.itemTranscriptBody(), "Diff · a.go") {
		t.Fatalf("rendered body missing diff section:\n%s", m.itemTranscriptBody())
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

func readExchange(id, path string, generation, iteration, attempt int) []transcript.Entry {
	input, _ := json.Marshal(map[string]any{"file_path": path})
	use := &toolcall.Activity{ID: id, Title: "Read", Kind: "read", Input: input}
	result := use.Clone()
	result.Status = "completed"
	result.Output = json.RawMessage(`{"text":"synthetic output"}`)
	return []transcript.Entry{
		{Seq: 1, Generation: generation, Iteration: iteration, Attempt: attempt, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: use}}},
		{Seq: 2, Generation: generation, Iteration: iteration, Attempt: attempt, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: result}}},
	}
}

func renumberEntries(entries []transcript.Entry, firstSeq int) []transcript.Entry {
	out := append([]transcript.Entry(nil), entries...)
	for i := range out {
		out[i].Seq = firstSeq + i
	}
	return out
}

func TestGroupReadTranscriptItems(t *testing.T) {
	t.Run("adjacent reads group and enumerate every exchange member", func(t *testing.T) {
		entries := append(renumberEntries(readExchange("one", "internal/alpha.go", 0, 0, 0), 1), renumberEntries(readExchange("two", "internal/beta.go", 0, 0, 0), 3)...)
		got := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, false)
		if len(got) != 1 || got[0].kind != transcriptItemToolGroup || len(got[0].groupMembers) != 2 {
			t.Fatalf("grouped items = %+v, want one two-member read group", got)
		}
		if refs := itemMembers(got[0]); len(refs) != 4 {
			t.Fatalf("itemMembers(group) = %d refs, want 4", len(refs))
		}
		if got[0].key.anchor != got[0].groupMembers[0].primary.key || got[0].key.kind != transcriptItemToolGroup {
			t.Fatalf("group key = %+v, want first-member anchor and group kind", got[0].key)
		}
	})

	t.Run("singleton is wrapped in its own read group", func(t *testing.T) {
		entries := readExchange("one", "internal/alpha.go", 0, 0, 0)
		before := buildTranscriptItems(entries, false)
		after := groupToolTranscriptItems(before, entries, false)
		if len(after) != 1 || after[0].kind != transcriptItemToolGroup || len(after[0].groupMembers) != 1 {
			t.Fatalf("singleton not grouped\nbefore=%+v\nafter=%+v", before, after)
		}
		if !reflect.DeepEqual(after[0].groupMembers[0], before[0]) {
			t.Fatalf("singleton member identity changed\nbefore=%+v\nmember=%+v", before[0], after[0].groupMembers[0])
		}
	})

	t.Run("adjacent use-only page-edge reads retain only loaded evidence", func(t *testing.T) {
		firstInput := json.RawMessage(`{"file_path":"synthetic/first.go"}`)
		secondInput := json.RawMessage(`{"file_path":"synthetic/second.go"}`)
		entries := []transcript.Entry{{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "first", Title: "Read", Kind: "read", Input: firstInput}},
			{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "second", Title: "Read", Kind: "read", Input: secondInput}},
		}}}
		got := groupToolTranscriptItems(buildTranscriptItems(entries, true), entries, false)
		if len(got) != 1 || got[0].kind != transcriptItemToolGroup || got[0].displayState != toolDisplayRunning {
			t.Fatalf("use-only items = %+v, want one running read group", got)
		}
		if refs := itemMembers(got[0]); len(refs) != 2 {
			t.Fatalf("use-only group refs = %d, want exactly two loaded uses", len(refs))
		}
	})

	t.Run("result-only item interrupts reads", func(t *testing.T) {
		left := renumberEntries(readExchange("left", "left.go", 0, 0, 0), 1)
		orphan := transcript.Entry{Seq: 3, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "orphan", Kind: "read", Status: "completed", Input: json.RawMessage(`{"file_path":"orphan.go"}`)}}}}
		right := renumberEntries(readExchange("right", "right.go", 0, 0, 0), 4)
		entries := append(append(left, orphan), right...)
		got := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, false)
		if len(got) != 3 || got[1].kind != transcriptItemToolResult {
			t.Fatalf("result-only boundary items = %+v, want read/result-only/read", got)
		}
		for i, item := range got {
			if i == 1 {
				continue
			}
			if item.kind != transcriptItemToolGroup || len(item.groupMembers) != 1 {
				t.Fatalf("result-only boundary should keep reads as separate singleton groups: %+v", got)
			}
		}
	})

	tests := []struct {
		name   string
		middle transcript.Entry
	}{
		{"bash", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, ToolUseID: "bash", Name: "Bash", Input: json.RawMessage(`{"command":"true"}`)}}}},
		{"text", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "between"}}}},
		{"thinking", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "between"}}}},
		{"system", transcript.Entry{Role: transcript.RoleSystem, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "between"}}}},
		{"unsupported", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockType("future"), Text: "between"}}}},
	}
	for _, tt := range tests {
		t.Run("boundary_"+tt.name, func(t *testing.T) {
			left := renumberEntries(readExchange("left", "left.go", 0, 0, 0), 1)
			tt.middle.Seq = 3
			right := renumberEntries(readExchange("right", "right.go", 0, 0, 0), 4)
			entries := append(append(left, tt.middle), right...)
			got := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, false)
			for i, item := range got {
				if item.kind != transcriptItemToolGroup {
					continue
				}
				if len(item.groupMembers) != 1 {
					t.Fatalf("interruption %s should keep reads as separate singleton groups (item %d): %+v", tt.name, i, got)
				}
			}
		})
	}

	for _, axis := range []string{"generation", "iteration", "attempt"} {
		t.Run("coordinate_"+axis, func(t *testing.T) {
			left := readExchange("left", "left.go", 0, 0, 0)
			g, i, a := 0, 0, 0
			switch axis {
			case "generation":
				g = 1
			case "iteration":
				i = 1
			case "attempt":
				a = 1
			}
			right := renumberEntries(readExchange("right", "right.go", g, i, a), 3)
			entries := append(left, right...)
			got := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, false)
			singleton := func(item transcriptItem) bool {
				return item.kind == transcriptItemToolGroup && len(item.groupMembers) == 1
			}
			if len(got) != 2 || !singleton(got[0]) || !singleton(got[1]) {
				t.Fatalf("%s boundary items = %+v, want two singleton read groups", axis, got)
			}
		})
	}
}

func TestGroupReadTranscriptItemsEligibility(t *testing.T) {
	tests := []struct {
		name     string
		activity *toolcall.Activity
		want     bool
	}{
		{"file path", &toolcall.Activity{Kind: "read", Input: json.RawMessage(`{"file_path":"internal/a.go"}`)}, true},
		{"path alias", &toolcall.Activity{Title: "Read", Input: json.RawMessage(`{"path":"/tmp/synthetic.txt"}`)}, true},
		{"kind wins over title", &toolcall.Activity{Kind: "bash", Title: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)}, false},
		{"missing target", &toolcall.Activity{Kind: "read", Input: json.RawMessage(`{}`)}, false},
		{"empty target", &toolcall.Activity{Kind: "read", Input: json.RawMessage(`{"path":""}`)}, false},
		{"https URI", &toolcall.Activity{Kind: "read", Input: json.RawMessage(`{"path":"https://example.invalid/a"}`)}, false},
		{"file URI", &toolcall.Activity{Kind: "read", Input: json.RawMessage(`{"path":"file:///tmp/a"}`)}, false},
		{"result only", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := readTargetForActivity(tt.activity)
			if got != tt.want {
				t.Fatalf("eligible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadGroupResultFirst(t *testing.T) {
	first := readExchange("first", "first.go", 0, 0, 0)
	second := renumberEntries(readExchange("second", "second.go", 0, 0, 0), 3)
	entries := []transcript.Entry{first[1], first[0], second[1], second[0]}
	entries[0].Seq, entries[1].Seq, entries[2].Seq, entries[3].Seq = 1, 2, 3, 4
	items := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, false)
	if len(items) != 1 || items[0].kind != transcriptItemToolGroup || len(items[0].groupMembers) != 2 {
		t.Fatalf("result-first items = %+v, want one two-member group", items)
	}
}

func TestReadGroupsAcrossHiddenReasoning(t *testing.T) {
	entries := append(
		renumberEntries(readExchange("first", "first.go", 0, 0, 0), 1),
		append(
			[]transcript.Entry{{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "synthetic reasoning"}}}},
			renumberEntries(readExchange("second", "second.go", 0, 0, 0), 4)...,
		)...,
	)
	m := newMonitorWithSteps(t)
	m.RunDir = t.TempDir()
	m.focus = focusTranscript
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	m, _ = m.Update(key("c"))
	if len(m.chatVisibleItems) != 1 || m.chatVisibleItems[0].kind != transcriptItemToolGroup || len(m.chatVisibleItems[0].groupMembers) != 2 {
		t.Fatalf("hidden reasoning between two reads should not stop them merging: %+v", m.chatVisibleItems)
	}
}

func TestReadGroupPageState(t *testing.T) {
	entries := append(renumberEntries(readExchange("one", "one.go", 0, 0, 0), 1), renumberEntries(readExchange("two", "two.go", 0, 0, 0), 3)...)
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
		t.Fatalf("page items = %+v, want group", m.chatItems)
	}
	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	m.setChatPage(transcript.Page{Entries: entries})
	if !m.chatItemExpand[key] || m.chatItems[0].key != key {
		t.Fatalf("surviving group state/key not preserved")
	}
	m.setChatPage(transcript.Page{})
	if len(m.chatItemExpand) != 0 || len(m.chatItemRendered) != 0 || len(m.chatItemLineRanges) != 0 {
		t.Fatalf("removed group retained page state")
	}
}

func readGroupFixture(reads ...struct {
	id, path, status string
	offset, limit    int
}) []transcript.Entry {
	var entries []transcript.Entry
	seq := 1
	for _, read := range reads {
		args := map[string]any{"file_path": read.path}
		if read.offset != 0 {
			args["offset"] = read.offset
		}
		if read.limit != 0 {
			args["limit"] = read.limit
		}
		input, _ := json.Marshal(args)
		use := &toolcall.Activity{ID: read.id, Title: "Read", Kind: "read", Input: input}
		result := use.Clone()
		result.Status = read.status
		result.Output = json.RawMessage(`{"text":"synthetic member output"}`)
		entries = append(entries,
			transcript.Entry{Seq: seq, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: use}}},
			transcript.Entry{Seq: seq + 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: result}}},
		)
		seq += 2
	}
	return entries
}

func TestReadGroupRenderTreeAndSelectors(t *testing.T) {
	reads := []struct {
		id, path, status string
		offset, limit    int
	}{
		{"a", "internal/alpha.go", "completed", 1, 0},
		{"b", "internal/alpha.go", "completed", 10, 2},
		{"c", "internal/beta.go", "completed", 0, 0},
		{"d", "internal/alpha.go", "completed", 20, 1},
		{"e", "internal/alpha.go", "completed", 40, 0},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 64
	m.setChatPage(transcript.Page{Entries: readGroupFixture(reads...)})
	plain := ansi.Strip(m.itemTranscriptBody())
	for _, want := range []string{"Read (5)", "├─ alpha.go:1, 10-11, …, 40", "└─ beta.go"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("render missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "╭") || strings.Count(plain, "alpha.go") != 1 {
		t.Fatalf("group used card chrome or failed target merge:\n%s", plain)
	}
	if strings.Contains(plain, "◈ alpha.go") || strings.Contains(plain, "• alpha.go") {
		t.Fatalf("successful target row unexpectedly has state glyph:\n%s", plain)
	}
}

func TestReadGroupRenderStatesAndWidths(t *testing.T) {
	reads := []struct {
		id, path, status string
		offset, limit    int
	}{
		{"ok", "synthetic/ok.go", "completed", 0, 0},
		{"bad", "synthetic/failed-name-that-is-long.go", "failed", 0, 0},
	}
	for _, width := range []int{8, 16, 44, 90} {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = width
		m.setChatPage(transcript.Page{Entries: readGroupFixture(reads...)})
		if m.chatItems[0].displayState != toolDisplayError {
			t.Fatalf("aggregate state = %v, want error", m.chatItems[0].displayState)
		}
		body := m.itemTranscriptBody()
		for _, row := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			if got := lipgloss.Width(row); got > width {
				t.Fatalf("width %d row overflow %d: %q", width, got, row)
			}
		}
		if width >= 44 {
			plain := ansi.Strip(body)
			if !strings.Contains(plain, "✗ Read (2)") || !strings.Contains(plain, "✗ failed-name") {
				t.Fatalf("failure not visible at width %d:\n%s", width, plain)
			}
		}
	}
}

func TestReadGroupRenderAggregateStates(t *testing.T) {
	tests := []struct {
		name       string
		states     []toolDisplayState
		wantState  toolDisplayState
		wantRowFor string
	}{
		{name: "all success", states: []toolDisplayState{toolDisplaySuccess, toolDisplaySuccess}, wantState: toolDisplaySuccess},
		{name: "running", states: []toolDisplayState{toolDisplaySuccess, toolDisplayRunning}, wantState: toolDisplayRunning, wantRowFor: "two.go"},
		{name: "unknown", states: []toolDisplayState{toolDisplaySuccess, toolDisplayUnknownUse}, wantState: toolDisplayUnknownUse, wantRowFor: "two.go"},
		{name: "error wins", states: []toolDisplayState{toolDisplayRunning, toolDisplayError}, wantState: toolDisplayError, wantRowFor: "two.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m.transcriptInnerW = 72
			m.setChatPage(readGroupInteractionPage())
			group := &m.chatItems[1]
			for i := range group.groupMembers {
				group.groupMembers[i].displayState = tt.states[i]
			}
			group.displayState = aggregateToolGroupState(group.groupMembers)
			plain := ansi.Strip(m.itemTranscriptBody())
			if group.displayState != tt.wantState {
				t.Fatalf("aggregate state = %v, want %v", group.displayState, tt.wantState)
			}
			glyph, _ := shared.ToolStatusIcon(tt.wantState, "read")
			if !strings.Contains(plain, glyph+" Read (2)") {
				t.Fatalf("header missing %q for state %v:\n%s", glyph, tt.wantState, plain)
			}
			if tt.wantRowFor != "" && !strings.Contains(plain, glyph+" "+tt.wantRowFor) {
				t.Fatalf("row missing %q for state %v:\n%s", glyph+" "+tt.wantRowFor, tt.wantState, plain)
			}
			if tt.wantState == toolDisplaySuccess && (strings.Contains(plain, glyph+" one.go") || strings.Contains(plain, glyph+" two.go")) {
				t.Fatalf("success glyph appeared on a target row:\n%s", plain)
			}
		})
	}
}

func TestReadGroupExpandedDetailsPreserveInterleavedMemberOrder(t *testing.T) {
	reads := []struct {
		id, path, status string
		offset, limit    int
	}{
		{"a", "synthetic/alpha.go", "completed", 1, 1},
		{"b", "synthetic/beta.go", "completed", 2, 1},
		{"c", "synthetic/alpha.go", "completed", 3, 1},
	}
	entries := readGroupFixture(reads...)
	for i := 1; i < len(entries); i += 2 {
		activity := entries[i].Blocks[0].Activity()
		activity.Output = json.RawMessage(fmt.Sprintf(`{"text":"member-%s"}`, activity.ID))
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 72
	m.setChatPage(transcript.Page{Entries: entries})
	// Group expansion lists member cards; global expansion also opens each
	// member's detail, which must appear in member order.
	m.chatItemExpandAll = true
	plain := ansi.Strip(m.itemTranscriptBody())
	previous := -1
	for _, want := range []string{"member-a", "member-b", "member-c"} {
		index := strings.Index(plain, want)
		if index < 0 || index <= previous {
			t.Fatalf("expanded member order does not preserve a,b,c; %q index=%d previous=%d:\n%s", want, index, previous, plain)
		}
		previous = index
	}
}

func TestReadGroupSelectorFallbackAndFullPathMergeIdentity(t *testing.T) {
	line := 17
	activity := &toolcall.Activity{Kind: "read", Input: json.RawMessage(`{"path":"synthetic/a.go","offset":"bad","limit":-1}`), Locations: []toolcall.Location{{Path: "synthetic/a.go", Line: &line}}}
	if got := readSelector(activity); got != "17" {
		t.Fatalf("selector = %q, want location fallback", got)
	}
	reads := []struct {
		id, path, status string
		offset, limit    int
	}{
		{"a", "one/same.go", "completed", 0, 0},
		{"b", "two/same.go", "completed", 0, 0},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: readGroupFixture(reads...)})
	if rows := readGroupRows(m.chatItems[0], m.chatEntries); len(rows) != 2 {
		t.Fatalf("same-basename full paths merged: %+v", rows)
	}
}

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
	if len(m.chatVisibleItems) != 3 || m.chatVisibleItems[1].kind != transcriptItemToolGroup {
		t.Fatalf("visible items = %+v, want text/group/text", m.chatVisibleItems)
	}
	return m
}

func TestReadGroupNavigation(t *testing.T) {
	m := interactionMonitor(t)
	m, _ = m.Update(key("n"))
	keyBefore := m.chatVisibleItems[m.chatItemCursor].key
	if m.chatItemCursor != 1 || keyBefore.kind != transcriptItemToolGroup {
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

// readMemberDetail is a prefix of the fixture output that survives the
// expanded detail's soft wrap at the default transcript width.
const readMemberDetail = "synthetic member out"

func TestReadGroupToggle(t *testing.T) {
	m := interactionMonitor(t)
	m.chatItemCursor = 1
	groupKey := m.chatVisibleItems[1].key
	collapsed := ansi.Strip(m.itemTranscriptBody())
	m, _ = m.Update(key("enter"))
	expanded := ansi.Strip(m.chatBody())
	if !m.chatItemExpand[groupKey] || expanded == collapsed || !strings.Contains(expanded, "Read: one.go") || !strings.Contains(expanded, "Read: two.go") {
		t.Fatalf("local toggle did not reveal member cards:\n%s", expanded)
	}
	if strings.Contains(expanded, readMemberDetail) {
		t.Fatalf("group toggle expanded member detail too:\n%s", expanded)
	}

	// Member cards are their own cursor stops; enter on one opens its detail.
	m, _ = m.Update(key("n"))
	member := m.chatVisibleItems[1].groupMembers[0].key
	if sel, ok := m.selectedTranscriptItem(); !ok || sel.key != member {
		t.Fatalf("n after expansion selected %+v, want first member", sel.key)
	}
	m, _ = m.Update(key("enter"))
	if !strings.Contains(ansi.Strip(m.chatBody()), readMemberDetail) {
		t.Fatalf("member toggle did not reveal detail:\n%s", ansi.Strip(m.chatBody()))
	}

	m, _ = m.Update(key("N"))
	m, _ = m.Update(key("enter"))
	if m.chatItemExpand[groupKey] {
		t.Fatalf("repeated toggle did not collapse group")
	}
	if got := ansi.Strip(m.chatBody()); strings.Contains(got, readMemberDetail) || strings.Contains(got, "Read: one.go") {
		t.Fatalf("collapsed group retained member cards or detail:\n%s", got)
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
	if !strings.Contains(ansi.Strip(m.chatBody()), readMemberDetail) {
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
		if item.kind == transcriptItemToolGroup && itemHasStructuredDiff(m.chatEntries, item) {
			t.Fatalf("read group entered structured-edit auto-expansion path")
		}
	}
}

func TestReadGroupCopy(t *testing.T) {
	m := interactionMonitor(t)
	m.chatItemCursor = 1
	if got := contextualItemCopyLabel(m); got != "copy group" {
		t.Fatalf("copy label = %q, want copy group", got)
	}
	msg := m.copyTranscriptItemCmd()()
	req, ok := msg.(shared.ClipboardRequest)
	if !ok {
		t.Fatalf("copy command = %T, want ClipboardRequest", msg)
	}
	if req.Target.Label != "tool group" {
		t.Fatalf("copy label = %q, want tool group", req.Target.Label)
	}
	payload := req.Loader()
	clean, err := shared.PrepareClipboardPayload(payload.Payload)
	if err != nil {
		t.Fatal(err)
	}
	// Like every tool group, the header copies every loaded member's
	// use and result whether or not the group is expanded.
	for _, want := range []string{"synthetic/one.go", "synthetic/two.go", "synthetic member output"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("group copy missing %q:\n%s", want, clean)
		}
	}
	// The request captured its payload before a later page replacement.
	m.setChatPage(transcript.Page{})
	if again := req.Loader().Payload; again != payload.Payload {
		t.Fatalf("captured group copy changed after page replacement")
	}
}

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
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
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
			if len(m.chatVisibleItems) != 1 || m.chatVisibleItems[0].kind != transcriptItemToolGroup || len(m.searchHits) != 1 {
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

// assertReadGroupRange checks the shared tool-group layout: collapsed, the
// group key owns its header plus every target row; expanded, the group key owns
// only the header and each member card owns its own range. wantDetail reports
// whether member detail (visible only when the member itself is expanded, e.g.
// by global expansion) should render. It returns the rendered row count.
func assertReadGroupRange(t *testing.T, m *Model, wantExpanded, wantDetail bool) int {
	t.Helper()
	body := ansi.Strip(m.itemTranscriptBody())
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	segmentOf := func(key transcriptItemKey) (lineRange, string) {
		rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: key}]
		if !ok {
			t.Fatalf("range missing for %+v: body=%q", key, body)
		}
		if rng.start < 0 || rng.end >= len(lines) || rng.start > rng.end {
			t.Fatalf("range %+v outside %d rendered rows", rng, len(lines))
		}
		return rng, strings.Join(lines[rng.start:rng.end+1], "\n")
	}
	group := m.chatVisibleItems[0]
	rng, segment := segmentOf(group.key)
	if !strings.Contains(segment, "Read (3)") {
		t.Fatalf("group range %+v misses header:\n%s", rng, segment)
	}
	targets := []string{"one.go", "two.go", "three.go"}
	if !wantExpanded {
		if got := rng.end - rng.start + 1; got != 1+len(targets) {
			t.Fatalf("collapsed height=%d, want header + %d targets", got, len(targets))
		}
		for _, want := range targets {
			if !strings.Contains(segment, want) {
				t.Fatalf("collapsed range %+v misses %q:\n%s", rng, want, segment)
			}
		}
	} else {
		if rng.start != rng.end {
			t.Fatalf("expanded group range %+v should cover only the header", rng)
		}
		// Narrow widths truncate member card titles, so only the
		// unbounded widths can assert each card names its target.
		for i, member := range group.groupMembers {
			memberRange, memberSegment := segmentOf(member.key)
			if m.transcriptInnerW >= 40 && !strings.Contains(memberSegment, targets[i]) {
				t.Fatalf("member range %+v misses %q:\n%s", memberRange, targets[i], memberSegment)
			}
		}
	}
	// A failed member card shows its error inline, so probe a successful
	// member's output for detail visibility.
	if hasDetail := strings.Contains(body, "synthetic"); hasDetail != wantDetail {
		t.Fatalf("detail-visible=%v, want %v:\n%s", hasDetail, wantDetail, body)
	}
	return len(lines)
}

func TestReadGroupLineRanges(t *testing.T) {
	m := readGroupIntegrityMonitor(t)
	collapsed := assertReadGroupRange(t, &m, false, false)
	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	if expanded := assertReadGroupRange(t, &m, true, false); expanded <= collapsed {
		t.Fatalf("expanded height %d did not grow from %d", expanded, collapsed)
	}

	// Search/filter activation, global toggle, same-page reload, and width
	// replacement all keep the group and member keys owning exact ranges.
	m.searchQuery = "third-output-only"
	m.rerunSearch()
	assertReadGroupRange(t, &m, true, false)
	m.filters = transcriptFilters{errors: true}
	m.rebuildTranscriptItemState(key)
	assertReadGroupRange(t, &m, true, false)
	m.chatItemExpand[key] = false
	m.chatItemExpandAll = true
	assertReadGroupRange(t, &m, true, true)
	m.setChatPage(threeMemberReadPage())
	assertReadGroupRange(t, &m, true, true)
	m.transcriptInnerW = 28
	m.rebuildRenderer()
	assertReadGroupRange(t, &m, true, true)
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
	assertReadGroupRange(t, &m, true, false)
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
	m, _ = m.Update(key("x"))
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

func TestReadGroupGallery(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the read-group gallery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	type fixtureRead = struct {
		id, path, status string
		offset, limit    int
	}
	failedReads := []fixtureRead{
		{"a", "synthetic/config/alpha.go", "completed", 1, 20},
		{"b", "synthetic/config/alpha.go", "completed", 30, 10},
		{"c", "synthetic/service/beta.go", "failed", 0, 0},
		{"d", "synthetic/config/alpha.go", "completed", 50, 1},
		{"e", "synthetic/config/alpha.go", "completed", 70, 5},
	}
	var gallery strings.Builder
	fmt.Fprintln(&gallery, "# Tool-call grouping gallery — synthetic transcript")
	writeScene := func(name string, width int, reads []fixtureRead, states []toolDisplayState, expanded bool) {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = width
		m.setChatPage(transcript.Page{Entries: readGroupFixture(reads...)})
		if len(states) > 0 {
			for i := range m.chatItems[0].groupMembers {
				m.chatItems[0].groupMembers[i].displayState = states[i]
			}
			m.chatItems[0].displayState = aggregateToolGroupState(m.chatItems[0].groupMembers)
		}
		m.chatItemExpand[m.chatItems[0].key] = expanded
		fmt.Fprintf(&gallery, "\n## %s · transcriptInnerW=%d\n", name, width)
		body := m.itemTranscriptBody()
		gallery.WriteString(body)
		for i, row := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			fmt.Fprintf(&gallery, "# row[%d] visible-width=%d\n", i, lipgloss.Width(row))
		}
	}
	stateReads := []fixtureRead{
		{"state-a", "synthetic/state/one.go", "completed", 0, 0},
		{"state-b", "synthetic/state/two.go", "completed", 0, 0},
	}
	writeScene("all-success collapsed", 72, stateReads, []toolDisplayState{toolDisplaySuccess, toolDisplaySuccess}, false)
	writeScene("running collapsed", 72, stateReads, []toolDisplayState{toolDisplaySuccess, toolDisplayRunning}, false)
	writeScene("unknown collapsed", 72, stateReads, []toolDisplayState{toolDisplaySuccess, toolDisplayUnknownUse}, false)
	writeScene("failed repeated-target collapsed narrow", 32, failedReads, nil, false)
	writeScene("failed repeated-target expanded wide", 72, failedReads, nil, true)

	txtPath := filepath.Join(dir, "25-task-02-read-group-gallery.txt")
	if err := os.WriteFile(txtPath, []byte(gallery.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var interaction strings.Builder
	fmt.Fprintln(&interaction, "# Read-group keyboard interaction — synthetic transcript")
	m := interactionMonitor(t)
	m.transcriptInnerW = 72
	m.setChatPage(readGroupInteractionPage())
	writeInteraction := func(label string) {
		selected := m.chatVisibleItems[m.chatItemCursor]
		fmt.Fprintf(&interaction, "\n## %s\n\ncursor=%d kind=%d visible-items=%d local-expanded=%v expand-all=%v\n\n```text\n%s```\n",
			label, m.chatItemCursor, selected.kind, len(m.chatVisibleItems), m.chatItemExpand[selected.key], m.chatItemExpandAll, proofPlainText(m.chatBody()))
	}
	writeInteraction("initial — item before group selected")
	m, _ = m.Update(key("n"))
	writeInteraction("after n — collapsed group selected")
	m, _ = m.Update(key("enter"))
	writeInteraction("after enter — group expanded")
	m, _ = m.Update(key("enter"))
	writeInteraction("after enter — group collapsed")
	m, _ = m.Update(key("o"))
	writeInteraction("after o — global expansion enabled")
	m, _ = m.Update(key("o"))
	writeInteraction("after o — global expansion disabled")
	m, _ = m.Update(key("n"))
	writeInteraction("after n — item after group selected")
	m, _ = m.Update(key("N"))
	writeInteraction("after N — group selected again")
	m, _ = m.Update(key("N"))
	writeInteraction("after N — item before group selected")
	interactionPath := filepath.Join(dir, "25-task-03-read-group-interaction.txt")
	if err := os.WriteFile(interactionPath, []byte(interaction.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	htmlPath := filepath.Join(dir, "25-task-02-read-group-gallery.html")
	if err := os.WriteFile(htmlPath, []byte(terminalHTML(gallery.String())), 0o644); err != nil {
		t.Fatal(err)
	}

	chrome := readGroupGalleryChrome()
	if chrome == "" {
		t.Fatal("JIG_UI_SNAPSHOT_DIR requires Chrome to generate the read-group PNG proof")
	}
	pngPath := filepath.Join(dir, "25-task-02-read-group-gallery.png")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
		"--force-device-scale-factor=2", "--window-size=1280,2400",
		"--screenshot="+pngPath, "file://"+htmlPath).CombinedOutput()
	if err != nil {
		t.Fatalf("chrome screenshot failed: %v\n%s", err, out)
	}
}

func proofPlainText(value string) string {
	lines := strings.Split(ansi.Strip(value), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}

func readGroupGalleryChrome() string {
	if chrome, err := exec.LookPath("google-chrome"); err == nil {
		return chrome
	}
	const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(macChrome); err == nil {
		return macChrome
	}
	return ""
}
