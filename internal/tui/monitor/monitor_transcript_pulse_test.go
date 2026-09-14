package monitor

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// FR-10.5: every frame in both the default and ASCII-fallback pulse glyph
// sets occupies exactly one visible cell, so neither the label nor anything
// after it shifts as the pulse animates.
func TestPulseFramesAreSingleCell(t *testing.T) {
	for _, set := range []struct {
		name   string
		frames []string
	}{
		{"default", shared.PulseFrames},
		{"ascii", shared.PulseFramesASCII},
	} {
		t.Run(set.name, func(t *testing.T) {
			if len(set.frames) == 0 {
				t.Fatalf("%s glyph set is empty", set.name)
			}
			for i, frame := range set.frames {
				if w := lipgloss.Width(frame); w != 1 {
					t.Fatalf("%s frame %d (%q) width = %d, want 1", set.name, i, frame, w)
				}
			}
		})
	}
}

// FR-10.8: the ASCII-fallback set produces a non-empty, single-cell frame
// for every index (covered above); this asserts non-emptiness directly.
func TestPulseFramesASCIINonEmpty(t *testing.T) {
	for i, frame := range shared.PulseFramesASCII {
		if frame == "" {
			t.Fatalf("ascii pulse frame %d is empty", i)
		}
	}
}

// Quantization: the selected frame is a pure, deterministic function of the
// TickMsg timestamp — no package-level counter or second ticker. Two calls
// with the same timestamp must always agree, and timestamps landing in the
// same 100ms bucket must select the same frame.
func TestPulseFrameIsPureFunctionOfTimestamp(t *testing.T) {
	frames := []string{"a", "b", "c", "d"}
	base := time.UnixMilli(1_700_000_000_000)

	first := pulseFrame(base, frames)
	second := pulseFrame(base, frames)
	if first != second {
		t.Fatalf("pulseFrame is not deterministic: %q != %q", first, second)
	}

	withinBucket := base.Add(42 * time.Millisecond)
	if got := pulseFrame(withinBucket, frames); got != first {
		t.Fatalf("pulseFrame changed within the same 100ms bucket: %q != %q", got, first)
	}

	nextBucket := base.Add(monitorFrameInterval)
	if got := pulseFrame(nextBucket, frames); got == first {
		// Not guaranteed to differ for every offset (frame count could wrap
		// back to the same index), but with 4 frames and one interval step
		// the index must advance by exactly one.
		t.Fatalf("pulseFrame did not advance one 100ms bucket later: still %q", got)
	}
}

func TestPulseFrameEmptySetReturnsEmpty(t *testing.T) {
	if got := pulseFrame(time.Now(), nil); got != "" {
		t.Fatalf("pulseFrame(nil) = %q, want empty", got)
	}
}
