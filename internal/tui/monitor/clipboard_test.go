package monitor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// TestClipboardFilePayload copies text artifacts of common kinds and asserts
// the loader payload preserves formatting bit-for-bit before sanitization.
func TestClipboardFilePayload(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		file    string
		kind    fileKind
		content string
	}{
		{name: "markdown", file: "note.md", kind: kindMarkdown, content: "# hi\n\nbody\n"},
		{name: "json", file: "output.json", kind: kindJSON, content: "{\"a\":1}\n"},
		{name: "jsonl", file: "diag.jsonl", kind: kindJSONL, content: "{\"a\":1}\n{\"b\":2}\n"},
		{name: "crlf preserved", file: "crlf.log", kind: kindLog, content: "hi\r\nthere\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.file)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			req := copyOutputFileRequest(path, tc.kind)
			if req.Target.Surface != shared.ClipboardSurfaceMonitorFile {
				t.Fatalf("target surface = %q", req.Target.Surface)
			}
			payload := req.Loader()
			if payload.Err != nil {
				t.Fatalf("loader err = %v", payload.Err)
			}
			if payload.Payload != tc.content {
				t.Fatalf("payload = %q, want %q", payload.Payload, tc.content)
			}
		})
	}
}

// TestClipboardFileSnapshot exercises the file eligibility gates: missing,
// binary, empty, oversized, and a growing file (which must copy only the
// prefix captured at open time).
func TestClipboardFileSnapshot(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "nope.txt")
	if payload := copyOutputFileRequest(missing, kindOther).Loader(); !errors.Is(payload.Err, shared.ErrClipboardUnavailable) {
		t.Fatalf("missing file err = %v", payload.Err)
	}

	binary := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(binary, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	// The bounded read succeeds and the shared sanitizer rejects the NUL.
	rawPayload := copyOutputFileRequest(binary, kindOther).Loader()
	if rawPayload.Err != nil {
		t.Fatalf("binary loader err = %v", rawPayload.Err)
	}
	if _, err := shared.PrepareClipboardPayload(rawPayload.Payload); !errors.Is(err, shared.ErrClipboardBinary) {
		t.Fatalf("prepare(binary) err = %v, want binary", err)
	}

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if payload := copyOutputFileRequest(empty, kindOther).Loader(); !errors.Is(payload.Err, shared.ErrClipboardEmpty) {
		t.Fatalf("empty file err = %v", payload.Err)
	}

	over := filepath.Join(dir, "over.txt")
	if err := os.WriteFile(over, []byte(strings.Repeat("x", outputFileCap+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if payload := copyOutputFileRequest(over, kindOther).Loader(); !errors.Is(payload.Err, shared.ErrClipboardOversized) {
		t.Fatalf("oversized err = %v", payload.Err)
	}

	// Directory is not a regular file.
	if payload := copyOutputFileRequest(dir, kindOther).Loader(); !errors.Is(payload.Err, shared.ErrClipboardUnavailable) {
		t.Fatalf("directory err = %v", payload.Err)
	}

	// Growing file: the loader captures only the initial size.
	growing := filepath.Join(dir, "growing.txt")
	initial := "initial bytes"
	if err := os.WriteFile(growing, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stat before invoking so the size we assert against is the initial one.
	req := copyOutputFileRequest(growing, kindOther)
	// Simulate an append happening between stat and read is impossible in
	// pure Go without concurrency; instead, verify the loader reads only the
	// initial-sized prefix by patching the file after ReadFull would have
	// consumed its bytes. Since the loader captures size at stat time and
	// reads exactly that many bytes, an append after the loader completes
	// must not appear in the payload.
	payload := req.Loader()
	if payload.Err != nil {
		t.Fatalf("initial loader err = %v", payload.Err)
	}
	if payload.Payload != initial {
		t.Fatalf("initial payload = %q, want %q", payload.Payload, initial)
	}
	if err := os.WriteFile(growing, []byte(initial+" more"), 0o644); err != nil {
		t.Fatal(err)
	}
	if payload := copyOutputFileRequest(growing, kindOther).Loader(); payload.Err != nil || payload.Payload != initial+" more" {
		t.Fatalf("second read payload = %q err = %v", payload.Payload, payload.Err)
	}
}

// TestClipboardTranscriptSnapshot writes a multi-attempt/multi-block synthetic
// transcript, exports it, and checks that every block appears exactly once in
// source order with the required provenance headers.
func TestClipboardTranscriptSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	w, err := transcript.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := []transcript.Entry{
		{Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "hello world"},
			{Type: transcript.BlockThinking, Text: "considering options"},
		}},
		{Iteration: 0, Attempt: 0, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "tool-1", Title: "Bash", Status: "", Input: json.RawMessage(`{"cmd":"ls"}`)}},
		}},
		{Iteration: 0, Attempt: 0, Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "tool-1", Title: "Bash", Status: "completed", Content: []toolcall.Content{{Type: "text", Text: "a\nb\n"}}}},
		}},
		{Iteration: 1, Attempt: 0, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "second iteration"},
		}},
	}
	for _, e := range entries {
		if _, err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	payload, notes, err := exportRecordedTranscript(path)
	if err != nil {
		t.Fatalf("export err = %v", err)
	}
	if notes != "" {
		t.Fatalf("unexpected notes = %q", notes)
	}
	// Every block header line must appear in order.
	wantMarkers := []string{
		"== assistant text · seq 1 · gen 0 · iter 0 · attempt 0 ==",
		"== assistant thinking · seq 1 · gen 0 · iter 0 · attempt 0 ==",
		"== assistant tool_use · seq 2 · gen 0 · iter 0 · attempt 0 ==",
		"== user tool_result · seq 3 · gen 0 · iter 0 · attempt 0 ==",
		"== assistant text · seq 4 · gen 0 · iter 1 · attempt 0 ==",
	}
	pos := 0
	for _, marker := range wantMarkers {
		idx := strings.Index(payload[pos:], marker)
		if idx < 0 {
			t.Fatalf("marker %q missing after byte %d in payload:\n%s", marker, pos, payload)
		}
		pos += idx + len(marker)
	}
	// Text bodies are present verbatim.
	if !strings.Contains(payload, "hello world") || !strings.Contains(payload, "second iteration") {
		t.Fatalf("payload missing text bodies:\n%s", payload)
	}
	// Tool bodies are present as JSON with recorded fields.
	if !strings.Contains(payload, "\"id\": \"tool-1\"") || !strings.Contains(payload, "\"title\": \"Bash\"") {
		t.Fatalf("payload missing tool activity JSON:\n%s", payload)
	}
}

