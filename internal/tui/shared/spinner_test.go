package shared

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// TestSpinnerFrameSameTimestampSameFrame locks FR-13.1 (all concurrent
// indicators show the same frame). Two lookups at the same instant must
// return the same glyph so a panel with several animating sites does
// not visibly desync.
func TestSpinnerFrameSameTimestampSameFrame(t *testing.T) {
	now := time.Unix(1_700_000_000, 500*int64(time.Millisecond))
	a, ok := SpinnerFrame("status", now)
	if !ok {
		t.Fatal("SpinnerFrame(status): ok=false, want a registered set")
	}
	b, ok := SpinnerFrame("status", now)
	if !ok {
		t.Fatal("second lookup: ok=false")
	}
	if a != b {
		t.Fatalf("concurrent frames differ: %q vs %q", a, b)
	}
}

// TestSpinnerFrameAdvancesWithClock locks FR-13.2 (wall-time derivation).
// The frame index must rotate by one at each SpinnerAdvanceMS window
// boundary — an item entering the animation mid-revolution is expected
// to land at the same frame as an item already animating.
func TestSpinnerFrameAdvancesWithClock(t *testing.T) {
	// Choose an anchor aligned to the start of a window so we can
	// predict the frame index without chasing a boundary.
	base := time.UnixMilli((1_700_000_000_000 / SpinnerAdvanceMS) * SpinnerAdvanceMS)
	prev, _ := SpinnerFrame("status", base)
	next, _ := SpinnerFrame("status", base.Add(SpinnerAdvanceMS*time.Millisecond))
	after, _ := SpinnerFrame("status", base.Add(2*SpinnerAdvanceMS*time.Millisecond))
	if prev == next {
		t.Fatalf("frame did not advance across %dms: %q → %q", SpinnerAdvanceMS, prev, next)
	}
	if next == after {
		t.Fatalf("frame did not advance across a second %dms window: %q → %q", SpinnerAdvanceMS, next, after)
	}
	if prev == after {
		t.Fatalf("frames should differ by two positions in an 8-frame set: %q ≡ %q", prev, after)
	}
}

// TestSpinnerFrameStableWithinWindow verifies the quantization: a
// lookup near the start and near the end of the same SpinnerAdvanceMS
// window must return the same glyph so a repaint that overshoots the
// window boundary by a few ms does not show a glyph jump.
func TestSpinnerFrameStableWithinWindow(t *testing.T) {
	base := time.UnixMilli((1_700_000_000_000 / SpinnerAdvanceMS) * SpinnerAdvanceMS)
	start, _ := SpinnerFrame("status", base)
	mid, _ := SpinnerFrame("status", base.Add((SpinnerAdvanceMS/2)*time.Millisecond))
	last, _ := SpinnerFrame("status", base.Add((SpinnerAdvanceMS-1)*time.Millisecond))
	if start != mid || start != last {
		t.Fatalf("frames drifted inside one window: %q / %q / %q", start, mid, last)
	}
}

// TestSpinnerFrameCyclesThroughSet exercises the full revolution: at
// every window boundary in one full N-frame revolution, the frame
// index must equal the position in the registered set. Locks FR-13.2
// and the modulo arithmetic together.
func TestSpinnerFrameCyclesThroughSet(t *testing.T) {
	set := spinnerFrameSets["status"]
	want := set.Unicode
	base := time.UnixMilli((1_700_000_000_000 / SpinnerAdvanceMS) * SpinnerAdvanceMS)
	// After one full revolution we should be back at the starting frame.
	got := make([]string, 0, len(want)+1)
	for i := 0; i <= len(want); i++ {
		g, _ := SpinnerFrame("status", base.Add(time.Duration(i)*SpinnerAdvanceMS*time.Millisecond))
		got = append(got, g)
	}
	for i, g := range want {
		if got[i] != g {
			t.Fatalf("frame[%d] = %q, want %q", i, got[i], g)
		}
	}
	if got[len(want)] != want[0] {
		t.Fatalf("revolution wrap-around: got %q, want %q", got[len(want)], want[0])
	}
}

