// Package shared: clipboard.go implements the cross-surface OSC52 copy
// contract described in docs/specs/23-spec-clipboard-yank.
//
// The primary types are ClipboardRequest and ClipboardResult, plus the
// PrepareClipboardPayload sanitizer that every surface routes text through
// before the root emits an OSC52 command. Nothing here reads the clipboard,
// invokes local clipboard helpers, or logs source content.
package shared

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// ClipboardMaxPayloadBytes is the MVP maximum size, in bytes, of the sanitized
// text emitted through OSC52 for one copy request. Aligned with the existing
// Monitor file-read cap (see internal/tui/monitor/outputfiles.go). The check is
// inclusive: exactly-the-limit succeeds; a single byte more is rejected.
const ClipboardMaxPayloadBytes = 256 * 1024

// ClipboardMaxTranscriptScanBytes caps the raw JSONL bytes a whole-step
// transcript export may scan for one copy request. Independent from
// ClipboardMaxPayloadBytes because many records yield little copied text.
const ClipboardMaxTranscriptScanBytes = 8 * 1024 * 1024

// ClipboardSurface identifies the visible content surface a copy targeted, for
// nonmodal notice text. Surface values are user-facing labels; do not rename
// without also updating documentation (docs/clipboard.md) and matching tests.
type ClipboardSurface string

const (
	ClipboardSurfaceRunID              ClipboardSurface = "run ID"
	ClipboardSurfaceMonitorFile        ClipboardSurface = "output file"
	ClipboardSurfaceTranscriptItem     ClipboardSurface = "transcript item"
	ClipboardSurfaceTranscriptSnapshot ClipboardSurface = "recorded transcript"
	ClipboardSurfaceReviewLine         ClipboardSurface = "line"
	ClipboardSurfaceReviewRange        ClipboardSurface = "range"
	ClipboardSurfaceReviewBlock        ClipboardSurface = "block"
	ClipboardSurfaceReviewHunk         ClipboardSurface = "hunk"
	ClipboardSurfaceReviewFileDiff     ClipboardSurface = "file diff"
	ClipboardSurfaceReviewDocument     ClipboardSurface = "document"
)

// ClipboardRequestID is a monotonically increasing identity assigned by the
// root as it admits a copy request. Late completions with an older ID are
// ignored so a stale loader cannot overwrite a newer request.
type ClipboardRequestID uint64

// ClipboardTarget names both the surface and the specific target identity so
// the root can render a useful "Copy requested: <label>" notice without
// pulling the payload into the message stream.
type ClipboardTarget struct {
	Surface ClipboardSurface
	// Label describes the specific target: a run ID, file path, "message",
	// "line 42", "hunk 3 in path/to/file", etc. Kept short.
	Label string
}

// ClipboardLoader produces the payload bytes for one request. The loader runs
// as a tea.Cmd (off the synchronous key handler) and captures every source
// identity it needs before it is invoked. A loader must never mutate model
// state; it should only read from data captured at dispatch time.
type ClipboardLoader func() ClipboardPayload

// ClipboardPayload carries a loader's outcome. Payload is the sanitized text
// that will be emitted through OSC52; Err records a reason to refuse the copy
// without touching the clipboard. Notes captures optional omission details
// (e.g. skipped malformed transcript records) surfaced in feedback.
type ClipboardPayload struct {
	Payload string
	Notes   string
	Err     error
}

// ClipboardRequest is queued from a focused surface. Loader is invoked exactly
// once by the root once the request is admitted.
type ClipboardRequest struct {
	Target ClipboardTarget
	Loader ClipboardLoader
}

// ClipboardResult carries the outcome of a request back to the root. Payload
// is the sanitized text scheduled for OSC52 emission when Err is nil.
type ClipboardResult struct {
	ID      ClipboardRequestID
	Target  ClipboardTarget
	Payload string
	Bytes   int
	Notes   string
	Err     error
}

// Errors returned by PrepareClipboardPayload. They are user-visible reasons; do
// not embed source text in wrapped errors — callers surface them in notices.
var (
	ErrClipboardEmpty       = errors.New("nothing to copy")
	ErrClipboardOversized   = errors.New("selection is too large to copy")
	ErrClipboardInvalidText = errors.New("selection is not valid UTF-8 text")
	ErrClipboardBinary      = errors.New("selection contains binary data")
	ErrClipboardUnavailable = errors.New("copy target is unavailable")
	ErrClipboardBusy        = errors.New("another copy is in progress")
)

