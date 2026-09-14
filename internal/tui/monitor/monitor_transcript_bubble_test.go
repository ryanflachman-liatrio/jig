package monitor

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// bubbleBackground is the raw SGR sequence for Theme.Chat.UserBubble
// (hexBBQ = #2D2C36 = rgb(45,44,54)), used to assert tint coverage without
// depending on shared.StyleBackground's internals.
const bubbleBackground = "\x1b[48;2;45;44;54m"

// FR-09.1/FR-09.2: a user-role text item renders with no literal "User"
// label and carries the bubble background on its content rows and both
// padding rows above/below.
func TestUserTextItemHasNoLabelAndTintedPadding(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "please continue"}}},
	}})
	raw := m.itemTranscriptBody()
	plain := stripANSI(raw)

	if strings.Contains(plain, "User") {
		t.Fatalf("rendered output still contains a literal User label:\n%s", plain)
	}
	rows := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(rows) < 3 {
		t.Fatalf("expected at least 3 bubble rows (padding, content, padding), got %d:\n%s", len(rows), plain)
	}
	if !strings.Contains(rows[0], bubbleBackground) {
		t.Fatalf("top padding row missing bubble background: %q", rows[0])
	}
	if !strings.Contains(rows[len(rows)-1], bubbleBackground) {
		t.Fatalf("bottom padding row missing bubble background: %q", rows[len(rows)-1])
	}
	for _, row := range rows[1 : len(rows)-1] {
		if !strings.Contains(row, bubbleBackground) {
			t.Fatalf("content row missing bubble background: %q", row)
		}
	}
}

// FR-09.3: assistant and user text begin at the same column, and only the
// user row carries a background tint.
func TestAssistantAndUserTextShareLeadingOffset(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "assistant reply"}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "operator input"}}},
	}})
	raw := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")

	assistantRow := ""
	userContentRow := ""
	for _, row := range rows {
		plain := stripANSI(row)
		switch {
		case strings.Contains(plain, "assistant reply") && assistantRow == "":
			assistantRow = row
		case strings.Contains(plain, "operator input") && userContentRow == "":
			userContentRow = row
		}
	}
	if assistantRow == "" || userContentRow == "" {
		t.Fatalf("could not locate both rows:\n%s", stripANSI(raw))
	}
	if strings.Contains(assistantRow, bubbleBackground) {
		t.Fatalf("assistant row unexpectedly carries the bubble background: %q", assistantRow)
	}
	assistantPlain, userPlain := stripANSI(assistantRow), stripANSI(userContentRow)
	assistantIdx := strings.Index(assistantPlain, "assistant")
	userIdx := strings.Index(userPlain, "operator")
	if assistantIdx < 0 || userIdx < 0 {
		t.Fatalf("could not locate first content rune: assistant=%q user=%q", assistantPlain, userPlain)
	}
	// Compare display-cell width rather than byte offset: the selection bar
	// "▌" is a single terminal cell but a multi-byte rune, so a byte-index
	// comparison would over-count a selected row's prefix.
	assistantOffset := lipgloss.Width(assistantPlain[:assistantIdx])
	userOffset := lipgloss.Width(userPlain[:userIdx])
	if assistantOffset != userOffset {
		t.Fatalf("leading offsets differ: assistant=%d user=%d\nassistant=%q\nuser=%q", assistantOffset, userOffset, assistantPlain, userPlain)
	}
}

// FR-09.10: a user bubble containing a fenced code block (glamour output that
// resets the background mid-row) has no visible cell lacking the bubble
// background after TintRow stabilization.
func TestUserBubbleSurvivesFencedCodeReset(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	text := "before\n\n```go\nfunc main() {}\n```\n\nafter"
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: text}}},
	}})
	raw := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	for i, row := range rows {
		if lipgloss.Width(stripANSI(row)) == 0 {
			continue
		}
		if !strings.Contains(row, bubbleBackground) {
			t.Fatalf("row %d has content but lacks the bubble background after a code fence:\n%q\nfull body:\n%s", i, row, stripANSI(raw))
		}
	}
}

// FR-09.9: a rendered bubble survives trimStructuralBlankEdges with its top
// and bottom tinted padding rows intact, since those rows carry an SGR
// escape and are not plain blank lines.
func TestUserBubbleSurvivesStructuralEdgeTrim(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 40
	rendered := m.renderUserBubble("  ", "hello")
	trimmed := trimStructuralBlankEdges(rendered)
	rows := strings.Split(trimmed, "\n")
	if len(rows) < 3 {
		t.Fatalf("expected padding+content+padding to survive the trim, got %d rows:\n%s", len(rows), stripANSI(trimmed))
	}
	if !strings.Contains(rows[0], bubbleBackground) || !strings.Contains(rows[len(rows)-1], bubbleBackground) {
		t.Fatalf("edge trim dropped a tinted padding row:\n%s", stripANSI(trimmed))
	}
}

// FR-09.13: a selected user bubble places the selection bar prefix on every
// bubble row (content and both padding rows), following prefixCardRows.
func TestSelectedUserBubbleCarriesPrefixOnEveryRow(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "please continue"}}},
	}})
	m.chatItemCursor = 0
	raw := m.itemTranscriptBody()
	rows := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	bar := stripANSI(shared.Theme.SelectedBar.Render(shared.CursorBar))
	for i, row := range rows {
		if !strings.HasPrefix(stripANSI(row), bar) {
			t.Fatalf("row %d missing selection bar prefix: %q", i, stripANSI(row))
		}
	}
}

// FR-09.11: Theme.Chat.UserBubble's background resolves to the existing
// hexBBQ token; no new palette constant should be needed for this spec.
func TestUserBubbleBackgroundIsHexBBQ(t *testing.T) {
	got := shared.StyleBackground(shared.Theme.Chat.UserBubble)
	if got != bubbleBackground {
		t.Fatalf("UserBubble background = %q, want %q (hexBBQ)", got, bubbleBackground)
	}
}
