package monitor

// Slice-12 (centered boundary banners) visual proof. Emits three
// Monitor scenes to JIG_UI_SNAPSHOT_DIR:
//
//   1. iteration-bump-interior — the banner appears in the two-line
//      gap between iteration 0 and iteration 1 turns; the closing
//      turn's slice-11 metadata row sits immediately above it so the
//      full gap composition (blank / metadata / banner / blank) is
//      observable in one frame.
//   2. all-three-labels-in-order — a fixture stepping through
//      retry → iteration → reset in one page; every banner variant
//      and its arriving item appear in order.
//   3. page-edge-banner — a page whose first item's coord is non-zero
//      (iteration = 2), demonstrating FR-12.6 (banner announces the
//      arriving coord above the very first rendered item).
//
// The .ansi/.html captures are the deterministic ground truth. The
// companion -notes.txt file records banner label placement,
// chatItemLineRanges folds, and independent gap arithmetic so a
// reviewer can validate the plan's §Layout matrix without diffing raw
// bytes.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
)

func slice12PageAllCoords() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "First attempt: exploring the fix."}}},
		{Seq: 2, Ts: "2026-09-14T10:00:04Z", Generation: 0, Iteration: 0, Attempt: 1, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Retrying after the failed tool call."}}},
		{Seq: 3, Ts: "2026-09-14T10:00:11Z", Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "New iteration; step rewound."}}},
		{Seq: 4, Ts: "2026-09-14T10:00:22Z", Generation: 1, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Operator hit Reset; fresh generation."}}},
	}}
}

func slice12PageIterationBump() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Planning the synthetic edit."}}},
		{Seq: 2, Ts: "2026-09-14T10:00:04Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Draft complete; handing back."}}},
		{Seq: 3, Ts: "2026-09-14T10:00:07Z", Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Applying the change now."}}},
	}}
}

func slice12PagePageEdge() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 42, Ts: "2026-09-14T10:15:00Z", Generation: 0, Iteration: 2, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Page loaded mid-run — this item's coord is iteration=2."}}},
		{Seq: 43, Ts: "2026-09-14T10:15:04Z", Generation: 0, Iteration: 2, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Assistant response inside the same iteration."}}},
	}}
}

func TestBoundaryBannerVisualProof(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture slice-12 Monitor frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name         string
		page         transcript.Page
		wantLabels   []string
		wantAbsent   []string
		expectFolded string // label the folded range of chatItems[0] must contain
	}{
		{
			name:         "iteration-bump-interior",
			page:         slice12PageIterationBump(),
			wantLabels:   []string{"iteration 2", "Δ"},
			expectFolded: "iteration 2",
		},
		{
			name:       "all-three-labels-in-order",
			page:       slice12PageAllCoords(),
			wantLabels: []string{"retry 1", "iteration 2", "reset 2"},
		},
		{
			name:       "page-edge-banner",
			page:       slice12PagePageEdge(),
			wantLabels: []string{"iteration 3"},
			wantAbsent: []string{"iteration 2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
			m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
				RunID: "run-1", StepID: "a", To: step.StatusRunning,
			}})
			m.chatStep = "a"
			m.focus = focusTranscript
			m.setChatPage(tc.page)
			m.chatItemCursor = 0

			stepIdx := m.index["a"]
			m.steps[stepIdx].status = step.StatusRunning
			m.steps[stepIdx].end = time.Time{}

			m.refreshPanels()
			view := m.View()

			body := m.itemTranscriptBody()
			plain := stripANSI(body)
			rows := strings.Split(strings.TrimRight(body, "\n"), "\n")

			var notes strings.Builder
			fmt.Fprintf(&notes, "Slice-12 (boundary banner) visual scene: %s\n", tc.name)
			notes.WriteString("Fixture: fabricated content only; no real .jig/ data.\n")
			fmt.Fprintf(&notes, "terminal=80x30 transcriptInnerW=%d chatItems=%d visibleItems=%d\n",
				m.transcriptInnerW, len(m.chatItems), len(m.chatVisibleItems))

			visibleKeys := make([]transcriptItemKey, 0, len(m.chatVisibleItems))
			for _, item := range m.chatVisibleItems {
				if _, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}]; ok {
					visibleKeys = append(visibleKeys, item.key)
				}
			}
			notes.WriteString("Item ranges and inter-item gaps in the transcript body:\n")
			for i, key := range visibleKeys {
				rng := m.chatItemLineRanges[transcriptLineKey{itemKey: key}]
				var firstRow string
				if rng.start < len(rows) {
					firstRow = stripANSI(rows[rng.start])
					if len(firstRow) > 60 {
						firstRow = firstRow[:60] + "…"
					}
				}
				fmt.Fprintf(&notes, "  item[%d] range=%+v first=%q\n", i, rng, firstRow)
				if i > 0 {
					prev := m.chatItemLineRanges[transcriptLineKey{itemKey: visibleKeys[i-1]}]
					fmt.Fprintf(&notes, "    ^ %d blank line(s) since previous item's last row (post-fold)\n",
						rng.start-prev.end-1)
				}
			}
			notes.WriteString("\nBanner label placement in the plain body:\n")
			for _, label := range tc.wantLabels {
				idx := strings.Index(plain, label)
				if idx < 0 {
					fmt.Fprintf(&notes, "  MISSING %q\n", label)
				} else {
					fmt.Fprintf(&notes, "  found  %q at byte offset %d\n", label, idx)
				}
			}
			for _, label := range tc.wantAbsent {
				if strings.Contains(plain, label) {
					fmt.Fprintf(&notes, "  UNEXPECTEDLY PRESENT %q\n", label)
				} else {
					fmt.Fprintf(&notes, "  absent %q (as expected)\n", label)
				}
			}
			if tc.expectFolded != "" && len(visibleKeys) > 0 {
				rng := m.chatItemLineRanges[transcriptLineKey{itemKey: visibleKeys[0]}]
				rangeContent := ""
				if rng.start >= 0 && rng.end < len(rows) {
					rangeContent = stripANSI(strings.Join(rows[rng.start:rng.end+1], "\n"))
				}
				if strings.Contains(rangeContent, tc.expectFolded) {
					fmt.Fprintf(&notes, "\nFold check: chatItems[0].range contains %q (banner folded per plan §Layout matrix).\n",
						tc.expectFolded)
				} else {
					fmt.Fprintf(&notes, "\nFold check FAILED: chatItems[0].range does not contain %q.\n",
						tc.expectFolded)
				}
			}

			base := "slice-12-" + tc.name
			if err := os.WriteFile(filepath.Join(dir, base+".ansi"), []byte(view), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, base+".html"), []byte(terminalHTML(view)), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, base+"-notes.txt"), []byte(notes.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