// PrepareClipboardPayload returns the sanitized final payload for OSC52
// emission, or an error explaining why no clipboard write should occur. The
// contract:
//
//   - Reject empty raw input (nothing to copy). A file placeholder is empty.
//   - Reject invalid UTF-8. Reject any NUL byte anywhere in the content.
//   - Strip ANSI escape sequences and other unsafe C0 control bytes; retain
//     the tab (0x09), LF (0x0A), and CR (0x0D) so source line endings and
//     indentation survive.
//   - Reject the sanitized payload if it exceeds ClipboardMaxPayloadBytes.
//     A payload exactly equal to the limit is accepted.
//   - Reject a payload that sanitized to an empty string, so sanitization
//     alone can never turn a copy into a clipboard-clearing write.
func PrepareClipboardPayload(raw string) (string, error) {
	if raw == "" {
		return "", ErrClipboardEmpty
	}
	if !utf8.ValidString(raw) {
		return "", ErrClipboardInvalidText
	}
	if strings.IndexByte(raw, 0x00) >= 0 {
		return "", ErrClipboardBinary
	}
	sanitized := sanitizeClipboardText(raw)
	if sanitized == "" {
		return "", ErrClipboardEmpty
	}
	if len(sanitized) > ClipboardMaxPayloadBytes {
		return "", ErrClipboardOversized
	}
	return sanitized, nil
}

// sanitizeClipboardText removes ANSI CSI/OSC/simple escape sequences and
// unsafe C0/C1 control characters. It preserves tab, LF, and CR because the
// spec requires source line endings and indentation to survive. This function
// is intentionally strict: pasting untrusted content into another terminal
// must not be able to inject cursor motions or hyperlinks.
func sanitizeClipboardText(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	i := 0
	for i < len(raw) {
		r, size := utf8.DecodeRuneInString(raw[i:])
		switch {
		case r == 0x1B: // ESC — skip the whole sequence
			skipped := skipEscapeSequence(raw, i)
			if skipped > 0 {
				i += skipped
				continue
			}
			i += size
			continue
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20:
			// Drop other C0 controls (BEL, BS, VT, FF, SO/SI, etc.)
		case r == 0x7F:
			// Drop DEL.
		case r >= 0x80 && r <= 0x9F:
			// Drop C1 controls.
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// skipEscapeSequence returns the number of bytes to skip past an ANSI escape
// sequence that begins at raw[i] (raw[i] == 0x1B). Supports CSI (ESC [ … final),
// OSC (ESC ] … BEL or ST), and simple two-byte sequences (ESC + one final).
func skipEscapeSequence(raw string, i int) int {
	if i+1 >= len(raw) {
		return 1
	}
	switch raw[i+1] {
	case '[':
		j := i + 2
		for j < len(raw) {
			c := raw[j]
			if c >= 0x40 && c <= 0x7E {
				return j - i + 1
			}
			j++
		}
		return len(raw) - i
	case ']':
		j := i + 2
		for j < len(raw) {
			c := raw[j]
			if c == 0x07 {
				return j - i + 1
			}
			if c == 0x1B && j+1 < len(raw) && raw[j+1] == '\\' {
				return j - i + 2
			}
			j++
		}
		return len(raw) - i
	default:
		return 2
	}
}

// FormatClipboardNotice returns the standardized "Copy requested" feedback
// string, per spec: identifies target and payload byte count and never claims
// verified terminal delivery. When notes is non-empty, it is appended after
// the byte count as a parenthetical.
func FormatClipboardNotice(target ClipboardTarget, payloadBytes int, notes string) string {
	label := string(target.Surface)
	if target.Label != "" {
		label = label + ": " + target.Label
	}
	msg := fmt.Sprintf("Copy requested: %s (%d bytes)", label, payloadBytes)
	if notes != "" {
		msg += " · " + notes
	}
	return msg
}

// FormatClipboardError returns the standardized rejection notice. It names the
// target and the reason without dumping source content.
func FormatClipboardError(target ClipboardTarget, err error) string {
	label := string(target.Surface)
	if target.Label != "" {
		label = label + ": " + target.Label
	}
	return fmt.Sprintf("Copy skipped (%s): %s", label, err.Error())
}

// ClipboardCommand is the injectable OSC52 emission seam. Tests replace it
// with a fake to assert that the sanitized payload reached the seam exactly
// once with the expected bytes; production code uses tea.SetClipboard.
type ClipboardCommand func(payload string) tea.Cmd

// DefaultClipboardCommand is tea.SetClipboard. Callers may override it in
// tests; the root reads whatever is set here at dispatch time.
var DefaultClipboardCommand ClipboardCommand = tea.SetClipboard