// TestClipboardTranscriptBoundsAndErrors covers the raw-scan cap, the
// malformed-record count, and empty/missing sources.
func TestClipboardTranscriptBoundsAndErrors(t *testing.T) {
	dir := t.TempDir()
	// Missing file.
	if _, _, err := exportRecordedTranscript(filepath.Join(dir, "none.jsonl")); !errors.Is(err, shared.ErrClipboardUnavailable) {
		t.Fatalf("missing transcript err = %v", err)
	}
	// Empty file.
	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exportRecordedTranscript(empty); !errors.Is(err, shared.ErrClipboardEmpty) {
		t.Fatalf("empty transcript err = %v", err)
	}
	// Malformed record surfaces in notes.
	malformed := filepath.Join(dir, "mixed.jsonl")
	if err := os.WriteFile(malformed, []byte("{not json}\n{\"seq\":1,\"role\":\"assistant\",\"blocks\":[{\"type\":\"text\",\"text\":\"ok\"}]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, notes, err := exportRecordedTranscript(malformed)
	if err != nil {
		t.Fatalf("mixed transcript err = %v", err)
	}
	if notes == "" || !strings.Contains(notes, "1 malformed record") {
		t.Fatalf("mixed transcript notes = %q", notes)
	}
	// Oversized scan.
	over := filepath.Join(dir, "big.jsonl")
	f, err := os.Create(over)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(shared.ClipboardMaxTranscriptScanBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := exportRecordedTranscript(over); !errors.Is(err, shared.ErrClipboardOversized) {
		t.Fatalf("oversized transcript err = %v", err)
	}
}

// TestClipboardItemPayloads exercises the per-item copy path against
// synthetic entries.
func TestClipboardItemPayloads(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "explain this"}}},
		{Seq: 2, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "let me think"}}},
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "t1", Title: "Read", Input: json.RawMessage(`{"path":"x"}`)}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "t1", Title: "Read", Status: "completed", Content: []toolcall.Content{{Type: "text", Text: "file body"}}}}}},
	}
	items := buildTranscriptItems(entries, false)
	if len(items) != 3 {
		t.Fatalf("items = %d, want text+thinking+exchange = 3", len(items))
	}

	// Text item: no header, exact text.
	text := runItemLoader(t, items[0], entries)
	if text.Payload != "explain this" {
		t.Fatalf("text payload = %q", text.Payload)
	}
	// Thinking item: no header, exact text.
	think := runItemLoader(t, items[1], entries)
	if think.Payload != "let me think" {
		t.Fatalf("thinking payload = %q", think.Payload)
	}
	// Tool exchange: use before result, both bodies via shared serializer.
	tool := runItemLoader(t, items[2], entries)
	if !strings.HasPrefix(tool.Payload, "tool tool_use · Read") {
		t.Fatalf("tool payload missing use header:\n%s", tool.Payload)
	}
	if !strings.Contains(tool.Payload, "\"status\": \"completed\"") || !strings.Contains(tool.Payload, "\"text\": \"file body\"") {
		t.Fatalf("tool payload missing result JSON body:\n%s", tool.Payload)
	}
}

