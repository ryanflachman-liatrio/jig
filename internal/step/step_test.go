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

// TestTokenCount verifies TokenCount sums all four token buckets and reports
// (0, false) when usage is absent, so the UI can distinguish "unknown" from 0.
func TestTokenCount(t *testing.T) {
	// Absent usage: not known.
	if n, ok := (&Result{}).TokenCount(); ok || n != 0 {
		t.Errorf("nil usage: got (%d, %v), want (0, false)", n, ok)
	}

	usage := map[string]any{
		"input_tokens":                float64(100),
		"output_tokens":               float64(20),
		"cache_creation_input_tokens": float64(5),
		"cache_read_input_tokens":     float64(1000),
	}
	r := &Result{Usage: &usage}
	n, ok := r.TokenCount()
	if !ok {
		t.Fatal("expected ok for populated usage")
	}
	if want := 100 + 20 + 5 + 1000; n != want {
		t.Errorf("TokenCount = %d, want %d", n, want)
	}

	// A partial map (only input) still totals what is present.
	partial := map[string]any{"input_tokens": float64(42)}
	if n, ok := (&Result{Usage: &partial}).TokenCount(); !ok || n != 42 {
		t.Errorf("partial usage: got (%d, %v), want (42, true)", n, ok)
	}
}
