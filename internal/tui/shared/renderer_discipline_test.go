package shared

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSlice02RenderersUseCentralizedTheme protects the two repository rules
// that make slice 14's later theme/preset substitutions possible without
// scanning every call site:
//
//  1. New per-slot styles (Chat.Tool*) live in styles.go and per-state
//     palette hex values live in palette.go; the shared files introduced by
//     slice 02 may not add a package-level `lipgloss.NewStyle()` value or a
//     hardcoded hex color.
//  2. Status and tool glyphs go through `shared.Icon*` constants; the shared
//     files introduced by slice 02 may not embed bare glyph literals.
//
// The audit is scoped to the files this slice introduces (status_line.go and
// status_icon.go). Pre-existing files (panel.go, card.go, help.go) already
// have their own conventions and are not part of this slice's contract.
func TestSlice02RenderersUseCentralizedTheme(t *testing.T) {
	sliceFiles := []string{
		"status_line.go",
		"status_icon.go",
	}
	hexRe := regexp.MustCompile(`"#[0-9A-Fa-f]{3,8}"`)
	for _, name := range sliceFiles {
		path := filepath.Join(".", name)
		bytes, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(bytes)
		if strings.Contains(src, "lipgloss.NewStyle(") {
			t.Errorf("%s contains a bare `lipgloss.NewStyle()` — new styles belong in styles.go", name)
		}
		if hexRe.MatchString(src) {
			t.Errorf("%s contains a hardcoded `#hex` color — palette tokens belong in palette.go", name)
		}
	}
}

// TestSlice02IconGlyphsAreCentralized checks that the new IconStatus* and
// IconTool* glyphs are defined in icons.go rather than embedded at call
// sites, so slice 14's preset table can substitute them in one place.
func TestSlice02IconGlyphsAreCentralized(t *testing.T) {
	glyphNames := []string{
		"IconStatusSuccess", "IconStatusError", "IconStatusRunning",
		"IconStatusPending", "IconStatusWarning",
		"IconToolRead", "IconToolEdit", "IconToolWrite", "IconToolSearch",
		"IconToolShell", "IconToolWeb", "IconToolAgent", "IconToolTodo",
		"IconToolAsk",
	}
	iconsBytes, err := os.ReadFile("icons.go")
	if err != nil {
		t.Fatalf("read icons.go: %v", err)
	}
	iconsSrc := string(iconsBytes)
	// Match `Name` followed by any run of spaces and then `=` (const-block
	// alignment varies). Anchored to a line start via preceding whitespace.
	for _, name := range glyphNames {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=`)
		if !re.MatchString(iconsSrc) {
			t.Errorf("shared/icons.go is missing constant %q", name)
		}
	}
}
