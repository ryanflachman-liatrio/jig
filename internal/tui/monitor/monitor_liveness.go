package monitor

import (
	"time"

	"jig/internal/tui/shared"
)

// Liveness pulse consumer (omp-transcript-parity slice 13).
//
// The Transcript panel's `LIVE` chip is the single production consumer of
// shared.SpinnerFrame introduced by this slice: when the panel is
// following a running step, the static word becomes a phase-locked
// glyph + label pair. Card headers do not animate (FR-13.7), boundary
// banners are transitions not liveness (see slice 12), and metadata
// rows are reference material — so the panel-level chip is the one
// place a running-step cue belongs. See
// docs/plans/omp-slice-13-liveness-and-spinners.md for the consumer
// audit that ruled out every other candidate site.
//
// The label survives verbatim; omp's rule at
// packages/coding-agent/src/modes/components/assistant-message.ts:451-474
// is *the label is mandatory* so the animation degrades cleanly on
// terminals without Unicode, in search text, in accessibility tools,
// and in test assertions that grep for "LIVE". Slice 14's ASCII glyph
// preset flips the SpinnerFrame lookup in one place.

// liveCrumb returns the trailing `LIVE` label for the Transcript
// panel's status line and title crumb. When the run is live, a
// running step exists, and the operator is currently following the
// transcript to the bottom, the label is prefixed with a phase-locked
// spinner glyph read from shared.SpinnerFrame("status", now). Under
// any other condition the label stays verbatim, so nothing regresses
// on paused / not-following / no-step-running paths.
//
// The `now` parameter is passed by callers as time.Now() during View
// composition. It is not captured; there is no per-caller state and no
// timer — the existing monitor frame loop (monitor_frame.go) re-arms
// while anyRunning() is true, and this function reads whatever frame
// that loop is currently on.
func (m Model) liveCrumb(now time.Time) string {
	if !m.liveCrumbShouldPulse() {
		return "LIVE"
	}
	glyph, ok := shared.SpinnerFrame("status", now)
	if !ok || glyph == "" {
		return "LIVE"
	}
	return glyph + " LIVE"
}

// liveCrumbShouldPulse encodes the four preconditions the pulse rides.
// All four must hold for the animated form to render:
//
//  1. The Transcript panel is showing a transcript (not a file or a
//     review workspace) — a file/review view has no notion of "LIVE".
//  2. Follow is available for the current view (showsTranscriptFollow):
//     the panel would render "LIVE" or "N new" today.
//  3. chatAutoScroll is true — the operator is at the tail; a paused
//     panel already shows "N new" instead of "LIVE" today.
//  4. anyRunning() is true — at least one step is executing, which is
//     also the condition under which the frame loop keeps ticking, so
//     the pulse never asks the loop to run for its sake alone (FR-13.5).
//
// Any change to these preconditions should ripple into the caller in
// monitor_view.go that composes the static "LIVE" word — the animated
// and static forms are two views of the same condition.
func (m Model) liveCrumbShouldPulse() bool {
	if m.selectedContent().kind != contentTranscript {
		return false
	}
	if !m.showsTranscriptFollow() {
		return false
	}
	if !m.chatAutoScroll {
		return false
	}
	return m.anyRunning()
}
