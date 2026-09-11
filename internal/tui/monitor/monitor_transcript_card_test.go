package monitor

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

func syntheticExchangePage(count int, detailRows int) transcript.Page {
	entries := make([]transcript.Entry, 0, count*2)
	seq := 1
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("tool-%03d", i)
		entries = append(entries, transcript.Entry{
			Seq: seq, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{
				ID: id, Kind: "read", Title: fmt.Sprintf("Read synthetic-%03d", i),
			}}},
		})
		seq++
		content := []toolcall.Content(nil)
		if detailRows > 0 && i == count/2 {
			content = []toolcall.Content{{Type: "text", Text: strings.Repeat("synthetic detail row\n", detailRows)}}
		}
		entries = append(entries, transcript.Entry{
			Seq: seq, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{
				ID: id, Kind: "read", Status: "completed", Content: content,
			}}},
		})
		seq++
	}
	return transcript.Page{Entries: entries}
}

func cloneLineRanges(in map[transcriptLineKey]lineRange) map[transcriptLineKey]lineRange {
	out := make(map[transcriptLineKey]lineRange, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func TestTranscriptCardLineRangesCachedAndFresh(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.chatVP.SetHeight(6)
	m.setChatPage(syntheticExchangePage(3, 30))
	m.chatItemExpand[m.chatItems[1].key] = true

	fresh := m.itemTranscriptBody()
	freshRanges := cloneLineRanges(m.chatItemLineRanges)
	if len(freshRanges) != 3 {
		t.Fatalf("fresh line ranges = %d, want 3", len(freshRanges))
	}
	if first := freshRanges[transcriptLineKey{itemKey: m.chatItems[0].key}]; first != (lineRange{start: 0, end: 1}) {
		t.Fatalf("first card range = %+v, want {0 1}", first)
	}
	tall := freshRanges[transcriptLineKey{itemKey: m.chatItems[1].key}]
	if tall.end-tall.start+1 <= m.chatVP.Height() {
		t.Fatalf("expanded exchange range %+v is not taller than viewport", tall)
	}
	third := freshRanges[transcriptLineKey{itemKey: m.chatItems[2].key}]
	if third.start != tall.end+2 {
		t.Fatalf("inter-item spacing entered a line range: tall=%+v third=%+v", tall, third)
	}

	cached := m.itemTranscriptBody()
	if cached != fresh || !reflect.DeepEqual(m.chatItemLineRanges, freshRanges) {
		t.Fatalf("cached render changed output or ranges\nfresh=%+v\ncached=%+v", freshRanges, m.chatItemLineRanges)
	}
	for i := 0; i < 2; i++ {
		m, _ = m.updateTranscript(key("n"))
		assertChatCursorVisible(t, m)
	}
	if m.chatItemCursor != 2 {
		t.Fatalf("card navigation cursor = %d, want 2", m.chatItemCursor)
	}
}

func TestTranscriptCardCacheLifecycleBounded(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.transcriptInnerW = 64
	page := syntheticExchangePage(4, 4)
	m.setChatPage(page)
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) != 4 {
		t.Fatalf("initial cache = %d, want 4", len(m.chatItemRendered))
	}

	for i := 0; i < 20; i++ {
		m.chatItemCursor = i % len(m.chatItems)
		m.chatItemExpand[m.chatItems[i%len(m.chatItems)].key] = i%2 == 0
		_ = m.itemTranscriptBody()
		if len(m.chatItemRendered) > len(m.chatItems) {
			t.Fatalf("selection/expansion iteration %d grew cache to %d for %d items", i, len(m.chatItemRendered), len(m.chatItems))
		}
	}

	replacement := syntheticExchangePage(4, 0)
	replacement.Entries[0].Blocks[0].Tool.Title = "Changed synthetic header"
	replacement.Entries[0].Blocks[0].Tool.Kind = "custom"
	replacement.Entries[1].Blocks[0].Tool.Kind = "custom"
	replacement.Entries[1].Blocks[0].Tool.Status = "failed"
	m.setChatPage(replacement)
	failed := m.itemTranscriptBody()
	// FR-02.12/02.15: the failed exchange no longer contains " failed" prose;
	// state is carried by the error icon and the card border.
	if !strings.Contains(stripANSI(failed), shared.IconStatusError) || !strings.Contains(stripANSI(failed), "Changed synthetic header") {
		t.Fatalf("same-key failed/header replacement stayed stale:\n%s", stripANSI(failed))
	}
	if strings.Contains(stripANSI(failed), " failed") {
		t.Fatalf("state prose reappeared in header after replacement:\n%s", stripANSI(failed))
	}
	if len(m.chatItemRendered) != 4 {
		t.Fatalf("replacement cache = %d, want 4", len(m.chatItemRendered))
	}

	replacement.Entries[1].Blocks[0].Tool.Status = "completed"
	m.setChatPage(replacement)
	completed := m.itemTranscriptBody()
	if strings.Contains(stripANSI(completed), shared.IconStatusError) || completed == failed {
		t.Fatalf("same-key success replacement stayed stale:\n%s", stripANSI(completed))
	}

	for _, width := range []int{48, 80, 52, 64} {
		m.transcriptInnerW = width
		m.rebuildRenderer()
		if len(m.chatItemRendered) != 0 {
			t.Fatalf("resize to %d retained %d cache entries", width, len(m.chatItemRendered))
		}
		_ = m.itemTranscriptBody()
		if len(m.chatItemRendered) != len(m.chatItems) {
			t.Fatalf("resize to %d cached %d entries, want %d", width, len(m.chatItemRendered), len(m.chatItems))
		}
	}

	selected := m.chatItems[2].key
	m.chatItemCursor = 2
	m.setChatPage(replacement)
	if got := m.chatVisibleItems[m.chatItemCursor].key; got != selected {
		t.Fatalf("same-step refresh selected %+v, want %+v", got, selected)
	}

	m.chatStep = "b"
	m.reloadTranscript()
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("step change retained %d card entries", len(m.chatItemRendered))
	}
}

