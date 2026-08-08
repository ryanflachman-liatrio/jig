package sentinel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Writer appends findings to a per-run findings.jsonl. It is not safe for
// concurrent use; a single sentinel goroutine drives it.
// Writes are flushed per Append so a concurrent reader always sees whole lines,
// matching the transcript.Writer contract.
// A zero-value Writer (or one with path == "") is a silent no-op — the
// persistence-off path works without any nil checks at call sites.
type Writer struct {
	path string
	f    *os.File
	bw   *bufio.Writer
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
	if w.f == nil {
		return nil
	}
	b, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("sentinel: marshal finding: %w", err)
	}
	if _, err := w.bw.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("sentinel: write finding: %w", err)
	}
	return w.bw.Flush()
}

// Close flushes and closes the underlying file.
func (w *Writer) Close() error {
	if w.f == nil {
		return nil
	}
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
