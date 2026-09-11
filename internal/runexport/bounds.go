package runexport

import (
	"bufio"
	"errors"
	"io"
)

// Resource bounds fixed by spec FR-16. None of these has an override flag or
// environment variable in this version.
const (
	maxInputRecord   = 4 << 20   // 4 MiB: largest single journal/transcript line
	maxTotalInput    = 256 << 20 // 256 MiB: cumulative bytes read across evidence discovery
	maxRetainedText  = 64 << 10  // 64 KiB: retained text value after sanitization
	maxArchiveBytes  = 256 << 20 // 256 MiB: total uncompressed archive member content
	maxStepInventory = 10000     // step directories inspected
)

var (
	errLineOversized  = errors.New("runexport: line exceeds the maximum record size")
	errLineTorn       = errors.New("runexport: final line has no trailing newline")
	errBudgetExceeded = errors.New("runexport: total input budget exceeded")
)

// budget is a shared cumulative byte counter across every confined read
// performed while collecting one export (journal, workflow snapshot, and
// every step transcript). Exceeding it aborts the export with an operational
// error rather than continuing to read (spec FR-16).
type budget struct {
	remaining int64
}

func newBudget(total int64) *budget { return &budget{remaining: total} }

func (b *budget) spend(n int64) error {
	if n > b.remaining {
		b.remaining = 0
		return errBudgetExceeded
	}
	b.remaining -= n
	return nil
}

// boundedReader wraps an io.Reader so every read it satisfies is charged
// against a shared budget, without buffering the source into memory.
type boundedReader struct {
	r io.Reader
	b *budget
}

func (br *boundedReader) Read(p []byte) (int, error) {
	n, err := br.r.Read(p)
	if n > 0 {
		if spendErr := br.b.spend(int64(n)); spendErr != nil {
			return n, spendErr
		}
	}
	return n, err
}

// readBoundedLine reads one newline-terminated record from br, enforcing max
// as the largest acceptable record. It distinguishes:
//   - a normal complete line (trailing \r\n or \n stripped), err == nil
//   - io.EOF with no data: clean end of input
//   - errLineTorn: EOF reached with a non-empty line lacking a trailing
//     newline (a torn final record)
//   - errLineOversized: the line (complete or torn) exceeds max; the reader
//     is resynchronized to the next newline (or EOF) before returning so the
//     caller can continue past it
func readBoundedLine(br *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	oversized := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !oversized {
			buf = append(buf, chunk...)
			if len(buf) > max {
				oversized = true
				buf = nil
			}
		}
		switch {
		case err == nil:
			if oversized {
				return nil, errLineOversized
			}
			n := len(buf)
			for n > 0 && (buf[n-1] == '\n' || buf[n-1] == '\r') {
				n--
			}
			return buf[:n], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if oversized {
				return nil, errLineOversized
			}
			if len(buf) == 0 {
				return nil, io.EOF
			}
			return nil, errLineTorn
		default:
			return nil, err
		}
	}
}
