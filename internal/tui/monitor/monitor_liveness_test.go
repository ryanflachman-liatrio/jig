package monitor

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/tui/shared"
)

// livePreconditionsModel returns a Model with the three non-clock
// preconditions satisfied: transcript content, follow available, and
// chatAutoScroll on. Tests toggle `anyRunning` separately by seating a
// step in the desired status. Persistence stays off — the panel-header
// consumer does not depend on RunDir.
func livePreconditionsModel(t *testing.T, status step.Status) Model {
	t.Helper()
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	m.chatAutoScroll = true
	// Seat a synthetic transcript entry so showsTranscriptFollow returns
	// true. The entry content is irrelevant — the pulse only consults
	// coordinate flags.
	m.chatEntries = nil
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: status,
	}})
	m.chatAutoScroll = true
	m.selKind = ""
	m.selFile = ""
	return m
}

// TestLiveCrumbPulsesWhileRunning locks the animated form: with all four
// preconditions true, the crumb becomes `<frame> LIVE`, and two lookups
// separated by one SpinnerAdvanceMS window return different glyphs but
// keep the trailing label byte-identical.
func TestLiveCrumbPulsesWhileRunning(t *testing.T) {
	m := livePreconditionsModel(t, step.StatusRunning)
	if !m.liveCrumbShouldPulse() {
		t.Fatalf("preconditions not met: content=%v follow=%v autoscroll=%v running=%v",
			m.selectedContent().kind, m.showsTranscriptFollow(), m.chatAutoScroll, m.anyRunning())
	}
	base := time.UnixMilli((1_700_000_000_000 / shared.SpinnerAdvanceMS) * shared.SpinnerAdvanceMS)
	a := m.liveCrumb(base)
	b := m.liveCrumb(base.Add(shared.SpinnerAdvanceMS * time.Millisecond))
	if !strings.HasSuffix(a, " LIVE") || !strings.HasSuffix(b, " LIVE") {
		t.Fatalf("crumb does not end in the LIVE label: %q / %q", a, b)
	}
	if a == b {
		t.Fatalf("crumb did not advance across a %dms window: both %q", shared.SpinnerAdvanceMS, a)
	}
	if a == "LIVE" || b == "LIVE" {
		t.Fatalf("crumb is static under running preconditions: %q / %q", a, b)
	}
}

// TestLiveCrumbStaticWhenNoRunning locks FR-13.4: the crumb collapses to
// the static label when no step is running, so the panel header is
// byte-identical to `main` on any run without live activity.
func TestLiveCrumbStaticWhenNoRunning(t *testing.T) {
	m := livePreconditionsModel(t, step.StatusSucceeded)
	if m.anyRunning() {
		t.Fatalf("fixture has a running step: %+v", m.steps)
	}
	if got := m.liveCrumb(time.UnixMilli(0)); got != "LIVE" {
		t.Fatalf("liveCrumb without a running step = %q, want %q", got, "LIVE")
	}
}

// TestLiveCrumbStaticWhenNotFollowing covers the paused / scrolled-up
// path: the operator has moved off the tail (chatAutoScroll == false),
// so the header should not animate. The `LIVE` word is not appended by
// the caller in this branch on main; the helper's own return therefore
// is only reached via chatAutoScroll==true, but liveCrumbShouldPulse
// still needs to reject on chatAutoScroll==false so a future call site
// that reused the helper would degrade cleanly.
func TestLiveCrumbStaticWhenNotFollowing(t *testing.T) {
	m := livePreconditionsModel(t, step.StatusRunning)
	m.chatAutoScroll = false
	if m.liveCrumbShouldPulse() {
		t.Fatal("liveCrumbShouldPulse returned true with chatAutoScroll=false")
	}
	if got := m.liveCrumb(time.UnixMilli(0)); got != "LIVE" {
		t.Fatalf("liveCrumb with chatAutoScroll=false = %q, want %q", got, "LIVE")
	}
}

