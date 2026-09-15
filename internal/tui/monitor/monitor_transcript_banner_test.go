package monitor

import (
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/tui/shared"
)

// Slice 12: boundary label composition.
//
// The precedence table (plan §Field selection):
//   1. generation change → "reset N" (N = curr.generation + 1)
//   2. iteration change  → "iteration N" (N = curr.iteration + 1)
//   3. attempt change    → "retry N" (N = curr.attempt)
//   -. no change         → "" (banner suppressed).

func TestBoundaryLabelGenerationBump(t *testing.T) {
	got := boundaryLabel(toolCorrelationKey{}, toolCorrelationKey{generation: 1})
	if got != "reset 2" {
		t.Fatalf("first reset label = %q, want %q", got, "reset 2")
	}
	got = boundaryLabel(toolCorrelationKey{generation: 3}, toolCorrelationKey{generation: 5})
	if got != "reset 6" {
		t.Fatalf("later reset label = %q, want %q", got, "reset 6")
	}
}

func TestBoundaryLabelIterationBump(t *testing.T) {
	got := boundaryLabel(toolCorrelationKey{}, toolCorrelationKey{iteration: 1})
	if got != "iteration 2" {
		t.Fatalf("first iteration label = %q, want %q", got, "iteration 2")
	}
}

func TestBoundaryLabelAttemptBump(t *testing.T) {
	got := boundaryLabel(toolCorrelationKey{}, toolCorrelationKey{attempt: 1})
	if got != "retry 1" {
		t.Fatalf("first retry label = %q, want %q", got, "retry 1")
	}
	got = boundaryLabel(toolCorrelationKey{attempt: 1}, toolCorrelationKey{attempt: 2})
	if got != "retry 2" {
		t.Fatalf("second retry label = %q, want %q", got, "retry 2")
	}
}

func TestBoundaryLabelGenerationWinsOverInnerBumps(t *testing.T) {
	// A generation bump implicitly resets iteration and attempt to 0;
	// composing all three would triple-count one event. The outermost
	// changed coordinate wins alone.
	got := boundaryLabel(
		toolCorrelationKey{generation: 0, iteration: 3, attempt: 2},
		toolCorrelationKey{generation: 1, iteration: 0, attempt: 0},
	)
	if got != "reset 2" {
		t.Fatalf("generation+inner-reset label = %q, want %q", got, "reset 2")
	}
}

func TestBoundaryLabelIterationWinsOverAttemptBump(t *testing.T) {
	got := boundaryLabel(
		toolCorrelationKey{iteration: 0, attempt: 2},
		toolCorrelationKey{iteration: 1, attempt: 0},
	)
	if got != "iteration 2" {
		t.Fatalf("iteration+attempt-reset label = %q, want %q", got, "iteration 2")
	}
}

func TestBoundaryLabelNoChangeReturnsEmpty(t *testing.T) {
	got := boundaryLabel(
		toolCorrelationKey{generation: 1, iteration: 2, attempt: 3, toolUseID: "same"},
		toolCorrelationKey{generation: 1, iteration: 2, attempt: 3, toolUseID: "different"},
	)
	if got != "" {
		t.Fatalf("no-coord-change label = %q, want empty (toolUseID must not count)", got)
	}
}

// TestBoundaryLabelNeverUsesRetiredReRun locks the vocabulary
// resolution for Q-12.1: "re-run" is the retired render-plan wording
// and must never resurface at any generation. A future refactor
// copying the old strings back would fail this test loudly.
func TestBoundaryLabelNeverUsesRetiredReRun(t *testing.T) {
	for gen := 1; gen <= 5; gen++ {
		got := boundaryLabel(toolCorrelationKey{}, toolCorrelationKey{generation: gen})
		if strings.Contains(got, "re-run") || strings.Contains(got, "rerun") {
			t.Fatalf("generation=%d label = %q, must not contain 're-run'/'rerun'", gen, got)
		}
		if !strings.HasPrefix(got, "reset ") {
			t.Fatalf("generation=%d label = %q, want prefix %q", gen, got, "reset ")
		}
	}
}

func TestRenderBoundaryBannerRoundtrips(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	row := m.renderBoundaryBanner(toolCorrelationKey{}, toolCorrelationKey{iteration: 1})
	if row == "" {
		t.Fatalf("renderBoundaryBanner returned empty for an iteration bump at width 60")
	}
	if !strings.HasPrefix(row, bannerPrefix()) {
		t.Fatalf("banner row missing two-space prefix: %q", ansi.Strip(row))
	}
	if got := lipgloss.Width(row); got != m.transcriptInnerW {
		t.Fatalf("banner row width = %d, want %d (transcriptInnerW)", got, m.transcriptInnerW)
	}
	if !strings.Contains(ansi.Strip(row), "iteration 2") {
		t.Fatalf("banner row missing label: %q", ansi.Strip(row))
	}
}

func TestRenderBoundaryBannerEmptyOnNoChange(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	row := m.renderBoundaryBanner(toolCorrelationKey{iteration: 2}, toolCorrelationKey{iteration: 2})
	if row != "" {
		t.Fatalf("banner rendered for no-op transition: %q", ansi.Strip(row))
	}
}

func TestRenderBoundaryBannerDegradesOnNarrowWidth(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 10 // banner budget = 8 cells, well below rule minimum.
	row := m.renderBoundaryBanner(toolCorrelationKey{}, toolCorrelationKey{iteration: 11})
	plain := ansi.Strip(row)
	if !strings.Contains(plain, "iteration 12") {
		t.Fatalf("narrow width lost label: %q", plain)
	}
	if strings.ContainsRune(plain, '─') {
		t.Fatalf("narrow width still drew rule glyphs: %q", plain)
	}
}

// TestRenderBoundaryBannerEmptyOnPersistenceOff exercises the
// transcriptInnerW == 0 edge case (Monitor before its first layout or
// with a degenerate terminal size). The helper must return "" so the
// item loop skips it cleanly rather than emitting a broken row.
func TestRenderBoundaryBannerEmptyOnPersistenceOff(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 0
	row := m.renderBoundaryBanner(toolCorrelationKey{}, toolCorrelationKey{iteration: 1})
	if row != "" {
		t.Fatalf("zero-width banner = %q, want empty", ansi.Strip(row))
	}
}

// TestBannerSourceUsesSharedRuleGlyph is the CC-7 regression guard:
// monitor_transcript_banner.go must not embed a bare "─" glyph literal
// or a hardcoded rule string. Every rule glyph must flow through
// shared.RuleGlyph so slice 14's preset table can retheme boundary
// banners without editing this file.
func TestBannerSourceUsesSharedRuleGlyph(t *testing.T) {
	src, err := os.ReadFile("monitor_transcript_banner.go")
	if err != nil {
		t.Fatalf("read banner source: %v", err)
	}
	if strings.Contains(string(src), string(shared.RuleGlyph)) {
		t.Fatalf("monitor_transcript_banner.go contains bare rule glyph literal %q; use shared.RuleGlyph indirectly via shared.Rule",
			shared.RuleGlyph)
	}
}