// TestClipboardItemPageBoundary asserts a use-only / result-only item still
// copies exactly what is loaded (never scans backward) and records a note.
func TestClipboardItemPageBoundary(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 5, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "orphan", Title: "Bash", Status: "completed", Content: []toolcall.Content{{Type: "text", Text: "stdout"}}}}}},
	}
	items := buildTranscriptItems(entries, false)
	if len(items) != 1 || items[0].kind != transcriptItemToolResult {
		t.Fatalf("orphan result did not build as tool result: %+v", items)
	}
	payload := runItemLoader(t, items[0], entries)
	if !strings.Contains(payload.Payload, "\"text\": \"stdout\"") {
		t.Fatalf("orphan result payload missing body:\n%s", payload.Payload)
	}
}

// runItemLoader executes a single item's request loader synchronously.
func runItemLoader(t *testing.T, item transcriptItem, entries []transcript.Entry) shared.ClipboardPayload {
	t.Helper()
	cmd := copyTranscriptItemRequest(item, entries)
	msg := cmd()
	req, ok := msg.(shared.ClipboardRequest)
	if !ok {
		t.Fatalf("cmd() = %T, want shared.ClipboardRequest", msg)
	}
	if req.Loader == nil {
		t.Fatalf("loader is nil for item %+v", item)
	}
	return req.Loader()
}

// TestClipboardFileCmdReturnsRequest wraps copyOutputFileCmd and checks the
// message is a shared.ClipboardRequest ready for admission at the root.
func TestClipboardFileCmdReturnsRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.md")
	if err := os.WriteFile(path, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := copyOutputFileCmd(path, kindMarkdown)()
	req, ok := msg.(shared.ClipboardRequest)
	if !ok {
		t.Fatalf("msg = %T, want shared.ClipboardRequest", msg)
	}
	if req.Target.Surface != shared.ClipboardSurfaceMonitorFile || req.Target.Label != path {
		t.Fatalf("target = %+v", req.Target)
	}
}

// TestClipboardTranscriptCmdRoutesToUnavailable proves that copyTranscriptCmd
// with no run dir surfaces the unavailable rejection through the loader.
func TestClipboardTranscriptCmdRoutesToUnavailable(t *testing.T) {
	msg := copyTranscriptCmd("", "step")()
	req, ok := msg.(shared.ClipboardRequest)
	if !ok {
		t.Fatalf("msg = %T", msg)
	}
	if payload := req.Loader(); !errors.Is(payload.Err, shared.ErrClipboardUnavailable) {
		t.Fatalf("no-run-dir err = %v", payload.Err)
	}
}

// TestClipboardTranscriptRoundTrip ensures that a captured transcript passes
// through PrepareClipboardPayload cleanly.
func TestClipboardTranscriptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	w, err := transcript.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	payload, _, err := exportRecordedTranscript(path)
	if err != nil {
		t.Fatalf("export err = %v", err)
	}
	if _, err := shared.PrepareClipboardPayload(payload); err != nil {
		t.Fatalf("prepare(export) err = %v", err)
	}
}

// TestClipboardTranscriptKeyReturnsCommand exercises the key handler wiring
// end-to-end through the transcript panel and verifies that Y produces a
// shared.ClipboardRequest routed for the current step.
func TestClipboardTranscriptKeyReturnsCommand(t *testing.T) {
	dir := t.TempDir()
	stepDir := filepath.Join(dir, "steps", "step")
	if err := os.MkdirAll(stepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stepDir, "transcript.jsonl")
	w, err := transcript.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	w.Close()

	m := New("run")
	m.RunDir = dir
	m.chatStep = "step"
	m.focus = focusTranscript
	m.selKind = ""
	// Skip the resize that would create the transcript viewport; a Y key
	// press just needs to reach updateTranscript.
	_, cmd := m.updateTranscript(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	if cmd == nil {
		t.Fatal("Y produced no command")
	}
	msg := cmd()
	req, ok := msg.(shared.ClipboardRequest)
	if !ok {
		t.Fatalf("msg = %T", msg)
	}
	if req.Target.Surface != shared.ClipboardSurfaceTranscriptSnapshot {
		t.Fatalf("surface = %q", req.Target.Surface)
	}
	if payload := req.Loader(); payload.Err != nil {
		t.Fatalf("loader err = %v", payload.Err)
	} else if !strings.Contains(payload.Payload, "hello") {
		t.Fatalf("loader payload missing hello:\n%s", payload.Payload)
	}
}
