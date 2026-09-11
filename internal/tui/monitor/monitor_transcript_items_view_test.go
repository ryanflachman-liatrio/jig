package monitor

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/step"
	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

func syntheticExchange(id, status string) []transcript.Entry {
	entries := []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: id, Kind: "read", Title: "Reading synthetic config"}}},
	}}
	if status != "" {
		entries = append(entries, transcript.Entry{
			Seq: 2, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: id, Kind: "read", Status: status}}},
		})
	}
	return entries
}

func TestToolExchangeHeaderCardStateMapping(t *testing.T) {
	tests := []struct {
		name string
		got  toolDisplayState
		want shared.CardState
	}{
		{name: "success", got: toolDisplaySuccess, want: shared.CardSuccess},
		{name: "error", got: toolDisplayError, want: shared.CardError},
		{name: "running", got: toolDisplayRunning, want: shared.CardRunning},
		{name: "incomplete use", got: toolDisplayUnknownUse, want: shared.CardWarning},
		{name: "incomplete result", got: toolDisplayUnknownResult, want: shared.CardWarning},
		{name: "invalid", got: toolDisplayState(99), want: shared.CardWarning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardState(tt.got); got != tt.want {
				t.Fatalf("cardState(%v) = %v, want %v", tt.got, got, tt.want)
			}
		})
	}
}

func TestToolExchangeHeaderCardStatesAndWidths(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		running   bool
		wantState toolDisplayState
		wantText  string
	}{
		{name: "success", status: "completed", wantState: toolDisplaySuccess},
		{name: "failed", status: "failed", wantState: toolDisplayError, wantText: "failed"},
		{name: "running use only", running: true, wantState: toolDisplayRunning, wantText: "running"},
		{name: "terminal incomplete use only", wantState: toolDisplayUnknownUse, wantText: "incomplete"},
	}
	for _, width := range []int{40, 72} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s_width_%d", tt.name, width), func(t *testing.T) {
				m := newMonitorWithSteps(t)
				m.transcriptInnerW = width
				m.steps[m.index["a"]].status = step.StatusPending
				if tt.running {
					m.steps[m.index["a"]].status = step.StatusRunning
				}
				m.chatStep = "a"
				m.setChatPage(transcript.Page{Entries: syntheticExchange("state", tt.status)})
				if len(m.chatItems) != 1 || m.chatItems[0].displayState != tt.wantState {
					t.Fatalf("display state = %+v, want %v", m.chatItems, tt.wantState)
				}
				body := m.itemTranscriptBody()
				rows := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
				if len(rows) != 2 {
					t.Fatalf("header-only card rows = %d, want 2:\n%s", len(rows), stripANSI(body))
				}
				for i, row := range rows {
					if got := lipgloss.Width(row); got != width {
						t.Fatalf("row %d width = %d, want %d: %q", i, got, width, row)
					}
				}
				plain := stripANSI(body)
				if !strings.HasPrefix(plain, "  ▌ ╭") || !strings.Contains(plain, "\n  ▌ ╰") {
					t.Fatalf("selected prefix was not applied to both rows:\n%s", plain)
				}
				if tt.wantText != "" && !strings.Contains(plain, tt.wantText) {
					t.Fatalf("header missing %q:\n%s", tt.wantText, plain)
				}
			})
		}
	}
}

func TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes(t *testing.T) {
	entries := append(syntheticExchange("one", "completed"), []transcript.Entry{
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "two", Kind: "read", Title: "Reading second"}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "two", Kind: "read", Status: "failed"}}}},
	}...)
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	plain := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(plain, "  ▌ ╭") || !strings.Contains(plain, "\n  ╭") {
		t.Fatalf("selected/unselected card prefixes missing:\n%s", plain)
	}
	for _, row := range strings.Split(strings.TrimSuffix(m.itemTranscriptBody(), "\n"), "\n") {
		if row == "" {
			continue
		}
		if got := lipgloss.Width(row); got != 60 {
			t.Fatalf("prefixed card row width = %d, want 60: %q", got, row)
		}
	}
	if len(m.chatItemRendered) != 2 {
		t.Fatalf("card cache size = %d, want 2", len(m.chatItemRendered))
	}
}

