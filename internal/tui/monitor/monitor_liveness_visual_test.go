package monitor

// Slice-13 (phase-locked spinners) visual proof. Emits four Monitor
// scenes to JIG_UI_SNAPSHOT_DIR:
//
//   1. pulse-running-following  — a running step is being followed, so
//      the Transcript panel's `LIVE` crumb renders as `<frame> LIVE`.
//      The full 8-frame revolution is enumerated in the notes so a
//      reviewer can verify every frame is single-cell and each keeps
//      the trailing label byte-identical.
//   2. settled-static-live      — the run has succeeded; the crumb is
//      the static `LIVE` word (no glyph), byte-identical to main.
//   3. paused-no-live-crumb     — the operator has scrolled up
//      (chatAutoScroll=false); no `LIVE` crumb appears (`N new`
//      renders in its place if unseen entries exist).
//   4. idle-no-live-crumb       — no running step; the crumb is
//      absent entirely, matching main.
//
// The .ansi/.html captures are the deterministic ground truth for the
// panel chrome. The animated frame captured in scene (1) is the frame
// that shared.SpinnerFrame returns for the wall-clock at capture time;
// the notes file separately enumerates every frame in the revolution
// with its rendered width so the presentation is reviewable without
// waiting for a live monitor.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// slice13Page is the transcript fixture behind the visual scenes. Two
// short turns so the panel has content but the header — not the body —
// remains the visible signal.
func slice13Page() transcript.Page {
	return transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Ts: "2026-09-14T10:00:00Z", Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Kicking off the streaming turn."}}},
		{Seq: 2, Ts: "2026-09-14T10:00:03Z", Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Working on it now."}}},
	}}
}

