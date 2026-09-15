package monitor

import (
	"fmt"
	"strings"
	"time"

	"jig/internal/tui/shared"
)

// Per-turn metadata row (omp-transcript-parity slice 11).
//
// The transcript panel emits one dim, two-space-joined row of reference
// material at each completed turn boundary and one at the settled-step
// boundary. Every field is conditional on data availability (see the
// audit in docs/plans/omp-slice-11-turn-metadata-row.md); an empty
// field-set yields "" and the caller skips the row entirely.
//
// The row is styled with shared.Theme.Chat.Hint. It carries no icon
// (matches omp's usage-row register change from the universal `" · "`
// separator to a plain two-space join). It never wears the cursor bar
// and never appears in chatVisibleItems: itemTranscriptBody folds its
// rendered rows into the previous item's chatItemLineRanges entry so
// n/N block navigation still lands on items.

// renderTurnMetadataRow returns the dim metadata row for one turn, or
// "" when no field is derivable. coord identifies the turn whose
// entries in m.chatEntries carry the timestamps to fold. When
// includeStepTotals is true the row also carries the settled step's
// cumulative cost and tokens; interior turn rows pass false because
// StepStatus.Cost/Tokens accrue across attempts, not per-turn.
//
// stepEnded selects between the step-end wall time (monitorStep.end)
// and the turn's own last transcript timestamp for the row's leading
// time slot. Callers wire it from monitorStep.end.IsZero() == false.
func (m *Model) renderTurnMetadataRow(coord toolCorrelationKey, includeStepTotals, stepEnded bool) string {
	turnStart, turnEnd, tsOK := turnTimestamps(m.chatEntries, coord)

	var fields []string

	// Time-of-day slot. For interior turns this is the last entry's
	// local time; for the step-end row it is monitorStep.end (which
	// matches the moment the terminal StepStatus arrived, i.e. what
	// the operator actually saw). We fall through to turnEnd when
	// step-end is unavailable so a live-follow of a running step
	// still carries a per-turn timestamp.
	if includeStepTotals && stepEnded {
		if i, ok := m.index[m.chatStep]; ok {
			if end := m.steps[i].end; !end.IsZero() {
				fields = append(fields, formatMetaTime(end))
			}
		}
	}
	if len(fields) == 0 && tsOK {
		fields = append(fields, formatMetaTime(turnEnd))
	}

	if tsOK {
		if d := formatMetaDuration(turnEnd.Sub(turnStart)); d != "" {
			fields = append(fields, "Δ "+d)
		}
	}

	if coord.iteration > 0 {
		fields = append(fields, fmt.Sprintf("iter %d", coord.iteration))
	}
	if coord.attempt > 0 {
		fields = append(fields, fmt.Sprintf("attempt %d", coord.attempt))
	}

	if includeStepTotals {
		if i, ok := m.index[m.chatStep]; ok {
			s := m.steps[i]
			if s.cost != nil {
				fields = append(fields, fmt.Sprintf("$%.4f", *s.cost))
			}
			if s.tokens > 0 {
				fields = append(fields, humanTokens(s.tokens)+" tok")
			}
		}
	}

	if len(fields) == 0 {
		return ""
	}
	return shared.Theme.Chat.Hint.Render(strings.Join(fields, "  "))
}

// formatMetaTime renders a wall-clock timestamp for the metadata row.
// Time-only (HH:MM:SS) in local time: a single jig run is typically one
// operator sitting so the date adds width without information (Q-11.2).
// The formatter matches the pre-existing t.Local().Format("15:04:05")
// at monitor_transcript.go:529-531 so per-turn and pre-existing display
// paths stay aligned.
func formatMetaTime(t time.Time) string {
	return t.Local().Format("15:04:05")
}

// formatMetaDuration renders a compact elapsed figure for the row's
// `Δ` slot. Sub-500ms durations return "" so the slot is dropped
// rather than rendering "0s" — transcript timestamps are 1-second
// precision, so anything shorter is presentation noise. Everything
// else is rounded to the nearest second so the row does not read
// with jittery sub-second remainders on a live follow.
func formatMetaDuration(d time.Duration) string {
	if d < 500*time.Millisecond {
		return ""
	}
	return d.Round(time.Second).String()
}
