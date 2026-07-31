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
// V, Seq, and Ts fields are overwritten (the writer owns them); Iteration,
// Attempt, Role, and Blocks are taken as given. Oversized block text/content is
// truncated in place.
func (w *Writer) Append(e Entry) (int, error) {
	w.seq++
	e.V = SchemaVersion
	e.Seq = w.seq
	e.Ts = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)

	cap := w.MaxBlockBytes
	if cap <= 0 {
		cap = DefaultMaxBlockBytes
	}
	for i := range e.Blocks {
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

// clampBlock truncates the sizeable field of a block to at most max bytes,
// backing off to a UTF-8 rune boundary, and marks it truncated. thinking and
// text share Text; tool_result uses Content. Other fields (tool input) are
// bounded by their producers and left alone.
func clampBlock(b *Block, max int) {
	switch b.Type {
	case BlockText, BlockThinking:
		if len(b.Text) > max {
			b.Text = clampString(b.Text, max)
			b.Truncated = true
		}
	case BlockToolResult:
		if len(b.Content) > max {
			b.Content = clampString(b.Content, max)
			b.Truncated = true
		}
	}
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