func TestTranscriptCardPageBoundsSearchExpansionAndClipboard(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunDir = "/synthetic/nonexistent/run"
	m.transcriptInnerW = 72
	m.setChatPage(syntheticExchangePage(150, 40)) // 300 transcript entries.
	if len(m.chatEntries) != chatWindowMax || len(m.chatItems) != 150 {
		t.Fatalf("bounded page = entries:%d items:%d, want %d/150", len(m.chatEntries), len(m.chatItems), chatWindowMax)
	}
	first := m.itemTranscriptBody()
	second := m.itemTranscriptBody()
	if first != second || len(m.chatItemRendered) != 150 {
		t.Fatalf("repeated render changed output or cache bound: cache=%d", len(m.chatItemRendered))
	}

	m.searchQuery = "synthetic-042"
	m.rebuildTranscriptItemState(transcriptItemKey{})
	if len(m.chatVisibleItems) != 1 {
		t.Fatalf("search membership = %d, want 1", len(m.chatVisibleItems))
	}
	payload := runItemLoader(t, m.chatVisibleItems[0], m.chatEntries)
	if payload.Err != nil || !strings.Contains(payload.Payload, "tool-042") || strings.ContainsAny(payload.Payload, "╭╮╰╯") {
		t.Fatalf("searched-item clipboard payload invalid: err=%v payload=%q", payload.Err, payload.Payload)
	}

	m.searchQuery = ""
	m.rebuildTranscriptItemState(transcriptItemKey{})
	m.chatItemExpandAll = true
	expanded := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(expanded, "lines hidden") {
		t.Fatalf("expanded 300-entry page did not retain detail bound notice")
	}
	if len(m.chatItemRendered) > len(m.chatItems) {
		t.Fatalf("expanded page cache = %d, want <= %d", len(m.chatItemRendered), len(m.chatItems))
	}
}

func BenchmarkTranscriptCardPage(b *testing.B) {
	m := New("benchmark")
	m.transcriptInnerW = 100
	m.setChatPage(syntheticExchangePage(150, 0)) // 300 transcript entries.
	_ = m.itemTranscriptBody()                   // Populate the card cache.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.itemTranscriptBody()
	}
}

