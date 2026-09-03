package monitor

import (
	"strings"
	"testing"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

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
