package monitor

import (
	"encoding/json"
	"fmt"
	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func TestReadCompactFailuresAndIncompleteCallsSplitGroups(t *testing.T) {
	for _, name := range []string{"failed", "missing result", "missing target", "malformed input"} {
		t.Run(name, func(t *testing.T) {
			middle := toolExchange("middle", "read", map[string]any{"path": "middle.go"}, 0, 0, 0, name == "failed")
			switch name {
			case "failed":
				middle[1].Blocks[0].Tool.Content = []toolcall.Content{{Type: "text", Text: "denied"}}
			case "missing result":
				middle = middle[:1]
			case "missing target":
				middle[0].Blocks[0].Tool.Input = json.RawMessage(`{}`)
			case "malformed input":
				middle[0].Blocks[0].Tool.Input = json.RawMessage(`{"path":`)
			}
			m := newMonitorWithSteps(t)
			m.compactToolGroups = true
			m.chatStep = "a"
			m.setChatPage(transcript.Page{Entries: toolGroupEntries(
				readExchange("before", "before.go", 0, 0, 0),
				middle,
				readExchange("after", "after.go", 0, 0, 0),
			)})
			wantKind := transcriptItemToolExchange
			if name == "missing result" {
				wantKind = transcriptItemToolGroup
			}
			if len(m.chatItems) != 3 || m.chatItems[1].kind != wantKind {
				t.Fatalf("boundary was swallowed into a read group: %+v", m.chatItems)
			}
			if len(m.chatItems[0].groupMembers) != 1 || len(m.chatItems[2].groupMembers) != 1 {
				t.Fatal("reads grouped across an incomplete or failed exchange")
			}
			if name == "failed" {
				if !strings.Contains(ansi.Strip(m.itemTranscriptBody()), "denied") {
					t.Fatal("failed read no longer exposes its error hint inline")
				}
				m.chatItemExpand[m.chatItems[1].key] = true
				if !strings.Contains(ansi.Strip(m.itemTranscriptBody()), "synthetic middle output") {
					t.Fatal("failed read lost its expanded output")
				}
			}
		})
	}
}

func TestReadCompactSummaryPreservesSelectors(t *testing.T) {
	line := 17
	for _, tt := range []struct {
		input string
		want  string
	}{
		{`{"path":"synthetic/a.go","offset":4,"limit":3}`, "a.go:4-6"},
		{`{"path":"synthetic/a.go","offset":4}`, "a.go:4"},
		{`{"path":"synthetic/a.go","offset":"bad","limit":-1}`, "a.go:17"},
	} {
		activity := &toolcall.Activity{Kind: "read", Input: json.RawMessage(tt.input), Locations: []toolcall.Location{{Path: "synthetic/a.go", Line: &line}}}
		if got := summarizeActivity(activity, 80).detail; got != tt.want {
			t.Fatalf("read summary = %q, want %q", got, tt.want)
		}
	}
}

func TestReadCompactionWithLocationOnlyActivitiesAfterReopen(t *testing.T) {
	first := readExchange("first", "unused", 0, 0, 0)
	second := readExchange("second", "unused", 0, 0, 0)
	listing := readExchange("listing", "unused", 0, 0, 0)
	line := 12
	for _, entry := range first {
		entry.Blocks[0].Tool.Input = nil
		entry.Blocks[0].Tool.Title = "Read synthetic/first.go"
		entry.Blocks[0].Tool.Locations = []toolcall.Location{{Path: "synthetic/first.go", Line: &line}}
	}
	for _, entry := range second {
		entry.Blocks[0].Tool.Input = nil
		entry.Blocks[0].Tool.Title = "Read synthetic/second.go"
		entry.Blocks[0].Tool.Locations = []toolcall.Location{{Path: "synthetic/second.go"}}
	}
	second[1].Blocks[0].Tool.Locations = nil
	second[1].Blocks[0].Tool.Title = "Read"
	for _, entry := range listing {
		entry.Blocks[0].Tool.Input = nil
		entry.Blocks[0].Tool.Title = "List synthetic/source"
	}
	runDir := writeTranscript(t, "a", toolGroupEntries(first, second, listing))
	m := newMonitorWithSteps(t)
	m.RunDir = datastore.RunAt(runDir)
	m.compactToolGroups = true
	m = enterChatStep(t, m, "a")
	if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup || len(m.chatItems[0].groupMembers) != 3 {
		t.Fatalf("location/title-only reads did not compact: %+v", m.chatItems)
	}
	for _, compact := range []bool{true, false} {
		if m.compactToolGroups != compact {
			m, _ = m.Update(key("c"))
		}
		body := ansi.Strip(m.itemTranscriptBody())
		for _, want := range []string{"first.go:12", "second.go", "List synthetic/source"} {
			if !strings.Contains(body, want) {
				t.Fatalf("read summary missing %q:\n%s", want, body)
			}
		}
	}
	for _, entry := range m.chatEntries {
		if len(entry.Blocks[0].Tool.Input) != 0 {
			t.Fatal("rendering fabricated input for a location/title-only activity")
		}
	}
	if len(m.chatEntries[3].Blocks[0].Tool.Locations) != 0 {
		t.Fatal("summary fallback mutated the recorded result locations")
	}
}

func TestReadSummaryUsesNormalizedMetadata(t *testing.T) {
	line := 9
	for _, tt := range []struct {
		name     string
		activity toolcall.Activity
		want     string
	}{
		{"locations", toolcall.Activity{Kind: "read", Title: "Read", Locations: []toolcall.Location{{Path: "synthetic/a.go"}, {Path: "synthetic/b.go", Line: &line}}}, "a.go, b.go:9"},
		{"descriptive title", toolcall.Activity{Kind: "read", Title: "Read synthetic/source.go"}, "Read synthetic/source.go"},
		{"listing title", toolcall.Activity{Kind: "read", Title: "List synthetic/source"}, "List synthetic/source"},
		{"bare title", toolcall.Activity{Kind: "read", Title: "Read"}, ""},
		{"generic title", toolcall.Activity{Kind: "read", Title: "Tool"}, ""},
		{"remote location", toolcall.Activity{Kind: "read", Title: "Read remote", Locations: []toolcall.Location{{Path: "https://example.invalid/a"}}}, ""},
		{"input precedence", toolcall.Activity{Kind: "read", Input: json.RawMessage(`{"path":"synthetic/input.go"}`), Locations: []toolcall.Location{{Path: "synthetic/location.go"}}}, "input.go"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, eligible := readSummaryForActivity(&tt.activity)
			if got != tt.want || eligible != (tt.want != "") {
				t.Fatalf("summary=%q eligible=%v, want %q", got, eligible, tt.want)
			}
		})
	}
}