func syntheticTranscriptCardVisualPage() transcript.Page {
	oldCode := "func synthetic() string { return \"old\" }"
	newCode := "func synthetic() string {\n\treturn \"new\"\n}"
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "ok", Kind: "read", Title: "Read synthetic config"}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "ok", Kind: "read", Status: "completed"}}}},
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "bad", Kind: "bash", Title: "Run synthetic failing check"}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "bad", Kind: "bash", Status: "failed", Content: []toolcall.Content{{Type: "text", Text: "synthetic failure: exit status 1"}}}}}},
		{Seq: 5, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "live", Kind: "grep", Title: "Search fabricated sources"}}}},
		{Seq: 6, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "incomplete", Kind: "read", Title: "Read a deliberately long synthetic path for narrow rendering"}}}},
		{Seq: 7, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{
			Type: transcript.BlockToolUse,
			Tool: &toolcall.Activity{ID: "edit", Kind: "edit", Title: "Edit synthetic.go"},
		}}},
		{Seq: 8, Role: transcript.RoleUser, Blocks: []transcript.Block{{
			Type: transcript.BlockToolResult,
			Tool: &toolcall.Activity{
				ID: "edit", Kind: "edit", Status: "completed",
				Content: []toolcall.Content{{Diff: &toolcall.Diff{
					Path: "synthetic/example.go", OldText: &oldCode, NewText: newCode,
				}}},
			},
		}}},
		// Slice-02 additions (task 3.1): broaden state × kind coverage so the
		// status-line header gallery shows the web signature glyph alongside
		// the read/edit signatures. Appended at the end so slice-01 tests keep
		// their existing chatItems[3] and chatItems[4] positions.
		{Seq: 9, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "web", Kind: "websearch", Title: "Search web for synthetic term", Input: []byte(`{"query":"synthetic release notes"}`)}}}},
		{Seq: 10, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "web", Kind: "websearch", Status: "completed"}}}},
	}
	return transcript.Page{Entries: entries}
}

type monitorVisualCapture struct {
	name          string
	width, height int
	selected      int
	gotoBottom    bool
}

func TestTranscriptCardVisualProof(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture deterministic Monitor frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	captures := []monitorVisualCapture{
		{name: "25-task-5-monitor-states", width: 100, height: 30, selected: 1},
		{name: "25-task-5-monitor-narrow", width: 58, height: 30, selected: 2},
		{name: "25-task-5-monitor-wide", width: 132, height: 32, selected: 4, gotoBottom: true},
	}
	writeMonitorVisualCaptures(t, dir, captures, "Task 05")
}

// TestStatusLineHeaderVisualProof captures deterministic Monitor frames
// that demonstrate slice-02's four-slot status-line header grammar
// (icon carries state, per-kind signature glyph on settled success,
// selection-only title emphasis, error hint routed to meta). It reuses
// the slice-01 fabricated fixture so slice-01 captures still render;
// output goes to the slice-02 proofs directory when JIG_UI_SNAPSHOT_DIR
// is set to that location.
func TestStatusLineHeaderVisualProof(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture slice-02 Monitor frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	captures := []monitorVisualCapture{
		{name: "25-task-3-monitor-headers", width: 100, height: 30, selected: 1},
		{name: "25-task-3-monitor-narrow", width: 58, height: 30, selected: 1},
		{name: "25-task-3-monitor-wide", width: 132, height: 32, selected: 4, gotoBottom: true},
	}
	writeMonitorVisualCaptures(t, dir, captures, "Task 03 (slice 02)")
}

