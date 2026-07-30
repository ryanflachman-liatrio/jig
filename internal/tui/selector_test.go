package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverWorkflows verifies the startup scan: it walks the tree, keeps only
// files with a [workflow] name, sorts by name, and returns an empty (non-error)
// result when the directory is absent.
func TestDiscoverWorkflows(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Two real workflows (one nested), one non-workflow .toml, one non-toml.
	write("zebra.toml", "[workflow]\nname = \"zebra\"\ndescription = \"z\"\n")
	write("nested/alpha.toml", "[workflow]\nname = \"alpha\"\n")
	write("notes.txt", "[workflow]\nname = \"ignored\"\n")
	write("config.toml", "[defaults]\nmodel = \"claude\"\n")

	msg, ok := discoverWorkflowsCmd(dir)().(workflowsLoadedMsg)
	if !ok {
		t.Fatalf("expected workflowsLoadedMsg")
	}
	if msg.err != nil {
		t.Fatalf("unexpected err: %v", msg.err)
	}
	if len(msg.items) != 2 {
		t.Fatalf("got %d items, want 2", len(msg.items))
	}
	// Sorted by name: alpha before zebra.
	if got := msg.items[0].(workflowItem).name; got != "alpha" {
		t.Fatalf("items[0] = %q, want alpha", got)
	}
	if got := msg.items[1].(workflowItem).name; got != "zebra" {
		t.Fatalf("items[1] = %q, want zebra", got)
	}

	// A missing directory is an empty result, not an error.
	msg, _ = discoverWorkflowsCmd(filepath.Join(dir, "does-not-exist"))().(workflowsLoadedMsg)
	if msg.err != nil || len(msg.items) != 0 {
		t.Fatalf("missing dir: got err=%v items=%d, want nil/0", msg.err, len(msg.items))
	}
}