func TestToolCompactionPoliciesAfterReopen(t *testing.T) {
	for _, kind := range []string{"read", "bash"} {
		t.Run(kind, func(t *testing.T) {
			var exchanges [][]transcript.Entry
			for i := range 6 {
				arg := "file_path"
				if kind == "bash" {
					arg = "command"
				}
				exchanges = append(exchanges, toolExchange(fmt.Sprint(i), kind, map[string]any{arg: fmt.Sprintf("target-%d", i)}, 0, 0, 0, false))
			}
			runDir := writeTranscript(t, "a", toolGroupEntries(exchanges...))
			// Each model loads the persisted transcript from scratch, just as
			// reopening an existing run does after restarting jig.
			for _, compact := range []bool{false, true} {
				m := newMonitorWithSteps(t)
				m.RunDir = datastore.RunAt(runDir)
				m.compactToolGroups = compact
				m = enterChatStep(t, m, "a")
				if !compact && kind != "read" {
					if len(m.chatItems) != 6 || m.chatItems[0].kind != transcriptItemToolExchange {
						t.Fatalf("compact off: got %d items, want six standalone exchanges", len(m.chatItems))
					}
					m, _ = m.Update(key("c"))
				}
				if len(m.chatItems) != 1 || m.chatItems[0].kind != transcriptItemToolGroup {
					t.Fatalf("compact on: got %+v, want shared tool group", m.chatItems)
				}
				group := m.chatItems[0]
				body := ansi.Strip(m.itemTranscriptBody())
				if !strings.Contains(body, shared.CollapsedMarker) {
					t.Fatalf("group lacks collapse marker:\n%s", body)
				}
				previews := []string{"target-0", "target-1", "target-2", "… 2 more calls", "target-5"}
				if kind == "read" {
					previews = []string{"target-0", "target-1", "target-2", "target-3", "target-4", "target-5"}
				}
				for _, want := range previews {
					if !strings.Contains(body, want) {
						t.Fatalf("collapsed preview missing %q:\n%s", want, body)
					}
				}
				if (kind != "read" && strings.Contains(body, "target-3")) || strings.Contains(body, "synthetic") {
					t.Fatalf("collapsed preview exposed omitted calls or output:\n%s", body)
				}
				payload := m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader()
				if payload.Err != nil || !strings.Contains(payload.Payload, "target-3") || !strings.Contains(payload.Payload, "synthetic 3 output") {
					t.Fatal("collapsed group copy lost omitted member evidence")
				}
				m, _ = m.Update(key("enter"))
				if len(m.chatCursorTargets) != 7 {
					t.Fatalf("expanded navigation targets=%d, want header and six children", len(m.chatCursorTargets))
				}
				body = ansi.Strip(m.itemTranscriptBody())
				if !strings.Contains(body, "target-3") || strings.Contains(body, "synthetic 0 output") {
					t.Fatalf("group expansion must show child cards with output still folded:\n%s", body)
				}
				m, _ = m.Update(key("n"))
				if m.selectedTranscriptItemKey() != group.groupMembers[0].key {
					t.Fatal("n did not select first child")
				}
				m, _ = m.Update(key("enter"))
				if !strings.Contains(ansi.Strip(m.itemTranscriptBody()), "synthetic 0 output") {
					t.Fatal("child expansion did not reveal output")
				}
				payload = m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader()
				if payload.Err != nil || !strings.Contains(payload.Payload, "synthetic 0 output") || strings.Contains(payload.Payload, "target-1") {
					t.Fatal("child copy did not isolate the selected exchange")
				}
				m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 35})
				m.setChatPage(m.chatPage)
				if m.selectedTranscriptItemKey() != group.groupMembers[0].key || !m.chatItemExpand[group.groupMembers[0].key] {
					t.Fatal("resize/reload lost child selection or expansion")
				}
				m, _ = m.Update(key("c"))
				wantItems := 6
				if kind == "read" {
					wantItems = 1
				}
				if len(m.chatItems) != wantItems || m.selectedTranscriptItemKey() != group.groupMembers[0].key {
					t.Fatal("compact toggle did not preserve the read policy and child selection")
				}
				m, _ = m.Update(key("c"))
				m.searchQuery = "target-3"
				m.rerunSearch()
				m.applyCurrentSearchHit()
				if len(m.chatVisibleItems) != 1 || m.selectedTranscriptItemKey() != group.groupMembers[3].key {
					t.Fatal("search did not retain the group and select its omitted member")
				}
				m, _ = m.Update(key("x"))
				m.chatItemExpand[group.key] = false
				for _, width := range []int{12, 40, 100} {
					m.transcriptInnerW = width
					for _, line := range strings.Split(m.itemTranscriptBody(), "\n") {
						if lipgloss.Width(line) > width {
							t.Fatalf("collapsed preview exceeds width %d: %q", width, line)
						}
					}
				}
			}
		})
	}
}
