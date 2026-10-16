package step

import (
	"testing"
	"time"
)

func TestTimingLifecycle(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var timing Timing
	for _, tc := range []struct {
		status   Status
		at, want time.Duration
	}{
		{StatusRunning, 0, 0},
		{StatusRunning, time.Second, time.Second}, // duplicate does not restart
		{StatusNeedsInput, 2 * time.Second, 2 * time.Second},
		{StatusRunning, time.Hour, 2 * time.Second}, // excludes human wait
		{StatusValidating, time.Hour + time.Second, 3 * time.Second},
		{StatusSucceeded, time.Hour + 2*time.Second, 4 * time.Second},
		{StatusPending, 2 * time.Hour, 0}, // retry/reset clears old interval
		{StatusRunning, 3 * time.Hour, 0},
		{StatusStopped, 3*time.Hour + 250*time.Millisecond, 250 * time.Millisecond},
	} {
		at := base.Add(tc.at)
		timing.Apply(tc.status, at)
		if got := timing.Duration(at); got != tc.want {
			t.Fatalf("%s: duration = %v, want %v", tc.status, got, tc.want)
		}
		if timing.ActiveSince.IsZero() && timing.Duration(at.Add(time.Hour)) != tc.want {
			t.Fatalf("%s: parked/terminal clock kept advancing", tc.status)
		}
	}
}

func TestTimingUnknownAndBackwardClock(t *testing.T) {
	var timing Timing
	timing.Apply(StatusRunning, time.Time{})
	timing.Apply(StatusSucceeded, time.Time{})
	if timing != (Timing{}) {
		t.Fatalf("missing time invented a duration: %+v", timing)
	}
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	timing.Apply(StatusRunning, base)
	timing.Apply(StatusFailed, base.Add(-time.Second))
	if timing.Duration(base) != 0 {
		t.Fatal("backward clock produced negative duration")
	}
}
