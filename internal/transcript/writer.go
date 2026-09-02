package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"jig/internal/toolcall"
)

// DefaultMaxBlockBytes is the write-time hard cap applied to a block's text,
// thinking, or tool-result content. A block over the cap is stored truncated
// with Truncated=true. This protects the write loop and disk from pathological
// tool output; it is separate from the TUI's 80-char render collapse. 256 KiB
// comfortably holds normal agent turns and large file reads while bounding the
// worst case.
const DefaultMaxBlockBytes = 256 * 1024

// Writer appends entries to a per-step transcript.jsonl. It is not safe for
// concurrent use; a single step is driven by one runner goroutine. Writes are
// buffered and flushed per Append so a concurrent reader always sees whole
// lines.
type Writer struct {
	f   *os.File
	bw  *bufio.Writer
	seq int // last assigned seq; next Append uses seq+1

	// MaxBlockBytes caps text/thinking/content per block. Initialized from
	// DefaultMaxBlockBytes; callers may override before the first Append.
	MaxBlockBytes int
}

// Create opens (creating if necessary) the transcript at path for appending.
// The sequence counter resumes from the number of lines already present so a
// retry or a later loop iteration continues the monotonic seq rather than
// restarting at 1.
func Create(path string) (*Writer, error) {
	// Count existing lines first so seq resumes correctly across reopen. We do
	// this before opening for append to avoid interleaving reads and writes on
	// the same handle.
	existing, err := countLines(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("transcript: open %q: %w", path, err)
	}
	return &Writer{
		f:             f,
		bw:            bufio.NewWriter(f),
		seq:           existing,
		MaxBlockBytes: DefaultMaxBlockBytes,
	}, nil
}

// Append stamps and writes one entry, returning its assigned seq. The caller's
// Seq and Ts fields are overwritten (the writer owns them); Iteration, Attempt,
// Role, and Blocks are taken as given. Oversized block text/content is
// truncated in place.
func (w *Writer) Append(e Entry) (int, error) {
	w.seq++
	e.Seq = w.seq
	e.Ts = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)

	cap := w.MaxBlockBytes
	if cap <= 0 {
		cap = DefaultMaxBlockBytes
	}
	for i := range e.Blocks {
		normalizeLegacyBlock(&e.Blocks[i])
		clampBlock(&e.Blocks[i], cap)
	}

	line, err := json.Marshal(e)
	if err != nil {
		w.seq-- // failed write leaves the sequence unconsumed
		return 0, fmt.Errorf("transcript: marshal entry: %w", err)
	}
	if _, err := w.bw.Write(append(line, '\n')); err != nil {
		return 0, fmt.Errorf("transcript: write entry: %w", err)
	}
	if err := w.bw.Flush(); err != nil {
		return 0, fmt.Errorf("transcript: flush: %w", err)
	}
	return e.Seq, nil
}

// Close flushes and closes the underlying file.
func (w *Writer) Close() error {
	if w.bw != nil {
		if err := w.bw.Flush(); err != nil {
			w.f.Close()
			return err
		}
	}
	return w.f.Close()
}

// clampBlock bounds every persisted tool value as well as text blocks. A tool
// receives an aggregate cap so a many-file edit cannot sidestep the per-value
// limit by spreading a payload across content items.
func clampBlock(b *Block, max int) {
	switch b.Type {
	case BlockText, BlockThinking:
		if len(b.Text) > max {
			b.Text = clampString(b.Text, max)
			b.Truncated = true
		}
	case BlockToolUse, BlockToolResult:
		if clampActivity(b.Tool, max) {
			b.Truncated = true
		}
	}
}

func clampActivity(a *toolcall.Activity, max int) bool {
	if a == nil {
		return false
	}
	// Individual values get a quarter of the block limit. The final aggregate
	// pass enforces the full cap across all strings and raw JSON values.
	perValue := max / 4
	if perValue < 1 {
		perValue = 1
	}
	truncated := false
	clamp := func(s *string, limit int) {
		if len(*s) > limit {
			*s = clampString(*s, limit)
			truncated = true
		}
	}
	clamp(&a.Title, perValue)
	clamp(&a.Kind, perValue)
	clamp(&a.Status, perValue)
	clampRaw := func(raw *json.RawMessage, limit int) {
		if len(*raw) > limit {
			*raw = json.RawMessage(clampString(string(*raw), limit))
			truncated = true
		}
	}
	clampRaw(&a.Input, perValue)
	clampRaw(&a.Output, perValue)
	for i := range a.Locations {
		clamp(&a.Locations[i].Path, perValue)
	}
	for i := range a.Content {
		c := &a.Content[i]
		clamp(&c.Type, perValue)
		clamp(&c.Text, perValue)
		clampRaw(&c.Raw, perValue)
		if c.Diff != nil {
			clamp(&c.Diff.Path, perValue)
			clamp(&c.Diff.NewText, perValue)
			if c.Diff.OldText != nil {
				clamp(c.Diff.OldText, perValue)
			}
		}
	}
	remaining := max
	consume := func(s *string) {
		if len(*s) > remaining {
			limit := remaining
			if limit < 0 {
				limit = 0
			}
			*s = clampString(*s, limit)
			truncated = true
		}
		remaining -= len(*s)
		if remaining < 0 {
			remaining = 0
		}
	}
	consume(&a.Title)
	consume(&a.Kind)
	consume(&a.Status)
	consumeRaw := func(raw *json.RawMessage) { s := string(*raw); consume(&s); *raw = json.RawMessage(s) }
	consumeRaw(&a.Input)
	consumeRaw(&a.Output)
	for i := range a.Locations {
		consume(&a.Locations[i].Path)
	}
	for i := range a.Content {
		c := &a.Content[i]
		consume(&c.Type)
		consume(&c.Text)
		consumeRaw(&c.Raw)
		if c.Diff != nil {
			consume(&c.Diff.Path)
			if c.Diff.OldText != nil {
				consume(c.Diff.OldText)
			}
			consume(&c.Diff.NewText)
		}
	}
	return truncated
}

// clampString returns the longest prefix of s that is at most max bytes and
// ends on a rune boundary.
func clampString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Back off to a rune boundary so we never emit half a multibyte rune.
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// countLines returns the number of newline-terminated lines in the file at
// path, or 0 if the file does not exist. Only complete lines are counted (we
// count '\n' bytes), so a trailing partial line — the fingerprint of a crash
// mid-write — does not advance the resumed seq, keeping it aligned with what a
// reader will successfully parse.
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("transcript: count lines %q: %w", path, err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	buf := make([]byte, 64*1024)
	n := 0
	for {
		c, err := br.Read(buf)
		n += bytes.Count(buf[:c], []byte{'\n'})
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return 0, fmt.Errorf("transcript: count lines %q: %w", path, err)
		}
	}
}
