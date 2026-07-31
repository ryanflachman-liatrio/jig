package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Reader provides bounded, repeatable reads of a transcript file. It holds no
// open file handle: each method opens the file, reads to the current EOF, and
// closes. That makes it safe to call while a Writer concurrently appends — a
// read simply sees whatever whole lines exist at that instant, and a later read
// picks up newly-appended entries.
type Reader struct {
	path string
}

// Open returns a Reader for the transcript at path. The file need not exist yet
// (a reader may open before the writer creates it); read methods treat a
// missing file as an empty transcript.
func Open(path string) (*Reader, error) {
	if path == "" {
		return nil, fmt.Errorf("transcript: empty reader path")
	}
	return &Reader{path: path}, nil
}

// Count returns the number of well-formed entries currently in the file.
// Malformed lines are not counted, keeping the count aligned with the index
// space Window and Tail operate over.
func (r *Reader) Count() (int, error) {
	entries, err := r.readAll()
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// Window returns up to limit entries starting at offset (0-based) in file
// order. An offset past the end yields an empty slice, not an error; a limit of
// zero or less means "no cap" (all entries from offset onward).
func (r *Reader) Window(offset, limit int) ([]Entry, error) {
	entries, err := r.readAll()
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(entries) {
		return nil, nil
	}
	end := len(entries)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return entries[offset:end], nil
}

// Tail returns the last n entries in file order. n<=0 yields an empty slice.
func (r *Reader) Tail(n int) ([]Entry, error) {
	if n <= 0 {
		return nil, nil
	}
	entries, err := r.readAll()
	if err != nil {
		return nil, err
	}
	if n >= len(entries) {
		return entries, nil
	}
	return entries[len(entries)-n:], nil
}

// readAll opens the file, parses every well-formed line into an Entry, and
// returns them in file order. Malformed lines (including a partial trailing
// line from a crash mid-write) are skipped without erroring; unknown block
// types are preserved verbatim for the caller to render as it sees fit. A
// missing file is an empty transcript, not an error.
func (r *Reader) readAll() ([]Entry, error) {
	f, err := os.Open(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("transcript: open %q: %w", r.path, err)
	}
	defer f.Close()

	// ReadString has no token-size ceiling, so a large tool_result line (up to
	// MaxBlockBytes) never trips a scanner limit.
	br := bufio.NewReader(f)
	var entries []Entry
	for {
		line, readErr := br.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			var e Entry
			if json.Unmarshal([]byte(trimmed), &e) == nil {
				entries = append(entries, e)
			}
			// A line that fails to parse is silently skipped: it is either
			// corrupt or a partial trailing write.
		}
		if readErr == io.EOF {
			return entries, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("transcript: read %q: %w", r.path, readErr)
		}
	}
}
