package monitor

// Slice-11 (per-turn metadata row) visual proof. Emits four Monitor
// scenes to JIG_UI_SNAPSHOT_DIR:
//
//   1. running-step-no-step-end  — interior boundary row appears in the
//      two-line gap between iteration 0 and iteration 1; the step-end
//      row is absent because monitorStep.end is zero.
//   2. settled-success-step-end  — the step has succeeded; the step-end
//      row appears below the last item with cost and tokens.
//   3. settled-failed-step-end   — the step has failed; the error banner
//      keeps its existing home at chatBody's top and the metadata row
//      still renders (Q-11.3 resolution).
//   4. no-timestamps-row-absent  — synthetic fixture with empty Ts and
//      iter=attempt=0; the row is omitted entirely and the layout
//      matches slice 04 byte-for-byte.
//
// The .ansi/.html captures are the deterministic ground truth. PNG
// conversion (if any) happens outside the test process.

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
	"jig/internal/toolcall"
	"jig/internal/transcript"
)

// twoTurnVisualPage is the timestamped multi-turn synthetic fixture the
// visual proof scenes render. Every content string is fabricated.
func twoTurnVisualPage() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Let me plan the synthetic edit."}}},
		{Seq: 2, Ts: "2026-09-14T10:00:02Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "read-target", Kind: "read", Title: "Read synthetic/target.go"}}}},
		{Seq: 3, Ts: "2026-09-14T10:00:04Z", Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "read-target", Kind: "read", Status: "completed"}}}},
		// Coordinate change: iteration bumps to 1. itemSpacingBefore
		// returns 2 for this transition and slice 11 emits the closing
		// turn 0 metadata row inside that gap.
		{Seq: 4, Ts: "2026-09-14T10:00:07Z", Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Now applying the change."}}},
		{Seq: 5, Ts: "2026-09-14T10:00:12Z", Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Edit landed; verifying."}}},
	}}
}

// noTsVisualPage is the "empty fields" fixture: entries carry no
// parseable Ts and coord.iteration is only used on the second turn, so
// the interior row keys on the previous turn (coord=zero, no fields).
func noTsVisualPage() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Generation: 0, Iteration: 0, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "left turn with no ts"}}},
		{Seq: 2, Generation: 0, Iteration: 1, Attempt: 0, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "right turn with no ts"}}},
	}}
}

func TestTurnMetadataRowVisualProof(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture slice-11 Monitor frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name           string
		page           transcript.Page
		status         step.Status
		cost           *float64
		tokens         int
		end            time.Time
		errText        string
		expectedRow    string // substring the notes should mention finding
		expectedAbsent string // substring the notes should mention NOT finding
	}{
		{
			name:        "running-step-no-step-end",
			page:        twoTurnVisualPage(),
			status:      step.StatusRunning,
			expectedRow: "Δ 4s",
		},
		{
			name:   "settled-success-step-end",
			page:   twoTurnVisualPage(),
			status: step.StatusSucceeded,
			cost:   func() *float64 { c := 0.0412; return &c }(),
			tokens: 4200,
			end:    time.Date(2026, 9, 14, 10, 0, 12, 0, time.UTC),
		},
		{
			name:    "settled-failed-step-end",
			page:    twoTurnVisualPage(),
			status:  step.StatusFailed,
			cost:    func() *float64 { c := 0.0189; return &c }(),
			tokens:  1800,
			end:     time.Date(2026, 9, 14, 10, 0, 12, 0, time.UTC),
			errText: "synthetic subprocess exited 1",
		},
		{
			name:           "no-timestamps-row-absent",
			page:           noTsVisualPage(),
			status:         step.StatusRunning,
			expectedAbsent: "Δ",
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
			m.steps[stepIdx].status = tc.status
			m.steps[stepIdx].cost = tc.cost
			m.steps[stepIdx].tokens = tc.tokens
			m.steps[stepIdx].end = tc.end
			m.steps[stepIdx].err = tc.errText

			m.refreshPanels()
			view := m.View()

			// Independently compute the item ranges and inter-item gaps so
			// a reviewer can see slice-11 row placement without diffing
			// the raw byte capture.
			body := m.itemTranscriptBody()
			rows := strings.Split(strings.TrimRight(body, "\n"), "\n")
			var notes strings.Builder
			fmt.Fprintf(&notes, "Slice-11 (per-turn metadata row) visual scene: %s\n", tc.name)
			notes.WriteString("Fixture: fabricated content only; no real .jig/ data.\n")
			fmt.Fprintf(&notes, "terminal=80x30 transcriptInnerW=%d chatItems=%d visibleItems=%d\n",
				m.transcriptInnerW, len(m.chatItems), len(m.chatVisibleItems))
			fmt.Fprintf(&notes, "monitorStep{status=%s cost=%v tokens=%d end.IsZero=%v err=%q}\n",
				m.steps[stepIdx].status, m.steps[stepIdx].cost, m.steps[stepIdx].tokens,
				m.steps[stepIdx].end.IsZero(), m.steps[stepIdx].err)

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
				fmt.Fprintf(&notes, "  item[%d] key=%+v range=%+v first=%q\n", i, key, rng, firstRow)
				if i > 0 {
					prev := m.chatItemLineRanges[transcriptLineKey{itemKey: visibleKeys[i-1]}]
					fmt.Fprintf(&notes, "    ^ %d blank line(s) since previous item's last row\n", rng.start-prev.end-1)
				}
			}
			if tc.expectedRow != "" {
				if strings.Contains(stripANSI(body), tc.expectedRow) {
					fmt.Fprintf(&notes, "\nSlice-11 assertion satisfied: metadata row containing %q present.\n", tc.expectedRow)
				} else {
					fmt.Fprintf(&notes, "\nSlice-11 assertion FAILED: expected substring %q not found in body.\n", tc.expectedRow)
				}
			}
			if tc.expectedAbsent != "" {
				if !strings.Contains(stripANSI(body), tc.expectedAbsent) {
					fmt.Fprintf(&notes, "\nSlice-11 assertion satisfied: %q absent (row dropped when all fields empty).\n", tc.expectedAbsent)
				} else {
					fmt.Fprintf(&notes, "\nSlice-11 assertion FAILED: expected substring %q was present.\n", tc.expectedAbsent)
				}
			}

			base := "slice-11-" + tc.name
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
