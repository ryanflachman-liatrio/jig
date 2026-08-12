// Package tui implements jig's Bubble Tea program. It opens on a workflow
// picker that scans .agents/jig for workflow files; selecting one shows a
// read-only view of its steps and validation status. Pressing r in the detail
// view starts a dry run using the FakeExecutor and opens the run monitor.
package tui

import (
	"context"

	keybind "charm.land/bubbles/v2/key"
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

	// showHelp toggles the global "?" modal overlay (see help.go). It is owned by
	// the root so the same chord works on every screen; while set, the root
	// composites the overlay over the active screen and swallows all other keys.
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

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if keybind.Matches(msg, shared.KeyQuit) {
			return m, tea.Quit
		}
		// Help is a global modal owned by the root. While open it swallows every
		// key except its own toggle and esc, so nothing fires on the screen behind
		// it. When closed, "?" opens it — unless the active screen is capturing free
		// text, where "?" is a literal character.
		if m.showHelp {
			if keybind.Matches(msg, shared.KeyHelp) || msg.String() == "esc" {
				m.showHelp = false
			}
			return m, nil
		}
		if keybind.Matches(msg, shared.KeyHelp) && !m.activeProvider().capturesText() {
			m.showHelp = true
			return m, nil
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
	case monitor.EngineEventMsg:
		var rc, mc tea.Cmd
		m.runs, rc = m.runs.Update(msg)
		m.monitor, mc = m.monitor.Update(msg)
		var rearm tea.Cmd
		if msg.IsLive {
			rearm = waitForLiveEventCmd(m.liveEvents)
		} else {
			rearm = waitForCtrlEventCmd(m.ctrlEvents)
		}
		return m, tea.Batch(rearm, rc, mc)

	// The monitor's live-clock tick is routed unconditionally (like engine events)
	// so the loop keeps advancing even while the user is on another screen; the
	// monitor stops re-arming it once no step is running.
	case monitor.TickMsg:
		var mc tea.Cmd
		m.monitor, mc = m.monitor.Update(msg)
		return m, mc

	// ── navigation ────────────────────────────────────────────────────────
	case showDetailMsg:
		m.detail = newDetailModel(msg.path)
		var sizeCmd tea.Cmd
		m.detail, sizeCmd = m.detail.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenDetail
		return m, tea.Batch(sizeCmd, m.detail.Init())

	case backToSelectorMsg:
		// Only detail emits this; selector has no back action.
		m.active = screenSelector
		return m, nil

	case runs.BackMsg:
		// Go back to whatever workflow's detail we came from; if none, selector.
		if m.detail.loaded {
			m.active = screenDetail
		} else {
			m.active = screenSelector
		}
		return m, nil

	case monitor.ShowRunsMsg:
		m.active = screenRuns
		return m, nil

	case runsHydratedMsg:
		m.runs = m.runs.Hydrate(msg.runs)
		return m, nil

	case runs.ShowMonitorMsg:
		// Preserve monitor state when returning to the same run — events that
		// arrived while on other screens are already reflected, and we avoid
		// an unnecessary Snapshot() call.
		if m.monitor.RunID != msg.RunID {
			m.monitor = monitor.New(msg.RunID)
			// RunDir lets the monitor read per-step transcripts from disk. Set it
			// before WithSnapshot so it preserves it.
			m.monitor.RunDir = m.manager.RunDir(msg.RunID)
			// Seed with a snapshot so already-completed or in-progress steps
			// show up immediately. Snapshot() is safe for completed runs. A run
			// from an earlier session has no handle, so fall back to replaying its
			// journal from disk — the same events a Snapshot would carry.
			if run, ok := m.handles[msg.RunID]; ok {
				snap := run.Snapshot()
				m.monitor = m.monitor.WithSnapshot(snap)
			} else if evs, err := engine.ReplayJournal(m.monitor.RunDir); err == nil && len(evs) > 0 {
				m.monitor = m.monitor.WithJournal(evs)
			}
		}
		m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.active = screenMonitor
		// A run seeded from a snapshot/journal may already have a step running (or the
		// prior frame loop fell silent while off-screen); restart the live clock.
		return m, m.monitor.EnsureFrame()

	case monitor.ReviewVerdictMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Resolve(msg.StepID, msg.Verdict)
		}
		return m, nil

	case monitor.ReviewMessageMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Message(msg.StepID, msg.Text)
		}
		return m, nil

	case monitor.AgentInputMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.SendInput(msg.StepID, msg.Text)
		}
		return m, nil

	case monitor.AgentQuestionResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.AnswerQuestion(msg.StepID, msg.ToolUseID, msg.Answer)
		}
		return m, nil

	case monitor.RecoverResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Recover(msg.StepID, msg.Action, msg.Text)
		}
		return m, nil

	case monitor.ResolveIntegrationResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.ResolveIntegration(msg.StepID, msg.Abort)
		}
		return m, nil

	case monitor.FinalMergeResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.FinalMerge(msg.Approve)
		}
		return m, nil

	case monitor.StopStepMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Stop(msg.StepID)
		}
		return m, nil

	case monitor.ResumeStepMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Resume(msg.StepID, msg.Message)
		}
		return m, nil

	case monitor.RequestResetMsg:
		run, ok := m.handles[msg.RunID]
		if !ok {
			return m, nil
		}
		closure := run.ClosureOf(msg.StepID)
		if len(closure) <= 1 {
			// Linear tip: reset immediately, no confirmation needed.
			run.Reset(msg.StepID)
			return m, nil
		}
		// Mid-graph reset: ask the monitor to show the confirmation gate entry.
		var monCmd tea.Cmd
		m.monitor, monCmd = m.monitor.Update(monitor.ShowResetConfirmMsg{
			RunID:   msg.RunID,
			StepID:  msg.StepID,
			Closure: closure,
		})
		return m, monCmd

	case monitor.ResetStepMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.Reset(msg.StepID)
		}
		return m, nil

	case monitor.UserInputResponseMsg:
		if run, ok := m.handles[msg.RunID]; ok {
			run.ProvideUserInput(msg.StepID, msg.As, msg.Text)
		}
		return m, nil

	case startRunMsg:
		return m.startRun(msg.wf)

	case runs.StartRunMsg:
		return m.startRun(msg.Wf)
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

