package shared

import (
	"strings"
	"testing"
)

const testBackground = "\x1b[48;2;10;20;30m"

func TestTintRowReappliesAfterBackgroundResets(t *testing.T) {
	cases := []struct {
		name  string
		reset string
	}{
		{"bare reset", "\x1b[0m"},
		{"empty params", "\x1b[m"},
		{"background only reset", "\x1b[49m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := "before" + tc.reset + "after"
			got := TintRow(row, testBackground)
			want := testBackground + "before" + tc.reset + testBackground + "after" + "\x1b[49m"
			if got != want {
				t.Fatalf("TintRow(%q) = %q, want %q", row, got, want)
			}
		})
	}
}

func TestTintRowLeavesNonResetSGRUntouched(t *testing.T) {
	row := "before\x1b[1mbold\x1b[22mafter"
	got := TintRow(row, testBackground)
	if strings.Count(got, testBackground) != 1 {
		t.Fatalf("TintRow reapplied background after a non-reset SGR sequence: %q", got)
	}
	if !strings.HasPrefix(got, testBackground) || !strings.HasSuffix(got, "\x1b[49m") {
		t.Fatalf("TintRow did not wrap row in background/reset: %q", got)
	}
}

func TestTintRowIgnoresLiteralZeroInExtendedColor(t *testing.T) {
	// 38;2;0;0;0 sets an RGB foreground of pure black; none of its zero
	// components is an SGR reset code and must not trigger a reapply.
	row := "before\x1b[38;2;0;0;0mafter"
	got := TintRow(row, testBackground)
	if strings.Count(got, testBackground) != 1 {
		t.Fatalf("TintRow treated a literal-zero color component as a reset: %q", got)
	}
}

func TestTintRowIgnoresLiteralZeroInPaletteColor(t *testing.T) {
	// 48;5;0 sets an indexed background color of index 0; the index itself is
	// not a reset code even though it is the literal string "0".
	row := "before\x1b[48;5;0mafter"
	got := TintRow(row, testBackground)
	if strings.Count(got, testBackground) != 1 {
		t.Fatalf("TintRow treated a literal-zero palette index as a reset: %q", got)
	}
}