// TestSpinnerFrameEveryFrameSingleCell locks FR-13.3 (all frames occupy
// the same number of terminal cells). The consumer relies on this to
// avoid width shifts as frames advance; a two-cell frame smuggled into
// the set would silently jitter the trailing breadcrumb slot.
func TestSpinnerFrameEveryFrameSingleCell(t *testing.T) {
	for name, set := range spinnerFrameSets {
		for i, glyph := range set.Unicode {
			if w := lipgloss.Width(glyph); w != 1 {
				t.Errorf("set %q unicode frame %d (%q) width = %d, want 1", name, i, glyph, w)
			}
		}
	}
}

// TestSpinnerFrameASCIIFallbackSingleCell locks FR-13.6 (ASCII
// fallback maintains the width invariant). Slice 14 will flip the
// preset predicate; if a set author has smuggled a wide ASCII glyph
// into the fallback it must fail here rather than during the flip.
func TestSpinnerFrameASCIIFallbackSingleCell(t *testing.T) {
	for name, set := range spinnerFrameSets {
		for i, glyph := range set.ASCII {
			if w := lipgloss.Width(glyph); w != 1 {
				t.Errorf("set %q ascii frame %d (%q) width = %d, want 1", name, i, glyph, w)
			}
		}
	}
}

// TestSpinnerFrameFrameSetNonEmpty asserts each registered set has at
// least one Unicode frame and one ASCII fallback frame; a zero-length
// slice would collapse the modulo arithmetic to div-by-zero.
func TestSpinnerFrameFrameSetNonEmpty(t *testing.T) {
	for name, set := range spinnerFrameSets {
		if len(set.Unicode) == 0 {
			t.Errorf("set %q has no Unicode frames", name)
		}
		if len(set.ASCII) == 0 {
			t.Errorf("set %q has no ASCII fallback frames", name)
		}
	}
}

// TestSpinnerFrameUnknownSetReturnsNotOK proves the (glyph, false)
// contract for unknown names so callers can no-op without a bespoke
// check for every set name.
func TestSpinnerFrameUnknownSetReturnsNotOK(t *testing.T) {
	glyph, ok := SpinnerFrame("does-not-exist", time.Now())
	if ok {
		t.Fatalf("SpinnerFrame(unknown): ok=true, want false")
	}
	if glyph != "" {
		t.Fatalf("SpinnerFrame(unknown): glyph=%q, want empty", glyph)
	}
}

// TestSpinnerFrameNegativeTime documents the pre-1970 branch: a
// negative UnixMilli returns the first frame rather than propagating a
// negative modulo. Not a production concern, but the branch is a real
// code path so the test locks its behavior.
func TestSpinnerFrameNegativeTime(t *testing.T) {
	set := spinnerFrameSets["status"]
	glyph, ok := SpinnerFrame("status", time.Unix(-1, 0))
	if !ok {
		t.Fatal("SpinnerFrame at t=-1: ok=false")
	}
	if glyph != set.Unicode[0] {
		t.Fatalf("SpinnerFrame at t=-1: glyph=%q, want %q", glyph, set.Unicode[0])
	}
}

// TestSpinnerFrameWidth reports the widest active frame; slice-13 sets
// are single-cell by contract so the value is always 1.
func TestSpinnerFrameWidth(t *testing.T) {
	if got := SpinnerFrameWidth("status"); got != 1 {
		t.Errorf("SpinnerFrameWidth(status) = %d, want 1", got)
	}
	if got := SpinnerFrameWidth("does-not-exist"); got != 0 {
		t.Errorf("SpinnerFrameWidth(unknown) = %d, want 0", got)
	}
}

// TestRegisterSpinnerSetIsAvailableForLaterSlices exercises the
// registration hook slice 10 will use for the thinking pulse; a set
// registered from an external test must be visible to SpinnerFrame.
func TestRegisterSpinnerSetIsAvailableForLaterSlices(t *testing.T) {
	name := "slice-13-test-only"
	set := SpinnerFrames{Unicode: []string{"a", "b"}, ASCII: []string{"a", "b"}}
	RegisterSpinnerSet(name, set)
	t.Cleanup(func() { delete(spinnerFrameSets, name) })
	glyph, ok := SpinnerFrame(name, time.UnixMilli(0))
	if !ok || glyph != "a" {
		t.Fatalf("registered set lookup at t=0: (%q, %v), want (\"a\", true)", glyph, ok)
	}
	glyph, ok = SpinnerFrame(name, time.UnixMilli(SpinnerAdvanceMS))
	if !ok || glyph != "b" {
		t.Fatalf("registered set lookup at t=advance: (%q, %v), want (\"b\", true)", glyph, ok)
	}
}
