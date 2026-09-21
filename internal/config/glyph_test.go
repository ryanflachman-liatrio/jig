package config

import (
	"testing"

	"jig/internal/tui/shared"
)

func TestResolveGlyphPreset(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag GlyphFlag
		cfg  Config
		want shared.SymbolPreset
	}{
		{name: "flag wins over config unicode value", flag: GlyphFlagASCII, cfg: Config{UI: UIConfig{GlyphPreset: "unicode"}}, want: shared.PresetASCII},
		{name: "explicit unicode flag wins over config ascii value", flag: GlyphFlagUnicode, cfg: Config{UI: UIConfig{GlyphPreset: "ascii"}}, want: shared.PresetUnicode},
		{name: "config wins when flag unset", flag: GlyphFlagUnset, cfg: Config{UI: UIConfig{GlyphPreset: "ascii"}}, want: shared.PresetASCII},
		{name: "default is unicode with no flag and no config", flag: GlyphFlagUnset, cfg: Config{}, want: shared.PresetUnicode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveGlyphPreset(tc.flag, tc.cfg); got != tc.want {
				t.Fatalf("ResolveGlyphPreset(%v, %+v) = %v, want %v", tc.flag, tc.cfg, got, tc.want)
			}
		})
	}
}
