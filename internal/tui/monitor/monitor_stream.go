package monitor

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// Live scrollback is independent of the persisted transcript's capture limits.
// Keep a contiguous tail, never splice the start and end of a response together.
const liveScrollbackBytes = 1024 * 1024

type liveTranscriptBuffer struct {
	text     strings.Builder
	dropped  bool
	width    int
	rendered string
}

func (b *liveTranscriptBuffer) append(delta string) {
	if delta == "" {
		return
	}
	b.rendered = ""
	b.width = 0
	if len(delta) >= liveScrollbackBytes {
		b.dropped = b.dropped || b.text.Len()+len(delta) > liveScrollbackBytes
		b.text.Reset()
		delta = delta[len(delta)-liveScrollbackBytes:]
		if b.dropped {
			delta = trimStreamContinuation(delta)
		}
	} else if excess := b.text.Len() + len(delta) - liveScrollbackBytes; excess > 0 {
		tail := trimStreamContinuation(b.text.String()[excess:])
		b.text.Reset()
		b.text.Grow(len(tail) + len(delta))
		b.text.WriteString(tail)
		b.dropped = true
	}
	b.text.WriteString(delta)
}

// A byte window may begin inside a UTF-8 rune. Inspect only the seam, not the
// whole buffer; malformed producer text is normalized when displayed.
func trimStreamContinuation(s string) string {
	for len(s) > 0 && s[0]&0xc0 == 0x80 {
		s = s[1:]
	}
	return s
}

func (b *liveTranscriptBuffer) render(width int) string {
	width = max(width, 1)
	if b.width == width {
		return b.rendered
	}
	text := b.text.String()
	// A transport chunk can end partway through a rune. Wait for its remaining
	// bytes instead of flashing a replacement character on screen.
	for start := max(0, len(text)-utf8.UTFMax+1); start < len(text); start++ {
		if utf8.RuneStart(text[start]) && !utf8.FullRuneInString(text[start:]) {
			text = text[:start]
			break
		}
	}
	text = shared.DisplayLiteralText(text)
	if b.dropped {
		text = "Earlier live output left scrollback.\n" + text
	}
	b.rendered = ansi.Hardwrap(text, width, true)
	b.width = width
	return b.rendered
}

// Preserve the selected preview before finalization or scrollback eviction.
// A paused reader keeps the same content until following or changing pages.
func (m *Model) preservePausedLive(stepID string) {
	if m.chatAutoScroll || stepID != m.chatStep || m.chatPausedLive != nil {
		return
	}
	if buf := m.liveTranscriptBuffer(); buf != nil && buf.text.Len() > 0 {
		m.chatPausedLive = &liveTranscriptBuffer{dropped: buf.dropped}
		m.chatPausedLive.append(buf.text.String())
	}
}