func (m rootModel) startRun(wf *workflow.Workflow) (tea.Model, tea.Cmd) {
	if wf == nil {
		return m, nil
	}
	run, err := m.manager.Start(wf)
	if err != nil {
		return m, nil
	}
	m.handles[run.ID] = run
	m.runs = m.runs.WithWorkflow(wf)
	// Navigate straight to the monitor so prompts and review gates are visible immediately.
	m.monitor = monitor.New(run.ID)
	m.monitor.RunDir = m.manager.RunDir(run.ID)
	m.monitor, _ = m.monitor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m.active = screenMonitor
	return m, nil
}

// hydrateRunsCmd reads the runs persisted on disk and replays each journal into
// its event stream, off the UI goroutine, so the runs list can show runs from
// earlier sessions at startup. It emits one runsHydratedMsg; runs whose journal
// is missing or undecodable are simply omitted. When persistence is off, the run
// list is empty and the message carries nothing.
func hydrateRunsCmd(mgr *engine.Manager) tea.Cmd {
	return func() tea.Msg {
		ids, err := mgr.PersistedRuns()
		if err != nil || len(ids) == 0 {
			return runsHydratedMsg{}
		}
		groups := make([][]engine.Event, 0, len(ids))
		for _, id := range ids {
			evs, err := engine.ReplayJournal(mgr.RunDir(id))
			if err != nil || len(evs) == 0 {
				continue
			}
			groups = append(groups, evs)
		}
		return runsHydratedMsg{runs: groups}
	}
}

// waitForLiveEventCmd drains one event from the live (liveness-signal) channel.
// The root re-arms it after each delivery, keeping a permanent drain loop running.
func waitForLiveEventCmd(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return monitor.EngineEventMsg{Event: e, IsLive: true}
	}
}

// waitForCtrlEventCmd drains one event from the ctrl (critical-control) channel.
// The root re-arms it after each delivery, keeping a permanent drain loop running.
func waitForCtrlEventCmd(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return monitor.EngineEventMsg{Event: e, IsLive: false}
	}
}

