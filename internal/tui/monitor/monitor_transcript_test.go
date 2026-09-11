package monitor

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/transcript"
)

// denseTranscriptFixture deliberately keeps each migration edge case in one
// place. Item-normalization and rendering tests share it without needing a
// transcript writer to accept malformed raw JSON.
func denseTranscriptFixture() []transcript.Entry {
	return []transcript.Entry{
		{
			Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{
				{Type: transcript.BlockText, Text: "I will inspect the file."},
				{Type: transcript.BlockThinking, Text: "Need the current implementation."},
				{Type: transcript.BlockToolUse, ToolUseID: "read-1", Name: "Read", Input: json.RawMessage(`{"file_path":"internal/tui/monitor/monitor.go"}`)},
			},
		},
		{
			Seq: 2, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{
				// The result intentionally precedes the later use in file order.
				{Type: transcript.BlockToolResult, ToolUseID: "edit-1", Content: "applied"},
				{Type: transcript.BlockToolResult, ToolUseID: "read-1", Content: "package monitor"},
			},
		},
		{
			Seq: 3, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{
				{Type: transcript.BlockToolUse, ToolUseID: "edit-1", Name: "Edit", Input: json.RawMessage(`{"file_path":"monitor.go"}`)},
				{Type: transcript.BlockToolUse, ToolUseID: "pending", Name: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`)},
				{Type: transcript.BlockToolUse, Name: "Unknown", Input: json.RawMessage(`{`)},
				{Type: transcript.BlockType("future_block"), Text: "future payload"},
			},
		},
		{
			Seq: 4, Generation: 0, Iteration: 1, Attempt: 1, Role: transcript.RoleResult,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "retry summary"}},
		},
	}
}

func TestDenseTranscriptFixtureCharacterizesNormalizationCases(t *testing.T) {
	entries := denseTranscriptFixture()
	if len(entries) != 4 {
		t.Fatalf("fixture entries = %d, want 4", len(entries))
	}

	var uses, results, thinking, text, unsupported, malformedInput int
	for _, entry := range entries {
		for _, block := range entry.Blocks {
			switch block.Type {
			case transcript.BlockToolUse:
				uses++
				if !json.Valid(block.Input) {
					malformedInput++
				}
			case transcript.BlockToolResult:
				results++
			case transcript.BlockThinking:
				thinking++
			case transcript.BlockText:
				text++
			default:
				unsupported++
			}
		}
	}

	if uses != 4 || results != 2 || thinking != 1 || text != 2 || unsupported != 1 || malformedInput != 1 {
		t.Fatalf("fixture coverage uses=%d results=%d thinking=%d text=%d unsupported=%d malformed=%d", uses, results, thinking, text, unsupported, malformedInput)
	}
	if entries[1].Blocks[0].ToolUseID != "edit-1" || entries[2].Blocks[0].ToolUseID != "edit-1" {
		t.Fatal("fixture no longer characterizes an out-of-order tool result")
	}
	if entries[2].Blocks[1].ToolUseID != "pending" {
		t.Fatal("fixture no longer characterizes a use-only tool exchange")
	}
	if entries[3].Generation != 0 || entries[3].Iteration != 1 || entries[3].Attempt != 1 {
		t.Fatal("fixture no longer characterizes an execution boundary")
	}
}

func TestTranscriptScrollHotkeysUseConfiguredRowCounts(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	m.chatVP.SetContent(strings.Repeat("line\n", 100))
	m.chatVP.GotoTop()
	m.chatAutoScroll = false

	for _, tc := range []struct {
		key  string
		want int
	}{
		{key: "j", want: 2},
		{key: "J", want: 12},
		{key: "k", want: 10},
		{key: "K", want: 0},
	} {
		m, _ = m.Update(key(tc.key))
		if got := m.chatVP.YOffset(); got != tc.want {
			t.Fatalf("after %q offset = %d, want %d", tc.key, got, tc.want)
		}
	}
}

func TestTranscriptMouseWheelPreservesAndRestoresFollowOnAppend(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	m.focus = focusTranscript
	panels := m.mousePanels()
	base := strings.Repeat("finalized line\n", 40)
	m.chatVP.SetContent(base)
	m.chatVP.GotoBottom()
	m.chatAutoScroll = true

	m, _ = m.Update(tea.MouseWheelMsg{X: panels.transcript.x, Y: panels.transcript.y, Button: tea.MouseWheelUp})
	away := m.chatVP.YOffset()
	if m.chatAutoScroll || m.chatVP.AtBottom() {
		t.Fatal("wheel above bottom did not disable transcript follow")
	}
	m.chatVP.SetContent(base + strings.Repeat("new finalized line\n", 5))
	if m.chatVP.YOffset() != away {
		t.Fatalf("append moved reader from %d to %d while follow was disabled", away, m.chatVP.YOffset())
	}

	m.chatVP.GotoBottom()
	m.updateTranscriptFollow(m.chatVP.AtBottom())
	if !m.chatAutoScroll {
		t.Fatal("reaching bottom did not restore transcript follow")
	}
	m.chatVP.SetContent(base + strings.Repeat("new finalized line\n", 10))
	if m.chatAutoScroll {
		m.chatVP.GotoBottom()
	}
	if !m.chatVP.AtBottom() {
		t.Fatal("followed transcript did not remain at bottom after append")
	}
}

func TestFilePreviewMouseWheelPreservesSelection(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	m.focus = focusTranscript
	m.selKind = "file"
	m.selFile = "/synthetic/output.txt"
	m.cursor = 1
	m.chatVP.SetContent(strings.Repeat("file line\n", 50))
	m.chatVP.GotoTop()
	panels := m.mousePanels()
	m, _ = m.Update(tea.MouseWheelMsg{X: panels.transcript.x, Y: panels.transcript.y, Button: tea.MouseWheelDown})
	if m.chatVP.YOffset() != 3 || m.selKind != "file" || m.selFile != "/synthetic/output.txt" || m.cursor != 1 {
		t.Fatalf("file wheel offset/kind/file/cursor = %d/%q/%q/%d", m.chatVP.YOffset(), m.selKind, m.selFile, m.cursor)
	}
}

func TestBuildTranscriptItemsPairsFixtureToolsByScopedFIFO(t *testing.T) {
	items := buildTranscriptItems(denseTranscriptFixture(), true)
	if len(items) != 8 {
		t.Fatalf("items = %d, want 8", len(items))
	}

	var read, edit, pending, resultOnly *transcriptItem
	for i := range items {
		item := &items[i]
		switch {
		case item.kind == transcriptItemToolExchange && item.coord.toolUseID == "read-1":
			read = item
		case item.kind == transcriptItemToolExchange && item.coord.toolUseID == "edit-1":
			edit = item
		case item.kind == transcriptItemToolExchange && item.coord.toolUseID == "pending":
			pending = item
		case item.kind == transcriptItemToolResult:
			resultOnly = item
		}
	}
	if read == nil || read.toolUse == nil || read.toolResult == nil || read.displayState != toolDisplaySuccess {
		t.Fatalf("read item = %+v, want successful paired exchange", read)
	}
	if edit == nil || edit.toolUse == nil || edit.toolResult == nil || edit.primary.key.seq != 3 {
		t.Fatalf("edit item = %+v, want use-anchored paired exchange", edit)
	}
	if pending == nil || pending.toolResult != nil || pending.displayState != toolDisplayRunning {
		t.Fatalf("pending item = %+v, want running use-only exchange", pending)
	}
	if resultOnly != nil {
		t.Fatalf("unexpected result-only item after matching out-of-order result: %+v", resultOnly)
	}
}

// TestIsStructuralBlankRawBytesSemantics locks slice-04 FR-04.1: the
// predicate must return true only when a line's raw bytes contain
// exclusively ASCII whitespace, so a tinted card padding row (whose bytes
// include SGR escapes) is treated as content while a bare " " line is not.
func TestIsStructuralBlankRawBytesSemantics(t *testing.T) {
	tintedPadding := "\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) + "\x1b[49m"
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: true},
		{name: "single space", in: " ", want: true},
		{name: "spaces and tabs", in: " \t  \t", want: true},
		{name: "carriage return", in: "\r", want: true},
		{name: "vertical tab", in: "\v", want: true},
		{name: "form feed", in: "\f", want: true},
		{name: "plain glyph", in: "x", want: false},
		{name: "leading escape byte only", in: "\x1b", want: false},
		{name: "tinted padding row", in: tintedPadding, want: false},
		{name: "glamour blank row", in: "\x1b[38;2;80;80;80m\x1b[0m", want: false},
		{name: "unicode nbsp", in: "\u00a0", want: false},
		{name: "content with trailing space", in: "hi ", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStructuralBlank(tt.in); got != tt.want {
				t.Fatalf("isStructuralBlank(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestTrimStructuralBlankEdges locks slice-04 FR-04.2 / FR-04.3: the wrapper
// splits on "\n", drops structurally-blank leading and trailing lines,
// returns "" for an entirely blank input, and preserves the tinted card
// padding row byte-for-byte so slice-09's user-message bubble can rely on
// the same trimmer without a card-side padding marker.
func TestTrimStructuralBlankEdges(t *testing.T) {
	tintedPadding := "\x1b[48;2;26;25;31m" + strings.Repeat(" ", 20) + "\x1b[49m"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "single blank", in: " ", want: ""},
		{name: "single content line no newline", in: "only", want: "only"},
		{name: "trailing newline", in: "a\n", want: "a"},
		{name: "leading blanks", in: "\n\na", want: "a"},
		{name: "trailing blanks", in: "a\n\n", want: "a"},
		{name: "leading and trailing blanks", in: "\n\na\n\n", want: "a"},
		{name: "interior blank preserved", in: "a\n\nb", want: "a\n\nb"},
		{name: "all blank multi-line", in: "\n \n\t\n", want: ""},
		{name: "tinted padding alone", in: tintedPadding, want: tintedPadding},
		{name: "tinted padding surrounded by plain blanks",
			in:   "\n" + tintedPadding + "\ncontent\n" + tintedPadding + "\n",
			want: tintedPadding + "\ncontent\n" + tintedPadding},
		{name: "glamour blank row survives",
			in:   "\x1b[38;2;80;80;80m\x1b[0m",
			want: "\x1b[38;2;80;80;80m\x1b[0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimStructuralBlankEdges(tt.in); got != tt.want {
				t.Fatalf("trimStructuralBlankEdges(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestVerticalRhythmHelperCallSites locks slice-04 FR-04.4: the two
// edge-trim helpers must not silently swap. stripBlankEdges (SGR-aware) is
// correct for Glamour output normalization inside renderNewCodeCard and
// fileBody; trimStructuralBlankEdges (raw-bytes) is correct for per-item
// trimming inside itemTranscriptBody. Anything else is a regression.
func TestVerticalRhythmHelperCallSites(t *testing.T) {
	items, err := os.ReadFile("monitor_transcript_items_view.go")
	if err != nil {
		t.Fatalf("read monitor_transcript_items_view.go: %v", err)
	}
	itemsView := string(items)

	transcriptSrc, err := os.ReadFile("monitor_transcript.go")
	if err != nil {
		t.Fatalf("read monitor_transcript.go: %v", err)
	}
	transcriptView := string(transcriptSrc)

	// stripBlankEdges in monitor_transcript_items_view.go must appear only
	// inside renderNewCodeCard.
	if got := countCallsInFunction(t, itemsView, "renderNewCodeCard", "stripBlankEdges("); got == 0 {
		t.Fatalf("expected stripBlankEdges( call inside renderNewCodeCard; not found")
	}
	if got, want := strings.Count(itemsView, "stripBlankEdges("), 1; got != want {
		t.Fatalf("stripBlankEdges( occurrences in monitor_transcript_items_view.go = %d, want %d (only renderNewCodeCard)", got, want)
	}

	// trimStructuralBlankEdges( in monitor_transcript_items_view.go must
	// appear only inside itemTranscriptBody.
	if got := countCallsInFunction(t, itemsView, "itemTranscriptBody", "trimStructuralBlankEdges("); got == 0 {
		t.Fatalf("expected trimStructuralBlankEdges( call inside itemTranscriptBody; not found")
	}
	if got, want := strings.Count(itemsView, "trimStructuralBlankEdges("), 1; got != want {
		t.Fatalf("trimStructuralBlankEdges( occurrences in monitor_transcript_items_view.go = %d, want %d (only itemTranscriptBody)", got, want)
	}

	// stripBlankEdges call sites in monitor_transcript.go: the definition
	// (`func stripBlankEdges`), a call inside chatBody's write path, and a
	// call inside fileBody. Anything else is a new consumer that must be
	// audited against the two semantics.
	if got := strings.Count(transcriptView, "stripBlankEdges("); got < 2 {
		t.Fatalf("stripBlankEdges( call sites in monitor_transcript.go = %d, want >= 2 (definition + callers)", got)
	}
	if !strings.Contains(transcriptView, "func stripBlankEdges(") {
		t.Fatalf("stripBlankEdges definition missing from monitor_transcript.go")
	}

	// trimStructuralBlankEdges must be defined in monitor_transcript.go and
	// must not be called from monitor_transcript.go (the transcript-item
	// loop is the sole consumer).
	if !strings.Contains(transcriptView, "func trimStructuralBlankEdges(") {
		t.Fatalf("trimStructuralBlankEdges definition missing from monitor_transcript.go")
	}
	if got, want := strings.Count(transcriptView, "trimStructuralBlankEdges("), 1; got != want {
		t.Fatalf("trimStructuralBlankEdges( occurrences in monitor_transcript.go = %d, want %d (definition only)", got, want)
	}
}

// countCallsInFunction returns how many times needle appears in the body of
// the named top-level function inside src. Brace-matching is naive but
// sufficient for the Monitor package's Go source, where string literals do
// not contain unbalanced braces at the top level of function bodies.
func countCallsInFunction(t *testing.T, src, funcName, needle string) int {
	t.Helper()
	sig := "func " + funcName + "("
	idx := strings.Index(src, sig)
	if idx < 0 {
		// Try method form: func (m ...) funcName(
		idx = strings.Index(src, ") "+funcName+"(")
		if idx < 0 {
			t.Fatalf("function %s not found in source", funcName)
		}
	}
	open := strings.Index(src[idx:], "{")
	if open < 0 {
		t.Fatalf("function %s has no opening brace", funcName)
	}
	body := src[idx+open:]
	depth := 0
	end := -1
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("function %s body did not close", funcName)
	}
	return strings.Count(body[:end], needle)
}
