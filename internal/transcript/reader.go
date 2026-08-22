package transcript

import (
	"bufio"
	"bytes"
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

// Page is a bounded transcript slice. Start and End are opaque byte cursors;
// callers may pass Start to PageBefore to walk toward the beginning without
// retaining an index proportional to the transcript.
type Page struct {
	Entries    []Entry
	Start      int64
	End        int64
	HasEarlier bool
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

// TailPage returns the newest limit well-formed entries using memory
// proportional to the page rather than the transcript.
func (r *Reader) TailPage(limit int) (Page, error) {
	return r.PageBefore(-1, limit)
}

// PageBefore returns up to limit well-formed entries ending before the opaque
// byte cursor end. A negative end means the current EOF. Appends do not
// invalidate cursors because transcript files are append-only.
func (r *Reader) PageBefore(end int64, limit int) (Page, error) {
	if limit <= 0 {
		return Page{}, nil
	}
	f, err := os.Open(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Page{}, nil
		}
		return Page{}, fmt.Errorf("transcript: open %q: %w", r.path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Page{}, fmt.Errorf("transcript: stat %q: %w", r.path, err)
	}
	size := info.Size()
	if end < 0 || end > size {
		end = size
	}
	if end < 0 {
		end = 0
	}

	page := Page{Start: end, End: end}
	cursor := end
	for cursor > 0 && len(page.Entries) < limit {
		line, start, readErr := previousLine(f, cursor)
		if readErr != nil {
			return Page{}, fmt.Errorf("transcript: read page %q: %w", r.path, readErr)
		}
		cursor = start
		page.Start = start
		if e, ok := decodeEntry(line); ok {
			page.Entries = append(page.Entries, e)
		}
	}
	reverseEntries(page.Entries)

	hasEarlier, err := hasEntryBefore(f, page.Start)
	if err != nil {
		return Page{}, fmt.Errorf("transcript: inspect page %q: %w", r.path, err)
	}
	page.HasEarlier = hasEarlier
	return page, nil
}

const reverseReadChunk = 64 * 1024

func previousLine(f *os.File, end int64) ([]byte, int64, error) {
	if end <= 0 {
		return nil, 0, io.EOF
	}

	lineEnd := end
	var last [1]byte
	if _, err := f.ReadAt(last[:], end-1); err != nil {
		return nil, 0, err
	}
	if last[0] == '\n' {
		lineEnd--
	}
	if lineEnd == 0 {
		return nil, 0, nil
	}

	pos := lineEnd
	var reverseParts [][]byte
	for pos > 0 {
		start := max(int64(0), pos-reverseReadChunk)
		buf := make([]byte, pos-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return nil, 0, err
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			reverseParts = append(reverseParts, append([]byte(nil), buf[i+1:]...))
			lineStart := start + int64(i) + 1
			return joinReverseParts(reverseParts), lineStart, nil
		}
		reverseParts = append(reverseParts, buf)
		pos = start
	}
	return joinReverseParts(reverseParts), 0, nil
}

func joinReverseParts(parts [][]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	line := make([]byte, 0, total)
	for i := len(parts) - 1; i >= 0; i-- {
		line = append(line, parts[i]...)
	}
	return line
}

func decodeEntry(line []byte) (Entry, bool) {
	var e Entry
	if strings.TrimSpace(string(line)) == "" || json.Unmarshal(bytes.TrimSpace(line), &e) != nil {
		return Entry{}, false
	}
	return e, true
}

func hasEntryBefore(f *os.File, end int64) (bool, error) {
	cursor := end
	for cursor > 0 {
		line, start, err := previousLine(f, cursor)
		if err != nil {
			return false, err
		}
		cursor = start
		if _, ok := decodeEntry(line); ok {
			return true, nil
		}
	}
	return false, nil
}

func reverseEntries(entries []Entry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
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
