package main

import (
	"os"
	"reflect"
	"testing"

	"jig/internal/tui/shared"
)

// TestApplyGlobalPresetFlag_StripsFlag proves applyGlobalPresetFlag
// removes the --ascii token from os.Args so downstream subcommand flag
// parsers never see it. Covers the positional variants documented on
// the function.
func TestApplyGlobalPresetFlag_StripsFlag(t *testing.T) {
	t.Cleanup(resetPreset)

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "long form before subcommand",
			in:   []string{"jig", "--ascii", "run", "wf.toml"},
			want: []string{"jig", "run", "wf.toml"},
		},
		{
			name: "long form after subcommand",
			in:   []string{"jig", "run", "--ascii", "wf.toml"},
			want: []string{"jig", "run", "wf.toml"},
		},
		{
			name: "short form",
			in:   []string{"jig", "-ascii"},
			want: []string{"jig"},
		},
		{
			name: "explicit true value",
			in:   []string{"jig", "--ascii=true", "help"},
			want: []string{"jig", "help"},
		},
		{
			name: "explicit false value",
			in:   []string{"jig", "--ascii=false", "help"},
			want: []string{"jig", "help"},
		},
		{
			name: "no flag present",
			in:   []string{"jig", "run", "wf.toml"},
			want: []string{"jig", "run", "wf.toml"},
		},
		{
			name: "after -- terminator (treated as positional)",
			in:   []string{"jig", "run", "--", "--ascii"},
			want: []string{"jig", "run", "--", "--ascii"},
		},
		{
			name: "multiple occurrences all stripped",
			in:   []string{"jig", "--ascii", "run", "--ascii", "wf.toml"},
			want: []string{"jig", "run", "wf.toml"},
		},
		{
			name: "only binary name",
			in:   []string{"jig"},
			want: []string{"jig"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetPreset()
			original := os.Args
			os.Args = append([]string(nil), tt.in...)
			t.Cleanup(func() { os.Args = original })

			applyGlobalPresetFlag()
			if !reflect.DeepEqual(os.Args, tt.want) {
				t.Errorf("os.Args = %v, want %v", os.Args, tt.want)
			}
		})
	}
}

// TestApplyGlobalPresetFlag_SwitchesPreset proves that a truthy --ascii
// switches the shared vocabulary. Covers the round trip: a run without
// the flag keeps Unicode; a run with the flag lands on ASCII; explicit
// --ascii=false is a no-op (kept for completeness, since the default
// is Unicode).
func TestApplyGlobalPresetFlag_SwitchesPreset(t *testing.T) {
	t.Cleanup(resetPreset)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"absent keeps Unicode", []string{"jig", "help"}, "✓"},
		{"--ascii flips to ASCII", []string{"jig", "--ascii", "help"}, "[ok]"},
		{"--ascii=true flips to ASCII", []string{"jig", "--ascii=true"}, "[ok]"},
		{"--ascii=false stays Unicode", []string{"jig", "--ascii=false"}, "✓"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetPreset()
			original := os.Args
			os.Args = append([]string(nil), tt.args...)
			t.Cleanup(func() { os.Args = original })

			applyGlobalPresetFlag()
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