func writeMonitorVisualCaptures(t *testing.T, dir string, captures []monitorVisualCapture, tag string) {
	t.Helper()
	var notes strings.Builder
	notes.WriteString(tag + " deterministic Monitor visual fixture\n")
	notes.WriteString("Fixture source: syntheticTranscriptCardVisualPage (shared between slice 01 and slice 02 proofs)\n")
	notes.WriteString("All paths, commands, status text, and code are fabricated.\n\n")
	notes.WriteString("Rendering: production Model.View ANSI -> deterministic test HTML -> local headless Chrome PNG.\n\n")
	for _, capture := range captures {
		m := newMonitorWithSteps(t)
		m, _ = m.Update(tea.WindowSizeMsg{Width: capture.width, Height: capture.height})
		m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{RunID: "run-1", StepID: "a", To: step.StatusRunning}})
		m.chatStep = "a"
		m.focus = focusTranscript
		m.setChatPage(syntheticTranscriptCardVisualPage())
		// The running step makes all use-only exchanges running; retain one
		// synthetic terminal-incomplete exchange to display the warning state.
		m.chatItems[3].displayState = toolDisplayUnknownUse
		m.rebuildTranscriptItemState(transcriptItemKey{})
		m.chatItemCursor = capture.selected
		m.refreshPanels()
		if capture.gotoBottom {
			m.chatVP.GotoBottom()
		}
		view := m.View()
		path := filepath.Join(dir, capture.name+".ansi")
		if err := os.WriteFile(path, []byte(view), 0o644); err != nil {
			t.Fatal(err)
		}
		htmlPath := filepath.Join(dir, capture.name+".html")
		if err := os.WriteFile(htmlPath, []byte(terminalHTML(view)), 0o644); err != nil {
			t.Fatal(err)
		}
		selected := m.chatVisibleItems[m.chatItemCursor]
		fmt.Fprintf(&notes, "%s.ansi\n  terminal=%dx%d transcriptInnerW=%d selectedIndex=%d selectedKey=%+v expandAll=%v editExpanded=%v viewportOffset=%d\n",
			capture.name, capture.width, capture.height, m.transcriptInnerW, capture.selected, selected.key,
			m.chatItemExpandAll, m.chatItemExpand[m.chatItems[4].key], m.chatVP.YOffset())
	}
	notes.WriteString("\nObserved semantic states: success uses the quiet dim border; failure uses danger; running uses primary; incomplete uses warning. Selection retains its outside rail and emphasized header without replacing those state borders. The wide frame shows structured-edit details below its header card.\n")
	notesName := "25-task-5-monitor-states-notes.txt"
	if strings.HasPrefix(tag, "Task 03") {
		notesName = "25-task-3-monitor-headers-notes.txt"
	}
	if err := os.WriteFile(filepath.Join(dir, notesName), []byte(notes.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

type terminalSGR struct {
	foreground string
	background string
	bold       bool
	italic     bool
}

func terminalHTML(input string) string {
	state := terminalSGR{foreground: "#C5C5D2", background: "#100F14"}
	var body strings.Builder
	body.WriteString(`<span style="` + terminalCSS(state) + `">`)
	for i := 0; i < len(input); {
		if input[i] == '\x1b' && i+2 < len(input) && input[i+1] == '[' {
			end := strings.IndexByte(input[i+2:], 'm')
			if end >= 0 {
				params := input[i+2 : i+2+end]
				state = applyTerminalSGR(state, params)
				body.WriteString(`</span><span style="` + terminalCSS(state) + `">`)
				i += end + 3
				continue
			}
		}
		next := strings.IndexByte(input[i:], '\x1b')
		if next < 0 {
			next = len(input) - i
		}
		body.WriteString(html.EscapeString(input[i : i+next]))
		i += next
	}
	body.WriteString(`</span>`)
	return `<!doctype html><html><head><meta charset="utf-8"><style>
html,body{margin:0;background:#100F14}body{padding:12px}pre{margin:0;color:#C5C5D2;background:#100F14;font:14px/1.25 "SFMono-Regular",Menlo,Consolas,monospace;white-space:pre}
</style></head><body><pre>` + body.String() + `</pre></body></html>`
}

func terminalCSS(state terminalSGR) string {
	weight := "normal"
	if state.bold {
		weight = "bold"
	}
	style := "normal"
	if state.italic {
		style = "italic"
	}
	return fmt.Sprintf("color:%s;background:%s;font-weight:%s;font-style:%s", state.foreground, state.background, weight, style)
}

func applyTerminalSGR(state terminalSGR, params string) terminalSGR {
	if params == "" {
		return terminalSGR{foreground: "#C5C5D2", background: "#100F14"}
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		code, _ := strconv.Atoi(fields[i])
		switch code {
		case 0:
			state = terminalSGR{foreground: "#C5C5D2", background: "#100F14"}
		case 1:
			state.bold = true
		case 3:
			state.italic = true
		case 22:
			state.bold = false
		case 23:
			state.italic = false
		case 39:
			state.foreground = "#C5C5D2"
		case 49:
			state.background = "#100F14"
		case 38, 48:
			if i+4 < len(fields) && fields[i+1] == "2" {
				r, _ := strconv.Atoi(fields[i+2])
				g, _ := strconv.Atoi(fields[i+3])
				b, _ := strconv.Atoi(fields[i+4])
				color := fmt.Sprintf("#%02X%02X%02X", r, g, b)
				if code == 38 {
					state.foreground = color
				} else {
					state.background = color
				}
				i += 4
			}
		}
	}
	return state
}
