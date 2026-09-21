package main

import (
	"os"
	"reflect"
	"testing"
)

// TestApplyGlobalConfigFlag_StripsFlag proves applyGlobalConfigFlag removes
// the --config token(s) from os.Args, the same pre-dispatch style as
// applyGlobalPresetFlag strips --ascii, and records the resolved path.
func TestApplyGlobalConfigFlag_StripsFlag(t *testing.T) {
	t.Cleanup(resetGlobalConfigFlagPath)

	tests := []struct {
		name     string
		in       []string
		wantArgs []string
		wantPath string
	}{
		{
			name:     "long form with separate value",
			in:       []string{"jig", "--config", "/tmp/alt.toml", "config", "show"},
			wantArgs: []string{"jig", "config", "show"},
			wantPath: "/tmp/alt.toml",
		},
		{
			name:     "long form with =value",
			in:       []string{"jig", "--config=/tmp/alt.toml", "config", "show"},
			wantArgs: []string{"jig", "config", "show"},
			wantPath: "/tmp/alt.toml",
		},
		{
			name:     "short form with =value",
			in:       []string{"jig", "-config=/tmp/alt.toml"},
			wantArgs: []string{"jig"},
			wantPath: "/tmp/alt.toml",
		},
		{
			name:     "no flag present",
			in:       []string{"jig", "run", "wf.toml"},
			wantArgs: []string{"jig", "run", "wf.toml"},
			wantPath: "",
		},
		{
			name:     "after -- terminator (treated as positional)",
			in:       []string{"jig", "run", "--", "--config", "x"},
			wantArgs: []string{"jig", "run", "--", "--config", "x"},
			wantPath: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalConfigFlagPath()
			original := os.Args
			os.Args = append([]string(nil), tt.in...)
			t.Cleanup(func() { os.Args = original })

			applyGlobalConfigFlag()
			if !reflect.DeepEqual(os.Args, tt.wantArgs) {
				t.Errorf("os.Args = %v, want %v", os.Args, tt.wantArgs)
			}
			if globalConfigFlagPath != tt.wantPath {
				t.Errorf("globalConfigFlagPath = %q, want %q", globalConfigFlagPath, tt.wantPath)
			}
		})
	}
}
