package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/step"
	"jig/internal/toolcall"
	"jig/internal/transcript"
)

// runningExchange builds a fabricated tool-use whose result has not
// arrived; jig maps the missing result to toolDisplayRunning. The Input is
// large so writeItemDetail's row bound triggers, letting the anchor
// selection be observed at the rendered body.
func runningExchange(id string, rows int) []transcript.Entry {
	body := makeLogBody(rows)
	return []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{
			Type: transcript.BlockToolUse,
			Tool: &toolcall.Activity{
				ID:    id,
				Kind:  "read",
				Title: "Read streaming log",
				Input: []byte(body),
			},
		}},
	}}
}

// settledExchange mirrors runningExchange but attaches a completed result,
// yielding toolDisplaySuccess. The result's Output carries the large body
// so writeItemDetail's row bound triggers there.
func settledExchange(id string, rows int) []transcript.Entry {
	body := makeLogBody(rows)
	return []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{
			Type: transcript.BlockToolUse,
			Tool: &toolcall.Activity{ID: id, Kind: "read", Title: "Read streaming log"},
		}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{
			Type: transcript.BlockToolResult,
			Tool: &toolcall.Activity{
				ID:     id,
				Kind:   "read",
				Status: "completed",
				Output: []byte(body),
			},
		}}},
	}
}

