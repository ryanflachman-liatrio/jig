package monitor

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

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
