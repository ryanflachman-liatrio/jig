package shared

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Rule renders a single line of exactly width visible cells consisting of
// RuleGlyph fill with a floating label centered inside the fill. It is the
// omp-transcript-parity slice 12 boundary-banner primitive.
//
// When width cannot accommodate the label plus a minimum rule on each side,
// the helper degrades to the bare label (matching omp's
// compaction-summary-message.ts narrow-panel behavior); no broken row is
// ever produced. When the label is empty, the row is `width` fill glyphs.
//
// Glyphs and label are styled through Theme.Marker and its label derivative
// so a single edit reskins every boundary; the helper measures every width
// with lipgloss.Width and never with len(), so wide runes and SGR-styled
// labels compose safely.
//
// Options are additive; unknown/future options (e.g. LeftAnchored) can be
// added without changing existing call sites.
func Rule(width int, label string, opts ...RuleOption) string {
	if width < 1 {
		return ""
	}
	cfg := ruleConfig{labelStyle: Theme.Marker.Bold(true)}
	for _, opt := range opts {
		opt(&cfg)
	}
	glyph := RuleGlyph
	glyphStyle := Theme.Marker
	labelWidth := lipgloss.Width(label)

	// Empty label: fill the full width with glyphs, one style.
	if label == "" {
		return glyphStyle.Render(strings.Repeat(glyph, width))
	}

	// Degradation: below labelWidth + 2*minCap + 2 (two spaces around the
	// label plus one glyph on each side) we cannot draw a rule without
	// cutting into the label. Render the bare label centered on the panel
	// so the caller's prefix and the transcript width still agree.
	const minCap = 1
	minRuled := labelWidth + 2*minCap + 2
	if width < minRuled {
		if labelWidth >= width {
			return cfg.labelStyle.Render(label)
		}
		leftPad := (width - labelWidth) / 2
		rightPad := width - labelWidth - leftPad
		return strings.Repeat(" ", leftPad) +
			cfg.labelStyle.Render(label) +
			strings.Repeat(" ", rightPad)
	}

	// Ruled form: floor((width - labelWidth - 2) / 2) glyphs on the left,
	// the label wrapped in single spaces, and the remaining glyphs on the
	// right. The " label " spacing pattern matches omp's usage-row and
	// compaction-summary banners.
	fill := width - labelWidth - 2
	capLeft := fill / 2
	capRight := fill - capLeft
	return glyphStyle.Render(strings.Repeat(glyph, capLeft)) +
		" " + cfg.labelStyle.Render(label) + " " +
		glyphStyle.Render(strings.Repeat(glyph, capRight))
}

// RuleOption configures Rule. Options are functional so future variants
// (short left-anchored rules, alternative label registers) can be added
// without a breaking API change. Reserved for later slices: LeftAnchored
// / WithLabelStyle. Slice 12 ships only the defaults.
type RuleOption func(*ruleConfig)

type ruleConfig struct {
	labelStyle lipgloss.Style
}
