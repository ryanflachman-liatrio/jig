package shared

import (
	"time"

	"charm.land/lipgloss/v2"
)

// Phase-locked spinner frame source (omp-transcript-parity slice 13).
//
// The Transcript panel already runs a single-owner frame loop
// (internal/tui/monitor/monitor_frame.go: monitorFrameInterval = 100ms,
// EnsureFrame, anyRunning). What was missing was a *frame index* callers
// could read from wall-clock time so several concurrent indicators would
// spin in lockstep instead of each owning a private timer. This file
// supplies that source as a pure function:
//
//	glyph, ok := shared.SpinnerFrame("status", time.Now())
//
// Callers read a frame during View construction; they never arm a timer,
// never spawn a goroutine, and never return a tea.Cmd. The monitor's
// existing TickMsg re-arm keeps repaints flowing while anyRunning() is
// true — the source itself schedules nothing.
//
// Two invariants drive the design:
//
//   - Wall-time derivation (FR-13.1/FR-13.2). The frame index is a pure
//     function of the current time and the set size, so two concurrent
//     lookups at the same instant return the same glyph and a caller
//     joining an animation mid-revolution lands at the correct phase.
//
//   - Bounded frames (FR-13.3/FR-13.6/FR-13.8). Every glyph in every
//     registered set is single-cell (asserted by spinner_test.go) so an
//     animating field never shifts the row layout. The ASCII fallback is
//     colocated with the Unicode set so slice 14's preset flip touches
//     one file.

// SpinnerAdvanceMS is the wall-time window (in milliseconds) during which
// SpinnerFrame returns the same frame. Set to match
// monitor.monitorFrameInterval so no frame ever arrives that the monitor
// has not already scheduled a repaint for; a higher rate would ask the
// panel to animate faster than it can flush, and a lower rate would waste
// frames the loop already publishes. See Q-13.2 in
// docs/plans/omp-slice-13-liveness-and-spinners.md.
const SpinnerAdvanceMS = 100

// SpinnerFrames is one named frame set: the Unicode glyphs seen on
// capable terminals plus an ASCII fallback for the ASCII glyph preset
// slice 14 will introduce. Both slices are ordered and cyclic; the frame
// index is (now.UnixMilli() / SpinnerAdvanceMS) mod len(active).
//
// Every glyph in both slices MUST measure lipgloss.Width == 1 so the
// consumer's row width does not shift as frames advance
// (TestSpinnerFrameEveryFrameSingleCell and
// TestSpinnerFrameASCIIFallbackSingleCell enforce this).
type SpinnerFrames struct {
	Unicode []string
	ASCII   []string
}

// spinnerFrameSets is the registry of frame sets keyed by a caller-facing
// name. It is unexported so callers register through RegisterSpinnerSet;
// direct map mutation from another package would defeat the width
// invariant that spinner_test.go enforces on package data.
var spinnerFrameSets = map[string]SpinnerFrames{}

func init() {
	// The "status" set — omp's status rotor. Braille glyphs are single
	// cell on every terminal that supports them (verified by the
	// per-set width tests). ASCII fallback is the four-frame `| / - \`
	// dial: single-cell everywhere, and half the frame count so the
	// same wall-clock advance produces a comparable rotation.
	RegisterSpinnerSet("status", SpinnerFrames{
		Unicode: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		ASCII:   []string{"|", "/", "-", "\\"},
	})
}

// RegisterSpinnerSet publishes a frame set under name for
// SpinnerFrame lookups. Later slices (slice 10's thinking pulse) call
// this from their own package init to add new sets without editing this
// file. Re-registering an existing name overwrites the previous entry;
// this is intentional so tests can substitute deterministic sets, but
// production code registers each set exactly once.
func RegisterSpinnerSet(name string, set SpinnerFrames) {
	spinnerFrameSets[name] = set
}

// SpinnerFrame returns the glyph for the current window of a registered
// set, or ("", false) when the set is unknown or empty so callers can
// no-op cleanly. The index is a pure function of now and the set size:
//
//	frame(t) = active[ (t / SpinnerAdvanceMS) mod len(active) ]
//
// Callers pass time.Now() at the site that composes their View; the
// value is not stored, so there is no per-caller state and no timer.
func SpinnerFrame(name string, now time.Time) (string, bool) {
	set, ok := spinnerFrameSets[name]
	if !ok {
		return "", false
	}
	active := activeSpinnerFrames(set)
	if len(active) == 0 {
		return "", false
	}
	ms := now.UnixMilli()
	if ms < 0 {
		// A negative UnixMilli can happen only for pre-1970 timestamps;
		// treat as the first window rather than propagating a negative
		// modulo (Go's % on negatives would return a negative index).
		return active[0], true
	}
	idx := (ms / SpinnerAdvanceMS) % int64(len(active))
	return active[idx], true
}

// activeSpinnerFrames selects the Unicode or ASCII slice according to
// the active glyph preset. Slice 14 will introduce the preset switch as
// a package-level accessor; today the branch is unreachable and Unicode
// always wins. The branch exists so slice 14 flips one predicate rather
// than editing every SpinnerFrame call site.
func activeSpinnerFrames(set SpinnerFrames) []string {
	if spinnerASCIIPreset() {
		return set.ASCII
	}
	if len(set.Unicode) > 0 {
		return set.Unicode
	}
	return set.ASCII
}

// spinnerASCIIPreset is the future ASCII glyph preset predicate (CC-7,
// slice 14). Today it always reports false; when slice 14 lands it will
// consult the same preset selector that Rule/IconStatus* will consult.
// Kept as a private predicate so slice 14 edits one function rather
// than every caller.
func spinnerASCIIPreset() bool {
	return false
}

// SpinnerFrameWidth returns the cell width of the widest glyph in a
// registered set, which callers use to reserve a fixed-width slot for a
// spinner so the row does not shift as frames advance. It returns 0 for
// an unknown set; callers should treat 0 as "do not reserve a slot".
func SpinnerFrameWidth(name string) int {
	set, ok := spinnerFrameSets[name]
	if !ok {
		return 0
	}
	active := activeSpinnerFrames(set)
	max := 0
	for _, glyph := range active {
		if w := lipgloss.Width(glyph); w > max {
			max = w
		}
	}
	return max
}
