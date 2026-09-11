package monitor

import (
	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// DiagnosticsRenderer produces a pre-formatted, secret-free dump of the
// process-wide notification diagnostic ring (spec 23-spec-run-notifications
// FR-17). The Monitor treats the returned text as opaque and never parses
// it. Implementations must never block and must never carry raw destination
// URLs, bearer tokens, response bodies, or error strings.
type DiagnosticsRenderer interface {
	RenderDiagnostics() string
}

// WithDiagnostics installs the process-wide notification diagnostics
// renderer the Monitor's "Notification diagnostics" command surfaces. A nil
// renderer is accepted (tests, persistence-off) and renders a fixed static
// line instead of stub-formatted text.
func (m Model) WithDiagnostics(r DiagnosticsRenderer) Model {
	m.diagnostics = r
	return m
}

// diagnosticsBody returns the current diagnostics snapshot, or a fixed
// friendly message when the renderer is nil or empty.
func (m Model) diagnosticsBody() string {
	if m.diagnostics == nil {
		return "Notification diagnostics are not wired for this process."
	}
	text := m.diagnostics.RenderDiagnostics()
	if text == "" {
		return "No notification diagnostics recorded in this session."
	}
	return text
}

// toggleNotificationDiagnostics opens or closes the diagnostics overlay. It
// never steals Gate focus: the overlay composites over whatever is on
// screen, and closing it restores the prior focus/queue state untouched
// because no other model field is touched.
func (m Model) toggleNotificationDiagnostics() Model {
	m.showDiagnostics = !m.showDiagnostics
	return m
}

// diagnosticsOverlay composites the bounded, themed diagnostics dump over
// base using the same centered-box Compositor technique as helpOverlay.
func (m Model) diagnosticsOverlay(base string) string {
	boxW := m.width * 70 / 100
	if boxW < 40 {
		boxW = 40
	}
	if boxW > m.width-2 {
		boxW = max(m.width-2, 1)
	}
	boxH := m.height * 70 / 100
	if boxH < 8 {
		boxH = 8
	}
	if boxH > m.height-2 {
		boxH = max(m.height-2, 1)
	}

	var body string
	body += shared.Theme.Help.Title.Render("Notification diagnostics") + "\n\n"
	body += m.diagnosticsBody()
	body += "\n\n" + shared.Theme.Help.Desc.Render("ctrl+g/esc close")

	box := shared.Theme.Help.Box.Width(boxW).Height(boxH).Render(fitBlock(body, boxW-2, boxH-2))
	x := (m.width - lipgloss.Width(box)) / 2
	y := (m.height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(m.width, m.height).Compose(comp).Render()
}
