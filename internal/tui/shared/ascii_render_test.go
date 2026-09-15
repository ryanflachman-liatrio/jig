package shared

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestRenderCardWidthInvariantUnderBothPresets locks FR-14.6 at the
// card renderer: swapping to PresetASCII must not change any row's
// cell width. This is the width contract's canonical proof: the pilot
// consumer that reskins under the preset flip keeps its box aligned.
// The test walks a matrix of widths (narrow, medium, wide) and header
// contents (empty, short, wide) so an ASCII form that widens under a
// specific column budget is caught here rather than in a screenshot.
func TestRenderCardWidthInvariantUnderBothPresets(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	cases := []struct {
		name  string
		card  Card
		width int
	}{
		{
			name:  "narrow_empty",
			width: 20,
			card: Card{
				Header:   "step-1",
				Sections: []CardSection{{Lines: []string{"ok"}}},
				State:    CardSuccess,
			},
		},
		{
			name:  "medium_header",
			width: 40,
			card: Card{
				Header:     "step-2",
				HeaderMeta: "1m 20s",
				Sections:   []CardSection{{Lines: []string{"body line one", "body line two"}}},
				State:      CardRunning,
			},
		},
		{
			name:  "wide_multi_section",
			width: 80,
			card: Card{
				Header: "step-3",
				Sections: []CardSection{
					{Label: "input", Lines: []string{"payload"}},
					{Rule: true, Lines: []string{"aftermath"}},
				},
				State: CardError,
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tt.card.Width = tt.width

			SetPreset(PresetUnicode)
			unicode := RenderCard(tt.card)
			SetPreset(PresetASCII)
			ascii := RenderCard(tt.card)

			unicodeRows := strings.Split(unicode, "\n")
			asciiRows := strings.Split(ascii, "\n")
			if len(unicodeRows) != len(asciiRows) {
				t.Fatalf("row count mismatch: unicode=%d ascii=%d", len(unicodeRows), len(asciiRows))
			}
			for i := range unicodeRows {
				uw := lipgloss.Width(unicodeRows[i])
				aw := lipgloss.Width(asciiRows[i])
				if uw != tt.width {
					t.Errorf("row %d unicode width = %d, want %d", i, uw, tt.width)
				}
				if aw != tt.width {
					t.Errorf("row %d ascii width = %d, want %d", i, aw, tt.width)
				}
			}
		})
	}
}

// TestRenderCardBorderBytesAreASCIIUnderPresetASCII locks FR-14.5 at
// the pilot renderer: under PresetASCII every byte of the border
// glyphs (corners, tees, verticals) is at most U+007F. The check
// strips ANSI SGR sequences first so it inspects the visible payload
// only. Complements TestASCIIVocabularyIsASCII by proving the
// vocabulary flows all the way to the rendered surface, not just the
// table.
func TestRenderCardBorderBytesAreASCIIUnderPresetASCII(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	SetPreset(PresetASCII)
	out := RenderCard(Card{
		Width:    30,
		Header:   "step",
		Sections: []CardSection{{Lines: []string{"content"}}},
		State:    CardSuccess,
	})
	plain := ansi.Strip(out)
	for i, r := range plain {
		if r >= 0x80 {
			t.Errorf("byte %d of rendered card is above U+007F: %U in %q", i, r, plain)
		}
	}
}

