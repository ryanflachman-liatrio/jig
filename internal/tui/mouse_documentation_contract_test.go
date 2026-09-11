package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMouseNavigationDocumentationContract(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "TUI.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := strings.ToLower(string(data))
	for _, phrase := range []string{
		"mouse navigation",
		"keyboard-primary",
		"no configuration",
		"workflows", "runs", "steps", "transcript", "detail",
		"primary click", "does not activate",
		"three rows", "three rendered lines",
		"panel under the pointer", "preserves keyboard focus",
		"modals", "text input",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("docs/TUI.md missing mouse contract phrase %q", phrase)
		}
	}
}
