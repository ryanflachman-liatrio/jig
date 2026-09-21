package config

import "jig/internal/tui/shared"

// GlyphFlag is the tri-state result of scanning os.Args for --ascii: whether
// the flag was passed at all, and if so, which value it carried. This is
// distinct from "unset" so an explicit --ascii=false is never confused with
// the flag being absent (cmd/jig's applyGlobalPresetFlag produces this).
type GlyphFlag int

const (
	// GlyphFlagUnset means --ascii was not passed at all.
	GlyphFlagUnset GlyphFlag = iota
	// GlyphFlagASCII means --ascii or --ascii=true was passed.
	GlyphFlagASCII
	// GlyphFlagUnicode means --ascii=false was passed explicitly.
	GlyphFlagUnicode
)

// ResolveGlyphPreset combines the CLI flag state with the merged Config to
// pick the active glyph vocabulary: an explicit flag (either state) always
// wins; an unset flag falls back to cfg.UI.GlyphPreset; no flag and no
// config value defaults to Unicode. Called once in main.go's pre-dispatch
// sequence, after loadEffectiveConfig(), uniformly across every entry point.
func ResolveGlyphPreset(flag GlyphFlag, cfg Config) shared.SymbolPreset {
	switch flag {
	case GlyphFlagASCII:
		return shared.PresetASCII
	case GlyphFlagUnicode:
		return shared.PresetUnicode
	}
	if cfg.UI.GlyphPreset == "ascii" {
		return shared.PresetASCII
	}
	return shared.PresetUnicode
}
