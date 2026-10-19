package transcript

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
)

func TestToolSummaryPersistenceAndBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeAll(t, path, []Entry{{Role: RoleAssistant, Blocks: []Block{{Type: BlockToolUse, Tool: &toolcall.Activity{ID: "run", Summary: "Check tests"}}}}})
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := r.Window(0, 0)
	if err != nil || len(entries) != 1 || entries[0].Blocks[0].Tool.Summary != "Check tests" {
		t.Fatalf("summary did not round trip: %+v, %v", entries, err)
	}
	a := &toolcall.Activity{Summary: strings.Repeat("x", 1000)}
	if !clampActivity(a, 100) || len(a.Summary) > 25 {
		t.Fatalf("summary escaped capture bound: %d bytes", len(a.Summary))
	}
}
