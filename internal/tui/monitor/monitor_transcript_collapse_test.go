package monitor

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/transcript"
)

// FR-09.4/FR-09.5/FR-09.14: a user-role text block over chatTextCollapseBytes
// renders exactly one dim summary row, and the markdown renderer is never
// invoked for it (proven by the render cache staying empty for that block,
// since renderMarkdown unconditionally populates chatRendered on every call).
func TestOversizedUserTextCollapsesToSummaryRow(t *testing.T) {
	body := strings.Repeat("x", chatTextCollapseBytes+1)
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: body}}},
	}})
	if !m.chatItems[0].oversized {
		t.Fatalf("expected item to be flagged oversized")
	}
	raw := m.itemTranscriptBody()
	plain := stripANSI(raw)
	if strings.Contains(plain, "xxxx") {
		t.Fatalf("collapsed item leaked block content into the rendered body:\n%s", plain)
	}
	if !strings.Contains(plain, "User input") {
		t.Fatalf("collapsed summary missing generic label:\n%s", plain)
	}
	if !strings.Contains(plain, "1 line") {
		t.Fatalf("collapsed summary missing line count:\n%s", plain)
	}
	key := m.chatItems[0].primary.key
	if _, ok := m.chatRendered[key]; ok {
		t.Fatalf("collapsed item populated the markdown render cache; it must never invoke the renderer")
	}
}

// FR-09.7: a heading-led block summarizes with that heading's text; a
// heading-less block falls back to the generic "User input" label.
func TestCollapseSummaryLabelDerivation(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "heading present", text: "# Session update\n\nrest of the body", want: "Session update"},
		{name: "no heading", text: "plain pasted content\nwith no heading", want: "User input"},
		{name: "blank lines before heading", text: "\n\n## Retry prompt\n\nbody", want: "Retry prompt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collapseSummaryLabel(tt.text); got != tt.want {
				t.Fatalf("collapseSummaryLabel(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// FR-09.8: at a narrow width, the summary row is ANSI-aware truncated with a
// trailing ellipsis and never exceeds the available content width.
func TestCollapseSummaryTruncatesAtNarrowWidth(t *testing.T) {
	text := "# A very long heading that will not fit in a narrow panel\n\nbody"
	const width = 20
	summary := buildCollapseSummary(text, width)
	if got := lipgloss.Width(summary); got > width {
		t.Fatalf("summary width = %d, want <= %d: %q", got, width, summary)
	}
	if !strings.HasSuffix(summary, "…") {
		t.Fatalf("summary missing trailing ellipsis at narrow width: %q", summary)
	}
}

// FR-09.16: an oversized assistant-role text block still renders full
// markdown; the collapse check is scoped to user-role text only.
func TestOversizedAssistantTextIsNeverCollapsed(t *testing.T) {
	body := strings.Repeat("y", chatTextCollapseBytes+1)
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: body}}},
	}})
	if m.chatItems[0].oversized {
		t.Fatalf("assistant item must never be flagged oversized")
	}
	plain := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(plain, "yyyy") {
		t.Fatalf("oversized assistant text did not render in full:\n%.200s", plain)
	}
	if strings.Contains(plain, "User input") {
		t.Fatalf("assistant text incorrectly rendered a collapse summary:\n%.200s", plain)
	}
}

// FR-09.15: itemHasDetail is true only for an oversized user-role text item;
// a user-role text item at or under the threshold is not expandable.
func TestItemHasDetailOversizedBoundary(t *testing.T) {
	tests := []struct {
		name string
		size int
		want bool
	}{
		{name: "at threshold", size: chatTextCollapseBytes, want: false},
		{name: "over threshold", size: chatTextCollapseBytes + 1, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m.transcriptInnerW = 60
			m.setChatPage(transcript.Page{Entries: []transcript.Entry{
				{Seq: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: strings.Repeat("z", tt.size)}}},
			}})
			if got := itemHasDetail(m.chatItems[0]); got != tt.want {
				t.Fatalf("itemHasDetail at size %d = %v, want %v", tt.size, got, tt.want)
			}
		})
	}
}

// humanizeByteSize is exercised indirectly by buildCollapseSummary above;
// this covers its own boundary directly since it has no other caller-visible
// proof artifact.
func TestHumanizeByteSize(t *testing.T) {
	if got := humanizeByteSize(512); got != "512 B" {
		t.Fatalf("humanizeByteSize(512) = %q, want %q", got, "512 B")
	}
	if got := humanizeByteSize(chatTextCollapseBytes + 1); !strings.HasSuffix(got, "KiB") {
		t.Fatalf("humanizeByteSize(%d) = %q, want a KiB suffix", chatTextCollapseBytes+1, got)
	}
}