func TestLiveCrumbVisualProof(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture slice-13 Monitor frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name           string
		status         step.Status
		autoScroll     bool
		wantSubstring  string
		absentIfStatic string
	}{
		{
			name:          "pulse-running-following",
			status:        step.StatusRunning,
			autoScroll:    true,
			wantSubstring: "LIVE",
		},
		{
			name:          "settled-static-live",
			status:        step.StatusSucceeded,
			autoScroll:    true,
			wantSubstring: "LIVE",
		},
		{
			name:           "paused-no-live-crumb",
			status:         step.StatusRunning,
			autoScroll:     false,
			absentIfStatic: "LIVE",
		},
		{
			name:           "idle-no-live-crumb",
			status:         step.StatusSucceeded,
			autoScroll:     false,
			absentIfStatic: "LIVE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
			m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
				RunID: "run-1", StepID: "a", To: tc.status,
			}})
			m.chatStep = "a"
			m.focus = focusTranscript
			m.setChatPage(slice13Page())
			m.chatAutoScroll = tc.autoScroll
			m.refreshPanels()

			view := m.View()
			plain := stripANSI(view)

			// Independently compute what the crumb resolves to for
			// this fixture so a reviewer does not need to eyeball the
			// header.
			crumbNow := m.liveCrumb(time.Now())
			shouldPulse := m.liveCrumbShouldPulse()

			var notes strings.Builder
			fmt.Fprintf(&notes, "Slice-13 (phase-locked spinner) visual scene: %s\n", tc.name)
			notes.WriteString("Fixture: fabricated content only; no real .jig/ data.\n")
			fmt.Fprintf(&notes, "terminal=80x30 focus=transcript status=%s chatAutoScroll=%v anyRunning=%v\n",
				tc.status, tc.autoScroll, m.anyRunning())
			fmt.Fprintf(&notes, "liveCrumbShouldPulse=%v liveCrumb(now)=%q\n", shouldPulse, crumbNow)
			fmt.Fprintf(&notes, "SpinnerAdvanceMS=%d\n\n", shared.SpinnerAdvanceMS)

			// Frame gallery: enumerate every frame in one full "status"
			// revolution so a reviewer can see all 8 glyphs without
			// waiting for wall-clock rotation. The base timestamp is
			// aligned to a window boundary so index i renders frame i.
			base := time.UnixMilli((1_700_000_000_000 / shared.SpinnerAdvanceMS) * shared.SpinnerAdvanceMS)
			frameCount := 8
			notes.WriteString("Full 'status' revolution (frame index → crumb → width):\n")
			for i := 0; i < frameCount; i++ {
				at := base.Add(time.Duration(i) * shared.SpinnerAdvanceMS * time.Millisecond)
				sample := m.liveCrumb(at)
				fmt.Fprintf(&notes, "  frame %d @ +%dms  %q (width=%d)\n",
					i, i*shared.SpinnerAdvanceMS, sample, lipgloss.Width(sample))
			}
			notes.WriteString("\n")

			if tc.wantSubstring != "" {
				if strings.Contains(plain, tc.wantSubstring) {
					fmt.Fprintf(&notes, "Slice-13 assertion satisfied: %q present in View().\n", tc.wantSubstring)
				} else {
					fmt.Fprintf(&notes, "Slice-13 assertion FAILED: %q missing from View().\n", tc.wantSubstring)
				}
			}
			if tc.absentIfStatic != "" {
				if !strings.Contains(plain, tc.absentIfStatic) {
					fmt.Fprintf(&notes, "Slice-13 assertion satisfied: %q absent from View() (as expected).\n", tc.absentIfStatic)
				} else {
					fmt.Fprintf(&notes, "Slice-13 assertion FAILED: %q unexpectedly present in View().\n", tc.absentIfStatic)
				}
			}

			base_ := "slice-13-" + tc.name
			if err := os.WriteFile(filepath.Join(dir, base_+".ansi"), []byte(view), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, base_+".html"), []byte(terminalHTML(view)), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, base_+"-notes.txt"), []byte(notes.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestLiveCrumbAnimationFrames captures one full 8-frame revolution of
// the panel-header pulse. Each frame is a full 80x30 Monitor View() at
// a real wall-clock moment shared.SpinnerFrame maps to that frame
// index: SpinnerAdvanceMS apart, aligned to a window boundary.
//
// The output is a small numbered gallery slice-13-pulse-frame-N.html
// that a downstream tool (ffmpeg, imagemagick, headless chrome + a
// stitcher) can convert into an animated GIF or MP4 for a PR
// walkthrough. The plain notes file records the frame glyph per file.
//
// The frames are captured with an actual wall-clock sleep between
// renders (SpinnerAdvanceMS = 100ms → ~800ms total for a full
// revolution) rather than injecting a clock; the animation surface is
// the panel chrome, and View() reads time.Now() at composition. Only
// runs under JIG_UI_SNAPSHOT_DIR so it never adds latency to a normal
// go test invocation.
func TestLiveCrumbAnimationFrames(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture slice-13 animation frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusRunning,
	}})
	m.chatStep = "a"
	m.focus = focusTranscript
	m.setChatPage(slice13Page())
	m.chatAutoScroll = true
	m.refreshPanels()

	// Sleep to the next window boundary so frame 0 starts at a
	// predictable index; then sample every SpinnerAdvanceMS ms.
	now := time.Now()
	boundary := now.Truncate(shared.SpinnerAdvanceMS * time.Millisecond).Add(shared.SpinnerAdvanceMS * time.Millisecond)
	time.Sleep(time.Until(boundary))

	frames := 8
	var index strings.Builder
	fmt.Fprintf(&index, "Slice-13 animation frames: one full revolution of the 'status' spinner set.\n\n")
	fmt.Fprintf(&index, "Each frame is a full 80x30 Monitor View() captured %dms apart.\n\n", shared.SpinnerAdvanceMS)
	for i := 0; i < frames; i++ {
		view := m.View()
		crumb := m.liveCrumb(time.Now())
		base := fmt.Sprintf("slice-13-pulse-frame-%d", i)
		if err := os.WriteFile(filepath.Join(dir, base+".ansi"), []byte(view), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, base+".html"), []byte(terminalHTML(view)), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&index, "  frame %d → %s  (%q)\n", i, base, crumb)
		if i < frames-1 {
			time.Sleep(shared.SpinnerAdvanceMS * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "slice-13-pulse-frames-index.txt"), []byte(index.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
