// Package tui implements jig's Bubble Tea program. It opens on a workflow
// picker that scans .agents/jig for workflow files; selecting one shows a
// read-only view of its steps and validation status. Pressing r in the detail
// view starts a dry run using the FakeExecutor and opens the run monitor.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"jig/internal/engine"
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

// showRunsMsg asks the root to switch to the runs list.
type showRunsMsg struct{}

// showMonitorMsg asks the root to open the monitor for a specific run.
type showMonitorMsg struct{ runID string }

// reviewVerdictMsg is emitted by the monitor when the user selects a verdict
// for a review step. The root delivers it to the run via Run.Resolve.
type reviewVerdictMsg struct {
	runID   string
	stepID  string
	verdict string
}

// userInputResponseMsg is emitted by the monitor when the user submits text
// for a from="user" input. The root delivers it via Run.ProvideUserInput.
type userInputResponseMsg struct {
	runID  string
	stepID string
	as     string
	text   string
}

// reviewMessageMsg is emitted by the monitor when the user submits a free-text
// message to the reviewed agent step. The root delivers it via Run.Message.
type reviewMessageMsg struct {
	runID  string
	stepID string
	text   string
}

// agentInputMsg is emitted by the monitor when the user submits a response to
// an agent step blocked by block_on. The root delivers it via Run.SendInput.
type agentInputMsg struct {
	runID  string
	stepID string
	text   string
}

// engineEventMsg wraps one engine.Event for delivery as a tea.Msg.
type engineEventMsg struct{ event engine.Event }

// ── root model ───────────────────────────────────────────────────────────────

type rootModel struct {
	active   screen
	selector selectorModel
	detail   detailModel
	runs     runsModel
	monitor  monitorModel

	ctx     context.Context
	dark    bool
	manager *engine.Manager
	events  <-chan engine.Event
	handles map[string]*engine.Run // runID → Run handle, for Snapshot()

	width  int
	height int
}

// New returns jig's root TUI model. mgr is the engine manager; it must be
// non-nil. darkBackground must be detected before tea.NewProgram takes over
// stdin (see cmd/jig/main.go).
func New(ctx context.Context, darkBackground bool, mgr *engine.Manager) tea.Model {
	return rootModel{
		active:   screenSelector,
		selector: newSelectorModel(),
		runs:     newRunsModel(),
		ctx:      ctx,
		dark:     darkBackground,
		manager:  mgr,
		events:   mgr.Subscribe(),
		handles:  make(map[string]*engine.Run),
	}
}

func (m rootModel) Init() tea.Cmd {
	return tea.Batch(
		discoverWorkflowsCmd(workflowsDir),
		waitForEngineEventCmd(m.events),
	)
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		var sc, dc, rc, mc tea.Cmd
		m.selector, sc = m.selector.Update(msg)
		if m.detail.ready {
			m.detail, dc = m.detail.Update(msg)
		}
		m.runs, rc = m.runs.Update(msg)
		m.monitor, mc = m.monitor.Update(msg)
		return m, tea.Batch(sc, dc, rc, mc)

	// ── engine events ─────────────────────────────────────────────────────
	case engineEventMsg:
		var rc, mc tea.Cmd
		m.runs, rc = m.runs.Update(msg)
		m.monitor, mc = m.monitor.Update(msg)
		return m, tea.Batch(waitForEngineEventCmd(m.events), rc, mc)

	// ── navigation ────────────────────────────────────────────────────────
	case showDetailMsg:
		m.detail = newDetailModel(msg.path)
		var sizeCmd tea.Cmd
		m.detail, sizeCmd = m.detail.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenDetail
		return m, tea.Batch(sizeCmd, m.detail.Init())

	case backToSelectorMsg:
		switch m.active {
		case screenDetail:
			m.active = screenSelector
		case screenRuns:
			// Go back to whatever workflow's detail we came from; if none, selector.
			if m.detail.loaded {
				m.active = screenDetail
			} else {
				m.active = screenSelector
			}
		default:
			m.active = screenSelector
		}
		return m, nil

	case showRunsMsg:
		m.active = screenRuns
		return m, nil

	case showMonitorMsg:
		// Preserve monitor state when returning to the same run — events that
		// arrived while on other screens are already reflected, and we avoid
		// an unnecessary Snapshot() call.
		if m.monitor.runID != msg.runID {
			m.monitor = newMonitorModel(msg.runID)
			// runDir/dark let the monitor read per-step transcripts from disk and
			// render markdown with the correct terminal style (Phase 5). Set them
			// before withSnapshot so it preserves them.
			m.monitor.runDir = m.manager.RunDir(msg.runID)
			m.monitor.dark = m.dark
			// Seed with a snapshot so already-completed or in-progress steps
			// show up immediately. Snapshot() is safe for completed runs.
			if run, ok := m.handles[msg.runID]; ok {
				snap := run.Snapshot()
				m.monitor = m.monitor.withSnapshot(snap)
			}
		}
		m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenMonitor
		return m, nil

	case reviewVerdictMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Resolve(msg.stepID, msg.verdict)
		}
		return m, nil

	case reviewMessageMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.Message(msg.stepID, msg.text)
		}
		return m, nil

	case agentInputMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.SendInput(msg.stepID, msg.text)
		}
		return m, nil

	case userInputResponseMsg:
		if run, ok := m.handles[msg.runID]; ok {
			run.ProvideUserInput(msg.stepID, msg.as, msg.text)
		}
		return m, nil

	case startRunMsg:
		if msg.wf == nil {
			return m, nil
		}
		run, err := m.manager.Start(msg.wf)
		if err != nil {
			return m, nil
		}
		m.handles[run.ID] = run
		m.runs = m.runs.withWorkflow(msg.wf)
		// Navigate straight to the monitor so prompts and review gates are visible immediately.
		m.monitor = newMonitorModel(run.ID)
		m.monitor.runDir = m.manager.RunDir(run.ID)
		m.monitor.dark = m.dark
		m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenMonitor
		return m, nil
	}

	// All other messages go to the active screen.
	var cmd tea.Cmd
	switch m.active {
	case screenSelector:
		m.selector, cmd = m.selector.Update(msg)
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	case screenRuns:
		m.runs, cmd = m.runs.Update(msg)
	case screenMonitor:
		m.monitor, cmd = m.monitor.Update(msg)
	}
	return m, cmd
}

func (m rootModel) View() string {
	switch m.active {
	case screenDetail:
		return m.detail.View()
	case screenRuns:
		return m.runs.View()
	case screenMonitor:
		return m.monitor.View()
	default:
		return m.selector.View()
	}
}

// waitForEngineEventCmd returns a tea.Cmd that blocks until one event arrives
// on ch, then wraps it as an engineEventMsg. The root re-arms it after each
// delivery, creating a permanent event-drain loop.
func waitForEngineEventCmd(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil // channel closed; stop draining
		}
		return engineEventMsg{event: e}
	}
}