// TestRenderCardHeaderUsesPresetGlyphs proves the pilot renderer's
// top-line header degrades to the ASCII corner glyphs. The Unicode
// case shows the rounded corners and the ASCII case shows the '+'
// substitute, so a snapshot of just the top row differs between
// presets exactly at the corners.
func TestRenderCardHeaderUsesPresetGlyphs(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	SetPreset(PresetUnicode)
	unicode := RenderCard(Card{Width: 20, Header: "step", State: CardSuccess})
	topUnicode := ansi.Strip(strings.SplitN(unicode, "\n", 2)[0])
	if !strings.HasPrefix(topUnicode, unicodeSymbols.BoxCornerTL) {
		t.Errorf("unicode top-left = %q, want prefix %q", topUnicode, unicodeSymbols.BoxCornerTL)
	}
	if !strings.HasSuffix(topUnicode, unicodeSymbols.BoxCornerTR) {
		t.Errorf("unicode top-right = %q, want suffix %q", topUnicode, unicodeSymbols.BoxCornerTR)
	}

	SetPreset(PresetASCII)
	ascii := RenderCard(Card{Width: 20, Header: "step", State: CardSuccess})
	topASCII := ansi.Strip(strings.SplitN(ascii, "\n", 2)[0])
	if !strings.HasPrefix(topASCII, asciiSymbols.BoxCornerTL) {
		t.Errorf("ascii top-left = %q, want prefix %q", topASCII, asciiSymbols.BoxCornerTL)
	}
	if !strings.HasSuffix(topASCII, asciiSymbols.BoxCornerTR) {
		t.Errorf("ascii top-right = %q, want suffix %q", topASCII, asciiSymbols.BoxCornerTR)
	}
}

// TestPanelTopEdgeWidthInvariantUnderBothPresets locks FR-14.6 on
// PanelTopEdge: the ASCII fallback (`+`) widens no more than the
// Unicode form so a panel title bar's visible width is preset-agnostic.
// This is the sibling proof for TestRenderCardWidthInvariantUnderBothPresets
// on the other pilot consumer.
func TestPanelTopEdgeWidthInvariantUnderBothPresets(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	for _, width := range []int{10, 25, 60, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			SetPreset(PresetUnicode)
			unicode := PanelTopEdge("Steps", width, Theme.Panel.FocusedBorder)
			SetPreset(PresetASCII)
			ascii := PanelTopEdge("Steps", width, Theme.Panel.FocusedBorder)

			if uw := lipgloss.Width(unicode); uw != width {
				t.Errorf("unicode top edge width = %d, want %d", uw, width)
			}
			if aw := lipgloss.Width(ascii); aw != width {
				t.Errorf("ascii top edge width = %d, want %d", aw, width)
			}
		})
	}
}

// TestTruncationVocabularyDegradesUnderASCII walks MoreItems,
// EarlierItems, and the three hint constructors and proves the
// leading ellipsis flows through the vocabulary. Under Unicode the
// output starts with `…`; under ASCII it starts with `...` — no
// escape from the vocabulary is possible without editing symbols.go.
func TestTruncationVocabularyDegradesUnderASCII(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	SetPreset(PresetUnicode)
	uMore := MoreItems(3, "line", "lines")
	uEarlier := EarlierItems(3, "line", "lines")
	uCapture := CaptureTruncatedHint()
	uClamped := DiffClampedHint()
	uUnavailable := DiffUnavailableHint()

	SetPreset(PresetASCII)
	aMore := MoreItems(3, "line", "lines")
	aEarlier := EarlierItems(3, "line", "lines")
	aCapture := CaptureTruncatedHint()
	aClamped := DiffClampedHint()
	aUnavailable := DiffUnavailableHint()

	cases := []struct {
		name string
		u, a string
	}{
		{"MoreItems", uMore, aMore},
		{"EarlierItems", uEarlier, aEarlier},
		{"CaptureTruncatedHint", uCapture, aCapture},
		{"DiffClampedHint", uClamped, aClamped},
		{"DiffUnavailableHint", uUnavailable, aUnavailable},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.u, "…") {
				t.Errorf("unicode %s = %q, want leading …", tt.name, tt.u)
			}
			if !strings.HasPrefix(tt.a, "...") {
				t.Errorf("ascii %s = %q, want leading ...", tt.name, tt.a)
			}
			for _, r := range tt.a {
				if r >= 0x80 {
					t.Errorf("ascii %s contains non-ASCII rune %U in %q", tt.name, r, tt.a)
				}
			}
		})
	}
}