// TestLiveCrumbAbsentOnFileContent proves the content-kind guard: when
// the panel is showing a file, showsTranscriptFollow becomes false and
// the pulse must not render even though a step is running.
func TestLiveCrumbAbsentOnFileContent(t *testing.T) {
	m := livePreconditionsModel(t, step.StatusRunning)
	m.selKind = "file"
	m.selFile = "/nonexistent/output.log"
	if m.liveCrumbShouldPulse() {
		t.Fatalf("liveCrumbShouldPulse returned true with selKind=file (content=%v follow=%v)",
			m.selectedContent().kind, m.showsTranscriptFollow())
	}
	if got := m.liveCrumb(time.UnixMilli(0)); got != "LIVE" {
		t.Fatalf("liveCrumb on file content = %q, want %q", got, "LIVE")
	}
}

// TestLiveCrumbConstantWidthAcrossOneRevolution locks FR-13.8: the
// rendered cell width is identical for every frame in one 800 ms
// revolution, so the trailing breadcrumb slot never jitters.
func TestLiveCrumbConstantWidthAcrossOneRevolution(t *testing.T) {
	m := livePreconditionsModel(t, step.StatusRunning)
	base := time.UnixMilli((1_700_000_000_000 / shared.SpinnerAdvanceMS) * shared.SpinnerAdvanceMS)
	frameCount := 8 // "status" set is 8 frames
	widths := make(map[int]int)
	for i := 0; i < frameCount; i++ {
		crumb := m.liveCrumb(base.Add(time.Duration(i) * shared.SpinnerAdvanceMS * time.Millisecond))
		widths[lipgloss.Width(crumb)]++
	}
	if len(widths) != 1 {
		t.Fatalf("width jittered across revolution: %v", widths)
	}
}

// TestLiveCrumbAddsNoTickers proves FR-13.5 and preserves the ticking
// invariant: replacing the static "LIVE" with a pulse must not schedule
// a `TickMsg` on its own; the only ticks that arrive are those the
// existing frame loop already publishes for anyRunning().
//
// The proof: with no running step, liveCrumb returns "LIVE" and the
// monitor never enters the frame loop. With a running step, the loop
// re-arms exactly as it did on main — one TickMsg per Update(TickMsg),
// no additional ticks from the pulse consumer.
func TestLiveCrumbAddsNoTickers(t *testing.T) {
	// With no running step, the monitor is idle and the pulse must not
	// wake it.
	m := drainFrames(t, newMonitorWithSteps(t))
	if m.ticking {
		t.Fatal("fixture did not settle before the pulse test")
	}
	// A View() call on an idle monitor exercises the pulse consumer's
	// codepath. It must not arm a frame.
	_ = m.View()
	if m.ticking {
		t.Fatal("liveCrumb armed a frame on an idle monitor")
	}
	// With a running step, ticking is armed by the StepStatus event
	// (existing behavior), and the TickMsg handler re-arms exactly once
	// per Update — the pulse consumer, which only reads a frame during
	// View, adds no additional TickMsg.
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{
		RunID: "run-1", StepID: "a", To: step.StatusRunning,
	}})
	if !m.ticking {
		t.Fatal("running step failed to arm the frame loop")
	}
	// Ten View() calls in a row from the same running-model must not
	// mutate ticking (View is pure by contract), and none must schedule
	// a `TickMsg`. The `ticking` bool being unchanged is the invariant.
	before := m.ticking
	for i := 0; i < 10; i++ {
		_ = m.View()
	}
	if m.ticking != before {
		t.Fatal("liveCrumb mutated ticking through View")
	}
}

// TestLiveCrumbPreservesLIVELabel is the regression that catches an
// ASCII preset swap or a set author error dropping the trailing label.
// Every test that greps for "LIVE" in monitor View() output must keep
// finding it, animated or not.
func TestLiveCrumbPreservesLIVELabel(t *testing.T) {
	m := livePreconditionsModel(t, step.StatusRunning)
	// Sample 16 frames across two revolutions to catch a set-cycling
	// bug that swaps or drops the label on a specific index.
	base := time.UnixMilli((1_700_000_000_000 / shared.SpinnerAdvanceMS) * shared.SpinnerAdvanceMS)
	for i := 0; i < 16; i++ {
		crumb := m.liveCrumb(base.Add(time.Duration(i) * shared.SpinnerAdvanceMS * time.Millisecond))
		if !strings.Contains(crumb, "LIVE") {
			t.Fatalf("frame %d dropped the LIVE label: %q", i, crumb)
		}
	}
}
