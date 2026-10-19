package transcript

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLocationOriginRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for attempt, dir := range []string{t.TempDir(), t.TempDir()} {
		_, err := w.Append(Entry{ExecutionDir: dir, Generation: 2, Iteration: 3, Attempt: attempt, Role: RoleAssistant, Blocks: []Block{{Type: BlockText, Text: "synthetic"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.TailPage(10)
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Entries[0].ExecutionDir == "" || page.Entries[1].ExecutionDir == "" || page.Entries[0].ExecutionDir == page.Entries[1].ExecutionDir {
		t.Fatal("lost per-attempt origins")
	}
	for i, e := range page.Entries {
		if e.Attempt != i || e.Generation != 2 || e.Iteration != 3 {
			t.Fatalf("coordinates=%+v", e)
		}
	}
	var old Entry
	if err := json.Unmarshal([]byte(`{"seq":1,"role":"assistant","blocks":[]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.ExecutionDir != "" {
		t.Fatal("invented origin")
	}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "execution_dir") {
		t.Fatal("absent origin not omitted")
	}
}
