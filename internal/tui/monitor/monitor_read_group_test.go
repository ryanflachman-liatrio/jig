package monitor

import (
	"encoding/json"
	"reflect"
	"testing"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

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
		got := groupReadTranscriptItems(buildTranscriptItems(entries, false), entries)
		if len(got) != 1 || got[0].kind != transcriptItemReadGroup || len(got[0].groupMembers) != 2 {
			t.Fatalf("grouped items = %+v, want one two-member read group", got)
		}
		if refs := itemMembers(got[0]); len(refs) != 4 {
			t.Fatalf("itemMembers(group) = %d refs, want 4", len(refs))
		}
		if got[0].key.anchor != got[0].groupMembers[0].primary.key || got[0].key.kind != transcriptItemReadGroup {
			t.Fatalf("group key = %+v, want first-member anchor and group kind", got[0].key)
		}
	})

	t.Run("singleton is wrapped in its own read group", func(t *testing.T) {
		entries := readExchange("one", "internal/alpha.go", 0, 0, 0)
		before := buildTranscriptItems(entries, false)
		after := groupReadTranscriptItems(before, entries)
		if len(after) != 1 || after[0].kind != transcriptItemReadGroup || len(after[0].groupMembers) != 1 {
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
		got := groupReadTranscriptItems(buildTranscriptItems(entries, true), entries)
		if len(got) != 1 || got[0].kind != transcriptItemReadGroup || got[0].displayState != toolDisplayRunning {
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
		got := groupReadTranscriptItems(buildTranscriptItems(entries, false), entries)
		if len(got) != 3 || got[1].kind != transcriptItemToolResult {
			t.Fatalf("result-only boundary items = %+v, want read/result-only/read", got)
		}
		for i, item := range got {
			if i == 1 {
				continue
			}
			if item.kind != transcriptItemReadGroup || len(item.groupMembers) != 1 {
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
			got := groupReadTranscriptItems(buildTranscriptItems(entries, false), entries)
			for i, item := range got {
				if item.kind != transcriptItemReadGroup {
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
			got := groupReadTranscriptItems(buildTranscriptItems(entries, false), entries)
			singleton := func(item transcriptItem) bool {
				return item.kind == transcriptItemReadGroup && len(item.groupMembers) == 1
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
	items := groupReadTranscriptItems(buildTranscriptItems(entries, false), entries)
	if len(items) != 1 || items[0].kind != transcriptItemReadGroup || len(items[0].groupMembers) != 2 {
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
	if len(m.chatVisibleItems) != 1 || m.chatVisibleItems[0].kind != transcriptItemReadGroup || len(m.chatVisibleItems[0].groupMembers) != 2 {
		t.Fatalf("hidden reasoning between two reads should not stop them merging: %+v", m.chatVisibleItems)
	}
}

func TestReadGroupPageState(t *testing.T) {
	entries := append(renumberEntries(readExchange("one", "one.go", 0, 0, 0), 1), renumberEntries(readExchange("two", "two.go", 0, 0, 0), 3)...)
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemReadGroup {
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
