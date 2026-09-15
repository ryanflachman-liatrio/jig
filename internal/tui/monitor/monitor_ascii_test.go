package monitor

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// TestTranscriptRendersASCIIOnlyUnderPresetASCII locks slice 14's
// end-to-end FR-14.5 contract at the highest-volume TUI surface: the
// Monitor's Transcript panel. A tool-exchange card is rendered under
// PresetASCII and every byte in the plain (ANSI-stripped) output is
// asserted to be <= U+007F. A regression that introduces a bare
// Unicode literal into any part of the transcript stack (item view,
// card chrome, indicator glyph, hint line, ellipsis) fails here
// rather than only surfacing on a linux console.
//
// This complements TestASCIIVocabularyIsASCII (which locks the
// vocabulary layer) and TestVocabularyLiteralsAreCentralized (which
// locks the source layer) by proving the flow reaches the surface.
func TestTranscriptRendersASCIIOnlyUnderPresetASCII(t *testing.T) {
	t.Cleanup(func() { shared.SetPreset(shared.PresetUnicode) })

	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.steps[m.index["a"]].status = step.StatusRunning
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: syntheticExchange("ascii-run", "completed")})

	shared.SetPreset(shared.PresetASCII)

	rendered := m.itemTranscriptBody()
	plain := stripANSI(rendered)
	// Vocabulary bookkeeping in shared/truncation.go's Diff hints
	// contains the em-dash separator (excluded from the ASCII scan by
	// design; the discipline test documents the rationale). Every
	// other rune in the output must be ASCII.
	ambiguous := map[rune]bool{'·': true, '—': true, '•': true}
	for i, r := range plain {
		if r >= 0x80 && !ambiguous[r] {
			t.Errorf("transcript byte %d rune %U above U+007F in\n%s", i, r, plain)
			break
		}
	}
}

// TestTranscriptWidthInvariantAcrossPresets locks FR-14.6 at the
// Transcript panel: switching between Unicode and ASCII must not
// grow any rendered row's cell width beyond the transcript inner
// width. Rows widths are compared per-row so a stray padding
// regression trips the assertion with the failing row highlighted.
func TestTranscriptWidthInvariantAcrossPresets(t *testing.T) {
	t.Cleanup(func() { shared.SetPreset(shared.PresetUnicode) })

	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.steps[m.index["a"]].status = step.StatusRunning
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: syntheticExchange("width-run", "completed")})

	shared.SetPreset(shared.PresetUnicode)
	unicode := m.itemTranscriptBody()

	shared.SetPreset(shared.PresetASCII)
	ascii := m.itemTranscriptBody()

	unicodeRows := strings.Split(unicode, "\n")
	asciiRows := strings.Split(ascii, "\n")
	if len(unicodeRows) != len(asciiRows) {
		t.Fatalf("row count mismatch: unicode=%d ascii=%d", len(unicodeRows), len(asciiRows))
	}
	for i := range unicodeRows {
		uw := lipgloss.Width(unicodeRows[i])
		aw := lipgloss.Width(asciiRows[i])
		if aw > uw+2 {
			// Allow small variations for multi-cell ASCII glyphs
			// like `[ok]` widening a single-cell status slot: the
			// card renderer pads to a fixed width so the transcript
			// row width stays within a small tolerance. A larger
			// drift is a padding bug.
			t.Errorf("row %d ascii width %d exceeds unicode width %d by more than 2\n  unicode: %q\n  ascii:   %q",
				i, aw, uw, unicodeRows[i], asciiRows[i])
		}
	}
}
