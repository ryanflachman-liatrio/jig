package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAll appends every entry to a fresh transcript at path and closes it.
func writeAll(t *testing.T, path string, entries []Entry) *Writer {
	t.Helper()
	w, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i, e := range entries {
		if _, err := w.Append(e); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return w
}

// sampleEntries exercises every block type plus iter/attempt tagging.
func sampleEntries() []Entry {
	return []Entry{
		{
			Iteration: 0, Attempt: 0, Role: RoleAssistant,
			Blocks: []Block{
				{Type: BlockThinking, Text: "let me think"},
				{Type: BlockText, Text: "Here is my plan."},
				{Type: BlockToolUse, ToolUseID: "toolu_1", Name: "Read",
					Input: json.RawMessage(`{"file_path":"/tmp/x"}`)},
			},
		},
		{
			Iteration: 0, Attempt: 0, Role: RoleUser,
			Blocks: []Block{
				{Type: BlockToolResult, ToolUseID: "toolu_1", Content: "file contents", IsError: false},
			},
		},
		{
			Iteration: 1, Attempt: 2, Role: RoleResult,
			Blocks: []Block{
				{Type: BlockText, Text: "done"},
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	want := sampleEntries()
	writeAll(t, path, want)

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := r.Window(0, 0)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		g := got[i]
		if g.Seq != i+1 {
			t.Errorf("entry %d: Seq = %d, want %d", i, g.Seq, i+1)
		}
		if g.Ts == "" {
			t.Errorf("entry %d: Ts is empty", i)
		}
		if g.Iteration != want[i].Iteration || g.Attempt != want[i].Attempt {
			t.Errorf("entry %d: iter/attempt = %d/%d, want %d/%d",
				i, g.Iteration, g.Attempt, want[i].Iteration, want[i].Attempt)
		}
		if g.Role != want[i].Role {
			t.Errorf("entry %d: Role = %q, want %q", i, g.Role, want[i].Role)
		}
		if len(g.Blocks) != len(want[i].Blocks) {
			t.Fatalf("entry %d: %d blocks, want %d", i, len(g.Blocks), len(want[i].Blocks))
		}
		for j := range want[i].Blocks {
			wb, gb := want[i].Blocks[j], g.Blocks[j]
			if gb.Type != wb.Type || gb.Text != wb.Text || gb.ToolUseID != wb.ToolUseID ||
				gb.Name != wb.Name || gb.Content != wb.Content || gb.IsError != wb.IsError {
				t.Errorf("entry %d block %d: got %+v, want %+v", i, j, gb, wb)
			}
			if string(gb.Input) != string(wb.Input) {
				t.Errorf("entry %d block %d: Input = %s, want %s", i, j, gb.Input, wb.Input)
			}
		}
	}
}

func TestTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	w, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.MaxBlockBytes = 100
	big := strings.Repeat("a", 500)
	if _, err := w.Append(Entry{Role: RoleUser, Blocks: []Block{
		{Type: BlockToolResult, ToolUseID: "t1", Content: big},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, _ := Open(path)
	got, err := r.Window(0, 0)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	b := got[0].Blocks[0]
	if !b.Truncated {
		t.Error("expected Truncated=true")
	}
	if len(b.Content) > 100 {
		t.Errorf("content len = %d, want <= 100", len(b.Content))
	}
}

func TestTruncationRuneBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	w, _ := Create(path)
	w.MaxBlockBytes = 10
	// Multi-byte runes (3 bytes each) so the cap lands mid-rune.
	if _, err := w.Append(Entry{Role: RoleAssistant, Blocks: []Block{
		{Type: BlockText, Text: strings.Repeat("世", 10)},
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	w.Close()

	r, _ := Open(path)
	got, _ := r.Window(0, 0)
	// The cap lands mid-rune; clampString must back off to a boundary, so the
	// stored length is a whole number of 3-byte runes and never exceeds the cap.
	if len(got[0].Blocks[0].Text) > 10 {
		t.Errorf("text len = %d, want <= 10", len(got[0].Blocks[0].Text))
	}
	if len(got[0].Blocks[0].Text)%3 != 0 {
		t.Errorf("text length %d is not a whole number of 3-byte runes", len(got[0].Blocks[0].Text))
	}
}

func TestReaderSkipsCorruptAndUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeAll(t, path, sampleEntries()) // 3 valid entries

	// Append an entry with an unknown block type, a garbage line, and a partial
	// trailing line (no newline) simulating a crash mid-write.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	unknown := Entry{Seq: 4, Ts: "2026-07-31T00:00:00Z", Role: RoleAssistant,
		Blocks: []Block{{Type: BlockType("future_block"), Text: "hi"}}}
	line, _ := json.Marshal(unknown)
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{ this is not valid json\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":6,"role":"assistant"`); err != nil { // no closing brace, no newline
		t.Fatal(err)
	}
	f.Close()

	r, _ := Open(path)
	got, err := r.Window(0, 0)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	// 3 valid + the unknown-block entry (still valid JSON) = 4; garbage and
	// partial lines dropped.
	if len(got) != 4 {
		t.Fatalf("read %d entries, want 4", len(got))
	}
	last := got[3]
	if len(last.Blocks) != 1 || last.Blocks[0].Type != BlockType("future_block") {
		t.Errorf("unknown block not preserved: %+v", last.Blocks)
	}
}

func TestWindowAndTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	var entries []Entry
	for i := 0; i < 10; i++ {
		entries = append(entries, Entry{Role: RoleAssistant,
			Blocks: []Block{{Type: BlockText, Text: string(rune('a' + i))}}})
	}
	writeAll(t, path, entries)

	r, _ := Open(path)

	if n, err := r.Count(); err != nil || n != 10 {
		t.Fatalf("Count = %d, %v; want 10", n, err)
	}

	win, err := r.Window(3, 4)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if len(win) != 4 || win[0].Seq != 4 || win[3].Seq != 7 {
		t.Errorf("Window(3,4) seqs = %v", seqs(win))
	}

	// Offset past the end is empty, not an error.
	if past, err := r.Window(50, 5); err != nil || len(past) != 0 {
		t.Errorf("Window past end = %v, %v", seqs(past), err)
	}

	tail, err := r.Tail(3)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(tail) != 3 || tail[0].Seq != 8 || tail[2].Seq != 10 {
		t.Errorf("Tail(3) seqs = %v", seqs(tail))
	}

	// Tail larger than the file returns everything.
	if all, _ := r.Tail(100); len(all) != 10 {
		t.Errorf("Tail(100) len = %d, want 10", len(all))
	}
}

func TestSeqResumesOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeAll(t, path, sampleEntries()) // seqs 1..3

	// Reopen (simulating a retry / next loop iteration) and append more.
	w, err := Create(path)
	if err != nil {
		t.Fatalf("reopen Create: %v", err)
	}
	seq, err := w.Append(Entry{Iteration: 1, Attempt: 1, Role: RoleAssistant,
		Blocks: []Block{{Type: BlockText, Text: "resumed"}}})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if seq != 4 {
		t.Errorf("resumed seq = %d, want 4", seq)
	}
	w.Close()

	r, _ := Open(path)
	if n, _ := r.Count(); n != 4 {
		t.Errorf("Count after reopen = %d, want 4", n)
	}
}

// TestReaderToleratesUnknownFields is the best-effort-render smoke test: a line
// carrying extra top-level fields and a new block type with new fields — the
// sort of thing a different jig build might write — must be read without
// panicking. Unknown fields are dropped by encoding/json; the unknown block
// type is preserved verbatim for the caller to render as a placeholder. The
// format is unversioned by design (see the package doc), so this is graceful
// degradation, not a compatibility guarantee.
func TestReaderToleratesUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	line := `{"seq":1,"ts":"2027-01-01T00:00:00Z","iter":0,"attempt":0,` +
		`"role":"assistant","cost_usd":0.42,"blocks":[` +
		`{"type":"text","text":"hello"},` +
		`{"type":"image","url":"https://x/y.png","alt":"a picture"}` +
		`],"trailing_unknown_field":{"nested":true}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	r, _ := Open(path)
	got, err := r.Window(0, 0)
	if err != nil {
		t.Fatalf("Window: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Role != RoleAssistant || len(e.Blocks) != 2 {
		t.Fatalf("entry not decoded: role=%q blocks=%d", e.Role, len(e.Blocks))
	}
	if e.Blocks[0].Type != BlockText || e.Blocks[0].Text != "hello" {
		t.Errorf("known block not decoded: %+v", e.Blocks[0])
	}
	if e.Blocks[1].Type != BlockType("image") {
		t.Errorf("unknown block type not preserved: %+v", e.Blocks[1])
	}
}

func TestReadMissingFileIsEmpty(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if n, err := r.Count(); err != nil || n != 0 {
		t.Errorf("Count on missing file = %d, %v; want 0, nil", n, err)
	}
}

func seqs(entries []Entry) []int {
	out := make([]int, len(entries))
	for i, e := range entries {
		out[i] = e.Seq
	}
	return out
}
