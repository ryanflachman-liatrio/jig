package step

import "testing"

// TestStatusValues locks the string form of each Status. The strings are the
// journal/UI contract (state = fold(journal)), so a rename here is a breaking
// change and must be deliberate. StatusStopped is the spec-07 addition: a
// parked-but-alive status for a deliberately-stopped step.
func TestStatusValues(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusValidating, "validating"},
		{StatusAwaitingReview, "awaiting_review"},
		{StatusNeedsInput, "needs_input"},
		{StatusAwaitingRecovery, "awaiting_recovery"},
		{StatusAwaitingIntegration, "awaiting_integration"},
		{StatusStopped, "stopped"},
		{StatusSucceeded, "succeeded"},
		{StatusFailed, "failed"},
		{StatusSkipped, "skipped"},
	}
	for _, tc := range cases {
		if string(tc.status) != tc.want {
			t.Errorf("Status %v = %q, want %q", tc.status, string(tc.status), tc.want)
		}
	}
}

// TestStatusStoppedDistinct guards against StatusStopped colliding with any
// other status value — it must be its own parked-but-alive state, distinct from
// the terminal and other parked statuses.
func TestStatusStoppedDistinct(t *testing.T) {
	others := []Status{
		StatusPending, StatusRunning, StatusValidating, StatusAwaitingReview,
		StatusNeedsInput, StatusAwaitingRecovery, StatusAwaitingIntegration,
		StatusSucceeded, StatusFailed, StatusSkipped,
	}
	for _, o := range others {
		if StatusStopped == o {
			t.Errorf("StatusStopped collides with %q", o)
		}
	}
}