func TestOrphanAndNonExchangeItemsStayFlat(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "assistant prose"}}},
		{Seq: 2, Role: transcript.RoleSystem, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "system prose"}}},
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "synthetic reasoning"}}},
		{Seq: 4, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockType("future"), Text: "future content"}}},
		{Seq: 5, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "orphan", Kind: "read", Status: "failed"}}}},
	}
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	plain := stripANSI(m.itemTranscriptBody())
	if strings.ContainsAny(plain, "╭╰") {
		t.Fatalf("non-exchange item acquired a card frame:\n%s", plain)
	}
	for _, want := range []string{"assistant prose", "system prose", "reasoning", "Unsupported future", "Result (unknown origin) failed"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("flat output missing %q:\n%s", want, plain)
		}
	}
}

func TestToolExchangeRendersHeaderOnlyCardAndOrphanStaysFlat(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "read-1", Kind: "read", Title: "Reading config"}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "read-1", Kind: "read", Status: "completed"}}}},
		{Seq: 3, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "orphan", Kind: "read", Status: "failed"}}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	body := m.itemTranscriptBody()
	plain := stripANSI(body)
	if strings.Count(plain, "╭") != 1 || strings.Count(plain, "╰") != 1 {
		t.Fatalf("paired exchange should have exactly one framed header card:\n%s", plain)
	}
	if !strings.Contains(plain, "Result (unknown origin) failed") {
		t.Fatalf("orphan result presentation changed:\n%s", plain)
	}
	for _, row := range strings.Split(body, "\n") {
		if lipgloss.Width(row) > m.transcriptInnerW {
			t.Fatalf("row overflows transcript width: %d > %d: %q", lipgloss.Width(row), m.transcriptInnerW, row)
		}
	}
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("card cache size = %d, want one exchange entry", len(m.chatItemRendered))
	}
}

func TestToolExchangeCardCacheRefreshesOnPageReplacementAndWidthChange(t *testing.T) {
	page := transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "tool-1", Kind: "read", Title: "Reading"}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "tool-1", Kind: "read", Status: "completed"}}}},
	}}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.rebuildRenderer()
	m.setChatPage(page)
	first := m.itemTranscriptBody()
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("first render cache = %d, want 1", len(m.chatItemRendered))
	}
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("cached render grew cache to %d", len(m.chatItemRendered))
	}

	page.Entries[1].Blocks[0].Tool.Status = "failed"
	m.setChatPage(page)
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("page replacement retained card cache: %d", len(m.chatItemRendered))
	}
	second := m.itemTranscriptBody()
	if first == second || !strings.Contains(stripANSI(second), "failed") {
		t.Fatalf("replacement did not refresh card output:\n%s", stripANSI(second))
	}

	m.transcriptInnerW = 44
	m.rebuildRenderer()
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("width rebuild retained card cache: %d", len(m.chatItemRendered))
	}
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("width rebuild did not cache current variant: %d", len(m.chatItemRendered))
	}
}

func TestStructuredEditShowsNewCodeInDefaultOpenCard(t *testing.T) {
	oldCode := "func greeting() string { return \"old implementation\" }"
	newCode := "func greeting() string {\n\treturn \"new implementation\"\n}"
	entries := []transcript.Entry{
		{
			Seq:  1,
			Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{
				Type: transcript.BlockToolUse,
				Tool: &toolcall.Activity{ID: "edit-1", Kind: "edit", Title: "Editing files"},
			}},
		},
		{
			Seq:  2,
			Role: transcript.RoleUser,
			Blocks: []transcript.Block{{
				Type: transcript.BlockToolResult,
				Tool: &toolcall.Activity{
					ID:     "edit-1",
					Kind:   "edit",
					Status: "completed",
					Content: []toolcall.Content{{Diff: &toolcall.Diff{
						Path:    "internal/greeting.go",
						OldText: &oldCode,
						NewText: newCode,
					}}},
				},
			}},
		},
	}

	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 1 || !m.chatItemExpand[m.chatItems[0].key] {
		t.Fatalf("structured edit was not expanded by default: %+v", m.chatItemExpand)
	}

	body := stripANSI(m.itemTranscriptBody())
	for _, want := range []string{"New code · internal/greeting.go", "new implementation", "╭", "╰"} {
		if !strings.Contains(body, want) {
			t.Fatalf("default-open code card missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"old implementation", "old:"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("code card included %q:\n%s", unwanted, body)
		}
	}

	m.chatItemExpand[m.chatItems[0].key] = false
	if body := stripANSI(m.itemTranscriptBody()); strings.Contains(body, "new implementation") {
		t.Fatalf("folded code card remained visible:\n%s", body)
	}
}
