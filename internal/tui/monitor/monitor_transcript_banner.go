package monitor

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// Execution-boundary banner (omp-transcript-parity slice 12).
//
// A boundary banner is one centered, width-aware labeled rule that
// announces an execution-coordinate transition — a manual Run.Reset
// (generation bump), a bounded route rewind (iteration bump), or a
// [step.retry] attempt (attempt bump). It is emitted inside the
// two-line execution-coordinate gap between visible items and folded
// into the closing item's chatItemLineRanges entry so n/N block
// navigation still lands on the arriving item, not the banner. See
// docs/plans/omp-slice-12-boundary-banners.md.

// boundaryLabel returns the banner label for a transition from prev to
// curr. Precedence is outermost-changed-wins: a bump on the outer
// coordinate implicitly resets the inner ones, so we never compose
// "reset 2 · iteration 1 · retry 0" from a single visible-item
// transition (that would triple-count one event).
//
// Display convention (see plan Q-12.1 audit):
//
//   - generation and iteration display the 1-indexed pass number
//     (raw+1): iteration=0 is the first pass; a transition into
//     iteration=1 renders as "iteration 2" (the second pass).
//   - attempt displays the 1-indexed retry count (raw): attempt=0
//     means "no retry yet" and produces no banner; a transition into
//     attempt=1 renders as "retry 1" (the first retry).
//
// These are different semantic anchors (pass number vs retry count),
// but each is internally consistent. Do not "normalize" attempt to
// raw+1: it would shift every retry label by one and lose the
// operator-facing meaning of "the first retry".
func boundaryLabel(prev, curr toolCorrelationKey) string {
	switch {
	case curr.generation != prev.generation:
		return fmt.Sprintf("reset %d", curr.generation+1)
	case curr.iteration != prev.iteration:
		return fmt.Sprintf("iteration %d", curr.iteration+1)
	case curr.attempt != prev.attempt:
		return fmt.Sprintf("retry %d", curr.attempt)
	}
	return ""
}

// bannerWidth returns the visible cell budget available for a banner
// inside the transcript panel. Banners always render with the two-space
// unselected transcript gutter (they are not selectable and never wear
// the cursor bar), so the width is transcriptInnerW minus the two-cell
// prefix.
func (m *Model) bannerWidth() int {
	w := m.transcriptInnerW - lipgloss.Width(bannerPrefix())
	if w < 1 {
		return 0
	}
	return w
}

// bannerPrefix returns the fixed two-space transcript gutter used for
// every banner row. Banners are not selectable, so this never varies
// with chatItemCursor and never renders the cursor bar.
func bannerPrefix() string { return "  " }

// renderBoundaryBanner returns the styled banner row for a transition
// from prev to curr, or "" when the transition is a no-op. The row is
// prefix + shared.Rule so the banner sits inside the same fixed-width
// gutter as every other transcript row.
func (m *Model) renderBoundaryBanner(prev, curr toolCorrelationKey) string {
	label := boundaryLabel(prev, curr)
	if label == "" {
		return ""
	}
	width := m.bannerWidth()
	if width < 1 {
		return ""
	}
	return bannerPrefix() + shared.Rule(width, label)
}
