// Package tui implements jig's Bubble Tea program. It opens on Home — workflows
// and runs side-by-side — and enters the run Monitor when a run is started or
// opened. Detail/chart is a Home overlay, not a root screen.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/tui/detail"
	"jig/internal/tui/monitor"
	"jig/internal/tui/palette"
	"jig/internal/tui/runs"
	"jig/internal/tui/selector"
	"jig/internal/tui/shared"
)

// screen identifies which top-level surface is currently driving the UI.
// Home owns workflows+runs (and an optional Detail overlay). Monitor is the
// only other root surface after Phase 0.
type screen int

const (
	screenHome screen = iota
	screenMonitor
)

// ── messages ─────────────────────────────────────────────────────────────────

// runsHydratedMsg carries runs recovered from disk at startup, one seq-ordered
// event slice per run (oldest first), for the runs list to fold in. It is
// produced once by hydrateRunsCmd so a fresh session shows runs from earlier
// sessions instead of an empty list.
type runsHydratedMsg struct{ runs [][]engine.Event }

type runResumedMsg struct {
	runID  string
	run    *engine.Run
	events []engine.Event
	err    error
}

// ── root model ───────────────────────────────────────────────────────────────

type rootModel struct {
	active   screen
	selector selector.Model
	detail   detail.Model
	runs     runs.Model
	monitor  monitor.Model

	// Home dual-pane state. showDetailOverlay layers Detail/chart over Home
	// without demoting Home as the leave-Monitor destination.
	homeFocus         homePane
	showDetailOverlay bool
	homeSelectedPath  string

	ctx        context.Context
	manager    *engine.Manager
	liveEvents <-chan engine.Event
	ctrlEvents <-chan engine.Event
	handles    map[string]*engine.Run // runID → Run handle, for Snapshot()

	// Root ownership lets the modal swallow navigation keys before they reach the
	// screen underneath; F1 remains available while that screen captures text.
	showHelp   bool
	helpOffset int

	// Command palette (ctrl+k) is root-owned like help so every screen shares
	// one overlay and CapturesText gating (Phase 1.3).
	palette palette.Model

	// confirmDelete and pendingDeleteID drive the delete-confirmation overlay.
	// Root owns this (mirroring showHelp) so it can cancel live runs directly
	// and swallow keys in handleGlobalKey while the modal is open.
	confirmDelete   bool
	pendingDeleteID string

	// pendingDeletions tracks live runIDs whose directories should be removed
	// once the engine emits RunFinished (Cancel is async — we can't remove the
	// directory until the scheduler goroutine has stopped writing to it).
	pendingDeletions map[string]bool

	// leaveConfirm asks before abandoning a dirty review compose buffer when
	// the operator presses a leave-Monitor chord (0.4 / A6).
	leaveConfirm bool

	// clipboard is root-owned copy-request state. The root admits a single
	// request at a time and matches completions by id, so a stale loader
	// finishing after navigation cannot overwrite a newer request.
	clipboard clipboardState

	// diagnostics renders a text dump of the process-wide notification
	// diagnostic ring. It is nil when notifications are not wired (tests) —
	// the overlay is inert in that case.
	diagnostics DiagnosticsRenderer
	// showDiagnostics is true while the notification-diagnostics overlay
	// is composited over the active screen.
	showDiagnostics bool

	width  int
	height int
}

// DiagnosticsRenderer produces a pre-formatted, secret-free dump of the
// process-wide notification diagnostic ring. The TUI treats the returned
// text as opaque and never parses it. Implementations should:
//
//   - never block (return the current snapshot synchronously),
//   - render a fixed short summary (a single "no diagnostics yet" line is fine
//     for the empty case), and
//   - never carry raw response bodies, URLs, or secret values.
//
// The renderer is optional: passing nil to WithDiagnostics leaves the overlay
// wired but reporting a static "not available" line. This matches the
// persistence-off/tests case where no process runtime exists.
type DiagnosticsRenderer interface {
	RenderDiagnostics() string
}

// DiagnosticsRendererFunc adapts a bare function to DiagnosticsRenderer.
type DiagnosticsRendererFunc func() string

// RenderDiagnostics implements DiagnosticsRenderer.
func (f DiagnosticsRendererFunc) RenderDiagnostics() string {
	if f == nil {
		return ""
	}
	return f()
}

// diagnosticsBody returns the current diagnostics snapshot, or a fixed
// friendly message when the renderer is nil or empty. The overlay never
// blocks the model — the renderer is expected to return synchronously.
func (m rootModel) diagnosticsBody() string {
	if m.diagnostics == nil {
		return "Notification diagnostics are not wired for this process."
	}
	text := m.diagnostics.RenderDiagnostics()
	if text == "" {
		return "No notification diagnostics recorded in this session."
	}
	return text
}

