package monitor

// Per-turn metadata row (omp-transcript-parity slice 11) unit tests.
// The behavioral tests that seat the row inside itemTranscriptBody live
// in monitor_transcript_metadata_view_test.go; the tables here pin the
// row's field composition and drop-on-empty rule.

import (
	"strings"
	"testing"
	"time"

	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

func timeAt(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tt
}

// entryAt is a small local helper: a transcript.Entry at the given
// coord and Ts with a single visible text block so the entry is
// enumerated by turnTimestamps.
func entryAt(seq int, ts string, coord toolCorrelationKey) transcript.Entry {
	return transcript.Entry{
		Seq:        seq,
		Ts:         ts,
		Generation: coord.generation,
		Iteration:  coord.iteration,
		Attempt:    coord.attempt,
		Role:       transcript.RoleAssistant,
		Blocks:     []transcript.Block{{Type: transcript.BlockText, Text: "metadata fixture"}},
	}
}

func TestFormatMetaTimeMatchesTranscriptFormat(t *testing.T) {
	// The pre-existing pattern at monitor_transcript.go:529-531 is
	// t.Local().Format("15:04:05"). formatMetaTime must produce the
	// same string for the same input so the two paths stay aligned.
	got := formatMetaTime(timeAt(t, "2026-09-14T10:30:45Z"))
	want := time.Date(2026, 9, 14, 10, 30, 45, 0, time.UTC).Local().Format("15:04:05")
	if got != want {
		t.Fatalf("formatMetaTime = %q, want %q", got, want)
	}
}

func TestFormatMetaDurationBucketing(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, ""},
		{"sub-500ms drops", 200 * time.Millisecond, ""},
		{"just under threshold drops", 499 * time.Millisecond, ""},
		{"threshold rounds to 1s", 500 * time.Millisecond, "1s"},
		{"whole seconds", 4 * time.Second, "4s"},
		{"round to nearest second", 4300 * time.Millisecond, "4s"},
		{"round up", 4700 * time.Millisecond, "5s"},
		{"minute", 90 * time.Second, "1m30s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatMetaDuration(tc.d); got != tc.want {
				t.Fatalf("formatMetaDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestRenderTurnMetadataRowInteriorAllFields(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	coord := toolCorrelationKey{iteration: 1, attempt: 2}
	m.chatEntries = []transcript.Entry{
		entryAt(1, "2026-09-14T10:00:00Z", coord),
		entryAt(2, "2026-09-14T10:00:04Z", coord),
	}
	got := m.renderTurnMetadataRow(coord, false, false)
	plain := stripANSI(got)
	// Time slot expected in local TZ, so recompute it the same way the
	// helper does rather than hard-coding an offset.
	wantTime := time.Date(2026, 9, 14, 10, 0, 4, 0, time.UTC).Local().Format("15:04:05")
	want := wantTime + "  Δ 4s  iter 1  attempt 2"
	if plain != want {
		t.Fatalf("interior row plain = %q, want %q", plain, want)
	}
}

func TestRenderTurnMetadataRowJoinDropsAbsentSlots(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	coord := toolCorrelationKey{}
	// One-entry turn: elapsed collapses to "" (Δ dropped), iter/attempt
	// are zero. Only the time slot survives; there must be no trailing
	// or leading whitespace.
	m.chatEntries = []transcript.Entry{entryAt(1, "2026-09-14T10:00:00Z", coord)}
	plain := stripANSI(m.renderTurnMetadataRow(coord, false, false))
	wantTime := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC).Local().Format("15:04:05")
	if plain != wantTime {
		t.Fatalf("single-field row = %q, want %q", plain, wantTime)
	}
	if strings.Contains(plain, "  ") {
		t.Fatalf("single-field row contains a double space: %q", plain)
	}
}

func TestRenderTurnMetadataRowAllFieldsAbsentReturnsEmpty(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	coord := toolCorrelationKey{}
	// Entries at the wrong coord — turnTimestamps returns ok=false —
	// and coord.iteration/attempt are both zero. The row must be "".
	m.chatEntries = []transcript.Entry{entryAt(1, "2026-09-14T10:00:00Z", toolCorrelationKey{iteration: 9})}
	if got := m.renderTurnMetadataRow(coord, false, false); got != "" {
		t.Fatalf("empty-fields row = %q, want \"\"", got)
	}
}

func TestRenderTurnMetadataRowStepEndAppendsCostAndTokens(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	coord := toolCorrelationKey{iteration: 2}
	m.chatEntries = []transcript.Entry{
		entryAt(1, "2026-09-14T10:00:00Z", coord),
		entryAt(2, "2026-09-14T10:00:12Z", coord),
	}
	stepIdx := m.index["a"]
	cost := 0.0412
	m.steps[stepIdx].cost = &cost
	m.steps[stepIdx].tokens = 4200
	m.steps[stepIdx].status = step.StatusSucceeded
	m.steps[stepIdx].end = time.Date(2026, 9, 14, 10, 0, 12, 0, time.UTC)

	plain := stripANSI(m.renderTurnMetadataRow(coord, true, true))
	// Time slot must derive from monitorStep.end (which for this fixture
	// happens to equal turnEnd), and the row must end with $cost + tokens.
	wantTime := m.steps[stepIdx].end.Local().Format("15:04:05")
	want := wantTime + "  Δ 12s  iter 2  $0.0412  4.2k tok"
	if plain != want {
		t.Fatalf("step-end row plain = %q, want %q", plain, want)
	}
}

func TestRenderTurnMetadataRowStepEndOmitsCostAndTokensWhenAbsent(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	coord := toolCorrelationKey{}
	m.chatEntries = []transcript.Entry{
		entryAt(1, "2026-09-14T10:00:00Z", coord),
		entryAt(2, "2026-09-14T10:00:03Z", coord),
	}
	stepIdx := m.index["a"]
	m.steps[stepIdx].status = step.StatusSucceeded
	m.steps[stepIdx].end = time.Date(2026, 9, 14, 10, 0, 3, 0, time.UTC)

	plain := stripANSI(m.renderTurnMetadataRow(coord, true, true))
	wantTime := m.steps[stepIdx].end.Local().Format("15:04:05")
	// Cost nil, tokens=0 → both slots drop; only the time and Δ remain.
	want := wantTime + "  Δ 3s"
	if plain != want {
		t.Fatalf("cost/tokens-absent row = %q, want %q", plain, want)
	}
}

func TestRenderTurnMetadataRowIsDimStyled(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	coord := toolCorrelationKey{}
	m.chatEntries = []transcript.Entry{
		entryAt(1, "2026-09-14T10:00:00Z", coord),
		entryAt(2, "2026-09-14T10:00:01Z", coord),
	}
	got := m.renderTurnMetadataRow(coord, false, false)
	if got == "" {
		t.Fatalf("expected a non-empty row for dim-style verification")
	}
	// Independently render an equivalent plain string through Chat.Hint
	// and check the two share the same ANSI wrapping. The row is one
	// call to shared.Theme.Chat.Hint.Render so the two strings must
	// agree byte-for-byte on the SGR envelope.
	plain := stripANSI(got)
	want := shared.Theme.Chat.Hint.Render(plain)
	if got != want {
		t.Fatalf("row not dim-styled through Chat.Hint\n got=%q\nwant=%q", got, want)
	}
}

func TestRenderTurnMetadataRowUsesTurnEndWhenStepEndAbsent(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.chatStep = "a"
	coord := toolCorrelationKey{}
	m.chatEntries = []transcript.Entry{
		entryAt(1, "2026-09-14T10:00:00Z", coord),
		entryAt(2, "2026-09-14T10:00:04Z", coord),
	}
	// includeStepTotals=true but stepEnded=false — the caller signals
	// "no terminal StepStatus yet". The time slot must still render
	// from turnEnd rather than dropping the row.
	plain := stripANSI(m.renderTurnMetadataRow(coord, true, false))
	wantTime := time.Date(2026, 9, 14, 10, 0, 4, 0, time.UTC).Local().Format("15:04:05")
	want := wantTime + "  Δ 4s"
	if plain != want {
		t.Fatalf("no-step-end row = %q, want %q", plain, want)
	}
}
