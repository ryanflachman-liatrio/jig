package main

import (
	"os"
	"reflect"
	"testing"

	"jig/internal/config"
	"jig/internal/tui/shared"
)

// TestApplyGlobalPresetFlag_StripsFlag proves applyGlobalPresetFlag
// removes the --ascii token from os.Args so downstream subcommand flag
// parsers never see it, and returns the correct tri-state config.GlyphFlag
// (Spec 28 Unit 3: an explicit --ascii=false must be distinguishable from
// the flag being absent). Covers the positional variants documented on
// the function.
func TestApplyGlobalPresetFlag_StripsFlag(t *testing.T) {
	tests := []struct {
		name     string
		in       []string
		want     []string
		wantFlag config.GlyphFlag
	}{
		{
			name:     "long form before subcommand",
			in:       []string{"jig", "--ascii", "run", "wf.toml"},
			want:     []string{"jig", "run", "wf.toml"},
			wantFlag: config.GlyphFlagASCII,
		},
		{
			name:     "long form after subcommand",
			in:       []string{"jig", "run", "--ascii", "wf.toml"},
			want:     []string{"jig", "run", "wf.toml"},
			wantFlag: config.GlyphFlagASCII,
		},
		{
			name:     "short form",
			in:       []string{"jig", "-ascii"},
			want:     []string{"jig"},
			wantFlag: config.GlyphFlagASCII,
		},
		{
			name:     "explicit true value",
			in:       []string{"jig", "--ascii=true", "help"},
			want:     []string{"jig", "help"},
			wantFlag: config.GlyphFlagASCII,
		},
		{
			name:     "explicit false value",
			in:       []string{"jig", "--ascii=false", "help"},
			want:     []string{"jig", "help"},
			wantFlag: config.GlyphFlagUnicode,
		},
		{
			name:     "no flag present",
			in:       []string{"jig", "run", "wf.toml"},
			want:     []string{"jig", "run", "wf.toml"},
			wantFlag: config.GlyphFlagUnset,
		},
		{
			name:     "after -- terminator (treated as positional)",
			in:       []string{"jig", "run", "--", "--ascii"},
			want:     []string{"jig", "run", "--", "--ascii"},
			wantFlag: config.GlyphFlagUnset,
		},
		{
			name:     "multiple occurrences all stripped",
			in:       []string{"jig", "--ascii", "run", "--ascii", "wf.toml"},
			want:     []string{"jig", "run", "wf.toml"},
			wantFlag: config.GlyphFlagASCII,
		},
		{
			name:     "only binary name",
			in:       []string{"jig"},
			want:     []string{"jig"},
			wantFlag: config.GlyphFlagUnset,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := os.Args
			os.Args = append([]string(nil), tt.in...)
			t.Cleanup(func() { os.Args = original })

			got := applyGlobalPresetFlag()
			if !reflect.DeepEqual(os.Args, tt.want) {
				t.Errorf("os.Args = %v, want %v", os.Args, tt.want)
			}
			if got != tt.wantFlag {
				t.Errorf("applyGlobalPresetFlag() = %v, want %v", got, tt.wantFlag)
			}
		})
	}
}

// TestApplyResolvedGlyphPreset_SwitchesPreset proves the tri-state flag
// returned by applyGlobalPresetFlag switches the shared vocabulary once
// combined with a Config via applyResolvedGlyphPreset. Covers the round
// trip: absent keeps Unicode; the flag lands on ASCII; explicit
// --ascii=false stays Unicode even when config asks for ascii.
func TestApplyResolvedGlyphPreset_SwitchesPreset(t *testing.T) {
	t.Cleanup(resetPreset)

	tests := []struct {
		name string
		flag config.GlyphFlag
		cfg  config.Config
		want string
	}{
		{"absent keeps Unicode", config.GlyphFlagUnset, config.Config{}, "✓"},
		{"ascii flag flips to ASCII", config.GlyphFlagASCII, config.Config{}, "[ok]"},
		{"explicit unicode flag wins over config ascii", config.GlyphFlagUnicode, config.Config{UI: config.UIConfig{GlyphPreset: "ascii"}}, "✓"},
		{"unset flag falls back to config ascii", config.GlyphFlagUnset, config.Config{UI: config.UIConfig{GlyphPreset: "ascii"}}, "[ok]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetPreset()
			globalPresetFlag = tt.flag
			t.Cleanup(func() { globalPresetFlag = config.GlyphFlagUnset })

			applyResolvedGlyphPreset(tt.cfg)
			if got := shared.IconSuccess; got != tt.want {
				t.Errorf("IconSuccess = %q, want %q", got, tt.want)
			}
		})
	}
}

// resetPreset restores the Unicode preset between subtests so the
// package's package-level vocabulary vars are always in a known state.
// A test that intentionally activates PresetASCII must call
// shared.SetPreset(shared.PresetUnicode) or install this Cleanup.
func resetPreset() {
	shared.SetPreset(shared.PresetUnicode)
}
