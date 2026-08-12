// Package tui implements jig's Bubble Tea program. It opens on a workflow
// picker that scans .agents/jig for workflow files; selecting one shows a
// read-only view of its steps and validation status. Pressing r in the detail
// view starts a dry run using the FakeExecutor and opens the run monitor.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/tui/monitor"
	"jig/internal/tui/runs"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

// screen identifies which sub-model is currently driving the UI.
type screen int

const (
	screenSelector screen = iota
	screenDetail
	screenRuns
	screenMonitor
)

// ── messages ─────────────────────────────────────────────────────────────────

// showDetailMsg asks the root to open the detail screen for a workflow file.
type showDetailMsg struct{ path string }

// backToSelectorMsg asks the root to return to the selector.
type backToSelectorMsg struct{}

// startRunMsg asks the root to start a new run for the given workflow.
type startRunMsg struct{ wf *workflow.Workflow }

// runsHydratedMsg carries runs recovered from disk at startup, one seq-ordered
// event slice per run (oldest first), for the runs list to fold in. It is
// produced once by hydrateRunsCmd so a fresh session shows runs from earlier
// sessions instead of an empty list.
type runsHydratedMsg struct{ runs [][]engine.Event }

// ── root model ───────────────────────────────────────────────────────────────

type rootModel struct {
	active   screen
	selector selectorModel
	detail   detailModel
	runs     runs.Model
	monitor  monitor.Model

	ctx        context.Context
	manager    *engine.Manager
	liveEvents <-chan engine.Event
	ctrlEvents <-chan engine.Event
	handles    map[string]*engine.Run // runID → Run handle, for Snapshot()

	// showHelp toggles the global "?" modal overlay. It is owned by the root so
	// the same chord works on every screen; while set, the root composites the
	// overlay over the active screen and swallows all other keys.
	showHelp bool

	width  int
	height int
}

// helpProvider is implemented by every screen model that contributes a help
// overlay. capturesText reports whether the screen is currently capturing free
// text (a list filter, a gate textarea), in which case "?" is a literal
// character and must not open the overlay.
type helpProvider interface {
	helpSections() []shared.HelpSection
	capturesText() bool
}

// monitorHelpBridge adapts monitor.Model to the local helpProvider interface.
type monitorHelpBridge struct{ m monitor.Model }

func (b monitorHelpBridge) helpSections() []shared.HelpSection { return b.m.HelpSections() }
func (b monitorHelpBridge) capturesText() bool                 { return b.m.CapturesText() }

// runsHelpBridge adapts runs.Model to the local helpProvider interface.
type runsHelpBridge struct{ m runs.Model }

func (b runsHelpBridge) helpSections() []shared.HelpSection { return b.m.HelpSections() }
func (b runsHelpBridge) capturesText() bool                 { return b.m.CapturesText() }

// activeProvider returns the help sections + text-capture state of the screen
// currently driving the UI.
func (m rootModel) activeProvider() helpProvider {
	switch m.active {
	case screenDetail:
		return m.detail
	case screenRuns:
		return runsHelpBridge{m.runs}
	case screenMonitor:
		return monitorHelpBridge{m.monitor}
	default:
		return m.selector
	}
}

// New returns jig's root TUI model. mgr is the engine manager; it must be
// non-nil. The theme is dark-only, so no terminal-background detection is needed.
func New(ctx context.Context, mgr *engine.Manager) tea.Model {
	live, ctrl := mgr.Subscribe()
	return rootModel{
		active:     screenSelector,
		selector:   newSelectorModel(),
		runs:       runs.NewModel(),
		ctx:        ctx,
		manager:    mgr,
		liveEvents: live,
		ctrlEvents: ctrl,
		handles:    make(map[string]*engine.Run),
	}
}

func (m rootModel) Init() tea.Cmd {
	return tea.Batch(
		discoverWorkflowsCmd(workflowsDir),
		hydrateRunsCmd(m.manager),
		waitForLiveEventCmd(m.liveEvents),
		waitForCtrlEventCmd(m.ctrlEvents),
	)
}

func (m rootModel) View() tea.View {
	var content string
	switch m.active {
	case screenDetail:
		content = m.detail.View()
	case screenRuns:
		content = m.runs.View()
	case screenMonitor:
		content = m.monitor.View()
	default:
		content = m.selector.View()
	}
	// The help overlay is a global modal: composite it over the active screen (via
	// a lipgloss Canvas) so the screen shows through around the box, and the same
	// "?" chord surfaces context-appropriate keys everywhere.
	if m.showHelp {
		content = shared.RenderHelpOverlay(content, m.width, m.height, m.activeProvider().helpSections())
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