// helpProvider is implemented by every screen model that contributes a help
// overlay. capturesText reports whether the screen is currently capturing free
// text (a list filter, a gate textarea), in which case "?" is a literal
// character and must not open the overlay. paletteSections may expose a fuller
// catalog than helpSections (Monitor simple mode).
type helpProvider interface {
	helpSections() []shared.HelpSection
	paletteSections() []shared.HelpSection
	capturesText() bool
}

type homeHelpBridge struct{ m rootModel }

func (b homeHelpBridge) helpSections() []shared.HelpSection    { return b.m.homeHelpSections() }
func (b homeHelpBridge) paletteSections() []shared.HelpSection { return b.m.homeHelpSections() }
func (b homeHelpBridge) capturesText() bool                    { return b.m.homeCapturesText() }

// monitorHelpBridge adapts monitor.Model to the local helpProvider interface.
type monitorHelpBridge struct{ m monitor.Model }

func (b monitorHelpBridge) helpSections() []shared.HelpSection    { return b.m.HelpSections() }
func (b monitorHelpBridge) paletteSections() []shared.HelpSection { return b.m.PaletteSections() }
func (b monitorHelpBridge) capturesText() bool                    { return b.m.CapturesText() }

// activeProvider returns the help sections + text-capture state of the screen
// currently driving the UI.
func (m rootModel) activeProvider() helpProvider {
	if m.active == screenMonitor {
		return monitorHelpBridge{m.monitor}
	}
	return homeHelpBridge{m}
}

// Option configures a rootModel at construction. Options exist so callers
// can add process-wide bridges (notification diagnostics, in future the
// clipboard / IPC hooks) without every test having to construct them.
type Option func(*rootModel)

// WithDiagnostics installs a process-wide notification diagnostics renderer
// that surfaces through the root notification-diagnostics overlay. A nil
// renderer is accepted and treated as "not wired" — the overlay renders a
// fixed static line instead of stub-formatted text. This matches the tests
// and headless entry paths, which construct the TUI without a shared runtime.
func WithDiagnostics(r DiagnosticsRenderer) Option {
	return func(m *rootModel) { m.diagnostics = r }
}

// New returns jig's root TUI model. mgr is the engine manager; it must be
// non-nil. The theme is dark-only, so no terminal-background detection is needed.
func New(ctx context.Context, mgr *engine.Manager, opts ...Option) tea.Model {
	live, ctrl := mgr.Subscribe()
	m := rootModel{
		active:           screenHome,
		selector:         selector.New(),
		runs:             runs.NewModel(),
		homeFocus:        homeWorkflows,
		ctx:              ctx,
		manager:          mgr,
		liveEvents:       live,
		ctrlEvents:       ctrl,
		handles:          make(map[string]*engine.Run),
		pendingDeletions: make(map[string]bool),
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

func (m rootModel) Init() tea.Cmd {
	return tea.Batch(
		m.selector.Init(),
		hydrateRunsCmd(m.manager),
		waitForLiveEventCmd(m.liveEvents),
		waitForCtrlEventCmd(m.ctrlEvents),
	)
}

func (m rootModel) View() tea.View {
	var content string
	switch m.active {
	case screenMonitor:
		content = m.monitor.View()
	default:
		content = m.homeView()
	}
	if notice := m.clipboard.notice; notice != "" {
		content = renderClipboardNoticeOverlay(content, notice, m.width, m.height)
	}
	// The help overlay is a global modal: composite it over the active screen (via
	// a lipgloss Canvas) so the screen shows through around the box, and the same
	// "?" chord surfaces context-appropriate keys everywhere.
	if m.showHelp {
		content = shared.RenderHelpOverlay(content, m.width, m.height, m.activeProvider().helpSections(), m.helpOffset)
	}
	if m.palette.Open() {
		content = m.palette.View(content, m.width, m.height)
	}
	// The delete-confirm overlay is also root-owned so it can swallow all keys
	// and call run.Cancel() directly without a round-trip message.
	if m.confirmDelete {
		body := m.pendingDeleteID + "\n\nRunning steps will be cancelled.\nAll output will be permanently deleted."
		content = shared.RenderConfirmOverlay(content, "Delete run?", body, m.width, m.height)
	}
	if m.leaveConfirm {
		content = shared.RenderConfirmOverlay(content, "Discard unsaved comment?",
			"You have an unsaved review comment.\nLeave and discard it?", m.width, m.height)
	}
	if m.showDiagnostics {
		content = shared.RenderConfirmOverlay(content, "Notification diagnostics",
			m.diagnosticsBody()+"\n\nesc to close", m.width, m.height)
	}
	// v2 declares alt-screen and the full-screen background on the View itself
	// (the compositor paints BackgroundColor edge-to-edge, so nested styled
	// spans no longer punch holes in a screen-wide background — the reason the
	// Pepper canvas was blocked on v1). shared.Theme.Canvas is Charmtone Pepper.
	v := tea.NewView(content)
	v.AltScreen = true
	v.BackgroundColor = shared.Theme.Canvas
	return v
}