// TestAnchorForStateMapping locks FR-06.11: only toolDisplayRunning
// tail-anchors; every other state uses head-anchor semantics.
func TestAnchorForStateMapping(t *testing.T) {
	tests := []struct {
		name  string
		state toolDisplayState
		want  detailAnchor
	}{
		{"running -> tail", toolDisplayRunning, detailAnchorTail},
		{"success -> head", toolDisplaySuccess, detailAnchorHead},
		{"error -> head", toolDisplayError, detailAnchorHead},
		{"unknown-use -> head", toolDisplayUnknownUse, detailAnchorHead},
		{"unknown-result -> head", toolDisplayUnknownResult, detailAnchorHead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := anchorForState(tt.state); got != tt.want {
				t.Fatalf("anchorForState(%v) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// TestWriteItemDetailRunningExchangeTailAnchors locks FR-06.11 and
// FR-06.12 together: a running tool exchange renders its bounded body
// tail-anchored, prepends the "… N earlier lines [enter: Expand]" marker
// above the visible rows, and keeps the newest rows (the distinctive last
// row) instead of the beginning.
func TestWriteItemDetailRunningExchangeTailAnchors(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.steps[m.index["a"]].status = step.StatusRunning
	m.chatStep = "a"
	m.chatItemExpandAll = true
	m.setChatPage(transcript.Page{Entries: runningExchange("run", 20)})

	if len(m.chatItems) != 1 || m.chatItems[0].displayState != toolDisplayRunning {
		t.Fatalf("expected running item, got %+v", m.chatItems)
	}
	plain := stripANSI(m.itemTranscriptBody())

	// 20 rows total, 12 kept -> 8 hidden. The tail anchor prepends the
	// marker and keeps rows 9..20; row-01 must be dropped, row-20 must be
	// present.
	if !strings.Contains(plain, "… 8 earlier lines [enter: Expand]") {
		t.Fatalf("running exchange missing prepended EarlierItems marker:\n%s", plain)
	}
	if !strings.Contains(plain, "row-20") {
		t.Fatalf("running exchange lost distinctive last row:\n%s", plain)
	}
	if strings.Contains(plain, "row-01") {
		t.Fatalf("running exchange kept distinctive first row (should be dropped):\n%s", plain)
	}
	// The marker must be prepended (appears before the first kept row on a
	// preceding line), not appended.
	markerIdx := strings.Index(plain, "… 8 earlier lines")
	firstRowIdx := strings.Index(plain, "row-09")
	if markerIdx < 0 || firstRowIdx < 0 || markerIdx > firstRowIdx {
		t.Fatalf("earlier-lines marker not positioned before the first kept row:\n%s", plain)
	}
}

// TestWriteItemDetailSettledExchangeHeadAnchors locks FR-06.11 and
// FR-06.12 for the head case: a settled success exchange renders
// head+tail, appends the "… N more lines" marker below, and keeps both the
// distinctive first row (via head) and the distinctive last row (via the
// 3-tail rows) while dropping middle rows.
func TestWriteItemDetailSettledExchangeHeadAnchors(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.chatItemExpandAll = true
	m.setChatPage(transcript.Page{Entries: settledExchange("settled", 20)})

	if len(m.chatItems) != 1 || m.chatItems[0].displayState != toolDisplaySuccess {
		t.Fatalf("expected settled item, got %+v", m.chatItems)
	}
	plain := stripANSI(m.itemTranscriptBody())

	if !strings.Contains(plain, "… 8 more lines [enter: Expand]") {
		t.Fatalf("settled exchange missing appended MoreItems marker:\n%s", plain)
	}
	if !strings.Contains(plain, "row-01") {
		t.Fatalf("settled exchange lost distinctive first row (head kept):\n%s", plain)
	}
	if !strings.Contains(plain, "row-20") {
		t.Fatalf("settled exchange lost distinctive last row (tail kept):\n%s", plain)
	}
	// The marker must be appended (appears after the last kept row).
	markerIdx := strings.Index(plain, "… 8 more lines")
	lastRowIdx := strings.LastIndex(plain, "row-20")
	if markerIdx < 0 || lastRowIdx < 0 || markerIdx < lastRowIdx {
		t.Fatalf("more-lines marker not positioned after the last kept row:\n%s", plain)
	}
	// Head anchor drops middle rows: row-10 (index 9) is in the elided
	// middle for a 20-row body with head 9 + tail 3.
	if strings.Contains(plain, "row-10") {
		t.Fatalf("head anchor kept a middle row (row-10):\n%s", plain)
	}
}

// TestWriteStreamingOutputEarlierLinesIndicator locks FR-06.13: the
// Steps-panel live tail shows a prepended "… N earlier lines" marker for
// dropped rows and keeps only the newest outputMaxLines rows.
func TestWriteStreamingOutputEarlierLinesIndicator(t *testing.T) {
	m := newMonitorWithSteps(t)
	stepID := "a"
	idx, ok := m.index[stepID]
	if !ok {
		t.Fatalf("step %q not found", stepID)
	}
	m.steps[idx].status = step.StatusRunning

	total := outputMaxLines + 5
	var buf strings.Builder
	for i := 0; i < total; i++ {
		fmt.Fprintf(&buf, "line-%02d\n", i+1)
	}
	m.stepOutput[stepID] = &buf

	body := stripANSI(m.body())

	want := fmt.Sprintf("… %d earlier lines", total-outputMaxLines)
	if !strings.Contains(body, want) {
		t.Fatalf("streaming drop indicator %q missing from Steps panel:\n%s", want, body)
	}
	// The marker must sit between the row header ("▸ a") and the first
	// tail row so a reviewer sees drop, then rows.
	headerIdx := strings.Index(body, "▸ "+stepID)
	markerIdx := strings.Index(body, want)
	firstTailIdx := strings.Index(body, fmt.Sprintf("line-%02d", total-outputMaxLines+1))
	if headerIdx < 0 || markerIdx < 0 || firstTailIdx < 0 {
		t.Fatalf("row-header/marker/first-tail-row missing from Steps panel:\n%s", body)
	}
	if !(headerIdx < markerIdx && markerIdx < firstTailIdx) {
		t.Fatalf("expected order header < marker < first tail row, got %d/%d/%d:\n%s", headerIdx, markerIdx, firstTailIdx, body)
	}
	// Dropped rows must not appear.
	for i := 1; i <= total-outputMaxLines; i++ {
		row := fmt.Sprintf("line-%02d", i)
		if strings.Contains(body, row) {
			t.Fatalf("dropped row %q still visible:\n%s", row, body)
		}
	}
	// Newest rows must appear.
	for i := total - outputMaxLines + 1; i <= total; i++ {
		row := fmt.Sprintf("line-%02d", i)
		if !strings.Contains(body, row) {
			t.Fatalf("kept row %q missing:\n%s", row, body)
		}
	}
}

// TestWriteStreamingOutputNoIndicatorWhenUnderCap ensures the drop
// indicator only appears when rows were actually dropped, so a small
// running step does not gain an "… 0 earlier lines" noise line.
func TestWriteStreamingOutputNoIndicatorWhenUnderCap(t *testing.T) {
	m := newMonitorWithSteps(t)
	stepID := "a"
	idx, ok := m.index[stepID]
	if !ok {
		t.Fatalf("step %q not found", stepID)
	}
	m.steps[idx].status = step.StatusRunning

	var buf strings.Builder
	for i := 0; i < outputMaxLines-2; i++ {
		fmt.Fprintf(&buf, "line-%02d\n", i+1)
	}
	m.stepOutput[stepID] = &buf

	body := stripANSI(m.body())
	if strings.Contains(body, "earlier lines") {
		t.Fatalf("drop indicator rendered even though nothing was dropped:\n%s", body)
	}
}

// TestChatItemLineRangesStableAcrossAnchorMode locks FR-06.14: whichever
// anchor is in play, chatItemLineRanges must cover the item's actual
// rendered rows so n/N navigation lands correctly. Head+3-tail (settled)
// and tail-only (running) can produce different row totals; the accounting
// must match either way.
func TestChatItemLineRangesStableAcrossAnchorMode(t *testing.T) {
	tests := []struct {
		name    string
		running bool
		entries func(string, int) []transcript.Entry
	}{
		{"settled head anchor", false, settledExchange},
		{"running tail anchor", true, runningExchange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m.transcriptInnerW = 80
			if tt.running {
				m.steps[m.index["a"]].status = step.StatusRunning
			}
			m.chatStep = "a"
			m.chatItemExpandAll = true
			m.setChatPage(transcript.Page{Entries: tt.entries("range", 20)})

			body := m.itemTranscriptBody()
			if len(m.chatVisibleItems) != 1 {
				t.Fatalf("visible items = %d, want 1", len(m.chatVisibleItems))
			}
			key := m.chatVisibleItems[0].key
			rng, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: key}]
			if !ok {
				t.Fatalf("no line range recorded for item")
			}
			total := strings.Count(strings.TrimSuffix(body, "\n"), "\n") + 1
			// The range's end index is inclusive of the last rendered
			// row; start is zero-based. Their span must equal the total
			// rendered rows for this item since it is the only item on
			// the page.
			if got := rng.end - rng.start + 1; got != total {
				t.Fatalf("line range covered %d rows, want %d (body has %d rows):\n%s", got, total, total, stripANSI(body))
			}
		})
	}
}

// TestTruncationAnchorGallery captures the three anchor scenes in one
// deterministic file: a settled head-anchored exchange, a running tail-
// anchored exchange, and a Steps-panel streaming buffer with the drop
// indicator. Opt-in via JIG_UI_SNAPSHOT_DIR.
func TestTruncationAnchorGallery(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the truncation anchor gallery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	settledPlain := func() string {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = 80
		m.chatItemExpandAll = true
		m.setChatPage(transcript.Page{Entries: settledExchange("gallery-settled", 20)})
		return stripANSI(m.itemTranscriptBody())
	}()

	runningPlain := func() string {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = 80
		m.steps[m.index["a"]].status = step.StatusRunning
		m.chatStep = "a"
		m.chatItemExpandAll = true
		m.setChatPage(transcript.Page{Entries: runningExchange("gallery-running", 20)})
		return stripANSI(m.itemTranscriptBody())
	}()

	stepsPlain := func() string {
		m := newMonitorWithSteps(t)
		stepID := "a"
		idx, ok := m.index[stepID]
		if !ok {
			t.Fatalf("step %q not found", stepID)
		}
		m.steps[idx].status = step.StatusRunning
		var buf strings.Builder
		total := outputMaxLines + 5
		for i := 0; i < total; i++ {
			fmt.Fprintf(&buf, "streaming line %02d\n", i+1)
		}
		m.stepOutput[stepID] = &buf
		return stripANSI(m.body())
	}()

	var b strings.Builder
	fmt.Fprintln(&b, "# Slice 06 anchor gallery")
	fmt.Fprintln(&b, "#")
	fmt.Fprintln(&b, "# transcriptInnerW=80 for the Transcript panels; the Steps panel uses")
	fmt.Fprintln(&b, "# its natural width. All fixtures fabricated; no real .jig/ data used.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "== (a) Settled tool exchange (toolDisplaySuccess) -> head anchor + appended \"more lines\" ==")
	fmt.Fprintln(&b, settledPlain)
	fmt.Fprintln(&b, "== (b) Running tool exchange (toolDisplayRunning) -> tail anchor + prepended \"earlier lines\" ==")
	fmt.Fprintln(&b, runningPlain)
	fmt.Fprintln(&b, "== (c) Steps-panel live streaming output (outputMaxLines drop) ==")
	fmt.Fprintln(&b, stepsPlain)

	out := filepath.Join(dir, "25-task-3-anchor-gallery.txt")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write gallery: %v", err)
	}

	notes := "Generated by TestTruncationAnchorGallery in internal/tui/monitor/monitor_truncation_anchor_test.go.\n" +
		"Command: JIG_UI_SNAPSHOT_DIR=<dir> go test ./internal/tui/monitor -run TestTruncationAnchorGallery -count=1\n" +
		"Scenes: (a) toolDisplaySuccess (head+tail with appended MoreItems marker),\n" +
		"        (b) toolDisplayRunning (tail-only with prepended EarlierItems marker),\n" +
		"        (c) Steps-panel writeStreamingOutput dropping leading rows (EarlierItems w/o expand hint).\n" +
		"Style token for (c): shared.Theme.Chat.Hint (resolves Q-06.3 in favor of Chat.Hint rather than Question).\n"
	if err := os.WriteFile(filepath.Join(dir, "25-task-3-anchor-gallery.notes.txt"), []byte(notes), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
}
