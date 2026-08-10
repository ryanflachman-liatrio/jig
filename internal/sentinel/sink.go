package sentinel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Writer appends findings to a per-run findings.jsonl. It is safe for concurrent
// use: the Tier-2 supervisor appends from its own goroutine while the run may
// close the writer during teardown, so Append and Close are serialised by mu.
// Writes are flushed per Append so a concurrent reader always sees whole lines,
// matching the transcript.Writer contract.
// A zero-value Writer (or one with path == "") is a silent no-op — the
// persistence-off path works without any nil checks at call sites.
type Writer struct {
	path string
	mu   sync.Mutex // guards bw/f access and the closed flag
	f    *os.File
	bw   *bufio.Writer
	// closed makes Close idempotent and turns post-Close Append into a no-op
	// rather than a write to a closed descriptor (the writer may be torn down
	// while a producer goroutine still holds a reference).
	closed bool
}

// NewWriter opens (creating if necessary) findings.jsonl at path for appending.
// Pass "" to get a silent no-op writer (persistence off).
func NewWriter(path string) (*Writer, error) {
	if path == "" {
		return &Writer{}, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("sentinel: open findings %q: %w", path, err)
	}
	return &Writer{path: path, f: f, bw: bufio.NewWriter(f)}, nil
}

// Append serialises f as a JSONL line and flushes immediately so a concurrent
// reader always sees whole lines. No-ops when the writer has no open file.
func (w *Writer) Append(f Finding) error {
	b, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("sentinel: marshal finding: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil || w.closed {
		return nil // persistence off, or the writer was already torn down
	}
	if _, err := w.bw.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("sentinel: write finding: %w", err)
	}
	return w.bw.Flush()
}

// Close flushes and closes the underlying file. It is idempotent: a second Close
// (or a Close racing an in-flight Append) is a no-op.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil || w.closed {
		return nil
	}
	w.closed = true
	if err := w.bw.Flush(); err != nil {
		_ = w.f.Close()
		return err
	}
	return w.f.Close()
}

// ReadAll reads all findings from path, returning them in append order.
// Returns an empty slice (not an error) when path is "" or the file does not exist.
func ReadAll(path string) ([]Finding, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sentinel: open findings %q: %w", path, err)
	}
	defer f.Close()

	var findings []Finding
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var finding Finding
		if err := json.Unmarshal(line, &finding); err != nil {
			return nil, fmt.Errorf("sentinel: decode finding line: %w", err)
		}
		findings = append(findings, finding)
	}
	return findings, sc.Err()
}
