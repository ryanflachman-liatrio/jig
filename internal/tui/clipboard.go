package tui

import (
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/tui/shared"
)

// clipboardState is the root-owned copy-request bookkeeping. Root admits a
// single request at a time, assigns a monotonically increasing ID, and matches
// completions by ID so a stale loader cannot overwrite a newer request.
type clipboardState struct {
	nextID  uint64
	pending shared.ClipboardRequestID
	busy    bool
	// notice is the last user-visible outcome ("Copy requested: …", "Copy
	// skipped: …"). Screens read it through the root and render it in an
	// existing themed notice/footer area; it is cleared on the next admission.
	notice string
}

// clipboardResultMsg is emitted by the request loader once it finishes. It is
// intentionally not exported: only the root produces and consumes it, and
// external screens should route work through a shared.ClipboardRequest.
type clipboardResultMsg struct{ shared.ClipboardResult }

// clipboardNoticeMsg is emitted internally to explicitly refresh the active
// screen after the root records a new notice (some screens paint the notice
// out of their existing footer, which is re-rendered on any Update).
type clipboardNoticeMsg struct{}

// clipboardTargeter is used by test callers who want to inspect the last
// notice recorded by the root.
type clipboardTargeter interface {
	ClipboardNotice() string
}

// ClipboardNotice returns the last recorded clipboard notice text. It is safe
// to call across screens; the empty string means no notice is currently shown.
func (m rootModel) ClipboardNotice() string { return m.clipboard.notice }

// clipboardBusy reports whether the root already has an in-flight copy request.
func (m rootModel) clipboardBusy() bool { return m.clipboard.busy }

// admitClipboard consumes one ClipboardRequest. If a request is already in
// flight the request is refused with a busy notice and no command runs; the
// caller must not queue the loader itself. On success a command is returned
// which runs the loader off the synchronous key handler.
func (m rootModel) admitClipboard(req shared.ClipboardRequest) (rootModel, tea.Cmd) {
	if req.Loader == nil {
		m.clipboard.notice = shared.FormatClipboardError(req.Target, shared.ErrClipboardUnavailable)
		return m, func() tea.Msg { return clipboardNoticeMsg{} }
	}
	if m.clipboard.busy {
		m.clipboard.notice = shared.FormatClipboardError(req.Target, shared.ErrClipboardBusy)
		return m, func() tea.Msg { return clipboardNoticeMsg{} }
	}
	m.clipboard.nextID++
	id := shared.ClipboardRequestID(m.clipboard.nextID)
	m.clipboard.pending = id
	m.clipboard.busy = true
	m.clipboard.notice = ""
	target := req.Target
	loader := req.Loader
	return m, func() tea.Msg {
		payload := loader()
		return clipboardResultMsg{ClipboardResult: shared.ClipboardResult{
			ID:      id,
			Target:  target,
			Payload: payload.Payload,
			Notes:   payload.Notes,
			Err:     payload.Err,
		}}
	}
}

// completeClipboard applies sanitization, records the notice, and (on
// success) returns the OSC52 emission command produced by
// shared.DefaultClipboardCommand. A completion with a stale ID is dropped so
// only the newest request can win the busy slot.
func (m rootModel) completeClipboard(msg clipboardResultMsg) (rootModel, tea.Cmd) {
	if msg.ID != m.clipboard.pending {
		return m, nil
	}
	// Reset busy first: even a rejected payload must release the slot so the
	// next request can be admitted immediately.
	m.clipboard.busy = false
	m.clipboard.pending = 0
	if msg.Err != nil {
		m.clipboard.notice = shared.FormatClipboardError(msg.Target, msg.Err)
		return m, func() tea.Msg { return clipboardNoticeMsg{} }
	}
	sanitized, err := shared.PrepareClipboardPayload(msg.Payload)
	if err != nil {
		m.clipboard.notice = shared.FormatClipboardError(msg.Target, err)
		return m, func() tea.Msg { return clipboardNoticeMsg{} }
	}
	m.clipboard.notice = shared.FormatClipboardNotice(msg.Target, len(sanitized), msg.Notes)
	emit := shared.DefaultClipboardCommand
	notify := func() tea.Msg { return clipboardNoticeMsg{} }
	if emit == nil {
		return m, notify
	}
	return m, tea.Batch(emit(sanitized), notify)
}

// clipboardHandle is the small hook a screen model receives when it needs to
// build a ClipboardRequest. It is set on construction so tests can inject an
// isolated ID sequence without touching the atomic counter below.
type clipboardHandle struct {
	// next assigns a request-local sequence number. Not used by the current
	// screens (they hand the ClipboardRequest to the root which owns the real
	// ID), but retained here so downstream request-side bookkeeping (e.g.
	// canceling an in-flight loader) can be added without a package-level
	// singleton later.
	next atomic.Uint64
}

var _ clipboardTargeter = rootModel{}

// clipboardUnavailable is a tiny helper for screens: emit a notice message
// with the given target so the operator sees a rejection reason.
func clipboardUnavailable(target shared.ClipboardTarget, err error) tea.Cmd {
	if err == nil {
		err = shared.ErrClipboardUnavailable
	}
	return func() tea.Msg {
		return clipboardImmediateNoticeMsg{Target: target, Err: err}
	}
}

// clipboardImmediateNoticeMsg surfaces a rejection from a screen that decided
// no request should even be admitted (e.g. an empty runs list). It bypasses
// the loader path but still records the notice through the same root state so
// the operator gets consistent feedback.
type clipboardImmediateNoticeMsg struct {
	Target shared.ClipboardTarget
	Err    error
}

// applyImmediateNotice records a screen-initiated notice without touching the
// admission counters. Screens use this when they need to advertise that a
// requested action is unavailable in the current context.
func (m rootModel) applyImmediateNotice(msg clipboardImmediateNoticeMsg) (rootModel, tea.Cmd) {
	err := msg.Err
	if err == nil {
		err = shared.ErrClipboardUnavailable
	}
	m.clipboard.notice = shared.FormatClipboardError(msg.Target, err)
	return m, func() tea.Msg { return clipboardNoticeMsg{} }
}

// renderClipboardNoticeOverlay composites the clipboard notice as a single
// bottom-of-screen line over the active view. It never grows the vertical
// footprint: the notice replaces whatever pixels are at the last row.
func renderClipboardNoticeOverlay(base, notice string, width, height int) string {
	if notice == "" || width <= 0 || height <= 0 {
		return base
	}
	trimmed := ansi.Truncate(notice, max(width-2, 1), "…")
	line := shared.Theme.Marker.Render("  " + trimmed)
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(line).X(0).Y(height-1).Z(1),
	)
	return lipgloss.NewCanvas(width, height).Compose(comp).Render()
}
